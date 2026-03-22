package renderer

import (
	"fmt"
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/tab"
)

func (r *Renderer) drawTabBar(tabs []*tab.Tab, activeTab int, hintMode bool) {
	tabBarH := r.TabBarHeight()
	physW, _ := r.screenSize()
	numTabs := len(tabs)
	if numTabs == 0 {
		return
	}

	// Clear the entire tab bar area to prevent artifacts on reorder/close.
	// Use darkened background (matching status bar) so divider lines are visible.
	tabBarBg := darken(parseHexColor(r.cfg.Colors.Background))
	tabBarRect := image.Rect(0, 0, physW, tabBarH)
	r.offscreen.SubImage(tabBarRect).(*ebiten.Image).Fill(tabBarBg)

	// Separator line at the bottom of the tab bar.
	r.offscreen.SubImage(image.Rect(0, tabBarH-1, physW, tabBarH)).(*ebiten.Image).Fill(r.separatorColor())

	// Each tab gets equal width, capped at configured max.
	maxTabW := r.cfg.Tabs.MaxWidthChars * r.font.CellW
	tabW := physW / numTabs
	if tabW > maxTabW {
		tabW = maxTabW
	}

	activeBg := parseHexColor(r.cfg.Colors.Background)
	activeFg := parseHexColor(r.cfg.Colors.Foreground)
	inactiveFg := parseHexColor(r.cfg.Colors.BrightBlack)
	divider := r.separatorColor()

	for i, t := range tabs {
		x := i * tabW
		tabRect := image.Rect(x, 0, x+tabW, tabBarH)

		if i == activeTab {
			r.offscreen.SubImage(tabRect).(*ebiten.Image).Fill(activeBg)
			// Accent line at bottom of active tab.
			r.offscreen.SubImage(image.Rect(x, tabBarH-2, x+tabW, tabBarH)).(*ebiten.Image).Fill(r.cursorColor)
		}

		// Right-edge divider between tabs (skip last).
		if i < numTabs-1 {
			r.offscreen.SubImage(image.Rect(x+tabW-1, 0, x+tabW, tabBarH)).(*ebiten.Image).Fill(divider)
		}

		// Build the display string: rename/note input if active, otherwise the tab title.
		// Pinned tabs are prefixed with ·N to indicate their fixed slot.
		// Tabs with notes show a trailing * indicator.
		var title string
		if t.Noting {
			title = "Note: " + inputWithCursor(t.NoteText, t.NoteCursorPos)
		} else if t.Renaming {
			title = inputWithCursor(t.RenameText, t.RenameCursorPos)
		} else {
			title = t.DisplayTitle(i)
			if t.PinnedSlot != 0 {
				title = fmt.Sprintf("\u00b7%c %s", t.PinnedSlot, title)
			}
			if t.Note != "" {
				title = title + " *"
			}
		}
		maxCols := (tabW - r.font.CellW) / r.font.CellW
		if maxCols < 1 {
			maxCols = 1
		}
		if StringDisplayWidth(title) > maxCols {
			runes := []rune(title)
			cols := 0
			cut := 0
			for i, ch := range runes {
				w := RuneDisplayWidth(ch)
				if cols+w > maxCols-1 {
					cut = i
					break
				}
				cols += w
				cut = i + 1
			}
			if cut > 0 {
				title = string(runes[:cut]) + "…"
			} else {
				title = "…"
			}
		}

		fg := inactiveFg
		if i == activeTab {
			fg = activeFg
		}

		// Vertically center text in the tab bar.
		textY := (tabBarH - r.font.CellH) / 2
		r.font.DrawString(r.offscreen, title, x+r.font.CellW/2, textY, fg)

		// Activity dot for background tabs with unseen output.
		if i != activeTab && t.HasActivity {
			dotSize := r.font.CellH / 4
			if dotSize < 3 {
				dotSize = 3
			}
			dotX := x + tabW - r.font.CellW/2 - dotSize
			dotY := (tabBarH - dotSize) / 2
			dotRect := image.Rect(dotX, dotY, dotX+dotSize, dotY+dotSize)
			dotColor := r.cursorColor
			if t.HasBell {
				dotColor = r.bellColor
			}
			r.offscreen.SubImage(dotRect).(*ebiten.Image).Fill(dotColor)
		}

		// Hint mode: overlay tab number badge (1-9) when Cmd is held.
		if hintMode && i < 9 {
			badge := fmt.Sprintf("%d", i+1)
			badgeW := r.font.CellW + 6
			badgeH := r.font.CellH + 4
			badgeX := x + (tabW-badgeW)/2
			badgeY := (tabBarH - badgeH) / 2
			badgeRect := image.Rect(badgeX, badgeY, badgeX+badgeW, badgeY+badgeH)
			r.offscreen.SubImage(badgeRect).(*ebiten.Image).Fill(r.cursorColor)
			textX := badgeX + (badgeW-r.font.CellW)/2
			textY := badgeY + 2
			r.font.DrawString(r.offscreen, badge, textX, textY, parseHexColor(r.cfg.Colors.Background))
		}
	}
}

// DrawPane renders a single pane into r.offscreen at the given physical rect.
// buf must be read-locked by the caller.
// isFocused controls cursor rendering; showBorder controls the focus border (multi-pane only).
// search, when non-nil and open, highlights matched cells in the pane.
