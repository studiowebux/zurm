package main

import (
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/tab"
)

// TabManager owns the tab slice, active index, focus history, drag state,
// and pin mode. Game holds a *TabManager and delegates tab operations to it.
type TabManager struct {
	Tabs     []*tab.Tab
	ActiveIdx int

	// Focus history for Cmd+` navigation.
	FocusHistory []focusEntry

	// Tab drag state (mouse reorder).
	Dragging    bool
	DragFromIdx int
	DragStartX  int

	// PinMode is true after Cmd+Space, waiting for a home-row slot keypress.
	PinMode bool
}

// NewTabManager creates a ready-to-use tab manager.
func NewTabManager() *TabManager {
	return &TabManager{}
}

// Count returns the number of open tabs.
func (tm *TabManager) Count() int {
	return len(tm.Tabs)
}

// Active returns the currently active tab, or nil if no tabs exist.
func (tm *TabManager) Active() *tab.Tab {
	if tm.ActiveIdx >= 0 && tm.ActiveIdx < len(tm.Tabs) {
		return tm.Tabs[tm.ActiveIdx]
	}
	return nil
}

// Add appends a new tab.
func (tm *TabManager) Add(t *tab.Tab) {
	tm.Tabs = append(tm.Tabs, t)
}

// Remove removes the tab at index i, adjusts ActiveIdx, and returns
// whether any tabs remain.
func (tm *TabManager) Remove(i int) bool {
	old := tm.Tabs
	tm.Tabs = append(tm.Tabs[:i], tm.Tabs[i+1:]...)
	old[len(old)-1] = nil // zero trailing slot to release Tab for GC
	if len(tm.Tabs) == 0 {
		return false
	}
	if tm.ActiveIdx >= len(tm.Tabs) {
		tm.ActiveIdx = len(tm.Tabs) - 1
	}
	return true
}

// Reorder moves the tab at index from to index to, keeping ActiveIdx correct.
func (tm *TabManager) Reorder(from, to int) {
	n := len(tm.Tabs)
	if from == to || from < 0 || to < 0 || from >= n || to >= n {
		return
	}

	t := tm.Tabs[from]

	// Build new slice without the tab at from, then insert at to.
	without := make([]*tab.Tab, 0, n-1)
	without = append(without, tm.Tabs[:from]...)
	without = append(without, tm.Tabs[from+1:]...)

	result := make([]*tab.Tab, 0, n)
	result = append(result, without[:to]...)
	result = append(result, t)
	result = append(result, without[to:]...)
	tm.Tabs = result

	// Keep ActiveIdx pointing at the same tab after the move.
	if tm.ActiveIdx == from {
		tm.ActiveIdx = to
	} else if from < to && tm.ActiveIdx > from && tm.ActiveIdx <= to {
		tm.ActiveIdx--
	} else if from > to && tm.ActiveIdx < from && tm.ActiveIdx >= to {
		tm.ActiveIdx++
	}
}

// PinnedTab returns the index of the tab pinned to the given slot rune, or -1.
func (tm *TabManager) PinnedTab(slot rune) int {
	for i, t := range tm.Tabs {
		if t.PinnedSlot == slot {
			return i
		}
	}
	return -1
}

// PinActive pins the active tab to the given home-row slot, evicting any
// previous occupant. Calling again with the same slot while already pinned
// there unpins (toggle off). Returns true if state changed.
func (tm *TabManager) PinActive(slot rune) {
	active := tm.Tabs[tm.ActiveIdx]
	if active.PinnedSlot == slot {
		active.PinnedSlot = 0 // toggle off
		return
	}
	// Evict any tab currently holding this slot.
	for _, t := range tm.Tabs {
		if t.PinnedSlot == slot {
			t.PinnedSlot = 0
		}
	}
	active.PinnedSlot = slot
}

// NextIdx returns the index of the next tab, wrapping around.
func (tm *TabManager) NextIdx() int {
	return (tm.ActiveIdx + 1) % len(tm.Tabs)
}

// PrevIdx returns the index of the previous tab, wrapping around.
func (tm *TabManager) PrevIdx() int {
	return (tm.ActiveIdx - 1 + len(tm.Tabs)) % len(tm.Tabs)
}

// PushFocus records the current focus state before changing it.
func (tm *TabManager) PushFocus(focused *pane.Pane) {
	if len(tm.Tabs) == 0 || focused == nil {
		return
	}
	e := focusEntry{tabIdx: tm.ActiveIdx, pane: focused}
	// Deduplicate: skip if top of stack is the same location.
	if n := len(tm.FocusHistory); n > 0 && tm.FocusHistory[n-1] == e {
		return
	}
	tm.FocusHistory = append(tm.FocusHistory, e)
	if len(tm.FocusHistory) > 50 {
		tm.FocusHistory = tm.FocusHistory[1:]
	}
}

// PopFocus pops the top of the focus history stack.
// Returns the entry and true, or a zero entry and false if empty.
func (tm *TabManager) PopFocus() (focusEntry, bool) {
	if len(tm.FocusHistory) == 0 {
		return focusEntry{}, false
	}
	e := tm.FocusHistory[len(tm.FocusHistory)-1]
	tm.FocusHistory = tm.FocusHistory[:len(tm.FocusHistory)-1]
	return e, true
}
