package renderer

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
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
	lastRenaming      bool
	lastRenameText    string
	lastSearchCurrent int
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
	cfg     *RenderConfig
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

	// VaultSuggestion is the ghost text to display after the cursor.
	// Set by main.go before DrawAll; cleared when no suggestion is available.
	VaultSuggestion string
}

// CopyTarget identifies which part of a block to copy.
type CopyTarget int

const (
	CopyNone   CopyTarget = iota
	CopyCmdText            // command line only
	CopyOutput             // output rows only
	CopyAll                // command + output
)

const noMatchesLabel = "no matches"

// filterResult holds the original index and match position from filterBySubstring.
type filterResult struct {
	index    int
	matchPos int
}

// filterBySubstring returns indices into an item list sorted by case-insensitive
// substring match position (pos==0 ranked first). textFn returns the search
// texts for item i; the first matching text wins.
func filterBySubstring(count int, query string, textFn func(i int) []string) []filterResult {
	if query == "" {
		results := make([]filterResult, count)
		for i := range results {
			results[i] = filterResult{index: i}
		}
		return results
	}
	q := strings.ToLower(query)
	var rank0, rank1 []filterResult
	for i := 0; i < count; i++ {
		pos := -1
		for _, text := range textFn(i) {
			if p := strings.Index(strings.ToLower(text), q); p >= 0 {
				pos = p
				break
			}
		}
		if pos < 0 {
			continue
		}
		r := filterResult{index: i, matchPos: pos}
		if pos == 0 {
			rank0 = append(rank0, r)
		} else {
			rank1 = append(rank1, r)
		}
	}
	return append(rank0, rank1...)
}

// modalSize returns the physical width and height of the modal layer.
func (r *Renderer) modalSize() (int, int) {
	b := r.modalLayer.Bounds()
	return b.Dx(), b.Dy()
}

// screenSize returns the physical width and height of the offscreen buffer.
func (r *Renderer) screenSize() (int, int) {
	b := r.offscreen.Bounds()
	return b.Dx(), b.Dy()
}

// drawBackdrop draws a semi-transparent backdrop over the full modal layer.
func (r *Renderer) drawBackdrop(physW, physH int) {
	if r.overlayBg == nil {
		r.overlayBg = ebiten.NewImage(1, 1)
		r.overlayBg.Fill(r.ui.Backdrop)
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(physW), float64(physH))
	r.modalLayer.DrawImage(r.overlayBg, op)
}

// drawBorder draws a 1px border around rect on img.
func drawBorder(img *ebiten.Image, rect image.Rectangle, c color.RGBA) {
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1)).(*ebiten.Image).Fill(c)
	img.SubImage(image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(c)
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y)).(*ebiten.Image).Fill(c)
	img.SubImage(image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(c)
}

// clampScroll clamps scrollOffset into [0, maxScroll] and ensures maxScroll >= 0.
func clampScroll(scrollOffset, maxScroll int) (int, int) {
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > maxScroll {
		scrollOffset = maxScroll
	}
	return scrollOffset, maxScroll
}

// pinnedBadge returns a "[X]" badge for the given pinned slot rune,
// or the placeholder string when slot is 0 (unpinned).
func pinnedBadge(slot rune, placeholder string) string {
	if slot != 0 {
		return fmt.Sprintf("[%c]", slot)
	}
	return placeholder
}


// NewRenderer creates a Renderer. Call SetSize after window dimensions are known.
func NewRenderer(font *FontRenderer, cfg *RenderConfig) *Renderer {
	return &Renderer{
		font:         font,
		cfg:          cfg,
		padding:      cfg.Window.Padding,
		cursorColor:  parseHexColor(cfg.Colors.Cursor),
		borderColor:  parseHexColor(cfg.Colors.Border),
		bellColor:    parseHexColor(cfg.Bell.Color),
		paneCache:    make(map[*pane.Pane]*paneCacheEntry),
		layoutDirty:  true, // force full clear on first frame
		blockTintImg: ebiten.NewImage(1, 1),
		ui:           deriveUIColors(cfg),
	}
}

// ReloadColors updates the renderer's color state from a new RenderConfig.
// Re-parses cursor/border colors, re-derives UI colors, clears pane cache,
// and marks the layout dirty so everything redraws.
func (r *Renderer) ReloadColors(cfg *RenderConfig) {
	r.cfg = cfg
	r.cursorColor = parseHexColor(cfg.Colors.Cursor)
	r.borderColor = parseHexColor(cfg.Colors.Border)
	r.bellColor = parseHexColor(cfg.Bell.Color)
	r.ui = deriveUIColors(cfg)
	if r.overlayBg != nil {
		r.overlayBg.Deallocate()
		r.overlayBg = nil // recreated with new Backdrop color on next draw
	}
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

// DrawState bundles all parameters for a single DrawAll invocation.
type DrawState struct {
	Screen         *ebiten.Image
	Tabs           []*tab.Tab
	ActiveTab      int
	Focused        *pane.Pane
	Zoomed         bool
	Menu           *MenuState
	Overlay        *OverlayState
	Confirm        *ConfirmState
	Search         *SearchState
	StatusBar      *StatusBarState
	TabSwitcher    *TabSwitcherState
	Palette        *PaletteState
	PaletteEntries []PaletteEntry
	FileExplorer   *FileExplorerState
	TabSearch      *TabSearchState
	Stats          *StatsState
	TabHover       *TabHoverState
	MdViewer       *MarkdownViewerState
	URLInput       *URLInputState
	HintMode       bool
}

// DrawAll renders the tab bar, all panes, status bar, and any active UI overlays onto screen.
func (r *Renderer) DrawAll(ds DrawState) {
	if r.offscreen == nil {
		return
	}

	// Phase 1 — prepare frame: clear layers, handle layout dirty.
	layout := r.prepareFrame(ds.Tabs, ds.ActiveTab, ds.Zoomed)

	// Phase 2 — draw tab bar.
	r.drawTabBar(ds.Tabs, ds.ActiveTab, ds.HintMode)

	// Phase 3 — draw panes and snapshot block data.
	blockSnaps := r.drawPanes(layout, ds.Focused, ds.Zoomed, ds.Search)

	// Phase 4 — render block decorations.
	r.renderBlocks(blockSnaps)

	// Phase 5 — non-modal overlays (drawn onto offscreen).
	r.drawSearchBar(ds.Search)
	r.drawStats(ds.Stats)
	r.drawStatusBar(ds.StatusBar)

	// Phase 6 — modal overlays (drawn onto modalLayer).
	r.drawModalOverlays(ds)

	// Phase 7 — final composite: offscreen → blocksLayer → modalLayer → screen.
	r.composeFinal(ds.Screen)
}

// prepareFrame clears layers, handles layout-dirty state, and draws pane dividers.
// Returns the active tab's layout node for pane rendering.
func (r *Renderer) prepareFrame(tabs []*tab.Tab, activeTab int, zoomed bool) *pane.LayoutNode {
	r.modalLayer.Clear()

	var layout *pane.LayoutNode
	if activeTab < len(tabs) {
		layout = tabs[activeTab].Layout
	}
	if r.layoutDirty {
		r.offscreen.Fill(r.borderColor)
		r.paneCache = make(map[*pane.Pane]*paneCacheEntry)
		r.layoutDirty = false
	} else if layout != nil && !zoomed {
		r.drawDividers(layout)
	}
	return layout
}

// drawPanes iterates visible panes, renders content, and collects block snapshots.
func (r *Renderer) drawPanes(layout *pane.LayoutNode, focused *pane.Pane, zoomed bool, search *SearchState) []*blockSnap {
	r.BlockHover = BlockHoverState{}

	if layout == nil {
		return nil
	}

	leaves := layout.Leaves()
	multiPane := len(leaves) > 1
	var blockSnaps []*blockSnap

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

		// Snapshot block data while the lock is held.
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

		// Cache check: only actual content changes trigger DrawPane + overlay redraw.
		hasURLHover := isFocused && r.HoveredURL != nil
		procName := p.ProcName
		searchCurrent := -1
		if isFocused && search != nil {
			searchCurrent = search.Current
		}
		unchanged := gen == cache.lastRenderGen &&
			viewOff == cache.lastViewOffset &&
			curRow == cache.lastCursorRow &&
			curCol == cache.lastCursorCol &&
			hasURLHover == cache.hadURLHover &&
			procName == cache.lastProcName &&
			p.CustomName == cache.lastCustomName &&
			p.Renaming == cache.lastRenaming &&
			p.RenameText == cache.lastRenameText &&
			searchCurrent == cache.lastSearchCurrent

		if !unchanged {
			var paneSearch *SearchState
			if isFocused {
				paneSearch = search
			}
			r.DrawPane(p.Term.Buf, p.Term.Cursor, p.Rect, isFocused, isFocused && multiPane && !zoomed, paneSearch, p.HeaderH)

			label := p.CustomName
			if label == "" {
				label = procName
			}
			if label == "" {
				label = fmt.Sprintf("Pane %d", i+1)
			}
			if p.Renaming {
				label = inputWithCursor(p.RenameText, p.RenameCursorPos)
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
			cache.lastSearchCurrent = searchCurrent
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
	return blockSnaps
}

// renderBlocks renders block decorations onto the dedicated blocks layer.
func (r *Renderer) renderBlocks(snaps []*blockSnap) {
	if !r.BlocksEnabled || r.blocksLayer == nil {
		return
	}
	r.blocksLayer.Clear()
	for _, s := range snaps {
		r.drawBlocksSnap(s)
	}
}

// drawModalOverlays renders all modal/overlay content onto modalLayer.
// Drawing order determines Z-order (last drawn = on top).
func (r *Renderer) drawModalOverlays(ds DrawState) {
	r.drawTabHoverPopover(ds.TabHover)

	if ds.Menu != nil {
		r.drawContextMenu(ds.Menu)
	}
	if ds.Overlay != nil {
		r.drawOverlay(ds.Overlay)
	}

	r.drawMarkdownViewer(ds.MdViewer)
	r.drawURLInput(ds.URLInput)

	if ds.Confirm != nil {
		r.drawConfirm(ds.Confirm)
	}

	r.drawTabSwitcher(ds.Tabs, ds.ActiveTab, ds.TabSwitcher)
	r.drawTabSearch(ds.Tabs, ds.ActiveTab, ds.TabSearch)

	if ds.Palette != nil && ds.Palette.Open {
		r.drawPalette(ds.PaletteEntries, ds.Palette)
	}
	if ds.FileExplorer != nil && ds.FileExplorer.Open {
		r.drawFileExplorer(ds.FileExplorer)
	}
}

// composeFinal composites offscreen → blocksLayer → modalLayer onto the final screen.
func (r *Renderer) composeFinal(screen *ebiten.Image) {
	screen.DrawImage(r.offscreen, nil)
	if r.BlocksEnabled && r.blocksLayer != nil {
		screen.DrawImage(r.blocksLayer, nil)
	}
	screen.DrawImage(r.modalLayer, nil)
}

// drawTabBar renders the tab strip at the top of the offscreen buffer.
// When hintMode is true, tab number badges (1-9) are overlaid for discoverability.
