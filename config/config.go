package config

import (
	"fmt"
	"image/color"
	"log"
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
#
# Font fallback chain — tried in order for missing glyphs.
# Use "fallbacks" (list) for multiple fonts, or "fallback" (string) for a single one.
# fallbacks = [
#   "/Users/you/Library/Fonts/NotoSansMonoCJKsc-Regular.otf",   # CJK characters
#   "/Users/you/Library/Fonts/NotoEmoji-Regular.ttf",           # monochrome emoji
#   "/Users/you/Library/Fonts/SymbolsNerdFontMono-Regular.ttf", # powerline + devicons
#   "/Users/you/Library/Fonts/NotoSansSymbols2-Regular.ttf",    # braille + extra symbols
# ]
#
# Download fallback fonts (one-time):
#   # CJK (~16 MB):
#   curl -sL "https://github.com/googlefonts/noto-cjk/raw/main/Sans/Mono/NotoSansMonoCJKsc-Regular.otf" \
#     -o ~/Library/Fonts/NotoSansMonoCJKsc-Regular.otf
#   # Monochrome emoji (~2 MB):
#   curl -sL "https://github.com/google/fonts/raw/main/ofl/notoemoji/NotoEmoji%5Bwght%5D.ttf" \
#     -o ~/Library/Fonts/NotoEmoji-Regular.ttf
#   # Nerd Font symbols (~2.4 MB) — check https://www.nerdfonts.com for latest version:
#   curl -sL "https://github.com/ryanoasis/nerd-fonts/releases/download/v3.4.0/NerdFontsSymbolsOnly.tar.xz" \
#     | tar xJ -C ~/Library/Fonts/ SymbolsNerdFontMono-Regular.ttf
#   # Braille + extra symbols (~1.2 MB):
#   curl -sL "https://github.com/google/fonts/raw/main/ofl/notosanssymbols2/NotoSansSymbols2-Regular.ttf" \
#     -o ~/Library/Fonts/NotoSansSymbols2-Regular.ttf

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
tps        = 30     # Ebitengine tick rate (Update calls/sec); lower = less idle CPU
auto_idle  = true   # reduce TPS when unfocused to save CPU; false = keep rendering
pprof      = false  # enable net/http/pprof endpoint on localhost for profiling
pprof_port = 6060   # port for pprof HTTP server (localhost only)

[input]
double_click_ms = 300   # max ms between clicks to register as double-click
cursor_blink    = false # true = blinking cursor at 530 ms; false = steady

[tabs]
max_width_chars = 24   # maximum tab label width in character cells
new_tab_dir     = "cwd"   # "cwd" = inherit active tab's directory; "home" = $HOME

[tabs.hover]
enabled  = true    # show minimap popover when hovering a background tab
delay_ms = 300     # ms before popover appears
width    = 320     # popover width in logical pixels
height   = 200     # popover height in logical pixels

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
show_commit          = true    # short commit hash next to branch name
show_dirty           = true    # modified (~N) and staged (+N) file counts
show_ahead_behind    = true    # commits ahead/behind upstream (N^ Nv)
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
enabled       = false    # render OSC 133 command blocks; toggle at runtime with Cmd+B (requires shell hooks)
show_duration = true     # show elapsed execution time (time from Enter to prompt return)
show_border   = true     # draw 4-sided border + bg tint (false = badges + hover buttons only)
border_width  = 3        # width in pixels of the left accent stripe
border_color  = "#1C1C2E" # border color when exit status is unknown
success_color = "#34D399" # border color for exit code 0
fail_color    = "#F87171" # border color for non-zero exit codes
bg_color      = ""        # optional hex background tint (empty = none)
bg_alpha      = 0.0       # opacity of background tint (0.0-1.0)
max_history   = 1000     # max completed blocks retained per pane (0 = unlimited)

[bell]
style       = "visual"  # "visual" = orange cell flash; "none" = no visual
sound       = true       # play NSBeep on bell
duration_ms = 150        # flash duration in milliseconds
color       = "#F59E0B"  # flash color (orange by default)

[vault]
enabled          = false  # enable command vault (encrypted local command history + ghost suggestions)
ghost_text       = true   # show inline ghost suggestions from vault history
history_path     = ""     # path to shell history file; empty = auto-detect (~/.zsh_history, etc.)
vault_path       = ""     # path to encrypted vault file; empty = ~/.config/zurm/vault.enc
ignore_prefix    = " "    # commands starting with this prefix are never stored (matches zsh HIST_IGNORE_SPACE)
suggestion_color = "#555570"  # ghost text color for inline suggestions

[server]
# address = "http://localhost:7777"  # zurm-server address for persistent PTY sessions
# binary  = "./zurm-server"          # path to zurm-server binary (auto-launch)

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

# Markdown viewer colors (Cmd+M reader mode)
md_bold             = "#FFFFFF"   # bold text
md_heading          = "#FFFFFF"   # heading text (# ## ###)
md_code             = "#A0D080"   # inline code and code block text
md_code_border      = "#606060"   # border around code blocks
md_table_border     = "#505050"   # table grid lines
md_match_bg         = "#808000"   # search match highlight background
md_match_current_bg = "#FFCC00"   # current search match highlight
md_badge_bg         = "#FFCC00"   # badge/label background (e.g. line count)
md_badge_fg         = "#000000"   # badge/label text color
`

type FontConfig struct {
	Family    string   `toml:"family"`
	Size      float64  `toml:"size"`
	File      string   `toml:"file"`      // path to a TTF/OTF; overrides embedded JetBrains Mono
	Fallback  string   `toml:"fallback"`  // single fallback font (backward compat; use fallbacks instead)
	Fallbacks []string `toml:"fallbacks"` // ordered list of fallback font paths (CJK, emoji, nerd font, etc.)
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

	// Markdown viewer colors (used by the llms.txt / markdown reader overlay).
	MdBold        string `toml:"md_bold"`
	MdHeading     string `toml:"md_heading"`
	MdCode        string `toml:"md_code"`
	MdCodeBorder  string `toml:"md_code_border"`
	MdTableBorder string `toml:"md_table_border"`
	MdMatchBg     string `toml:"md_match_bg"`
	MdMatchCurBg  string `toml:"md_match_current_bg"`
	MdBadgeBg     string `toml:"md_badge_bg"`
	MdBadgeFg     string `toml:"md_badge_fg"`
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
	ShowCommit         bool   `toml:"show_commit"`           // short commit hash
	ShowDirty          bool   `toml:"show_dirty"`            // modified/staged file counts
	ShowAheadBehind    bool   `toml:"show_ahead_behind"`     // commits ahead/behind upstream
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

type TabHoverConfig struct {
	// Enabled controls whether hovering a background tab shows a minimap popover.
	Enabled bool `toml:"enabled"`
	// DelayMs is the hover delay before the popover appears.
	DelayMs int `toml:"delay_ms"`
	// Width is the popover width in logical pixels.
	Width int `toml:"width"`
	// Height is the popover height in logical pixels.
	Height int `toml:"height"`
}

type TabsConfig struct {
	// MaxWidthChars caps each tab label width in character cells.
	MaxWidthChars int `toml:"max_width_chars"`
	// NewTabDir controls where new tabs open: "cwd" inherits the active tab's
	// working directory; "home" always opens in $HOME.
	NewTabDir string `toml:"new_tab_dir"`
	// Hover configures the tab hover minimap popover.
	Hover TabHoverConfig `toml:"hover"`
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
	// AutoIdle reduces TPS and pauses PTY readers when the window loses focus
	// for more than 5 seconds. Saves CPU/memory when zurm is in the background.
	// Disable if you need zurm to keep rendering while unfocused.
	AutoIdle bool `toml:"auto_idle"`
	// Pprof enables the net/http/pprof endpoint on localhost for runtime
	// memory and goroutine profiling. Access via:
	//   go tool pprof http://localhost:<pprof_port>/debug/pprof/heap
	Pprof bool `toml:"pprof"`
	// PprofPort is the localhost port for the pprof HTTP server.
	PprofPort int `toml:"pprof_port"`
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

type VaultConfig struct {
	// Enabled controls whether the command vault is active.
	Enabled bool `toml:"enabled"`
	// GhostText controls whether inline ghost suggestions are drawn while typing.
	// Set to false to keep vault history without showing suggestions.
	GhostText bool `toml:"ghost_text"`
	// HistoryPath is the path to the zsh history file. Empty = ~/.zsh_history.
	HistoryPath string `toml:"history_path"`
	// VaultPath is the path to the encrypted vault file. Empty = ~/.config/zurm/vault.enc.
	VaultPath string `toml:"vault_path"`
	// IgnorePrefix excludes commands starting with this string (default: space).
	IgnorePrefix string `toml:"ignore_prefix"`
	// SuggestionColor is the hex color for ghost text suggestions.
	SuggestionColor string `toml:"suggestion_color"`
	// MaxEntries caps the number of commands stored in the vault. 0 = unlimited.
	MaxEntries int `toml:"max_entries"`
	// SyncIntervalSecs is how often (in seconds) the vault re-imports zsh history.
	// 0 = import once at startup only.
	SyncIntervalSecs int `toml:"sync_interval"`
}

type ServerConfig struct {
	// Address is the Unix socket path of the zurm-server.
	// Empty = ~/.config/zurm/server.sock
	Address string `toml:"address"`
	// Binary is the path to the zurm-server executable.
	// Empty = look in the same directory as the zurm binary, then PATH.
	Binary string `toml:"binary"`
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
	// BorderWidth is the width in pixels of the left accent stripe.
	BorderWidth int `toml:"border_width"`
	// BorderColor is the hex border color when exit status is unknown.
	BorderColor string `toml:"border_color"`
	// SuccessColor is the hex border color for exit code 0.
	SuccessColor string `toml:"success_color"`
	// FailColor is the hex border color for non-zero exit codes.
	FailColor string `toml:"fail_color"`
	// ShowBorder draws the 4-sided border (left stripe + top/right/bottom lines)
	// and background tint. When false, only badges (exit code, duration) and
	// hover copy buttons are rendered.
	ShowBorder bool `toml:"show_border"`
	// BgColor is an optional hex background tint drawn inside the block.
	// Leave empty for no background tint. Only drawn when show_border is true.
	BgColor string `toml:"bg_color"`
	// BgAlpha controls the opacity of the background tint (0.0–1.0).
	BgAlpha float64 `toml:"bg_alpha"`
	// MaxHistory caps the number of completed blocks retained per pane.
	// Oldest blocks are evicted when the limit is exceeded. 0 = unlimited.
	MaxHistory int `toml:"max_history"`
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
	Vault        VaultConfig        `toml:"vault"`
	Server       ServerConfig       `toml:"server"`
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
			if wErr := os.WriteFile(path, []byte(configTemplate), 0o600); wErr != nil {
				log.Printf("config: could not write default config to %s: %v", path, wErr)
			}
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
	raw := s
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		log.Printf("config: invalid hex color %q, falling back to white", raw)
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	r, rErr := strconv.ParseUint(s[0:2], 16, 8)
	g, gErr := strconv.ParseUint(s[2:4], 16, 8)
	b, bErr := strconv.ParseUint(s[4:6], 16, 8)
	if rErr != nil || gErr != nil || bErr != nil {
		log.Printf("config: invalid hex color %q, falling back to white", raw)
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
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
