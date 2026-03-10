package renderer

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/terminal"
)

// paneCacheEntry tracks the last-rendered state of a pane for skip detection.
type paneCacheEntry struct {
	lastRenderGen  uint64
	lastViewOffset int
	lastCursorRow  int
	lastCursorCol  int
	hadURLHover    bool // true if URL hover was active on last draw
	lastProcName   string
	lastCustomName string
	lastRenaming   bool
	lastRenameText string
}

// blockSnap holds a point-in-time copy of block-related buffer state.
// Taken under RLock in DrawAll, used by drawBlocksSnap without holding any lock.
// Pattern: snapshot — decouple data collection from rendering to minimise lock duration.
type blockSnap struct {
	blocks    []terminal.BlockBoundary // completed blocks + active block appended if running
	rows      int
	sbLen     int
	viewOff   int
	cursorRow int
	buf       *terminal.ScreenBuffer // pointer kept for TextRange in the click handler
	paneRect  image.Rectangle
	headerH   int // pane header height offset — block Y coords must include this
}

// Renderer implements ebiten.Game's Draw() logic.
// It reads from the ScreenBuffer (read lock) and paints each cell.
type Renderer struct {
	font    *FontRenderer
	cfg     *config.Config
	padding int

	// offscreen is the render target for pane content, tab bar, and always-visible UI
	// strips (status bar, search bar, dividers). Modal overlays draw onto modalLayer instead.
	offscreen *ebiten.Image

	// blocksLayer is a separate image for command block decorations (borders, badges).
	// Cleared and redrawn each frame so hover state is always current without
	// requiring DrawPane to rerun for mouse-move-only updates.
	blocksLayer *ebiten.Image

	// modalLayer is cleared every frame and holds all modal/overlay drawing
	// (context menu, help overlay, confirm dialog, tab switcher, palette, file explorer).
	// Composited onto screen after blocksLayer so modals always appear above block decorations.
	modalLayer *ebiten.Image

	// overlayBg is a 1×1 image scaled to cover the screen for the help overlay backdrop.
	overlayBg *ebiten.Image

	cursorColor color.RGBA
	borderColor color.RGBA
	bellColor   color.RGBA

	// paneCache enables per-pane skip: unchanged panes are not redrawn.
	paneCache map[*pane.Pane]*paneCacheEntry

	// layoutDirty triggers a full offscreen clear on the next Draw.
	// Set on tab switch, pane split/close, resize, and zoom toggle.
	layoutDirty bool

	// BlocksEnabled is a runtime toggle for command block rendering.
	// Mirrors Game.blocksEnabled; set before each DrawAll call.
	BlocksEnabled bool

	// blockTintImg is a 1×1 image scaled to block rects for alpha-blended tints.
	blockTintImg *ebiten.Image

	// BlockHover tracks the block currently under the cursor for click handling.
	// Updated by drawBlocksSnap each frame; read by main.go in handleMouse.
	BlockHover BlockHoverState

	// ui holds the derived UI chrome colors (panels, menus, overlays).
	// Computed from cfg in NewRenderer so no draw function needs hardcoded colors.
	ui UIColors

	// HoveredURL is the URL currently under the cursor, if any.
	// Set by main.go before DrawAll; read by DrawPane to render hover underline.
	HoveredURL *terminal.URLMatch
}

// CopyTarget identifies which part of a block to copy.
type CopyTarget int

const (
	CopyNone   CopyTarget = iota
	CopyCmdText            // command line only
	CopyOutput             // output rows only
	CopyAll                // command + output
)

// BlockHoverState describes the block currently under the cursor.
type BlockHoverState struct {
	Active    bool
	Buf       *terminal.ScreenBuffer // the pane's buffer (for TextRange)
	AbsStart  int                    // AbsPromptRow of the hovered block
	AbsCmdRow int                    // AbsCmdRow of the hovered block
	AbsEnd    int                    // AbsEndRow of the hovered block
	CmdCol    int                    // column where user input starts (from OSC 133;B); -1 if unknown
	CopyTarget CopyTarget            // which copy button is under cursor
	CmdRect   image.Rectangle        // hit rect of "cmd" button
	OutRect   image.Rectangle        // hit rect of "out" button
	AllRect   image.Rectangle        // hit rect of "all" button
	blockIdx  int                    // index in buf.Blocks (-1 = activeBlock)
}

// NewRenderer creates a Renderer. Call SetSize after window dimensions are known.
func NewRenderer(font *FontRenderer, cfg *config.Config) *Renderer {
	return &Renderer{
		font:         font,
		cfg:          cfg,
		padding:      cfg.Window.Padding,
		cursorColor:  config.ParseHexColor(cfg.Colors.Cursor),
		borderColor:  config.ParseHexColor(cfg.Colors.Border),
		bellColor:    config.ParseHexColor(cfg.Bell.Color),
		paneCache:    make(map[*pane.Pane]*paneCacheEntry),
		layoutDirty:  true, // force full clear on first frame
		blockTintImg: ebiten.NewImage(1, 1),
		ui:           deriveUIColors(cfg),
	}
}

// ReloadColors updates the renderer's color state from a new config.
// Re-parses cursor/border colors, re-derives UI colors, clears pane cache,
// and marks the layout dirty so everything redraws.
func (r *Renderer) ReloadColors(cfg *config.Config) {
	r.cfg = cfg
	r.cursorColor = config.ParseHexColor(cfg.Colors.Cursor)
	r.borderColor = config.ParseHexColor(cfg.Colors.Border)
	r.bellColor = config.ParseHexColor(cfg.Bell.Color)
	r.ui = deriveUIColors(cfg)
	r.paneCache = make(map[*pane.Pane]*paneCacheEntry)
	r.layoutDirty = true
}

// SetFont replaces the renderer's font. Call SetSize after to recompute
// cell dimensions throughout the layout.
func (r *Renderer) SetFont(font *FontRenderer) {
	r.font = font
}

// SetLayoutDirty marks the renderer for a full offscreen clear on the next Draw.
// Call whenever the pane layout or tab changes.
func (r *Renderer) SetLayoutDirty() { r.layoutDirty = true }

// ClearPaneCache forces all panes to be fully redrawn on the next Draw.
// Call when an overlay dismisses so pane pixels underneath are restored.
func (r *Renderer) ClearPaneCache() {
	r.paneCache = make(map[*pane.Pane]*paneCacheEntry)
}

// Offscreen returns the last rendered image. Used by Draw() to re-blit
// without a full redraw when the frame is not dirty.
func (r *Renderer) Offscreen() *ebiten.Image { return r.offscreen }

// SetSize (re)allocates the offscreen and blocks layer images when the window resizes.
func (r *Renderer) SetSize(w, h int) {
	if r.offscreen != nil {
		ow, oh := r.offscreen.Bounds().Dx(), r.offscreen.Bounds().Dy()
		if ow == w && oh == h {
			return
		}
		r.offscreen.Deallocate()
		r.blocksLayer.Deallocate()
		r.modalLayer.Deallocate()
	}
	r.offscreen = ebiten.NewImage(w, h)
	r.blocksLayer = ebiten.NewImage(w, h)
	r.modalLayer = ebiten.NewImage(w, h)
}

// TabBarHeight returns the physical pixel height of the tab bar.
func (r *Renderer) TabBarHeight() int {
	return r.font.CellH + 4
}

// StatusBarHeight returns the physical pixel height of the status bar, or 0 if disabled.
func (r *Renderer) StatusBarHeight() int {
	return StatusBarHeight(r.font, r.cfg)
}

// DrawAll renders the tab bar, all panes, status bar, and any active UI overlays onto screen.
func (r *Renderer) DrawAll(screen *ebiten.Image, tabs []*tab.Tab, activeTab int, focused *pane.Pane, zoomed bool, menu *MenuState, overlay *OverlayState, confirm *ConfirmState, search *SearchState, statusBar *StatusBarState, tabSwitcher *TabSwitcherState, palette *PaletteState, paletteEntries []PaletteEntry, fileExplorer *FileExplorerState, tabSearch *TabSearchState, stats *StatsState, tabHover *TabHoverState, dictation *DictationState, mdViewer *MarkdownViewerState, urlInput *URLInputState, hintMode bool) {
	if r.offscreen == nil {
		return
	}

	// Clear the modal layer every frame so overlay draws never accumulate across frames.
	r.modalLayer.Clear()

	// On layout changes (tab switch, split, resize, zoom) clear the entire
	// offscreen so stale pixels from the previous layout are gone.
	// Otherwise only paint the divider strips between panes; pane content
	// is handled per-pane below with skip logic.
	var layout *pane.LayoutNode
	if activeTab < len(tabs) {
		layout = tabs[activeTab].Layout
	}
	if r.layoutDirty {
		r.offscreen.Fill(r.borderColor)
		r.layoutDirty = false
	} else if layout != nil && !zoomed {
		r.drawDividers(layout)
	}

	r.drawTabBar(tabs, activeTab, hintMode)

	// Reset block hover each frame so stale state doesn't linger when the
	// cursor moves off a block or blocks are disabled.
	r.BlockHover = BlockHoverState{}

	var blockSnaps []*blockSnap

	if layout != nil {
		leaves := layout.Leaves()
		multiPane := len(leaves) > 1

		for i, leaf := range leaves {
			p := leaf.Pane
			isFocused := p == focused
			if zoomed && !isFocused {
				continue
			}

			p.Term.Buf.RLock()
			gen := p.Term.Buf.RenderGen()
			viewOff := p.Term.Buf.ViewOffset
			curRow := p.Term.Buf.CursorRow
			curCol := p.Term.Buf.CursorCol
			sbLen := p.Term.Buf.ScrollbackLen()

			// Snapshot block data while the lock is held — cheap copy of small structs.
			// drawBlocksSnap uses this after the lock is released, keeping the RLock
			// window as short as possible (covers DrawPane only).
			var snap *blockSnap
			if r.BlocksEnabled && !p.Term.Buf.IsAltActive() {
				snap = &blockSnap{
					blocks:    make([]terminal.BlockBoundary, len(p.Term.Buf.Blocks)),
					rows:      p.Term.Buf.Rows,
					sbLen:     sbLen,
					viewOff:   p.Term.Buf.ViewOffset,
					cursorRow: p.Term.Buf.CursorRow,
					buf:       p.Term.Buf,
					paneRect:  p.Rect,
					headerH:   p.HeaderH,
				}
				copy(snap.blocks, p.Term.Buf.Blocks)
				if ab := p.Term.Buf.ActiveBlock(); ab != nil {
					abCopy := *ab
					snap.blocks = append(snap.blocks, abCopy)
				}
			}

			cache := r.paneCache[p]
			if cache == nil {
				cache = &paneCacheEntry{}
				r.paneCache[p] = cache
			}

			// Cache check: only actual content changes (gen, cursor, viewOffset,
			// process name) trigger DrawPane + overlay redraw.
			hasURLHover := isFocused && r.HoveredURL != nil
			procName := p.ProcName
			unchanged := gen == cache.lastRenderGen &&
				viewOff == cache.lastViewOffset &&
				curRow == cache.lastCursorRow &&
				curCol == cache.lastCursorCol &&
				hasURLHover == cache.hadURLHover &&
				procName == cache.lastProcName &&
				p.CustomName == cache.lastCustomName &&
				p.Renaming == cache.lastRenaming &&
				p.RenameText == cache.lastRenameText

			if !unchanged {
				var paneSearch *SearchState
				if isFocused {
					paneSearch = search
				}
				r.DrawPane(p.Term.Buf, p.Term.Cursor, p.Rect, isFocused, isFocused && multiPane && !zoomed, paneSearch, p.HeaderH)

				// Pane overlays: name label (multi-pane only) and scroll indicator.
				label := p.CustomName
				if label == "" {
					label = procName
				}
				if label == "" {
					label = fmt.Sprintf("Pane %d", i+1)
				}
				if p.Renaming {
					label = p.RenameText + "_"
				}
				r.drawPaneOverlay(p.Rect, label, multiPane, viewOff, sbLen)

				cache.lastRenderGen = gen
				cache.lastViewOffset = viewOff
				cache.lastCursorRow = curRow
				cache.lastCursorCol = curCol
				cache.hadURLHover = hasURLHover
				cache.lastProcName = procName
				cache.lastCustomName = p.CustomName
				cache.lastRenaming = p.Renaming
				cache.lastRenameText = p.RenameText
			}
			p.Term.Buf.RUnlock()

			// Visual bell: draw a 2px border flash when BEL was received.
			if !p.BellUntil.IsZero() && time.Now().Before(p.BellUntil) {
				r.drawThickBorder(p.Rect, r.bellColor, 2)
			}

			if snap != nil {
				blockSnaps = append(blockSnaps, snap)
			}
		}
	}

	// Render block decorations onto the dedicated blocks layer.
	// The layer is cleared every frame so hover state is always current without
	// requiring DrawPane to rerun when only the cursor position changes.
	if r.BlocksEnabled && r.blocksLayer != nil {
		r.blocksLayer.Clear()
		for _, s := range blockSnaps {
			r.drawBlocksSnap(s)
		}
	}

	// Search bar drawn above pane content, below status bar (non-modal, stays in offscreen).
	r.drawSearchBar(search)

	// Stats overlay drawn above pane content (non-modal, stays in offscreen).
	r.drawStats(stats)

	// Status bar drawn last into offscreen so it always sits on top of pane content.
	r.drawStatusBar(statusBar)

	// All modal/overlay content draws onto modalLayer (cleared above) so it always
	// composites above blocksLayer in the final screen pass below.

	// Tab hover popover — drawn first onto modalLayer so it sits below other modals.
	// modalLayer is cleared every frame, preventing stale pixel accumulation.
	r.drawTabHoverPopover(tabHover)

	// Context menu drawn above terminal content.
	if menu != nil {
		r.drawContextMenu(menu)
	}

	// Help overlay — covers everything when open.
	if overlay != nil {
		r.drawOverlay(overlay)
	}

	// Markdown viewer overlay — same layer as help overlay.
	r.drawMarkdownViewer(mdViewer)

	// URL input overlay — drawn above markdown viewer.
	r.drawURLInput(urlInput)

	// Confirm dialog drawn above overlay.
	if confirm != nil {
		r.drawConfirm(confirm)
	}

	// Dictation overlay drawn above confirm dialog.
	r.drawDictation(dictation)

	// Tab switcher overlay drawn above everything when open.
	r.drawTabSwitcher(tabs, activeTab, tabSwitcher)

	// Tab search overlay (Cmd+J) drawn above tab switcher.
	r.drawTabSearch(tabs, activeTab, tabSearch)

	// Command palette drawn above everything else when open.
	if palette != nil && palette.Open {
		r.drawPalette(paletteEntries, palette)
	}

	// File explorer panel drawn on top of terminal content.
	if fileExplorer != nil && fileExplorer.Open {
		r.drawFileExplorer(fileExplorer)
	}

	// Final composite: offscreen → blocksLayer → modalLayer → screen.
	// This guarantees modals are always on top of block decorations.
	screen.DrawImage(r.offscreen, nil)
	if r.BlocksEnabled && r.blocksLayer != nil {
		screen.DrawImage(r.blocksLayer, nil)
	}
	screen.DrawImage(r.modalLayer, nil)
}

// drawTabBar renders the tab strip at the top of the offscreen buffer.
// When hintMode is true, tab number badges (1-9) are overlaid for discoverability.
func (r *Renderer) drawTabBar(tabs []*tab.Tab, activeTab int, hintMode bool) {
	tabBarH := r.TabBarHeight()
	physW := r.offscreen.Bounds().Dx()
	numTabs := len(tabs)
	if numTabs == 0 {
		return
	}

	// Clear the entire tab bar area to prevent artifacts on reorder/close.
	// Use darkened background (matching status bar) so divider lines are visible.
	tabBarBg := darken(config.ParseHexColor(r.cfg.Colors.Background))
	tabBarRect := image.Rect(0, 0, physW, tabBarH)
	r.offscreen.SubImage(tabBarRect).(*ebiten.Image).Fill(tabBarBg)

	// Separator line at the bottom of the tab bar.
	r.offscreen.SubImage(image.Rect(0, tabBarH-1, physW, tabBarH)).(*ebiten.Image).Fill(r.separatorColor())

	// Each tab gets equal width, capped at configured max.
	maxTabW := r.cfg.Tabs.MaxWidthChars * r.font.CellW
	tabW := physW / numTabs
	if tabW > maxTabW {
		tabW = maxTabW
	}

	activeBg := config.ParseHexColor(r.cfg.Colors.Background)
	activeFg := config.ParseHexColor(r.cfg.Colors.Foreground)
	inactiveFg := config.ParseHexColor(r.cfg.Colors.BrightBlack)
	divider := r.separatorColor()

	for i, t := range tabs {
		x := i * tabW
		tabRect := image.Rect(x, 0, x+tabW, tabBarH)

		if i == activeTab {
			r.offscreen.SubImage(tabRect).(*ebiten.Image).Fill(activeBg)
			// Accent line at bottom of active tab.
			r.offscreen.SubImage(image.Rect(x, tabBarH-2, x+tabW, tabBarH)).(*ebiten.Image).Fill(r.cursorColor)
		}

		// Right-edge divider between tabs (skip last).
		if i < numTabs-1 {
			r.offscreen.SubImage(image.Rect(x+tabW-1, 0, x+tabW, tabBarH)).(*ebiten.Image).Fill(divider)
		}

		// Build the display string: rename/note input if active, otherwise the tab title.
		// Pinned tabs are prefixed with ·N to indicate their fixed slot.
		// Tabs with notes show a trailing * indicator.
		var title string
		if t.Noting {
			title = "Note: " + t.NoteText + "_"
		} else if t.Renaming {
			title = t.RenameText + "_"
		} else {
			title = t.DisplayTitle(i)
			if t.PinnedSlot != 0 {
				title = fmt.Sprintf("\u00b7%c %s", t.PinnedSlot, title)
			}
			if t.Note != "" {
				title = title + " *"
			}
		}
		maxCols := (tabW - r.font.CellW) / r.font.CellW
		if maxCols < 1 {
			maxCols = 1
		}
		if StringDisplayWidth(title) > maxCols {
			runes := []rune(title)
			cols := 0
			cut := 0
			for i, ch := range runes {
				w := RuneDisplayWidth(ch)
				if cols+w > maxCols-1 {
					cut = i
					break
				}
				cols += w
				cut = i + 1
			}
			if cut > 0 {
				title = string(runes[:cut]) + "…"
			} else {
				title = "…"
			}
		}

		fg := inactiveFg
		if i == activeTab {
			fg = activeFg
		}

		// Vertically center text in the tab bar.
		textY := (tabBarH - r.font.CellH) / 2
		r.font.DrawString(r.offscreen, title, x+r.font.CellW/2, textY, fg)

		// Activity dot for background tabs with unseen output.
		if i != activeTab && t.HasActivity {
			dotSize := r.font.CellH / 4
			if dotSize < 3 {
				dotSize = 3
			}
			dotX := x + tabW - r.font.CellW/2 - dotSize
			dotY := (tabBarH - dotSize) / 2
			dotRect := image.Rect(dotX, dotY, dotX+dotSize, dotY+dotSize)
			dotColor := r.cursorColor
			if t.HasBell {
				dotColor = r.bellColor
			}
			r.offscreen.SubImage(dotRect).(*ebiten.Image).Fill(dotColor)
		}

		// Hint mode: overlay tab number badge (1-9) when Cmd is held.
		if hintMode && i < 9 {
			badge := fmt.Sprintf("%d", i+1)
			badgeW := r.font.CellW + 6
			badgeH := r.font.CellH + 4
			badgeX := x + (tabW-badgeW)/2
			badgeY := (tabBarH - badgeH) / 2
			badgeRect := image.Rect(badgeX, badgeY, badgeX+badgeW, badgeY+badgeH)
			r.offscreen.SubImage(badgeRect).(*ebiten.Image).Fill(r.cursorColor)
			textX := badgeX + (badgeW-r.font.CellW)/2
			textY := badgeY + 2
			r.font.DrawString(r.offscreen, badge, textX, textY, config.ParseHexColor(r.cfg.Colors.Background))
		}
	}
}

// DrawPane renders a single pane into r.offscreen at the given physical rect.
// buf must be read-locked by the caller.
// isFocused controls cursor rendering; showBorder controls the focus border (multi-pane only).
// search, when non-nil and open, highlights matched cells in the pane.
func (r *Renderer) DrawPane(buf *terminal.ScreenBuffer, cur *terminal.Cursor,
	rect image.Rectangle, isFocused bool, showBorder bool, search *SearchState, headerH int) {
	r.drawPaneTo(r.offscreen, buf, cur, rect, isFocused, showBorder, search, headerH)
}

// drawPaneTo renders a single pane into an arbitrary destination image.
// Extracted from DrawPane so thumbnails can render to a temp image.
func (r *Renderer) drawPaneTo(dst *ebiten.Image, buf *terminal.ScreenBuffer, cur *terminal.Cursor,
	rect image.Rectangle, isFocused bool, showBorder bool, search *SearchState, headerH int) {

	bg := config.ParseHexColor(r.cfg.Colors.Background)

	dst.SubImage(rect).(*ebiten.Image).Fill(bg)

	rows := buf.Rows
	cols := buf.Cols
	pad := r.padding
	cellW := r.font.CellW
	cellH := r.font.CellH

	originX := rect.Min.X
	originY := rect.Min.Y + headerH

	// Pre-index search matches by display row for O(1) lookup during cell iteration.
	type matchRange struct {
		colStart, colEnd int
		isCurrent        bool
	}
	var matchByRow [][]matchRange
	if search != nil && len(search.Matches) > 0 {
		matchByRow = make([][]matchRange, rows)
		sbLen := buf.ScrollbackLen()
		for i, m := range search.Matches {
			displayRow := m.AbsRow - sbLen + buf.ViewOffset
			if displayRow < 0 || displayRow >= rows {
				continue
			}
			matchByRow[displayRow] = append(matchByRow[displayRow],
				matchRange{m.Col, m.Col + m.Len, i == search.Current})
		}
	}

	searchBgOther := config.ParseHexColor(r.cfg.Colors.Blue)
	searchBgCurrent := config.ParseHexColor(r.cfg.Colors.Yellow)
	searchFg := config.ParseHexColor(r.cfg.Colors.Background)

	for row := 0; row < rows; row++ {
		var rowMatches []matchRange
		if matchByRow != nil {
			rowMatches = matchByRow[row]
		}
		for col := 0; col < cols; col++ {
			cell := buf.GetDisplayCell(row, col)

			// Skip continuation cells — the wide char's first cell already drew both columns.
			if cell.Width == 0 {
				continue
			}

			wCells := 1
			if cell.Width == 2 {
				wCells = 2
			}

			fg := cell.FG
			cbg := cell.BG

			if cell.Inverse {
				fg, cbg = cbg, fg
			}

			absRow := buf.DisplayToAbsRow(row)
			if buf.Selection.Contains(absRow, col) {
				fg, cbg = cbg, fg
			}

			// Search highlights override selection.
			for _, mr := range rowMatches {
				if col >= mr.colStart && col < mr.colEnd {
					if mr.isCurrent {
						cbg = searchBgCurrent
					} else {
						cbg = searchBgOther
					}
					fg = searchFg
					break
				}
			}

			// URL hover underline on the focused pane.
			underline := cell.Underline
			if r.HoveredURL != nil && isFocused && r.HoveredURL.ContainsCell(row, col) {
				underline = true
				fg = r.ui.Accent
			}

			x := originX + col*cellW + pad
			y := originY + row*cellH + pad

			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}

			r.font.DrawGlyph(dst, ch, x, y, fg, cbg, cell.Bold, underline, wCells)

			// Skip the continuation column for wide chars so the outer loop doesn't re-process it.
			if wCells == 2 {
				col++
			}
		}
	}

	if isFocused && cur.IsVisible() && buf.ViewOffset == 0 && buf.CursorVisible {
		curRow := buf.CursorRow
		curCol := buf.CursorCol
		if curRow >= 0 && curRow < rows && curCol >= 0 && curCol < cols {
			cell := buf.GetDisplayCell(curRow, curCol)
			curW := 1
			if cell.Width == 2 {
				curW = 2
			}

			x := originX + curCol*cellW + pad
			y := originY + curRow*cellH + pad

			cursorStyle := 0
			switch cur.Style {
			case terminal.CursorBlock:
				cursorStyle = 0
			case terminal.CursorUnderline:
				cursorStyle = 1
			case terminal.CursorBar:
				cursorStyle = 2
			}

			r.font.DrawCursor(dst, x, y, cursorStyle, r.cursorColor, curW)

			if cur.Style == terminal.CursorBlock {
				ch := cell.Char
				if ch == 0 {
					ch = ' '
				}
				r.font.DrawGlyph(dst, ch, x, y,
					config.ParseHexColor(r.cfg.Colors.Background),
					r.cursorColor,
					cell.Bold, cell.Underline, curW)
			}
		}
	}

	if showBorder {
		r.drawBorderTo(dst, rect, r.cursorColor)
	}
}

// drawPaneOverlay renders a pane name label (top-left) and scroll position
// indicator (top-right) as small opaque pills on the pane content.
func (r *Renderer) drawPaneOverlay(rect image.Rectangle, label string, multiPane bool, viewOffset int, scrollbackLen int) {
	pad := r.padding
	cellW := r.font.CellW
	cellH := r.font.CellH

	pillBg := color.RGBA{R: r.ui.PanelBg.R, G: r.ui.PanelBg.G, B: r.ui.PanelBg.B, A: 220}
	pillFg := r.ui.Dim

	pillH := cellH + 4
	pillPad := 6

	// Pane name label — top-left, only when multiple panes visible.
	if multiPane && label != "" {
		labelW := len([]rune(label))*cellW + 2*pillPad
		labelRect := image.Rect(
			rect.Min.X+pad, rect.Min.Y+pad,
			rect.Min.X+pad+labelW, rect.Min.Y+pad+pillH,
		)
		sub := r.offscreen.SubImage(labelRect).(*ebiten.Image)
		sub.Fill(pillBg)
		r.font.DrawString(r.offscreen, label, rect.Min.X+pad+pillPad, rect.Min.Y+pad+2, pillFg)
	}

	// Scroll position indicator — top-right, always shown when scrolled.
	if viewOffset > 0 {
		text := fmt.Sprintf("↑ %d lines", viewOffset)
		textW := len([]rune(text))*cellW + 2*pillPad
		scrollRect := image.Rect(
			rect.Max.X-pad-textW, rect.Min.Y+pad,
			rect.Max.X-pad, rect.Min.Y+pad+pillH,
		)
		sub := r.offscreen.SubImage(scrollRect).(*ebiten.Image)
		sub.Fill(pillBg)
		r.font.DrawString(r.offscreen, text, rect.Max.X-pad-textW+pillPad, rect.Min.Y+pad+2, pillFg)
	}
}

// drawDividers paints the gap strips between sibling panes with the border color.
// Called every frame when the layout has not changed, so stale pixels from
// previously drawn panes don't bleed into the divider area.
func (r *Renderer) drawDividers(node *pane.LayoutNode) {
	if node == nil || node.Kind == pane.Leaf {
		return
	}
	left := node.Left
	right := node.Right
	if left != nil && right != nil {
		var gap image.Rectangle
		if node.Kind == pane.HSplit {
			// Vertical strip between left.Rect.Max.X and right.Rect.Min.X.
			if right.Rect.Min.X > left.Rect.Max.X {
				gap = image.Rect(left.Rect.Max.X, node.Rect.Min.Y, right.Rect.Min.X, node.Rect.Max.Y)
			}
		} else {
			// Horizontal strip between left.Rect.Max.Y and right.Rect.Min.Y.
			if right.Rect.Min.Y > left.Rect.Max.Y {
				gap = image.Rect(node.Rect.Min.X, left.Rect.Max.Y, node.Rect.Max.X, right.Rect.Min.Y)
			}
		}
		if !gap.Empty() {
			r.offscreen.SubImage(gap).(*ebiten.Image).Fill(r.borderColor)
		}
	}
	r.drawDividers(node.Left)
	r.drawDividers(node.Right)
}

// drawBorderTo draws a 1px rectangle border just inside rect on dst.
func (r *Renderer) drawBorderTo(dst *ebiten.Image, rect image.Rectangle, clr color.RGBA) {
	dst.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1)).(*ebiten.Image).Fill(clr)
	dst.SubImage(image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	dst.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	dst.SubImage(image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
}

// drawThickBorder draws a border of the given thickness just inside rect.
func (r *Renderer) drawThickBorder(rect image.Rectangle, clr color.RGBA, thick int) {
	img := r.offscreen
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+thick)).(*ebiten.Image).Fill(clr)
	img.SubImage(image.Rect(rect.Min.X, rect.Max.Y-thick, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+thick, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	img.SubImage(image.Rect(rect.Max.X-thick, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
}

// drawDividersTo paints divider strips between sibling panes onto dst,
// offsetting all rects by (offsetX, offsetY) for thumbnail rendering.
func (r *Renderer) drawDividersTo(dst *ebiten.Image, node *pane.LayoutNode, offsetX, offsetY int) {
	if node == nil || node.Kind == pane.Leaf {
		return
	}
	left := node.Left
	right := node.Right
	if left != nil && right != nil {
		var gap image.Rectangle
		if node.Kind == pane.HSplit {
			if right.Rect.Min.X > left.Rect.Max.X {
				gap = image.Rect(
					left.Rect.Max.X+offsetX, node.Rect.Min.Y+offsetY,
					right.Rect.Min.X+offsetX, node.Rect.Max.Y+offsetY,
				)
			}
		} else {
			if right.Rect.Min.Y > left.Rect.Max.Y {
				gap = image.Rect(
					node.Rect.Min.X+offsetX, left.Rect.Max.Y+offsetY,
					node.Rect.Max.X+offsetX, right.Rect.Min.Y+offsetY,
				)
			}
		}
		if !gap.Empty() {
			dst.SubImage(gap).(*ebiten.Image).Fill(r.borderColor)
		}
	}
	r.drawDividersTo(dst, node.Left, offsetX, offsetY)
	r.drawDividersTo(dst, node.Right, offsetX, offsetY)
}
