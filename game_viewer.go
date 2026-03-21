package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/markdown"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/terminal"
	"github.com/studiowebux/zurm/zserver"
)

// llmsFetchResult is the result of an async llms.txt HTTP fetch.
// Both files are fetched in parallel; either may be empty if unavailable.
type llmsFetchResult struct {
	Short  string // /llms.txt content (may be empty)
	Full   string // /llms-full.txt content (may be empty)
	Domain string
	Err    error
}

// llmsHistoryEntry captures one visited page for back/forward navigation.
type llmsHistoryEntry struct {
	Domain       string
	Short        string
	Full         string
	ViewingFull  bool
	ScrollOffset int
}

// openMarkdownViewer captures terminal content and opens the markdown viewer overlay.
func (g *Game) openMarkdownViewer() {
	content := g.captureMarkdownContent()
	if content == "" {
		g.flashStatus("No content to render")
		return
	}
	g.openMarkdownViewerWithContent(content, "Markdown Viewer")
}

// openMarkdownViewerWithContent opens the markdown viewer with arbitrary content.
// Reuse point for future llms.txt browser.
func (g *Game) openMarkdownViewerWithContent(content, title string) {
	// Derive wrap columns from the actual panel pixel width and cell width.
	// Panel is 80% of window width; subtract padding (2 * cellW) and scrollbar (4px).
	physW := int(float64(g.winW) * g.dpi)
	panelW := physW * 80 / 100
	cw := g.font.CellW
	wrapCols := (panelW - 2*cw - 4) / cw
	if wrapCols < 40 {
		wrapCols = 40
	}

	lines := markdown.Parse(content, wrapCols)
	g.mdViewerState = renderer.MarkdownViewerState{
		Open:  true,
		Title: title,
		Lines: lines,
	}
	g.screenDirty = true
}

// openURLInput opens the llms.txt URL input overlay.
func (g *Game) openURLInput() {
	g.urlInputState = renderer.URLInputState{Open: true}
	g.screenDirty = true
}

// handleURLInputInput processes keyboard input while the URL input overlay is open.
func (g *Game) handleURLInputInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// ESC: close overlay (also cancels any pending fetch).
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.urlInputState = renderer.URLInputState{}
		g.llmsFetchCh = nil
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// While loading, ignore all other input.
	if g.urlInputState.Loading {
		return
	}

	ti := &TextInput{Text: g.urlInputState.Query, CursorPos: g.urlInputState.CursorPos}

	// Edge-triggered: Enter submits.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		if pressed && !g.prevKeys[key] {
			q := strings.TrimSpace(ti.Text)
			if q != "" {
				g.urlInputState.Query = ti.Text
				g.urlInputState.CursorPos = ti.CursorPos
				g.startLLMSFetch(q)
			}
		}
		g.prevKeys[key] = pressed
	}

	// Cmd+V — async clipboard paste (first line only).
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		g.requestClipboard()
	}
	select {
	case clip := <-g.clipboardCh:
		line := strings.TrimSpace(strings.SplitN(clip, "\n", 2)[0])
		if line != "" {
			ti.AddString(line)
		}
	default:
	}

	ti.Update(&g.urlRepeat, meta, alt)

	g.urlInputState.Query = ti.Text
	g.urlInputState.CursorPos = ti.CursorPos
}

// startLLMSFetch initiates an async HTTP fetch for both /llms.txt and /llms-full.txt
// from the given domain. Both are fetched in parallel; either may be empty.
func (g *Game) startLLMSFetch(domain string) {
	// Strip protocol prefix, known paths, and trailing slash.
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimRight(domain, "/")
	domain = strings.TrimSuffix(domain, "/llms.txt")
	domain = strings.TrimSuffix(domain, "/llms-full.txt")

	g.urlInputState.Loading = true
	ch := make(chan llmsFetchResult, 1)
	g.llmsFetchCh = ch

	go func() {
		client := &http.Client{Timeout: llmsFetchTimeout}
		type partial struct {
			body string
			ok   bool
		}

		fetch := func(path string) partial {
			resp, err := client.Get("https://" + domain + path) // #nosec G107 — user-provided URL, intentional
			if err != nil {
				return partial{}
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return partial{}
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return partial{}
			}
			return partial{body: string(body), ok: true}
		}

		shortCh := make(chan partial, 1)
		fullCh := make(chan partial, 1)
		go func() { shortCh <- fetch("/llms.txt") }()
		go func() { fullCh <- fetch("/llms-full.txt") }()

		short := <-shortCh
		full := <-fullCh

		if !short.ok && !full.ok {
			ch <- llmsFetchResult{Err: fmt.Errorf("no llms.txt found on %s", domain), Domain: domain}
			return
		}

		ch <- llmsFetchResult{Short: short.body, Full: full.body, Domain: domain}
	}()
}

// drainLLMSFetch reads a completed async llms.txt fetch result when available.
func (g *Game) drainLLMSFetch() {
	if g.llmsFetchCh == nil {
		return
	}
	select {
	case result := <-g.llmsFetchCh:
		g.urlInputState = renderer.URLInputState{}
		g.llmsFetchCh = nil
		g.screenDirty = true

		if result.Err != nil {
			g.flashStatus("llms.txt: " + result.Err.Error())
			return
		}

		// Cache both results for Tab switching.
		g.llmsShort = result.Short
		g.llmsFull = result.Full
		g.llmsDomain = result.Domain
		g.llmsViewingFull = false

		// Show /llms.txt if available, otherwise /llms-full.txt.
		content := result.Short
		title := "llms.txt — " + result.Domain
		if content == "" {
			content = result.Full
			title = "llms-full.txt — " + result.Domain
			g.llmsViewingFull = true
		}
		g.openMarkdownViewerWithContent(content, title)
		g.mdViewerState.HasAlt = result.Short != "" && result.Full != ""
		g.mdViewerState.IsLLMS = true
		g.mdViewerState.HistoryLen = len(g.llmsHistory)
		g.mdViewerState.ForwardLen = len(g.llmsForward)
	default:
	}
}

// captureMarkdownContent extracts text from the terminal for markdown viewing.
// Priority: last block output > active selection > visible screen.
func (g *Game) captureMarkdownContent() string {
	if g.focused == nil {
		return ""
	}

	buf := g.focused.Term.Buf
	buf.RLock()
	defer buf.RUnlock()

	// Priority 1: last completed block output.
	// Blocks are always tracked by the parser regardless of blocksEnabled.
	if len(buf.Blocks) > 0 {
		// Find the last block with valid output range.
		for i := len(buf.Blocks) - 1; i >= 0; i-- {
			b := buf.Blocks[i]
			if b.AbsCmdRow >= 0 && b.AbsEndRow > b.AbsCmdRow {
				return buf.TextRange(b.AbsCmdRow, b.AbsEndRow)
			}
		}
	}

	// Priority 2: active selection.
	sel := buf.Selection
	if sel.Active {
		norm := sel.Normalize()
		return buf.TextRange(norm.StartRow, norm.EndRow)
	}

	// Priority 3: if scrolled back, capture from scroll position to end
	// of primary screen (user positioned viewport at start of content).
	// Otherwise, capture primary screen only.
	absStart := buf.DisplayToAbsRow(0)
	absEnd := buf.ScrollbackLen() + buf.Rows - 1
	return buf.TextRange(absStart, absEnd)
}

// handleMarkdownViewerInput processes keyboard input while the markdown viewer is open.
func (g *Game) handleMarkdownViewerInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// Follow-link mode: letter keys follow, Esc cancels.
	if g.mdViewerState.FollowMode {
		if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
			g.mdViewerState.FollowMode = false
			g.mdViewerState.LinkHints = nil
			g.screenDirty = true
			return
		}
		// Check for letter key press (a-z). Only the first input char matters.
		if chars := ebiten.AppendInputChars(nil); len(chars) > 0 {
			r := chars[0]
			if r >= 'a' && r <= 'z' {
				for _, hint := range g.mdViewerState.LinkHints {
					if hint.Label == r {
						g.mdViewerState.FollowMode = false
						g.mdViewerState.LinkHints = nil
						g.llmsFollowLink(hint.URL)
						return
					}
				}
			}
			// Any non-matching key cancels follow mode.
			g.mdViewerState.FollowMode = false
			g.mdViewerState.LinkHints = nil
			g.screenDirty = true
			return
		}
		return
	}

	// Search mode: text input takes priority.
	if g.mdViewerState.SearchOpen {
		g.handleMarkdownSearchInput()
		return
	}

	// ESC or Cmd+Shift+M: close viewer.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.mdViewerState = renderer.MarkdownViewerState{}
		g.renderer.SetLayoutDirty()
		g.renderer.ClearPaneCache()
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	if meta && shift && inpututil.IsKeyJustPressed(ebiten.KeyM) {
		g.mdViewerState = renderer.MarkdownViewerState{}
		g.renderer.SetLayoutDirty()
		g.renderer.ClearPaneCache()
		g.screenDirty = true
		return
	}

	// Cmd+Enter — send viewer content to a pane.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		g.sendViewerToPane()
		return
	}

	// f — enter follow-link mode.
	if !meta && !shift && inpututil.IsKeyJustPressed(ebiten.KeyF) {
		hints := g.collectVisibleLinkHints()
		if len(hints) > 0 {
			g.mdViewerState.FollowMode = true
			g.mdViewerState.LinkHints = hints
			g.screenDirty = true
		}
		return
	}

	// Tab — switch between llms.txt and llms-full.txt when both are available.
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) && g.llmsShort != "" && g.llmsFull != "" {
		g.llmsViewingFull = !g.llmsViewingFull
		if g.llmsViewingFull {
			g.openMarkdownViewerWithContent(g.llmsFull, "llms-full.txt — "+g.llmsDomain)
		} else {
			g.openMarkdownViewerWithContent(g.llmsShort, "llms.txt — "+g.llmsDomain)
		}
		g.mdViewerState.HasAlt = true
		g.mdViewerState.IsLLMS = true
		g.mdViewerState.HistoryLen = len(g.llmsHistory)
		g.mdViewerState.ForwardLen = len(g.llmsForward)
		return
	}

	// Backspace or Shift+H — navigate back in llms.txt history.
	if g.mdViewerState.IsLLMS && len(g.llmsHistory) > 0 {
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) || (shift && inpututil.IsKeyJustPressed(ebiten.KeyH)) {
			g.llmsNavigateBack()
			return
		}
	}

	// Shift+L — navigate forward in llms.txt history.
	if g.mdViewerState.IsLLMS && len(g.llmsForward) > 0 {
		if shift && inpututil.IsKeyJustPressed(ebiten.KeyL) {
			g.llmsNavigateForward()
			return
		}
	}

	// Cmd+F or / — open search.
	if (meta && inpututil.IsKeyJustPressed(ebiten.KeyF)) || (!meta && inpututil.IsKeyJustPressed(ebiten.KeySlash)) {
		g.mdViewerState.SearchOpen = true
		g.mdViewerState.SearchQuery = ""
		g.mdViewerState.SearchMatches = nil
		g.mdViewerState.SearchIdx = -1
		g.screenDirty = true
		return
	}

	// n/N — jump to next/previous match (when matches exist from a previous search).
	if len(g.mdViewerState.SearchMatches) > 0 {
		if !meta && inpututil.IsKeyJustPressed(ebiten.KeyN) {
			if shift {
				g.mdViewerSearchPrev()
			} else {
				g.mdViewerSearchNext()
			}
			return
		}
	}

	rowH := g.mdViewerState.RowH
	if rowH == 0 {
		rowH = 16
	}

	// Keyboard scroll with key-repeat support.
	// Initial delay: 20 frames (~333ms at 60fps), repeat every 3 frames (~50ms).
	const repeatDelay = 20
	const repeatInterval = 3

	scrollKeys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyJ, ebiten.KeyK,
		ebiten.KeyPageUp, ebiten.KeyPageDown,
		ebiten.KeyHome, ebiten.KeyEnd,
	}

	for _, key := range scrollKeys {
		dur := inpututil.KeyPressDuration(key)
		if dur == 0 {
			continue
		}
		fire := dur == 1 || (dur >= repeatDelay && (dur-repeatDelay)%repeatInterval == 0)
		if !fire {
			continue
		}
		switch key {
		case ebiten.KeyArrowUp, ebiten.KeyK:
			g.mdViewerState.ScrollOffset -= rowH
		case ebiten.KeyArrowDown, ebiten.KeyJ:
			g.mdViewerState.ScrollOffset += rowH
		case ebiten.KeyPageUp:
			g.mdViewerState.ScrollOffset -= 10 * rowH
		case ebiten.KeyPageDown:
			g.mdViewerState.ScrollOffset += 10 * rowH
		case ebiten.KeyHome:
			g.mdViewerState.ScrollOffset = 0
		case ebiten.KeyEnd:
			g.mdViewerState.ScrollOffset = g.mdViewerState.MaxScroll
		}
		g.screenDirty = true
	}

	// Vim motions: gg (top), G (bottom), Ctrl+d (half-page down), Ctrl+u (half-page up).
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	if ctrl && inpututil.IsKeyJustPressed(ebiten.KeyD) {
		g.mdViewerState.ScrollOffset += 15 * rowH
		g.screenDirty = true
	}
	if ctrl && inpututil.IsKeyJustPressed(ebiten.KeyU) {
		g.mdViewerState.ScrollOffset -= 15 * rowH
		g.screenDirty = true
	}
	if !ctrl && inpututil.IsKeyJustPressed(ebiten.KeyG) {
		if shift {
			// Shift+G → bottom
			g.mdViewerState.ScrollOffset = g.mdViewerState.MaxScroll
			g.screenDirty = true
		} else {
			// gg detection: two 'g' presses within 500ms.
			now := time.Now()
			if now.Sub(g.mdViewerLastG) < 500*time.Millisecond {
				g.mdViewerState.ScrollOffset = 0
				g.screenDirty = true
				g.mdViewerLastG = time.Time{} // reset
			} else {
				g.mdViewerLastG = now
			}
		}
	}

	// Mouse wheel scroll.
	_, wy := ebiten.Wheel()
	if wy != 0 {
		g.mdViewerState.ScrollOffset -= int(wy * float64(rowH) * 3)
		g.screenDirty = true
	}

	g.clampMdViewerScroll()
}

// handleMarkdownSearchInput processes keyboard input while the search bar is active.
func (g *Game) handleMarkdownSearchInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// ESC — close search bar (matches stay visible for n/N in normal mode).
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.mdViewerState.SearchOpen = false
		g.screenDirty = true
		return
	}

	// Enter — next match; Shift+Enter — previous match.
	// n/N are NOT intercepted here — the search bar is a text field, so all
	// printable characters go to the query. n/N navigation works in normal mode
	// (handleMarkdownViewerInput) after the search bar is closed.
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		if shift {
			g.mdViewerSearchPrev()
		} else {
			g.mdViewerSearchNext()
		}
		return
	}

	ti := &TextInput{Text: g.mdViewerState.SearchQuery, CursorPos: g.mdViewerState.SearchCursorPos}
	prevQuery := g.mdViewerState.SearchQuery

	ti.Update(&g.mdSearchRepeat, meta, alt)

	g.mdViewerState.SearchQuery = ti.Text
	g.mdViewerState.SearchCursorPos = ti.CursorPos

	if g.mdViewerState.SearchQuery != prevQuery {
		g.mdViewerUpdateSearch()
		g.screenDirty = true
	}
}

// mdViewerUpdateSearch rebuilds the match list for the current search query.
func (g *Game) mdViewerUpdateSearch() {
	q := strings.ToLower(g.mdViewerState.SearchQuery)
	g.mdViewerState.SearchMatches = nil
	g.mdViewerState.SearchIdx = -1

	if q == "" {
		return
	}

	qLen := len([]rune(q))
	for lineIdx, line := range g.mdViewerState.Lines {
		// Concatenate span text to find matches across span boundaries.
		col := 0
		for _, span := range line.Spans {
			lower := strings.ToLower(span.Text)
			runes := []rune(lower)
			for j := 0; j <= len(runes)-qLen; j++ {
				if string(runes[j:j+qLen]) == q {
					g.mdViewerState.SearchMatches = append(g.mdViewerState.SearchMatches, renderer.SearchMatch{
						LineIdx: lineIdx,
						Col:     col + j,
						Len:     qLen,
					})
				}
			}
			col += len(runes)
		}
	}

	// Auto-scroll to first match.
	if len(g.mdViewerState.SearchMatches) > 0 {
		g.mdViewerState.SearchIdx = 0
		g.mdViewerScrollToMatch()
	}
}

// mdViewerSearchNext jumps to the next search match.
func (g *Game) mdViewerSearchNext() {
	if len(g.mdViewerState.SearchMatches) == 0 {
		return
	}
	g.mdViewerState.SearchIdx = (g.mdViewerState.SearchIdx + 1) % len(g.mdViewerState.SearchMatches)
	g.mdViewerScrollToMatch()
	g.screenDirty = true
}

// mdViewerSearchPrev jumps to the previous search match.
func (g *Game) mdViewerSearchPrev() {
	if len(g.mdViewerState.SearchMatches) == 0 {
		return
	}
	g.mdViewerState.SearchIdx--
	if g.mdViewerState.SearchIdx < 0 {
		g.mdViewerState.SearchIdx = len(g.mdViewerState.SearchMatches) - 1
	}
	g.mdViewerScrollToMatch()
	g.screenDirty = true
}

// mdViewerScrollToMatch scrolls the viewer so the current match is visible.
func (g *Game) mdViewerScrollToMatch() {
	if g.mdViewerState.SearchIdx < 0 || g.mdViewerState.SearchIdx >= len(g.mdViewerState.SearchMatches) {
		return
	}
	rowH := g.mdViewerState.RowH
	if rowH == 0 {
		rowH = 16
	}
	m := g.mdViewerState.SearchMatches[g.mdViewerState.SearchIdx]
	targetOffset := m.LineIdx * rowH
	g.mdViewerState.ScrollOffset = targetOffset
	g.clampMdViewerScroll()
}

// clampMdViewerScroll keeps the scroll offset within valid bounds.
func (g *Game) clampMdViewerScroll() {
	if g.mdViewerState.ScrollOffset < 0 {
		g.mdViewerState.ScrollOffset = 0
	}
	if g.mdViewerState.MaxScroll > 0 && g.mdViewerState.ScrollOffset > g.mdViewerState.MaxScroll {
		g.mdViewerState.ScrollOffset = g.mdViewerState.MaxScroll
	}
}

// spanToANSI converts a markdown span to ANSI-colored text for terminal display.
func spanToANSI(span markdown.Span) string {
	const reset = "\033[0m"
	switch span.Style {
	case markdown.StyleHeading1:
		return "\033[1;97m" + span.Text + reset // bold bright white
	case markdown.StyleHeading2:
		return "\033[1;36m" + span.Text + reset // bold cyan
	case markdown.StyleHeading3:
		return "\033[2;37m" + span.Text + reset // dim white
	case markdown.StyleBold:
		return "\033[1;97m" + span.Text + reset // bold bright white
	case markdown.StyleItalic:
		return "\033[3;37m" + span.Text + reset // italic dim
	case markdown.StyleInlineCode:
		return "\033[7;33m" + span.Text + reset // reverse yellow
	case markdown.StyleCodeBlock:
		return "\033[32m" + span.Text + reset // green
	case markdown.StyleLink:
		if span.Extra != "" {
			return "\033[4;36m" + span.Text + reset + "\033[2m (" + span.Extra + ")" + reset
		}
		return "\033[4;36m" + span.Text + reset // underline cyan
	case markdown.StyleBlockquote:
		return "\033[2;37m" + span.Text + reset // dim
	case markdown.StyleListItem:
		return "\033[36m" + span.Text + reset // cyan marker
	case markdown.StyleTableHeader:
		return "\033[1;97m" + span.Text + reset // bold white
	case markdown.StyleTableCell:
		return span.Text
	case markdown.StyleTableSeparator, markdown.StyleHRule:
		return "\033[2m────────────────────────────────────────" + reset
	case markdown.StyleStrikethrough:
		return "\033[9;2m" + span.Text + reset // strikethrough dim
	case markdown.StyleImage:
		return "\033[35m" + span.Text + reset // magenta
	case markdown.StyleCheckboxChecked:
		return "\033[32m" + span.Text + reset // green
	case markdown.StyleCheckboxUnchecked:
		return "\033[2m" + span.Text + reset // dim
	default:
		return span.Text
	}
}

// collectVisibleLinkHints scans visible lines for StyleLink spans and assigns
// letter badges (a-z). Returns at most 26 hints.
func (g *Game) collectVisibleLinkHints() []renderer.LinkHint {
	rowH := g.mdViewerState.RowH
	if rowH == 0 {
		rowH = 16
	}

	// Approximate visible area height from panel dimensions (80% of window height).
	physH := int(float64(g.winH) * g.dpi)
	visibleH := physH * 80 / 100

	var hints []renderer.LinkHint
	label := 'a'
	for lineIdx, line := range g.mdViewerState.Lines {
		lineY := lineIdx*rowH - g.mdViewerState.ScrollOffset
		// Only include lines visible in the content area.
		if lineY+rowH < 0 {
			continue
		}
		if lineY > visibleH {
			break // past visible area
		}

		for spanIdx, span := range line.Spans {
			if span.Style == markdown.StyleLink && span.Extra != "" {
				hints = append(hints, renderer.LinkHint{
					LineIdx: lineIdx,
					SpanIdx: spanIdx,
					URL:     span.Extra,
					Label:   label,
				})
				label++
				if label > 'z' {
					return hints
				}
			}
		}
	}
	return hints
}

// llmsPushHistory saves the current llms state onto the back stack.
func (g *Game) llmsPushHistory() {
	g.llmsHistory = append(g.llmsHistory, llmsHistoryEntry{
		Domain:       g.llmsDomain,
		Short:        g.llmsShort,
		Full:         g.llmsFull,
		ViewingFull:  g.llmsViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
}

// llmsNavigateBack pops from the back stack and pushes current to forward.
func (g *Game) llmsNavigateBack() {
	if len(g.llmsHistory) == 0 {
		return
	}
	// Push current to forward stack.
	g.llmsForward = append(g.llmsForward, llmsHistoryEntry{
		Domain:       g.llmsDomain,
		Short:        g.llmsShort,
		Full:         g.llmsFull,
		ViewingFull:  g.llmsViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
	// Pop from history.
	entry := g.llmsHistory[len(g.llmsHistory)-1]
	g.llmsHistory = g.llmsHistory[:len(g.llmsHistory)-1]
	g.llmsRestoreEntry(entry)
}

// llmsNavigateForward pops from the forward stack and pushes current to back.
func (g *Game) llmsNavigateForward() {
	if len(g.llmsForward) == 0 {
		return
	}
	// Push current to back stack.
	g.llmsHistory = append(g.llmsHistory, llmsHistoryEntry{
		Domain:       g.llmsDomain,
		Short:        g.llmsShort,
		Full:         g.llmsFull,
		ViewingFull:  g.llmsViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
	// Pop from forward.
	entry := g.llmsForward[len(g.llmsForward)-1]
	g.llmsForward = g.llmsForward[:len(g.llmsForward)-1]
	g.llmsRestoreEntry(entry)
}

// llmsRestoreEntry restores the viewer to a history entry's state.
func (g *Game) llmsRestoreEntry(entry llmsHistoryEntry) {
	g.llmsDomain = entry.Domain
	g.llmsShort = entry.Short
	g.llmsFull = entry.Full
	g.llmsViewingFull = entry.ViewingFull

	content := entry.Short
	title := "llms.txt — " + entry.Domain
	if entry.ViewingFull {
		content = entry.Full
		title = "llms-full.txt — " + entry.Domain
	}
	g.openMarkdownViewerWithContent(content, title)
	g.mdViewerState.HasAlt = entry.Short != "" && entry.Full != ""
	g.mdViewerState.IsLLMS = true
	g.mdViewerState.ScrollOffset = entry.ScrollOffset
	g.mdViewerState.HistoryLen = len(g.llmsHistory)
	g.mdViewerState.ForwardLen = len(g.llmsForward)
}

// llmsFollowLink handles following a link from the markdown viewer.
// If the URL looks like an llms.txt-capable domain, fetch it; otherwise open in browser.
func (g *Game) llmsFollowLink(url string) {
	// Check if this is an HTTP(S) URL we can try to fetch llms.txt from.
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		// Push current state to history and clear forward stack.
		g.llmsPushHistory()
		g.llmsForward = nil
		g.startLLMSFetch(url)
		return
	}
	// Non-HTTP URL or relative path — open in system browser.
	exec.Command("open", url).Start() // #nosec G204 — user-initiated URL open
}

// sendViewerToPane writes the current viewer content to a temp file and opens
// it in a new pane running `less`.
func (g *Game) sendViewerToPane() {
	if !g.mdViewerState.Open {
		return
	}

	// Capture state before clearing.
	lines := g.mdViewerState.Lines
	paneName := g.mdViewerState.Title
	if g.llmsDomain != "" {
		paneName = "llms — " + g.llmsDomain
	}

	// Build ANSI-colored text from styled lines for rich rendering in less -R.
	var buf strings.Builder
	for _, line := range lines {
		// Indent.
		for i := 0; i < line.Indent; i++ {
			buf.WriteByte(' ')
		}
		for _, span := range line.Spans {
			buf.WriteString(spanToANSI(span))
		}
		// Heading underlines.
		if len(line.Spans) > 0 {
			switch line.Spans[0].Style {
			case markdown.StyleHeading1:
				textLen := 0
				for _, s := range line.Spans {
					textLen += len([]rune(s.Text))
				}
				buf.WriteByte('\n')
				buf.WriteString("\033[97m")
				buf.WriteString(strings.Repeat("━", textLen))
				buf.WriteString("\033[0m")
			case markdown.StyleHeading2:
				textLen := 0
				for _, s := range line.Spans {
					textLen += len([]rune(s.Text))
				}
				buf.WriteByte('\n')
				buf.WriteString("\033[36m")
				buf.WriteString(strings.Repeat("─", textLen))
				buf.WriteString("\033[0m")
			}
		}
		buf.WriteByte('\n')
	}

	// Write to temp file.
	tmpFile, err := os.CreateTemp("", "zurm-llms-*.md")
	if err != nil {
		g.flashStatus("Failed to create temp file: " + err.Error())
		return
	}
	if _, err := tmpFile.WriteString(buf.String()); err != nil {
		tmpFile.Close()
		g.flashStatus("Failed to write temp file: " + err.Error())
		return
	}
	tmpFile.Close()

	// Close the viewer.
	g.mdViewerState = renderer.MarkdownViewerState{}
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()

	// Create a new tab with a pane running `less -R <tmpfile>`.
	paneRect := g.contentRect()

	term := terminal.New(terminal.TerminalConfig{
		Rows:            g.cfg.Window.Rows,
		Cols:            g.cfg.Window.Columns,
		ScrollbackLines: g.cfg.Scrollback.Lines,
		MaxBlocks:       g.cfg.Blocks.MaxHistory,
		FG:              config.ParseHexColor(g.cfg.Colors.Foreground),
		BG:              config.ParseHexColor(g.cfg.Colors.Background),
		Palette:         g.cfg.Palette(),
		CursorBlink:     g.cfg.Input.CursorBlink,
		ShellProgram:    g.cfg.Shell.Program,
		ShellArgs:       g.cfg.Shell.Args,
		ShowProcess:     g.cfg.StatusBar.ShowProcess,
	})
	term.StartCmd("less", []string{"-R", tmpFile.Name()}, "")
	p := &pane.Pane{
		Term:       term,
		Rect:       paneRect,
		Cols:       (paneRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW,
		Rows:       (paneRect.Dy() - g.cfg.Window.Padding*2) / g.font.CellH,
		CustomName: paneName,
	}

	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	term.Resize(p.Cols, p.Rows)

	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   p.CustomName,
	}
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
	g.screenDirty = true
}

// fetchSessions connects to zurm-server and returns the list of active sessions.
func fetchSessions(addr string) ([]zserver.SessionInfo, error) {
	conn, err := net.Dial("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to zurm-server at %s: %w", addr, err)
	}
	defer conn.Close()

	if err := zserver.WriteMessage(conn, zserver.MsgListSessions, nil); err != nil {
		return nil, fmt.Errorf("send list request: %w", err)
	}

	msg, err := zserver.ReadMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if msg.Type != zserver.MsgSessionList {
		return nil, fmt.Errorf("unexpected response type 0x%02x", msg.Type)
	}

	var sessions []zserver.SessionInfo
	if len(msg.Payload) > 0 {
		if err := json.Unmarshal(msg.Payload, &sessions); err != nil {
			return nil, fmt.Errorf("decode session list: %w", err)
		}
	}
	return sessions, nil
}

// killSession connects to zurm-server and kills the session with the given ID.
func killSession(addr, id string) error {
	conn, err := net.Dial("unix", addr)
	if err != nil {
		return fmt.Errorf("cannot connect to zurm-server at %s: %w", addr, err)
	}
	defer conn.Close()
	data, err := json.Marshal(zserver.KillSessionRequest{ID: id})
	if err != nil {
		return fmt.Errorf("marshal kill request: %w", err)
	}
	return zserver.WriteMessage(conn, zserver.MsgKillSession, data)
}

// resolveSessionPrefix matches a short prefix (like Docker short IDs) against
// active server sessions. Returns the full ID or an error if zero or multiple
// sessions match.
func resolveSessionPrefix(addr, prefix string) (string, error) {
	sessions, err := fetchSessions(addr)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, s := range sessions {
		if len(s.ID) >= len(prefix) && s.ID[:len(prefix)] == prefix {
			matches = append(matches, s.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matching prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches %d sessions: %v", prefix, len(matches), matches)
	}
}

// runListSessions connects to zurm-server, fetches the session list, prints a
// table to stdout, and returns. Called by the --list-sessions / -ls flag before
// the GUI starts.
func runListSessions() error {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load warning: %v (using defaults)", err)
	}

	addr := zserver.ResolveSocket(cfg.Server.Address)
	sessions, err := fetchSessions(addr)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		fmt.Println("No active server sessions.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPID\tSIZE\tDIR")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%d\t%dx%d\t%s\n", s.ID, s.Name, s.PID, s.Cols, s.Rows, s.Dir)
	}
	w.Flush()
	return nil
}

// attachServerSession connects to zurm-server, lists active sessions, and
// populates the command palette with an entry per session. Selecting an entry
// opens a new server-backed tab attached to that session.
// Called from the "Attach to Server Session" palette action.
func (g *Game) attachServerSession() {
	addr := zserver.ResolveSocket(g.cfg.Server.Address)
	sessions, err := fetchSessions(addr)
	if err != nil {
		g.flashStatus("zurm-server unreachable")
		return
	}

	if len(sessions) == 0 {
		g.flashStatus("No active server sessions")
		return
	}

	// Append per-session palette entries. The base palette is restored on the
	// next buildPalette call (theme switch, config reload, etc.).
	for _, s := range sessions {
		si := s // capture for closure
		displayName := si.ID
		if si.Name != "" {
			displayName = si.Name
		} else if len(si.ID) > 8 {
			displayName = si.ID[:8]
		}
		attachLabel := fmt.Sprintf("Attach: %s (pid %d, %dx%d, %s)", displayName, si.PID, si.Cols, si.Rows, si.Dir)
		g.paletteEntries = append(g.paletteEntries, renderer.PaletteEntry{Name: attachLabel})
		g.paletteActions = append(g.paletteActions, func() {
			g.openServerTabForSession(si.ID)
		})

		killLabel := fmt.Sprintf("Kill: %s (pid %d)", displayName, si.PID)
		g.paletteEntries = append(g.paletteEntries, renderer.PaletteEntry{Name: killLabel})
		g.paletteActions = append(g.paletteActions, func() {
			if err := killSession(addr, si.ID); err != nil {
				g.flashStatus("Kill failed: " + err.Error())
			} else {
				g.flashStatus("Killed session " + displayName)
			}
		})
	}

	// Open palette pre-filtered to the injected entries.
	g.paletteState.Open = true
	g.paletteState.Query = ""
	g.screenDirty = true
}

// openServerTabForSession opens a new tab backed by an existing zurm-server
// session identified by sessionID.
func (g *Game) openServerTabForSession(sessionID string) {
	paneRect := g.contentRect()

	p, err := pane.NewServer(g.cfg, paneRect, g.font.CellW, g.font.CellH, "", sessionID)
	if err != nil {
		g.flashStatus("Attach failed: " + err.Error())
		return
	}
	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   fmt.Sprintf("tab %d", len(g.tabs)+1),
	}
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
}
