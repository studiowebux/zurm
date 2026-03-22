package main

import (
	"testing"

	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/tab"
)

func TestNextIdx_EmptyTabs(t *testing.T) {
	tm := NewTabManager()
	got := tm.NextIdx()
	if got != 0 {
		t.Errorf("NextIdx() on empty tabs = %d, want 0", got)
	}
}

func TestPrevIdx_EmptyTabs(t *testing.T) {
	tm := NewTabManager()
	got := tm.PrevIdx()
	if got != 0 {
		t.Errorf("PrevIdx() on empty tabs = %d, want 0", got)
	}
}

func TestNextIdx_Wraps(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 2

	got := tm.NextIdx()
	if got != 0 {
		t.Errorf("NextIdx() at last = %d, want 0", got)
	}
}

func TestPrevIdx_Wraps(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 0

	got := tm.PrevIdx()
	if got != 1 {
		t.Errorf("PrevIdx() at first = %d, want 1", got)
	}
}

func TestPinActive_EmptyTabs(t *testing.T) {
	tm := NewTabManager()
	// Should not panic.
	tm.PinActive('a')
}

func TestPinActive_ToggleOff(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 0

	tm.PinActive('a')
	if tm.Tabs[0].PinnedSlot != 'a' {
		t.Errorf("PinnedSlot = %c, want 'a'", tm.Tabs[0].PinnedSlot)
	}

	// Toggle off.
	tm.PinActive('a')
	if tm.Tabs[0].PinnedSlot != 0 {
		t.Errorf("PinnedSlot = %c after toggle, want 0", tm.Tabs[0].PinnedSlot)
	}
}

func TestPinActive_EvictsPrevious(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})

	tm.ActiveIdx = 0
	tm.PinActive('a')

	tm.ActiveIdx = 1
	tm.PinActive('a')

	if tm.Tabs[0].PinnedSlot != 0 {
		t.Errorf("tab 0 should have been evicted, PinnedSlot = %c", tm.Tabs[0].PinnedSlot)
	}
	if tm.Tabs[1].PinnedSlot != 'a' {
		t.Errorf("tab 1 PinnedSlot = %c, want 'a'", tm.Tabs[1].PinnedSlot)
	}
}

func TestRemove_AdjustsActiveIdx(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 2

	remaining := tm.Remove(1)
	if !remaining {
		t.Error("Remove should return true when tabs remain")
	}
	if tm.ActiveIdx != 1 {
		t.Errorf("ActiveIdx = %d after removing tab 1, want 1", tm.ActiveIdx)
	}
	if len(tm.Tabs) != 2 {
		t.Errorf("len(Tabs) = %d, want 2", len(tm.Tabs))
	}
}

func TestRemove_LastTab(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 0

	remaining := tm.Remove(0)
	if remaining {
		t.Error("Remove should return false when no tabs remain")
	}
}

func TestActive_NilOnEmpty(t *testing.T) {
	tm := NewTabManager()
	if tm.Active() != nil {
		t.Error("Active() should return nil on empty TabManager")
	}
}

func TestReorder_SameIndex(t *testing.T) {
	tm := NewTabManager()
	t1 := &tab.Tab{}
	t2 := &tab.Tab{}
	tm.Add(t1)
	tm.Add(t2)
	tm.ActiveIdx = 0

	tm.Reorder(0, 0)
	if tm.Tabs[0] != t1 || tm.Tabs[1] != t2 {
		t.Error("Reorder(0, 0) should be a no-op")
	}
}

func TestReorder_MoveForward(t *testing.T) {
	tm := NewTabManager()
	t1 := &tab.Tab{}
	t2 := &tab.Tab{}
	t3 := &tab.Tab{}
	tm.Add(t1)
	tm.Add(t2)
	tm.Add(t3)
	tm.ActiveIdx = 0

	tm.Reorder(0, 2)
	if tm.Tabs[0] != t2 || tm.Tabs[1] != t3 || tm.Tabs[2] != t1 {
		t.Error("Reorder(0, 2) produced wrong order")
	}
	if tm.ActiveIdx != 2 {
		t.Errorf("ActiveIdx = %d after moving active tab 0→2, want 2", tm.ActiveIdx)
	}
}

func TestPushFocus_Dedup(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 0
	p := &pane.Pane{}

	tm.PushFocus(p)
	tm.PushFocus(p) // duplicate — should be skipped

	if len(tm.FocusHistory) != 1 {
		t.Errorf("FocusHistory len = %d, want 1 (dedup)", len(tm.FocusHistory))
	}
}

func TestPopFocus_Empty(t *testing.T) {
	tm := NewTabManager()
	_, ok := tm.PopFocus()
	if ok {
		t.Error("PopFocus should return false on empty history")
	}
}
