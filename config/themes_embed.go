package config

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed themes/dark.toml
var darkTheme []byte

//go:embed themes/light.toml
var lightTheme []byte

// EnsureBuiltinThemes writes the embedded dark and light theme files to
// ~/.config/zurm/themes/ if they do not already exist. Called on first launch.
func EnsureBuiltinThemes() {
	dir := ThemesDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	builtins := map[string][]byte{
		"dark.toml":  darkTheme,
		"light.toml": lightTheme,
	}
	for name, data := range builtins {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_ = os.WriteFile(path, data, 0o600)
		}
	}
}
