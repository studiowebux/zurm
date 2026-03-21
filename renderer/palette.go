package renderer

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
)

// PaletteEntry is a single command shown in the palette.
// Actions are not stored here — main.go holds a parallel []func() slice.
type PaletteEntry struct {
	Name     string
	Shortcut string
}

// PaletteState is the rendering + interaction state for the command palette.
type PaletteState struct {
	Open      bool
	Query     string
	CursorPos int // rune index of the text cursor within Query
	Cursor    int // index into the filtered list
}


const (
	palMaxVisible  = 12 // max visible entries at once
	palPanelWidthP = 60 // panel width as % of screen
	palPad         = 8  // horizontal padding (cells)
)

// FilterPalette returns the subset of entries matching query (case-insensitive
// substring match) sorted by match position (earlier = higher rank).
// Returns the filtered entries and a mapping from filtered index → original index.
func FilterPalette(entries []PaletteEntry, query string) ([]PaletteEntry, []int) {
	results := filterBySubstring(len(entries), query, func(i int) []string {
		return []string{entries[i].Name}
	})
	out := make([]PaletteEntry, len(results))
	idx := make([]int, len(results))
	for i, r := range results {
		out[i] = entries[r.index]
		idx[i] = r.index
	}
	return out, idx
}

// drawPalette renders the command palette overlay.
// entries is the already-filtered list; state.Cursor indexes into it.
func (r *Renderer) drawPalette(allEntries []PaletteEntry, state *PaletteState) {
	if !state.Open {
		return
	}

	physW, physH := r.modalSize()

	filtered, _ := FilterPalette(allEntries, state.Query)
	visible := len(filtered)
	if visible > palMaxVisible {
		visible = palMaxVisible
	}

	inputH := r.font.CellH + 10
	rowH := r.font.CellH + 6
	panelH := inputH + 2 + visible*rowH + 6 // +2 divider, +6 bottom pad
	panelW := physW * palPanelWidthP / 100
	if panelW < 40*r.font.CellW {
		panelW = 40 * r.font.CellW
	}
	if panelW > physW-4*r.font.CellW {
		panelW = physW - 4*r.font.CellW
	}

	panelX := (physW - panelW) / 2
	panelY := physH / 6

	ui := r.ui

	// Backdrop.
	r.modalLayer.Fill(ui.Backdrop)

	// Panel background.
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(ui.PanelBg)

	// Panel border.
	drawBorder(r.modalLayer, panelRect, ui.Border)

	// Input area background.
	inputRect := image.Rect(panelX+1, panelY+1, panelX+panelW-1, panelY+inputH)
	r.modalLayer.SubImage(inputRect).(*ebiten.Image).Fill(ui.HoverBg)

	// Prompt + query.
	promptX := panelX + palPad*r.font.CellW/2
	promptY := panelY + (inputH-r.font.CellH)/2
	r.font.DrawString(r.modalLayer, ">", promptX, promptY, ui.Accent)
	queryX := promptX + 2*r.font.CellW
	query := state.Query
	if query == "" {
		r.font.DrawString(r.modalLayer, "type to filter...", queryX, promptY, ui.Dim)
	} else {
		r.font.DrawString(r.modalLayer, query, queryX, promptY, ui.Fg)
	}
	// Cursor bar at CursorPos within query text.
	cursorX := queryX + state.CursorPos*r.font.CellW
	r.modalLayer.SubImage(image.Rect(cursorX, promptY, cursorX+2, promptY+r.font.CellH)).(*ebiten.Image).Fill(ui.Accent)

	// Divider below input.
	divY := panelY + inputH
	r.modalLayer.SubImage(image.Rect(panelX+1, divY, panelX+panelW-1, divY+1)).(*ebiten.Image).Fill(ui.Border)

	if len(filtered) == 0 {
		noMatchY := divY + 2 + (rowH-r.font.CellH)/2
		r.font.DrawString(r.modalLayer, noMatchesLabel, panelX+palPad*r.font.CellW/2, noMatchY, ui.Dim)
		return
	}

	// Scroll window: keep cursor visible.
	scrollOffset := 0
	if state.Cursor >= palMaxVisible {
		scrollOffset = state.Cursor - palMaxVisible + 1
	}

	shortcutColW := 16 * r.font.CellW // reserved right column for shortcuts
	nameMaxW := panelW - palPad*r.font.CellW - shortcutColW - palPad*r.font.CellW/2

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

		textY := rowY + (rowH-r.font.CellH)/2
		nameX := panelX + palPad*r.font.CellW/2

		// Truncate name if needed.
		name := truncateRunes(entry.Name, nameMaxW/r.font.CellW)
		fg := ui.Fg
		if idx == state.Cursor {
			fg = ui.Accent
		}
		r.font.DrawString(r.modalLayer, name, nameX, textY, fg)

		// Shortcut right-aligned.
		if entry.Shortcut != "" {
			scW := len([]rune(entry.Shortcut)) * r.font.CellW
			scX := panelX + panelW - palPad*r.font.CellW/2 - scW
			r.font.DrawString(r.modalLayer, entry.Shortcut, scX, textY, ui.KeyName)
		}

		rowY += rowH
	}
}

// drawRect draws a 1px border around rect.
// truncateRunes truncates s to maxCols runes, appending "…" if truncated.
func truncateRunes(s string, maxCols int) string {
	r := []rune(s)
	if len(r) <= maxCols {
		return s
	}
	if maxCols > 1 {
		return string(r[:maxCols-1]) + "…"
	}
	return string(r[:maxCols])
}
