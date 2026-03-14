package zserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Session is a running PTY session managed by zurm-server.
// It persists independently of any connected client.
type Session struct {
	ID   string
	Dir  string
	Cols int
	Rows int

	mu     sync.Mutex
	spty   *sessionPTY
	replay *ringBuf
	subs   []chan []byte // one buffered channel per attached client
	dead   chan struct{}  // mirrors spty.dead
}

func newSession(id string, spty *sessionPTY, dir string, cols, rows int) *Session {
	s := &Session{
		ID:     id,
		Dir:    dir,
		Cols:   cols,
		Rows:   rows,
		spty:   spty,
		replay: newRingBuf(),
		dead:   spty.dead,
	}
	spty.startReader(func(chunk []byte) {
		s.replay.write(chunk)
		s.mu.Lock()
		for _, ch := range s.subs {
			select {
			case ch <- chunk:
			default:
				// Slow client — drop chunk to avoid blocking the reader.
			}
		}
		s.mu.Unlock()
	})
	return s
}

func (s *Session) subscribe() chan []byte {
	ch := make(chan []byte, 256)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	return ch
}

func (s *Session) unsubscribe(ch chan []byte) {
	s.mu.Lock()
	for i, sub := range s.subs {
		if sub == ch {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	close(ch)
}

func (s *Session) write(p []byte) { s.spty.write(p) } //nolint:errcheck

func (s *Session) resize(cols, rows int) {
	s.mu.Lock()
	s.Cols = cols
	s.Rows = rows
	s.mu.Unlock()
	s.spty.resize(cols, rows) //nolint:errcheck
}

func (s *Session) pid() int { return s.spty.pid() }

// Dead returns a channel closed when the shell exits.
func (s *Session) Dead() <-chan struct{} { return s.dead }

func genID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}
