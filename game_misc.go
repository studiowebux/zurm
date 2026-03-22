package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"slices"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/session"
	"github.com/studiowebux/zurm/recorder"
	"github.com/studiowebux/zurm/terminal"
	"github.com/studiowebux/zurm/vault"
)

// urlHoverState groups URL hover detection fields.
type urlHoverState struct {
	HoveredURL *terminal.URLMatch // URL under the cursor, nil if none
	Matches    []terminal.URLMatch // cached URL matches for the focused pane
}

// recState groups screen recording fields (FFMPEG pipe → MP4).
type recState struct {
	Recorder      *recorder.Recorder
	Done          chan string // background Stop() completion signal
	Buf           []byte     // reusable pixel buffer (avoids per-frame alloc)
	LastFrame     time.Time  // throttle frame capture to 30fps
	LastStatusSec int64      // throttle status bar update (blink + file size)
}

// vaultState groups command vault fields (encrypted history + ghost text suggestions).
type vaultState struct {
	Vault     *vault.Vault
	Suggest   string // current ghost text (completion tail)
	LineCache string // last line used for suggestion (avoids recomputing every frame)
	Skip      int    // Tab cycles through matches: 0=most recent, 1=next, etc.
}

// --- Tab renaming ---

// startRenameTab enters inline rename mode for tab at index idx.
func (g *Game) startRenameTab(idx int) {
	if idx < 0 || idx >= len(g.tabMgr.Tabs) {
		return
	}
	// Cancel any existing rename first.
	g.cancelRename()
	g.tabMgr.Tabs[idx].Renaming = true
	g.tabMgr.Tabs[idx].RenameText = g.tabMgr.Tabs[idx].Title
	g.tabMgr.Tabs[idx].RenameCursorPos = len([]rune(g.tabMgr.Tabs[idx].Title))
}

// commitRename applies the rename text and exits rename mode.
// If the tab's focused pane is server-backed, propagate the name to the server
// so it appears in the session list / attach palette.
func (g *Game) commitRename() {
	for _, t := range g.tabMgr.Tabs {
		if t.Renaming {
			name := sanitizeTitle(t.RenameText)
			t.Title = name
			t.UserRenamed = true
			t.Renaming = false
			t.RenameText = ""
			if t.Focused != nil && t.Focused.ServerSessionID != "" {
				t.Focused.Term.RenameSession(name)
			}
			break
		}
	}
}

// cancelRename exits rename mode without applying changes.
func (g *Game) cancelRename() {
	for _, t := range g.tabMgr.Tabs {
		if t.Renaming {
			t.Renaming = false
			t.RenameText = ""
			break
		}
	}
}

// renamingTabIdx returns the index of the tab currently being renamed, or -1.
func (g *Game) renamingTabIdx() int {
	for i, t := range g.tabMgr.Tabs {
		if t.Renaming {
			return i
		}
	}
	return -1
}

// --- Tab notes ---

// startNoteEdit enters inline note editing mode for tab at index idx.
func (g *Game) startNoteEdit(idx int) {
	if idx < 0 || idx >= len(g.tabMgr.Tabs) {
		return
	}
	g.cancelNote()
	g.cancelRename()
	g.tabMgr.Tabs[idx].Noting = true
	g.tabMgr.Tabs[idx].NoteText = g.tabMgr.Tabs[idx].Note
	g.tabMgr.Tabs[idx].NoteCursorPos = len([]rune(g.tabMgr.Tabs[idx].Note))
}

// commitNote applies the note text and exits note editing mode.
func (g *Game) commitNote() {
	for _, t := range g.tabMgr.Tabs {
		if t.Noting {
			t.Note = strings.TrimSpace(t.NoteText)
			t.Noting = false
			t.NoteText = ""
			break
		}
	}
}

// cancelNote exits note editing mode without applying changes.
func (g *Game) cancelNote() {
	for _, t := range g.tabMgr.Tabs {
		if t.Noting {
			t.Noting = false
			t.NoteText = ""
			break
		}
	}
}

// notingTabIdx returns the index of the tab currently in note editing mode, or -1.
func (g *Game) notingTabIdx() int {
	for i, t := range g.tabMgr.Tabs {
		if t.Noting {
			return i
		}
	}
	return -1
}

// --- Text editing ---

// handleTextEdit is the shared text-editing loop for Note, Rename, and PaneRename inputs.
// cancel is called on Escape; commit on Enter (return true to stop processing);
// getText/setText load and store the TextInput state; repeat tracks key-repeat timing.
func (g *Game) handleTextEdit(
	cancel func(),
	commit func(text string, cursor int) bool,
	getText func() (string, int),
	setText func(string, int),
	repeat *TextInputRepeat,
) {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		cancel()
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	text, cursor := getText()
	ti := &TextInput{Text: text, CursorPos: cursor}

	// Edge-triggered: Enter commits.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		if pressed && !g.input.PrevKeys[key] {
			if commit(ti.Text, ti.CursorPos) {
				return
			}
		}
		g.input.PrevKeys[key] = pressed
	}
	g.input.PrevKeys[ebiten.KeyMeta] = meta
	g.input.PrevKeys[ebiten.KeyAlt] = alt

	// Cmd+V — async clipboard paste.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		g.requestClipboard()
	}
	select {
	case clip := <-g.clipboardCh:
		ti.AddString(clip)
	default:
	}

	ti.Update(repeat, meta, alt)
	setText(ti.Text, ti.CursorPos)
}

func (g *Game) handleNoteInput() {
	idx := g.notingTabIdx()
	if idx < 0 {
		return
	}
	g.handleTextEdit(
		g.cancelNote,
		func(text string, cursor int) bool {
			g.tabMgr.Tabs[idx].NoteText = text
			g.tabMgr.Tabs[idx].NoteCursorPos = cursor
			g.commitNote()
			return false
		},
		func() (string, int) { return g.tabMgr.Tabs[idx].NoteText, g.tabMgr.Tabs[idx].NoteCursorPos },
		func(t string, c int) { g.tabMgr.Tabs[idx].NoteText = t; g.tabMgr.Tabs[idx].NoteCursorPos = c },
		&g.repeats.Note,
	)
}

func (g *Game) handleRenameInput() {
	idx := g.renamingTabIdx()
	if idx < 0 {
		return
	}
	g.handleTextEdit(
		g.cancelRename,
		func(text string, cursor int) bool {
			g.tabMgr.Tabs[idx].RenameText = text
			g.tabMgr.Tabs[idx].RenameCursorPos = cursor
			g.commitRename()
			return false
		},
		func() (string, int) { return g.tabMgr.Tabs[idx].RenameText, g.tabMgr.Tabs[idx].RenameCursorPos },
		func(t string, c int) { g.tabMgr.Tabs[idx].RenameText = t; g.tabMgr.Tabs[idx].RenameCursorPos = c },
		&g.repeats.Rename,
	)
}

func (g *Game) handlePaneRenameInput() {
	g.handleTextEdit(
		g.cancelPaneRename,
		func(text string, cursor int) bool {
			g.activeFocused().RenameText = text
			g.activeFocused().RenameCursorPos = cursor
			g.commitPaneRename()
			return true // stop processing after pane rename commit
		},
		func() (string, int) { return g.activeFocused().RenameText, g.activeFocused().RenameCursorPos },
		func(t string, c int) { g.activeFocused().RenameText = t; g.activeFocused().RenameCursorPos = c },
		&g.repeats.PaneRename,
	)
}

// --- Session persistence ---

// saveSession persists the current tab state to the session file.
// Called before every exit path. Errors are logged but do not block exit.
// Only saves if AutoSave is enabled in config.
func (g *Game) saveSession() {
	if !g.cfg.Session.Enabled || !g.cfg.Session.AutoSave {
		return
	}
	g.doSaveSession()
}

// manualSaveSession saves the session regardless of AutoSave setting.
// Called from command palette for explicit user saves.
func (g *Game) manualSaveSession() {
	if !g.cfg.Session.Enabled {
		g.flashStatus("Session saving is disabled in config")
		return
	}
	g.doSaveSession()
	g.flashStatus("Session saved")
}

// doSaveSession performs the actual session save operation.
func (g *Game) doSaveSession() {
	data := &session.SessionData{
		Version:   1,
		ActiveTab: g.tabMgr.ActiveIdx,
	}
	for _, t := range g.tabMgr.Tabs {
		leaves := t.Layout.Leaves()
		if len(leaves) == 0 {
			continue
		}
		// Use first pane's CWD as fallback for tab CWD
		term := leaves[0].Pane.Term
		td := session.TabData{
			Cwd:         term.Cwd,
			Title:       t.Title,
			UserRenamed: t.UserRenamed,
			Note:        t.Note,
			Layout:      serializePaneLayout(t.Layout), // Save the pane layout
		}
		if t.PinnedSlot != 0 {
			td.PinnedSlot = string(t.PinnedSlot)
		}
		data.Tabs = append(data.Tabs, td)
	}
	if err := session.Save(data); err != nil {
		log.Printf("zurm: session save: %v", err)
	}
}

// serializePaneLayout converts a pane.LayoutNode tree to session.PaneLayout for persistence.
func serializePaneLayout(node *pane.LayoutNode) *session.PaneLayout {
	if node == nil {
		return nil
	}

	layout := &session.PaneLayout{
		Ratio: node.Ratio,
	}

	switch node.Kind {
	case pane.Leaf:
		layout.Kind = "leaf"
		if node.Pane != nil && node.Pane.Term != nil {
			layout.Cwd = node.Pane.Term.Cwd
		}
		if node.Pane != nil {
			layout.CustomName = node.Pane.CustomName
			layout.ServerSessionID = node.Pane.ServerSessionID
		}
	case pane.HSplit:
		layout.Kind = "hsplit"
		layout.Left = serializePaneLayout(node.Left)
		layout.Right = serializePaneLayout(node.Right)
	case pane.VSplit:
		layout.Kind = "vsplit"
		layout.Left = serializePaneLayout(node.Left)
		layout.Right = serializePaneLayout(node.Right)
	}

	return layout
}

// deserializePaneLayout reconstructs a pane.LayoutNode tree from saved session.PaneLayout.
func deserializePaneLayout(cfg *config.Config, rect image.Rectangle, cellW, cellH int, layout *session.PaneLayout) (*pane.LayoutNode, error) {
	if layout == nil {
		return nil, fmt.Errorf("nil layout")
	}

	switch layout.Kind {
	case "leaf":
		dir := sanitizeDirectory(layout.Cwd)
		var p *pane.Pane
		var err error
		if layout.ServerSessionID != "" {
			// Reconnect to the zurm-server session (Mode B).
			// Falls back to local PTY if the server or session is gone.
			p, err = pane.NewServer(cfg, rect, cellW, cellH, dir, layout.ServerSessionID)
		} else {
			p, err = pane.New(cfg, rect, cellW, cellH, dir)
		}
		if err != nil {
			return nil, err
		}
		p.CustomName = layout.CustomName
		return pane.NewLeaf(p), nil

	case "hsplit", "vsplit":
		// Recursively deserialize children
		left, err := deserializePaneLayout(cfg, rect, cellW, cellH, layout.Left)
		if err != nil {
			return nil, err
		}
		right, err := deserializePaneLayout(cfg, rect, cellW, cellH, layout.Right)
		if err != nil {
			return nil, err
		}

		kind := pane.HSplit
		if layout.Kind == "vsplit" {
			kind = pane.VSplit
		}

		node := &pane.LayoutNode{
			Kind:  kind,
			Left:  left,
			Right: right,
			Ratio: layout.Ratio,
		}

		// Ensure ratio is valid
		if node.Ratio <= 0 || node.Ratio >= 1 {
			node.Ratio = 0.5
		}

		return node, nil

	default:
		return nil, fmt.Errorf("unknown layout kind: %s", layout.Kind)
	}
}

// --- Stats ---

// collectStats populates the stats overlay with current runtime metrics.
// Uses runtime/metrics (non-STW) for heap data and debug.ReadGCStats for
// GC pause data, avoiding the stop-the-world pause from runtime.ReadMemStats.
func (g *Game) collectStats() {
	// Heap stats — non-STW via runtime/metrics.
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
	}
	metrics.Read(samples)

	// GC stats — lighter than ReadMemStats; holds heap lock briefly, not STW.
	var gcStats debug.GCStats
	debug.ReadGCStats(&gcStats)

	g.statsState.TPS = ebiten.ActualTPS()
	g.statsState.FPS = ebiten.ActualFPS()
	g.statsState.Goroutines = runtime.NumGoroutine()
	g.statsState.HeapAlloc = samples[0].Value.Uint64()
	g.statsState.HeapSys = samples[1].Value.Uint64()
	if gcStats.NumGC >= 0 {
		g.statsState.NumGC = uint32(gcStats.NumGC) // #nosec G115 — NumGC is a monotonic counter, always non-negative
	}
	if len(gcStats.Pause) > 0 && gcStats.Pause[0] >= 0 {
		g.statsState.GCPauseNs = uint64(gcStats.Pause[0]) // #nosec G115 — pause duration is always non-negative
	}
	g.statsState.TabCount = len(g.tabMgr.Tabs)
	paneCount := 0
	for _, t := range g.tabMgr.Tabs {
		paneCount += len(t.Layout.Leaves())
	}
	g.statsState.PaneCount = paneCount
	if g.activeFocused() != nil {
		g.activeFocused().Term.Buf.RLock()
		g.statsState.BufRows = g.activeFocused().Term.Buf.Rows
		g.statsState.BufCols = g.activeFocused().Term.Buf.Cols
		g.statsState.Scrollback = g.activeFocused().Term.Buf.ScrollbackLen()
		g.activeFocused().Term.Buf.RUnlock()
	}
	g.statsLastTick = time.Now()
	g.screenDirty = true
}

// --- Utilities ---

// flashStatus shows msg in the status bar for 3 seconds.
func (g *Game) flashStatus(msg string) {
	g.statusBarState.FlashMessage = msg
	g.flashExpiry = time.Now().Add(3 * time.Second)
	g.screenDirty = true
}

// installShellHooks appends OSC 133 shell integration hooks to ~/.zshrc or
// ~/.bashrc, guarded by idempotency markers so repeated calls are safe.
func (g *Game) installShellHooks() {
	shell := g.cfg.Shell.Program
	if shell == "" {
		shell = os.Getenv("SHELL")
	}

	const markerStart = "# zurm-hooks-start"
	const markerEnd = "# zurm-hooks-end"

	var rcFile, hooks string
	switch {
	case strings.HasSuffix(shell, "zsh"):
		rcFile = filepath.Join(os.Getenv("HOME"), ".zshrc")
		// _zurm_cmd_started guards against emitting D before the first command.
		hooks = markerStart + `
_zurm_precmd() {
  local _code=$?
  [[ -n $_zurm_cmd_started ]] && printf '\033]133;D;%d\007' "$_code"
  unset _zurm_cmd_started
  printf '\033]133;A\007'
}
_zurm_preexec() { _zurm_cmd_started=1; printf '\033]133;C\007'; }
precmd_functions+=(_zurm_precmd)
preexec_functions+=(_zurm_preexec)
# Emit B (prompt end) via zle-line-init — fires after the prompt is drawn,
# works with any prompt tool (starship, p10k, oh-my-zsh).
_zurm_line_init() { print -n '\033]133;B\007'; }
zle -N zle-line-init _zurm_line_init
` + markerEnd + "\n"
	case strings.HasSuffix(shell, "bash"):
		rcFile = filepath.Join(os.Getenv("HOME"), ".bashrc")
		hooks = markerStart + `
PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }printf '\033]133;D;%s\007' \"$?\"; printf '\033]133;A\007'"
` + markerEnd + "\n"
	default:
		g.flashStatus("Auto-hooks not supported for: " + filepath.Base(shell))
		return
	}

	existing, err := os.ReadFile(rcFile) // #nosec G304 G703 — rcFile is ~/.zshrc derived from os.UserHomeDir()
	if err != nil && !os.IsNotExist(err) {
		g.flashStatus("Shell hooks: cannot read " + filepath.Base(rcFile))
		return
	}
	if strings.Contains(string(existing), markerStart) {
		g.flashStatus("Shell hooks already installed in " + filepath.Base(rcFile))
		return
	}

	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // #nosec G304 G302 G703 — path from UserHomeDir; 0644 is correct for shell RC files
	if err != nil {
		g.flashStatus("Shell hooks: cannot write " + filepath.Base(rcFile))
		return
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + hooks); err != nil {
		g.flashStatus("Shell hooks: write error")
		return
	}
	g.flashStatus("Shell hooks installed — restart your shell")
}

// toggleRecording starts or stops a screen recording (FFMPEG → MP4).
// Stop runs in a background goroutine because ffmpeg finalization can block
// for several seconds. Completion is communicated via g.rec.Done.
func (g *Game) toggleRecording() {
	if g.rec.Recorder.Active() {
		g.flashStatus("Saving recording…")
		ctx := g.ctx
		go func() {
			path, err := g.rec.Recorder.Stop()
			var msg string
			if err != nil {
				msg = "Recording failed: " + err.Error()
			} else {
				msg = "Saved: " + filepath.Base(path)
			}
			select {
			case g.rec.Done <- msg:
			case <-ctx.Done():
			}
		}()
		return
	}
	// Sync recorder dimensions to match what Layout() produces — the DPI
	// round-trip (physical→logical→physical) can lose a pixel, which would
	// cause AddFrame's size check to silently drop every frame.
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	g.rec.Recorder.Resize(physW, physH)
	if err := g.rec.Recorder.Start(); err != nil {
		g.flashStatus("Record error: " + err.Error())
		return
	}
	g.flashStatus("Recording (" + g.rec.Recorder.OutputMode() + ") — Cmd+Shift+. to stop")
}

// extractSelectedText returns the selected text from the focused pane,
// or empty string if no selection is active.
func (g *Game) extractSelectedText() string {
	if g.activeFocused() == nil {
		return ""
	}
	g.activeFocused().Term.Buf.RLock()
	sel := g.activeFocused().Term.Buf.Selection
	cols := g.activeFocused().Term.Buf.Cols

	if !sel.Active {
		g.activeFocused().Term.Buf.RUnlock()
		return ""
	}

	norm := sel.Normalize()
	maxAbsRow := g.activeFocused().Term.Buf.ScrollbackLen() + g.activeFocused().Term.Buf.Rows - 1
	if norm.StartRow < 0 {
		norm.StartRow = 0
	}
	if norm.EndRow > maxAbsRow {
		norm.EndRow = maxAbsRow
	}

	var text strings.Builder
	for r := norm.StartRow; r <= norm.EndRow; r++ {
		if r > norm.StartRow && !g.activeFocused().Term.Buf.IsAbsRowWrapped(r) {
			text.WriteByte('\n')
		}
		colStart := 0
		colEnd := cols - 1
		if r == norm.StartRow {
			colStart = norm.StartCol
		}
		if r == norm.EndRow {
			colEnd = norm.EndCol
		}
		var line strings.Builder
		for c := colStart; c <= colEnd && c < cols; c++ {
			cell := g.activeFocused().Term.Buf.GetAbsCell(r, c)
			if cell.Width == 0 {
				continue
			}
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		text.WriteString(strings.TrimRight(line.String(), " "))
	}
	g.activeFocused().Term.Buf.RUnlock()
	return text.String()
}

// updateURLHover rescans URLs in the focused pane and updates hover state.
func (g *Game) updateURLHover(mx, my, pad int) {
	if g.activeFocused() == nil {
		return
	}
	// Convert pixel to cell coordinates within the focused pane.
	col := (mx - g.activeFocused().Rect.Min.X - pad) / g.font.CellW
	row := (my - g.activeFocused().Rect.Min.Y - pad - g.activeFocused().HeaderH) / g.font.CellH
	if col < 0 || row < 0 || col >= g.activeFocused().Cols || row >= g.activeFocused().Rows {
		if g.urlHover.HoveredURL != nil {
			g.urlHover.HoveredURL = nil
			g.screenDirty = true
		}
		return
	}

	// Rescan URLs from the buffer.
	g.activeFocused().Term.Buf.RLock()
	g.urlHover.Matches = g.activeFocused().Term.Buf.DetectURLs()
	g.activeFocused().Term.Buf.RUnlock()

	hit := terminal.URLAt(g.urlHover.Matches, row, col)
	if hit != g.urlHover.HoveredURL {
		// Pointer comparison is sufficient — URLAt returns a pointer into urlMatches.
		g.urlHover.HoveredURL = hit
		g.screenDirty = true
	}
}

// --- Config reload ---

// reloadConfig re-reads config.toml and propagates all changes at runtime.
func (g *Game) reloadConfig() {
	g.dismissTabHover()
	newCfg, meta, err := config.LoadWithMeta()
	if err != nil {
		g.flashStatus("Config reload failed: " + err.Error())
		return
	}
	config.ApplyTheme(newCfg, meta)

	oldFont := g.cfg.Font
	g.cfg = newCfg

	g.reloadColors(newCfg)
	g.reloadFont(oldFont, newCfg)
	g.reloadRuntimeSettings(newCfg)
	g.recomputeAllTabs()
	g.buildPalette()
	g.reloadVault()

	g.screenDirty = true
	g.flashStatus("Config reloaded")
}

// reloadColors propagates the new color config to the renderer and all terminal panes.
func (g *Game) reloadColors(cfg *config.Config) {
	g.renderer.ReloadColors(buildRenderConfig(cfg))
	for _, t := range g.tabMgr.Tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.UpdateColors(config.ParseHexColor(cfg.Colors.Foreground), config.ParseHexColor(cfg.Colors.Background), cfg.Palette())
		}
	}
}

// reloadFont attempts to reload the font when the font config changed.
// Skipped during active recording. Rolls back font config on failure.
func (g *Game) reloadFont(oldFont config.FontConfig, newCfg *config.Config) {
	fontChanged := newCfg.Font.Size != oldFont.Size ||
		newCfg.Font.File != oldFont.File ||
		newCfg.Font.Fallback != oldFont.Fallback ||
		!slices.Equal(newCfg.Font.Fallbacks, oldFont.Fallbacks)
	if !fontChanged || (g.rec.Recorder != nil && g.rec.Recorder.Active()) {
		return
	}
	fontBytes := jetbrainsMono
	if newCfg.Font.File != "" {
		if data, loadErr := os.ReadFile(newCfg.Font.File); loadErr == nil {
			fontBytes = data
		}
	}
	fbSlice := loadFontFallbacks(newCfg.Font)
	fontR, fontErr := renderer.NewFontRenderer(fontBytes, newCfg.Font.Size*g.dpi, fbSlice...)
	if fontErr != nil {
		g.cfg.Font = oldFont // rollback
		return
	}
	g.font = fontR
	g.renderer.SetFont(fontR)
	physW, physH := g.physSize()
	g.renderer.SetSize(physW, physH)
	g.recomputeAllTabs()
}

// reloadRuntimeSettings applies keyboard, performance, and blocks config.
func (g *Game) reloadRuntimeSettings(cfg *config.Config) {
	if cfg.Keyboard.RepeatDelayMs > 0 {
		keyRepeatDelay = time.Duration(cfg.Keyboard.RepeatDelayMs) * time.Millisecond
	}
	if cfg.Keyboard.RepeatIntervalMs > 0 {
		keyRepeatInterval = time.Duration(cfg.Keyboard.RepeatIntervalMs) * time.Millisecond
	}
	ebiten.SetTPS(cfg.Performance.TPS)
	g.blocksEnabled = cfg.Blocks.Enabled
	g.renderer.BlocksEnabled = g.blocksEnabled

	// Propagate cursor blink and ShowProcess to all terminals.
	for _, t := range g.tabMgr.Tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.Cursor.SetBlink(cfg.Input.CursorBlink)
			leaf.Pane.Term.SetShowProcess(cfg.StatusBar.ShowProcess)
		}
	}
}

// recomputeAllTabs recomputes layout rects for every tab and restores the active layout pointer.
func (g *Game) recomputeAllTabs() {
	for _, t := range g.tabMgr.Tabs {
		g.recomputeLayoutNode(t.Layout)
	}
	// activeLayout() reads directly from the tab — no cache refresh needed.
}

// reloadVault disables the vault and clears ghost text when vault is no longer enabled.
func (g *Game) reloadVault() {
	if !g.cfg.Vault.Enabled && g.vlt.Vault != nil {
		g.vlt.Vault.Close()
		g.vlt.Vault = nil
		g.vlt.Suggest = ""
		g.vlt.LineCache = ""
		g.vlt.Skip = 0
	}
}

// switchTheme applies a theme by name at runtime.
// When the user explicitly picks a theme from the palette, the theme colors
// are applied directly — the meta-merge (user-explicit overrides) only applies
// during reloadConfig / ApplyTheme for partial config.toml customization.
func (g *Game) switchTheme(name string) {
	themeColors, err := config.LoadTheme(name)
	if err != nil {
		g.flashStatus("Theme not found: " + name)
		return
	}

	g.cfg.Theme.Name = name
	g.cfg.Colors = themeColors

	// Propagate.
	g.renderer.ReloadColors(buildRenderConfig(g.cfg))
	for _, t := range g.tabMgr.Tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.UpdateColors(config.ParseHexColor(g.cfg.Colors.Foreground), config.ParseHexColor(g.cfg.Colors.Background), g.cfg.Palette())
		}
	}

	g.screenDirty = true
	g.flashStatus("Theme: " + name)
}

// adjustFontSize changes the font size by delta points and reloads the font.
// Clamped to [6, 72]. Skipped when recording is active.
func (g *Game) adjustFontSize(delta float64) {
	if g.rec.Recorder != nil && g.rec.Recorder.Active() {
		g.flashStatus("Cannot resize font while recording")
		return
	}
	newSize := g.cfg.Font.Size + delta
	if newSize < 6 {
		newSize = 6
	}
	if newSize > 72 {
		newSize = 72
	}
	if newSize == g.cfg.Font.Size {
		return
	}

	fontBytes := jetbrainsMono
	if g.cfg.Font.File != "" {
		if data, loadErr := os.ReadFile(g.cfg.Font.File); loadErr == nil {
			fontBytes = data
		}
	}
	fbSlice := loadFontFallbacks(g.cfg.Font)
	fontR, err := renderer.NewFontRenderer(fontBytes, newSize*g.dpi, fbSlice...)
	if err != nil {
		g.flashStatus("Font resize failed: " + err.Error())
		return
	}

	g.cfg.Font.Size = newSize
	g.font = fontR
	g.renderer.SetFont(fontR)
	g.renderer.SetSize(int(float64(g.winW)*g.dpi), int(float64(g.winH)*g.dpi))
	for _, t := range g.tabMgr.Tabs {
		g.recomputeLayoutNode(t.Layout)
	}
	// activeLayout() reads directly from the tab — no cache refresh needed.
	g.screenDirty = true
	g.flashStatus(fmt.Sprintf("Font size: %.0fpt", newSize))
}
