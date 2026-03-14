package pane

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

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

	// BellUntil is the time until which the visual bell flash is active.
	BellUntil time.Time

	// CustomName is set by the user via rename. Overrides ProcName in the pane label.
	CustomName string
	// Renaming is true while the user is typing a new pane name inline.
	Renaming   bool
	RenameText string

	// ServerSessionID is non-empty when this pane is backed by a zurm-server session (Mode B).
	// Persisted in session.json so the session can be re-attached on restore.
	ServerSessionID string
}

// New creates a Pane for the given physical pixel rect, computes grid dimensions,
// creates a Terminal, and starts the shell.
//
// serverSessionID is non-empty when restoring a session saved in Mode B: the
// pane will attempt to re-attach to that zurm-server session. Pass empty string
// for new panes.
//
// dir is the working directory for the shell; empty string inherits the parent
// process CWD.
func New(cfg *config.Config, rect image.Rectangle, cellW, cellH int, dir, serverSessionID string) (*Pane, error) {
	cols := (rect.Dx() - cfg.Window.Padding*2) / cellW
	rows := (rect.Dy() - cfg.Window.Padding*2) / cellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}

	term := terminal.New(cfg)

	if cfg.Server.Enabled {
		if backend, err := connectServer(cfg, serverSessionID, cols, rows, dir); err == nil {
			if startErr := term.StartWithBackend(backend); startErr == nil {
				return &Pane{
					Term:            term,
					Rect:            rect,
					Cols:            cols,
					Rows:            rows,
					ServerSessionID: backend.SessionID(),
				}, nil
			}
		}
		// Fall through to Mode A silently.
	}

	if err := term.Start(dir); err != nil {
		return nil, fmt.Errorf("pane new: %w", err)
	}
	return &Pane{Term: term, Rect: rect, Cols: cols, Rows: rows}, nil
}

// connectServer tries to attach to serverSessionID (if non-empty) or create a
// new session via zurm-server.
func connectServer(cfg *config.Config, serverSessionID string, cols, rows int, dir string) (*terminal.ServerBackend, error) {
	addr := cfg.Server.Address
	if addr == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		addr = filepath.Join(home, ".config", "zurm", "server.sock")
	}

	if serverSessionID != "" {
		return terminal.AttachServerBackend(addr, serverSessionID)
	}

	shell := cfg.Shell.Program
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
	}
	env := terminal.BuildEnv(cols, rows)
	return terminal.NewServerBackend(addr, shell, cfg.Shell.Args, cols, rows, env, dir)
}
