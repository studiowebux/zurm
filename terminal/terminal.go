package terminal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/studiowebux/zurm/config"
)

// Terminal ties together the screen buffer, PTY, parser, cursor, and input.
// It is the central coordinator consumed by the renderer and game loop.
type Terminal struct {
	Buf              *ScreenBuffer
	Cursor           *Cursor
	pty              *PTYManager
	parser           *Parser
	cfg              *config.Config
	TitleCh          chan string
	CwdCh            chan string
	ForegroundProcCh chan string // foreground process name, updated by pollForeground

	// Cwd is the last known working directory of the shell.
	// Updated exclusively from the main goroutine (drainCwd).
	Cwd string

	// lastCursorStyle is the last DECSCUSR code applied to Cursor.
	// Used by SyncCursorStyle to avoid redundant SetStyle calls.
	lastCursorStyle int
}

// New creates a Terminal from the given config.
// Call Start() to spawn the shell.
func New(cfg *config.Config) *Terminal {
	palette := cfg.Palette()
	fg := config.ParseHexColor(cfg.Colors.Foreground)
	bg := config.ParseHexColor(cfg.Colors.Background)

	titleCh := make(chan string, 4)
	cwdCh := make(chan string, 4)
	buf := NewScreenBuffer(cfg.Window.Rows, cfg.Window.Columns, cfg.Scrollback.Lines, fg, bg, palette)
	parser := NewParser(buf, titleCh, cwdCh)

	cur := NewCursor()
	if cfg.Input.CursorBlink {
		cur.EnableBlink()
	}

	return &Terminal{
		Buf:              buf,
		Cursor:           cur,
		parser:           parser,
		cfg:              cfg,
		TitleCh:          titleCh,
		CwdCh:            cwdCh,
		ForegroundProcCh: make(chan string, 4),
	}
}

// Start spawns the shell process.
// dir is the working directory for the shell; empty string inherits the parent process CWD.
func (t *Terminal) Start(dir string) error {
	shell := t.cfg.Shell.Program
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
	}

	env := buildEnv(t.cfg.Window.Columns, t.cfg.Window.Rows)

	pty, err := NewPTYManager(shell, t.cfg.Shell.Args, t.cfg.Window.Columns, t.cfg.Window.Rows, env, dir)
	if err != nil {
		return fmt.Errorf("terminal start: %w", err)
	}
	t.pty = pty
	t.pty.StartReader(t.parser, t.Buf)
	go t.pollCWD()
	if t.cfg.StatusBar.ShowProcess {
		go t.pollForeground()
	}
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

// UpdateColors propagates new color settings to the buffer and parser.
// Called during config hot-reload.
func (t *Terminal) UpdateColors(cfg *config.Config) {
	fg := config.ParseHexColor(cfg.Colors.Foreground)
	bg := config.ParseHexColor(cfg.Colors.Background)
	palette := cfg.Palette()
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

// pollCWD runs in a background goroutine, querying the shell process CWD
// via lsof every 2 seconds. This is the fallback for shells that do not
// send OSC 7. Results are sent to CwdCh; OSC 7 updates take priority
// because they arrive immediately on cd.
func (t *Terminal) pollCWD() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	dead := t.Dead()
	for {
		select {
		case <-dead:
			return
		case <-ticker.C:
			pid := t.Pid()
			if pid <= 0 {
				continue
			}
			out, err := exec.Command("lsof", "-a", "-p", // #nosec G204 — fixed binary, only argument is numeric PID
				fmt.Sprintf("%d", pid), "-d", "cwd", "-Fn").Output()
			if err != nil {
				continue
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
	}
}

// pollForeground runs in a background goroutine, querying the foreground
// process group via TIOCGPGRP every second and resolving its name via ps.
// Results are sent to ForegroundProcCh when the name changes.
func (t *Terminal) pollForeground() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	dead := t.Dead()
	var lastName string
	for {
		select {
		case <-dead:
			return
		case <-ticker.C:
			name := t.foregroundProcessName()
			if name != lastName {
				lastName = name
				select {
				case t.ForegroundProcCh <- name:
				default:
				}
			}
		}
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
	if !t.cfg.StatusBar.ShowProcess {
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

// buildEnv constructs the child process environment.
func buildEnv(cols, rows int) []string {
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
