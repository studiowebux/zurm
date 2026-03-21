package renderer

import (
	"fmt"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
)

// drawSearchBar renders the find bar above the status bar when search is open.
func (r *Renderer) drawSearchBar(state *SearchState) {
	if state == nil || !state.Open {
		return
	}

	h := r.font.CellH + 8
	physW, physH := r.screenSize()
	statusH := StatusBarHeight(r.font, r.cfg)
	barTop := physH - statusH - h
	barRect := image.Rect(0, barTop, physW, barTop+h)

	barBg := parseHexColor(r.cfg.Colors.Background)
	r.offscreen.SubImage(barRect).(*ebiten.Image).Fill(barBg)
	r.offscreen.SubImage(image.Rect(0, barTop, physW, barTop+1)).(*ebiten.Image).Fill(r.borderColor)

	fg := parseHexColor(r.cfg.Colors.Foreground)
	dimFg := parseHexColor(r.cfg.Colors.BrightBlack)
	redFg := parseHexColor(r.cfg.Colors.Red)
	textY := barTop + (h-r.font.CellH)/2
	x := r.font.CellW

	// Label.
	const label = "Find: "
	r.font.DrawString(r.offscreen, label, x, textY, dimFg)
	x += len([]rune(label)) * r.font.CellW

	// Query + cursor at insertion point.
	r.font.DrawString(r.offscreen, inputWithCursor(state.Query, state.CursorPos), x, textY, fg)

	// Right side: match count then navigation hint.
	hint := "↑↓ navigate · Esc close"
	hintW := len([]rune(hint)) * r.font.CellW
	rightX := physW - r.font.CellW - hintW
	r.font.DrawString(r.offscreen, hint, rightX, textY, dimFg)

	if state.Query != "" {
		var countStr string
		var countColor color.RGBA
		if len(state.Matches) == 0 {
			countStr = noMatchesLabel
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
