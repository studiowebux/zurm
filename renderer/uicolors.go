package renderer

import (
	"image/color"
)

const backdropAlpha = 0xb4 // 180 — translucency for modal overlays

// inputWithCursor returns text with a block cursor character (|) inserted at
// the given rune index. Used by all input field renderers instead of appending "_".
func inputWithCursor(text string, pos int) string {
	runes := []rune(text)
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	return string(runes[:pos]) + "|" + string(runes[pos:])
}

// UIColors holds all derived UI chrome colors for overlays, menus, and panels.
// Computed once from RenderConfig so draw functions never hard-code color literals.
// Pattern: derived value — computed at renderer init, re-derived on config reload.
type UIColors struct {
	// PanelBg is the background of floating panels (command palette, menu, help overlay).
	// Darker than the terminal body so panels visually float above content.
	PanelBg color.RGBA

	// HoverBg is used for hover rows, input fields, and elevated surfaces.
	// Slightly lighter than the terminal body.
	HoverBg color.RGBA

	// Backdrop is the full-screen semi-transparent overlay behind modal panels.
	Backdrop color.RGBA

	// Border is used for panel outlines and divider lines.
	Border color.RGBA

	// Accent is the primary highlight color: prompt ">", titles, active rows,
	// cursor bar, copy buttons.
	Accent color.RGBA

	// CatHdr is the category header color in the help overlay.
	CatHdr color.RGBA

	// KeyName is the color for keybinding labels and shortcut text.
	KeyName color.RGBA

	// Fg is the primary foreground color for menu items and descriptions.
	Fg color.RGBA

	// Dim is the secondary / muted text color for hints, arrows, and dim labels.
	Dim color.RGBA

	// SearchMatch is the highlight color for matching search results.
	SearchMatch color.RGBA

	// Markdown viewer colors.
	MdBold        color.RGBA
	MdHeading     color.RGBA
	MdCode        color.RGBA
	MdCodeBorder  color.RGBA
	MdTableBorder color.RGBA
	MdMatchBg     color.RGBA
	MdMatchCurBg  color.RGBA
	MdBadgeBg     color.RGBA
	MdBadgeFg     color.RGBA
}

// deriveUIColors computes UIColors from a Config.
// The relationships are:
//   - PanelBg  = background darkened twice (~65% brightness)
//   - HoverBg  = background brightened (~130% brightness)
//   - Backdrop = background at ~40% brightness, semi-transparent
//   - Border   = Colors.Border (directly)
//   - Accent   = Colors.Cursor
//   - CatHdr   = Colors.BrightMagenta
//   - KeyName  = Colors.Yellow
//   - Fg       = Colors.Foreground
//   - Dim      = Colors.BrightBlack
func deriveUIColors(cfg *RenderConfig) UIColors {
	bg := parseHexColor(cfg.Colors.Background)
	return UIColors{
		PanelBg:     darken(darken(bg)),
		HoverBg:     brighten(bg),
		Backdrop:    color.RGBA{R: bg.R * 2 / 5, G: bg.G * 2 / 5, B: bg.B * 2 / 5, A: backdropAlpha},
		Border:      parseHexColor(cfg.Colors.Border),
		Accent:      parseHexColor(cfg.Colors.Cursor),
		CatHdr:      parseHexColor(cfg.Colors.BrightMagenta),
		KeyName:     parseHexColor(cfg.Colors.Yellow),
		Fg:          parseHexColor(cfg.Colors.Foreground),
		Dim:         parseHexColor(cfg.Colors.BrightBlack),
		SearchMatch:   parseHexColor(cfg.Colors.Yellow),
		MdBold:        parseHexColor(cfg.Colors.MdBold),
		MdHeading:     parseHexColor(cfg.Colors.MdHeading),
		MdCode:        parseHexColor(cfg.Colors.MdCode),
		MdCodeBorder:  parseHexColor(cfg.Colors.MdCodeBorder),
		MdTableBorder: parseHexColor(cfg.Colors.MdTableBorder),
		MdMatchBg:     withAlpha(parseHexColor(cfg.Colors.MdMatchBg), 0x60),
		MdMatchCurBg:  withAlpha(parseHexColor(cfg.Colors.MdMatchCurBg), 0x80),
		MdBadgeBg:     parseHexColor(cfg.Colors.MdBadgeBg),
		MdBadgeFg:     parseHexColor(cfg.Colors.MdBadgeFg),
	}
}

func withAlpha(c color.RGBA, a uint8) color.RGBA {
	c.A = a
	return c
}

// separatorColor returns the configured separator color, falling back to BrightBlack.
func (r *Renderer) separatorColor() color.RGBA {
	if r.cfg.Colors.Separator != "" {
		return parseHexColor(r.cfg.Colors.Separator)
	}
	return parseHexColor(r.cfg.Colors.BrightBlack)
}

// brighten returns a lighter version of c (scales RGB by 130%, clamped at 255).
func brighten(c color.RGBA) color.RGBA {
	scale := func(v uint8) uint8 {
		n := int(v) * 130 / 100
		if n > 255 {
			return 255
		}
		return uint8(n)
	}
	return color.RGBA{R: scale(c.R), G: scale(c.G), B: scale(c.B), A: c.A}
}
