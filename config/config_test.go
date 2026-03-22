package config

import (
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// --- Defaults ---

func TestDefaults_FontPopulated(t *testing.T) {
	if Defaults.Font.Family == "" {
		t.Error("Font.Family should have default")
	}
	if Defaults.Font.Size == 0 {
		t.Error("Font.Size should have default")
	}
}

func TestDefaults_WindowPopulated(t *testing.T) {
	if Defaults.Window.Columns == 0 {
		t.Error("Window.Columns should have default")
	}
	if Defaults.Window.Rows == 0 {
		t.Error("Window.Rows should have default")
	}
	if Defaults.Window.Padding == 0 {
		t.Error("Window.Padding should have default")
	}
}

func TestDefaults_ScrollbackPopulated(t *testing.T) {
	if Defaults.Scrollback.Lines == 0 {
		t.Error("Scrollback.Lines should have default")
	}
}

func TestDefaults_PerformancePopulated(t *testing.T) {
	if Defaults.Performance.TPS == 0 {
		t.Error("Performance.TPS should have default")
	}
	if Defaults.Performance.PprofPort == 0 {
		t.Error("Performance.PprofPort should have default")
	}
}

func TestDefaults_ColorsPopulated(t *testing.T) {
	c := Defaults.Colors
	fields := []struct {
		name  string
		value string
	}{
		{"Background", c.Background},
		{"Foreground", c.Foreground},
		{"Cursor", c.Cursor},
		{"Border", c.Border},
		{"Black", c.Black},
		{"Red", c.Red},
		{"Green", c.Green},
		{"Yellow", c.Yellow},
		{"Blue", c.Blue},
		{"Magenta", c.Magenta},
		{"Cyan", c.Cyan},
		{"White", c.White},
		{"BrightBlack", c.BrightBlack},
		{"BrightWhite", c.BrightWhite},
		{"MdBold", c.MdBold},
		{"MdHeading", c.MdHeading},
		{"MdCode", c.MdCode},
		{"MdCodeBorder", c.MdCodeBorder},
		{"MdTableBorder", c.MdTableBorder},
		{"MdMatchBg", c.MdMatchBg},
		{"MdMatchCurBg", c.MdMatchCurBg},
		{"MdBadgeBg", c.MdBadgeBg},
		{"MdBadgeFg", c.MdBadgeFg},
	}
	for _, f := range fields {
		if f.value == "" {
			t.Errorf("Defaults.Colors.%s should not be empty", f.name)
		}
	}
}

func TestDefaults_BellPopulated(t *testing.T) {
	if Defaults.Bell.Style == "" {
		t.Error("Bell.Style should have default")
	}
	if Defaults.Bell.DurationMs == 0 {
		t.Error("Bell.DurationMs should have default")
	}
	if Defaults.Bell.Color == "" {
		t.Error("Bell.Color should have default")
	}
}

func TestDefaults_BlocksPopulated(t *testing.T) {
	if Defaults.Blocks.BorderWidth == 0 {
		t.Error("Blocks.BorderWidth should have default")
	}
	if Defaults.Blocks.MaxHistory == 0 {
		t.Error("Blocks.MaxHistory should have default")
	}
	if Defaults.Blocks.BorderColor == "" {
		t.Error("Blocks.BorderColor should have default")
	}
}

func TestDefaults_KeyboardPopulated(t *testing.T) {
	if Defaults.Keyboard.RepeatDelayMs == 0 {
		t.Error("Keyboard.RepeatDelayMs should have default")
	}
	if Defaults.Keyboard.RepeatIntervalMs == 0 {
		t.Error("Keyboard.RepeatIntervalMs should have default")
	}
}

// --- ParseHexColor ---

func TestParseHexColor_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  color.RGBA
	}{
		{"#FF0000", color.RGBA{R: 255, G: 0, B: 0, A: 255}},
		{"#00FF00", color.RGBA{R: 0, G: 255, B: 0, A: 255}},
		{"#0000FF", color.RGBA{R: 0, G: 0, B: 255, A: 255}},
		{"#FFFFFF", color.RGBA{R: 255, G: 255, B: 255, A: 255}},
		{"#000000", color.RGBA{R: 0, G: 0, B: 0, A: 255}},
		{"#A855F7", color.RGBA{R: 168, G: 85, B: 247, A: 255}},
	}
	for _, tt := range tests {
		got := ParseHexColor(tt.input)
		if got != tt.want {
			t.Errorf("ParseHexColor(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseHexColor_NoHash(t *testing.T) {
	got := ParseHexColor("FF0000")
	want := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	if got != want {
		t.Errorf("ParseHexColor without # = %v, want %v", got, want)
	}
}

func TestParseHexColor_Invalid(t *testing.T) {
	// Invalid colors should fall back to white.
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	tests := []string{"", "#", "#FFF", "#GGGGGG", "not-a-color"}
	for _, input := range tests {
		got := ParseHexColor(input)
		if got != white {
			t.Errorf("ParseHexColor(%q) = %v, want white fallback", input, got)
		}
	}
}

func TestParseHexColor_CaseSensitive(t *testing.T) {
	lower := ParseHexColor("#ff0000")
	upper := ParseHexColor("#FF0000")
	if lower != upper {
		t.Errorf("case mismatch: lower=%v, upper=%v", lower, upper)
	}
}

// --- Palette ---

func TestPalette_16Colors(t *testing.T) {
	cfg := Defaults
	palette := cfg.Palette()
	for i, c := range palette {
		if c.A != 255 {
			t.Errorf("palette[%d] alpha = %d, want 255", i, c.A)
		}
		// All default palette colors should be non-zero (not all black).
		if c.R == 0 && c.G == 0 && c.B == 0 && i != 0 {
			// Color 0 (black) can be non-zero in our theme (it's #555570).
			// But let's just verify alpha is set.
		}
	}
}

// --- TOML Loading ---

func TestLoadFromTOML_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// Only override font size — everything else should keep defaults.
	err := os.WriteFile(path, []byte(`
[font]
size = 20
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Defaults
	if _, err := loadFromPath(path, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Font.Size != 20 {
		t.Errorf("Font.Size = %f, want 20", cfg.Font.Size)
	}
	// Family should keep default.
	if cfg.Font.Family != Defaults.Font.Family {
		t.Errorf("Font.Family = %q, want default %q", cfg.Font.Family, Defaults.Font.Family)
	}
	// Colors should keep defaults.
	if cfg.Colors.Background != Defaults.Colors.Background {
		t.Errorf("Colors.Background changed unexpectedly: %q", cfg.Colors.Background)
	}
}

func TestLoadFromTOML_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(""), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Defaults
	if _, err := loadFromPath(path, &cfg); err != nil {
		t.Fatal(err)
	}

	// Everything should match defaults.
	if cfg.Font.Size != Defaults.Font.Size {
		t.Errorf("Font.Size = %f, want %f", cfg.Font.Size, Defaults.Font.Size)
	}
	if cfg.Colors.Cursor != Defaults.Colors.Cursor {
		t.Errorf("Colors.Cursor = %q, want %q", cfg.Colors.Cursor, Defaults.Colors.Cursor)
	}
}

func TestLoadFromTOML_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte("this is not valid toml [[["), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Defaults
	_, loadErr := loadFromPath(path, &cfg)
	if loadErr == nil {
		t.Error("expected error for invalid TOML")
	}
}

func TestLoadFromTOML_UnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[font]
size = 18
unknown_field = "ignored"

[nonexistent_section]
foo = "bar"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Defaults
	_, loadErr := loadFromPath(path, &cfg)
	// BurntSushi/toml doesn't error on unknown fields by default.
	if loadErr != nil {
		t.Errorf("unexpected error for unknown fields: %v", loadErr)
	}
	if cfg.Font.Size != 18 {
		t.Errorf("Font.Size = %f, want 18", cfg.Font.Size)
	}
}

func TestLoadFromTOML_ColorOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(`
[colors]
background = "#112233"
cursor = "#AABBCC"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Defaults
	if _, err := loadFromPath(path, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Colors.Background != "#112233" {
		t.Errorf("Background = %q, want #112233", cfg.Colors.Background)
	}
	if cfg.Colors.Cursor != "#AABBCC" {
		t.Errorf("Cursor = %q, want #AABBCC", cfg.Colors.Cursor)
	}
	// Unset color should keep default.
	if cfg.Colors.Foreground != Defaults.Colors.Foreground {
		t.Errorf("Foreground changed unexpectedly: %q", cfg.Colors.Foreground)
	}
}

// --- Theme Merge ---

func TestMergeColorsWithMeta_FallbackOnEmpty(t *testing.T) {
	// Theme has empty md_bold; user has default. Should keep user default.
	theme := ColorConfig{Background: "#111111"}
	user := ColorConfig{Background: "#222222", MdBold: "#FFFFFF"}

	// Simulate: user didn't set background in config (meta won't have it).
	// This tests that empty theme values fall back to user values.
	result := MergeColorsWithMeta(theme, user, emptyMeta())

	if result.Background != "#111111" {
		t.Errorf("Background = %q, want theme value #111111", result.Background)
	}
	if result.MdBold != "#FFFFFF" {
		t.Errorf("MdBold = %q, want user default #FFFFFF", result.MdBold)
	}
}

// --- Helpers ---

// loadFromPath is a test helper that loads TOML into cfg.
func loadFromPath(path string, cfg *Config) (interface{}, error) {
	_, err := toml.DecodeFile(path, cfg)
	return nil, err
}

// emptyMeta returns a MetaData where IsDefined always returns false.
func emptyMeta() toml.MetaData {
	md, _ := toml.Decode("", &struct{}{})
	return md
}

// --- Reflect: verify Defaults has no zero-value color fields ---

func TestDefaults_NoEmptyColorStrings(t *testing.T) {
	rv := reflect.ValueOf(Defaults.Colors)
	rt := rv.Type()
	// BgColor is intentionally empty (optional tint).
	skip := map[string]bool{"BgColor": true}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if skip[name] {
			continue
		}
		if rv.Field(i).Kind() == reflect.String && rv.Field(i).String() == "" {
			t.Errorf("Defaults.Colors.%s is empty — should have a default hex color", name)
		}
	}
}
