package session

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/studiowebux/zurm/config"
)


// TabData holds the persisted state of a single tab.
type TabData struct {
	Cwd         string `json:"cwd"`
	Title       string `json:"title"`
	UserRenamed bool   `json:"user_renamed"`
	PinnedSlot  string `json:"pinned_slot"` // single rune as string, or ""
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

// Save writes the session file. tabs is a slice of TabData already populated
// by the caller. If cfg.Session is disabled this is a no-op.
func Save(data *SessionData, cfg *config.Config) error {
	if !cfg.Session.Enabled {
		return nil
	}
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), "session-*.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()          // #nosec G104 G703 — cleanup on error; path from UserHomeDir
		os.Remove(f.Name()) // #nosec G104 G703
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name()) // #nosec G104 G703 — cleanup on error
		return err
	}
	return os.Rename(f.Name(), p) // #nosec G703 — path from UserHomeDir
}

// Load reads and parses the session file. Returns nil, nil when the file does
// not exist or when cfg.Session.RestoreOnLaunch is false.
func Load(cfg *config.Config) (*SessionData, error) {
	if !cfg.Session.Enabled || !cfg.Session.RestoreOnLaunch {
		return nil, nil
	}
	p, err := Path()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p) // #nosec G304 G703 — path from os.UserHomeDir(), not user input
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var data SessionData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}
