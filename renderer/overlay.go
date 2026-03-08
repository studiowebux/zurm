package renderer

import (
	"fmt"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/help"
	"github.com/studiowebux/zurm/markdown"
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

// DictationState holds the rendering state for the speech-to-text overlay.
type DictationState struct {
	Open       bool
	Status     string // "Listening…", "Requesting permission…", error messages
	Transcript string // latest interim or final transcript text
}

// URLInputState holds the rendering state for the llms.txt URL input overlay.
type URLInputState struct {
	Open    bool
	Query   string
	Loading bool
}

// LinkHint is a follow-mode badge mapping a letter to a link span.
type LinkHint struct {
	LineIdx int
	SpanIdx int
	URL     string
	Label   rune
}

// MarkdownViewerState holds the rendering state for the markdown reader overlay.
type MarkdownViewerState struct {
	Open         bool
	Title        string
	Lines        []markdown.StyledLine
	ScrollOffset int
	MaxScroll    int // written by renderer each frame
	RowH         int // written by renderer each frame

	// Search state (Cmd+F / /).
	SearchOpen    bool
	SearchQuery   string
	SearchMatches []int // line indices that contain a match
	SearchIdx     int   // current match index (-1 = none)

	// HasAlt is true when an alternate view is available (e.g. llms.txt ↔ llms-full.txt).
	// When set, the hint bar shows "Tab switch".
	HasAlt bool

	// Follow-link mode (f key).
	FollowMode bool
	LinkHints  []LinkHint

	// IsLLMS marks this viewer as browsing llms.txt content (enables history nav).
	IsLLMS bool

	// HistoryLen / ForwardLen control hint bar display of back/forward availability.
	HistoryLen int
	ForwardLen int
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
	rightCats := []string{"Pins", "Scroll", "Copy / Paste", "Search", "Blocks", "Recording", "Help", "App"}

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

// drawDictation renders a centered speech-to-text overlay with status and transcript.
func (r *Renderer) drawDictation(state *DictationState) {
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

	// Layout: title, blank, status, blank, transcript (up to 60 chars), blank, hint.
	title := "Speech to Text"
	hint := "[Esc] stop and close"
	status := state.Status
	transcript := state.Transcript
	if transcript == "" {
		transcript = "(waiting for speech…)"
	}

	// Clamp transcript display width.
	maxW := 60
	if tr := []rune(transcript); len(tr) > maxW {
		transcript = string(tr[:maxW]) + "…"
	}

	// Panel width = widest line.
	innerW := len([]rune(title))
	if l := len([]rune(hint)); l > innerW {
		innerW = l
	}
	if l := len([]rune(status)); l > innerW {
		innerW = l
	}
	if l := len([]rune(transcript)); l > innerW {
		innerW = l
	}

	panelPad := cw
	panelW := innerW*cw + panelPad*2
	panelH := ch*7 + panelPad*2
	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(panelRect)

	// Title (centered, accent color).
	titleX := panelX + (panelW-len([]rune(title))*cw)/2
	r.font.DrawString(r.modalLayer, title, titleX, panelY+panelPad, r.ui.Accent)

	// Status line.
	r.font.DrawString(r.modalLayer, status, panelX+panelPad, panelY+panelPad+ch*2, r.ui.Fg)

	// Transcript line.
	r.font.DrawString(r.modalLayer, transcript, panelX+panelPad, panelY+panelPad+ch*4, r.ui.Accent)

	// Hint line (dim).
	hintX := panelX + (panelW-len([]rune(hint))*cw)/2
	r.font.DrawString(r.modalLayer, hint, hintX, panelY+panelPad+ch*6, r.ui.Dim)
}

// drawMarkdownViewer renders a full-screen markdown reader overlay onto r.modalLayer.
func (r *Renderer) drawMarkdownViewer(state *MarkdownViewerState) {
	if state == nil || !state.Open {
		return
	}

	physW := r.modalLayer.Bounds().Dx()
	physH := r.modalLayer.Bounds().Dy()

	// Semi-transparent backdrop.
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
	if state.MaxScroll < 0 {
		state.MaxScroll = 0
	}
	if state.ScrollOffset < 0 {
		state.ScrollOffset = 0
	}
	if state.ScrollOffset > state.MaxScroll {
		state.ScrollOffset = state.MaxScroll
	}

	panelX := (physW - panelW) / 2
	panelY := (physH - panelH) / 2
	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	// Panel background and border.
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(panelRect)

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

	// Bright white for bold text.
	boldColor := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}

	// Build match set for highlight rendering.
	matchSet := make(map[int]bool, len(state.SearchMatches))
	for _, idx := range state.SearchMatches {
		matchSet[idx] = true
	}
	currentMatchLine := -1
	if state.SearchIdx >= 0 && state.SearchIdx < len(state.SearchMatches) {
		currentMatchLine = state.SearchMatches[state.SearchIdx]
	}
	matchBg := color.RGBA{0x80, 0x80, 0x00, 0x60}   // dim yellow for matches
	currentBg := color.RGBA{0xFF, 0xCC, 0x00, 0x80}  // bright yellow for current match

	for lineIdx, line := range state.Lines {
		// HRule or table separator: draw a horizontal line.
		if len(line.Spans) == 1 && (line.Spans[0].Style == markdown.StyleHRule || line.Spans[0].Style == markdown.StyleTableSeparator) {
			lineY := drawY + rowH/2
			contentImg.SubImage(image.Rect(contentLeft, lineY, contentRight, lineY+1)).(*ebiten.Image).Fill(r.ui.Border)
			drawY += rowH
			continue
		}

		// Search match highlight.
		if matchSet[lineIdx] {
			bg := matchBg
			if lineIdx == currentMatchLine {
				bg = currentBg
			}
			hlRect := image.Rect(contentLeft, drawY, contentRight, drawY+rowH)
			contentImg.SubImage(hlRect).(*ebiten.Image).Fill(bg)
		}

		x := contentLeft + line.Indent*cw

		// Code block lines get full-width background.
		isCodeLine := len(line.Spans) > 0 && line.Spans[0].Style == markdown.StyleCodeBlock
		if isCodeLine {
			bgRect := image.Rect(contentLeft, drawY, contentRight, drawY+rowH)
			contentImg.SubImage(bgRect).(*ebiten.Image).Fill(r.ui.HoverBg)
		}

		// Blockquote accent stripe.
		if len(line.Spans) > 0 && line.Spans[0].Style == markdown.StyleBlockquote {
			stripeX := contentLeft
			contentImg.SubImage(image.Rect(stripeX, drawY, stripeX+2, drawY+rowH)).(*ebiten.Image).Fill(r.ui.Accent)
		}

		for _, span := range line.Spans {
			textW := len([]rune(span.Text)) * cw

			switch span.Style {
			case markdown.StyleHeading1, markdown.StyleHeading2, markdown.StyleHeading3:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case markdown.StyleBold:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, boldColor)
			case markdown.StyleItalic:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			case markdown.StyleInlineCode:
				bgRect := image.Rect(x-1, drawY, x+textW+1, drawY+rowH)
				contentImg.SubImage(bgRect).(*ebiten.Image).Fill(r.ui.HoverBg)
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.KeyName)
			case markdown.StyleCodeBlock:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Fg)
			case markdown.StyleLink:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case markdown.StyleBlockquote:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			case markdown.StyleListItem:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case markdown.StyleTableHeader:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, boldColor)
			case markdown.StyleTableCell:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Fg)
			case markdown.StyleStrikethrough:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			case markdown.StyleImage:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case markdown.StyleCheckboxChecked:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Accent)
			case markdown.StyleCheckboxUnchecked:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Dim)
			default:
				r.font.DrawString(contentImg, span.Text, x, drawY+1, r.ui.Fg)
			}

			x += textW
		}

		drawY += rowH
	}

	// Follow-mode link badges: draw letter labels over link spans.
	if state.FollowMode && len(state.LinkHints) > 0 {
		badgeBg := color.RGBA{0xFF, 0xCC, 0x00, 0xFF}  // bright yellow badge
		badgeFg := color.RGBA{0x00, 0x00, 0x00, 0xFF}  // black text on badge
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
		query := state.SearchQuery + "_" // cursor
		r.font.DrawString(r.modalLayer, query, queryX, barY+4, r.ui.Fg)

		// Match count.
		if len(state.SearchMatches) > 0 {
			countStr := fmt.Sprintf("%d/%d", state.SearchIdx+1, len(state.SearchMatches))
			countX := panelRect.Max.X - panelPad - len([]rune(countStr))*cw
			r.font.DrawString(r.modalLayer, countStr, countX, barY+4, r.ui.Dim)
		} else if state.SearchQuery != "" {
			noMatch := "no matches"
			nmX := panelRect.Max.X - panelPad - len([]rune(noMatch))*cw
			r.font.DrawString(r.modalLayer, noMatch, nmX, barY+4, r.ui.Dim)
		}
	}
}

// drawURLInput renders a centered URL input dialog for the llms.txt browser.
func (r *Renderer) drawURLInput(state *URLInputState) {
	if state == nil || !state.Open {
		return
	}

	physW := r.modalLayer.Bounds().Dx()
	physH := r.modalLayer.Bounds().Dy()

	// Semi-transparent backdrop.
	if r.overlayBg == nil {
		r.overlayBg = ebiten.NewImage(1, 1)
	}
	r.overlayBg.Fill(r.ui.Backdrop)
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(physW), float64(physH))
	r.modalLayer.DrawImage(r.overlayBg, op)

	cw := r.font.CellW
	ch := r.font.CellH

	title := "Open llms.txt"
	hint := "[Enter] fetch    [Esc] cancel"
	inputText := state.Query + "_"
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
	r.drawOverlayBorder(panelRect)

	// Title (centered, accent color).
	titleX := panelX + (panelW-len([]rune(title))*cw)/2
	r.font.DrawString(r.modalLayer, title, titleX, panelY+panelPad, r.ui.Accent)

	// Input field with HoverBg background.
	inputY := panelY + panelPad + ch*2
	inputRect := image.Rect(panelX+panelPad, inputY, panelX+panelW-panelPad, inputY+ch+6)
	r.modalLayer.SubImage(inputRect).(*ebiten.Image).Fill(r.ui.HoverBg)
	r.drawOverlayBorder(inputRect)
	r.font.DrawString(r.modalLayer, inputText, panelX+panelPad+cw/2, inputY+3, r.ui.Fg)

	// Hint line (dim).
	hintColor := r.ui.Dim
	hintY := panelY + panelPad + ch*4
	hintX := panelX + (panelW-len([]rune(hint))*cw)/2
	r.font.DrawString(r.modalLayer, hint, hintX, hintY, hintColor)
}

// drawOverlayBorder draws a 1px border around rect.
func (r *Renderer) drawOverlayBorder(rect image.Rectangle) {
	img := r.modalLayer
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+1)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Min.X, rect.Max.Y-1, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
	img.SubImage(image.Rect(rect.Max.X-1, rect.Min.Y, rect.Max.X, rect.Max.Y)).(*ebiten.Image).Fill(r.ui.Border)
}
