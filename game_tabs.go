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

// newTab creates a new tab and switches to it.
// The starting directory is controlled by cfg.Tabs.NewTabDir:
//   - "cwd"  → inherit the active tab's current working directory
//   - "home" → always open in $HOME
func (g *Game) newTab() {
	paneRect := g.contentRect()

	var dir string
	switch g.cfg.Tabs.NewTabDir {
	case "home":
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	default: // "cwd"
		dir = g.statusBarState.Cwd
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
	paneRect := g.contentRect()

	var dir string
	switch g.cfg.Tabs.NewTabDir {
	case "home":
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	default: // "cwd"
		dir = g.statusBarState.Cwd
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
		g.layout = nil
		g.focused = nil
		return
	}
	g.renderer.ClearPaneCache()
	g.renderer.SetLayoutDirty()
	g.syncActive()
}

// dismissTabHover clears the tab hover popover state and marks the screen dirty.
func (g *Game) dismissTabHover() {
	if g.tabHoverState.TabIdx >= 0 || g.tabHoverState.Active {
		renderer.DismissTabHover(&g.tabHoverState)
		g.screenDirty = true
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
	if numTabs <= 1 || g.tabMgr.Dragging || g.menuState.Open || g.overlayState.Open ||
		g.confirmState.Open || g.search.State.Open || g.palette.State.Open ||
		g.explorer.State.Open || g.tabSwitcherState.Open || g.tabSearchState.Open {
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
	if hoverIdx != g.tabHoverState.TabIdx {
		g.dismissTabHover()
		g.tabHoverState.TabIdx = hoverIdx
		g.tabHoverState.HoverStart = time.Now()
		return
	}

	// Check if delay has elapsed.
	delay := time.Duration(g.cfg.Tabs.Hover.DelayMs) * time.Millisecond
	if !g.tabHoverState.Active && time.Since(g.tabHoverState.HoverStart) < delay {
		return
	}

	// Activate the popover.
	if !g.tabHoverState.Active {
		g.tabHoverState.Active = true

		// Compute popover position (centered below the hovered tab).
		popW := int(float64(g.cfg.Tabs.Hover.Width) * g.dpi)
		popH := int(float64(g.cfg.Tabs.Hover.Height) * g.dpi)
		tabCenterX := hoverIdx*tabW + tabW/2
		popX := tabCenterX - popW/2
		popY := tabBarH + 4 // small gap below tab bar

		g.tabHoverState.PopoverX = popX
		g.tabHoverState.PopoverY = popY
		g.tabHoverState.PopoverW = popW
		g.tabHoverState.PopoverH = popH
		g.screenDirty = true
	}

	// Check cache validity and regenerate thumbnail if stale.
	hoveredTab := g.tabMgr.Tabs[hoverIdx]
	cacheKey := renderer.TabHoverCacheKey(hoveredTab)
	if cacheKey != g.tabHoverState.CacheKey || g.tabHoverState.Thumbnail == nil {
		if g.tabHoverState.Thumbnail != nil {
			g.tabHoverState.Thumbnail.Deallocate()
		}
		contentRect := g.renderer.ComputeContentRect(hoveredTab)
		g.tabHoverState.Thumbnail = g.renderer.RenderTabThumbnail(hoveredTab, contentRect)
		g.tabHoverState.CacheKey = cacheKey
		g.screenDirty = true
	}
}

// pushFocus records the current focus state before changing it.
func (g *Game) pushFocus() {
	g.tabMgr.PushFocus(g.focused)
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
		if e.tabIdx == g.tabMgr.ActiveIdx && e.pane == g.focused {
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
	g.syncActive()
	g.selDrag.Active = false
	g.statusBarState.ForegroundProc = ""
	g.focused.Term.RefreshForeground()
	if g.search.State.Open {
		g.closeSearchOverlay()
	}
	g.screenDirty = true
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
	g.screenDirty = true
}

// switchToSlot activates the tab pinned to the given home-row slot.
// Does nothing if no tab is pinned there.
func (g *Game) switchToSlot(slot rune) {
	if idx := g.tabMgr.PinnedTab(slot); idx >= 0 {
		g.switchTab(idx)
	}
}

// openTabSwitcher opens (or closes) the tab switcher overlay.
func (g *Game) openTabSwitcher() {
	if g.tabSwitcherState.Open {
		g.tabSwitcherState.Open = false
	} else {
		g.tabSwitcherState.Open = true
		g.tabSwitcherState.Cursor = g.tabMgr.ActiveIdx
	}
	g.screenDirty = true
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
		wasPressed := g.prevKeys[hr.key]
		g.prevKeys[hr.key] = pressed
		if pressed && !wasPressed {
			if shift {
				g.pinTab(hr.slot)
			} else {
				g.switchToSlot(hr.slot)
			}
			g.tabMgr.PinMode = false
			g.screenDirty = true
			return
		}
	}

	// ESC cancels immediately. inpututil catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.tabMgr.PinMode = false
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Any other keypress (Space, Enter) cancels the mode.
	cancelKeys := []ebiten.Key{
		ebiten.KeySpace, ebiten.KeyEnter, ebiten.KeyNumpadEnter,
	}
	for _, key := range cancelKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if pressed && !wasPressed {
			g.tabMgr.PinMode = false
			g.screenDirty = true
			return
		}
	}
	// Cancel on any printable char not in home row.
	if len(ebiten.AppendInputChars(nil)) > 0 {
		g.tabMgr.PinMode = false
		g.screenDirty = true
	}
}

// reorderTab moves the tab at index from to index to, keeping activeTab correct.
func (g *Game) reorderTab(from, to int) {
	g.dismissTabHover()
	g.tabMgr.Reorder(from, to)
	g.tabSwitcherState.Cursor = to
	g.syncActive()
	g.screenDirty = true
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
		g.tabSwitcherState.Open = false
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	keys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyEnter, ebiten.KeyNumpadEnter,
		ebiten.KeyT,
	}
	for _, key := range keys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch {
			case meta && shift && key == ebiten.KeyT:
				g.tabSwitcherState.Open = false
			case key == ebiten.KeyArrowUp && !shift:
				if g.tabSwitcherState.Cursor > 0 {
					g.tabSwitcherState.Cursor--
				}
			case key == ebiten.KeyArrowDown && !shift:
				if g.tabSwitcherState.Cursor < len(g.tabMgr.Tabs)-1 {
					g.tabSwitcherState.Cursor++
				}
			case key == ebiten.KeyArrowUp && shift:
				c := g.tabSwitcherState.Cursor
				if c > 0 {
					g.reorderTab(c, c-1)
				}
			case key == ebiten.KeyArrowDown && shift:
				c := g.tabSwitcherState.Cursor
				if c < len(g.tabMgr.Tabs)-1 {
					g.reorderTab(c, c+1)
				}
			case key == ebiten.KeyEnter || key == ebiten.KeyNumpadEnter:
				g.switchTab(g.tabSwitcherState.Cursor)
				g.tabSwitcherState.Open = false
			}
		}
		g.prevKeys[key] = pressed
	}
	g.screenDirty = true
}

// openTabSearch opens the tab search overlay, closing conflicting surfaces.
func (g *Game) openTabSearch() {
	g.tabSearchState = renderer.TabSearchState{Open: true}
	g.tabSwitcherState = renderer.TabSwitcherState{}
	g.palette.Close()
	g.overlayState = renderer.OverlayState{}
	g.closeMenu()
	g.tabSearchRepeatActive = false
	g.prevKeys[ebiten.KeyArrowUp] = ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	g.prevKeys[ebiten.KeyArrowDown] = ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	g.screenDirty = true
}

// closeTabSearch closes the tab search overlay.
func (g *Game) closeTabSearch() {
	g.tabSearchState = renderer.TabSearchState{}
	g.tabSearchRepeatActive = false
	g.screenDirty = true
}

// updateTabSearchRepeat handles key repeat for arrow keys in the tab search overlay.
func (g *Game) updateTabSearchRepeat(key ebiten.Key, now time.Time) bool {
	pressed := ebiten.IsKeyPressed(key)
	wasPressed := g.prevKeys[key]
	g.prevKeys[key] = pressed

	if !pressed {
		if g.tabSearchRepeatActive && g.tabSearchRepeatKey == key {
			g.tabSearchRepeatActive = false
		}
		return false
	}

	keyRepeatDelay := time.Duration(g.cfg.Keyboard.RepeatDelayMs) * time.Millisecond
	keyRepeatInterval := time.Duration(g.cfg.Keyboard.RepeatIntervalMs) * time.Millisecond

	if !wasPressed {
		g.tabSearchRepeatKey = key
		g.tabSearchRepeatActive = true
		g.tabSearchRepeatStart = now
		g.tabSearchRepeatLast = now
		return true
	}
	if g.tabSearchRepeatActive && g.tabSearchRepeatKey == key &&
		now.Sub(g.tabSearchRepeatStart) >= keyRepeatDelay &&
		now.Sub(g.tabSearchRepeatLast) >= keyRepeatInterval {
		g.tabSearchRepeatLast = now
		return true
	}
	return false
}

// handleTabSearchInput processes keyboard input while the tab search overlay is open.
func (g *Game) handleTabSearchInput() {
	now := time.Now()
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	filtered := renderer.FilterTabSearch(g.tabMgr.Tabs, g.tabSearchState.Query)

	if g.updateTabSearchRepeat(ebiten.KeyArrowUp, now) && g.tabSearchState.Cursor > 0 {
		g.tabSearchState.Cursor--
	}
	if g.updateTabSearchRepeat(ebiten.KeyArrowDown, now) && g.tabSearchState.Cursor < len(filtered)-1 {
		g.tabSearchState.Cursor++
	}

	// ESC: clear query if non-empty, otherwise close.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if g.tabSearchState.Query != "" {
			g.tabSearchState.Query = ""
			g.tabSearchState.Cursor = 0
		} else {
			g.closeTabSearch()
		}
		g.prevKeys[ebiten.KeyEscape] = true
		g.screenDirty = true
		return
	}

	// Cmd+J toggles off.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyJ) {
		g.closeTabSearch()
		g.prevKeys[ebiten.KeyJ] = true
		return
	}

	// Enter — select the highlighted tab.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if pressed && !wasPressed {
			if len(filtered) > 0 && g.tabSearchState.Cursor < len(filtered) {
				g.switchTab(filtered[g.tabSearchState.Cursor].OrigIdx)
				g.closeTabSearch()
			}
			return
		}
	}

	prevQuery := g.tabSearchState.Query
	ti := &TextInput{Text: g.tabSearchState.Query, CursorPos: g.tabSearchState.CursorPos}
	ti.Update(&g.tabSearchInputRepeat, meta, alt)
	g.tabSearchState.Query = ti.Text
	g.tabSearchState.CursorPos = ti.CursorPos
	if g.tabSearchState.Query != prevQuery {
		g.tabSearchState.Cursor = 0
	}

	g.screenDirty = true
}
