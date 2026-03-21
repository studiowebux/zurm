package renderer

import (
	"fmt"
	"image"

	"github.com/hajimehoshi/ebiten/v2"
)

// StatsState holds runtime metrics for the stats overlay.
// Populated by the game loop on a 1-second timer.
type StatsState struct {
	Open bool

	// Ebitengine
	TPS float64
	FPS float64

	// Go runtime
	Goroutines int
	HeapAlloc  uint64 // bytes
	HeapSys    uint64 // bytes
	GCPauseNs  uint64 // last GC pause in nanoseconds
	NumGC      uint32

	// App
	TabCount  int
	PaneCount int
	BufRows   int // focused pane rows
	BufCols   int // focused pane cols
	Scrollback int // focused pane scrollback depth
}

// drawStats renders a small semi-transparent stats panel in the top-right corner.
// Non-modal: drawn onto offscreen, does not capture input.
func (r *Renderer) drawStats(state *StatsState) {
	if state == nil || !state.Open {
		return
	}

	cw := r.font.CellW
	ch := r.font.CellH
	pad := cw / 2

	lines := []string{
		fmt.Sprintf("TPS  %5.1f   FPS  %5.1f", state.TPS, state.FPS),
		fmt.Sprintf("Goroutines   %d", state.Goroutines),
		fmt.Sprintf("Heap  %s / %s", fmtBytes(state.HeapAlloc), fmtBytes(state.HeapSys)),
		fmt.Sprintf("GC #%d  pause %s", state.NumGC, fmtDuration(state.GCPauseNs)),
		fmt.Sprintf("Tabs %d  Panes %d", state.TabCount, state.PaneCount),
		fmt.Sprintf("Buffer %dx%d  scroll %d", state.BufCols, state.BufRows, state.Scrollback),
	}

	// Find widest line for panel sizing.
	maxLen := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > maxLen {
			maxLen = n
		}
	}

	panelW := maxLen*cw + pad*2
	panelH := len(lines)*ch + pad*2
	physW, _ := r.screenSize()
	tabBarH := r.TabBarHeight()

	panelX := physW - panelW - pad
	panelY := tabBarH + pad
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	// Semi-transparent background.
	r.offscreen.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)

	// Draw each line.
	for i, line := range lines {
		r.font.DrawString(r.offscreen, line, panelX+pad, panelY+pad+i*ch, r.ui.Dim)
	}
}

// fmtBytes formats bytes as a human-readable string.
func fmtBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// fmtDuration formats nanoseconds as a human-readable duration.
func fmtDuration(ns uint64) string {
	switch {
	case ns >= 1_000_000:
		return fmt.Sprintf("%.1f ms", float64(ns)/1e6)
	case ns >= 1_000:
		return fmt.Sprintf("%.1f µs", float64(ns)/1e3)
	default:
		return fmt.Sprintf("%d ns", ns)
	}
}
