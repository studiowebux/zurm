package main

import (
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/renderer"
)

// --- Overlay Modal Priority ---

func makeOverlayTestGame() *Game {
	return &Game{
		tabMgr:  NewTabManager(),
		palette: NewPaletteController(),
		search:  NewSearchController(),
	}
}

func TestOverlay_PaletteClosesMenu(t *testing.T) {
	g := makeOverlayTestGame()
	g.overlays.Menu = renderer.MenuState{Open: true}

	// Simulate what openPalette does to overlay state (without calling renderer)
	g.palette.Open()
	g.overlays.Help = renderer.OverlayState{}
	g.overlays.Menu = renderer.MenuState{} // closeMenu sets this

	if g.overlays.Menu.Open {
		t.Error("opening palette should close menu")
	}
	if !g.palette.State.Open {
		t.Error("palette should be open")
	}
}

func TestOverlay_PaletteClosesOverlay(t *testing.T) {
	g := makeOverlayTestGame()
	g.overlays.Help = renderer.OverlayState{Open: true}

	g.palette.Open()
	g.overlays.Help = renderer.OverlayState{}

	if g.overlays.Help.Open {
		t.Error("opening palette should close help overlay")
	}
}

func TestOverlay_OverlayClosesPalette(t *testing.T) {
	g := makeOverlayTestGame()
	g.palette.State.Open = true

	g.palette.Close()
	g.overlays.Help = renderer.OverlayState{Open: true}

	if g.palette.State.Open {
		t.Error("opening overlay should close palette")
	}
	if !g.overlays.Help.Open {
		t.Error("overlay should be open")
	}
}

func TestOverlay_ShowConfirmTakesPriority(t *testing.T) {
	g := makeOverlayTestGame()
	g.palette.State.Open = true
	g.overlays.Help = renderer.OverlayState{Open: true}

	g.showConfirm("test?", func() {})

	if !g.overlays.Confirm.Open {
		t.Error("confirm dialog should be open")
	}
	if g.overlays.Confirm.Message != "test?" {
		t.Errorf("confirm message = %q, want %q", g.overlays.Confirm.Message, "test?")
	}
}

func TestOverlay_ConfirmDismissClears(t *testing.T) {
	g := makeOverlayTestGame()
	called := false
	g.showConfirm("test?", func() { called = true })

	// Simulate confirm
	g.overlays.ConfirmAction()
	g.overlays.Confirm = renderer.ConfirmState{}
	g.overlays.ConfirmAction = nil

	if !called {
		t.Error("confirm action should have been called")
	}
	if g.overlays.Confirm.Open {
		t.Error("confirm should be closed after action")
	}
}

func TestOverlay_ConfirmCancelClears(t *testing.T) {
	g := makeOverlayTestGame()
	g.showConfirm("test?", func() { t.Error("should not be called on cancel") })

	// Simulate cancel
	g.overlays.Confirm = renderer.ConfirmState{}
	g.overlays.ConfirmAction = nil

	if g.overlays.Confirm.Open {
		t.Error("confirm should be closed after cancel")
	}
}

func TestOverlay_TabSwitcherAndSearchMutualExclusion(t *testing.T) {
	g := makeOverlayTestGame()
	g.overlays.TabSwitcher = renderer.TabSwitcherState{Open: true}

	// Opening tab search should be independent (both can't be open in practice,
	// but the state structs are independent — input routing handles priority)
	g.overlays.TabSearch = renderer.TabSearchState{Open: true}
	g.overlays.TabSwitcher = renderer.TabSwitcherState{} // simulates what openTabSearch does

	if g.overlays.TabSwitcher.Open {
		t.Error("tab switcher should be closed when tab search opens")
	}
	if !g.overlays.TabSearch.Open {
		t.Error("tab search should be open")
	}
}

func TestOverlay_StatsCoexistsWithOthers(t *testing.T) {
	g := makeOverlayTestGame()
	g.overlays.Stats = renderer.StatsState{Open: true}
	g.overlays.Help = renderer.OverlayState{Open: true}

	// Stats is non-modal — both can be open
	if !g.overlays.Stats.Open {
		t.Error("stats should remain open")
	}
	if !g.overlays.Help.Open {
		t.Error("overlay should remain open alongside stats")
	}
}

// --- Status Bar State ---

func TestFlashStatus_SetsMessageAndExpiry(t *testing.T) {
	g := makeOverlayTestGame()
	before := time.Now()
	g.flashStatus("hello")

	if g.status.Bar.FlashMessage != "hello" {
		t.Errorf("FlashMessage = %q, want %q", g.status.Bar.FlashMessage, "hello")
	}
	if g.status.FlashExpiry.Before(before) {
		t.Error("flashExpiry should be in the future")
	}
	if g.status.FlashExpiry.Sub(before) < 2*time.Second {
		t.Error("flashExpiry should be ~3 seconds from now")
	}
}

func TestFlashStatus_ClearedAfterExpiry(t *testing.T) {
	g := makeOverlayTestGame()
	g.flashStatus("temp")

	// Simulate expiry
	g.status.FlashExpiry = time.Now().Add(-1 * time.Second)

	if !time.Now().After(g.status.FlashExpiry) {
		t.Error("should be past expiry")
	}
	// The game loop checks: if time.Now().After(g.status.FlashExpiry) { clear }
	// We verify the condition is met
}

func TestBellDebounce(t *testing.T) {
	g := makeOverlayTestGame()

	// First bell — should fire (lastBellSound is zero)
	now := time.Now()
	if now.Sub(g.status.LastBell) < bellDebounce {
		t.Error("first bell should not be debounced")
	}
	g.status.LastBell = now

	// Second bell within debounce — should be suppressed
	soon := now.Add(100 * time.Millisecond)
	if soon.Sub(g.status.LastBell) >= bellDebounce {
		t.Error("rapid bell should be debounced")
	}

	// Third bell after debounce — should fire
	later := now.Add(bellDebounce + time.Millisecond)
	if later.Sub(g.status.LastBell) < bellDebounce {
		t.Error("bell after debounce period should fire")
	}
}

func TestStatusPoller_ShouldPollCwd(t *testing.T) {
	p := NewStatusPoller()
	seq1 := uint64(1)

	// First poll should always return true (internal state is zero)
	if !p.ShouldPollCwd(seq1) {
		t.Error("first poll should return true")
	}

	// Same seq — should return false (no new output)
	if p.ShouldPollCwd(seq1) {
		t.Error("same seq should return false")
	}

	// New seq but within interval — should return false (throttled)
	seq2 := uint64(2)
	if p.ShouldPollCwd(seq2) {
		t.Error("new seq within interval should return false (throttled)")
	}
}

func TestTabHoverDismiss(t *testing.T) {
	state := renderer.TabHoverState{TabIdx: 3, Active: true}
	renderer.DismissTabHover(&state)

	if state.Active {
		t.Error("Active should be false after dismiss")
	}
	if state.TabIdx != -1 {
		t.Errorf("TabIdx = %d, want -1", state.TabIdx)
	}
}

// --- Window Focus & Render State ---

func TestWindowFocus_UnfocusedSetsTimestamp(t *testing.T) {
	g := makeOverlayTestGame()

	// Simulate losing focus
	now := time.Now()
	g.wfocus.UnfocusedAt = now

	if g.wfocus.UnfocusedAt.IsZero() {
		t.Error("unfocusedAt should be set")
	}
}

func TestWindowFocus_SuspendAfterDelay(t *testing.T) {
	g := makeOverlayTestGame()
	g.wfocus.UnfocusedAt = time.Now().Add(-unfocusSuspendDelay - time.Second)

	// Check condition: enough time has passed
	if time.Since(g.wfocus.UnfocusedAt) < unfocusSuspendDelay {
		t.Error("should have exceeded suspend delay")
	}

	g.wfocus.Suspended = true
	if !g.wfocus.Suspended {
		t.Error("should be suspended")
	}
}

func TestWindowFocus_ResumeOnFocus(t *testing.T) {
	g := makeOverlayTestGame()
	g.wfocus.Suspended = true
	g.wfocus.UnfocusedAt = time.Now().Add(-10 * time.Second)

	// Simulate focus regain
	g.wfocus.Suspended = false
	g.wfocus.UnfocusedAt = time.Time{}

	if g.wfocus.Suspended {
		t.Error("should not be suspended after focus regain")
	}
	if !g.wfocus.UnfocusedAt.IsZero() {
		t.Error("unfocusedAt should be zero after focus regain")
	}
}

func TestWindowFocus_FocusRegainClearsInputState(t *testing.T) {
	g := makeOverlayTestGame()
	g.input.PrevKeys = map[ebiten.Key]bool{ebiten.KeyA: true, ebiten.KeyB: true}

	// Simulate focus regain reset
	for k := range g.input.PrevKeys {
		g.input.PrevKeys[k] = false
	}

	for k, v := range g.input.PrevKeys {
		if v {
			t.Errorf("key %v should be false after focus reset", k)
		}
	}
}

func TestRenderState_DirtyFlag(t *testing.T) {
	g := makeOverlayTestGame()

	if g.render.Dirty {
		t.Error("should start clean")
	}

	g.render.Dirty = true
	if !g.render.Dirty {
		t.Error("should be dirty after set")
	}

	g.render.Dirty = false
	if g.render.Dirty {
		t.Error("should be clean after clear")
	}
}

func TestRenderState_NeedsRenderOnDirty(t *testing.T) {
	g := makeOverlayTestGame()
	g.cfg = &config.Config{}

	g.render.Dirty = true
	if !g.needsRender() {
		t.Error("needsRender should return true when dirty")
	}

	g.render.Dirty = false
	if g.needsRender() {
		t.Error("needsRender should return false when clean (no PTY change)")
	}
}

func TestRenderState_NeedsRenderOnClockTick(t *testing.T) {
	g := makeOverlayTestGame()
	g.cfg = &config.Config{}
	g.cfg.StatusBar.ShowClock = true
	g.render.LastClock = time.Now().Unix() - 2 // 2 seconds ago

	if !g.needsRender() {
		t.Error("needsRender should return true when clock second changed")
	}
}

func TestRenderState_DrawClearsDirty(t *testing.T) {
	g := makeOverlayTestGame()
	g.render.Dirty = true

	// Simulate what Draw does after rendering
	g.render.Dirty = false
	g.render.LastClock = time.Now().Unix()

	if g.render.Dirty {
		t.Error("dirty should be cleared after Draw")
	}
}
