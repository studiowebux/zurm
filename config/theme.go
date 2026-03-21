package config

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// ThemeFile represents the contents of an external theme TOML file.
// Only the [colors] section is loaded from themes.
type ThemeFile struct {
	Colors ColorConfig `toml:"colors"`
}

// ThemesDir returns the path to the themes directory (~/.config/zurm/themes/).
func ThemesDir() string {
	dir := ConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "themes")
}

// LoadTheme reads a theme TOML file by name (without .toml extension)
// and returns its ColorConfig.
func LoadTheme(name string) (ColorConfig, error) {
	dir := ThemesDir()
	if dir == "" {
		return ColorConfig{}, os.ErrNotExist
	}
	path := filepath.Join(dir, name+".toml")
	var tf ThemeFile
	if _, err := toml.DecodeFile(path, &tf); err != nil {
		return ColorConfig{}, err
	}
	return tf.Colors, nil
}

// ListThemes scans the themes directory and returns available theme names
// (filenames without .toml extension).
func ListThemes() []string {
	dir := ThemesDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".toml") {
			names = append(names, strings.TrimSuffix(name, ".toml"))
		}
	}
	return names
}

// MergeColorsWithMeta merges theme colors with user-explicit colors.
// For each color field, if the user explicitly set it in config.toml
// (tracked by meta.IsDefined), the user value wins. Otherwise the theme
// value is used.
func MergeColorsWithMeta(theme, user ColorConfig, meta toml.MetaData) ColorConfig {
	result := theme
	rt := reflect.TypeOf(result)
	rv := reflect.ValueOf(&result).Elem()
	uv := reflect.ValueOf(user)

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("toml")
		if tag == "" {
			continue
		}
		if meta.IsDefined("colors", tag) {
			rv.Field(i).Set(uv.Field(i))
		}
	}
	return result
}

// ApplyTheme loads the named theme (if any) and merges it with user colors.
// If no theme is configured (empty name), cfg is returned unchanged.
func ApplyTheme(cfg *Config, meta toml.MetaData) {
	if cfg.Theme.Name == "" {
		return
	}
	themeColors, err := LoadTheme(cfg.Theme.Name)
	if err != nil {
		log.Printf("config: load theme %q: %v", cfg.Theme.Name, err)
		return
	}
	cfg.Colors = MergeColorsWithMeta(themeColors, cfg.Colors, meta)
}
