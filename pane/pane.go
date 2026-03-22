package pane

import (
	"image"
	"time"

	"github.com/studiowebux/zurm/terminal"
)

// Pane wraps a Terminal with a physical pixel rect.
// Pattern: simple view — Pane holds a Terminal reference but does not manage its lifecycle.
// Use NewLocal/New or NewServer (factory.go) for full lifecycle creation.
type Pane struct {
	Term     *terminal.Terminal
	Rect     image.Rectangle // physical pixels in the window
	Cols     int
	Rows     int
	ProcName string // last known foreground process name (drained from Term.ForegroundProcCh)
	HeaderH  int    // height in pixels of the pane header bar (0 = no header)

	// BellUntil is the time until which the visual bell flash is active.
	BellUntil time.Time

	// CustomName is set by the user via rename. Overrides ProcName in the pane label.
	CustomName string
	// Renaming is true while the user is typing a new pane name inline.
	Renaming        bool
	RenameText      string
	RenameCursorPos int // rune index of the text cursor during rename

	// ServerSessionID is non-empty when this pane is backed by a zurm-server session (Mode B).
	ServerSessionID string
}

// NewPane creates a Pane from a pre-built terminal.
// The terminal must already be started (via term.Start or term.StartWithBackend).
// This is the dependency-injection constructor — no config, no server connection.
func NewPane(term *terminal.Terminal, rect image.Rectangle, cols, rows int) *Pane {
	return &Pane{Term: term, Rect: rect, Cols: cols, Rows: rows}
}
