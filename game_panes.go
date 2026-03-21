package main

import (
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/tab"
)

// performSplit splits the focused pane using the given kind and pane constructor.
func (g *Game) performSplit(kind pane.NodeKind, createPane func() (*pane.Pane, error)) {
	g.zoomed = false
	paneRect := g.contentRect()
	newRoot, newPane, err := g.layout.Split(g.focused, kind, createPane)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.setFocus(newPane)
}

func (g *Game) splitH() {
	dir := sanitizeDirectory(g.statusBarState.Cwd)
	g.performSplit(pane.HSplit, func() (*pane.Pane, error) {
		return pane.New(g.cfg, g.focused.Rect, g.font.CellW, g.font.CellH, dir)
	})
}

func (g *Game) splitV() {
	dir := sanitizeDirectory(g.statusBarState.Cwd)
	g.performSplit(pane.VSplit, func() (*pane.Pane, error) {
		return pane.New(g.cfg, g.focused.Rect, g.font.CellW, g.font.CellH, dir)
	})
}

func (g *Game) splitHServer() {
	dir := sanitizeDirectory(g.statusBarState.Cwd)
	g.performSplit(pane.HSplit, func() (*pane.Pane, error) {
		return pane.NewServer(g.cfg, g.focused.Rect, g.font.CellW, g.font.CellH, dir, "")
	})
}

func (g *Game) splitVServer() {
	dir := sanitizeDirectory(g.statusBarState.Cwd)
	g.performSplit(pane.VSplit, func() (*pane.Pane, error) {
		return pane.NewServer(g.cfg, g.focused.Rect, g.font.CellW, g.font.CellH, dir, "")
	})
}

// closePane removes a pane. Focuses the nearest remaining pane.
func (g *Game) closePane(p *pane.Pane) {
	g.zoomed = false
	paneRect := g.contentRect()

	var nextFocus *pane.Pane
	if p == g.focused {
		nextFocus = g.layout.NextLeaf(p)
	}

	newRoot := g.layout.Remove(p)
	if newRoot == nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}

	g.renderer.SetLayoutDirty()
	if nextFocus != nil && nextFocus != p {
		g.setFocus(nextFocus)
	} else if len(g.layout.Leaves()) > 0 {
		g.setFocus(g.layout.Leaves()[0].Pane)
	}
}

// detachPaneToTab extracts the focused pane into a new tab.
// Only works when the current tab has multiple panes.
func (g *Game) detachPaneToTab() {
	leaves := g.layout.Leaves()
	if len(leaves) <= 1 {
		g.flashStatus("Only one pane — nothing to detach")
		return
	}
	g.zoomed = false

	// Remember the pane to detach.
	p := g.focused

	// Focus the next pane before detaching.
	nextFocus := g.layout.NextLeaf(p)

	// Detach from the current layout (does NOT close the terminal).
	newRoot := g.layout.Detach(p)
	if newRoot == nil {
		return
	}
	g.updateLayout(newRoot)
	if nextFocus != nil && nextFocus != p {
		g.setFocusNoHistory(nextFocus)
	} else if len(g.layout.Leaves()) > 0 {
		g.setFocusNoHistory(g.layout.Leaves()[0].Pane)
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
	setPaneHeaders(g.layout, g.font.CellH)
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

	p := g.focused
	srcIdx := g.tabMgr.ActiveIdx
	singlePane := len(g.layout.Leaves()) <= 1

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
		setPaneHeaders(g.layout, g.font.CellH)
		g.recomputeLayout()
		g.setFocus(p)
	} else {
		// Multi-pane tab: detach focused pane and move it.
		nextFocus := g.layout.NextLeaf(p)
		newRoot := g.layout.Detach(p)
		if newRoot == nil {
			return
		}
		g.updateLayout(newRoot)
		if nextFocus != nil && nextFocus != p {
			g.setFocusNoHistory(nextFocus)
		} else if len(g.layout.Leaves()) > 0 {
			g.setFocusNoHistory(g.layout.Leaves()[0].Pane)
		}
		g.recomputeLayout()

		// Attach into target tab.
		targetTab := g.tabMgr.Tabs[targetIdx]
		targetTab.Layout = targetTab.Layout.AttachH(targetTab.Focused, p)
		g.switchTab(targetIdx)
		setPaneHeaders(g.layout, g.font.CellH)
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
	g.focused = p
	g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Focused = p
	g.selDrag.Active = false
	g.hoveredURL = nil
	g.urlMatches = nil
	g.scrollAccum = 0
	g.mouseHeldBtn = -1
	g.lastMouseCol = 0
	g.lastMouseRow = 0
	g.statusBarState.ForegroundProc = ""
	p.Term.RefreshForeground()
	if g.search.State.Open {
		g.search.Close()
		g.screenDirty = true
		if g.focused != nil {
			g.focused.Term.Buf.BumpRenderGen()
		}
	}
	// Force both old and new pane borders to redraw; the pane cache
	// does not track focus state, so clearing it ensures correct borders.
	g.renderer.ClearPaneCache()
	g.screenDirty = true
}

// focusDir moves focus to the nearest pane in direction (dx, dy).
func (g *Game) focusDir(dx, dy int) {
	if p := g.layout.NeighborInDir(g.focused, dx, dy); p != nil {
		g.setFocus(p)
	}
}

// resizePane adjusts the split ratio of the parent split containing the focused pane.
// dx/dy indicate direction: dx=-1 shrinks left, dx=1 grows right, etc.
func (g *Game) resizePane(dx, dy int) {
	if g.zoomed {
		return
	}
	parent, isLeft := g.layout.FindParent(g.focused)
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
	g.screenDirty = true
}

// startRenamePane enters inline rename mode for the focused pane.
func (g *Game) startRenamePane() {
	if g.focused == nil {
		return
	}
	// Cancel any active tab rename first.
	g.cancelRename()
	g.focused.Renaming = true
	g.focused.RenameText = g.focused.CustomName
	g.focused.RenameCursorPos = len([]rune(g.focused.CustomName))
	g.screenDirty = true
}

// commitPaneRename applies the pane rename text.
func (g *Game) commitPaneRename() {
	if g.focused != nil && g.focused.Renaming {
		name := sanitizeTitle(g.focused.RenameText)
		g.focused.CustomName = name
		g.focused.Renaming = false
		g.focused.RenameText = ""
		if g.focused.ServerSessionID != "" {
			g.focused.Term.RenameSession(name)
		}
		g.screenDirty = true
	}
}

// cancelPaneRename exits pane rename mode without applying changes.
func (g *Game) cancelPaneRename() {
	if g.focused != nil && g.focused.Renaming {
		g.focused.Renaming = false
		g.focused.RenameText = ""
		g.screenDirty = true
	}
}

// renamingPane returns true if the focused pane is being renamed.
func (g *Game) renamingPane() bool {
	return g.focused != nil && g.focused.Renaming
}

// toggleZoom fullscreens the focused pane (Cmd+Z). Calling again restores the layout.
func (g *Game) toggleZoom() {
	if g.zoomed {
		g.unzoom()
		return
	}
	fullRect := g.contentRect()

	g.zoomed = true
	g.screenDirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
	p := g.focused
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
	g.recomputeLayoutNode(g.layout)
}

// recomputeLayoutNode recomputes rects and resizes terminals for the given layout.
// Use this instead of recomputeLayout when operating on a layout that isn't g.layout
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

// unzoom restores pane rects to the normal layout. Called by toggleZoom and
// switchTab so the layout is always consistent when leaving zoom state.
func (g *Game) unzoom() {
	g.zoomed = false
	g.screenDirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()

	fullRect := g.contentRect()

	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(fullRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
}
