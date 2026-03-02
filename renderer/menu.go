package renderer

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/help"
)


// menuSepH is the pixel height of a separator row.
const menuSepH = 9

// MenuState holds the full rendering and hit-test state for a context menu.
// Stored on Game; passed by pointer to DrawAll.
type MenuState struct {
	Open     bool
	Items    []help.MenuItem
	Rect     image.Rectangle // bounding rect of main menu (physical px)
	HoverIdx int             // -1 = none

	SubOpen      bool
	SubItems     []help.MenuItem
	SubRect      image.Rectangle
	SubHoverIdx  int
	SubParentIdx int // index in Items that owns this submenu
}

// menuItemH returns the pixel height of a regular menu item row.
func (r *Renderer) menuItemH() int {
	return r.font.CellH + 6
}

// menuWidth returns the fixed pixel width of a menu panel.
// Width accommodates: padding (1) + label (16) + arrow/shortcut (11) + padding (1) = 29 chars.
func (r *Renderer) menuWidth() int {
	return 29 * r.font.CellW
}

// menuTotalH returns the total height of a menu panel for the given items,
// including the 1px top and bottom border.
func (r *Renderer) menuTotalH(items []help.MenuItem) int {
	h := 2 // 1px top + 1px bottom border
	for _, item := range items {
		if item.Separator {
			h += menuSepH
		} else {
			h += r.menuItemH()
		}
	}
	return h
}

// BuildMenuRect computes and clamps the bounding rect for a menu of items
// positioned at (x, y) within a physical screen of (physW, physH).
func (r *Renderer) BuildMenuRect(items []help.MenuItem, x, y, physW, physH int) image.Rectangle {
	w := r.menuWidth()
	h := r.menuTotalH(items)
	x, y = clampMenuPos(x, y, w, h, physW, physH)
	return image.Rect(x, y, x+w, y+h)
}

// BuildSubRect computes and clamps the bounding rect for a submenu opened from
// parentIdx in state, within a physical screen of (physW, physH).
func (r *Renderer) BuildSubRect(state *MenuState, parentIdx, physW, physH int) image.Rectangle {
	subW := r.menuWidth()
	subH := r.menuTotalH(state.Items[parentIdx].Children)

	// Y: aligned to the parent item's top edge.
	parentY := state.Rect.Min.Y + 1 // skip top border
	for i := 0; i < parentIdx; i++ {
		if state.Items[i].Separator {
			parentY += menuSepH
		} else {
			parentY += r.menuItemH()
		}
	}

	// X: open to the right; flip left if near right edge.
	x := state.Rect.Max.X
	if x+subW > physW {
		x = state.Rect.Min.X - subW
	}
	y := parentY
	x, y = clampMenuPos(x, y, subW, subH, physW, physH)
	return image.Rect(x, y, x+subW, y+subH)
}

// clampMenuPos adjusts (x, y) so a panel of (w, h) stays within screen bounds.
func clampMenuPos(x, y, w, h, physW, physH int) (int, int) {
	if x+w > physW {
		x = physW - w
	}
	if x < 0 {
		x = 0
	}
	if y+h > physH {
		y = physH - h
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// MenuItemAt returns the index of the item in state.Items at physical pixel
// (px, py), or -1 if the position is outside or on a separator.
func (r *Renderer) MenuItemAt(state *MenuState, px, py int) int {
	return r.itemAtInRect(state.Items, state.Rect, px, py)
}

// SubItemAt returns the index of the item in state.SubItems at (px, py), or -1.
func (r *Renderer) SubItemAt(state *MenuState, px, py int) int {
	return r.itemAtInRect(state.SubItems, state.SubRect, px, py)
}

// itemAtInRect returns the index of the non-separator item at (px, py) within
// menuRect, or -1.
func (r *Renderer) itemAtInRect(items []help.MenuItem, menuRect image.Rectangle, px, py int) int {
	if !image.Pt(px, py).In(menuRect) {
		return -1
	}
	itemH := r.menuItemH()
	cy := menuRect.Min.Y + 1 // skip top border
	for i, item := range items {
		h := itemH
		if item.Separator {
			h = menuSepH
		}
		if py >= cy && py < cy+h {
			if item.Separator {
				return -1
			}
			return i
		}
		cy += h
	}
	return -1
}

// drawContextMenu draws the context menu (and submenu if open) onto r.offscreen.
func (r *Renderer) drawContextMenu(state *MenuState) {
	if !state.Open {
		return
	}
	r.drawMenuPanel(state.Items, state.Rect, state.HoverIdx, true)
	if state.SubOpen {
		r.drawMenuPanel(state.SubItems, state.SubRect, state.SubHoverIdx, false)
	}
}

// drawMenuPanel renders a single menu panel onto r.modalLayer.
// showArrows controls whether parent-item "▶" arrows are drawn.
func (r *Renderer) drawMenuPanel(items []help.MenuItem, rect image.Rectangle, hoverIdx int, showArrows bool) {
	img := r.modalLayer
	ui := r.ui
	itemH := r.menuItemH()
	padX := r.font.CellW

	// Background and border.
	img.SubImage(rect).(*ebiten.Image).Fill(ui.PanelBg)
	r.drawMenuBorder(rect)

	cy := rect.Min.Y + 1 // skip top border

	for i, item := range items {
		if item.Separator {
			lineY := cy + menuSepH/2
			img.SubImage(image.Rect(rect.Min.X+padX, lineY, rect.Max.X-padX, lineY+1)).(*ebiten.Image).Fill(ui.Border)
			cy += menuSepH
			continue
		}

		// Hover highlight.
		if i == hoverIdx {
			itemRect := image.Rect(rect.Min.X+1, cy, rect.Max.X-1, cy+itemH)
			img.SubImage(itemRect).(*ebiten.Image).Fill(ui.HoverBg)
		}

		textY := cy + 3 // 3px top padding

		// Label.
		r.font.DrawString(img, item.Label, rect.Min.X+padX, textY, ui.Fg)

		// Right side: submenu arrow or shortcut.
		if showArrows && len(item.Children) > 0 {
			arrowX := rect.Max.X - padX - r.font.CellW
			r.font.DrawString(img, "\u25b6", arrowX, textY, ui.Dim)
		} else if item.Shortcut != "" {
			scW := len([]rune(item.Shortcut)) * r.font.CellW
			r.font.DrawString(img, item.Shortcut, rect.Max.X-padX-scW, textY, ui.Dim)
		}

		cy += itemH
	}
}

// drawMenuBorder draws a 1px border around rect onto r.modalLayer.
func (r *Renderer) drawMenuBorder(rect image.Rectangle) {
	img := r.modalLayer
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
}
