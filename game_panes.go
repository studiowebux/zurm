package main

import (
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/tab"
)

// performSplit splits the focused pane using the given kind and pane constructor.
func (g *Game) performSplit(kind pane.NodeKind, createPane func() (*pane.Pane, error)) {
	g.zoomed = false
	paneRect := g.contentRect()
	newRoot, newPane, err := g.activeLayout().Split(g.activeFocused(), kind, createPane)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.activeLayout(), g.font.CellH)
	g.activeLayout().ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.activeLayout().Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.setFocus(newPane)
}

func (g *Game) splitH() {
	dir := sanitizeDirectory(g.status.Bar.Cwd)
	g.performSplit(pane.HSplit, func() (*pane.Pane, error) {
		return pane.New(g.cfg, g.activeFocused().Rect, g.font.CellW, g.font.CellH, dir)
	})
}

func (g *Game) splitV() {
	dir := sanitizeDirectory(g.status.Bar.Cwd)
	g.performSplit(pane.VSplit, func() (*pane.Pane, error) {
		return pane.New(g.cfg, g.activeFocused().Rect, g.font.CellW, g.font.CellH, dir)
	})
}

func (g *Game) splitHServer() {
	dir := sanitizeDirectory(g.status.Bar.Cwd)
	g.performSplit(pane.HSplit, func() (*pane.Pane, error) {
		return pane.NewServer(g.cfg, g.activeFocused().Rect, g.font.CellW, g.font.CellH, dir, "")
	})
}

func (g *Game) splitVServer() {
	dir := sanitizeDirectory(g.status.Bar.Cwd)
	g.performSplit(pane.VSplit, func() (*pane.Pane, error) {
		return pane.NewServer(g.cfg, g.activeFocused().Rect, g.font.CellW, g.font.CellH, dir, "")
	})
}

// closePane removes a pane. Focuses the nearest remaining pane.
func (g *Game) closePane(p *pane.Pane) {
	g.zoomed = false
	paneRect := g.contentRect()

	var nextFocus *pane.Pane
	if p == g.activeFocused() {
		nextFocus = g.activeLayout().NextLeaf(p)
	}

	newRoot := g.activeLayout().Remove(p)
	if newRoot == nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.activeLayout(), g.font.CellH)
	g.activeLayout().ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.activeLayout().Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}

	g.renderer.SetLayoutDirty()
	if nextFocus != nil && nextFocus != p {
		g.setFocus(nextFocus)
	} else if len(g.activeLayout().Leaves()) > 0 {
		g.setFocus(g.activeLayout().Leaves()[0].Pane)
	}
}

// detachPaneToTab extracts the focused pane into a new tab.
// Only works when the current tab has multiple panes.
func (g *Game) detachPaneToTab() {
	leaves := g.activeLayout().Leaves()
	if len(leaves) <= 1 {
		g.flashStatus("Only one pane — nothing to detach")
		return
	}
	g.zoomed = false

	// Remember the pane to detach.
	p := g.activeFocused()

	// Focus the next pane before detaching.
	nextFocus := g.activeLayout().NextLeaf(p)

	// Detach from the current layout (does NOT close the terminal).
	newRoot := g.activeLayout().Detach(p)
	if newRoot == nil {
		return
	}
	g.updateLayout(newRoot)
	if nextFocus != nil && nextFocus != p {
		g.setFocusNoHistory(nextFocus)
	} else if len(g.activeLayout().Leaves()) > 0 {
		g.setFocusNoHistory(g.activeLayout().Leaves()[0].Pane)
	}
	g.recomputeLayout()

	// Create a new tab with the detached pane.
	newTab := &tab.Tab{
		Layout:  pane.NewLeaf(p),
		Focused: p,
		Title:   p.CustomName,
	}
	// Insert the new tab right after the active tab.
	insertIdx := g.tabMgr.ActiveIdx + 1
	g.tabMgr.Tabs = append(g.tabMgr.Tabs, nil)
	copy(g.tabMgr.Tabs[insertIdx+1:], g.tabMgr.Tabs[insertIdx:])
	g.tabMgr.Tabs[insertIdx] = newTab
	g.switchTab(insertIdx)
	setPaneHeaders(g.activeLayout(), g.font.CellH)
	g.recomputeLayout()
}

// mergePaneToTab moves the focused pane into the target tab as a horizontal split.
// If the current tab becomes empty, it is removed.
func (g *Game) mergePaneToTab(targetIdx int) {
	g.dismissTabHover()
	if targetIdx < 0 || targetIdx >= len(g.tabMgr.Tabs) || targetIdx == g.tabMgr.ActiveIdx {
		return
	}
	g.zoomed = false

	p := g.activeFocused()
	srcIdx := g.tabMgr.ActiveIdx
	singlePane := len(g.activeLayout().Leaves()) <= 1

	if singlePane {
		// Single-pane tab: move the whole tab's pane into the target.
		// Detach the pane from its layout.
		targetTab := g.tabMgr.Tabs[targetIdx]
		targetTab.Layout = targetTab.Layout.AttachH(targetTab.Focused, p)

		// Remove the source tab (don't close the terminal).
		g.tabMgr.Tabs = append(g.tabMgr.Tabs[:srcIdx], g.tabMgr.Tabs[srcIdx+1:]...)
		if len(g.tabMgr.Tabs) == 0 {
			return
		}
		// Adjust target index if source was before it.
		if srcIdx < targetIdx {
			targetIdx--
		}
		// Clamp activeTab to prevent out-of-bounds access inside switchTabNoHistory.
		if g.tabMgr.ActiveIdx >= len(g.tabMgr.Tabs) {
			g.tabMgr.ActiveIdx = len(g.tabMgr.Tabs) - 1
		}
		g.switchTabNoHistory(targetIdx)
		// Recompute the target tab's layout.
		setPaneHeaders(g.activeLayout(), g.font.CellH)
		g.recomputeLayout()
		g.setFocus(p)
	} else {
		// Multi-pane tab: detach focused pane and move it.
		nextFocus := g.activeLayout().NextLeaf(p)
		newRoot := g.activeLayout().Detach(p)
		if newRoot == nil {
			return
		}
		g.updateLayout(newRoot)
		if nextFocus != nil && nextFocus != p {
			g.setFocusNoHistory(nextFocus)
		} else if len(g.activeLayout().Leaves()) > 0 {
			g.setFocusNoHistory(g.activeLayout().Leaves()[0].Pane)
		}
		g.recomputeLayout()

		// Attach into target tab.
		targetTab := g.tabMgr.Tabs[targetIdx]
		targetTab.Layout = targetTab.Layout.AttachH(targetTab.Focused, p)
		g.switchTab(targetIdx)
		setPaneHeaders(g.activeLayout(), g.font.CellH)
		g.recomputeLayout()
		g.setFocus(p)
	}
	g.flashStatus("Pane moved to tab")
}

// mergePaneToNextTab moves the focused pane into the next tab.
func (g *Game) mergePaneToNextTab() {
	target := (g.tabMgr.ActiveIdx + 1) % len(g.tabMgr.Tabs)
	if target == g.tabMgr.ActiveIdx {
		g.flashStatus("Only one tab")
		return
	}
	g.mergePaneToTab(target)
}

// mergePaneToPrevTab moves the focused pane into the previous tab.
func (g *Game) mergePaneToPrevTab() {
	target := (g.tabMgr.ActiveIdx - 1 + len(g.tabMgr.Tabs)) % len(g.tabMgr.Tabs)
	if target == g.tabMgr.ActiveIdx {
		g.flashStatus("Only one tab")
		return
	}
	g.mergePaneToTab(target)
}

// setFocus updates the focused pane in both Game and the active tab.
func (g *Game) setFocus(p *pane.Pane) {
	g.pushFocus()
	g.setFocusNoHistory(p)
}

// setFocusNoHistory sets focus without recording history.
// Used by goBack to avoid polluting the stack.
func (g *Game) setFocusNoHistory(p *pane.Pane) {
	g.setActiveFocused(p)
	g.input.SelDrag.Active = false
	g.urlHover.HoveredURL = nil
	g.urlHover.Matches = nil
	g.input.ScrollAccum = 0
	g.input.MouseHeldBtn = -1
	g.input.LastMouseCol = 0
	g.input.LastMouseRow = 0
	g.status.Bar.ForegroundProc = ""
	p.Term.RefreshForeground(g.ctx)
	if g.search.State.Open {
		g.closeSearchOverlay()
	}
	// Force both old and new pane borders to redraw; the pane cache
	// does not track focus state, so clearing it ensures correct borders.
	g.renderer.ClearPaneCache()
	g.render.Dirty = true
}

// focusDir moves focus to the nearest pane in direction (dx, dy).
func (g *Game) focusDir(dx, dy int) {
	layout := g.activeLayout()
	focused := g.activeFocused()
	if layout == nil || focused == nil {
		return
	}
	if p := layout.NeighborInDir(focused, dx, dy); p != nil {
		g.setFocus(p)
	}
}

// resizePane adjusts the split ratio of the parent split containing the focused pane.
// dx/dy indicate direction: dx=-1 shrinks left, dx=1 grows right, etc.
func (g *Game) resizePane(dx, dy int) {
	if g.zoomed {
		return
	}
	layout := g.activeLayout()
	focused := g.activeFocused()
	if layout == nil || focused == nil {
		return
	}
	parent, isLeft := layout.FindParent(focused)
	if parent == nil {
		return
	}
	step := 0.05
	switch parent.Kind {
	case pane.HSplit:
		if dx != 0 {
			delta := step * float64(dx)
			if !isLeft {
				delta = -delta
			}
			parent.Ratio += delta
		}
	case pane.VSplit:
		if dy != 0 {
			delta := step * float64(dy)
			if !isLeft {
				delta = -delta
			}
			parent.Ratio += delta
		}
	}
	if parent.Ratio < 0.1 {
		parent.Ratio = 0.1
	} else if parent.Ratio > 0.9 {
		parent.Ratio = 0.9
	}
	g.recomputeLayout()
	g.render.Dirty = true
}

// startRenamePane enters inline rename mode for the focused pane.
func (g *Game) startRenamePane() {
	if g.activeFocused() == nil {
		return
	}
	// Cancel any active tab rename first.
	g.cancelRename()
	g.activeFocused().Renaming = true
	g.activeFocused().RenameText = g.activeFocused().CustomName
	g.activeFocused().RenameCursorPos = len([]rune(g.activeFocused().CustomName))
	g.render.Dirty = true
}

// commitPaneRename applies the pane rename text.
func (g *Game) commitPaneRename() {
	if g.activeFocused() != nil && g.activeFocused().Renaming {
		name := sanitizeTitle(g.activeFocused().RenameText)
		g.activeFocused().CustomName = name
		g.activeFocused().Renaming = false
		g.activeFocused().RenameText = ""
		if g.activeFocused().ServerSessionID != "" {
			g.activeFocused().Term.RenameSession(name)
		}
		g.render.Dirty = true
	}
}

// cancelPaneRename exits pane rename mode without applying changes.
func (g *Game) cancelPaneRename() {
	if g.activeFocused() != nil && g.activeFocused().Renaming {
		g.activeFocused().Renaming = false
		g.activeFocused().RenameText = ""
		g.render.Dirty = true
	}
}

// renamingPane returns true if the focused pane is being renamed.
func (g *Game) renamingPane() bool {
	return g.activeFocused() != nil && g.activeFocused().Renaming
}

// toggleZoom fullscreens the focused pane (Cmd+Z). Calling again restores the layout.
func (g *Game) toggleZoom() {
	if g.zoomed {
		g.unzoom()
		return
	}
	fullRect := g.contentRect()

	g.zoomed = true
	g.render.Dirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
	p := g.activeFocused()
	p.HeaderH = 0 // zoomed pane has no header — only one pane visible
	p.Rect = fullRect
	cols := (fullRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW
	rows := (fullRect.Dy() - g.cfg.Window.Padding) / g.font.CellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	p.Cols = cols
	p.Rows = rows
	p.Term.Resize(cols, rows)
}

// recomputeLayout recalculates rects and resizes all pane terminals.
// Call after any layout mutation (split ratio change, split/close, zoom toggle).
func (g *Game) recomputeLayout() {
	g.recomputeLayoutNode(g.activeLayout())
}

// recomputeLayoutNode recomputes rects and resizes terminals for the given layout.
// Use this instead of recomputeLayout when operating on a different tab's layout
// (e.g. iterating over all tabs during font size change or config reload).
func (g *Game) recomputeLayoutNode(n *pane.LayoutNode) {
	g.dismissTabHover()
	fullRect := g.contentRect()

	setPaneHeaders(n, g.font.CellH)
	n.ComputeRects(fullRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range n.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
}

// setPaneHeaders sets HeaderH on every leaf pane in the layout. When there are
// multiple panes, each gets a header bar; single-pane layouts get no header.
// Must be called before ComputeRects so the row calculation accounts for header space.
func setPaneHeaders(layout *pane.LayoutNode, cellH int) {
	leaves := layout.Leaves()
	headerH := 0
	if len(leaves) > 1 {
		headerH = cellH + paneHeaderPadding
	}
	for _, leaf := range leaves {
		leaf.Pane.HeaderH = headerH
	}
}

// reapplyZoom re-applies the zoom geometry to the active focused pane.
// Must be called after any operation that recomputes layout rects (font change,
// config reload, resize) while g.zoomed is true, so the zoomed pane keeps the
// full content rect instead of reverting to its normal split rect.
func (g *Game) reapplyZoom() {
	if !g.zoomed {
		return
	}
	p := g.activeFocused()
	if p == nil {
		return
	}
	fullRect := g.contentRect()
	p.HeaderH = 0
	p.Rect = fullRect
	cols := (fullRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW
	rows := (fullRect.Dy() - g.cfg.Window.Padding) / g.font.CellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	p.Cols = cols
	p.Rows = rows
	p.Term.Resize(cols, rows)
}

// unzoom restores pane rects to the normal layout. Called by toggleZoom and
// switchTab so the layout is always consistent when leaving zoom state.
func (g *Game) unzoom() {
	g.zoomed = false
	g.render.Dirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()

	fullRect := g.contentRect()

	setPaneHeaders(g.activeLayout(), g.font.CellH)
	g.activeLayout().ComputeRects(fullRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.activeLayout().Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
}
