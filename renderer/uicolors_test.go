package renderer

import (
	"image/color"
	"testing"
)

func TestWithAlpha_Premultiplied(t *testing.T) {
	c := color.RGBA{R: 255, G: 128, B: 0, A: 255}
	result := withAlpha(c, 0x80) // ~50% alpha

	// With premultiplied alpha, RGB channels should be scaled down.
	// factor = 0x80/255 ≈ 0.502
	// R: 255 * 0.502 ≈ 128
	// G: 128 * 0.502 ≈ 64
	// B: 0 * 0.502 = 0
	if result.A != 0x80 {
		t.Errorf("A = %d, want %d", result.A, 0x80)
	}
	if result.R > 130 || result.R < 126 {
		t.Errorf("R = %d, want ~128 (premultiplied)", result.R)
	}
	if result.G > 66 || result.G < 62 {
		t.Errorf("G = %d, want ~64 (premultiplied)", result.G)
	}
	if result.B != 0 {
		t.Errorf("B = %d, want 0", result.B)
	}
}

func TestWithAlpha_FullOpaque(t *testing.T) {
	c := color.RGBA{R: 100, G: 200, B: 50, A: 255}
	result := withAlpha(c, 255)

	// factor = 255/255 = 1.0, RGB should be unchanged.
	if result.R != 100 || result.G != 200 || result.B != 50 || result.A != 255 {
		t.Errorf("withAlpha(c, 255) = %v, want unchanged RGBA", result)
	}
}

func TestWithAlpha_FullTransparent(t *testing.T) {
	c := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	result := withAlpha(c, 0)

	if result.R != 0 || result.G != 0 || result.B != 0 || result.A != 0 {
		t.Errorf("withAlpha(c, 0) = %v, want all zeros", result)
	}
}

func TestBackdropColor_NoOverflow(t *testing.T) {
	// White background: R=255. Before fix, 255*2 would overflow uint8.
	cfg := &RenderConfig{}
	cfg.Colors.Background = "#ffffff"
	cfg.Colors.Border = "#333333"
	cfg.Colors.Cursor = "#ff0000"
	cfg.Colors.BrightMagenta = "#ff00ff"
	cfg.Colors.Yellow = "#ffff00"
	cfg.Colors.Foreground = "#ffffff"
	cfg.Colors.BrightBlack = "#888888"
	cfg.Colors.MdBold = "#ffffff"
	cfg.Colors.MdHeading = "#ffffff"
	cfg.Colors.MdCode = "#ffffff"
	cfg.Colors.MdCodeBorder = "#333333"
	cfg.Colors.MdTableBorder = "#333333"
	cfg.Colors.MdMatchBg = "#ffff00"
	cfg.Colors.MdMatchCurBg = "#ff8800"
	cfg.Colors.MdBadgeBg = "#333333"
	cfg.Colors.MdBadgeFg = "#ffffff"

	ui := deriveUIColors(cfg)

	// 255 * 2 / 5 = 102, not the overflowed value.
	expected := uint8(102)
	if ui.Backdrop.R != expected {
		t.Errorf("Backdrop.R = %d, want %d (no overflow)", ui.Backdrop.R, expected)
	}
	if ui.Backdrop.G != expected {
		t.Errorf("Backdrop.G = %d, want %d", ui.Backdrop.G, expected)
	}
	if ui.Backdrop.B != expected {
		t.Errorf("Backdrop.B = %d, want %d", ui.Backdrop.B, expected)
	}
}

func TestBackdropColor_DarkBackground(t *testing.T) {
	cfg := &RenderConfig{}
	cfg.Colors.Background = "#1a1a2e"
	cfg.Colors.Border = "#333333"
	cfg.Colors.Cursor = "#ff0000"
	cfg.Colors.BrightMagenta = "#ff00ff"
	cfg.Colors.Yellow = "#ffff00"
	cfg.Colors.Foreground = "#ffffff"
	cfg.Colors.BrightBlack = "#888888"
	cfg.Colors.MdBold = "#ffffff"
	cfg.Colors.MdHeading = "#ffffff"
	cfg.Colors.MdCode = "#ffffff"
	cfg.Colors.MdCodeBorder = "#333333"
	cfg.Colors.MdTableBorder = "#333333"
	cfg.Colors.MdMatchBg = "#ffff00"
	cfg.Colors.MdMatchCurBg = "#ff8800"
	cfg.Colors.MdBadgeBg = "#333333"
	cfg.Colors.MdBadgeFg = "#ffffff"

	ui := deriveUIColors(cfg)

	// 0x1a = 26, 26*2/5 = 10
	if ui.Backdrop.R != 10 {
		t.Errorf("Backdrop.R = %d, want 10", ui.Backdrop.R)
	}
}
