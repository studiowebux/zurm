package main

import "github.com/studiowebux/zurm/pane"

// SelectionDragger tracks whether a text selection drag is in progress.
// The actual selection coordinates live on terminal.ScreenBuffer.Selection.
type SelectionDragger struct {
	Active bool
}

// DividerDragHandler tracks pane divider resize dragging.
type DividerDragHandler struct {
	Active bool
	Split  *pane.LayoutNode
}

// Start begins a divider drag on the given split node.
func (dh *DividerDragHandler) Start(split *pane.LayoutNode) {
	dh.Active = true
	dh.Split = split
}

// Update adjusts the split ratio based on cursor position.
// Returns true if the ratio changed (screen needs redraw).
func (dh *DividerDragHandler) Update(mx, my int) bool {
	if dh.Split == nil {
		return false
	}
	switch dh.Split.Kind {
	case pane.HSplit:
		dx := dh.Split.Rect.Dx()
		if dx == 0 {
			return false
		}
		newRatio := float64(mx-dh.Split.Rect.Min.X) / float64(dx)
		if newRatio < 0.1 {
			newRatio = 0.1
		} else if newRatio > 0.9 {
			newRatio = 0.9
		}
		dh.Split.Ratio = newRatio
	case pane.VSplit:
		dy := dh.Split.Rect.Dy()
		if dy == 0 {
			return false
		}
		newRatio := float64(my-dh.Split.Rect.Min.Y) / float64(dy)
		if newRatio < 0.1 {
			newRatio = 0.1
		} else if newRatio > 0.9 {
			newRatio = 0.9
		}
		dh.Split.Ratio = newRatio
	}
	return true
}

// End stops the divider drag.
func (dh *DividerDragHandler) End() {
	dh.Active = false
	dh.Split = nil
}
