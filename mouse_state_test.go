package main

import (
	"image"
	"testing"

	"github.com/studiowebux/zurm/pane"
)

func TestDividerDragHandler_NilSplit(t *testing.T) {
	dh := DividerDragHandler{}
	got := dh.Update(100, 100)
	if got {
		t.Error("Update with nil split should return false")
	}
}

func TestDividerDragHandler_ZeroWidthRect(t *testing.T) {
	node := &pane.LayoutNode{
		Kind: pane.HSplit,
		Rect: image.Rect(50, 0, 50, 100), // zero width
	}
	dh := DividerDragHandler{Active: true, Split: node}
	got := dh.Update(50, 50)
	if got {
		t.Error("Update with zero-width rect should return false")
	}
}

func TestDividerDragHandler_ZeroHeightRect(t *testing.T) {
	node := &pane.LayoutNode{
		Kind: pane.VSplit,
		Rect: image.Rect(0, 50, 100, 50), // zero height
	}
	dh := DividerDragHandler{Active: true, Split: node}
	got := dh.Update(50, 50)
	if got {
		t.Error("Update with zero-height rect should return false")
	}
}

func TestDividerDragHandler_HSplit(t *testing.T) {
	node := &pane.LayoutNode{
		Kind:  pane.HSplit,
		Rect:  image.Rect(0, 0, 200, 100),
		Ratio: 0.5,
	}
	dh := DividerDragHandler{Active: true, Split: node}

	changed := dh.Update(100, 50)
	if !changed {
		t.Error("Update should return true when ratio changes")
	}
	if node.Ratio != 0.5 {
		t.Errorf("Ratio = %f, want 0.5 (mouse at midpoint)", node.Ratio)
	}

	// Drag far left — should clamp to 0.1.
	dh.Update(0, 50)
	if node.Ratio != 0.1 {
		t.Errorf("Ratio = %f, want 0.1 (clamped)", node.Ratio)
	}

	// Drag far right — should clamp to 0.9.
	dh.Update(200, 50)
	if node.Ratio != 0.9 {
		t.Errorf("Ratio = %f, want 0.9 (clamped)", node.Ratio)
	}
}

func TestDividerDragHandler_VSplit(t *testing.T) {
	node := &pane.LayoutNode{
		Kind:  pane.VSplit,
		Rect:  image.Rect(0, 0, 100, 200),
		Ratio: 0.5,
	}
	dh := DividerDragHandler{Active: true, Split: node}

	dh.Update(50, 100)
	if node.Ratio != 0.5 {
		t.Errorf("Ratio = %f, want 0.5", node.Ratio)
	}

	dh.Update(50, 0)
	if node.Ratio != 0.1 {
		t.Errorf("Ratio = %f, want 0.1 (clamped)", node.Ratio)
	}
}

func TestDividerDragHandler_StartEnd(t *testing.T) {
	node := &pane.LayoutNode{Kind: pane.HSplit}
	dh := DividerDragHandler{}

	dh.Start(node)
	if !dh.Active || dh.Split != node {
		t.Error("Start should set Active=true and Split")
	}

	dh.End()
	if dh.Active || dh.Split != nil {
		t.Error("End should set Active=false and Split=nil")
	}
}
