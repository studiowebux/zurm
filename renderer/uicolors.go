package renderer

import (
	"image/color"

	"github.com/studiowebux/zurm/config"
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
// Computed once from config.Config so draw functions never hard-code color literals.
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
}

// deriveUIColors computes UIColors from a Config.
// The relationships are:
//   - PanelBg  = background darkened twice (~65% brightness)
//   - HoverBg  = background brightened (~130% brightness)
//   - Backdrop = background at ~40% brightness, semi-transparent
//   - Border   = config.Colors.Border (directly)
//   - Accent   = config.Colors.Cursor
//   - CatHdr   = config.Colors.BrightMagenta
//   - KeyName  = config.Colors.Yellow
//   - Fg       = config.Colors.Foreground
//   - Dim      = config.Colors.BrightBlack
func deriveUIColors(cfg *config.Config) UIColors {
	bg := config.ParseHexColor(cfg.Colors.Background)
	return UIColors{
		PanelBg:     darken(darken(bg)),
		HoverBg:     brighten(bg),
		Backdrop:    color.RGBA{R: bg.R * 2 / 5, G: bg.G * 2 / 5, B: bg.B * 2 / 5, A: backdropAlpha},
		Border:      config.ParseHexColor(cfg.Colors.Border),
		Accent:      config.ParseHexColor(cfg.Colors.Cursor),
		CatHdr:      config.ParseHexColor(cfg.Colors.BrightMagenta),
		KeyName:     config.ParseHexColor(cfg.Colors.Yellow),
		Fg:          config.ParseHexColor(cfg.Colors.Foreground),
		Dim:         config.ParseHexColor(cfg.Colors.BrightBlack),
		SearchMatch: config.ParseHexColor(cfg.Colors.Yellow),
	}
}

// separatorColor returns the configured separator color, falling back to BrightBlack.
func (r *Renderer) separatorColor() color.RGBA {
	if r.cfg.Colors.Separator != "" {
		return config.ParseHexColor(r.cfg.Colors.Separator)
	}
	return config.ParseHexColor(r.cfg.Colors.BrightBlack)
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
