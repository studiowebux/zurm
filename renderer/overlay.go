package renderer

import (
	"fmt"
	"image"

	"github.com/hajimehoshi/ebiten/v2"
)

// categoryGroup groups bindings under one category for rendering.
type categoryGroup struct {
	Category string
	Bindings []OverlayKeyBinding
}

// groupByCategory converts a flat binding list into ordered categoryGroups.
func groupByCategory(bindings []OverlayKeyBinding) []categoryGroup {
	var groups []categoryGroup
	idx := make(map[string]int)
	for _, b := range bindings {
		if i, ok := idx[b.Category]; ok {
			groups[i].Bindings = append(groups[i].Bindings, b)
		} else {
			idx[b.Category] = len(groups)
			groups = append(groups, categoryGroup{Category: b.Category, Bindings: []OverlayKeyBinding{b}})
		}
	}
	return groups
}

// columnHeight returns the total pixel height for the given category names.
func (r *Renderer) columnHeight(allBindings []OverlayKeyBinding, categories []string, rowH, headerH int) int {
	all := groupByCategory(allBindings)
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

	physW, physH := r.modalSize()

	r.drawBackdrop(physW, physH)

	cw := r.font.CellW
	ch := r.font.CellH
	rowH := ch + 2
	headerH := ch + 8

	// Two column groups.
	leftCats := []string{"Navigation", "Panes", "File Explorer"}
	rightCats := []string{"Pins", "Scroll", "Copy / Paste", "Search", "Blocks", "Recording", "Help", "App"}

	leftH := r.columnHeight(state.AllBindings, leftCats, rowH, headerH)
	rightH := r.columnHeight(state.AllBindings, rightCats, rowH, headerH)
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
		n := len(state.FilteredBindings)
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
	state.ScrollOffset, state.MaxScroll = clampScroll(state.ScrollOffset, state.MaxScroll)

	// Clamp panel width to screen.
	if panelW > physW {
		panelW = physW
	}

	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	// Panel background and border.
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	drawBorder(r.modalLayer, panelRect, r.ui.Border)

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
	drawBorder(r.modalLayer, searchBoxRect, r.ui.Border)
	searchText := "Search: " + inputWithCursor(state.SearchQuery, state.SearchCursorPos)
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
		r.drawFlatBindings(contentImg, state.FilteredBindings, panelX+panelPad, drawY, panelW-2*panelPad, keyColW, rowH)
	} else {
		leftX := panelX + panelPad
		rightX := leftX + colW + colGap
		r.drawColumnGroups(contentImg, state.AllBindings, leftCats, leftX, drawY, keyColW, descColW, rowH, headerH)
		r.drawColumnGroups(contentImg, state.AllBindings, rightCats, rightX, drawY, keyColW, descColW, rowH, headerH)
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
func (r *Renderer) drawColumnGroups(img *ebiten.Image, allBindings []OverlayKeyBinding, categories []string, x, y, keyW, descW, rowH, headerH int) {
	all := groupByCategory(allBindings)
	catMap := make(map[string][]OverlayKeyBinding)
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
func (r *Renderer) drawFlatBindings(img *ebiten.Image, bindings []OverlayKeyBinding, x, y, width, keyW, rowH int) {
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

	physW, physH := r.modalSize()

	r.drawBackdrop(physW, physH)

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
	drawBorder(r.modalLayer, panelRect, r.ui.Border)

	msgX := panelX + (panelW-msgLen*cw)/2
	r.font.DrawString(r.modalLayer, state.Message, msgX, panelY+panelPad, r.ui.Accent)

	hintX := panelX + (panelW-hintLen*cw)/2
	r.font.DrawString(r.modalLayer, hint, hintX, panelY+panelPad+ch*2, r.ui.Dim)
}

// drawMarkdownViewer renders a full-screen markdown reader overlay onto r.modalLayer.
func (r *Renderer) drawMarkdownViewer(state *MarkdownViewerState) {
	if state == nil || !state.Open {
		return
	}

	physW, physH := r.modalSize()

	r.drawBackdrop(physW, physH)

	cw := r.font.CellW
	ch := r.font.CellH
	rowH := ch + 2

	// Panel layout: 80% width, full height minus 1-cell margin.
	panelPad := cw
	panelW := physW * 80 / 100
	if panelW > physW-2*cw {
		panelW = physW - 2*cw
	}

	screenPad := ch
	topPad := ch / 2
	titleH := ch + 12
	dividerH := 8
	bottomPad := ch / 2
	overhead := topPad + titleH + dividerH + bottomPad

	totalContentH := len(state.Lines) * rowH

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

	// Update scroll metrics for input handler.
	state.RowH = rowH
	state.MaxScroll = totalContentH - visibleContentH
	state.ScrollOffset, state.MaxScroll = clampScroll(state.ScrollOffset, state.MaxScroll)

	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	// Panel background and border.
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	drawBorder(r.modalLayer, panelRect, r.ui.Border)

	// Title row.
	titleY := panelY + topPad + 3
	title := state.Title
	if title == "" {
		title = "Markdown Viewer"
	}
	titleX := panelX + (panelW-len([]rune(title))*cw)/2
	r.font.DrawString(r.modalLayer, title, titleX, titleY, r.ui.Accent)

	// Breadcrumb for history navigation.
	if state.IsLLMS && state.HistoryLen > 0 {
		breadcrumb := fmt.Sprintf("< %d", state.HistoryLen)
		r.font.DrawString(r.modalLayer, breadcrumb, panelX+panelPad, titleY, r.ui.Dim)
	}

	hint := "/ search  Esc close"
	if state.FollowMode {
		hint = "a-z follow  Esc cancel"
	} else {
		if state.IsLLMS {
			hint = "f follow  " + hint
			if state.ForwardLen > 0 {
				hint = "L fwd  " + hint
			}
			if state.HistoryLen > 0 {
				hint = "H back  " + hint
			}
		}
		hint = "Cmd+Ret pane  " + hint
		if state.HasAlt {
			hint = "Tab switch  " + hint
		}
		if state.MaxScroll > 0 {
			hint = "j/k scroll  " + hint
		}
		if len(state.SearchMatches) > 0 {
			hint = "n/N match  " + hint
		}
	}
	hintX := panelRect.Max.X - panelPad - len([]rune(hint))*cw
	r.font.DrawString(r.modalLayer, hint, hintX, titleY, r.ui.Dim)

	// Divider.
	divY := panelY + topPad + titleH + dividerH/2
	r.modalLayer.SubImage(image.Rect(panelX+panelPad, divY, panelX+panelW-panelPad, divY+1)).(*ebiten.Image).Fill(r.ui.Border)

	// Content clip region.
	contentTop := divY + dividerH/2 + 2
	contentClipRect := image.Rect(panelX, contentTop, panelX+panelW, contentTop+visibleContentH)
	contentImg := r.modalLayer.SubImage(contentClipRect).(*ebiten.Image)

	// Draw styled lines.
	drawY := contentTop - state.ScrollOffset
	contentLeft := panelX + panelPad
	contentRight := panelX + panelW - panelPad

	// Heading and emphasis colors.
	boldColor := r.ui.MdBold
	h1Color := r.ui.MdHeading
	h2Color := r.ui.Accent // theme accent
	h3Color := r.ui.Dim    // subdued
	codeFg := r.ui.MdCode
	codeBorder := r.ui.MdCodeBorder
	tableBorder := r.ui.MdTableBorder

	// Index search matches by line for the per-line drawing loop.
	matchBg := r.ui.MdMatchBg
	currentBg := r.ui.MdMatchCurBg
	matchesByLine := map[int][]int{} // lineIdx -> match indices
	for i, m := range state.SearchMatches {
		matchesByLine[m.LineIdx] = append(matchesByLine[m.LineIdx], i)
	}

	lineIdx := 0
	for _, line := range state.Lines {
		// HRule or table separator: draw a horizontal line.
		if len(line.Spans) == 1 && (line.Spans[0].Style == MdStyleHRule || line.Spans[0].Style == MdStyleTableSeparator) {
			lineY := drawY + rowH/2
			contentImg.SubImage(image.Rect(contentLeft, lineY, contentRight, lineY+1)).(*ebiten.Image).Fill(r.ui.Border)
			drawY += rowH
			lineIdx++
			continue
		}

		x := contentLeft + line.Indent*cw

		// Code block lines get full-width background + left border stripe.
		isCodeLine := len(line.Spans) > 0 && line.Spans[0].Style == MdStyleCodeBlock
		if isCodeLine {
			bgRect := image.Rect(contentLeft, drawY, contentRight, drawY+rowH)
			contentImg.SubImage(bgRect).(*ebiten.Image).Fill(r.ui.HoverBg)
			contentImg.SubImage(image.Rect(contentLeft, drawY, contentLeft+2, drawY+rowH)).(*ebiten.Image).Fill(codeBorder)
		}

		// Table row lines get a subtle bottom border.
		isTableLine := len(line.Spans) > 0 && (line.Spans[0].Style == MdStyleTableHeader || line.Spans[0].Style == MdStyleTableCell)
		if isTableLine {
			contentImg.SubImage(image.Rect(contentLeft, drawY+rowH-1, contentRight, drawY+rowH)).(*ebiten.Image).Fill(tableBorder)
		}

		// Blockquote accent stripe.
		if len(line.Spans) > 0 && line.Spans[0].Style == MdStyleBlockquote {
			stripeX := contentLeft
			contentImg.SubImage(image.Rect(stripeX, drawY, stripeX+2, drawY+rowH)).(*ebiten.Image).Fill(r.ui.Accent)
		}

		// Search highlights — after backgrounds, before text.
		if matches, ok := matchesByLine[lineIdx]; ok {
			hlBase := x
			if isCodeLine {
				hlBase += cw
			}
			for _, mi := range matches {
				m := state.SearchMatches[mi]
				bg := matchBg
				if mi == state.SearchIdx {
					bg = currentBg
				}
				hlX := hlBase + m.Col*cw
				hlW := m.Len * cw
				hlRect := image.Rect(hlX, drawY, hlX+hlW, drawY+rowH)
				contentImg.SubImage(hlRect).(*ebiten.Image).Fill(bg)
			}
		}

		for _, span := range line.Spans {
			textW := len([]rune(span.Text)) * cw

			switch span.Style {
			case MdStyleHeading1:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, h1Color)
			case MdStyleHeading2:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, h2Color)
			case MdStyleHeading3:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, h3Color)
			case MdStyleBold:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, boldColor)
			case MdStyleItalic:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			case MdStyleInlineCode:
				bgRect := image.Rect(x-1, drawY, x+textW+1, drawY+rowH)
				contentImg.SubImage(bgRect).(*ebiten.Image).Fill(r.ui.HoverBg)
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.KeyName)
			case MdStyleCodeBlock:
				r.font.DrawString(contentImg, span.Text, x+cw, drawY+1, codeFg)
			case MdStyleLink:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case MdStyleBlockquote:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			case MdStyleListItem:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case MdStyleTableHeader:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, boldColor)
			case MdStyleTableCell:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Fg)
			case MdStyleStrikethrough:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			case MdStyleImage:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case MdStyleCheckboxChecked:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case MdStyleCheckboxUnchecked:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			default:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Fg)
			}

			x += textW
		}

		// Heading underlines after the line is drawn.
		if len(line.Spans) > 0 {
			switch line.Spans[0].Style {
			case MdStyleHeading1:
				ulY := drawY + rowH - 2
				contentImg.SubImage(image.Rect(contentLeft, ulY, contentRight, ulY+1)).(*ebiten.Image).Fill(h1Color)
			case MdStyleHeading2:
				ulY := drawY + rowH - 2
				contentImg.SubImage(image.Rect(contentLeft, ulY, x, ulY+1)).(*ebiten.Image).Fill(h2Color)
			}
		}

		drawY += rowH
		lineIdx++
	}

	// Follow-mode link badges: draw letter labels over link spans.
	if state.FollowMode && len(state.LinkHints) > 0 {
		badgeBg := r.ui.MdBadgeBg
		badgeFg := r.ui.MdBadgeFg
		for _, hint := range state.LinkHints {
			if hint.LineIdx >= len(state.Lines) {
				continue
			}
			lineY := hint.LineIdx*rowH - state.ScrollOffset + contentTop
			if lineY+rowH < contentTop || lineY > contentTop+visibleContentH {
				continue // off-screen
			}
			// Calculate X position of the link span.
			line := state.Lines[hint.LineIdx]
			sx := contentLeft + line.Indent*cw
			for si := 0; si < hint.SpanIdx && si < len(line.Spans); si++ {
				sx += len([]rune(line.Spans[si].Text)) * cw
			}
			// Draw badge: [letter] before the link text.
			badgeStr := string(hint.Label)
			bx := sx - 2*cw
			if bx < contentLeft {
				bx = sx // fallback: draw at span start
			}
			badgeRect := image.Rect(bx, lineY, bx+cw+2, lineY+rowH)
			r.modalLayer.SubImage(badgeRect).(*ebiten.Image).Fill(badgeBg)
			r.font.DrawString(r.modalLayer, badgeStr, bx+1, lineY+1, badgeFg)
		}
	}

	// Scrollbar.
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

	// Search bar at bottom of panel.
	if state.SearchOpen {
		barH := ch + 8
		barY := panelRect.Max.Y - barH
		barRect := image.Rect(panelX, barY, panelX+panelW, panelRect.Max.Y)
		r.modalLayer.SubImage(barRect).(*ebiten.Image).Fill(r.ui.PanelBg)
		// Top border.
		r.modalLayer.SubImage(image.Rect(panelX, barY, panelX+panelW, barY+1)).(*ebiten.Image).Fill(r.ui.Border)

		// Search label and query.
		label := "/"
		r.font.DrawString(r.modalLayer, label, panelX+panelPad, barY+4, r.ui.Dim)
		queryX := panelX + panelPad + cw*2
		query := inputWithCursor(state.SearchQuery, state.SearchCursorPos)
		r.font.DrawString(r.modalLayer, query, queryX, barY+4, r.ui.Fg)

		// Match count.
		if len(state.SearchMatches) > 0 {
			countStr := fmt.Sprintf("%d/%d", state.SearchIdx+1, len(state.SearchMatches))
			countX := panelRect.Max.X - panelPad - len([]rune(countStr))*cw
			r.font.DrawString(r.modalLayer, countStr, countX, barY+4, r.ui.Dim)
		} else if state.SearchQuery != "" {
			nmX := panelRect.Max.X - panelPad - len([]rune(noMatchesLabel))*cw
			r.font.DrawString(r.modalLayer, noMatchesLabel, nmX, barY+4, r.ui.Dim)
		}
	}
}

// drawURLInput renders a centered URL input dialog for the llms.txt browser.
func (r *Renderer) drawURLInput(state *URLInputState) {
	if state == nil || !state.Open {
		return
	}

	physW, physH := r.modalSize()

	r.drawBackdrop(physW, physH)

	cw := r.font.CellW
	ch := r.font.CellH

	title := "Open llms.txt"
	hint := "[Enter] fetch    [Esc] cancel"
	inputText := inputWithCursor(state.Query, state.CursorPos)
	if state.Loading {
		inputText = state.Query
		hint = "Fetching..."
	}

	// Panel width = widest line.
	innerW := len([]rune(title))
	if l := len([]rune(hint)); l > innerW {
		innerW = l
	}
	if l := len([]rune(inputText)) + 2; l > innerW {
		innerW = l
	}
	if innerW < 40 {
		innerW = 40
	}

	panelPad := cw
	panelW := innerW*cw + panelPad*2
	panelH := ch*6 + panelPad*2
	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	drawBorder(r.modalLayer, panelRect, r.ui.Border)

	// Title (centered, accent color).
	titleX := panelX + (panelW-len([]rune(title))*cw)/2
	r.font.DrawString(r.modalLayer, title, titleX, panelY+panelPad, r.ui.Accent)

	// Input field with HoverBg background.
	inputY := panelY + panelPad + ch*2
	inputRect := image.Rect(panelX+panelPad, inputY, panelX+panelW-panelPad, inputY+ch+6)
	r.modalLayer.SubImage(inputRect).(*ebiten.Image).Fill(r.ui.HoverBg)
	drawBorder(r.modalLayer, inputRect, r.ui.Border)
	r.font.DrawString(r.modalLayer, inputText, panelX+panelPad+cw/2, inputY+3, r.ui.Fg)

	// Hint line (dim).
	hintColor := r.ui.Dim
	hintY := panelY + panelPad + ch*4
	hintX := panelX + (panelW-len([]rune(hint))*cw)/2
	r.font.DrawString(r.modalLayer, hint, hintX, hintY, hintColor)
}
