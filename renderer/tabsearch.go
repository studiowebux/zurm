package renderer

import (
	"image"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/tab"
)

// TabSearchState holds rendering and interaction state for the Cmd+J tab search overlay.
type TabSearchState struct {
	Open      bool
	Query     string
	CursorPos int // rune index of the text cursor within Query
	Cursor    int // index into the filtered list
}

const (
	tsMaxVisible  = 12 // max visible entries
	tsPanelWidthP = 60 // panel width as % of screen
	tsPad         = 8  // horizontal padding (cells)
)

// TabSearchEntry is a filtered tab entry with its original index preserved.
type TabSearchEntry struct {
	DisplayTitle string
	PinnedSlot   rune
	Cwd          string
	OrigIdx      int
	matchPos     int
}

// FilterTabSearch returns tabs matching query (case-insensitive substring on title or CWD).
// Sorted by match position (earlier = higher rank).
func FilterTabSearch(tabs []*tab.Tab, query string) []TabSearchEntry {
	entries := make([]TabSearchEntry, 0, len(tabs))
	for i, t := range tabs {
		title := t.DisplayTitle(i)
		cwd := ""
		if leaves := t.Layout.Leaves(); len(leaves) > 0 {
			cwd = leaves[0].Pane.Term.Cwd
		}
		entries = append(entries, TabSearchEntry{
			DisplayTitle: title,
			PinnedSlot:   t.PinnedSlot,
			Cwd:          cwd,
			OrigIdx:      i,
		})
	}

	results := filterBySubstring(len(entries), query, func(i int) []string {
		return []string{entries[i].DisplayTitle, entries[i].Cwd}
	})
	filtered := make([]TabSearchEntry, len(results))
	for i, r := range results {
		e := entries[r.index]
		e.matchPos = r.matchPos
		filtered[i] = e
	}
	return filtered
}

// drawTabSearch renders the tab search overlay onto r.modalLayer.
func (r *Renderer) drawTabSearch(tabs []*tab.Tab, activeTab int, state *TabSearchState) {
	if state == nil || !state.Open {
		return
	}

	physW, physH := r.modalSize()

	filtered := FilterTabSearch(tabs, state.Query)
	visible := len(filtered)
	if visible > tsMaxVisible {
		visible = tsMaxVisible
	}

	cw := r.font.CellW
	ch := r.font.CellH
	ui := r.ui

	inputH := ch + 10
	rowH := ch + 6
	panelH := inputH + 2 + visible*rowH + 6
	if len(filtered) == 0 {
		panelH = inputH + 2 + rowH + 6 // room for noMatchesLabel
	}
	panelW := physW * tsPanelWidthP / 100
	if panelW < 40*cw {
		panelW = 40 * cw
	}
	if panelW > physW-4*cw {
		panelW = physW - 4*cw
	}

	panelX := (physW - panelW) / 2
	panelY := physH / 6

	// Backdrop.
	r.modalLayer.Fill(ui.Backdrop)

	// Panel background.
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(ui.PanelBg)
	drawBorder(r.modalLayer, panelRect, ui.Border)

	// Input area.
	inputRect := image.Rect(panelX+1, panelY+1, panelX+panelW-1, panelY+inputH)
	r.modalLayer.SubImage(inputRect).(*ebiten.Image).Fill(ui.HoverBg)

	promptX := panelX + tsPad*cw/2
	promptY := panelY + (inputH-ch)/2
	r.font.DrawString(r.modalLayer, ">", promptX, promptY, ui.Accent)
	queryX := promptX + 2*cw
	query := state.Query
	if query == "" {
		r.font.DrawString(r.modalLayer, "search tabs...", queryX, promptY, ui.Dim)
	} else {
		r.font.DrawString(r.modalLayer, query, queryX, promptY, ui.Fg)
	}
	// Cursor bar at CursorPos within query text.
	cursorX := queryX + state.CursorPos*cw
	r.modalLayer.SubImage(image.Rect(cursorX, promptY, cursorX+2, promptY+ch)).(*ebiten.Image).Fill(ui.Accent)

	// Divider below input.
	divY := panelY + inputH
	r.modalLayer.SubImage(image.Rect(panelX+1, divY, panelX+panelW-1, divY+1)).(*ebiten.Image).Fill(ui.Border)

	if len(filtered) == 0 {
		noMatchY := divY + 2 + (rowH-ch)/2
		r.font.DrawString(r.modalLayer, noMatchesLabel, panelX+tsPad*cw/2, noMatchY, ui.Dim)
		return
	}

	// Scroll window: keep cursor visible.
	scrollOffset := 0
	if state.Cursor >= tsMaxVisible {
		scrollOffset = state.Cursor - tsMaxVisible + 1
	}

	badgeW := 4 * cw // "[N] " or "    "
	cwdColW := 20 * cw
	nameMaxW := panelW - tsPad*cw - badgeW - cwdColW - tsPad*cw/2

	rowY := divY + 2
	for i := 0; i < visible; i++ {
		idx := i + scrollOffset
		if idx >= len(filtered) {
			break
		}
		entry := filtered[idx]
		rowRect := image.Rect(panelX+1, rowY, panelX+panelW-1, rowY+rowH)

		if idx == state.Cursor {
			r.modalLayer.SubImage(rowRect).(*ebiten.Image).Fill(ui.HoverBg)
		}

		textY := rowY + (rowH-ch)/2
		nameX := panelX + tsPad*cw/2

		// Pin badge.
		badge := pinnedBadge(entry.PinnedSlot, "   ") + " "
		badgeColor := ui.KeyName
		if idx == state.Cursor {
			badgeColor = ui.Accent
		}
		r.font.DrawString(r.modalLayer, badge, nameX, textY, badgeColor)

		// Tab title.
		titleColor := ui.Fg
		if entry.OrigIdx == activeTab {
			titleColor = ui.Accent
		}
		if idx == state.Cursor {
			titleColor = ui.Accent
		}
		maxTitleCols := nameMaxW / cw
		title := truncateRunes(entry.DisplayTitle, maxTitleCols)
		r.font.DrawString(r.modalLayer, title, nameX+badgeW, textY, titleColor)

		// CWD right-aligned (dimmed, truncated).
		if entry.Cwd != "" {
			cwdText := shortenCwd(entry.Cwd, cwdColW/cw)
			cwdW := len([]rune(cwdText)) * cw
			cwdX := panelX + panelW - tsPad*cw/2 - cwdW
			r.font.DrawString(r.modalLayer, cwdText, cwdX, textY, ui.Dim)
		}

		rowY += rowH
	}
}

// shortenCwd truncates a CWD path to fit maxCols, showing the last path segments.
func shortenCwd(path string, maxCols int) string {
	if maxCols < 2 {
		return "…"
	}
	if len([]rune(path)) <= maxCols {
		return path
	}
	// Show "…/last/segments"
	parts := strings.Split(path, "/")
	result := ""
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := parts[i]
		if result != "" {
			candidate = parts[i] + "/" + result
		}
		if len([]rune(candidate))+1 > maxCols { // +1 for "…"
			break
		}
		result = candidate
	}
	if result == "" {
		r := []rune(path)
		if len(r) > maxCols-1 {
			return "…" + string(r[len(r)-maxCols+1:])
		}
		return path
	}
	return "…/" + result
}
