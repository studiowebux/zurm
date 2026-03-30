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

func TestPark_RefusesLastTab(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 0

	ok := tm.Park(0)
	if ok {
		t.Error("Park should return false when only one visible tab remains")
	}
	if len(tm.Tabs) != 1 {
		t.Errorf("Tabs len = %d after refused park, want 1", len(tm.Tabs))
	}
	if len(tm.Parked) != 0 {
		t.Errorf("Parked len = %d after refused park, want 0", len(tm.Parked))
	}
}

func TestPark_MovesTabToParked(t *testing.T) {
	tm := NewTabManager()
	t1 := &tab.Tab{}
	t2 := &tab.Tab{}
	tm.Add(t1)
	tm.Add(t2)
	tm.ActiveIdx = 0

	ok := tm.Park(1)
	if !ok {
		t.Error("Park should return true")
	}
	if len(tm.Tabs) != 1 {
		t.Errorf("Tabs len = %d, want 1", len(tm.Tabs))
	}
	if len(tm.Parked) != 1 {
		t.Errorf("Parked len = %d, want 1", len(tm.Parked))
	}
	if tm.Parked[0] != t2 {
		t.Error("Parked[0] should be t2")
	}
}

func TestPark_AdjustsActiveIdx(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{})
	tm.ActiveIdx = 2

	tm.Park(2)
	if tm.ActiveIdx != 1 {
		t.Errorf("ActiveIdx = %d after parking last tab, want 1", tm.ActiveIdx)
	}
}

func TestUnpark_MovesTabToVisible(t *testing.T) {
	tm := NewTabManager()
	t1 := &tab.Tab{}
	t2 := &tab.Tab{}
	tm.Add(t1)
	tm.Add(t2)
	tm.ActiveIdx = 0
	tm.Park(1)

	tm.Unpark(0)
	if len(tm.Tabs) != 2 {
		t.Errorf("Tabs len = %d after unpark, want 2", len(tm.Tabs))
	}
	if len(tm.Parked) != 0 {
		t.Errorf("Parked len = %d after unpark, want 0", len(tm.Parked))
	}
	if tm.Tabs[1] != t2 {
		t.Error("Unparked tab should be appended at end of Tabs")
	}
}

func TestUnpark_OutOfRange(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	// Should not panic.
	tm.Unpark(-1)
	tm.Unpark(0)
}

func TestPinnedParkedTab_Found(t *testing.T) {
	tm := NewTabManager()
	t1 := &tab.Tab{}
	t2 := &tab.Tab{PinnedSlot: 'a'}
	tm.Add(t1)
	tm.Add(t2)
	tm.Park(1)

	idx := tm.PinnedParkedTab('a')
	if idx != 0 {
		t.Errorf("PinnedParkedTab('a') = %d, want 0", idx)
	}
}

func TestPinnedParkedTab_NotFound(t *testing.T) {
	tm := NewTabManager()
	tm.Add(&tab.Tab{})
	tm.Add(&tab.Tab{PinnedSlot: 'b'})
	tm.Park(1)

	idx := tm.PinnedParkedTab('a')
	if idx != -1 {
		t.Errorf("PinnedParkedTab('a') = %d, want -1", idx)
	}
}
