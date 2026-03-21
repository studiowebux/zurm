package renderer

import (
	"fmt"
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/tab"
)

// TabSwitcherState holds the rendering and interaction state for the tab switcher overlay.
type TabSwitcherState struct {
	Open   bool
	Cursor int // index of the highlighted row
}

// drawTabSwitcher renders the pin-style tab picker over r.modalLayer.
func (r *Renderer) drawTabSwitcher(tabs []*tab.Tab, activeTab int, state *TabSwitcherState) {
	if state == nil || !state.Open {
		return
	}

	physW := r.modalLayer.Bounds().Dx()
	physH := r.modalLayer.Bounds().Dy()

	// Semi-transparent backdrop.
	if r.overlayBg == nil {
		r.overlayBg = ebiten.NewImage(1, 1)
		r.overlayBg.Fill(r.ui.Backdrop)
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(physW), float64(physH))
	r.modalLayer.DrawImage(r.overlayBg, op)

	cw := r.font.CellW
	ch := r.font.CellH

	rowH := ch + 4
	panelPad := cw
	badgeW := 4 * cw  // "[N] " or "[ ] "
	titleColW := 24 * cw
	innerW := badgeW + titleColW
	panelW := innerW + 2*panelPad

	headerH := ch + 12
	divH := 6
	hintH := ch + 8
	rowsH := len(tabs) * rowH
	panelH := headerH + divH + rowsH + 4 + divH + hintH + panelPad

	if panelW > physW {
		panelW = physW
	}
	if panelH > physH {
		panelH = physH
	}

	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(panelRect)

	// Title row.
	titleY := panelY + 4
	r.font.DrawString(r.modalLayer, "Tabs", panelX+panelPad, titleY, r.ui.Accent)
	escLabel := "Esc"
	r.font.DrawString(r.modalLayer, escLabel,
		panelX+panelW-panelPad-len([]rune(escLabel))*cw, titleY, r.ui.Dim)

	// Divider below title.
	divY := panelY + headerH - 2
	r.modalLayer.SubImage(image.Rect(panelX, divY, panelX+panelW, divY+1)).(*ebiten.Image).Fill(r.ui.Border)

	contentY := divY + divH/2 + 2

	maxTitleCols := titleColW / cw

	for i, t := range tabs {
		rowY := contentY + i*rowH

		// Cursor row highlight.
		if i == state.Cursor {
			rowRect := image.Rect(panelX+1, rowY-1, panelX+panelW-1, rowY+rowH-1)
			r.modalLayer.SubImage(rowRect).(*ebiten.Image).Fill(r.ui.HoverBg)
		}

		// Slot badge.
		var badge string
		if t.PinnedSlot != 0 {
			badge = fmt.Sprintf("[%c]", t.PinnedSlot)
		} else {
			badge = "[ ]"
		}

		badgeColor := r.ui.KeyName
		titleColor := r.ui.Fg
		if i == activeTab {
			titleColor = r.ui.Accent
		}
		if i == state.Cursor {
			badgeColor = r.ui.Accent
			titleColor = r.ui.Accent
		}

		r.font.DrawString(r.modalLayer, badge, panelX+panelPad, rowY+1, badgeColor)

		title := t.DisplayTitle(i)
		if StringDisplayWidth(title) > maxTitleCols {
			runes := []rune(title)
			cols := 0
			cut := 0
			for j, ch := range runes {
				w := RuneDisplayWidth(ch)
				if cols+w > maxTitleCols-1 {
					cut = j
					break
				}
				cols += w
				cut = j + 1
			}
			if cut > 0 {
				title = string(runes[:cut]) + "\u2026"
			} else {
				title = "\u2026"
			}
		}
		r.font.DrawString(r.modalLayer, title, panelX+panelPad+badgeW, rowY+1, titleColor)
	}

	// Divider above hint.
	bottomDivY := contentY + rowsH + 2
	r.modalLayer.SubImage(image.Rect(panelX, bottomDivY, panelX+panelW, bottomDivY+1)).(*ebiten.Image).Fill(r.ui.Border)

	// Hint line.
	hint := "\u2191\u2193 navigate   \u21e7\u2191\u21e7\u2193 reorder   \u21b5 switch"
	hintX := panelX + (panelW-len([]rune(hint))*cw)/2
	if hintX < panelX+panelPad {
		hintX = panelX + panelPad
	}
	r.font.DrawString(r.modalLayer, hint, hintX, bottomDivY+4, r.ui.Dim)
}
