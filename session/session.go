package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PaneLayout represents a single pane or split in the layout tree.
type PaneLayout struct {
	Kind       string      `json:"kind"`                  // "leaf", "hsplit", or "vsplit"
	Ratio      float64     `json:"ratio,omitempty"`       // split ratio (0.0-1.0)
	Cwd             string      `json:"cwd,omitempty"`              // working directory for leaf panes
	CustomName      string      `json:"custom_name,omitempty"`      // user-set pane name
	ServerSessionID string      `json:"server_session_id,omitempty"` // non-empty for zurm-server panes (Mode B)
	Left       *PaneLayout `json:"left,omitempty"`        // left/top child for splits
	Right      *PaneLayout `json:"right,omitempty"`       // right/bottom child for splits
}

// TabData holds the persisted state of a single tab.
type TabData struct {
	Cwd         string      `json:"cwd"`
	Title       string      `json:"title"`
	UserRenamed bool        `json:"user_renamed"`
	PinnedSlot  string      `json:"pinned_slot"`      // single rune as string, or ""
	Note        string      `json:"note,omitempty"`   // user annotation for session context
	Layout      *PaneLayout `json:"layout,omitempty"` // pane layout tree
}

// SessionData is the root of the session file.
type SessionData struct {
	Version   int       `json:"version"`
	ActiveTab int       `json:"active_tab"`
	Tabs      []TabData `json:"tabs"`
}

// Path returns the path to the session file.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "zurm", "session.json"), nil
}

// Save writes the session file. The caller is responsible for checking
// whether session saving is enabled before calling.
func Save(data *SessionData) error {
	p, err := Path()
	if err != nil {
		return fmt.Errorf("session: resolve path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("session: create dir: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), "session-*.json")
	if err != nil {
		return fmt.Errorf("session: create temp: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()          // #nosec G104 G703 — cleanup on error; path from UserHomeDir
		os.Remove(f.Name()) // #nosec G104 G703
		return fmt.Errorf("session: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name()) // #nosec G104 G703 — cleanup on error
		return fmt.Errorf("session: close temp: %w", err)
	}
	if err := os.Rename(f.Name(), p); err != nil { // #nosec G703 — path from UserHomeDir
		return fmt.Errorf("session: rename: %w", err)
	}
	return nil
}

// Load reads and parses the session file. Returns nil, nil when the file does
// not exist. The caller is responsible for checking session config flags.
func Load() (*SessionData, error) {
	p, err := Path()
	if err != nil {
		return nil, fmt.Errorf("session: resolve path: %w", err)
	}
	f, err := os.Open(p) // #nosec G304 G703 — path from os.UserHomeDir(), not user input
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: open: %w", err)
	}
	defer f.Close()
	var data SessionData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("session: decode: %w", err)
	}
	return &data, nil
}
