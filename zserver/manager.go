package zserver

import (
	"fmt"
	"sync"
)

// Manager tracks all active sessions on the zurm-server.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewManager creates an empty session manager.
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// Create spawns a new PTY session and registers it.
func (m *Manager) Create(shell string, args []string, cols, rows int, env []string, dir string) (*Session, error) {
	spty, err := newSessionPTY(shell, args, cols, rows, env, dir)
	if err != nil {
		return nil, fmt.Errorf("spawn pty: %w", err)
	}
	id := genID()
	s := newSession(id, spty, dir, cols, rows)

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	// Remove from map and close PTY when shell exits.
	go func() {
		<-s.Dead()
		s.spty.close()
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
	}()

	return s, nil
}

// Get returns the session with the given ID, or false if not found.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// List returns a snapshot of all active sessions.
func (m *Manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		name, cols, rows := s.Name, s.Cols, s.Rows
		s.mu.Unlock()
		out = append(out, SessionInfo{
			ID:   s.ID,
			Name: name,
			PID:  s.pid(),
			Cols: cols,
			Rows: rows,
			Dir:  s.Dir,
		})
	}
	return out
}
