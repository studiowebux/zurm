package main

import (
	"bytes"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/terminal"
)

func (g *Game) handleResize() {
	w, h := ebiten.WindowSize()
	if w == g.winW && h == g.winH {
		return
	}
	g.winW = w
	g.winH = h
	physW, physH := g.physSize()
	g.renderer.SetSize(physW, physH)
	g.renderer.SetLayoutDirty()
	g.rec.Recorder.Resize(physW, physH)

	paneRect := g.contentRect()

	// Pause all PTY readers before resizing to avoid lock starvation.
	// Without this, heavy PTY output (e.g. Claude Code streaming) continuously
	// holds the buffer write lock, preventing Resize from acquiring it.
	for _, t := range g.tabMgr.Tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.SetPaused(true)
		}
	}

	// Recompute rects for every tab's layout.
	for _, t := range g.tabMgr.Tabs {
		setPaneHeaders(t.Layout, g.font.CellH)
		t.Layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
		}
	}

	// Resume PTY readers after all resizes are complete.
	// Skip if window is idle-suspended — readers should stay paused.
	if !g.wfocus.Suspended {
		for _, t := range g.tabMgr.Tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(false)
			}
		}
	}

	// When zoomed, the focused pane must fill the entire pane area.
	// ComputeRects above set it to the normal split rect — override it.
	// Clear HeaderH — zoomed pane has no header (only one visible pane).
	if g.zoomed && g.activeFocused() != nil {
		g.activeFocused().HeaderH = 0
		g.activeFocused().Rect = paneRect
		cols := (paneRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW
		rows := (paneRect.Dy() - g.cfg.Window.Padding) / g.font.CellH
		if cols < 1 {
			cols = 1
		}
		if rows < 1 {
			rows = 1
		}
		g.activeFocused().Cols = cols
		g.activeFocused().Rows = rows
		g.activeFocused().Term.Resize(cols, rows)
	}

	g.render.Dirty = true
}

func (g *Game) drainTitle() {
	if g.activeFocused() == nil || g.tabMgr.ActiveIdx >= len(g.tabMgr.Tabs) {
		return
	}
	select {
	case title := <-g.activeFocused().Term.TitleCh:
		clean := sanitizeTitle(title) // SEC-003
		// Do not overwrite a user-set tab name with OSC 0/2 from the shell.
		if !g.tabMgr.Tabs[g.tabMgr.ActiveIdx].UserRenamed {
			g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Title = clean
		}
		ebiten.SetWindowTitle(clean)
		g.render.Dirty = true
	default:
	}
}


// drainCwd reads the latest CWD from the focused pane's OSC 7 channel.
// When the CWD changes it kicks off an async git status lookup via the poller.
func (g *Game) drainCwd() {
	if g.activeFocused() == nil {
		return
	}
	select {
	case cwd := <-g.activeFocused().Term.CwdCh:
		if cwd != g.status.Bar.Cwd {
			g.status.Bar.Cwd = cwd
			g.activeFocused().Term.Cwd = cwd
			g.status.Bar.GitBranch = ""
			g.status.Bar.GitCommit = ""
			g.status.Bar.GitDirty = 0
			g.status.Bar.GitStaged = 0
			g.status.Bar.GitAhead = 0
			g.status.Bar.GitBehind = 0
			if g.cfg.StatusBar.ShowGit {
				g.status.Poller.StartGitQuery(cwd)
			}
			g.render.Dirty = true
		}
	default:
	}
}

// drainBell reads BEL events from all panes and triggers visual/audio/dock feedback.
func (g *Game) drainBell() {
	dur := time.Duration(g.cfg.Bell.DurationMs) * time.Millisecond
	now := time.Now()
	fired := false

	// Active tab panes — visual border flash on bell.
	for _, leaf := range g.activeLayout().Leaves() {
		select {
		case <-leaf.Pane.Term.BellCh:
			if g.cfg.Bell.Style != "none" {
				leaf.Pane.BellUntil = now.Add(dur)
			}
			fired = true
			g.render.Dirty = true
		default:
		}
	}

	// Background tabs — mark tab activity on bell.
	for i, t := range g.tabMgr.Tabs {
		if i == g.tabMgr.ActiveIdx {
			continue
		}
		for _, leaf := range t.Layout.Leaves() {
			select {
			case <-leaf.Pane.Term.BellCh:
				t.HasActivity = true
				t.HasBell = true
				fired = true
				g.render.Dirty = true
			default:
			}
		}
	}

	if !fired {
		return
	}

	// Debounce sound + dock notifications (500ms).
	if now.Sub(g.status.LastBell) < bellDebounce {
		return
	}
	g.status.LastBell = now

	if g.cfg.Bell.Sound {
		go playBellSound()
	}

	// Dock badge + bounce only when the window is not focused.
	if !ebiten.IsFocused() {
		setDockBadge()
		requestDockAttention()
	}
}

// drainBlockDone reads completed command block output from all panes.
// Background tab channels are drained silently to prevent buildup.
func (g *Game) drainBlockDone() {
	// Drain all active tab panes and capture completed commands for the vault.
	for _, leaf := range g.activeLayout().Leaves() {
		select {
		case <-leaf.Pane.Term.Buf.BlockDoneCh:
			// Capture the command text from the completed block for the vault.
			if g.vlt.Vault != nil {
				leaf.Pane.Term.Buf.RLock()
				if ab := leaf.Pane.Term.Buf.ActiveBlock(); ab == nil {
					// Active block is nil after D fires — check the most recent completed block.
					blocks := leaf.Pane.Term.Buf.Blocks
					if len(blocks) > 0 {
						cmd := strings.TrimSpace(blocks[len(blocks)-1].CommandText)
						if cmd != "" {
							g.vlt.Vault.Add(cmd)
						}
					}
				}
				leaf.Pane.Term.Buf.RUnlock()
			}
		default:
		}
	}

	// Drain background tabs silently.
	for i, t := range g.tabMgr.Tabs {
		if i == g.tabMgr.ActiveIdx {
			continue
		}
		for _, leaf := range t.Layout.Leaves() {
			select {
			case <-leaf.Pane.Term.Buf.BlockDoneCh:
			default:
			}
		}
	}
}

// updateVaultSuggestion extracts the current line from the focused pane's buffer
// and queries the vault for a prefix-matched suggestion. The result is stored in
// g.vlt.Suggest for the renderer to draw as ghost text.
func (g *Game) updateVaultSuggestion() {
	if g.vlt.Vault == nil || !g.cfg.Vault.GhostText {
		g.vlt.Suggest = ""
		return
	}
	if g.activeFocused() == nil {
		return
	}

	buf := g.activeFocused().Term.Buf
	buf.RLock()
	// No suggestions when scrolled back, in alt screen, or cursor is hidden.
	if buf.ViewOffset != 0 || buf.IsAltActive() || !buf.CursorVisible {
		buf.RUnlock()
		g.vlt.Suggest = ""
		return
	}

	// Extract the text on the cursor row up to the cursor column.
	row := buf.CursorRow
	col := buf.CursorCol
	cells := buf.Cells
	if row < 0 || row >= len(cells) || col <= 0 {
		buf.RUnlock()
		g.vlt.Suggest = ""
		return
	}

	var line strings.Builder
	for c := 0; c < col && c < len(cells[row]); c++ {
		cell := cells[row][c]
		if cell.Width == 0 {
			continue // skip continuation cells
		}
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		line.WriteRune(ch)
	}
	buf.RUnlock()

	lineStr := line.String()
	if lineStr == g.vlt.LineCache {
		return // no change — keep current suggestion
	}
	g.vlt.LineCache = lineStr
	g.vlt.Skip = 0
	g.vlt.Suggest = g.vlt.Vault.Suggest(lineStr, g.vlt.Skip)
}

// drainGitBranch reads a completed async git info result from the poller.
func (g *Game) drainGitBranch() {
	if info, ok := g.status.Poller.DrainGit(); ok {
		g.status.Bar.GitBranch = info.Branch
		g.status.Bar.GitCommit = info.Commit
		g.status.Bar.GitDirty = info.Dirty
		g.status.Bar.GitStaged = info.Staged
		g.status.Bar.GitAhead = info.Ahead
		g.status.Bar.GitBehind = info.Behind
		g.render.Dirty = true
	}
}

// drainForeground reads the latest foreground process name from all visible panes
// and updates ProcName on each. The focused pane's name also feeds the status bar.
func (g *Game) drainForeground() {
	if !g.cfg.StatusBar.ShowProcess {
		return
	}
	if g.tabMgr.ActiveIdx >= len(g.tabMgr.Tabs) {
		return
	}
	for _, leaf := range g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Layout.Leaves() {
		p := leaf.Pane
		select {
		case name := <-p.Term.ForegroundProcCh:
			if name != p.ProcName {
				p.ProcName = name
				g.render.Dirty = true
				if p == g.activeFocused() {
					g.status.Bar.ForegroundProc = name
				}
			}
		default:
		}
	}
}

// drainShellIntegration reads OSC 133 shell integration events from all visible
// panes and updates the foreground process name event-driven (no polling).
// A/D = shell is at prompt → clear proc name. C = command starting → query once.
func (g *Game) drainShellIntegration() {
	if !g.cfg.StatusBar.ShowProcess {
		return
	}
	if g.tabMgr.ActiveIdx >= len(g.tabMgr.Tabs) {
		return
	}
	for _, leaf := range g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Layout.Leaves() {
		p := leaf.Pane
		select {
		case kind := <-p.Term.ShellIntCh:
			switch kind {
			case 'A', 'D':
				// Shell at prompt — clear foreground process.
				if p.ProcName != "" {
					p.ProcName = ""
					g.render.Dirty = true
					if p == g.activeFocused() {
						g.status.Bar.ForegroundProc = ""
					}
				}
			case 'C':
				// Command about to execute — one-shot query for foreground name.
				go p.Term.QueryForeground(g.ctx)
			}
		default:
		}
	}
}

// pollStatusOnOutput triggers CWD and foreground process queries when PTY
// output arrives. Poll intervals are managed by the StatusPoller.
func (g *Game) pollStatusOnOutput() {
	seq := terminal.RenderSeq()

	if g.status.Poller.ShouldPollCwd(seq) {
		if g.activeFocused() != nil {
			go g.activeFocused().Term.QueryCWD(g.ctx)
		}
	}

	if g.cfg.StatusBar.ShowProcess && g.status.Poller.ShouldPollFg(seq) && g.tabMgr.ActiveIdx < len(g.tabMgr.Tabs) {
		for _, leaf := range g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Layout.Leaves() {
			if !leaf.Pane.Term.HasOSC133() {
				go leaf.Pane.Term.QueryForeground(g.ctx)
			}
		}
	}
}


// sanitizeTitle strips control characters and caps length (SEC-003).
func sanitizeTitle(s string) string {
	out := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
	if r := []rune(out); len(r) > 256 {
		out = string(r[:256])
	}
	return out
}

// requestClipboard spawns a background goroutine that reads the system clipboard
// via pbpaste and sends the result to clipboardCh. Non-blocking: if a previous
// request is still pending, the new result replaces it.
func (g *Game) requestClipboard() {
	ctx := g.ctx
	go func() {
		out, err := exec.CommandContext(ctx, "pbpaste").Output() // #nosec G204 — fixed binary
		if err != nil || len(out) == 0 {
			return
		}
		clip := strings.ToValidUTF8(string(out), "")
		select {
		case g.clipboardCh <- clip:
		case <-ctx.Done():
		}
	}()
}

// handlePaste triggers an async clipboard read. The result is consumed by
// drainTerminalPaste on the next frame. Called from Cmd+V in the main input
// handler, context menu, and palette.
func (g *Game) handlePaste() {
	g.requestClipboard()
}

// drainTerminalPaste consumes a pending clipboard result and sends it to the
// focused PTY with NFC normalization, line-ending conversion, and bracketed
// paste wrapping. Called every frame from the main input path when no overlay
// is consuming the clipboard.
func (g *Game) drainTerminalPaste() bool {
	select {
	case clip := <-g.clipboardCh:
		if g.activeFocused() == nil {
			return false
		}
		out := norm.NFC.Bytes([]byte(clip))
		out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\r"))
		out = bytes.ReplaceAll(out, []byte("\n"), []byte("\r"))

		g.activeFocused().Term.Buf.RLock()
		bracketed := g.activeFocused().Term.Buf.BracketedPaste
		g.activeFocused().Term.Buf.RUnlock()
		if bracketed {
			g.activeFocused().Term.SendBytes([]byte("\x1B[200~"))
			g.activeFocused().Term.SendBytes(out)
			g.activeFocused().Term.SendBytes([]byte("\x1B[201~"))
		} else {
			g.activeFocused().Term.SendBytes(out)
		}
		return true
	default:
		return false
	}
}
