package terminal

import "testing"

// Close must cancel the terminal's lifecycle context so in-flight CWD/foreground
// query goroutines (and their lsof/ps subprocesses) stop instead of lingering
// until the whole app exits.
func TestClose_CancelsContext(t *testing.T) {
	term := New(TerminalConfig{Rows: 24, Cols: 80})

	select {
	case <-term.ctx.Done():
		t.Fatal("context cancelled before Close")
	default:
	}

	term.Close()

	select {
	case <-term.ctx.Done():
	default:
		t.Fatal("context not cancelled after Close")
	}
}
