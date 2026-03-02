// Package recorder provides screenshot capture for Ebitengine apps.
package recorder

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
)

// SavePNG encodes raw RGBA pixels as a PNG file and returns the output path.
// The caller must provide raw pixel data (from screen.ReadPixels) and the
// image bounds. PNG encoding runs on the calling goroutine — call from a
// background goroutine to avoid blocking the render loop.
func SavePNG(raw []byte, bounds image.Rectangle) (string, error) {
	outDir := screenshotDir()
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return "", fmt.Errorf("create screenshot dir: %w", err)
	}

	ts := time.Now().Format("2006-01-02-15-04-05")
	outPath := filepath.Join(outDir, "zurm-"+ts+".png")

	img := &image.RGBA{
		Pix:    raw,
		Stride: bounds.Dx() * 4,
		Rect:   bounds,
	}

	f, err := os.Create(outPath) // #nosec G304 — path built from time.Now() in screenshotDir()
	if err != nil {
		return "", fmt.Errorf("create png: %w", err)
	}
	defer f.Close() // #nosec G307

	if err := png.Encode(f, img); err != nil {
		return "", fmt.Errorf("encode png: %w", err)
	}
	return outPath, nil
}

// screenshotDir returns the directory where screenshots are saved.
// Preference order: ~/Pictures, ~/Desktop, ~/zurm-screenshots.
func screenshotDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "zurm-screenshots"
	}
	for _, sub := range []string{"Pictures", "Desktop"} {
		dir := filepath.Join(home, sub)
		if _, err := os.Stat(dir); err == nil {
			return filepath.Join(dir, "zurm-screenshots")
		}
	}
	return filepath.Join(home, "zurm-screenshots")
}
