package main

import (
	"testing"

	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/tab"
)

// assertSyncInvariant verifies that activeLayout()/activeFocused() return
// consistent values from the active tab.
func assertSyncInvariant(t *testing.T, g *Game, label string) {
	t.Helper()
	if g.tabMgr.ActiveIdx < 0 || g.tabMgr.ActiveIdx >= len(g.tabMgr.Tabs) {
		if g.activeLayout() != nil {
			t.Errorf("%s: no active tab but activeLayout() != nil", label)
		}
		if g.activeFocused() != nil {
			t.Errorf("%s: no active tab but activeFocused() != nil", label)
		}
		return
	}
	active := g.tabMgr.Tabs[g.tabMgr.ActiveIdx]
	if g.activeLayout() != active.Layout {
		t.Errorf("%s: layout mismatch — activeLayout()=%p, active.Layout=%p", label, g.activeLayout(), active.Layout)
	}
	if g.activeFocused() != active.Focused {
		t.Errorf("%s: focused mismatch — activeFocused()=%p, active.Focused=%p", label, g.activeFocused(), active.Focused)
	}
}

// makeTestTab creates a minimal tab with a leaf layout and pane (no real PTY).
func makeTestTab(name string) *tab.Tab {
	p := &pane.Pane{CustomName: name}
	layout := pane.NewLeaf(p)
	return &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   name,
	}
}

// makeTestGame creates a minimal Game with the given tabs for testing sync operations.
func makeTestGame(tabs ...*tab.Tab) *Game {
	tm := NewTabManager()
	for _, t := range tabs {
		tm.Add(t)
	}
	return &Game{tabMgr: tm}
}

// --- syncActive ---

func TestSyncActive_LoadsFromActiveTab(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	g := makeTestGame(t1, t2)

	g.tabMgr.ActiveIdx = 1
	g.syncActive()
	assertSyncInvariant(t, g, "after switch to tab 1")
}

func TestSyncActive_FirstTab(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	g := makeTestGame(t1, t2)

	g.tabMgr.ActiveIdx = 0
	g.syncActive()
	assertSyncInvariant(t, g, "after switch to tab 0")
}

func TestSyncActive_OutOfBounds(t *testing.T) {
	t1 := makeTestTab("tab1")
	g := makeTestGame(t1)

	// Set active index past the end — syncActive should not crash
	g.tabMgr.ActiveIdx = 5
	g.syncActive()
	// layout/focused should still be from the initial setup (stale but not nil-panicked)
}

func TestSyncActive_AfterRemoveMiddle(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	t3 := makeTestTab("tab3")
	g := makeTestGame(t1, t2, t3)

	g.tabMgr.ActiveIdx = 2
	g.syncActive()
	assertSyncInvariant(t, g, "initial active=2")

	// Remove middle tab (index 1)
	g.tabMgr.Remove(1)
	// ActiveIdx should be clamped to 1 (was 2, now only 2 tabs)
	g.syncActive()
	assertSyncInvariant(t, g, "after remove middle")

	if g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Title != "tab3" {
		t.Errorf("expected tab3 to be active, got %q", g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Title)
	}
}

func TestSyncActive_AfterRemoveActive(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	t3 := makeTestTab("tab3")
	g := makeTestGame(t1, t2, t3)

	// Active is tab 1, remove it
	g.tabMgr.ActiveIdx = 1
	g.tabMgr.Remove(1)
	g.syncActive()
	assertSyncInvariant(t, g, "after remove active")

	// Active should now be tab3 (index 1 after removal)
	if g.tabMgr.ActiveIdx != 1 {
		t.Errorf("ActiveIdx = %d, want 1", g.tabMgr.ActiveIdx)
	}
}

func TestSyncActive_AfterRemoveLastTab(t *testing.T) {
	t1 := makeTestTab("tab1")
	g := makeTestGame(t1)

	g.tabMgr.ActiveIdx = 0
	remaining := g.tabMgr.Remove(0)
	if remaining {
		t.Error("should return false when no tabs remain")
	}
	// After removing the last tab, the app exits — layout/focused become stale
	// The important thing is that TabManager.Remove returns false
}

// --- updateLayout ---

func TestUpdateLayout_SyncsTab(t *testing.T) {
	t1 := makeTestTab("tab1")
	g := makeTestGame(t1)

	// Create a new layout
	newPane := &pane.Pane{CustomName: "new"}
	newLayout := pane.NewLeaf(newPane)

	g.updateLayout(newLayout)

	if g.activeLayout() != newLayout {
		t.Error("activeLayout() should return new layout")
	}
	if g.tabMgr.Tabs[0].Layout != newLayout {
		t.Error("tab.Layout should point to new layout")
	}
}

func TestUpdateLayout_NoTabsSafe(t *testing.T) {
	g := &Game{tabMgr: NewTabManager()}
	newLayout := pane.NewLeaf(&pane.Pane{})

	// Should not panic with empty tabs
	g.updateLayout(newLayout)
	// No active tab — activeLayout() returns nil regardless
	if g.activeLayout() != nil {
		t.Error("activeLayout() should be nil with no tabs")
	}
}

// --- Reorder + syncActive ---

func TestReorder_SyncInvariantPreserved(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	t3 := makeTestTab("tab3")
	g := makeTestGame(t1, t2, t3)

	// Active is tab1 (index 0), reorder 0 → 2
	g.tabMgr.ActiveIdx = 0
	g.syncActive()
	assertSyncInvariant(t, g, "before reorder")

	g.tabMgr.Reorder(0, 2)
	g.syncActive()
	assertSyncInvariant(t, g, "after reorder 0→2")

	// tab1 should now be at index 2
	if g.tabMgr.Tabs[2] != t1 {
		t.Error("tab1 should be at index 2 after reorder")
	}
	if g.tabMgr.ActiveIdx != 2 {
		t.Errorf("ActiveIdx = %d, want 2", g.tabMgr.ActiveIdx)
	}
}

func TestReorder_NonActiveTabMoved(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	t3 := makeTestTab("tab3")
	g := makeTestGame(t1, t2, t3)

	// Active is tab1 (index 0), move tab3 (index 2) to index 0
	g.tabMgr.ActiveIdx = 0
	g.syncActive()

	g.tabMgr.Reorder(2, 0)
	g.syncActive()
	assertSyncInvariant(t, g, "after non-active reorder")

	// tab1 should have moved to index 1
	if g.tabMgr.ActiveIdx != 1 {
		t.Errorf("ActiveIdx = %d, want 1 (shifted by insert)", g.tabMgr.ActiveIdx)
	}
}

// --- Multiple operations ---

func TestMultipleOperations_AddRemoveSwitchReorder(t *testing.T) {
	g := makeTestGame()

	// Add 3 tabs
	for i := 0; i < 3; i++ {
		tab := makeTestTab("tab" + string(rune('A'+i)))
		g.tabMgr.Add(tab)
	}
	g.tabMgr.ActiveIdx = 0
	g.syncActive()
	assertSyncInvariant(t, g, "after adding 3 tabs")

	// Switch to tab 2
	g.tabMgr.ActiveIdx = 2
	g.syncActive()
	assertSyncInvariant(t, g, "after switch to 2")

	// Reorder tab 0 to end
	g.tabMgr.Reorder(0, 2)
	g.syncActive()
	assertSyncInvariant(t, g, "after reorder 0→2")

	// Remove middle tab
	g.tabMgr.Remove(1)
	g.syncActive()
	assertSyncInvariant(t, g, "after remove middle")

	// Add another tab
	g.tabMgr.Add(makeTestTab("tabD"))
	g.syncActive()
	assertSyncInvariant(t, g, "after adding tabD")

	// Switch to new tab
	g.tabMgr.ActiveIdx = len(g.tabMgr.Tabs) - 1
	g.syncActive()
	assertSyncInvariant(t, g, "after switch to last")
}

// --- Focus management ---

func TestSetFocusUpdatesTab(t *testing.T) {
	p1 := &pane.Pane{CustomName: "pane1"}
	p2 := &pane.Pane{CustomName: "pane2"}
	layout := &pane.LayoutNode{
		Kind:  pane.HSplit,
		Left:  pane.NewLeaf(p1),
		Right: pane.NewLeaf(p2),
		Ratio: 0.5,
	}
	tb := &tab.Tab{Layout: layout, Focused: p1}

	tm := NewTabManager()
	tm.Add(tb)
	g := &Game{tabMgr: tm}

	// Use setActiveFocused — this is what setFocusNoHistory does
	g.setActiveFocused(p2)

	assertSyncInvariant(t, g, "after focus change to pane2")

	if g.activeFocused().CustomName != "pane2" {
		t.Errorf("focused = %q, want pane2", g.activeFocused().CustomName)
	}
}

// --- Edge cases ---

func TestSyncActive_EmptyTabManager(t *testing.T) {
	g := &Game{tabMgr: NewTabManager()}
	// Should not panic
	g.syncActive()
}

func TestSyncActive_SingleTab(t *testing.T) {
	t1 := makeTestTab("only")
	g := makeTestGame(t1)
	g.syncActive()
	assertSyncInvariant(t, g, "single tab")
}

func TestRemoveAllTabs_Sequential(t *testing.T) {
	t1 := makeTestTab("tab1")
	t2 := makeTestTab("tab2")
	t3 := makeTestTab("tab3")
	g := makeTestGame(t1, t2, t3)

	// Remove tabs one by one from the front
	for g.tabMgr.Count() > 1 {
		g.tabMgr.Remove(0)
		g.syncActive()
		assertSyncInvariant(t, g, "after sequential remove")
	}

	// Last tab
	remaining := g.tabMgr.Remove(0)
	if remaining {
		t.Error("should return false when last tab removed")
	}
}

func TestSyncActive_AfterAddingFirstTab(t *testing.T) {
	g := &Game{tabMgr: NewTabManager()}

	t1 := makeTestTab("first")
	g.tabMgr.Add(t1)
	g.tabMgr.ActiveIdx = 0
	g.syncActive()
	assertSyncInvariant(t, g, "after first tab added")
}
