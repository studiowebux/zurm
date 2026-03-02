package renderer

import (
	"fmt"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/terminal"
)

// SearchState holds the live state of the in-buffer search (Cmd+F).
type SearchState struct {
	Open    bool
	Query   string
	Matches []terminal.SearchMatch
	Current int // index of the active (highlighted) match
}

// drawSearchBar renders the find bar above the status bar when search is open.
func (r *Renderer) drawSearchBar(state *SearchState) {
	if state == nil || !state.Open {
		return
	}

	h := r.font.CellH + 8
	physH := r.offscreen.Bounds().Dy()
	physW := r.offscreen.Bounds().Dx()
	statusH := StatusBarHeight(r.font, r.cfg)
	barTop := physH - statusH - h
	barRect := image.Rect(0, barTop, physW, barTop+h)

	barBg := config.ParseHexColor(r.cfg.Colors.Background)
	r.offscreen.SubImage(barRect).(*ebiten.Image).Fill(barBg)
	r.offscreen.SubImage(image.Rect(0, barTop, physW, barTop+1)).(*ebiten.Image).Fill(r.borderColor)

	fg := config.ParseHexColor(r.cfg.Colors.Foreground)
	dimFg := config.ParseHexColor(r.cfg.Colors.BrightBlack)
	redFg := config.ParseHexColor(r.cfg.Colors.Red)
	textY := barTop + (h-r.font.CellH)/2
	x := r.font.CellW

	// Label.
	const label = "Find: "
	r.font.DrawString(r.offscreen, label, x, textY, dimFg)
	x += len([]rune(label)) * r.font.CellW

	// Query + blinking cursor marker.
	r.font.DrawString(r.offscreen, state.Query+"_", x, textY, fg)

	// Right side: match count then navigation hint.
	hint := "↑↓ navigate · Esc close"
	hintW := len([]rune(hint)) * r.font.CellW
	rightX := physW - r.font.CellW - hintW
	r.font.DrawString(r.offscreen, hint, rightX, textY, dimFg)

	if state.Query != "" {
		var countStr string
		var countColor color.RGBA
		if len(state.Matches) == 0 {
			countStr = "no matches"
			countColor = redFg
		} else {
			countStr = fmt.Sprintf("%d / %d", state.Current+1, len(state.Matches))
			countColor = fg
		}
		countW := len([]rune(countStr)) * r.font.CellW
		rightX -= r.font.CellW + countW
		r.font.DrawString(r.offscreen, countStr, rightX, textY, countColor)
	}
}
