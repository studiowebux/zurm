package terminal

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"

	"github.com/studiowebux/zurm/zserver"
)

// ServerBackend implements PtyBackend by delegating to a zurm-server session.
// It connects over a Unix socket (or TCP in the future).
type ServerBackend struct {
	conn      net.Conn
	sessionID string
	pid       int
	dead      chan struct{}
}

// NewServerBackend connects to zurm-server and creates a new session.
func NewServerBackend(address, shell string, args []string, cols, rows int, env []string, dir string) (*ServerBackend, error) {
	conn, err := net.Dial("unix", address)
	if err != nil {
		return nil, fmt.Errorf("connect to zurm-server at %s: %w", address, err)
	}

	req := zserver.CreateSessionRequest{Shell: shell, Args: args, Cols: cols, Rows: rows, Env: env, Dir: dir}
	data, _ := json.Marshal(req)
	if err := zserver.WriteMessage(conn, zserver.MsgCreateSession, data); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send create: %w", err)
	}

	msg, err := zserver.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read response: %w", err)
	}
	if msg.Type == zserver.MsgError {
		conn.Close()
		return nil, fmt.Errorf("server: %s", msg.Payload)
	}
	if msg.Type != zserver.MsgSessionInfo {
		conn.Close()
		return nil, fmt.Errorf("unexpected response type 0x%02x", msg.Type)
	}

	var info zserver.SessionInfo
	json.Unmarshal(msg.Payload, &info) //nolint:errcheck

	return &ServerBackend{
		conn:      conn,
		sessionID: info.ID,
		pid:       info.PID,
		dead:      make(chan struct{}),
	}, nil
}

// AttachServerBackend connects to an existing zurm-server session by ID.
func AttachServerBackend(address, sessionID string) (*ServerBackend, error) {
	conn, err := net.Dial("unix", address)
	if err != nil {
		return nil, fmt.Errorf("connect to zurm-server at %s: %w", address, err)
	}

	req := zserver.AttachSessionRequest{ID: sessionID}
	data, _ := json.Marshal(req)
	if err := zserver.WriteMessage(conn, zserver.MsgAttachSession, data); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send attach: %w", err)
	}

	msg, err := zserver.ReadMessage(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read response: %w", err)
	}
	if msg.Type == zserver.MsgError {
		conn.Close()
		return nil, fmt.Errorf("server: %s", msg.Payload)
	}
	if msg.Type != zserver.MsgSessionInfo {
		conn.Close()
		return nil, fmt.Errorf("unexpected response type 0x%02x", msg.Type)
	}

	var info zserver.SessionInfo
	json.Unmarshal(msg.Payload, &info) //nolint:errcheck

	return &ServerBackend{
		conn:      conn,
		sessionID: info.ID,
		pid:       info.PID,
		dead:      make(chan struct{}),
	}, nil
}

// SessionID returns the server-assigned session ID (for session save/restore).
func (b *ServerBackend) SessionID() string { return b.sessionID }

// Write sends input bytes to the remote shell.
func (b *ServerBackend) Write(p []byte) (int, error) {
	if err := zserver.WriteMessage(b.conn, zserver.MsgInput, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize sends a resize request to the server.
func (b *ServerBackend) Resize(cols, rows int) error {
	payload := make([]byte, 4)
	binary.LittleEndian.PutUint16(payload[0:2], uint16(cols)) //nolint:gosec
	binary.LittleEndian.PutUint16(payload[2:4], uint16(rows)) //nolint:gosec
	return zserver.WriteMessage(b.conn, zserver.MsgResize, payload)
}

// Dead returns a channel closed when the session ends.
func (b *ServerBackend) Dead() <-chan struct{} { return b.dead }

// Close detaches from the session. The session remains alive on the server.
func (b *ServerBackend) Close() {
	zserver.WriteMessage(b.conn, zserver.MsgDetachSession, nil) //nolint:errcheck
	b.conn.Close()
}

// Pid returns the shell PID reported by the server.
func (b *ServerBackend) Pid() int { return b.pid }

// ForegroundPgid is not available for remote sessions.
func (b *ServerBackend) ForegroundPgid() (int, error) { return 0, nil }

// StartReader reads output from the server and feeds it into the terminal parser.
// Satisfies PtyBackend. Must be called exactly once.
func (b *ServerBackend) StartReader(parser *Parser, buf *ScreenBuffer, paused *atomic.Bool) {
	go func() {
		defer close(b.dead)
		for {
			msg, err := zserver.ReadMessage(b.conn)
			if err != nil {
				return
			}
			switch msg.Type {
			case zserver.MsgOutput:
				for paused.Load() {
					runtime.Gosched()
				}
				buf.Lock()
				parser.Feed(msg.Payload)
				buf.Unlock()
				buf.BumpRenderGen()
				BumpRenderSeq()
			case zserver.MsgSessionDead:
				return
			}
		}
	}()
}
