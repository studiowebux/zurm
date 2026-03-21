package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
)

// renderSeq is incremented whenever any PTY reader processes new data.
// The main loop compares against its last-seen value to skip needless redraws.
var renderSeq atomic.Uint64

// RenderSeq returns the current render sequence counter.
// When the value changes, at least one pane has new output since last draw.
func RenderSeq() uint64 { return renderSeq.Load() }

// BumpRenderSeq increments the global render sequence counter.
// Called by ServerBackend after feeding output from a remote session.
func BumpRenderSeq() { renderSeq.Add(1) }

// PTYManager spawns a shell process attached to a pseudo-terminal.
//
// Pattern: goroutine-per-concern — one goroutine owns PTY reads,
// the main game loop owns writes via WriteToPTY.
type PTYManager struct {
	ptmx    *os.File
	cmd     *exec.Cmd
	cols    int
	rows    int
	dead    chan struct{}
}

// NewPTYManager spawns the shell and returns a running PTYManager.
// cols and rows are the initial terminal dimensions.
// dir is the working directory for the shell; empty string inherits the parent process CWD.
func NewPTYManager(shell string, args []string, cols, rows int, env []string, dir string) (*PTYManager, error) {
	cmd := exec.Command(shell, args...) // #nosec G204 G702 — shell is user-configured; launching a shell is the purpose of a terminal emulator
	cmd.Env = env
	cmd.Dir = dir

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{ // #nosec G702 — shell is user-configured; terminal emulators launch shells by design
		Rows: uint16(rows), // #nosec G115 — terminal dimensions always fit uint16
		Cols: uint16(cols), // #nosec G115
	})
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	return &PTYManager{
		ptmx: ptmx,
		cmd:  cmd,
		cols: cols,
		rows: rows,
		dead: make(chan struct{}),
	}, nil
}

// StartReader launches the goroutine that reads PTY output and feeds it
// into the parser. Each read batch is processed under the buffer write lock.
// renderSeq is incremented after each batch so the game loop can detect new output.
// The paused flag is checked before acquiring the buffer lock so that resize
// operations can complete without lock starvation during heavy PTY output.
func (m *PTYManager) StartReader(parser *Parser, buf *ScreenBuffer, paused *atomic.Bool) {
	go func() {
		defer func() {
			close(m.dead)
			// Reap the child process to prevent zombie accumulation.
			// Signal the UI first (dead channel) so pane removal is immediate,
			// then block briefly for the kernel to clean up the process entry.
			m.cmd.Wait() //nolint:errcheck // exit status is irrelevant; we just need to reap
		}()
		const ptyReadBufSize = 4096
		scratch := make([]byte, ptyReadBufSize)
		for {
			n, err := m.ptmx.Read(scratch)
			if n > 0 {
				// Spin-wait while paused so resize can acquire the buffer lock
				// without contention from the reader goroutine.
				for paused.Load() {
					runtime.Gosched()
				}
				buf.Lock()
				parser.Feed(scratch[:n])
				buf.Unlock()
				buf.BumpRenderGen()
				renderSeq.Add(1)
			}
			if err != nil {
				return
			}
		}
	}()
}

// Pid returns the PID of the shell process.
func (m *PTYManager) Pid() int {
	if m.cmd.Process == nil {
		return 0
	}
	return m.cmd.Process.Pid
}

// ForegroundPgid returns the foreground process group ID of the PTY via TIOCGPGRP.
// Returns 0 and an error when the ioctl fails (e.g. no foreground process).
func (m *PTYManager) ForegroundPgid() (int, error) {
	var pgid int32
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		m.ptmx.Fd(),
		syscall.TIOCGPGRP,
		uintptr(unsafe.Pointer(&pgid)), //nolint:gosec // G103: required for TIOCGPGRP ioctl
	)
	if errno != 0 {
		return 0, errno
	}
	return int(pgid), nil
}

// Write sends bytes to the PTY's stdin (i.e. the shell's input).
func (m *PTYManager) Write(p []byte) (int, error) {
	return m.ptmx.Write(p)
}

// Resize updates the PTY window size and notifies the child process.
func (m *PTYManager) Resize(cols, rows int) error {
	m.cols = cols
	m.rows = rows
	return pty.Setsize(m.ptmx, &pty.Winsize{
		Rows: uint16(rows), // #nosec G115 — terminal dimensions always fit uint16
		Cols: uint16(cols), // #nosec G115
	})
}

// Dead returns a channel that is closed when the shell process exits.
func (m *PTYManager) Dead() <-chan struct{} {
	return m.dead
}

// Close terminates the child process and closes the PTY.
func (m *PTYManager) Close() {
	if m.cmd.Process != nil {
		m.cmd.Process.Signal(syscall.SIGHUP)
	}
	m.ptmx.Close()
}
