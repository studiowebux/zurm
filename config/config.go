package config

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// configTemplate is the documented default config written on first launch.
const configTemplate = `# zurm configuration
# Location: ~/.config/zurm/config.toml

[font]
family = "JetBrains Mono"
size   = 15
# file = "/Users/you/Library/Fonts/JetBrainsMonoNerdFont-Regular.ttf"  # overrides embedded font

[window]
columns = 120
rows    = 35
padding = 4    # pixels inside each pane edge

[shell]
program = ""   # empty = read from $SHELL, fallback /bin/zsh
args    = ["-l"]

[scrollback]
lines = 10000

[scroll]
wheel_lines_per_tick = 3   # lines scrolled per mouse wheel tick

[performance]
tps = 30   # Ebitengine tick rate (Update calls/sec); lower = less idle CPU

[input]
double_click_ms = 300   # max ms between clicks to register as double-click
cursor_blink    = false # true = blinking cursor at 530 ms; false = steady

[tabs]
max_width_chars = 24   # maximum tab label width in character cells
new_tab_dir     = "cwd"   # "cwd" = inherit active tab's directory; "home" = $HOME

[panes]
divider_width_pixels = 1   # pixel width of the border between panes

[keyboard]
left_option_as_meta = true   # left Option sends ESC-prefix (word delete, etc.)
                              # right Option still composes macOS characters
repeat_delay_ms     = 500    # ms before key repeat starts
repeat_interval_ms  = 50     # ms between repeated key events (~20/sec)

[status_bar]
enabled              = true
show_cwd             = true
show_git             = true
show_clock           = false   # forces a redraw every second; enable if you want it
show_process         = true    # foreground process name (polls every 1 s via TIOCGPGRP)
branch_prefix        = ""      # set to " " if you have a Nerd Font patched JetBrains Mono
segment_separator    = " · "   # separator drawn between status bar segments
separator_height_px  = 1       # height of top border line (0 = hidden)
padding_px           = 4       # top padding above text (visual spacing from content)

[help]
enabled       = true
context_menu  = true    # right-click opens context menu
                        # set false if your PTY apps use right-click
close_confirm = true    # ask before closing a tab or pane

[session]
enabled           = true   # save/restore session on quit/launch
restore_on_launch = true   # reopen tabs with last CWDs and titles
auto_save         = false  # automatically save session on quit (set true to enable)

[file_explorer]
enabled   = true
side      = "left"   # "left" or "right"
width_pct = 35       # panel width as percent of screen width

[blocks]
enabled       = false   # render OSC 133 command blocks; toggle at runtime with Cmd+B (requires shell hooks)
show_duration = true    # show elapsed execution time (time from Enter to prompt return)
padding       = 2       # px from cell top to top border (ascender gap zone; keep ≤ 4)
gap           = 4       # px from cell bottom to bottom border (descender zone; keep ≤ 6)
                        # visible gap between consecutive blocks = padding + gap
border_width  = 3       # width in pixels of the left accent stripe
border_color  = "#1C1C2E" # border color when exit status is unknown
success_color = "#34D399" # border color for exit code 0
fail_color    = "#F87171" # border color for non-zero exit codes
bg_color      = ""        # optional hex background tint (empty = none)
bg_alpha      = 0.0       # opacity of background tint (0.0–1.0)

[theme]
name = ""   # theme file name without .toml (e.g. "dark", "light"); empty = no theme

[colors]
background = "#0F0F18"
foreground = "#E8E8F0"
cursor     = "#A855F7"
border     = "#1C1C2E"
separator  = "#555570"   # shared separator line color for tab bar and status bar

# Purple-accent dark 16-color ANSI palette
black          = "#555570"
red            = "#F87171"
green          = "#34D399"
yellow         = "#F59E0B"
blue           = "#7C3AED"
magenta        = "#C084FC"
cyan           = "#67E8F9"
white          = "#8888A8"
bright_black   = "#555570"
bright_red     = "#F87171"
bright_green   = "#34D399"
bright_yellow  = "#F59E0B"
bright_blue    = "#A855F7"
bright_magenta = "#C084FC"
bright_cyan    = "#67E8F9"
bright_white   = "#E8E8F0"
`

type FontConfig struct {
	Family string  `toml:"family"`
	Size   float64 `toml:"size"`
	File   string  `toml:"file"` // path to a TTF/OTF; overrides embedded JetBrains Mono
}

type WindowConfig struct {
	Columns int `toml:"columns"`
	Rows    int `toml:"rows"`
	Padding int `toml:"padding"`
}

type ColorConfig struct {
	Background    string `toml:"background"`
	Foreground    string `toml:"foreground"`
	Cursor        string `toml:"cursor"`
	Border        string `toml:"border"`
	Separator     string `toml:"separator"`
	Black         string `toml:"black"`
	Red           string `toml:"red"`
	Green         string `toml:"green"`
	Yellow        string `toml:"yellow"`
	Blue          string `toml:"blue"`
	Magenta       string `toml:"magenta"`
	Cyan          string `toml:"cyan"`
	White         string `toml:"white"`
	BrightBlack   string `toml:"bright_black"`
	BrightRed     string `toml:"bright_red"`
	BrightGreen   string `toml:"bright_green"`
	BrightYellow  string `toml:"bright_yellow"`
	BrightBlue    string `toml:"bright_blue"`
	BrightMagenta string `toml:"bright_magenta"`
	BrightCyan    string `toml:"bright_cyan"`
	BrightWhite   string `toml:"bright_white"`
}

type ShellConfig struct {
	Program string   `toml:"program"`
	Args    []string `toml:"args"`
}

type ScrollbackConfig struct {
	Lines int `toml:"lines"`
}

type StatusBarConfig struct {
	Enabled            bool   `toml:"enabled"`
	ShowGit            bool   `toml:"show_git"`
	ShowCwd            bool   `toml:"show_cwd"`
	ShowClock          bool   `toml:"show_clock"`
	ShowProcess        bool   `toml:"show_process"`
	BranchPrefix       string `toml:"branch_prefix"`         // e.g. " " with a Nerd Font
	SegmentSeparator   string `toml:"segment_separator"`     // e.g. " · " or " | "
	SeparatorHeightPx  int    `toml:"separator_height_px"`   // height of top border line
	PaddingPx          int    `toml:"padding_px"`            // top padding above text
}

type KeyboardConfig struct {
	// LeftOptionAsMeta sends ESC-prefixed sequences for left Option key combos
	// (e.g. Opt+Backspace → word delete, Opt+← → word left).
	// Right Option continues to produce composed macOS characters (ð, ™, etc.).
	// Set to false to treat both Option keys as standard macOS Option.
	LeftOptionAsMeta bool `toml:"left_option_as_meta"`
	// RepeatDelayMs is the initial delay before key repeat begins (milliseconds).
	RepeatDelayMs int `toml:"repeat_delay_ms"`
	// RepeatIntervalMs is the interval between repeated key events (milliseconds).
	RepeatIntervalMs int `toml:"repeat_interval_ms"`
}

type HelpConfig struct {
	// Enabled controls all in-app help surfaces (overlay, context menu).
	Enabled bool `toml:"enabled"`
	// ContextMenu controls whether right-click opens a context menu.
	// Set to false when running apps that use right-click themselves.
	ContextMenu bool `toml:"context_menu"`
	// CloseConfirm shows a confirmation dialog before closing a tab or pane.
	CloseConfirm bool `toml:"close_confirm"`
}

type TabsConfig struct {
	// MaxWidthChars caps each tab label width in character cells.
	MaxWidthChars int `toml:"max_width_chars"`
	// NewTabDir controls where new tabs open: "cwd" inherits the active tab's
	// working directory; "home" always opens in $HOME.
	NewTabDir string `toml:"new_tab_dir"`
}

type PanesConfig struct {
	// DividerWidthPixels is the physical pixel width of the border between panes.
	DividerWidthPixels int `toml:"divider_width_pixels"`
}

type InputConfig struct {
	// DoubleClickMs is the maximum milliseconds between clicks to count as a double-click.
	DoubleClickMs int `toml:"double_click_ms"`
	// CursorBlink enables the 530 ms cursor blink animation.
	CursorBlink bool `toml:"cursor_blink"`
}

type ScrollConfig struct {
	// WheelLinesPerTick is the number of lines scrolled per mouse wheel tick.
	WheelLinesPerTick int `toml:"wheel_lines_per_tick"`
}

type PerformanceConfig struct {
	// TPS is the Ebitengine tick rate (Update calls per second).
	// Lower values reduce idle CPU. 30 is sufficient for a terminal.
	TPS int `toml:"tps"`
}

type SessionConfig struct {
	// Enabled controls whether the session is saved on quit and loaded on launch.
	Enabled bool `toml:"enabled"`
	// RestoreOnLaunch reopens saved tabs with their last CWDs and titles.
	RestoreOnLaunch bool `toml:"restore_on_launch"`
	// AutoSave controls whether sessions are automatically saved on exit.
	// When false, sessions must be saved manually via command palette.
	AutoSave bool `toml:"auto_save"`
}

type FileExplorerConfig struct {
	// Enabled controls whether the Cmd+E file explorer is available.
	Enabled bool `toml:"enabled"`
	// Side controls which side the panel appears on: "left" or "right".
	Side string `toml:"side"`
	// WidthPct is the panel width as a percentage of screen width.
	WidthPct int `toml:"width_pct"`
}

type BellConfig struct {
	// Style controls how BEL (0x07) is presented to the user.
	// "visual" = flash the pane border, "none" = ignore.
	Style string `toml:"style"`
	// Sound plays the macOS system alert sound on BEL.
	Sound bool `toml:"sound"`
	// DurationMs is how long the visual flash lasts in milliseconds.
	DurationMs int `toml:"duration_ms"`
	// Color is the hex color used for the visual flash.
	Color string `toml:"color"`
}

type ThemeConfig struct {
	// Name is the theme filename without .toml extension (e.g. "dark", "light").
	// Empty string means no theme — uses config colors directly.
	Name string `toml:"name"`
}

type BlocksConfig struct {
	// Enabled controls whether OSC 133 command blocks are rendered.
	// Requires shell hooks — run "Install Shell Hooks" from the command palette.
	Enabled bool `toml:"enabled"`
	// ShowDuration controls whether the elapsed time is shown for commands > 1 s.
	ShowDuration bool `toml:"show_duration"`
	// Padding is the number of pixels from the cell top to the top border.
	// The top border sits inside the ascender gap (blank above tallest glyphs).
	// Keep at 2–4 px for clean rendering (JetBrains Mono 2× DPI ascender gap ≈ 4–5 px).
	Padding int `toml:"padding"`
	// Gap is the number of pixels from the cell bottom to the bottom border.
	// The bottom border sits in the descender zone (blank below baseline for most chars).
	// Visible gap between consecutive blocks = Padding + Gap.
	// Keep at 4–6 px for clean rendering (descender zone ≈ 5–6 px).
	Gap int `toml:"gap"`
	// BorderWidth is the width in pixels of the left accent stripe.
	BorderWidth int `toml:"border_width"`
	// BorderColor is the hex border color when exit status is unknown.
	BorderColor string `toml:"border_color"`
	// SuccessColor is the hex border color for exit code 0.
	SuccessColor string `toml:"success_color"`
	// FailColor is the hex border color for non-zero exit codes.
	FailColor string `toml:"fail_color"`
	// BgColor is an optional hex background tint drawn inside the block.
	// Leave empty for no background tint.
	BgColor string `toml:"bg_color"`
	// BgAlpha controls the opacity of the background tint (0.0–1.0).
	BgAlpha float64 `toml:"bg_alpha"`
}

type Config struct {
	Font         FontConfig         `toml:"font"`
	Window       WindowConfig       `toml:"window"`
	Colors       ColorConfig        `toml:"colors"`
	Shell        ShellConfig        `toml:"shell"`
	Scrollback   ScrollbackConfig   `toml:"scrollback"`
	StatusBar    StatusBarConfig    `toml:"status_bar"`
	Keyboard     KeyboardConfig     `toml:"keyboard"`
	Help         HelpConfig         `toml:"help"`
	Tabs         TabsConfig         `toml:"tabs"`
	Panes        PanesConfig        `toml:"panes"`
	Input        InputConfig        `toml:"input"`
	Scroll       ScrollConfig       `toml:"scroll"`
	Performance  PerformanceConfig  `toml:"performance"`
	Session      SessionConfig      `toml:"session"`
	FileExplorer FileExplorerConfig `toml:"file_explorer"`
	Blocks       BlocksConfig       `toml:"blocks"`
	Bell         BellConfig         `toml:"bell"`
	Theme        ThemeConfig        `toml:"theme"`
}

// ConfigDir returns the zurm configuration directory (~/.config/zurm).
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "zurm")
}

// Load reads ~/.config/zurm/config.toml and merges with defaults.
// Missing fields fall back to Defaults.
func Load() (*Config, error) {
	cfg := Defaults

	dir := ConfigDir()
	if dir == "" {
		return &cfg, nil
	}

	path := filepath.Join(dir, "config.toml")

	EnsureBuiltinThemes()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		// First launch: write a fully documented default config so the user
		// can discover all available options without reading the source.
		if mkErr := os.MkdirAll(dir, 0o700); mkErr == nil {
			_ = os.WriteFile(path, []byte(configTemplate), 0o600)
		}
		return &cfg, nil
	}

	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return &cfg, err
	}

	resolveShell(&cfg)
	ApplyTheme(&cfg, meta)
	return &cfg, nil
}

// Reload re-reads config.toml without writing defaults on missing file.
// Returns nil config if the file does not exist.
func Reload() (*Config, error) {
	dir := ConfigDir()
	if dir == "" {
		return nil, fmt.Errorf("config reload: cannot resolve home directory")
	}
	path := filepath.Join(dir, "config.toml")

	cfg := Defaults
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("config reload: %w", err)
	}
	resolveShell(&cfg)
	return &cfg, nil
}

// LoadWithMeta re-reads config.toml and returns the TOML MetaData alongside
// the config. MetaData.IsDefined tracks which keys the user explicitly set,
// needed for theme merge (user-explicit colors override theme colors).
func LoadWithMeta() (*Config, toml.MetaData, error) {
	dir := ConfigDir()
	if dir == "" {
		cfg := Defaults
		return &cfg, toml.MetaData{}, fmt.Errorf("config: cannot resolve home directory")
	}
	path := filepath.Join(dir, "config.toml")

	cfg := Defaults
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return &cfg, meta, fmt.Errorf("config: %w", err)
	}
	resolveShell(&cfg)
	return &cfg, meta, nil
}

// resolveShell fills in Shell.Program from $SHELL if not set.
func resolveShell(cfg *Config) {
	if cfg.Shell.Program == "" {
		cfg.Shell.Program = os.Getenv("SHELL")
		if cfg.Shell.Program == "" {
			cfg.Shell.Program = "/bin/zsh"
		}
	}
}

// ParseHexColor converts a "#rrggbb" string to color.RGBA.
func ParseHexColor(s string) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	r, _ := strconv.ParseUint(s[0:2], 16, 8)
	g, _ := strconv.ParseUint(s[2:4], 16, 8)
	b, _ := strconv.ParseUint(s[4:6], 16, 8)
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}

// Palette returns the 16-color ANSI palette derived from the config.
func (c *Config) Palette() [16]color.RGBA {
	return [16]color.RGBA{
		ParseHexColor(c.Colors.Black),
		ParseHexColor(c.Colors.Red),
		ParseHexColor(c.Colors.Green),
		ParseHexColor(c.Colors.Yellow),
		ParseHexColor(c.Colors.Blue),
		ParseHexColor(c.Colors.Magenta),
		ParseHexColor(c.Colors.Cyan),
		ParseHexColor(c.Colors.White),
		ParseHexColor(c.Colors.BrightBlack),
		ParseHexColor(c.Colors.BrightRed),
		ParseHexColor(c.Colors.BrightGreen),
		ParseHexColor(c.Colors.BrightYellow),
		ParseHexColor(c.Colors.BrightBlue),
		ParseHexColor(c.Colors.BrightMagenta),
		ParseHexColor(c.Colors.BrightCyan),
		ParseHexColor(c.Colors.BrightWhite),
	}
}
