package main

import (
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/pane"
)

// inputTracker groups all input edge-detection, drag, click, scroll, and mouse state.
type inputTracker struct {
	PrevKeys         map[ebiten.Key]bool
	PrevMouseButtons map[ebiten.MouseButton]bool
	PrevMX, PrevMY   int

	PtyRepeat KeyRepeatHandler
	RepeatSeq []byte // exact bytes to resend on PTY repeat

	SelDrag SelectionDragger
	DivDrag DividerDragHandler

	LastMouseCol int // last col sent to PTY (1-based)
	LastMouseRow int // last row sent to PTY (1-based)
	MouseHeldBtn int // -1 = none, 0=left, 1=mid, 2=right

	ScrollAccum float64 // fractional trackpad wheel accumulation

	LastClickTime time.Time
	LastClickRow  int
	LastClickCol  int
	ClickCount    int
}

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
