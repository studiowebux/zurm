package renderer

import (
	"fmt"
	"image"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/fileexplorer"
)

// ExplorerMode controls what the file explorer input box is doing.
type ExplorerMode int

const (
	ExplorerModeNormal  ExplorerMode = iota
	ExplorerModeRename               // waiting for new name
	ExplorerModeNewFile              // waiting for new file name
	ExplorerModeNewDir               // waiting for new dir name
)

// FileExplorerState holds all rendering and interaction state for the file explorer sidebar.
type FileExplorerState struct {
	Open         bool
	Root         string
	Entries      []fileexplorer.Entry
	Cursor       int
	ScrollOffset int
	MaxScroll    int // written by renderer each frame
	RowH         int // written by renderer each frame

	Mode       ExplorerMode
	InputText  string
	InputLabel string

	Clipboard *fileexplorer.Clipboard

	ConfirmOpen   bool
	ConfirmMsg    string
	ConfirmAction func()

	StatusMsg   string
	StatusTimer int

	Side string // "left" or "right"
}

// FileExplorerPanelWidth returns the physical pixel width of the panel when open, 0 when closed.
func (r *Renderer) FileExplorerPanelWidth() int {
	if r.offscreen == nil {
		return 0
	}
	physW := r.offscreen.Bounds().Dx()
	return explorerPanelWidth(physW, r.font.CellW)
}

// explorerPanelWidth computes the panel width in physical pixels.
// cellW is the physical-pixel width of one character cell.
// Minimum: 56 cells (enough for the hint bar). Maximum: 72 cells.
func explorerPanelWidth(physW, cellW int) int {
	w := physW * 35 / 100
	minW := 56 * cellW
	maxW := 72 * cellW
	if w < minW {
		w = minW
	}
	if w > maxW {
		w = maxW
	}
	return w
}

// drawFileExplorer renders the file explorer panel onto r.modalLayer.
func (r *Renderer) drawFileExplorer(state *FileExplorerState) {
	if !state.Open {
		return
	}

	physW := r.modalLayer.Bounds().Dx()
	physH := r.modalLayer.Bounds().Dy()
	tabBarH := r.TabBarHeight()
	statusBarH := r.StatusBarHeight()

	cw := r.font.CellW
	ch := r.font.CellH

	panelW := explorerPanelWidth(physW, cw)
	panelH := physH - tabBarH - statusBarH

	var panelX int
	if state.Side == "right" {
		panelX = physW - panelW
	} else {
		panelX = 0
	}
	panelY := tabBarH

	panelRect := image.Rect(panelX, panelY, panelX+panelW, panelY+panelH)

	// Panel background.
	r.modalLayer.SubImage(panelRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(panelRect)
	rowH := ch + 2
	state.RowH = rowH

	// Header row: root path left, "Esc" right.
	headerH := ch + 6
	headerRect := image.Rect(panelX, panelY, panelX+panelW, panelY+headerH)
	r.modalLayer.SubImage(headerRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	// Divider under header.
	r.modalLayer.SubImage(image.Rect(panelX, panelY+headerH-1, panelX+panelW, panelY+headerH)).(*ebiten.Image).Fill(r.ui.Border)

	maxRootChars := (panelW - 6*cw) / cw
	root := state.Root
	if maxRootChars > 0 && len([]rune(root)) > maxRootChars {
		root = "\u2026" + string([]rune(root)[len([]rune(root))-maxRootChars+1:])
	}
	r.font.DrawString(r.modalLayer, root, panelX+cw/2, panelY+3, r.ui.Accent)
	escLabel := "Esc"
	escX := panelX + panelW - len([]rune(escLabel))*cw - cw/2
	r.font.DrawString(r.modalLayer, escLabel, escX, panelY+3, r.ui.Dim)

	// Compute content area.
	hintH := 2*ch + 10 // two hint lines
	inputH := 0
	if state.Mode != ExplorerModeNormal {
		inputH = ch + 8
	}

	contentTop := panelY + headerH
	contentBottom := panelY + panelH - hintH - inputH
	if contentBottom < contentTop {
		contentBottom = contentTop
	}
	visibleH := contentBottom - contentTop

	// Total content height.
	totalH := len(state.Entries) * rowH

	// Clamp scroll.
	state.MaxScroll = totalH - visibleH
	if state.MaxScroll < 0 {
		state.MaxScroll = 0
	}
	if state.ScrollOffset < 0 {
		state.ScrollOffset = 0
	}
	if state.ScrollOffset > state.MaxScroll {
		state.ScrollOffset = state.MaxScroll
	}

	// Draw entries into clipped region.
	contentRect := image.Rect(panelX, contentTop, panelX+panelW, contentBottom)
	contentImg := r.modalLayer.SubImage(contentRect).(*ebiten.Image)

	drawY := contentTop - state.ScrollOffset
	for i, e := range state.Entries {
		rowTop := drawY + i*rowH
		rowBot := rowTop + rowH

		// Skip rows outside clip region (but still need to render cursor highlight).
		if rowBot < contentTop || rowTop >= contentBottom {
			continue
		}

		if i == state.Cursor {
			highlightRect := image.Rect(panelX, rowTop, panelX+panelW, rowBot)
			r.modalLayer.SubImage(highlightRect).(*ebiten.Image).Fill(r.ui.HoverBg)
		}

		indent := e.Depth * 2 * cw
		x := panelX + cw/2 + indent

		// Dir indicator.
		var indicator string
		if e.IsDir {
			if e.Expanded {
				indicator = "\u25bc " // ▼
			} else {
				indicator = "\u25ba " // ▶
			}
		} else {
			indicator = "  "
		}

		nameColor := r.ui.Fg
		if e.IsDir {
			nameColor = r.ui.Accent
		}

		label := indicator + e.Name
		maxChars := (panelX + panelW - x - cw/2) / cw
		if maxChars < 1 {
			maxChars = 1
		}
		runes := []rune(label)
		if len(runes) > maxChars {
			if maxChars > 1 {
				label = string(runes[:maxChars-1]) + "\u2026"
			} else {
				label = string(runes[:maxChars])
			}
		}
		r.font.DrawString(contentImg, label, x, rowTop+1, nameColor)
	}

	// Scrollbar.
	if state.MaxScroll > 0 && visibleH > 0 {
		sbX := panelX + panelW - 4
		thumbH := visibleH * visibleH / totalH
		if thumbH < 8 {
			thumbH = 8
		}
		thumbY := contentTop + (visibleH-thumbH)*state.ScrollOffset/state.MaxScroll
		r.modalLayer.SubImage(image.Rect(sbX, thumbY, sbX+3, thumbY+thumbH)).(*ebiten.Image).Fill(r.ui.Dim)
	}

	// Input box (when in rename/new-file/new-dir mode).
	if state.Mode != ExplorerModeNormal {
		inputY := contentBottom
		inputRect := image.Rect(panelX, inputY, panelX+panelW, inputY+inputH)
		r.modalLayer.SubImage(inputRect).(*ebiten.Image).Fill(r.ui.HoverBg)
		r.modalLayer.SubImage(image.Rect(panelX, inputY, panelX+panelW, inputY+1)).(*ebiten.Image).Fill(r.ui.Border)
		label := state.InputLabel + " " + state.InputText + "_"
		r.font.DrawString(r.modalLayer, label, panelX+cw/2, inputY+4, r.ui.Fg)
	}

	// Status message row (shown above hint bar when StatusTimer > 0).
	if state.StatusTimer > 0 {
		statusY := panelY + panelH - hintH - inputH - rowH
		if statusY > panelY {
			r.font.DrawString(r.modalLayer, state.StatusMsg, panelX+cw/2, statusY+3, r.ui.Accent)
		}
	}

	// Hint bar at bottom.
	hintY := panelY + panelH - hintH
	hintRect := image.Rect(panelX, hintY, panelX+panelW, hintY+hintH)
	r.modalLayer.SubImage(hintRect).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.modalLayer.SubImage(image.Rect(panelX, hintY, panelX+panelW, hintY+1)).(*ebiten.Image).Fill(r.ui.Border)

	hint1, hint2 := buildHintBars(state, cw, panelW)
	r.font.DrawString(r.modalLayer, hint1, panelX+cw/2, hintY+3, r.ui.Dim)
	r.font.DrawString(r.modalLayer, hint2, panelX+cw/2, hintY+3+ch, r.ui.Dim)

	// Confirm dialog (centered over panel).
	if state.ConfirmOpen {
		r.drawExplorerConfirm(state, panelRect)
	}
}

// buildHintBars returns two hint lines for the two-row hint bar.
// line1 always shows the full action hints so they are never displaced.
// line2 shows clipboard state (when active) or navigation hints.
func buildHintBars(state *FileExplorerState, cw, panelW int) (line1, line2 string) {
	maxChars := (panelW - cw) / cw
	if maxChars < 1 {
		return "", ""
	}

	const actions = "c cp  x cut  p pst  d del  r ren  n fil  N dir  o fin"
	const nav = "Enter open  \u2191\u2193 nav  \u2190 col  \u2192 exp  Esc close"

	line1 = truncHintRunes(actions, maxChars)

	if state.Clipboard != nil {
		name := filepath.Base(state.Clipboard.Path)
		const maxName = 16
		if len([]rune(name)) > maxName {
			name = string([]rune(name)[:maxName-1]) + "\u2026"
		}
		raw := fmt.Sprintf("[%s: %s]  p pst  d del  r ren", state.Clipboard.Op, name)
		line2 = truncHintRunes(raw, maxChars)
	} else {
		line2 = truncHintRunes(nav, maxChars)
	}
	return
}

func truncHintRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// drawExplorerConfirm renders a small confirm dialog centered over panelRect.
func (r *Renderer) drawExplorerConfirm(state *FileExplorerState, panelRect image.Rectangle) {
	cw := r.font.CellW
	ch := r.font.CellH

	hint := "[Enter] yes   [Esc] no"
	msgLen := len([]rune(state.ConfirmMsg))
	hintLen := len([]rune(hint))
	innerW := hintLen
	if msgLen > innerW {
		innerW = msgLen
	}

	pad := cw
	dw := innerW*cw + pad*2
	dh := ch*4 + pad*2
	dx := panelRect.Min.X + (panelRect.Dx()-dw)/2
	dy := panelRect.Min.Y + (panelRect.Dy()-dh)/2
	dr := image.Rect(dx, dy, dx+dw, dy+dh)

	r.modalLayer.SubImage(dr).(*ebiten.Image).Fill(r.ui.PanelBg)
	r.drawOverlayBorder(dr)

	msgX := dx + (dw-msgLen*cw)/2
	r.font.DrawString(r.modalLayer, state.ConfirmMsg, msgX, dy+pad, r.ui.Accent)

	hintX := dx + (dw-hintLen*cw)/2
	r.font.DrawString(r.modalLayer, hint, hintX, dy+pad+ch*2, r.ui.Dim)
}
