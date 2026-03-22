package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/markdown"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/terminal"
)

const (
	viewerPanelWidthPct = 80          // panel width as % of window
	viewerMinWrapCols   = 40          // minimum columns for word wrap
	viewerPageScrollH   = 10          // page scroll multiplier (in row heights)
	viewerHalfPageH     = 15          // ctrl+D/U scroll multiplier (in row heights)
	viewerGGTimeout     = 500 * time.Millisecond // gg double-tap window
	viewerMaxHTTPBody   = 10 << 20    // 10 MB cap on HTTP response bodies
)

// llmsState groups all llms.txt browser fields (URL input, fetch, cache, navigation).
type llmsState struct {
	URLInput    renderer.URLInputState
	FetchCh     chan llmsFetchResult
	FetchCancel context.CancelFunc
	Short       string             // cached /llms.txt content
	Full        string             // cached /llms-full.txt content
	Domain      string             // domain of the last fetch
	ViewingFull bool               // true when showing /llms-full.txt
	History     []llmsHistoryEntry // back stack
	Forward     []llmsHistoryEntry // forward stack
	LastG       time.Time          // timestamp of last 'g' press for gg detection
}

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
	panelW := physW * viewerPanelWidthPct / 100
	cw := g.font.CellW
	wrapCols := (panelW - 2*cw - 4) / cw
	if wrapCols < viewerMinWrapCols {
		wrapCols = viewerMinWrapCols
	}

	lines := convertMdLines(markdown.Parse(content, wrapCols))
	g.mdViewerState = renderer.MarkdownViewerState{
		Open:  true,
		Title: title,
		Lines: lines,
	}
	g.screenDirty = true
}

// openURLInput opens the llms.txt URL input overlay.
func (g *Game) openURLInput() {
	g.llms.URLInput = renderer.URLInputState{Open: true}
	g.screenDirty = true
}

// handleURLInputInput processes keyboard input while the URL input overlay is open.
func (g *Game) handleURLInputInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// ESC: close overlay (also cancels any pending fetch).
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.llms.URLInput = renderer.URLInputState{}
		if g.llms.FetchCancel != nil {
			g.llms.FetchCancel()
			g.llms.FetchCancel = nil
		}
		g.llms.FetchCh = nil
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	// While loading, ignore all other input.
	if g.llms.URLInput.Loading {
		return
	}

	ti := &TextInput{Text: g.llms.URLInput.Query, CursorPos: g.llms.URLInput.CursorPos}

	// Edge-triggered: Enter submits.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		if pressed && !g.input.PrevKeys[key] {
			q := strings.TrimSpace(ti.Text)
			if q != "" {
				g.llms.URLInput.Query = ti.Text
				g.llms.URLInput.CursorPos = ti.CursorPos
				g.startLLMSFetch(q)
			}
		}
		g.input.PrevKeys[key] = pressed
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

	ti.Update(&g.repeats.URL, meta, alt)

	g.llms.URLInput.Query = ti.Text
	g.llms.URLInput.CursorPos = ti.CursorPos
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

	// Cancel any in-flight fetch before starting a new one.
	if g.llms.FetchCancel != nil {
		g.llms.FetchCancel()
	}
	ctx, cancel := context.WithTimeout(g.ctx, llmsFetchTimeout)
	g.llms.FetchCancel = cancel

	g.llms.URLInput.Loading = true
	ch := make(chan llmsFetchResult, 1)
	g.llms.FetchCh = ch

	go func() {
		defer cancel()
		client := &http.Client{}
		type partial struct {
			body string
			ok   bool
		}

		fetch := func(path string) partial {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+path, nil)
			if err != nil {
				return partial{}
			}
			resp, err := client.Do(req) // #nosec G107 — user-provided URL, intentional
			if err != nil {
				return partial{}
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return partial{}
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, viewerMaxHTTPBody))
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
	if g.llms.FetchCh == nil {
		return
	}
	select {
	case result := <-g.llms.FetchCh:
		g.llms.URLInput = renderer.URLInputState{}
		g.llms.FetchCh = nil
		g.screenDirty = true

		if result.Err != nil {
			g.flashStatus("llms.txt: " + result.Err.Error())
			return
		}

		// Cache both results for Tab switching.
		g.llms.Short = result.Short
		g.llms.Full = result.Full
		g.llms.Domain = result.Domain
		g.llms.ViewingFull = false

		// Show /llms.txt if available, otherwise /llms-full.txt.
		content := result.Short
		title := "llms.txt — " + result.Domain
		if content == "" {
			content = result.Full
			title = "llms-full.txt — " + result.Domain
			g.llms.ViewingFull = true
		}
		g.openMarkdownViewerWithContent(content, title)
		g.mdViewerState.HasAlt = result.Short != "" && result.Full != ""
		g.mdViewerState.IsLLMS = true
		g.mdViewerState.HistoryLen = len(g.llms.History)
		g.mdViewerState.ForwardLen = len(g.llms.Forward)
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
		g.handleViewerFollowMode()
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
		g.input.PrevKeys[ebiten.KeyEscape] = true
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
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) && g.llms.Short != "" && g.llms.Full != "" {
		g.llms.ViewingFull = !g.llms.ViewingFull
		if g.llms.ViewingFull {
			g.openMarkdownViewerWithContent(g.llms.Full, "llms-full.txt — "+g.llms.Domain)
		} else {
			g.openMarkdownViewerWithContent(g.llms.Short, "llms.txt — "+g.llms.Domain)
		}
		g.mdViewerState.HasAlt = true
		g.mdViewerState.IsLLMS = true
		g.mdViewerState.HistoryLen = len(g.llms.History)
		g.mdViewerState.ForwardLen = len(g.llms.Forward)
		return
	}

	// Backspace or Shift+H — navigate back in llms.txt history.
	if g.mdViewerState.IsLLMS && len(g.llms.History) > 0 {
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) || (shift && inpututil.IsKeyJustPressed(ebiten.KeyH)) {
			g.llmsNavigateBack()
			return
		}
	}

	// Shift+L — navigate forward in llms.txt history.
	if g.mdViewerState.IsLLMS && len(g.llms.Forward) > 0 {
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

	g.handleViewerScroll()
}

// handleViewerFollowMode processes input when follow-link mode is active.
// Letter keys (a-z) follow the matching link; Esc or any other key cancels.
func (g *Game) handleViewerFollowMode() {
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
	}
}

// handleViewerScroll processes keyboard and mouse scroll input in the markdown viewer.
func (g *Game) handleViewerScroll() {
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

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
			g.mdViewerState.ScrollOffset -= viewerPageScrollH * rowH
		case ebiten.KeyPageDown:
			g.mdViewerState.ScrollOffset += viewerPageScrollH * rowH
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
		g.mdViewerState.ScrollOffset += viewerHalfPageH * rowH
		g.screenDirty = true
	}
	if ctrl && inpututil.IsKeyJustPressed(ebiten.KeyU) {
		g.mdViewerState.ScrollOffset -= viewerHalfPageH * rowH
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
			if now.Sub(g.llms.LastG) < viewerGGTimeout {
				g.mdViewerState.ScrollOffset = 0
				g.screenDirty = true
				g.llms.LastG = time.Time{} // reset
			} else {
				g.llms.LastG = now
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

	ti.Update(&g.repeats.MdSearch, meta, alt)

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

// convertMdLines converts markdown.StyledLine slices to renderer.MdStyledLine.
func convertMdLines(src []markdown.StyledLine) []renderer.MdStyledLine {
	out := make([]renderer.MdStyledLine, len(src))
	for i, line := range src {
		spans := make([]renderer.MdSpan, len(line.Spans))
		for j, s := range line.Spans {
			spans[j] = renderer.MdSpan{Text: s.Text, Style: renderer.MdSpanStyle(s.Style), Extra: s.Extra}
		}
		out[i] = renderer.MdStyledLine{Spans: spans, Indent: line.Indent}
	}
	return out
}

// spanToANSI converts a markdown span to ANSI-colored text for terminal display.
func spanToANSI(span renderer.MdSpan) string {
	const reset = "\033[0m"
	switch span.Style {
	case renderer.MdStyleHeading1:
		return "\033[1;97m" + span.Text + reset // bold bright white
	case renderer.MdStyleHeading2:
		return "\033[1;36m" + span.Text + reset // bold cyan
	case renderer.MdStyleHeading3:
		return "\033[2;37m" + span.Text + reset // dim white
	case renderer.MdStyleBold:
		return "\033[1;97m" + span.Text + reset // bold bright white
	case renderer.MdStyleItalic:
		return "\033[3;37m" + span.Text + reset // italic dim
	case renderer.MdStyleInlineCode:
		return "\033[7;33m" + span.Text + reset // reverse yellow
	case renderer.MdStyleCodeBlock:
		return "\033[32m" + span.Text + reset // green
	case renderer.MdStyleLink:
		if span.Extra != "" {
			return "\033[4;36m" + span.Text + reset + "\033[2m (" + span.Extra + ")" + reset
		}
		return "\033[4;36m" + span.Text + reset // underline cyan
	case renderer.MdStyleBlockquote:
		return "\033[2;37m" + span.Text + reset // dim
	case renderer.MdStyleListItem:
		return "\033[36m" + span.Text + reset // cyan marker
	case renderer.MdStyleTableHeader:
		return "\033[1;97m" + span.Text + reset // bold white
	case renderer.MdStyleTableCell:
		return span.Text
	case renderer.MdStyleTableSeparator, renderer.MdStyleHRule:
		return "\033[2m────────────────────────────────────────" + reset
	case renderer.MdStyleStrikethrough:
		return "\033[9;2m" + span.Text + reset // strikethrough dim
	case renderer.MdStyleImage:
		return "\033[35m" + span.Text + reset // magenta
	case renderer.MdStyleCheckboxChecked:
		return "\033[32m" + span.Text + reset // green
	case renderer.MdStyleCheckboxUnchecked:
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
			if span.Style == renderer.MdStyleLink && span.Extra != "" {
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
	g.llms.History = append(g.llms.History, llmsHistoryEntry{
		Domain:       g.llms.Domain,
		Short:        g.llms.Short,
		Full:         g.llms.Full,
		ViewingFull:  g.llms.ViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
}

// llmsNavigateBack pops from the back stack and pushes current to forward.
func (g *Game) llmsNavigateBack() {
	if len(g.llms.History) == 0 {
		return
	}
	// Push current to forward stack.
	g.llms.Forward = append(g.llms.Forward, llmsHistoryEntry{
		Domain:       g.llms.Domain,
		Short:        g.llms.Short,
		Full:         g.llms.Full,
		ViewingFull:  g.llms.ViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
	// Pop from history.
	entry := g.llms.History[len(g.llms.History)-1]
	g.llms.History = g.llms.History[:len(g.llms.History)-1]
	g.llmsRestoreEntry(entry)
}

// llmsNavigateForward pops from the forward stack and pushes current to back.
func (g *Game) llmsNavigateForward() {
	if len(g.llms.Forward) == 0 {
		return
	}
	// Push current to back stack.
	g.llms.History = append(g.llms.History, llmsHistoryEntry{
		Domain:       g.llms.Domain,
		Short:        g.llms.Short,
		Full:         g.llms.Full,
		ViewingFull:  g.llms.ViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
	// Pop from forward.
	entry := g.llms.Forward[len(g.llms.Forward)-1]
	g.llms.Forward = g.llms.Forward[:len(g.llms.Forward)-1]
	g.llmsRestoreEntry(entry)
}

// llmsRestoreEntry restores the viewer to a history entry's state.
func (g *Game) llmsRestoreEntry(entry llmsHistoryEntry) {
	g.llms.Domain = entry.Domain
	g.llms.Short = entry.Short
	g.llms.Full = entry.Full
	g.llms.ViewingFull = entry.ViewingFull

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
	g.mdViewerState.HistoryLen = len(g.llms.History)
	g.mdViewerState.ForwardLen = len(g.llms.Forward)
}

// llmsFollowLink handles following a link from the markdown viewer.
// If the URL looks like an llms.txt-capable domain, fetch it; otherwise open in browser.
func (g *Game) llmsFollowLink(url string) {
	// Check if this is an HTTP(S) URL we can try to fetch llms.txt from.
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		// Push current state to history and clear forward stack.
		g.llmsPushHistory()
		g.llms.Forward = nil
		g.startLLMSFetch(url)
		return
	}
	// Non-HTTP URL or relative path — open in system browser.
	if err := exec.Command("open", url).Start(); err != nil { // #nosec G204 — user-initiated URL open
		g.flashStatus("Failed to open URL: " + err.Error())
	}
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
	if g.llms.Domain != "" {
		paneName = "llms — " + g.llms.Domain
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
			case renderer.MdStyleHeading1:
				textLen := 0
				for _, s := range line.Spans {
					textLen += len([]rune(s.Text))
				}
				buf.WriteByte('\n')
				buf.WriteString("\033[97m")
				buf.WriteString(strings.Repeat("━", textLen))
				buf.WriteString("\033[0m")
			case renderer.MdStyleHeading2:
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
		_ = tmpFile.Close()
		g.flashStatus("Failed to write temp file: " + err.Error())
		return
	}
	if err := tmpFile.Close(); err != nil {
		log.Printf("viewer: temp file close failed: %v", err)
	}

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
	tmpPath := tmpFile.Name()
	if err := term.StartCmd("less", []string{"-R", tmpPath}, ""); err != nil {
		g.flashStatus("Failed to start less: " + err.Error())
		_ = os.Remove(tmpPath)
		return
	}
	go func() {
		<-term.Dead()
		if err := os.Remove(tmpPath); err != nil {
			log.Printf("viewer: temp file cleanup failed: %v", err)
		}
	}()
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
	g.tabMgr.Tabs = append(g.tabMgr.Tabs, t)
	g.switchTab(len(g.tabMgr.Tabs) - 1)
	g.screenDirty = true
}

