package zserver

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// sessionPTY is a minimal PTY wrapper used by the server.
// It reads raw bytes without VT parsing.
type sessionPTY struct {
	ptmx *os.File
	cmd  *exec.Cmd
	dead chan struct{}
}

func newSessionPTY(shell string, args []string, cols, rows int, env []string, dir string) (*sessionPTY, error) {
	cmd := exec.Command(shell, args...) // #nosec G204
	cmd.Env = env
	cmd.Dir = dir
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows), // #nosec G115
		Cols: uint16(cols), // #nosec G115
	})
	if err != nil {
		return nil, err
	}
	return &sessionPTY{ptmx: ptmx, cmd: cmd, dead: make(chan struct{})}, nil
}

func (s *sessionPTY) write(p []byte) (int, error) { return s.ptmx.Write(p) }

func (s *sessionPTY) resize(cols, rows int) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: uint16(rows), // #nosec G115
		Cols: uint16(cols), // #nosec G115
	})
}

func (s *sessionPTY) pid() int {
	if s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

func (s *sessionPTY) close() {
	if s.cmd.Process != nil {
		s.cmd.Process.Signal(syscall.SIGHUP) // #nosec G104 — best-effort signal; process may already be dead
	}
	s.ptmx.Close() // #nosec G104 — cleanup; error is unactionable during shutdown
}

// startReader launches the goroutine that reads raw PTY output.
// handler is called for each chunk. dead is closed when the shell exits.
func (s *sessionPTY) startReader(handler func([]byte)) {
	go func() {
		defer func() {
			close(s.dead)
			s.cmd.Wait() // #nosec G104 — reap the child process; exit status is tracked via the dead channel, not the error
		}()
		buf := make([]byte, 4096)
		for {
			n, err := s.ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				handler(chunk)
			}
			if err != nil {
				return
			}
		}
	}()
}
