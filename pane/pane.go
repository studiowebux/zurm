package pane

import (
	"fmt"
	"image"

	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/terminal"
)

// Pane wraps a Terminal with a physical pixel rect.
// Pattern: composite — Pane owns its Terminal lifecycle.
type Pane struct {
	Term     *terminal.Terminal
	Rect     image.Rectangle // physical pixels in the window
	Cols     int
	Rows     int
	ProcName string // last known foreground process name (drained from Term.ForegroundProcCh)
	HeaderH  int    // height in pixels of the pane header bar (0 = no header)
}

// New creates a Pane for the given physical pixel rect, computes grid dimensions,
// creates a Terminal, and starts the shell.
// dir is the working directory for the shell; empty string inherits the parent process CWD.
func New(cfg *config.Config, rect image.Rectangle, cellW, cellH int, dir string) (*Pane, error) {
	cols := (rect.Dx() - cfg.Window.Padding*2) / cellW
	rows := (rect.Dy() - cfg.Window.Padding*2) / cellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}

	term := terminal.New(cfg)
	if err := term.Start(dir); err != nil {
		return nil, fmt.Errorf("pane new: %w", err)
	}

	return &Pane{
		Term: term,
		Rect: rect,
		Cols: cols,
		Rows: rows,
	}, nil
}
