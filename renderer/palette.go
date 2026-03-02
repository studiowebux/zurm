package renderer

import (
	"image"
	"image/color"
	"strings"

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
	Open   bool
	Query  string
	Cursor int // index into the filtered list
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
	if query == "" {
		idx := make([]int, len(entries))
		for i := range idx {
			idx[i] = i
		}
		return entries, idx
	}
	q := strings.ToLower(query)
	type ranked struct {
		entry    PaletteEntry
		orig     int
		matchPos int
	}
	var ranked1, ranked2 []ranked // rank 0: match at start; rank 1: match elsewhere
	for i, e := range entries {
		pos := strings.Index(strings.ToLower(e.Name), q)
		if pos < 0 {
			continue
		}
		r := ranked{entry: e, orig: i, matchPos: pos}
		if pos == 0 {
			ranked1 = append(ranked1, r)
		} else {
			ranked2 = append(ranked2, r)
		}
	}
	all := append(ranked1, ranked2...)
	out := make([]PaletteEntry, len(all))
	origIdx := make([]int, len(all))
	for i, r := range all {
		out[i] = r.entry
		origIdx[i] = r.orig
	}
	return out, origIdx
}

// drawPalette renders the command palette overlay.
// entries is the already-filtered list; state.Cursor indexes into it.
func (r *Renderer) drawPalette(allEntries []PaletteEntry, state *PaletteState) {
	if !state.Open {
		return
	}

	physW := r.modalLayer.Bounds().Dx()
	physH := r.modalLayer.Bounds().Dy()

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
	drawRect(r.modalLayer, panelRect, ui.Border)

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
	// Cursor bar after query text.
	cursorX := queryX + len([]rune(query))*r.font.CellW
	r.modalLayer.SubImage(image.Rect(cursorX, promptY, cursorX+2, promptY+r.font.CellH)).(*ebiten.Image).Fill(ui.Accent)

	// Divider below input.
	divY := panelY + inputH
	r.modalLayer.SubImage(image.Rect(panelX+1, divY, panelX+panelW-1, divY+1)).(*ebiten.Image).Fill(ui.Border)

	if len(filtered) == 0 {
		noMatchY := divY + 2 + (rowH-r.font.CellH)/2
		r.font.DrawString(r.modalLayer, "no matches", panelX+palPad*r.font.CellW/2, noMatchY, ui.Dim)
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
		name := truncatePalette(entry.Name, nameMaxW/r.font.CellW)
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
func drawRect(img *ebiten.Image, r image.Rectangle, c color.RGBA) {
	img.SubImage(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1)).(*ebiten.Image).Fill(c)
	img.SubImage(image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y)).(*ebiten.Image).Fill(c)
	img.SubImage(image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y)).(*ebiten.Image).Fill(c)
	img.SubImage(image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y)).(*ebiten.Image).Fill(c)
}

func truncatePalette(s string, maxCols int) string {
	r := []rune(s)
	if len(r) <= maxCols {
		return s
	}
	if maxCols > 1 {
		return string(r[:maxCols-1]) + "…"
	}
	return string(r[:maxCols])
}
