package main

import (
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/terminal"
)

// searchResult carries the output of an async SearchAll call.
type searchResult struct {
	gen     uint64
	matches []terminal.SearchMatch
}

// SearchController owns in-buffer search state (Cmd+F) and logic.
// Game holds a *SearchController and delegates all search operations to it.
type SearchController struct {
	State     renderer.SearchState
	lastQuery string
	resultCh  chan searchResult
	gen       uint64
	repeat    TextInputRepeat
}

// NewSearchController creates a ready-to-use search controller.
func NewSearchController() *SearchController {
	return &SearchController{
		resultCh: make(chan searchResult, 1),
	}
}

// Open activates the search overlay.
func (sc *SearchController) Open() {
	sc.State.Open = true
}

// Close deactivates search and clears all state including matches and query.
func (sc *SearchController) Close() {
	sc.State = renderer.SearchState{}
	sc.lastQuery = ""
}

// Recompute triggers an async SearchAll when the query changes and drains
// completed results. Returns true if the screen needs redrawing.
func (sc *SearchController) Recompute(buf *terminal.ScreenBuffer) bool {
	if buf == nil {
		return false
	}
	dirty := false

	// Drain completed search result (arrives from background goroutine).
	select {
	case res := <-sc.resultCh:
		if res.gen == sc.gen {
			sc.State.Matches = res.matches
			sc.State.Current = 0
			if len(res.matches) > 0 {
				sc.jumpToMatch(0, buf)
			}
			buf.BumpRenderGen()
			dirty = true
		}
	default:
	}

	if !sc.State.Open || sc.State.Query == sc.lastQuery {
		return dirty
	}
	sc.lastQuery = sc.State.Query

	// Empty query — clear immediately, no goroutine needed.
	if sc.State.Query == "" {
		sc.State.Matches = nil
		sc.State.Current = 0
		buf.BumpRenderGen()
		return true
	}

	// Drain stale result before spawning to keep the channel available.
	select {
	case <-sc.resultCh:
	default:
	}
	sc.gen++
	gen := sc.gen
	query := sc.State.Query
	ch := sc.resultCh
	go func() {
		buf.RLock()
		matches := buf.SearchAll(query)
		buf.RUnlock()
		select {
		case ch <- searchResult{gen: gen, matches: matches}:
		default:
		}
	}()
	return dirty
}

// jumpToMatch scrolls the buffer so match i is centered on screen.
func (sc *SearchController) jumpToMatch(i int, buf *terminal.ScreenBuffer) {
	if i < 0 || i >= len(sc.State.Matches) {
		return
	}
	sc.State.Current = i
	m := sc.State.Matches[i]
	buf.RLock()
	sbLen := buf.ScrollbackLen()
	rows := buf.Rows
	buf.RUnlock()

	viewOffset := sbLen - m.AbsRow + rows/2
	if viewOffset < 0 {
		viewOffset = 0
	}
	if viewOffset > sbLen {
		viewOffset = sbLen
	}
	buf.Lock()
	buf.SetViewOffset(viewOffset)
	buf.Unlock()
}

// Next advances to the next match, wrapping around. Returns true if dirty.
func (sc *SearchController) Next(buf *terminal.ScreenBuffer) bool {
	if len(sc.State.Matches) == 0 {
		return false
	}
	sc.jumpToMatch((sc.State.Current+1)%len(sc.State.Matches), buf)
	return true
}

// Prev retreats to the previous match, wrapping around. Returns true if dirty.
func (sc *SearchController) Prev(buf *terminal.ScreenBuffer) bool {
	if len(sc.State.Matches) == 0 {
		return false
	}
	n := len(sc.State.Matches)
	sc.jumpToMatch((sc.State.Current-1+n)%n, buf)
	return true
}

// openSearchOverlay opens the search bar and marks the screen dirty.
func (g *Game) openSearchOverlay() {
	g.search.Open()
	g.screenDirty = true
	if g.activeFocused() != nil {
		g.activeFocused().Term.Buf.BumpRenderGen()
	}
}

// closeSearchOverlay closes the search bar, clears state, and marks the screen dirty.
func (g *Game) closeSearchOverlay() {
	g.search.Close()
	g.screenDirty = true
	if g.activeFocused() != nil {
		g.activeFocused().Term.Buf.BumpRenderGen()
	}
}

// handleSearchInput routes keyboard events while the search bar is open.
// This stays on Game because it accesses prevKeys, clipboard, and TextInput.
func (g *Game) handleSearchInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeSearchOverlay()
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	// Arrow up/down navigate search results (edge-triggered).
	for _, key := range []ebiten.Key{ebiten.KeyArrowDown, ebiten.KeyArrowUp} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		if pressed && !wasPressed {
			var buf *terminal.ScreenBuffer
			if g.activeFocused() != nil {
				buf = g.activeFocused().Term.Buf
			}
			if key == ebiten.KeyArrowDown {
				if g.search.Next(buf) {
					g.screenDirty = true
				}
			} else {
				if g.search.Prev(buf) {
					g.screenDirty = true
				}
			}
		}
		g.input.PrevKeys[key] = pressed
	}

	ti := &TextInput{Text: g.search.State.Query, CursorPos: g.search.State.CursorPos}

	// Cmd+V — async clipboard paste into search query.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		g.requestClipboard()
	}
	select {
	case clip := <-g.clipboardCh:
		line := strings.TrimSpace(strings.SplitN(clip, "\n", 2)[0])
		if line != "" {
			ti.AddString(line)
			g.screenDirty = true
		}
	default:
	}

	prevQuery := g.search.State.Query
	prevCursor := g.search.State.CursorPos
	ti.Update(&g.search.repeat, meta, alt)
	if ti.Text != prevQuery || ti.CursorPos != prevCursor {
		g.screenDirty = true
	}

	g.search.State.Query = ti.Text
	g.search.State.CursorPos = ti.CursorPos
}
