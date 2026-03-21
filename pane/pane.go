package pane

import (
	"fmt"
	"image"
	"log"
	"os"
	"time"

	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/terminal"
	"github.com/studiowebux/zurm/zserver"
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
	Renaming        bool
	RenameText      string
	RenameCursorPos int // rune index of the text cursor during rename

	// ServerSessionID is non-empty when this pane is backed by a zurm-server session (Mode B).
	// Not persisted in session.json — use --attach <id> or the palette to reattach.
	ServerSessionID string
}

// New creates a Pane backed by a local PTY (Mode A).
//
// This is the default path. For a server-backed pane use NewServer instead.
// dir is the working directory for the shell; empty string inherits the parent
// process CWD.
func New(cfg *config.Config, rect image.Rectangle, cellW, cellH int, dir string) (*Pane, error) {
	cols, rows := gridDims(rect, cfg.Window.Padding, cellW, cellH)

	term := terminal.New(buildTermConfig(cfg))
	if err := term.Start(dir); err != nil {
		return nil, fmt.Errorf("pane new: %w", err)
	}
	return &Pane{Term: term, Rect: rect, Cols: cols, Rows: rows}, nil
}

// NewServer creates a Pane backed by a zurm-server session (Mode B).
//
// Auto-start: if zurm-server is not yet running at the configured socket,
// EnsureServer spawns it as a detached background process before connecting.
//
// serverSessionID is non-empty when restoring a saved session — the pane
// re-attaches to that existing server session. Pass empty for a new session.
//
// Fallback: if the server cannot be reached or the binary is not found, the
// pane falls back to a local PTY (Mode A) and logs the reason.
func NewServer(cfg *config.Config, rect image.Rectangle, cellW, cellH int, dir, serverSessionID string) (*Pane, error) {
	cols, rows := gridDims(rect, cfg.Window.Padding, cellW, cellH)

	term := terminal.New(buildTermConfig(cfg))

	addr, err := zserver.EnsureServer(cfg.Server.Address, cfg.Server.Binary)
	if err != nil {
		log.Printf("pane: zurm-server unavailable (%v) — falling back to Mode A", err)
		return localFallback(term, rect, cols, rows, dir)
	}

	if backend, err := connectServer(addr, cfg, serverSessionID, cols, rows, dir); err == nil {
		if startErr := term.StartWithBackend(backend); startErr == nil {
			log.Printf("pane: Mode B — connected to zurm-server session %s", backend.SessionID())
			return &Pane{
				Term:            term,
				Rect:            rect,
				Cols:            cols,
				Rows:            rows,
				ServerSessionID: backend.SessionID(),
			}, nil
		} else {
			log.Printf("pane: zurm-server backend start failed: %v — falling back to Mode A", startErr)
		}
	} else {
		log.Printf("pane: zurm-server connect failed: %v — falling back to Mode A", err)
	}

	return localFallback(term, rect, cols, rows, dir)
}

// localFallback starts the terminal with a local PTY and returns a Mode A pane.
func localFallback(term *terminal.Terminal, rect image.Rectangle, cols, rows int, dir string) (*Pane, error) {
	if err := term.Start(dir); err != nil {
		return nil, fmt.Errorf("pane fallback: %w", err)
	}
	return &Pane{Term: term, Rect: rect, Cols: cols, Rows: rows}, nil
}

// buildTermConfig constructs a TerminalConfig from the application config.
func buildTermConfig(cfg *config.Config) terminal.TerminalConfig {
	return terminal.TerminalConfig{
		Rows:            cfg.Window.Rows,
		Cols:            cfg.Window.Columns,
		ScrollbackLines: cfg.Scrollback.Lines,
		MaxBlocks:       cfg.Blocks.MaxHistory,
		FG:              config.ParseHexColor(cfg.Colors.Foreground),
		BG:              config.ParseHexColor(cfg.Colors.Background),
		Palette:         cfg.Palette(),
		CursorBlink:     cfg.Input.CursorBlink,
		ShellProgram:    cfg.Shell.Program,
		ShellArgs:       cfg.Shell.Args,
		ShowProcess:     cfg.StatusBar.ShowProcess,
	}
}

// gridDims computes terminal grid dimensions from a physical pixel rect.
func gridDims(rect image.Rectangle, padding, cellW, cellH int) (cols, rows int) {
	cols = (rect.Dx() - padding*2) / cellW
	rows = (rect.Dy() - padding*2) / cellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// connectServer tries to attach to serverSessionID (if non-empty) or create a
// new session via zurm-server at addr.
func connectServer(addr string, cfg *config.Config, serverSessionID string, cols, rows int, dir string) (*terminal.ServerBackend, error) {
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

