package renderer

import (
	"image/color"
	"log"
	"strconv"
	"strings"
)

// RenderConfig holds the subset of application config that the renderer needs.
// Built by main.go from *config.Config so the renderer never imports config.
type RenderConfig struct {
	Colors    RenderColorConfig
	Window    RenderWindowConfig
	Tabs      RenderTabsConfig
	StatusBar RenderStatusBarConfig
	Blocks    RenderBlocksConfig
	Bell      RenderBellConfig
	Vault     RenderVaultConfig
}

// RenderColorConfig contains all color hex strings the renderer reads.
type RenderColorConfig struct {
	Background    string
	Foreground    string
	Cursor        string
	Border        string
	BrightBlack   string
	BrightMagenta string
	Yellow        string
	Red           string
	Blue          string
	Cyan          string
	Separator     string
	MdBold        string
	MdHeading     string
	MdCode        string
	MdCodeBorder  string
	MdTableBorder string
	MdMatchBg     string
	MdMatchCurBg  string
	MdBadgeBg     string
	MdBadgeFg     string
}

// RenderWindowConfig contains window layout values.
type RenderWindowConfig struct {
	Padding int
}

// RenderTabsConfig contains tab bar sizing values.
type RenderTabsConfig struct {
	MaxWidthChars int
}

// RenderStatusBarConfig contains status bar layout and visibility flags.
type RenderStatusBarConfig struct {
	Enabled           bool
	PaddingPx         int
	SeparatorHeightPx int
	ShowClock         bool
	ShowGit           bool
	BranchPrefix      string
	ShowCommit        bool
	ShowDirty         bool
	ShowAheadBehind   bool
	ShowProcess       bool
	ShowCwd           bool
	SegmentSeparator  string
}

// RenderBlocksConfig contains command block decoration settings.
type RenderBlocksConfig struct {
	Enabled      bool
	ShowDuration bool
	BorderWidth  int
	BorderColor  string
	SuccessColor string
	FailColor    string
	ShowBorder   bool
	BgColor      string
	BgAlpha      float64
}

// RenderBellConfig contains bell alert settings.
type RenderBellConfig struct {
	Color string
}

// RenderVaultConfig contains vault suggestion display settings.
type RenderVaultConfig struct {
	SuggestionColor string
}

// parseHexColor converts a "#RRGGBB" string to color.RGBA.
// Falls back to white on invalid input.
func parseHexColor(s string) color.RGBA {
	raw := s
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		log.Printf("renderer: invalid hex color %q, falling back to white", raw)
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	r, rErr := strconv.ParseUint(s[0:2], 16, 8)
	g, gErr := strconv.ParseUint(s[2:4], 16, 8)
	b, bErr := strconv.ParseUint(s[4:6], 16, 8)
	if rErr != nil || gErr != nil || bErr != nil {
		log.Printf("renderer: invalid hex color %q, falling back to white", raw)
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}
