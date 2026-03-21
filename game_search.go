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

func (g *Game) openSearch() {
	g.searchState.Open = true
	g.screenDirty = true
	if g.focused != nil {
		g.focused.Term.Buf.BumpRenderGen()
	}
}

// closeSearch clears all search state including matches and query.
// Called on Esc to fully exit search mode.
func (g *Game) closeSearch() {
	g.searchState = renderer.SearchState{}
	g.lastSearchQuery = ""
	g.screenDirty = true
	if g.focused != nil {
		g.focused.Term.Buf.BumpRenderGen()
	}
}

// recomputeSearch triggers an async SearchAll when the query changes and
// drains completed results each frame. Called every Update.
func (g *Game) recomputeSearch() {
	if g.focused == nil {
		return
	}

	// Drain completed search result (arrives from background goroutine).
	select {
	case res := <-g.searchResultCh:
		if res.gen == g.searchGen {
			g.searchState.Matches = res.matches
			g.searchState.Current = 0
			if len(res.matches) > 0 {
				g.jumpToMatch(0)
			}
			g.focused.Term.Buf.BumpRenderGen()
			g.screenDirty = true
		}
	default:
	}

	if !g.searchState.Open || g.searchState.Query == g.lastSearchQuery {
		return
	}
	g.lastSearchQuery = g.searchState.Query

	// Empty query — clear immediately, no goroutine needed.
	if g.searchState.Query == "" {
		g.searchState.Matches = nil
		g.searchState.Current = 0
		g.focused.Term.Buf.BumpRenderGen()
		g.screenDirty = true
		return
	}

	// Drain stale result before spawning to keep the channel available.
	select {
	case <-g.searchResultCh:
	default:
	}
	g.searchGen++
	gen := g.searchGen
	query := g.searchState.Query
	buf := g.focused.Term.Buf
	ch := g.searchResultCh
	go func() {
		buf.RLock()
		matches := buf.SearchAll(query)
		buf.RUnlock()
		select {
		case ch <- searchResult{gen: gen, matches: matches}:
		default:
		}
	}()
}

// jumpToMatch scrolls the focused pane so match i is centered on screen.
func (g *Game) jumpToMatch(i int) {
	if i < 0 || i >= len(g.searchState.Matches) {
		return
	}
	g.searchState.Current = i
	m := g.searchState.Matches[i]
	g.focused.Term.Buf.RLock()
	sbLen := g.focused.Term.Buf.ScrollbackLen()
	rows := g.focused.Term.Buf.Rows
	g.focused.Term.Buf.RUnlock()

	viewOffset := sbLen - m.AbsRow + rows/2
	if viewOffset < 0 {
		viewOffset = 0
	}
	if viewOffset > sbLen {
		viewOffset = sbLen
	}
	g.focused.Term.Buf.Lock()
	g.focused.Term.Buf.SetViewOffset(viewOffset)
	g.focused.Term.Buf.Unlock()
	g.screenDirty = true
}

// searchNext advances to the next match, wrapping around.
func (g *Game) searchNext() {
	if len(g.searchState.Matches) == 0 {
		return
	}
	g.jumpToMatch((g.searchState.Current + 1) % len(g.searchState.Matches))
}

// searchPrev retreats to the previous match, wrapping around.
func (g *Game) searchPrev() {
	if len(g.searchState.Matches) == 0 {
		return
	}
	n := len(g.searchState.Matches)
	g.jumpToMatch((g.searchState.Current - 1 + n) % n)
}

// handleSearchInput routes keyboard events while the search bar is open.
func (g *Game) handleSearchInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeSearch()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Arrow up/down navigate search results (edge-triggered).
	for _, key := range []ebiten.Key{ebiten.KeyArrowDown, ebiten.KeyArrowUp} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			if key == ebiten.KeyArrowDown {
				g.searchNext()
			} else {
				g.searchPrev()
			}
		}
		g.prevKeys[key] = pressed
	}

	ti := &TextInput{Text: g.searchState.Query, CursorPos: g.searchState.CursorPos}

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

	prevQuery := g.searchState.Query
	prevCursor := g.searchState.CursorPos
	ti.Update(&g.searchRepeat, meta, alt)
	if ti.Text != prevQuery || ti.CursorPos != prevCursor {
		g.screenDirty = true
	}

	g.searchState.Query = ti.Text
	g.searchState.CursorPos = ti.CursorPos
}
