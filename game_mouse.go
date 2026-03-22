package main

import (
	"fmt"
	"image"
	"log"
	"math"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/terminal"
)

func (g *Game) handleMouse() {
	mx, my := ebiten.CursorPosition()
	pad := g.cfg.Window.Padding
	tabBarH := g.renderer.TabBarHeight()

	leftPressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	leftWas := g.prevMouseButtons[ebiten.MouseButtonLeft]
	rightPressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight)
	rightWas := g.prevMouseButtons[ebiten.MouseButtonRight]

	// Any mouse button state change or scroll makes the frame dirty.
	if leftPressed != leftWas || rightPressed != rightWas {
		g.screenDirty = true
	}

	// When blocks are enabled, cursor movement may change hover state.
	// Signal a redraw so the blocks layer is updated this frame.
	if g.blocksEnabled && (mx != g.prevMX || my != g.prevMY) {
		g.screenDirty = true
	}

	// URL hover detection — update when cursor moves over the focused pane.
	if mx != g.prevMX || my != g.prevMY {
		g.updateURLHover(mx, my, pad)
	}

	g.prevMX = mx
	g.prevMY = my

	// Tab hover popover tracking — update before processing clicks.
	g.updateTabHover(mx, my)

	_, scrollY := ebiten.Wheel()
	if scrollY != 0 {
		g.screenDirty = true
	}

	// Block copy buttons — left click while blocks are enabled.
	if g.blocksEnabled && !leftWas && leftPressed {
		if h := g.renderer.BlockHover; h.Active && h.CopyTarget != renderer.CopyNone {
			var copyText, label string
			h.Buf.RLock()
			switch h.CopyTarget {
			case renderer.CopyCmdText:
				// Extract only the user-typed command, excluding the prompt.
				// CmdCol (from OSC 133;B) gives the exact column where user input starts.
				// Fall back to StripPrompt pattern matching when B was not received.
				raw := h.Buf.TextRange(h.AbsStart, h.AbsStart)
				if h.CmdCol >= 0 {
					runes := []rune(raw)
					if h.CmdCol < len(runes) {
						copyText = strings.TrimSpace(string(runes[h.CmdCol:]))
					}
				}
				if copyText == "" {
					copyText = renderer.StripPrompt(raw)
				}
				label = "Command copied"
			case renderer.CopyOutput:
				// AbsCmdRow is the first output row (cursor position when C fires).
				if h.AbsCmdRow >= 0 && h.AbsEnd >= h.AbsCmdRow {
					copyText = h.Buf.TextRange(h.AbsCmdRow, h.AbsEnd)
					label = "Output copied"
				}
			case renderer.CopyAll:
				copyText = h.Buf.TextRange(h.AbsStart, h.AbsEnd)
				label = "Block copied"
			}
			h.Buf.RUnlock()
			if copyText != "" {
				cmd := exec.Command("pbcopy")
				cmd.Stdin = strings.NewReader(copyText)
				if err := cmd.Run(); err != nil {
					log.Printf("pbcopy (block): %v", err)
				}
				g.flashStatus(label)
			}
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	// Palette dismisses on any click.
	if g.palette.State.Open {
		if leftPressed && !leftWas {
			g.closePalette()
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// File explorer: wheel scrolls content, left-click outside panel closes it.
	if g.explorer.State.Open {
		panelW := g.renderer.FileExplorerPanelWidth()
		var panelX int
		physW := int(float64(g.winW) * g.dpi)
		if g.explorer.State.Side == "right" {
			panelX = physW - panelW
		}
		panelRect := image.Rect(panelX, tabBarH, panelX+panelW, int(float64(g.winH)*g.dpi))

		if scrollY != 0 && g.explorer.State.RowH > 0 {
			raw := -scrollY * float64(g.explorer.State.RowH) * 3
		step := int(math.Round(raw))
			g.explorer.State.ScrollOffset += step
			if g.explorer.State.ScrollOffset < 0 {
				g.explorer.State.ScrollOffset = 0
			}
			if g.explorer.State.ScrollOffset > g.explorer.State.MaxScroll {
				g.explorer.State.ScrollOffset = g.explorer.State.MaxScroll
			}
		}
		if leftPressed && !leftWas {
			if image.Pt(mx, my).In(panelRect) {
				g.handleExplorerClick(mx, my, panelRect)
			} else {
				g.closeFileExplorer()
			}
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Overlay: wheel scrolls, click dismisses.
	if g.overlayState.Open {
		if scrollY != 0 && g.overlayState.RowH > 0 {
			step := int(-scrollY*float64(g.overlayState.RowH)*3 + 0.5)
			g.overlayState.ScrollOffset += step
			if g.overlayState.ScrollOffset < 0 {
				g.overlayState.ScrollOffset = 0
			}
			if g.overlayState.ScrollOffset > g.overlayState.MaxScroll {
				g.overlayState.ScrollOffset = g.overlayState.MaxScroll
			}
		}
		if leftPressed && !leftWas {
			g.overlayState = renderer.OverlayState{}
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// ? button in status bar toggles the keybinding overlay.
	if leftPressed && !leftWas && image.Pt(mx, my).In(g.statusBarState.HelpBtnRect) {
		g.toggleOverlay()
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Tab switcher dismisses on any click.
	if g.tabSwitcherState.Open {
		if leftPressed && !leftWas {
			g.tabSwitcherState.Open = false
			g.screenDirty = true
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Tab search dismisses on any click.
	if g.tabSearchState.Open {
		if leftPressed && !leftWas {
			g.closeTabSearch()
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Context menu takes priority: route all mouse events to menu handling.
	if g.menuState.Open {
		g.updateMenuHover(mx, my)

		if leftPressed && !leftWas {
			if g.menuState.SubOpen && image.Pt(mx, my).In(g.menuState.SubRect) {
				if g.menuState.SubHoverIdx >= 0 {
					item := g.menuState.SubItems[g.menuState.SubHoverIdx]
					if item.Action != nil {
						g.closeMenu()
						item.Action()
					}
				}
			} else if g.menuState.HoverIdx >= 0 &&
				len(g.menuState.Items[g.menuState.HoverIdx].Children) == 0 {
				item := g.menuState.Items[g.menuState.HoverIdx]
				if item.Action != nil {
					g.closeMenu()
					item.Action()
				} else {
					g.closeMenu()
				}
			} else if !image.Pt(mx, my).In(g.menuState.Rect) &&
				(!g.menuState.SubOpen || !image.Pt(mx, my).In(g.menuState.SubRect)) {
				g.closeMenu()
			}
		}

		if rightPressed && !rightWas {
			// Right-click while menu is open: reposition to new cursor location.
			g.openContextMenu(mx, my)
		}

		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Click in tab bar — switch tab, rename on double-click, or drag to reorder.
	// During selection drag, skip the tab bar handler so auto-scroll can run.
	// tabW must match the renderer's cap (24 * CellW) so click regions align with drawn tabs.
	// Dismiss tab hover popover on any click.
	if (leftPressed && !leftWas) || (rightPressed && !rightWas) {
		g.dismissTabHover()
	}
	if my < tabBarH && !g.selDrag.Active {
		physW := int(float64(g.winW) * g.dpi)
		numTabs := len(g.tabMgr.Tabs)
		maxTabW := g.cfg.Tabs.MaxWidthChars * g.font.CellW
		tabW := 0
		if numTabs > 0 {
			tabW = physW / numTabs
			if tabW > maxTabW {
				tabW = maxTabW
			}
		}

		// Continue tab drag.
		if g.tabMgr.Dragging && leftPressed {
			if tabW > 0 {
				overIdx := mx / tabW
				if overIdx < 0 {
					overIdx = 0
				} else if overIdx >= numTabs {
					overIdx = numTabs - 1
				}
				if overIdx != g.tabMgr.ActiveIdx {
					g.reorderTab(g.tabMgr.ActiveIdx, overIdx)
					g.tabMgr.DragFromIdx = overIdx
				}
			}
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			g.screenDirty = true
			return
		}

		// End tab drag on release.
		if g.tabMgr.Dragging && !leftPressed {
			g.tabMgr.Dragging = false
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}

		if leftPressed && !leftWas {
			if numTabs > 0 && tabW > 0 {
				clicked := mx / tabW
				if clicked >= 0 && clicked < numTabs {
					now := time.Now()
					if clicked == g.tabMgr.ActiveIdx && now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
						// Double-click on the active tab → rename.
						g.startRenameTab(clicked)
					} else {
						g.switchTab(clicked)
						// Record drag start position.
						g.tabMgr.DragFromIdx = clicked
						g.tabMgr.DragStartX = mx
					}
					g.lastClickTime = now
				}
			}
		} else if leftPressed && leftWas && !g.tabMgr.Dragging {
			// Initiate drag after 8px threshold.
			dx := mx - g.tabMgr.DragStartX
			if dx < 0 {
				dx = -dx
			}
			if dx >= 8 {
				g.tabMgr.Dragging = true
			}
		} else if rightPressed && !rightWas {
			// Right-click in tab bar → show tab context menu.
			g.openTabContextMenu(mx, my)
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	if g.focused == nil {
		return
	}

	g.focused.Term.Buf.RLock()
	mouseMode := g.focused.Term.Buf.MouseMode
	sgrMouse := g.focused.Term.Buf.SgrMouse
	g.focused.Term.Buf.RUnlock()

	// Terminal-level events always take priority over PTY mouse passthrough.

	// Right-click opens context menu regardless of PTY mouse mode.
	if rightPressed && !rightWas && g.cfg.Help.ContextMenu {
		g.openContextMenu(mx, my)
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Divider drag — resize pane splits by dragging the divider.
	if g.divDrag.Active {
		if leftPressed {
			if g.divDrag.Update(mx, my) {
				g.recomputeLayout()
				g.screenDirty = true
			}
		} else {
			g.divDrag.End()
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Start divider drag on click — 4px hit margin around the 1px divider.
	if leftPressed && !leftWas && !g.zoomed {
		if split := g.layout.SplitAt(mx, my, 4); split != nil {
			g.divDrag.Start(split)
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	// Click on an inactive pane always switches focus, regardless of PTY mouse mode.
	// When zoomed, only the focused pane is visible — skip pane-switch hit test.
	if leftPressed && !leftWas && !g.zoomed {
		if clicked := g.layout.PaneAt(mx, my); clicked != nil && clicked != g.focused {
			g.setFocus(clicked)
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	// Double-click on pane header area → rename pane (mirrors tab double-click).
	if leftPressed && !leftWas && g.focused.HeaderH > 0 &&
		my >= g.focused.Rect.Min.Y && my < g.focused.Rect.Min.Y+g.focused.HeaderH {
		now := time.Now()
		if now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
			g.startRenamePane()
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
		g.lastClickTime = now
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	if mouseMode == 0 {
		col := (mx - g.focused.Rect.Min.X - pad) / g.font.CellW
		row := (my - g.focused.Rect.Min.Y - pad - g.focused.HeaderH) / g.font.CellH

		g.focused.Term.Buf.RLock()
		maxRow := g.focused.Term.Buf.Rows - 1
		maxCol := g.focused.Term.Buf.Cols - 1
		g.focused.Term.Buf.RUnlock()

		// Save unclamped row for auto-scroll during selection drag.
		rawRow := row

		if col < 0 {
			col = 0
		} else if col > maxCol {
			col = maxCol
		}
		if row < 0 {
			row = 0
		} else if row > maxRow {
			row = maxRow
		}

		// Cmd+click opens the URL under the cursor in the default browser.
		if leftPressed && !leftWas && ebiten.IsKeyPressed(ebiten.KeyMeta) {
			if g.hoveredURL != nil {
				exec.Command("open", g.hoveredURL.Text).Start() // #nosec G204 — opens user-visible URL in default browser
				g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
				g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
				return
			}
		}

		// Shift+click extends the current selection to the clicked cell.
		if leftPressed && !leftWas && ebiten.IsKeyPressed(ebiten.KeyShift) {
			g.focused.Term.Buf.Lock()
			if g.focused.Term.Buf.Selection.Active {
				absRow := g.focused.Term.Buf.DisplayToAbsRow(row)
				snapCol := col
				if snapCol >= 0 && snapCol < g.focused.Term.Buf.Cols &&
					g.focused.Term.Buf.GetDisplayCell(row, snapCol).Width == 0 && snapCol > 0 {
					snapCol--
				}
				g.focused.Term.Buf.Selection.EndRow = absRow
				g.focused.Term.Buf.Selection.EndCol = snapCol
				g.focused.Term.Buf.BumpRenderGen()
			}
			g.focused.Term.Buf.Unlock()
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}

		if leftPressed && !leftWas {
			now := time.Now()
			sameCell := row == g.lastClickRow && col == g.lastClickCol
			if sameCell && now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
				g.clickCount++
			} else {
				g.clickCount = 1
			}
			g.lastClickTime = now
			g.lastClickRow = row
			g.lastClickCol = col

			g.focused.Term.Buf.Lock()
			absRow := g.focused.Term.Buf.DisplayToAbsRow(row)
			// Snap col to parent cell if clicking on a wide char continuation.
			snapCol := col
			if snapCol >= 0 && snapCol < g.focused.Term.Buf.Cols && g.focused.Term.Buf.GetDisplayCell(row, snapCol).Width == 0 && snapCol > 0 {
				snapCol--
			}
			switch g.clickCount {
			case 1:
				g.selDrag.Active = true
				g.focused.Term.Buf.Selection = terminal.Selection{
					Active:   true,
					StartRow: absRow, StartCol: snapCol,
					EndRow:   absRow, EndCol: snapCol,
				}
			case 2:
				g.selDrag.Active = false
				g.focused.Term.Buf.Selection = g.wordSelection(row, col)
			default:
				g.selDrag.Active = false
				g.focused.Term.Buf.Selection = terminal.Selection{
					Active:   true,
					StartRow: absRow, StartCol: 0,
					EndRow:   absRow, EndCol: g.focused.Term.Buf.Cols - 1,
				}
				g.clickCount = 0
			}
			g.focused.Term.Buf.BumpRenderGen()
			g.focused.Term.Buf.Unlock()
		} else if leftPressed && leftWas && g.selDrag.Active {
			g.focused.Term.Buf.Lock()
			// Auto-scroll when dragging past the pane edges.
			// Selection uses absolute rows, so StartRow stays stable across
			// ViewOffset changes — no adjustment needed.
			if rawRow < 0 {
				vo := g.focused.Term.Buf.ViewOffset + 1
				maxVO := g.focused.Term.Buf.ScrollbackLen()
				if vo > maxVO {
					vo = maxVO
				}
				g.focused.Term.Buf.SetViewOffset(vo)
			} else if rawRow > maxRow {
				vo := g.focused.Term.Buf.ViewOffset - 1
				if vo < 0 {
					vo = 0
				}
				g.focused.Term.Buf.SetViewOffset(vo)
			}
			g.focused.Term.Buf.Selection.EndRow = g.focused.Term.Buf.DisplayToAbsRow(row)
			// Snap to parent cell if dragging onto a continuation cell.
			dragCol := col
			if dragCol >= 0 && dragCol < g.focused.Term.Buf.Cols && g.focused.Term.Buf.GetDisplayCell(row, dragCol).Width == 0 && dragCol > 0 {
				dragCol--
			}
			g.focused.Term.Buf.Selection.EndCol = dragCol
			g.focused.Term.Buf.BumpRenderGen()
			g.focused.Term.Buf.Unlock()
			g.screenDirty = true
		} else if !leftPressed && leftWas {
			if g.selDrag.Active {
				g.selDrag.Active = false
				g.focused.Term.Buf.Lock()
				sel := g.focused.Term.Buf.Selection.Normalize()
				if sel.StartRow == sel.EndRow && sel.StartCol == sel.EndCol {
					g.focused.Term.Buf.Selection = terminal.Selection{}
				}
				g.focused.Term.Buf.Unlock()
			}
		}

		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// PTY mouse mode.
	col := (mx-g.focused.Rect.Min.X-pad)/g.font.CellW + 1
	row := (my-g.focused.Rect.Min.Y-pad-g.focused.HeaderH)/g.font.CellH + 1
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}

	type btnEntry struct {
		btn    ebiten.MouseButton
		btnNum int
	}
	buttons := []btnEntry{
		{ebiten.MouseButtonLeft, 0},
		{ebiten.MouseButtonMiddle, 1},
		{ebiten.MouseButtonRight, 2},
	}
	for _, b := range buttons {
		pressed := ebiten.IsMouseButtonPressed(b.btn)
		was := g.prevMouseButtons[b.btn]
		if pressed && !was {
			g.mouseHeldBtn = b.btnNum
			g.sendMouseEvent(b.btnNum, col, row, true, sgrMouse)
		} else if !pressed && was {
			if g.mouseHeldBtn == b.btnNum {
				g.mouseHeldBtn = -1
			}
			g.sendMouseEvent(b.btnNum, col, row, false, sgrMouse)
		}
		g.prevMouseButtons[b.btn] = pressed
	}

	// Send motion events for mode 1002 (button-tracking: only while a button is held)
	// and mode 1003 (any-motion: always). The motion button code = held button + 32,
	// or 35 when no button is held (mode 1003 only).
	if mouseMode >= 1002 && (col != g.lastMouseCol || row != g.lastMouseRow) {
		if mouseMode == 1003 || g.mouseHeldBtn >= 0 {
			motionBtn := 35 // no button held
			if g.mouseHeldBtn >= 0 {
				motionBtn = g.mouseHeldBtn + 32
			}
			g.sendMouseMotion(motionBtn, col, row, sgrMouse)
		}
		g.lastMouseCol = col
		g.lastMouseRow = row
	}

	_, wy := ebiten.Wheel()
	if wy != 0 {
		// Shift+scroll bypasses PTY mouse mode and scrolls the terminal's own
		// scrollback buffer (standard behaviour in iTerm2, kitty, etc.).
		// Blocked in alt screen — TUI apps own the viewport.
		g.focused.Term.Buf.RLock()
		altShift := g.focused.Term.Buf.IsAltActive()
		g.focused.Term.Buf.RUnlock()
		if ebiten.IsKeyPressed(ebiten.KeyShift) && !altShift {
			g.scrollAccum += wy * float64(g.cfg.Scroll.WheelLinesPerTick)
			lines := int(g.scrollAccum)
			if lines != 0 {
				g.scrollAccum -= float64(lines)
				g.focused.Term.Buf.Lock()
				if lines > 0 {
					g.focused.Term.Buf.ScrollViewUp(lines)
				} else {
					g.focused.Term.Buf.ScrollViewDown(-lines)
				}
				g.focused.Term.Buf.Unlock()
			}
		} else {
			btn := mouseScrollUp
			if wy < 0 {
				btn = mouseScrollDown
			}
			g.sendMouseEvent(btn, col, row, true, sgrMouse)
		}
	}
}

// wordSelection returns a Selection covering the word at (row, col).
// Scans across soft-wrap boundaries so that a word split across two or more
// display rows is selected in full.
// Must be called with Buf write lock held.
func (g *Game) wordSelection(row, col int) terminal.Selection {
	buf := g.focused.Term.Buf
	isWordChar := func(r rune) bool {
		return r != ' ' && r != 0 &&
			(unicode.IsLetter(r) || unicode.IsDigit(r) ||
				r == '_' || r == '.' || r == '/')
	}

	// Snap to parent cell if clicking on a continuation cell.
	cell := buf.GetDisplayCell(row, col)
	if cell.Width == 0 && col > 0 {
		col--
		cell = buf.GetDisplayCell(row, col)
	}

	absRow := buf.DisplayToAbsRow(row)
	if !isWordChar(cell.Char) {
		return terminal.Selection{Active: true, StartRow: absRow, StartCol: col, EndRow: absRow, EndCol: col}
	}

	startRow, startCol := row, col
scanBackward:
	for {
		for startCol > 0 {
			prev := buf.GetDisplayCell(startRow, startCol-1)
			if prev.Width == 0 {
				startCol--
				continue
			}
			if !isWordChar(prev.Char) {
				break scanBackward
			}
			startCol--
		}
		// Reached column 0. Cross soft-wrap boundary to the previous row.
		if startRow > 0 && buf.IsDisplayRowWrapped(startRow) {
			// Peek at the last usable cell of the previous row (skip trailing continuation).
			peekCol := buf.Cols - 1
			if buf.GetDisplayCell(startRow-1, peekCol).Width == 0 && peekCol > 0 {
				peekCol--
			}
			if !isWordChar(buf.GetDisplayCell(startRow-1, peekCol).Char) {
				break scanBackward
			}
			startRow--
			startCol = peekCol
			// Inner loop continues scanning leftward from peekCol.
		} else {
			break scanBackward
		}
	}

	endRow, endCol := row, col
scanForward:
	for {
		for endCol < buf.Cols-1 {
			next := buf.GetDisplayCell(endRow, endCol+1)
			if next.Width == 0 {
				endCol++
				continue
			}
			if !isWordChar(next.Char) {
				break scanForward
			}
			endCol++
		}
		// Reached last column. Cross soft-wrap boundary to the next row.
		if endRow+1 < buf.Rows && buf.IsDisplayRowWrapped(endRow+1) {
			// Peek at the first usable cell of the next row (skip leading continuation).
			peekCol := 0
			if buf.GetDisplayCell(endRow+1, peekCol).Width == 0 && buf.Cols > 1 {
				peekCol++
			}
			if !isWordChar(buf.GetDisplayCell(endRow+1, peekCol).Char) {
				break scanForward
			}
			endRow++
			endCol = peekCol
			// Inner loop continues scanning rightward from peekCol.
		} else {
			break scanForward
		}
	}

	return terminal.Selection{
		Active:   true,
		StartRow: buf.DisplayToAbsRow(startRow), StartCol: startCol,
		EndRow:   buf.DisplayToAbsRow(endRow), EndCol: endCol,
	}
}

// copySelection copies the current selection text to the clipboard via pbcopy.
func (g *Game) copySelection() {
	result := g.extractSelectedText()
	if result == "" {
		return
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(result)
	if err := cmd.Run(); err != nil {
		log.Printf("pbcopy (selection): %v", err)
	}
}

// sendMouseEvent encodes and sends a single mouse event to the focused PTY.
func (g *Game) sendMouseEvent(btn, col, row int, press bool, sgr bool) {
	if sgr {
		final := 'M'
		if !press && btn < mouseScrollUp {
			final = 'm'
		}
		seq := fmt.Sprintf("\x1B[<%d;%d;%d%c", btn, col, row, final)
		g.focused.Term.SendBytes([]byte(seq))
	} else {
		if col > mouseX10CoordMax || row > mouseX10CoordMax {
			return
		}
		b := byte(btn + mouseX10Offset) // #nosec G115 — btn is 0-4; col/row guarded above; all fit byte
		if !press {
			b = mouseX10Release
		}
		g.focused.Term.SendBytes([]byte{0x1B, '[', 'M', b, byte(col + mouseX10Offset), byte(row + mouseX10Offset)}) // #nosec G115
	}
}

// sendMouseMotion encodes and sends a motion event to the focused PTY.
// btn is the motion button code (held button + 32, or 35 for no-button).
func (g *Game) sendMouseMotion(btn, col, row int, sgr bool) {
	if sgr {
		seq := fmt.Sprintf("\x1B[<%d;%d;%dM", btn, col, row)
		g.focused.Term.SendBytes([]byte(seq))
	} else {
		if col > mouseX10CoordMax || row > mouseX10CoordMax {
			return
		}
		g.focused.Term.SendBytes([]byte{0x1B, '[', 'M', byte(btn), byte(col + mouseX10Offset), byte(row + mouseX10Offset)}) // #nosec G115 — col/row guarded above
	}
}