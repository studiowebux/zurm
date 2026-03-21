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
	os.Remove(socketPath) // #nosec G104 — stale socket cleanup; file may not exist, error is irrelevant
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close() // #nosec G104 — error cleanup path, socket is being abandoned
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
func (srv *Server) Close() { srv.listener.Close() } // #nosec G104 — shutdown; error is unactionable

func (srv *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	msg, err := ReadMessage(conn)
	if err != nil {
		return
	}

	switch msg.Type {
	case MsgListSessions:
		list := srv.manager.List()
		data, err := json.Marshal(list)
		if err != nil {
			log.Printf("zserver: marshal session list: %v", err)
			return
		}
		WriteMessage(conn, MsgSessionList, data) // #nosec G104 — fire-and-forget response; client may have disconnected
		return

	case MsgKillSession:
		var req KillSessionRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			WriteMessage(conn, MsgError, []byte("invalid kill request: "+err.Error())) // #nosec G104
			return
		}
		s, ok := srv.manager.Get(req.ID)
		if !ok {
			WriteMessage(conn, MsgError, []byte("session not found: "+req.ID)) // #nosec G104
			return
		}
		log.Printf("zserver: killing session %s (pid %d)", s.ID, s.pid())
		s.Kill()
		return

	case MsgCreateSession:
		var req CreateSessionRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			WriteMessage(conn, MsgError, []byte("invalid create request: "+err.Error())) // #nosec G104 — error response; client may have disconnected
			return
		}
		s, err := srv.manager.Create(req.Shell, req.Args, req.Cols, req.Rows, req.Env, req.Dir)
		if err != nil {
			log.Printf("zserver: create session failed: %v", err)
			WriteMessage(conn, MsgError, []byte(err.Error())) // #nosec G104 — error response; client may have disconnected
			return
		}
		log.Printf("zserver: created session %s (pid %d, %dx%d, dir=%s)", s.ID, s.pid(), req.Cols, req.Rows, req.Dir)
		info := SessionInfo{ID: s.ID, PID: s.pid(), Cols: s.Cols, Rows: s.Rows, Dir: s.Dir}
		data, err := json.Marshal(info)
		if err != nil {
			log.Printf("zserver: marshal session info: %v", err)
			return
		}
		WriteMessage(conn, MsgSessionInfo, data) // #nosec G104 — fire-and-forget; if write fails serveSession will detect the broken connection
		srv.serveSession(conn, s)

	case MsgAttachSession:
		var req AttachSessionRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			WriteMessage(conn, MsgError, []byte("invalid attach request: "+err.Error())) // #nosec G104 — error response; client may have disconnected
			return
		}
		s, ok := srv.manager.Get(req.ID)
		if !ok {
			log.Printf("zserver: attach failed — session not found: %s", req.ID)
			WriteMessage(conn, MsgError, []byte("session not found: "+req.ID)) // #nosec G104 — error response; client may have disconnected
			return
		}
		log.Printf("zserver: client attached to session %s (pid %d)", s.ID, s.pid())
		info := SessionInfo{ID: s.ID, PID: s.pid(), Cols: s.Cols, Rows: s.Rows, Dir: s.Dir}
		data, err := json.Marshal(info)
		if err != nil {
			log.Printf("zserver: marshal session info: %v", err)
			return
		}
		WriteMessage(conn, MsgSessionInfo, data) // #nosec G104 — fire-and-forget; if write fails serveSession will detect the broken connection
		srv.serveSession(conn, s)

	default:
		WriteMessage(conn, MsgError, []byte("unexpected message type")) // #nosec G104 — error response; client may have disconnected
	}
}

// serveSession handles bidirectional I/O for an attached client.
func (srv *Server) serveSession(conn net.Conn, s *Session) {
	sub := s.subscribe()
	defer s.unsubscribe(sub)

	// Replay buffered output so the client sees recent terminal state.
	if snap := s.replay.snapshot(); len(snap) > 0 {
		WriteMessage(conn, MsgOutput, snap) // #nosec G104 — fire-and-forget replay; connection loss detected in reader goroutine
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
				WriteMessage(conn, MsgSessionDead, nil) // #nosec G104 — best-effort notification; client may have already disconnected
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
		case MsgRenameSession:
			var req RenameSessionRequest
			if err := json.Unmarshal(msg.Payload, &req); err == nil {
				s.Rename(req.Name)
				log.Printf("zserver: session %s renamed to %q", s.ID, req.Name)
			}
		case MsgDetachSession:
			conn.Close() // #nosec G104 — intentional teardown; error is unactionable
			return
		}
	}

	<-writerDone
	log.Printf("zserver: client detached from session %s", s.ID)
}
