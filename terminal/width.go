package terminal

import "golang.org/x/text/width"

// RuneWidth returns the number of terminal columns needed to display r.
// Returns 2 for wide characters (CJK, emoji), 0 for zero-width combiners, 1 otherwise.
func RuneWidth(r rune) int {
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
