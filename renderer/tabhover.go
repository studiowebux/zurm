package renderer

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/tab"
)

// RenderTabThumbnail renders a full-resolution snapshot of the given tab's layout
// into a temporary image. The caller is responsible for scaling it down when drawing.
// All pane buffers must NOT be locked by the caller — this method acquires RLock internally.
func (r *Renderer) RenderTabThumbnail(t *tab.Tab, contentRect image.Rectangle) *ebiten.Image {
	if t == nil || t.Layout == nil {
		return nil
	}

	w := contentRect.Dx()
	h := contentRect.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}

	tmp := ebiten.NewImage(w, h)
	tmp.Fill(parseHexColor(r.cfg.Colors.Background))

	offsetX := -contentRect.Min.X
	offsetY := -contentRect.Min.Y

	leaves := t.Layout.Leaves()
	for _, leaf := range leaves {
		p := leaf.Pane
		localRect := p.Rect.Add(image.Pt(offsetX, offsetY))

		p.Term.Buf.RLock()
		r.drawPaneTo(tmp, p.Term.Buf, p.Term.Cursor, localRect, false, false, nil, p.HeaderH)
		p.Term.Buf.RUnlock()
	}

	// Draw dividers with the same offset so they align with local pane rects.
	r.drawDividersTo(tmp, t.Layout, offsetX, offsetY)

	return tmp
}

// TabHoverCacheKey computes a cache key from the aggregate RenderGen of all panes in a tab.
func TabHoverCacheKey(t *tab.Tab) uint64 {
	if t == nil || t.Layout == nil {
		return 0
	}
	var sum uint64
	for _, leaf := range t.Layout.Leaves() {
		sum += leaf.Pane.Term.Buf.RenderGen()
	}
	return sum
}

// drawTabHoverPopover renders the cached thumbnail as a scaled-down popover onto r.modalLayer.
// modalLayer is cleared every frame, so the popover never accumulates stale pixels.
func (r *Renderer) drawTabHoverPopover(state *TabHoverState) {
	if state == nil || !state.Active || state.Thumbnail == nil {
		return
	}

	dst := r.modalLayer

	popX := state.PopoverX
	popY := state.PopoverY
	popW := state.PopoverW
	popH := state.PopoverH

	if popW <= 0 || popH <= 0 {
		return
	}

	physW := dst.Bounds().Dx()

	// Clamp horizontally so the popover stays on screen.
	borderPad := 2
	if popX+popW+borderPad > physW {
		popX = physW - popW - borderPad
	}
	if popX < borderPad {
		popX = borderPad
	}

	// Outer border rect (2px border around the thumbnail).
	outerRect := image.Rect(popX-borderPad, popY-borderPad, popX+popW+borderPad, popY+popH+borderPad)
	dst.SubImage(outerRect).(*ebiten.Image).Fill(r.ui.Border)

	// Inner background.
	innerRect := image.Rect(popX, popY, popX+popW, popY+popH)
	dst.SubImage(innerRect).(*ebiten.Image).Fill(r.ui.PanelBg)

	// Scale and draw the thumbnail.
	thumbW := float64(state.Thumbnail.Bounds().Dx())
	thumbH := float64(state.Thumbnail.Bounds().Dy())
	if thumbW <= 0 || thumbH <= 0 {
		return
	}

	sx := float64(popW) / thumbW
	sy := float64(popH) / thumbH

	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(sx, sy)
	op.GeoM.Translate(float64(popX), float64(popY))
	// Linear filter for the scaled-down minimap.
	op.Filter = ebiten.FilterLinear
	dst.DrawImage(state.Thumbnail, op)
}

// ComputeContentRect returns the content area rect (below tab bar, above status bar)
// for the given tab. This is the area the thumbnail should capture.
func (r *Renderer) ComputeContentRect(t *tab.Tab) image.Rectangle {
	if t == nil || t.Layout == nil {
		return image.Rectangle{}
	}
	return t.Layout.Rect
}

// DismissTabHover disposes the thumbnail and resets the hover state.
func DismissTabHover(state *TabHoverState) {
	if state == nil {
		return
	}
	if state.Thumbnail != nil {
		state.Thumbnail.Deallocate()
		state.Thumbnail = nil
	}
	*state = TabHoverState{TabIdx: -1}
}
