package terminal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"image/color"
)

// TerminalConfig holds the settings needed to construct and run a Terminal.
// Passed at creation time instead of the full application config.
type TerminalConfig struct {
	Rows           int
	Cols           int
	ScrollbackLines int
	MaxBlocks      int
	FG             color.RGBA
	BG             color.RGBA
	Palette        [16]color.RGBA
	CursorBlink    bool
	ShellProgram   string
	ShellArgs      []string
	ShowProcess    bool // whether foreground process polling is enabled
}

// Terminal ties together the screen buffer, PTY, parser, cursor, and input.
// It is the central coordinator consumed by the renderer and game loop.
type Terminal struct {
	Buf              *ScreenBuffer
	Cursor           *Cursor
	pty              PtyBackend
	parser           *Parser
	tcfg             TerminalConfig
	TitleCh          chan string
	CwdCh            chan string
	BellCh           chan struct{}
	ForegroundProcCh chan string // foreground process name, updated by pollForeground

	// Cwd is the last known working directory of the shell.
	// Updated exclusively from the main goroutine (drainCwd).
	Cwd string

	// lastCursorStyle is the last DECSCUSR code applied to Cursor.
	// Used by SyncCursorStyle to avoid redundant SetStyle calls.
	lastCursorStyle int

	// paused is set when the window is idle/unfocused. Polling goroutines
	// (pollCWD, pollForeground) skip work while this is true.
	paused atomic.Bool

	// osc7Active is set true on the first OSC 7 CWD notification from the shell.
	// Once set, QueryCWD skips the lsof fallback — the shell delivers CWD itself.
	osc7Active atomic.Bool

	// osc133Active is set true on the first OSC 133 shell integration event.
	// Once set, periodic ps-based foreground polling is skipped.
	osc133Active atomic.Bool

	// ShellIntCh receives OSC 133 event codes (A/C/D) from the parser.
	// Drained by the game loop to update the foreground process name.
	ShellIntCh chan byte
}

// New creates a Terminal from the given config.
// Call Start() to spawn the shell. Parser is created lazily on first Start call.
func New(tc TerminalConfig) *Terminal {
	buf := NewScreenBuffer(tc.Rows, tc.Cols, tc.ScrollbackLines, tc.MaxBlocks, tc.FG, tc.BG, tc.Palette)

	cur := NewCursor()
	if tc.CursorBlink {
		cur.EnableBlink()
	}

	return &Terminal{
		Buf:              buf,
		Cursor:           cur,
		tcfg:             tc,
		TitleCh:          make(chan string, 4),
		CwdCh:            make(chan string, 4),
		BellCh:           make(chan struct{}, 4),
		ForegroundProcCh: make(chan string, 4),
		ShellIntCh:       make(chan byte, 4),
	}
}

// ensureParser creates the parser on first call. Idempotent.
// Separated from New() so Terminal construction does not depend on Parser.
func (t *Terminal) ensureParser() {
	if t.parser != nil {
		return
	}
	t.parser = NewParser(t.Buf, t.TitleCh, t.CwdCh, t.BellCh, t.ShellIntCh, &t.osc7Active, &t.osc133Active)
}

// Start spawns the shell process.
// dir is the working directory for the shell; empty string inherits the parent process CWD.
func (t *Terminal) Start(dir string) error {
	t.ensureParser()
	shell := t.tcfg.ShellProgram
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
	}

	env := BuildEnv(t.tcfg.Cols, t.tcfg.Rows)

	pty, err := NewPTYManager(shell, t.tcfg.ShellArgs, t.tcfg.Cols, t.tcfg.Rows, env, dir)
	if err != nil {
		return fmt.Errorf("terminal start: %w", err)
	}
	t.pty = pty
	t.pty.StartReader(t.parser, t.Buf, &t.paused)
	return nil
}

// StartCmd launches a specific command (instead of the user's shell) in the PTY.
func (t *Terminal) StartCmd(program string, args []string, dir string) error {
	t.ensureParser()
	env := BuildEnv(t.tcfg.Cols, t.tcfg.Rows)
	pty, err := NewPTYManager(program, args, t.tcfg.Cols, t.tcfg.Rows, env, dir)
	if err != nil {
		return fmt.Errorf("terminal start cmd: %w", err)
	}
	t.pty = pty
	t.pty.StartReader(t.parser, t.Buf, &t.paused)
	return nil
}

// StartWithBackend attaches a pre-created PtyBackend (e.g. from zserver)
// and starts its reader. The backend must already have a live session.
func (t *Terminal) StartWithBackend(backend PtyBackend) error {
	t.ensureParser()
	t.pty = backend
	backend.StartReader(t.parser, t.Buf, &t.paused)
	return nil
}

// SendBytes writes raw bytes to the PTY stdin.
func (t *Terminal) SendBytes(b []byte) {
	if t.pty != nil && len(b) > 0 {
		t.pty.Write(b)
	}
}

// SendDA1Response sends primary device attributes if one is pending (CSI c).
// Safe to call every frame — no-op when nothing is pending.
func (t *Terminal) SendDA1Response() {
	if t.pty == nil {
		return
	}
	t.Buf.Lock()
	if !t.Buf.PendingDA1 {
		t.Buf.Unlock()
		return
	}
	t.Buf.PendingDA1 = false
	t.Buf.Unlock()
	// VT400-class terminal with ANSI color.
	t.pty.Write([]byte("\x1B[?62;22c"))
}

// SendDA2Response sends secondary device attributes if one is pending (CSI > c).
// Safe to call every frame — no-op when nothing is pending.
func (t *Terminal) SendDA2Response() {
	if t.pty == nil {
		return
	}
	t.Buf.Lock()
	if !t.Buf.PendingDA2 {
		t.Buf.Unlock()
		return
	}
	t.Buf.PendingDA2 = false
	t.Buf.Unlock()
	// VT terminal, version 10, ROM cartridge 1.
	t.pty.Write([]byte("\x1B[>0;10;1c"))
}

// SendFocusEvent sends a focus-in or focus-out event when mode 1004 is active.
func (t *Terminal) SendFocusEvent(focused bool) {
	if t.pty == nil {
		return
	}
	t.Buf.RLock()
	enabled := t.Buf.FocusEvents
	t.Buf.RUnlock()
	if !enabled {
		return
	}
	if focused {
		t.pty.Write([]byte("\x1B[I"))
	} else {
		t.pty.Write([]byte("\x1B[O"))
	}
}

// SendPendingResponses drains DCS responses and the Kitty keyboard query.
// Safe to call every frame — no-op when nothing is pending.
func (t *Terminal) SendPendingResponses() {
	if t.pty == nil {
		return
	}

	// Drain XTGETTCAP and other DCS responses queued by the parser.
	t.Buf.Lock()
	responses := t.Buf.PendingDCSResponses
	t.Buf.PendingDCSResponses = nil

	kittyQuery := t.Buf.PendingKittyQuery
	t.Buf.PendingKittyQuery = false

	kittyFlags := 0
	if len(t.Buf.kittyStack) > 0 {
		kittyFlags = t.Buf.kittyStack[len(t.Buf.kittyStack)-1]
	}
	t.Buf.Unlock()

	for _, resp := range responses {
		t.pty.Write(resp)
	}
	if kittyQuery {
		t.pty.Write([]byte(fmt.Sprintf("\x1B[?%du", kittyFlags)))
	}
}

// SyncCursorStyle reads CursorStyleCode from the buffer and updates the Cursor
// when it changes. Call once per frame before rendering.
func (t *Terminal) SyncCursorStyle() {
	t.Buf.RLock()
	code := t.Buf.CursorStyleCode
	t.Buf.RUnlock()
	if code != t.lastCursorStyle {
		t.lastCursorStyle = code
		t.Cursor.SetStyle(code)
	}
}

// SendCPRResponse sends a cursor position report if one is pending (CSI 6 n).
// Safe to call every frame — no-op when nothing is pending.
func (t *Terminal) SendCPRResponse() {
	if t.pty == nil {
		return
	}
	t.Buf.Lock()
	if !t.Buf.PendingCPR {
		t.Buf.Unlock()
		return
	}
	t.Buf.PendingCPR = false
	row := t.Buf.CursorRow + 1
	col := t.Buf.CursorCol + 1
	t.Buf.Unlock()
	resp := fmt.Sprintf("\x1B[%d;%dR", row, col)
	t.pty.Write([]byte(resp))
}

// SendClipboardResponses drains OSC 52 clipboard write requests and query
// responses. Safe to call every frame — no-op when nothing is pending.
func (t *Terminal) SendClipboardResponses() {
	if t.pty == nil {
		return
	}

	t.Buf.Lock()
	writes := t.Buf.PendingClipboardWrite
	t.Buf.PendingClipboardWrite = nil
	query := t.Buf.PendingClipboardQuery
	t.Buf.PendingClipboardQuery = false
	t.Buf.Unlock()

	for _, data := range writes {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = bytes.NewReader(data)
		_ = cmd.Run()
	}

	if query {
		out, err := exec.Command("pbpaste").Output()
		if err == nil {
			encoded := base64.StdEncoding.EncodeToString(out)
			resp := fmt.Sprintf("\x1B]52;c;%s\x1B\\", encoded)
			t.pty.Write([]byte(resp))
		}
	}
}

// SetShowProcess updates the ShowProcess flag (used by RefreshForeground).
func (t *Terminal) SetShowProcess(show bool) {
	t.tcfg.ShowProcess = show
}

// UpdateColors propagates new color settings to the buffer and parser.
// Called during config hot-reload.
func (t *Terminal) UpdateColors(fg, bg color.RGBA, palette [16]color.RGBA) {
	t.Buf.Lock()
	t.Buf.UpdateColors(fg, bg, palette)
	t.parser.SetPalette(palette)
	t.Buf.Unlock()
}

// Resize resizes both the screen buffer and PTY.
func (t *Terminal) Resize(cols, rows int) {
	t.Buf.Lock()
	t.Buf.Resize(rows, cols)
	t.parser.resetTabStops() // rebuild tab stops for new column count
	t.Buf.Unlock()
	if t.pty != nil {
		t.pty.Resize(cols, rows)
	}
}

// Dead returns a channel closed when the shell exits.
func (t *Terminal) Dead() <-chan struct{} {
	if t.pty == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return t.pty.Dead()
}

// Close cleans up the PTY.
func (t *Terminal) Close() {
	if t.pty != nil {
		t.pty.Close()
	}
}

// Pid returns the PID of the shell process, or 0 if not started.
func (t *Terminal) Pid() int {
	if t.pty == nil {
		return 0
	}
	return t.pty.Pid()
}

// SetPaused controls whether background polling goroutines skip work.
// Called by the game loop when the window becomes idle or regains focus.
func (t *Terminal) SetPaused(p bool) { t.paused.Store(p) }

// sessionRenamer is implemented by backends that support session naming (ServerBackend).
type sessionRenamer interface {
	RenameSession(name string) error
}

// RenameSession sends a human-readable name to the backing server session.
// No-op for local PTY sessions. Called when the user renames a server-backed pane.
func (t *Terminal) RenameSession(name string) {
	if r, ok := t.pty.(sessionRenamer); ok {
		r.RenameSession(name) //nolint:errcheck — best-effort; pane CustomName is already applied
	}
}

// HasOSC133 reports whether the shell has emitted at least one OSC 133 event.
// When true, the game loop skips periodic ps-based foreground polling for this terminal.
func (t *Terminal) HasOSC133() bool { return t.osc133Active.Load() }

// QueryCWD performs a one-shot CWD query via lsof and sends the result
// to CwdCh. No-op when the shell already delivers CWD via OSC 7 (osc7Active)
// or when the terminal is idle-suspended.
func (t *Terminal) QueryCWD() {
	if t.paused.Load() || t.osc7Active.Load() {
		return
	}
	pid := t.Pid()
	if pid <= 0 {
		return
	}
	out, err := exec.Command("lsof", "-a", "-p", // #nosec G204 — fixed binary, only argument is numeric PID
		fmt.Sprintf("%d", pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			cwd := line[1:]
			if cwd != "" {
				select {
				case t.CwdCh <- cwd:
				default:
				}
			}
			break
		}
	}
}

// QueryForeground performs a one-shot foreground process query and sends
// the result to ForegroundProcCh if it changed. Safe to call from a goroutine.
func (t *Terminal) QueryForeground() {
	if t.paused.Load() {
		return
	}
	name := t.foregroundProcessName()
	select {
	case t.ForegroundProcCh <- name:
	default:
	}
}

// foregroundProcessName returns the basename of the foreground process in the PTY.
// Returns an empty string when the shell itself is the foreground process or on error.
func (t *Terminal) foregroundProcessName() string {
	if t.pty == nil {
		return ""
	}
	pgid, err := t.pty.ForegroundPgid()
	if err != nil || pgid <= 0 {
		return ""
	}
	// When PGID == shell PID, the shell is foregrounded — show nothing.
	if pgid == t.Pid() {
		return ""
	}
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pgid), "-o", "comm=").Output() // #nosec G204 — fixed binary, only argument is numeric PGID
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// RefreshForeground immediately queries the foreground process and sends the
// result to ForegroundProcCh. Called on focus switch so the status bar updates
// right away without waiting for the next 1-second poll tick.
func (t *Terminal) RefreshForeground() {
	if !t.tcfg.ShowProcess {
		return
	}
	go func() {
		name := t.foregroundProcessName()
		select {
		case t.ForegroundProcCh <- name:
		default:
		}
	}()
}

// BuildEnv constructs the child process environment.
// Exported so external backends (e.g. zserver) can build a compatible env.
func BuildEnv(cols, rows int) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+4)
	skip := map[string]bool{
		"TERM": true, "TERM_PROGRAM": true,
		"COLUMNS": true, "LINES": true,
		"COLORTERM": true,
	}
	for _, e := range env {
		key := e
		for j := 0; j < len(e); j++ {
			if e[j] == '=' {
				key = e[:j]
				break
			}
		}
		if !skip[key] {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered,
		"TERM=xterm-256color",
		"TERM_PROGRAM=zurm",
		"COLORTERM=truecolor",
		fmt.Sprintf("COLUMNS=%d", cols),
		fmt.Sprintf("LINES=%d", rows),
	)
	return filtered
}
