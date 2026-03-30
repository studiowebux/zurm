package main

import (
	"fmt"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/tab"
)

// focusEntry records a tab+pane focus state for the history stack.
type focusEntry struct {
	tabIdx int
	pane   *pane.Pane
}

// parkActiveTab moves the active tab to the parking lot (hidden but still alive).
// Refused if only one visible tab remains.
func (g *Game) parkActiveTab() {
	if !g.tabMgr.Park(g.tabMgr.ActiveIdx) {
		g.flashStatus("Cannot park last tab.")
		return
	}
	g.renderer.ClearPaneCache()
	g.renderer.SetLayoutDirty()
	g.render.Dirty = true
}

// unparkTab moves the parked tab at parkedIdx back to the visible slice and activates it.
// Recomputes the tab's pane layout in case the window was resized while it was parked.
func (g *Game) unparkTab(parkedIdx int) {
	g.tabMgr.Unpark(parkedIdx)
	t := g.tabMgr.Tabs[len(g.tabMgr.Tabs)-1]
	paneRect := g.contentRect()
	setPaneHeaders(t.Layout, g.font.CellH)
	t.Layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range t.Layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.switchTab(len(g.tabMgr.Tabs) - 1)
}

// newTab creates a new tab and switches to it.
// The starting directory is controlled by cfg.Tabs.NewTabDir:
//   - "cwd"  → inherit the active tab's current working directory
//   - "home" → always open in $HOME
func (g *Game) newTab() {
	if g.cfg.Tabs.MaxOpen > 0 && g.tabMgr.Count() >= g.cfg.Tabs.MaxOpen {
		g.flashStatus(fmt.Sprintf("Tab limit reached (%d). Park or close a tab first.", g.cfg.Tabs.MaxOpen))
		return
	}
	paneRect := g.contentRect()

	var dir string
	switch g.cfg.Tabs.NewTabDir {
	case "home":
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	default: // "cwd"
		dir = g.status.Bar.Cwd
	}

	// Sanitize the directory in case it's inside a .app bundle
	dir = sanitizeDirectory(dir)

	t, err := tab.New(g.cfg, paneRect, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	t.Title = fmt.Sprintf("tab %d", g.tabMgr.Count()+1)
	g.tabMgr.Add(t)
	g.switchTab(g.tabMgr.Count() - 1)
}

// newServerTab creates a new tab whose root pane is backed by zurm-server (Mode B).
// If the server binary is not found or the connection fails, the pane falls back
// to a local PTY — the tab is always created.
func (g *Game) newServerTab() {
	if g.cfg.Tabs.MaxOpen > 0 && g.tabMgr.Count() >= g.cfg.Tabs.MaxOpen {
		g.flashStatus(fmt.Sprintf("Tab limit reached (%d). Park or close a tab first.", g.cfg.Tabs.MaxOpen))
		return
	}
	paneRect := g.contentRect()

	var dir string
	switch g.cfg.Tabs.NewTabDir {
	case "home":
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	default: // "cwd"
		dir = g.status.Bar.Cwd
	}
	dir = sanitizeDirectory(dir)

	p, err := pane.NewServer(g.cfg, paneRect, g.font.CellW, g.font.CellH, dir, "")
	if err != nil {
		return
	}
	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   fmt.Sprintf("tab %d", g.tabMgr.Count()+1),
	}
	g.tabMgr.Add(t)
	g.switchTab(g.tabMgr.Count() - 1)
}

// closeActiveTab closes all panes in the active tab and removes it.
func (g *Game) closeActiveTab() {
	g.dismissTabHover()
	for _, leaf := range g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Layout.Leaves() {
		leaf.Pane.Term.Close()
	}
	if !g.tabMgr.Remove(g.tabMgr.ActiveIdx) {
		// No tabs remain — activeLayout()/activeFocused() return nil automatically.
		return
	}
	g.renderer.ClearPaneCache()
	g.renderer.SetLayoutDirty()
}

// dismissTabHover clears the tab hover popover state and marks the screen dirty.
func (g *Game) dismissTabHover() {
	if g.status.TabHover.TabIdx >= 0 || g.status.TabHover.Active {
		renderer.DismissTabHover(&g.status.TabHover)
		g.render.Dirty = true
	}
}

// updateTabHover tracks which tab the mouse is hovering over and manages the
// popover lifecycle (delay, activation, cache invalidation).
func (g *Game) updateTabHover(mx, my int) {
	if !g.cfg.Tabs.Hover.Enabled {
		return
	}

	tabBarH := g.renderer.TabBarHeight()
	numTabs := len(g.tabMgr.Tabs)

	// Dismiss conditions: single tab, overlays open, dragging, cursor outside tab bar.
	if numTabs <= 1 || g.tabMgr.Dragging || g.overlays.Menu.Open || g.overlays.Help.Open ||
		g.overlays.Confirm.Open || g.search.State.Open || g.palette.State.Open ||
		g.explorer.State.Open || g.overlays.TabSwitcher.Open || g.overlays.TabSearch.Open {
		g.dismissTabHover()
		return
	}

	if my < 0 || my >= tabBarH {
		g.dismissTabHover()
		return
	}

	// Compute which tab the cursor is over (same width calc as tab click handler).
	physW := int(float64(g.winW) * g.dpi)
	maxTabW := g.cfg.Tabs.MaxWidthChars * g.font.CellW
	tabW := physW / numTabs
	if tabW > maxTabW {
		tabW = maxTabW
	}
	if tabW <= 0 {
		g.dismissTabHover()
		return
	}

	hoverIdx := mx / tabW
	if hoverIdx < 0 || hoverIdx >= numTabs {
		g.dismissTabHover()
		return
	}

	// Skip the active tab — user already sees it.
	if hoverIdx == g.tabMgr.ActiveIdx {
		g.dismissTabHover()
		return
	}

	// Tab changed — reset hover timer.
	if hoverIdx != g.status.TabHover.TabIdx {
		g.dismissTabHover()
		g.status.TabHover.TabIdx = hoverIdx
		g.status.TabHover.HoverStart = time.Now()
		return
	}

	// Check if delay has elapsed.
	delay := time.Duration(g.cfg.Tabs.Hover.DelayMs) * time.Millisecond
	if !g.status.TabHover.Active && time.Since(g.status.TabHover.HoverStart) < delay {
		return
	}

	// Activate the popover.
	if !g.status.TabHover.Active {
		g.status.TabHover.Active = true

		// Compute popover position (centered below the hovered tab).
		popW := int(float64(g.cfg.Tabs.Hover.Width) * g.dpi)
		popH := int(float64(g.cfg.Tabs.Hover.Height) * g.dpi)
		tabCenterX := hoverIdx*tabW + tabW/2
		popX := tabCenterX - popW/2
		popY := tabBarH + 4 // small gap below tab bar

		g.status.TabHover.PopoverX = popX
		g.status.TabHover.PopoverY = popY
		g.status.TabHover.PopoverW = popW
		g.status.TabHover.PopoverH = popH
		g.render.Dirty = true
	}

	// Check cache validity and regenerate thumbnail if stale.
	hoveredTab := g.tabMgr.Tabs[hoverIdx]
	cacheKey := renderer.TabHoverCacheKey(hoveredTab)
	if cacheKey != g.status.TabHover.CacheKey || g.status.TabHover.Thumbnail == nil {
		if g.status.TabHover.Thumbnail != nil {
			g.status.TabHover.Thumbnail.Deallocate()
		}
		contentRect := g.renderer.ComputeContentRect(hoveredTab)
		g.status.TabHover.Thumbnail = g.renderer.RenderTabThumbnail(hoveredTab, contentRect)
		g.status.TabHover.CacheKey = cacheKey
		g.render.Dirty = true
	}
}

// pushFocus records the current focus state before changing it.
func (g *Game) pushFocus() {
	g.tabMgr.PushFocus(g.activeFocused())
}

// goBack pops the focus history stack and navigates to the previous location.
func (g *Game) goBack() {
	for {
		e, ok := g.tabMgr.PopFocus()
		if !ok {
			return
		}
		// Skip stale entries (tab removed or pane closed).
		if e.tabIdx < 0 || e.tabIdx >= len(g.tabMgr.Tabs) {
			continue
		}
		// Verify the pane still exists in that tab.
		found := false
		for _, leaf := range g.tabMgr.Tabs[e.tabIdx].Layout.Leaves() {
			if leaf.Pane == e.pane {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		// Skip if it's the current location.
		if e.tabIdx == g.tabMgr.ActiveIdx && e.pane == g.activeFocused() {
			continue
		}
		if e.tabIdx != g.tabMgr.ActiveIdx {
			g.switchTabNoHistory(e.tabIdx)
		}
		g.setFocusNoHistory(e.pane)
		return
	}
}

// switchTab activates the tab at index i, recording focus history.
func (g *Game) switchTab(i int) {
	g.pushFocus()
	g.switchTabNoHistory(i)
}

// switchTabNoHistory activates the tab at index i without recording history.
// Used by goBack to avoid polluting the stack.
func (g *Game) switchTabNoHistory(i int) {
	if i < 0 || i >= len(g.tabMgr.Tabs) {
		return
	}
	// Snapshot the outgoing tab's render generation so that UI-only bumps
	// (selection, search, cursor blink) do not trigger a false activity dot.
	g.tabMgr.Tabs[g.tabMgr.ActiveIdx].SnapshotGen()

	// Restore pane rects before leaving a zoomed tab so the layout is
	// correct when switching back later.
	if g.zoomed {
		g.unzoom()
	}
	g.tabMgr.ActiveIdx = i
	g.tabMgr.Tabs[i].SnapshotGen()
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
	g.input.SelDrag.Active = false
	g.status.Bar.ForegroundProc = ""
	if f := g.activeFocused(); f != nil {
		f.Term.RefreshForeground(g.ctx)
	}
	if g.search.State.Open {
		g.closeSearchOverlay()
	}
	g.render.Dirty = true
}

// nextTab cycles to the next tab.
func (g *Game) nextTab() {
	g.switchTab(g.tabMgr.NextIdx())
}

// prevTab cycles to the previous tab.
func (g *Game) prevTab() {
	g.switchTab(g.tabMgr.PrevIdx())
}

// pinTab pins the active tab to the given home-row slot, evicting any previous
// occupant. Calling again with the same slot while already pinned there unpins.
func (g *Game) pinTab(slot rune) {
	g.tabMgr.PinActive(slot)
	g.render.Dirty = true
}

// switchToSlot activates the tab pinned to the given home-row slot.
// If the pinned tab is parked, it is unparked first.
// Does nothing if no tab is pinned to the slot.
func (g *Game) switchToSlot(slot rune) {
	if idx := g.tabMgr.PinnedTab(slot); idx >= 0 {
		g.switchTab(idx)
		return
	}
	if idx := g.tabMgr.PinnedParkedTab(slot); idx >= 0 {
		g.unparkTab(idx)
	}
}

// openTabSwitcher opens (or closes) the tab switcher overlay.
func (g *Game) openTabSwitcher() {
	if g.overlays.TabSwitcher.Open {
		g.overlays.TabSwitcher.Open = false
	} else {
		g.overlays.TabSwitcher.Open = true
		g.overlays.TabSwitcher.Cursor = g.tabMgr.ActiveIdx
	}
	g.render.Dirty = true
}

// handlePinInput processes the second keypress of the Cmd+Space chord.
// A home-row letter jumps to that slot; Shift+letter pins the active tab there.
// Any other keypress cancels the mode without action.
func (g *Game) handlePinInput() {
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	type slotKey struct {
		key  ebiten.Key
		slot rune
	}
	keys := []slotKey{
		{ebiten.KeyA, 'a'}, {ebiten.KeyS, 's'}, {ebiten.KeyD, 'd'},
		{ebiten.KeyF, 'f'}, {ebiten.KeyG, 'g'}, {ebiten.KeyH, 'h'},
		{ebiten.KeyJ, 'j'}, {ebiten.KeyK, 'k'}, {ebiten.KeyL, 'l'},
	}

	for _, hr := range keys {
		pressed := ebiten.IsKeyPressed(hr.key)
		wasPressed := g.input.PrevKeys[hr.key]
		g.input.PrevKeys[hr.key] = pressed
		if pressed && !wasPressed {
			if shift {
				g.pinTab(hr.slot)
			} else {
				g.switchToSlot(hr.slot)
			}
			g.tabMgr.PinMode = false
			g.render.Dirty = true
			return
		}
	}

	// ESC cancels immediately. inpututil catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.tabMgr.PinMode = false
		g.render.Dirty = true
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	// Any other keypress (Space, Enter) cancels the mode.
	cancelKeys := []ebiten.Key{
		ebiten.KeySpace, ebiten.KeyEnter, ebiten.KeyNumpadEnter,
	}
	for _, key := range cancelKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		g.input.PrevKeys[key] = pressed
		if pressed && !wasPressed {
			g.tabMgr.PinMode = false
			g.render.Dirty = true
			return
		}
	}
	// Cancel on any printable char not in home row.
	if len(ebiten.AppendInputChars(nil)) > 0 {
		g.tabMgr.PinMode = false
		g.render.Dirty = true
	}
}

// reorderTab moves the tab at index from to index to, keeping activeTab correct.
func (g *Game) reorderTab(from, to int) {
	g.dismissTabHover()
	g.tabMgr.Reorder(from, to)
	g.overlays.TabSwitcher.Cursor = to
	g.render.Dirty = true
}

// moveTabLeft moves the active tab one position to the left.
func (g *Game) moveTabLeft() {
	if g.tabMgr.ActiveIdx > 0 {
		g.reorderTab(g.tabMgr.ActiveIdx, g.tabMgr.ActiveIdx-1)
	}
}

// moveTabRight moves the active tab one position to the right.
func (g *Game) moveTabRight() {
	if g.tabMgr.ActiveIdx < len(g.tabMgr.Tabs)-1 {
		g.reorderTab(g.tabMgr.ActiveIdx, g.tabMgr.ActiveIdx+1)
	}
}

// handleTabSwitcherInput processes keyboard events while the tab switcher is open.
func (g *Game) handleTabSwitcherInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.overlays.TabSwitcher.Open = false
		g.render.Dirty = true
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	keys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyEnter, ebiten.KeyNumpadEnter,
		ebiten.KeyT,
	}
	for _, key := range keys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		if pressed && !wasPressed {
			switch {
			case meta && shift && key == ebiten.KeyT:
				g.overlays.TabSwitcher.Open = false
			case key == ebiten.KeyArrowUp && !shift:
				if g.overlays.TabSwitcher.Cursor > 0 {
					g.overlays.TabSwitcher.Cursor--
				}
			case key == ebiten.KeyArrowDown && !shift:
				if g.overlays.TabSwitcher.Cursor < len(g.tabMgr.Tabs)-1 {
					g.overlays.TabSwitcher.Cursor++
				}
			case key == ebiten.KeyArrowUp && shift:
				c := g.overlays.TabSwitcher.Cursor
				if c > 0 {
					g.reorderTab(c, c-1)
				}
			case key == ebiten.KeyArrowDown && shift:
				c := g.overlays.TabSwitcher.Cursor
				if c < len(g.tabMgr.Tabs)-1 {
					g.reorderTab(c, c+1)
				}
			case key == ebiten.KeyEnter || key == ebiten.KeyNumpadEnter:
				g.switchTab(g.overlays.TabSwitcher.Cursor)
				g.overlays.TabSwitcher.Open = false
			}
		}
		g.input.PrevKeys[key] = pressed
	}
	g.render.Dirty = true
}

// openTabSearch opens the tab search overlay, closing conflicting surfaces.
func (g *Game) openTabSearch() {
	g.overlays.TabSearch = renderer.TabSearchState{Open: true}
	g.overlays.TabSwitcher = renderer.TabSwitcherState{}
	g.palette.Close()
	g.overlays.Help = renderer.OverlayState{}
	g.closeMenu()
	g.repeats.TabSearch.Reset()
	g.input.PrevKeys[ebiten.KeyArrowUp] = ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	g.input.PrevKeys[ebiten.KeyArrowDown] = ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	g.render.Dirty = true
}

// closeTabSearch closes the tab search overlay.
func (g *Game) closeTabSearch() {
	g.overlays.TabSearch = renderer.TabSearchState{}
	g.repeats.TabSearch.Reset()
	g.render.Dirty = true
}

// handleTabSearchInput processes keyboard input while the tab search overlay is open.
func (g *Game) handleTabSearchInput() {
	now := time.Now()
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	filtered := renderer.FilterTabSearch(g.tabMgr.Tabs, g.tabMgr.Parked, g.overlays.TabSearch.Query)

	upPressed := ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	downPressed := ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	if g.repeats.TabSearch.Update(ebiten.KeyArrowUp, upPressed, g.input.PrevKeys[ebiten.KeyArrowUp], now) && g.overlays.TabSearch.Cursor > 0 {
		g.overlays.TabSearch.Cursor--
	}
	if g.repeats.TabSearch.Update(ebiten.KeyArrowDown, downPressed, g.input.PrevKeys[ebiten.KeyArrowDown], now) && g.overlays.TabSearch.Cursor < len(filtered)-1 {
		g.overlays.TabSearch.Cursor++
	}
	g.input.PrevKeys[ebiten.KeyArrowUp] = upPressed
	g.input.PrevKeys[ebiten.KeyArrowDown] = downPressed

	// ESC: clear query if non-empty, otherwise close.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if g.overlays.TabSearch.Query != "" {
			g.overlays.TabSearch.Query = ""
			g.overlays.TabSearch.Cursor = 0
		} else {
			g.closeTabSearch()
		}
		g.input.PrevKeys[ebiten.KeyEscape] = true
		g.render.Dirty = true
		return
	}

	// Cmd+J toggles off.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyJ) {
		g.closeTabSearch()
		g.input.PrevKeys[ebiten.KeyJ] = true
		return
	}

	// Enter — select the highlighted tab.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		g.input.PrevKeys[key] = pressed
		if pressed && !wasPressed {
			if len(filtered) > 0 && g.overlays.TabSearch.Cursor < len(filtered) {
				entry := filtered[g.overlays.TabSearch.Cursor]
				if entry.Parked {
					g.unparkTab(-(entry.OrigIdx + 1))
				} else {
					g.switchTab(entry.OrigIdx)
				}
				g.closeTabSearch()
			}
			return
		}
	}

	prevQuery := g.overlays.TabSearch.Query
	ti := &TextInput{Text: g.overlays.TabSearch.Query, CursorPos: g.overlays.TabSearch.CursorPos}
	ti.Update(&g.repeats.TabInput, meta, alt)
	g.overlays.TabSearch.Query = ti.Text
	g.overlays.TabSearch.CursorPos = ti.CursorPos
	if g.overlays.TabSearch.Query != prevQuery {
		g.overlays.TabSearch.Cursor = 0
	}

	g.render.Dirty = true
}
