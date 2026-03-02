package renderer

import (
	"bytes"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/text/language"
	"golang.org/x/text/width"
)

// FontRenderer manages font face loading and glyph rendering.
// It uses Ebitengine's text/v2 package with a Go freetype face.
//
// Pattern: cache — glyphs are pre-measured; Ebitengine handles atlas internally.
type FontRenderer struct {
	face     *text.GoTextFace
	src      *text.GoTextFaceSource
	size     float64
	CellW    int // width of a single monospace cell in pixels
	CellH    int // height of a single monospace cell in pixels
	Baseline int // pixels from top of cell to text baseline

	// emojiFace is an optional fallback face used for emoji/wide-char glyphs.
	// When non-nil, DrawGlyph and DrawString use it for runes in emoji ranges.
	emojiFace *text.GoTextFace

	// drawOpts is reused across DrawGlyph/DrawString calls to avoid per-glyph heap allocation.
	drawOpts text.DrawOptions
}

// NewFontRenderer loads the embedded TTF and calculates cell metrics.
func NewFontRenderer(ttfData []byte, size float64) (*FontRenderer, error) {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(ttfData))
	if err != nil {
		return nil, err
	}

	face := &text.GoTextFace{
		Source: src,
		Size:   size,
	}

	// Measure a reference character to get cell dimensions.
	// 'M' is the standard reference for monospace width.
	w, h := text.Measure("M", face, 0)
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

	return &FontRenderer{
		face:     face,
		src:      src,
		size:     size,
		CellW:    cellW,
		CellH:    cellH,
		Baseline: baseline,
	}, nil
}

// DrawGlyph renders a single character onto dst at pixel position (x, y) — top-left of cell.
// Background is filled via SubImage (no per-cell allocation).
// Text is positioned at the cell top-left as required by Ebitengine text/v2.
func (f *FontRenderer) DrawGlyph(dst *ebiten.Image, ch rune, x, y int, fg, bg color.RGBA, bold, underline bool) {
	// Fill background using SubImage — zero allocations per call.
	dst.SubImage(image.Rect(x, y, x+f.CellW, y+f.CellH)).(*ebiten.Image).Fill(bg)

	if ch == ' ' || ch == 0 {
		return
	}

	// Reuse drawOpts to avoid a heap allocation per glyph.
	f.drawOpts.GeoM.Reset()
	f.drawOpts.GeoM.Translate(float64(x), float64(y))
	f.drawOpts.ColorScale.Reset()
	f.drawOpts.ColorScale.ScaleWithColor(fg)

	text.Draw(dst, string(ch), f.face, &f.drawOpts)

	if underline {
		dst.SubImage(image.Rect(x, y+f.CellH-2, x+f.CellW, y+f.CellH)).(*ebiten.Image).Fill(fg)
	}
}

// DrawCursor draws a cursor block, underline, or bar at the given cell position.
// Uses SubImage for zero-allocation fills.
func (f *FontRenderer) DrawCursor(dst *ebiten.Image, x, y int, style int, fg color.RGBA) {
	switch style {
	case 0: // block
		dst.SubImage(image.Rect(x, y, x+f.CellW, y+f.CellH)).(*ebiten.Image).Fill(fg)
	case 1: // underline
		dst.SubImage(image.Rect(x, y+f.CellH-2, x+f.CellW, y+f.CellH)).(*ebiten.Image).Fill(fg)
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
			text.Draw(dst, string(ch), f.faceFor(ch), &f.drawOpts)
		}
		x += w * f.CellW
	}
}

// LoadEmojiFont loads a secondary font used as fallback for emoji/wide-char glyphs.
// The face is sized to match the primary font's cell height for visual consistency.
func (f *FontRenderer) LoadEmojiFont(ttfData []byte) error {
	src, err := text.NewGoTextFaceSource(bytes.NewReader(ttfData))
	if err != nil {
		return err
	}
	f.emojiFace = &text.GoTextFace{
		Source: src,
		Size:   f.size,
		// Add language hint for better emoji rendering on macOS
		Language: language.English,
	}
	return nil
}

// HasEmojiSupport returns true if emoji font is loaded and ready.
func (f *FontRenderer) HasEmojiSupport() bool {
	return f.emojiFace != nil
}

// faceFor returns the emoji face when r is in an emoji/wide-char range and
// the emoji face is loaded; otherwise returns the primary monospace face.
func (f *FontRenderer) faceFor(r rune) *text.GoTextFace {
	if f.emojiFace != nil && RuneDisplayWidth(r) > 1 {
		return f.emojiFace
	}
	return f.face
}

// RuneDisplayWidth returns the number of terminal columns needed to display r.
// Returns 2 for wide characters (CJK, emoji), 0 for zero-width combiners, 1 otherwise.
func RuneDisplayWidth(r rune) int {
	// Zero-width: ZWJ, variation selectors, BOM.
	if r == 0x200D || r == 0xFEFF || (r >= 0xFE00 && r <= 0xFE0F) {
		return 0
	}
	// Supplementary multilingual plane emoji (U+1F000 and above) are double-width.
	if r >= 0x1F000 {
		return 2
	}
	// Common symbol/emoji blocks that are double-width in terminals.
	if r >= 0x2600 && r <= 0x27BF {
		return 2
	}
	// Unicode east-asian width classification covers CJK, Hangul, fullwidth forms, etc.
	p, _ := width.Lookup([]byte(string(r)))
	switch p.Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
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
