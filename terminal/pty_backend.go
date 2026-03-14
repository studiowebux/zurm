package terminal

import "sync/atomic"

// PtyBackend abstracts the PTY I/O layer so Terminal works with either a
// local process (LocalBackend / PTYManager) or a remote zurm-server session
// (zserver.ServerBackend). Callers never reference PTYManager directly.
type PtyBackend interface {
	// Write sends raw input bytes to the shell.
	Write(p []byte) (int, error)
	// Resize updates the terminal dimensions on the backing PTY.
	Resize(cols, rows int) error
	// Dead returns a channel closed when the session ends.
	Dead() <-chan struct{}
	// Close terminates or detaches from the session.
	Close()
	// Pid returns the shell process ID, or 0 if unavailable.
	Pid() int
	// ForegroundPgid returns the foreground process group ID, or 0 if unavailable.
	ForegroundPgid() (int, error)
	// StartReader launches the goroutine that reads output and feeds it into
	// the parser. Must be called exactly once after the backend is created.
	StartReader(parser *Parser, buf *ScreenBuffer, paused *atomic.Bool)
}
