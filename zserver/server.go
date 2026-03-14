package zserver

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"os"
)

// Server listens on a Unix socket and manages client connections.
type Server struct {
	manager  *Manager
	listener net.Listener
}

// NewServer creates a Server bound to the given Unix socket path.
// Any stale socket file at socketPath is removed before binding.
func NewServer(socketPath string) (*Server, error) {
	os.Remove(socketPath) //nolint:errcheck
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &Server{manager: NewManager(), listener: ln}, nil
}

// Serve accepts connections until Close is called.
func (srv *Server) Serve() {
	for {
		conn, err := srv.listener.Accept()
		if err != nil {
			return
		}
		go srv.handleConn(conn)
	}
}

// Close shuts the server down.
func (srv *Server) Close() { srv.listener.Close() }

func (srv *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	msg, err := ReadMessage(conn)
	if err != nil {
		return
	}

	switch msg.Type {
	case MsgListSessions:
		list := srv.manager.List()
		data, _ := json.Marshal(list)
		WriteMessage(conn, MsgSessionList, data) //nolint:errcheck
		return

	case MsgCreateSession:
		var req CreateSessionRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			WriteMessage(conn, MsgError, []byte("invalid create request: "+err.Error())) //nolint:errcheck
			return
		}
		s, err := srv.manager.Create(req.Shell, req.Args, req.Cols, req.Rows, req.Env, req.Dir)
		if err != nil {
			WriteMessage(conn, MsgError, []byte(err.Error())) //nolint:errcheck
			return
		}
		info := SessionInfo{ID: s.ID, PID: s.pid(), Cols: s.Cols, Rows: s.Rows, Dir: s.Dir}
		data, _ := json.Marshal(info)
		WriteMessage(conn, MsgSessionInfo, data) //nolint:errcheck
		srv.serveSession(conn, s)

	case MsgAttachSession:
		var req AttachSessionRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			WriteMessage(conn, MsgError, []byte("invalid attach request: "+err.Error())) //nolint:errcheck
			return
		}
		s, ok := srv.manager.Get(req.ID)
		if !ok {
			WriteMessage(conn, MsgError, []byte("session not found: "+req.ID)) //nolint:errcheck
			return
		}
		info := SessionInfo{ID: s.ID, PID: s.pid(), Cols: s.Cols, Rows: s.Rows, Dir: s.Dir}
		data, _ := json.Marshal(info)
		WriteMessage(conn, MsgSessionInfo, data) //nolint:errcheck
		srv.serveSession(conn, s)

	default:
		WriteMessage(conn, MsgError, []byte("unexpected message type")) //nolint:errcheck
	}
}

// serveSession handles bidirectional I/O for an attached client.
func (srv *Server) serveSession(conn net.Conn, s *Session) {
	sub := s.subscribe()
	defer s.unsubscribe(sub)

	// Replay buffered output so the client sees recent terminal state.
	if snap := s.replay.snapshot(); len(snap) > 0 {
		WriteMessage(conn, MsgOutput, snap) //nolint:errcheck
	}

	// Goroutine: session output → client.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case chunk, ok := <-sub:
				if !ok {
					return
				}
				if err := WriteMessage(conn, MsgOutput, chunk); err != nil {
					return
				}
			case <-s.Dead():
				WriteMessage(conn, MsgSessionDead, nil) //nolint:errcheck
				return
			}
		}
	}()

	// Main loop: client input → session.
	for {
		msg, err := ReadMessage(conn)
		if err != nil {
			break
		}
		switch msg.Type {
		case MsgInput:
			s.write(msg.Payload)
		case MsgResize:
			if len(msg.Payload) == 4 {
				cols := int(binary.LittleEndian.Uint16(msg.Payload[0:2]))
				rows := int(binary.LittleEndian.Uint16(msg.Payload[2:4]))
				s.resize(cols, rows)
			}
		case MsgDetachSession:
			conn.Close()
			break
		}
	}

	<-writerDone
	log.Printf("zserver: client detached from session %s", s.ID)
}
