package main

import (
	"image"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

func (g *Game) handleFocus() {
	// NSWorkspaceDidWakeNotification: macOS fires this exactly once on system wake.
	// Trigger unsuspend and full repaint immediately, before any focus-state
	// transition, which is unreliable after sleep/wake on some macOS configurations.
	if consumeWake() {
		g.unsuspendAndRedraw()
	}

	focused := ebiten.IsFocused()

	// Idle suspension: reduce TPS after 5 seconds unfocused (when auto_idle is enabled).
	if g.cfg.Performance.AutoIdle && !focused && !g.wfocus.Suspended && !g.wfocus.UnfocusedAt.IsZero() &&
		time.Since(g.wfocus.UnfocusedAt) > unfocusSuspendDelay {
		ebiten.SetTPS(5)
		g.wfocus.Suspended = true
		for _, t := range g.tabMgr.Tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(true)
			}
		}
		for _, t := range g.tabMgr.Parked {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(true)
			}
		}
	}

	if focused != g.wfocus.PrevFocused {
		if focused {
			// Unsuspend, zero unfocusedAt, and force full repaint.
			g.unsuspendAndRedraw()

			// Reset edge-detection state: only snapshot modifier keys so that
			// Cmd held from Cmd+Tab doesn't appear as a stale press. Non-modifier
			// keys start as "not pressed" so the first real keystroke or paste
			// after focus regain fires its leading edge correctly.
			for k := ebiten.Key(0); k <= ebiten.KeyMax; k++ {
				switch k {
				case ebiten.KeyMeta, ebiten.KeyMetaLeft, ebiten.KeyMetaRight,
					ebiten.KeyControl, ebiten.KeyControlLeft, ebiten.KeyControlRight,
					ebiten.KeyShift, ebiten.KeyShiftLeft, ebiten.KeyShiftRight,
					ebiten.KeyAlt, ebiten.KeyAltLeft, ebiten.KeyAltRight:
					g.input.PrevKeys[k] = ebiten.IsKeyPressed(k)
				default:
					g.input.PrevKeys[k] = false
				}
			}
			// Reset mouse button edge-detection state on focus gain, matching
			// prevKeys reset above. Stale prevMouseButtons[left]=true from the
			// last interaction before focus loss would cause the first click to
			// be silently skipped (pressed==was → no edge detected).
			for btn := range g.input.PrevMouseButtons {
				g.input.PrevMouseButtons[btn] = false
			}
			g.input.PtyRepeat.Reset()
			g.input.ScrollAccum = 0

			// Clear dock badge when window regains focus.
			clearDockBadge()
		} else {
			// Record when focus was lost.
			g.wfocus.UnfocusedAt = time.Now()
		}
		g.wfocus.PrevFocused = focused
		if g.activeFocused() != nil {
			g.activeFocused().Term.SendFocusEvent(focused)
		}
	}

	// Emergency recovery for systems where IsFocused() doesn't reliably update
	// after sleep/wake (e.g. work machines with screen lock or MDM policies).
	// If still suspended but the user interacts (click or keystroke), unsuspend
	// immediately without waiting for a focus-state transition.
	if g.wfocus.Suspended && (ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) ||
		len(ebiten.AppendInputChars(nil)) > 0) {
		g.unsuspendAndRedraw()
	}
}

// unsuspendAndRedraw lifts idle suspension (if active) and forces a full repaint.
// Called from handleFocus (focus-gain, wake notification, and emergency recovery).
func (g *Game) unsuspendAndRedraw() {
	if g.wfocus.Suspended {
		ebiten.SetTPS(g.cfg.Performance.TPS)
		g.wfocus.Suspended = false
		for _, t := range g.tabMgr.Tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(false)
			}
		}
		for _, t := range g.tabMgr.Parked {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(false)
			}
		}
	}
	g.wfocus.UnfocusedAt = time.Time{}
	// Reset cached window size so handleResize fires unconditionally on the
	// next Update. handleResize runs before handleFocus, so on wake or
	// focus-gain the stale size check would silently skip the resize pass.
	// Clearing g.winW guarantees the next frame re-applies pane rects with
	// whatever size macOS has settled on after sleep/wake.
	g.winW = 0
	g.render.Dirty = true
	g.renderer.SetLayoutDirty()
	for _, t := range g.tabMgr.Tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.Buf.Lock()
			leaf.Pane.Term.Buf.MarkAllDirty()
			leaf.Pane.Term.Buf.Unlock()
		}
	}
}

// physSize returns the physical pixel dimensions of the window.
func (g *Game) physSize() (int, int) {
	return int(float64(g.winW) * g.dpi), int(float64(g.winH) * g.dpi)
}

// contentRect returns the pane content area: full window minus tab bar and status bar.
func (g *Game) contentRect() image.Rectangle {
	physW, physH := g.physSize()
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	return image.Rect(0, tabBarH, physW, physH-statusBarH)
}

// handleDroppedFiles checks for files dropped onto the window and sends their
// paths to the focused PTY as space-separated, shell-escaped strings.
func (g *Game) handleDroppedFiles() {
	dropped := ebiten.DroppedFiles()
	if dropped == nil {
		return
	}
	entries, err := fs.ReadDir(dropped, ".")
	if err != nil || len(entries) == 0 {
		return
	}
	var paths []string
	for _, e := range entries {
		// Open the entry to get the real *os.File with the full path.
		// Ebitengine's VirtualFS wraps os.Open on the original absolute path.
		f, fErr := dropped.Open(e.Name())
		if fErr != nil {
			continue
		}
		if osFile, ok := f.(*os.File); ok {
			paths = append(paths, shellEscape(osFile.Name()))
		} else {
			paths = append(paths, shellEscape(e.Name()))
		}
		_ = f.Close()
	}
	if len(paths) == 0 {
		return
	}
	text := strings.Join(paths, " ")
	if g.activeFocused() == nil {
		return
	}
	g.activeFocused().Term.SendBytes([]byte(text))
	g.render.Dirty = true
}

// shellEscape wraps a path in single quotes for safe shell insertion.
// Interior single quotes are escaped as '\''.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// If the string has no special characters, return as-is.
	needsQuote := false
	for _, r := range s {
		if r == ' ' || r == '\'' || r == '"' || r == '\\' || r == '(' || r == ')' ||
			r == '&' || r == '|' || r == ';' || r == '$' || r == '`' || r == '!' ||
			r == '*' || r == '?' || r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '<' || r == '>' || r == '#' || r == '~' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
