package renderer

import (
	"bytes"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/studiowebux/zurm/terminal"
)

// FontRenderer manages font face loading and glyph rendering.
// It uses Ebitengine's text/v2 package with a Go freetype face.
// When a fallback font is configured, a MultiFace is used so missing glyphs
// (e.g. CJK characters absent from JetBrains Mono) fall through to the
// secondary font automatically.
//
// Pattern: cache — glyphs are pre-measured; Ebitengine handles atlas internally.
type FontRenderer struct {
	face     text.Face // primary GoTextFace or MultiFace with fallback
	src      *text.GoTextFaceSource
	size     float64
	CellW    int // width of a single monospace cell in pixels
	CellH    int // height of a single monospace cell in pixels
	Baseline int // pixels from top of cell to text baseline

	// drawOpts is reused across DrawGlyph/DrawString calls to avoid per-glyph heap allocation.
	drawOpts text.DrawOptions

	// runeStr caches string(rune) conversions for ASCII to avoid per-glyph heap allocation.
	// Index 32..126 covers printable ASCII. Non-ASCII runes fall back to string(ch).
	runeStr [127]string
}

// NewFontRenderer loads the primary TTF and calculates cell metrics.
// If fallbackData is non-nil, a MultiFace is created so glyphs missing from
// the primary font (e.g. CJK) are rendered from the fallback.
func NewFontRenderer(ttfData []byte, size float64, fallbackData ...[]byte) (*FontRenderer, error) {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(ttfData))
	if err != nil {
		return nil, err
	}

	primary := &text.GoTextFace{
		Source: src,
		Size:   size,
	}

	// Measure a reference character to get cell dimensions.
	// 'M' is the standard reference for monospace width.
	w, h := text.Measure("M", primary, 0)
	cellW := int(w + 0.5)
	cellH := int(h + 0.5)
	if cellW < 1 {
		cellW = int(size/2 + 0.5)
	}
	if cellH < 1 {
		cellH = int(size + 0.5)
	}

	// Baseline: approximately 80% of cell height is a reasonable default.
	baseline := int(float64(cellH)*0.80 + 0.5)

	// Build the effective face: primary alone or MultiFace with fallback.
	var face text.Face = primary
	if len(fallbackData) > 0 && fallbackData[0] != nil {
		fbSrc, fbErr := text.NewGoTextFaceSource(bytes.NewReader(fallbackData[0]))
		if fbErr == nil {
			fbFace := &text.GoTextFace{Source: fbSrc, Size: size}
			if multi, mErr := text.NewMultiFace(primary, fbFace); mErr == nil {
				face = multi
			}
		}
	}

	f := &FontRenderer{
		face:     face,
		src:      src,
		size:     size,
		CellW:    cellW,
		CellH:    cellH,
		Baseline: baseline,
	}
	// Pre-compute string(rune) for printable ASCII to avoid per-glyph allocation.
	for r := rune(32); r < 127; r++ {
		f.runeStr[r] = string(r)
	}
	return f, nil
}

// runeString returns a cached string for ASCII runes, avoiding per-call allocation.
func (f *FontRenderer) runeString(ch rune) string {
	if ch >= 32 && ch < 127 {
		return f.runeStr[ch]
	}
	return string(ch)
}

// DrawGlyph renders a single character onto dst at pixel position (x, y) — top-left of cell.
// widthCells is the number of columns the glyph spans (1 for normal, 2 for wide).
// Background is filled via SubImage (no per-cell allocation).
// Text is positioned at the cell top-left as required by Ebitengine text/v2.
func (f *FontRenderer) DrawGlyph(dst *ebiten.Image, ch rune, x, y int, fg, bg color.RGBA, bold, underline bool, widthCells int) {
	w := widthCells * f.CellW
	// Fill background using SubImage — zero allocations per call.
	dst.SubImage(image.Rect(x, y, x+w, y+f.CellH)).(*ebiten.Image).Fill(bg)

	if ch == ' ' || ch == 0 {
		return
	}

	// Reuse drawOpts to avoid a heap allocation per glyph.
	f.drawOpts.GeoM.Reset()
	f.drawOpts.GeoM.Translate(float64(x), float64(y))
	f.drawOpts.ColorScale.Reset()
	f.drawOpts.ColorScale.ScaleWithColor(fg)

	text.Draw(dst, f.runeString(ch), f.face, &f.drawOpts)

	if underline {
		dst.SubImage(image.Rect(x, y+f.CellH-2, x+w, y+f.CellH)).(*ebiten.Image).Fill(fg)
	}
}

// DrawCursor draws a cursor block, underline, or bar at the given cell position.
// widthCells is the number of columns the cursor spans (1 for normal, 2 for wide).
// Uses SubImage for zero-allocation fills.
func (f *FontRenderer) DrawCursor(dst *ebiten.Image, x, y int, style int, fg color.RGBA, widthCells int) {
	w := widthCells * f.CellW
	switch style {
	case 0: // block
		dst.SubImage(image.Rect(x, y, x+w, y+f.CellH)).(*ebiten.Image).Fill(fg)
	case 1: // underline
		dst.SubImage(image.Rect(x, y+f.CellH-2, x+w, y+f.CellH)).(*ebiten.Image).Fill(fg)
	case 2: // bar
		dst.SubImage(image.Rect(x, y, x+2, y+f.CellH)).(*ebiten.Image).Fill(fg)
	}
}

// WindowSize returns the pixel dimensions for the given grid size.
func (f *FontRenderer) WindowSize(cols, rows, padding int) (w, h int) {
	w = cols*f.CellW + padding*2
	h = rows*f.CellH + padding*2
	return
}

// GridDimensions returns how many cols/rows fit in the given pixel area.
func (f *FontRenderer) GridDimensions(pixW, pixH, padding int) (cols, rows int) {
	cols = (pixW - padding*2) / f.CellW
	rows = (pixH - padding*2) / f.CellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return
}

// DrawString renders a string left-to-right starting at (x, y).
// Wide characters (CJK, emoji) advance by 2×CellW; all others by CellW.
// Background is not filled — the caller is responsible for pre-filling the background rect.
func (f *FontRenderer) DrawString(dst *ebiten.Image, s string, x, y int, clr color.RGBA) {
	for _, ch := range s {
		w := RuneDisplayWidth(ch)
		if w > 0 && ch != ' ' {
			f.drawOpts.GeoM.Reset()
			f.drawOpts.GeoM.Translate(float64(x), float64(y))
			f.drawOpts.ColorScale.Reset()
			f.drawOpts.ColorScale.ScaleWithColor(clr)
			text.Draw(dst, f.runeString(ch), f.face, &f.drawOpts)
		}
		x += w * f.CellW
	}
}

// Emoji support removed - see docs/emoji-limitations.md
// Ebiten doesn't support color fonts due to golang.org/x/image/font limitations

// RuneDisplayWidth returns the number of terminal columns needed to display r.
// Delegates to terminal.RuneWidth — single source of truth for width calculation.
func RuneDisplayWidth(r rune) int {
	return terminal.RuneWidth(r)
}

// StringDisplayWidth returns the total number of terminal columns needed to display s.
func StringDisplayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += RuneDisplayWidth(r)
	}
	return n
}

// CellRect returns an image.Rectangle for a cell at (col, row).
func (f *FontRenderer) CellRect(col, row, padding int) image.Rectangle {
	x := col*f.CellW + padding
	y := row*f.CellH + padding
	return image.Rect(x, y, x+f.CellW, y+f.CellH)
}
