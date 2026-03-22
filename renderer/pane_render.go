package renderer

import (
	"fmt"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/terminal"
)

func (r *Renderer) DrawPane(buf *terminal.ScreenBuffer, cur *terminal.Cursor,
	rect image.Rectangle, isFocused bool, showBorder bool, search *SearchState, headerH int) {
	r.drawPaneTo(r.offscreen, buf, cur, rect, isFocused, showBorder, search, headerH)
}

// drawPaneTo renders a single pane into an arbitrary destination image.
// Extracted from DrawPane so thumbnails can render to a temp image.
func (r *Renderer) drawPaneTo(dst *ebiten.Image, buf *terminal.ScreenBuffer, cur *terminal.Cursor,
	rect image.Rectangle, isFocused bool, showBorder bool, search *SearchState, headerH int) {

	bg := parseHexColor(r.cfg.Colors.Background)

	dst.SubImage(rect).(*ebiten.Image).Fill(bg)

	rows := buf.Rows
	cols := buf.Cols
	pad := r.padding
	cellW := r.font.CellW
	cellH := r.font.CellH

	originX := rect.Min.X
	originY := rect.Min.Y + headerH

	// Pre-index search matches by display row for O(1) lookup during cell iteration.
	type matchRange struct {
		colStart, colEnd int
		isCurrent        bool
	}
	var matchByRow [][]matchRange
	if search != nil && len(search.Matches) > 0 {
		matchByRow = make([][]matchRange, rows)
		sbLen := buf.ScrollbackLen()
		for i, m := range search.Matches {
			displayRow := m.AbsRow - sbLen + buf.ViewOffset
			if displayRow < 0 || displayRow >= rows {
				continue
			}
			matchByRow[displayRow] = append(matchByRow[displayRow],
				matchRange{m.Col, m.Col + m.Len, i == search.Current})
		}
	}

	searchBgOther := parseHexColor(r.cfg.Colors.Blue)
	searchBgCurrent := parseHexColor(r.cfg.Colors.Yellow)
	searchFg := parseHexColor(r.cfg.Colors.Background)

	for row := 0; row < rows; row++ {
		var rowMatches []matchRange
		if matchByRow != nil {
			rowMatches = matchByRow[row]
		}
		for col := 0; col < cols; col++ {
			cell := buf.GetDisplayCell(row, col)

			// Skip continuation cells — the wide char's first cell already drew both columns.
			if cell.Width == 0 {
				continue
			}

			wCells := 1
			if cell.Width == 2 {
				wCells = 2
			}

			fg := cell.FG
			cbg := cell.BG

			if cell.Inverse {
				fg, cbg = cbg, fg
			}

			absRow := buf.DisplayToAbsRow(row)
			if buf.Selection.Contains(absRow, col) {
				fg, cbg = cbg, fg
			}

			// Search highlights override selection.
			for _, mr := range rowMatches {
				if col >= mr.colStart && col < mr.colEnd {
					if mr.isCurrent {
						cbg = searchBgCurrent
					} else {
						cbg = searchBgOther
					}
					fg = searchFg
					break
				}
			}

			// URL hover underline on the focused pane.
			underline := cell.Underline
			if r.HoveredURL != nil && isFocused && r.HoveredURL.ContainsCell(row, col) {
				underline = true
				fg = r.ui.Accent
			}

			x := originX + col*cellW + pad
			y := originY + row*cellH + pad

			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}

			r.font.DrawGlyph(dst, ch, x, y, fg, cbg, cell.Bold, underline, wCells)

			// Skip the continuation column for wide chars so the outer loop doesn't re-process it.
			if wCells == 2 {
				col++
			}
		}
	}

	if isFocused && cur.IsVisible() && buf.ViewOffset == 0 && buf.CursorVisible {
		curRow := buf.CursorRow
		curCol := buf.CursorCol
		if curRow >= 0 && curRow < rows && curCol >= 0 && curCol < cols {
			cell := buf.GetDisplayCell(curRow, curCol)
			curW := 1
			if cell.Width == 2 {
				curW = 2
			}

			x := originX + curCol*cellW + pad
			y := originY + curRow*cellH + pad

			cursorStyle := 0
			switch cur.Style {
			case terminal.CursorBlock:
				cursorStyle = 0
			case terminal.CursorUnderline:
				cursorStyle = 1
			case terminal.CursorBar:
				cursorStyle = 2
			}

			r.font.DrawCursor(dst, x, y, cursorStyle, r.cursorColor, curW)

			if cur.Style == terminal.CursorBlock {
				ch := cell.Char
				if ch == 0 {
					ch = ' '
				}
				r.font.DrawGlyph(dst, ch, x, y,
					parseHexColor(r.cfg.Colors.Background),
					r.cursorColor,
					cell.Bold, cell.Underline, curW)
			}

			// Ghost text — vault suggestion rendered after cursor in dim color.
			if r.VaultSuggestion != "" {
				ghostColor := parseHexColor(r.cfg.Vault.SuggestionColor)
				ghostX := x + curW*cellW
				for _, gr := range r.VaultSuggestion {
					if ghostX >= rect.Max.X-pad {
						break // clip at pane edge
					}
					r.font.DrawGlyph(dst, gr, ghostX, y, ghostColor, bg, false, false, 1)
					ghostX += cellW
				}
			}
		}
	}

	if showBorder {
		r.drawBorderTo(dst, rect, r.cursorColor)
	}
}

// drawPaneOverlay renders a pane name label (top-left) and scroll position
// indicator (top-right) as small opaque pills on the pane content.
func (r *Renderer) drawPaneOverlay(rect image.Rectangle, label string, multiPane bool, viewOffset int, scrollbackLen int) {
	pad := r.padding
	cellW := r.font.CellW
	cellH := r.font.CellH

	pillBg := color.RGBA{R: r.ui.PanelBg.R, G: r.ui.PanelBg.G, B: r.ui.PanelBg.B, A: 220}
	pillFg := r.ui.Dim

	pillH := cellH + 4
	pillPad := 6

	// Pane name label — top-left, only when multiple panes visible.
	if multiPane && label != "" {
		labelW := len([]rune(label))*cellW + 2*pillPad
		labelRect := image.Rect(
			rect.Min.X+pad, rect.Min.Y+pad,
			rect.Min.X+pad+labelW, rect.Min.Y+pad+pillH,
		)
		sub := r.offscreen.SubImage(labelRect).(*ebiten.Image)
		sub.Fill(pillBg)
		r.font.DrawString(r.offscreen, label, rect.Min.X+pad+pillPad, rect.Min.Y+pad+2, pillFg)
	}

	// Scroll position indicator — top-right, always shown when scrolled.
	if viewOffset > 0 {
		text := fmt.Sprintf("↑ %d lines", viewOffset)
		textW := len([]rune(text))*cellW + 2*pillPad
		scrollRect := image.Rect(
			rect.Max.X-pad-textW, rect.Min.Y+pad,
			rect.Max.X-pad, rect.Min.Y+pad+pillH,
		)
		sub := r.offscreen.SubImage(scrollRect).(*ebiten.Image)
		sub.Fill(pillBg)
		r.font.DrawString(r.offscreen, text, rect.Max.X-pad-textW+pillPad, rect.Min.Y+pad+2, pillFg)
	}
}

// drawDividers paints the gap strips between sibling panes with the border color.
// Called every frame when the layout has not changed, so stale pixels from
// previously drawn panes don't bleed into the divider area.
func (r *Renderer) drawDividers(node *pane.LayoutNode) {
	if node == nil || node.Kind == pane.Leaf {
		return
	}
	left := node.Left
	right := node.Right
	if left != nil && right != nil {
		var gap image.Rectangle
		if node.Kind == pane.HSplit {
			// Vertical strip between left.Rect.Max.X and right.Rect.Min.X.
			if right.Rect.Min.X > left.Rect.Max.X {
				gap = image.Rect(left.Rect.Max.X, node.Rect.Min.Y, right.Rect.Min.X, node.Rect.Max.Y)
			}
		} else {
			// Horizontal strip between left.Rect.Max.Y and right.Rect.Min.Y.
			if right.Rect.Min.Y > left.Rect.Max.Y {
				gap = image.Rect(node.Rect.Min.X, left.Rect.Max.Y, node.Rect.Max.X, right.Rect.Min.Y)
			}
		}
		if !gap.Empty() {
			r.offscreen.SubImage(gap).(*ebiten.Image).Fill(r.borderColor)
		}
	}
	r.drawDividers(node.Left)
	r.drawDividers(node.Right)
}

// drawBorderTo draws a 1px rectangle border just inside rect on dst.
func (r *Renderer) drawBorderTo(dst *ebiten.Image, rect image.Rectangle, clr color.RGBA) {
	dst.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1)).(*ebiten.Image).Fill(clr)
	dst.SubImage(image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	dst.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	dst.SubImage(image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
}

// drawThickBorder draws a border of the given thickness just inside rect.
func (r *Renderer) drawThickBorder(rect image.Rectangle, clr color.RGBA, thick int) {
	img := r.offscreen
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+thick)).(*ebiten.Image).Fill(clr)
	img.SubImage(image.Rect(rect.Min.X, rect.Max.Y-thick, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+thick, rect.Max.Y)).(*ebiten.Image).Fill(clr)
	img.SubImage(image.Rect(rect.Max.X-thick, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(clr)
}

// drawDividersTo paints divider strips between sibling panes onto dst,
// offsetting all rects by (offsetX, offsetY) for thumbnail rendering.
func (r *Renderer) drawDividersTo(dst *ebiten.Image, node *pane.LayoutNode, offsetX, offsetY int) {
	if node == nil || node.Kind == pane.Leaf {
		return
	}
	left := node.Left
	right := node.Right
	if left != nil && right != nil {
		var gap image.Rectangle
		if node.Kind == pane.HSplit {
			if right.Rect.Min.X > left.Rect.Max.X {
				gap = image.Rect(
					left.Rect.Max.X+offsetX, node.Rect.Min.Y+offsetY,
					right.Rect.Min.X+offsetX, node.Rect.Max.Y+offsetY,
				)
			}
		} else {
			if right.Rect.Min.Y > left.Rect.Max.Y {
				gap = image.Rect(
					node.Rect.Min.X+offsetX, left.Rect.Max.Y+offsetY,
					node.Rect.Max.X+offsetX, right.Rect.Min.Y+offsetY,
				)
			}
		}
		if !gap.Empty() {
			dst.SubImage(gap).(*ebiten.Image).Fill(r.borderColor)
		}
	}
	r.drawDividersTo(dst, node.Left, offsetX, offsetY)
	r.drawDividersTo(dst, node.Right, offsetX, offsetY)
}
