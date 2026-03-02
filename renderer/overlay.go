package renderer

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/help"
)


// OverlayState holds the rendering state for the keybinding overlay.
type OverlayState struct {
	Open         bool
	SearchQuery  string
	ScrollOffset int
	MaxScroll    int // written by renderer each frame
	RowH         int // written by renderer each frame
}

// ConfirmState holds the rendering state for the close-confirmation dialog.
type ConfirmState struct {
	Open    bool
	Message string // e.g. "Close pane?" or "Close tab?"
}

// categoryGroup groups bindings under one category for rendering.
type categoryGroup struct {
	Category string
	Bindings []help.KeyBinding
}

// groupByCategory converts a flat binding list into ordered categoryGroups.
func groupByCategory(bindings []help.KeyBinding) []categoryGroup {
	var groups []categoryGroup
	idx := make(map[string]int)
	for _, b := range bindings {
		if i, ok := idx[b.Category]; ok {
			groups[i].Bindings = append(groups[i].Bindings, b)
		} else {
			idx[b.Category] = len(groups)
			groups = append(groups, categoryGroup{Category: b.Category, Bindings: []help.KeyBinding{b}})
		}
	}
	return groups
}

// columnHeight returns the total pixel height for the given category names.
func (r *Renderer) columnHeight(categories []string, rowH, headerH int) int {
	all := groupByCategory(help.AllBindings())
	catMap := make(map[string]int)
	for _, g := range all {
		catMap[g.Category] = len(g.Bindings)
	}
	h := 0
	for _, cat := range categories {
		h += headerH + catMap[cat]*rowH
	}
	return h
}

// drawOverlay renders the keybinding overlay onto r.modalLayer.
func (r *Renderer) drawOverlay(state *OverlayState) {
	if !state.Open {
		return
	}

	physW := r.modalLayer.Bounds().Dx()
	physH := r.modalLayer.Bounds().Dy()

	// Semi-transparent backdrop: scale a 1×1 image to full screen.
	if r.overlayBg == nil {
		r.overlayBg = ebiten.NewImage(1, 1)
	}
	r.overlayBg.Fill(r.ui.Backdrop)
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(physW), float64(physH))
	r.modalLayer.DrawImage(r.overlayBg, op)

	cw := r.font.CellW
	ch := r.font.CellH
	rowH := ch + 2
	headerH := ch + 8

	// Two column groups.
	leftCats := []string{"Navigation", "Panes", "File Explorer"}
	rightCats := []string{"Pins", "Scroll", "Copy / Paste", "Search", "Blocks", "Help", "App"}

	leftH := r.columnHeight(leftCats, rowH, headerH)
	rightH := r.columnHeight(rightCats, rowH, headerH)
	totalContentH := leftH
	if rightH > totalContentH {
		totalContentH = rightH
	}

	// Panel layout constants.
	panelPad := cw
	colGap := 2 * cw
	keyColW := 16 * cw
	descColW := 24 * cw
	colW := keyColW + descColW
	panelW := 2*colW + colGap + 2*panelPad

	topPad := ch / 2
	titleH := ch + 12
	searchH := ch + 10
	dividerH := 8
	bottomPad := ch / 2
	overhead := topPad + titleH + searchH + dividerH + bottomPad + 4

	// When searching, total content is the flat filtered list height.
	if state.SearchQuery != "" {
		filtered := help.FilterBindings(state.SearchQuery)
		n := len(filtered)
		if n == 0 {
			n = 1 // "No matches" line
		}
		totalContentH = n * rowH
	}

	// How much content can be visible — cap panel at screen height minus a
	// vertical margin so the panel never touches the screen edges.
	screenPad := ch
	maxPanelH := physH - 2*screenPad
	if maxPanelH < overhead {
		maxPanelH = overhead
	}
	visibleContentH := maxPanelH - overhead
	if visibleContentH > totalContentH {
		visibleContentH = totalContentH
	}
	panelH := overhead + visibleContentH
	if panelH > physH {
		panelH = physH
		visibleContentH = panelH - overhead
	}

	// Update scroll metrics so input handler can clamp and step.
	state.RowH = rowH
	state.MaxScroll = totalContentH - visibleContentH
	if state.MaxScroll < 0 {
		state.MaxScroll = 0
	}
	if state.ScrollOffset < 0 {
		state.ScrollOffset = 0
	}
	if state.ScrollOffset > state.MaxScroll {
		state.ScrollOffset = state.MaxScroll
	}

	// Clamp panel width to screen.
	if panelW > physW {
		panelW = physW
	}

	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	// Panel background and border.
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(panelRect)

	// Title row.
	titleY := panelY + topPad + 3
	title := "Keybindings"
	titleX := panelX + (panelW-len([]rune(title))*cw)/2
	r.font.DrawString(r.modalLayer, title, titleX, titleY, r.ui.Accent)

	hint := "Esc to close"
	if state.MaxScroll > 0 {
		hint = "↑↓ scroll  " + hint
	}
	hintX := panelRect.Max.X - panelPad - len([]rune(hint))*cw
	r.font.DrawString(r.modalLayer, hint, hintX, titleY, r.ui.Dim)

	// Search box.
	searchY := panelY + topPad + titleH
	searchBoxRect := image.Rect(panelX+panelPad, searchY, panelX+panelW-panelPad, searchY+searchH-2)
	r.modalLayer.SubImage(searchBoxRect).(*ebiten.Image).Fill(r.ui.HoverBg)
	r.drawOverlayBorder(searchBoxRect)
	searchText := "Search: " + state.SearchQuery + "_"
	r.font.DrawString(r.modalLayer, searchText, panelX+panelPad+cw/2, searchY+3, r.ui.Fg)

	// Divider.
	divY := searchY + searchH + dividerH/2
	r.modalLayer.SubImage(image.Rect(panelX+panelPad, divY, panelX+panelW-panelPad, divY+1)).(*ebiten.Image).Fill(r.ui.Border)

	// Content clip region — rows outside this rect are invisible.
	contentTop := divY + dividerH/2 + 2
	contentClipRect := image.Rect(panelX, contentTop, panelX+panelW, contentTop+visibleContentH)
	contentImg := r.modalLayer.SubImage(contentClipRect).(*ebiten.Image)
	drawY := contentTop - state.ScrollOffset

	if state.SearchQuery != "" {
		filtered := help.FilterBindings(state.SearchQuery)
		r.drawFlatBindings(contentImg, filtered, panelX+panelPad, drawY, panelW-2*panelPad, keyColW, rowH)
	} else {
		leftX := panelX + panelPad
		rightX := leftX + colW + colGap
		r.drawColumnGroups(contentImg, leftCats, leftX, drawY, keyColW, descColW, rowH, headerH)
		r.drawColumnGroups(contentImg, rightCats, rightX, drawY, keyColW, descColW, rowH, headerH)
	}

	// Scrollbar — only when content overflows.
	if state.MaxScroll > 0 {
		sbX := panelRect.Max.X - 4
		sbTrackY := contentTop
		sbTrackH := visibleContentH
		thumbH := sbTrackH * visibleContentH / totalContentH
		if thumbH < 8 {
			thumbH = 8
		}
		thumbY := sbTrackY + (sbTrackH-thumbH)*state.ScrollOffset/state.MaxScroll
		r.modalLayer.SubImage(image.Rect(sbX, thumbY, sbX+3, thumbY+thumbH)).(*ebiten.Image).Fill(r.ui.Dim)
	}
}

// drawColumnGroups renders category headers and binding rows for the given
// categories into img (a clipped sub-image) starting at absolute coords (x, y).
func (r *Renderer) drawColumnGroups(img *ebiten.Image, categories []string, x, y, keyW, descW, rowH, headerH int) {
	all := groupByCategory(help.AllBindings())
	catMap := make(map[string][]help.KeyBinding)
	for _, g := range all {
		catMap[g.Category] = g.Bindings
	}

	cy := y
	for _, cat := range categories {
		bindings := catMap[cat]
		if len(bindings) == 0 {
			continue
		}

		// Category header.
		r.font.DrawString(img, cat, x, cy+4, r.ui.CatHdr)
		// Thin underline below header text.
		underY := cy + headerH - 3
		img.SubImage(image.Rect(x, underY, x+keyW+descW, underY+1)).(*ebiten.Image).Fill(r.ui.Dim)
		cy += headerH

		// Binding rows.
		for _, b := range bindings {
			r.font.DrawString(img, b.Key, x, cy+1, r.ui.KeyName)
			// Truncate description to descW.
			desc := b.Description
			maxChars := descW / r.font.CellW
			if len([]rune(desc)) > maxChars && maxChars > 1 {
				desc = string([]rune(desc)[:maxChars-1]) + "\u2026"
			}
			r.font.DrawString(img, desc, x+keyW, cy+1, r.ui.Fg)
			cy += rowH
		}
	}
}

// drawFlatBindings renders a filtered flat list of bindings into img (a clipped
// sub-image) starting at absolute coords (x, y).
func (r *Renderer) drawFlatBindings(img *ebiten.Image, bindings []help.KeyBinding, x, y, width, keyW, rowH int) {
	if len(bindings) == 0 {
		r.font.DrawString(img, "No matches", x, y+1, r.ui.Dim)
		return
	}

	cy := y
	for _, b := range bindings {
		r.font.DrawString(img, b.Key, x, cy+1, r.ui.KeyName)
		r.font.DrawString(img, b.Description, x+keyW, cy+1, r.ui.Fg)

		// Category dim label on the right.
		catW := len([]rune(b.Category)) * r.font.CellW
		catX := x + width - catW
		descEnd := x + keyW + len([]rune(b.Description))*r.font.CellW
		if catX > descEnd+r.font.CellW {
			r.font.DrawString(img, b.Category, catX, cy+1, r.ui.Dim)
		}
		cy += rowH
	}
}

// drawConfirm renders a small centered confirmation dialog.
func (r *Renderer) drawConfirm(state *ConfirmState) {
	if !state.Open {
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

	hint := "[Enter] confirm    [Esc] cancel"
	msgLen := len([]rune(state.Message))
	hintLen := len([]rune(hint))
	innerW := hintLen
	if msgLen > innerW {
		innerW = msgLen
	}

	panelPad := cw
	panelW := innerW*cw + panelPad*2
	panelH := ch*4 + panelPad*2
	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(panelRect)

	msgX := panelX + (panelW-msgLen*cw)/2
	r.font.DrawString(r.modalLayer, state.Message, msgX, panelY+panelPad, r.ui.Accent)

	hintX := panelX + (panelW-hintLen*cw)/2
	r.font.DrawString(r.modalLayer, hint, hintX, panelY+panelPad+ch*2, r.ui.Dim)
}

// drawOverlayBorder draws a 1px border around rect.
func (r *Renderer) drawOverlayBorder(rect image.Rectangle) {
	img := r.modalLayer
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
}
