package renderer

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/config"
)

// StatusBarState holds the data the renderer needs to draw one frame of the bar.
type StatusBarState struct {
	Cwd             string
	GitBranch       string
	GitCommit       string // short commit hash (7 chars)
	GitDirty        int    // modified file count
	GitStaged       int    // staged file count
	GitAhead        int    // commits ahead of upstream
	GitBehind       int    // commits behind upstream
	ForegroundProc  string          // foreground process name, "" when shell is foreground
	ScrollOffset    int             // buf.ViewOffset of the focused pane; 0 = live output
	Zoomed          bool            // true when a pane is fullscreened via Cmd+Z
	PinMode         bool            // true while waiting for a pin slot keypress
	HelpBtnRect     image.Rectangle // set during draw; used by main.go for click detection
	FlashMessage    string          // transient message shown in place of cwd; cleared by Game.Update
	BlocksEnabled   bool            // show block indicator when true
	BlockCount      int             // number of completed blocks in focused pane
	Recording         bool          // true while a screen recording is in progress
	RecordingDuration time.Duration // elapsed recording time
	RecordingBytes    int64         // output MP4 file size on disk
	RecordingMode     string        // "MP4"
	Listening         bool          // true while STT dictation is active
	Version           string        // app version, e.g. "v0.4.1"
	TabNote           string        // active tab's annotation, shown as a middle segment
	ServerSessionCount int           // number of open panes backed by zurm-server; 0 in local mode
}

// StatusBarHeight returns the physical pixel height of the status bar,
// or 0 when the bar is disabled.
func StatusBarHeight(font *FontRenderer, cfg *config.Config) int {
	if !cfg.StatusBar.Enabled {
		return 0
	}
	padding := cfg.StatusBar.PaddingPx
	sepH := cfg.StatusBar.SeparatorHeightPx
	return font.CellH + 4 + padding + sepH
}

// drawStatusBar renders the status bar at the bottom of the offscreen image.
func (r *Renderer) drawStatusBar(state *StatusBarState) {
	if !r.cfg.StatusBar.Enabled || state == nil {
		return
	}

	h := StatusBarHeight(r.font, r.cfg)
	physH := r.offscreen.Bounds().Dy()
	physW := r.offscreen.Bounds().Dx()
	barRect := image.Rect(0, physH-h, physW, physH)

	barBg := darken(config.ParseHexColor(r.cfg.Colors.Background))
	r.offscreen.SubImage(barRect).(*ebiten.Image).Fill(barBg)

	// Separator line at the top of the bar.
	sepH := r.cfg.StatusBar.SeparatorHeightPx
	if sepH > 0 {
		sepColor := r.separatorColor()
		r.offscreen.SubImage(image.Rect(0, physH-h, physW, physH-h+sepH)).(*ebiten.Image).Fill(sepColor)
	}

	fg := config.ParseHexColor(r.cfg.Colors.BrightBlack)
	accentFg := config.ParseHexColor(r.cfg.Colors.Foreground)
	padding := r.cfg.StatusBar.PaddingPx
	textY := physH - h + sepH + padding + (h-sepH-padding-r.font.CellH)/2

	totalCols := physW / r.font.CellW

	// Separator used between every segment on both sides.
	sep := r.cfg.StatusBar.SegmentSeparator
	if sep == "" {
		sep = " · "
	}
	sepCols := len([]rune(sep))

	type seg struct {
		text  string
		color color.RGBA
	}

	// --- Right-side segments (rightmost first: clock, scroll, zoom) ---
	var rightSegs []seg
	if r.cfg.StatusBar.ShowClock {
		rightSegs = append(rightSegs, seg{time.Now().Format("15:04:05"), fg})
	}
	if state.ScrollOffset > 0 {
		rightSegs = append(rightSegs, seg{fmt.Sprintf("↑ %d", state.ScrollOffset), accentFg})
	}
	if state.ServerSessionCount > 0 {
		rightSegs = append(rightSegs, seg{fmt.Sprintf("S%d", state.ServerSessionCount), config.ParseHexColor(r.cfg.Colors.Cyan)})
	}
	if state.Zoomed {
		rightSegs = append(rightSegs, seg{"[ZOOM]", accentFg})
	}
	if state.PinMode {
		rightSegs = append(rightSegs, seg{"[PIN]", accentFg})
	}
	if state.BlocksEnabled {
		blockInd := fmt.Sprintf("[B:%d]", state.BlockCount)
		rightSegs = append(rightSegs, seg{blockInd, r.ui.Accent})
	}
	if state.Recording {
		// Blink the dot each second to make the recording indicator visually distinct.
		dot := "●"
		if time.Now().Second()%2 == 0 {
			dot = "○"
		}
		recText := fmt.Sprintf("%s [%s] %s", dot, state.RecordingMode, fmtRecDuration(state.RecordingDuration))
		if state.RecordingBytes > 0 {
			recText += " " + fmtFileSize(state.RecordingBytes)
		}
		rightSegs = append(rightSegs, seg{recText, config.ParseHexColor(r.cfg.Colors.Red)})
	}
	if state.Listening {
		dot := "●"
		if time.Now().Second()%2 == 0 {
			dot = "○"
		}
		rightSegs = append(rightSegs, seg{dot + " MIC", config.ParseHexColor(r.cfg.Colors.Green)})
	}
	if state.Version != "" {
		rightSegs = append(rightSegs, seg{state.Version, fg})
	}

	// Right column budget (margin + ? button + segments + separators between them).
	rightCols := 1 + 3 // right margin + " ? " help button
	for i, s := range rightSegs {
		rightCols += len([]rune(s.text))
		if i > 0 {
			rightCols += sepCols
		}
	}

	// --- Left middle segments: branch then process ---
	var midSegs []seg
	if r.cfg.StatusBar.ShowGit && state.GitBranch != "" {
		gitText := r.cfg.StatusBar.BranchPrefix + state.GitBranch
		if r.cfg.StatusBar.ShowCommit && state.GitCommit != "" {
			gitText += " " + state.GitCommit
		}
		if r.cfg.StatusBar.ShowDirty {
			var gitFlags []string
			if state.GitStaged > 0 {
				gitFlags = append(gitFlags, fmt.Sprintf("+%d", state.GitStaged))
			}
			if state.GitDirty > 0 {
				gitFlags = append(gitFlags, fmt.Sprintf("~%d", state.GitDirty))
			}
			if len(gitFlags) > 0 {
				gitText += " " + strings.Join(gitFlags, " ")
			}
		}
		if r.cfg.StatusBar.ShowAheadBehind && (state.GitAhead > 0 || state.GitBehind > 0) {
			var arrows []string
			if state.GitAhead > 0 {
				arrows = append(arrows, fmt.Sprintf("%d^", state.GitAhead))
			}
			if state.GitBehind > 0 {
				arrows = append(arrows, fmt.Sprintf("%dv", state.GitBehind))
			}
			gitText += " " + strings.Join(arrows, " ")
		}
		midSegs = append(midSegs, seg{gitText, fg})
	}
	if r.cfg.StatusBar.ShowProcess && state.ForegroundProc != "" {
		midSegs = append(midSegs, seg{state.ForegroundProc, accentFg})
	}
	if state.TabNote != "" {
		midSegs = append(midSegs, seg{"[" + state.TabNote + "]", fg})
	}

	midCols := 0
	for _, s := range midSegs {
		midCols += len([]rune(s.text))
	}
	if len(midSegs) > 1 {
		midCols += sepCols * (len(midSegs) - 1)
	}

	// --- CWD: whatever columns remain (or FlashMessage when set) ---
	x := r.font.CellW / 2
	if state.FlashMessage != "" {
		// Flash message takes priority — skip CWD and middle segments entirely.
		maxFlashCols := totalCols - rightCols - 1
		flash := state.FlashMessage
		if fr := []rune(flash); len(fr) > maxFlashCols {
			flash = string(fr[:maxFlashCols])
		}
		r.font.DrawString(r.offscreen, flash, x, textY, accentFg)
	} else {
		cwdDrawn := false
		if r.cfg.StatusBar.ShowCwd && state.Cwd != "" {
			cwdSep := 0
			if len(midSegs) > 0 {
				cwdSep = sepCols
			}
			maxCwdCols := totalCols - rightCols - midCols - cwdSep - 1
			if maxCwdCols < 4 {
				maxCwdCols = 4
			}
			cwd := abbreviatePath(state.Cwd, maxCwdCols)
			r.font.DrawString(r.offscreen, cwd, x, textY, accentFg)
			x += len([]rune(cwd)) * r.font.CellW
			cwdDrawn = true
		}

		// Draw middle segments with separators.
		for i, s := range midSegs {
			if i == 0 && cwdDrawn {
				r.font.DrawString(r.offscreen, sep, x, textY, fg)
				x += sepCols * r.font.CellW
			} else if i > 0 {
				r.font.DrawString(r.offscreen, sep, x, textY, fg)
				x += sepCols * r.font.CellW
			}
			r.font.DrawString(r.offscreen, s.text, x, textY, s.color)
			x += len([]rune(s.text)) * r.font.CellW
		}
	}

	// ? help button at the far right edge (below the separator line).
	helpBtnW := r.font.CellW * 3 // " ? " — one cell padding each side
	helpBtnX := physW - helpBtnW
	helpBtnRect := image.Rect(helpBtnX, physH-h+sepH, physW, physH)
	r.offscreen.SubImage(helpBtnRect).(*ebiten.Image).Fill(darken(barBg))
	r.font.DrawString(r.offscreen, "?", helpBtnX+r.font.CellW, textY, accentFg)
	if state != nil {
		state.HelpBtnRect = helpBtnRect
	}

	// Draw right-aligned segments right-to-left; separators go between them.
	// Start leftward of the ? button.
	rightX := helpBtnX - r.font.CellW/2
	for i, s := range rightSegs {
		w := len([]rune(s.text)) * r.font.CellW
		rightX -= w
		r.font.DrawString(r.offscreen, s.text, rightX, textY, s.color)
		if i < len(rightSegs)-1 {
			rightX -= sepCols * r.font.CellW
			r.font.DrawString(r.offscreen, sep, rightX, textY, fg)
		}
	}
}

// darken returns a slightly darker version of c for the status bar background.
func darken(c color.RGBA) color.RGBA {
	scale := func(v uint8) uint8 {
		n := int(v) * 85 / 100
		if n < 0 {
			return 0
		}
		return uint8(n) // #nosec G115 — n = v*85/100 where v is uint8; result is always ≤ 216
	}
	return color.RGBA{R: scale(c.R), G: scale(c.G), B: scale(c.B), A: c.A}
}

// abbreviatePath shortens a path to fit within maxCols character cells.
// Replaces the home directory prefix with ~, then drops interior path
// components until it fits, producing e.g. "~/…/parent/dir".
func abbreviatePath(path string, maxCols int) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if len(path) >= len(home) && path[:len(home)] == home {
			path = "~" + path[len(home):]
		}
	}
	if len([]rune(path)) <= maxCols {
		return path
	}
	// Try dropping leading components until it fits.
	parts := strings.Split(path, "/")
	for drop := 1; drop < len(parts)-1; drop++ {
		candidate := "…/" + strings.Join(parts[drop+1:], "/")
		if len([]rune(candidate)) <= maxCols {
			return candidate
		}
	}
	// Last resort: truncate the final component.
	last := parts[len(parts)-1]
	lr := []rune(last)
	if maxCols > 4 && len(lr) > maxCols-2 {
		return "…/" + string(lr[len(lr)-(maxCols-2):])
	}
	return string([]rune(path)[:maxCols])
}

// fmtRecDuration formats a recording duration as MM:SS.
func fmtRecDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// fmtFileSize formats a byte count as a human-readable size (e.g. "2.3MB").
func fmtFileSize(b int64) string {
	const kb = 1024.0
	const mb = 1024.0 * kb
	switch {
	case float64(b) >= mb:
		return fmt.Sprintf("%.1fMB", float64(b)/mb)
	case float64(b) >= kb:
		return fmt.Sprintf("%.1fKB", float64(b)/kb)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
