---
title: Configuration
description: Full reference for zurm's TOML configuration file.
---

# Configuration

Config file: `~/.config/zurm/config.toml` — created automatically on first launch with all defaults.

Hot-reload with `Cmd+,` — no restart needed.

## Full Reference

```toml
[font]
family = "JetBrains Mono"    # font family name (informational)
size   = 15                   # font size in points
# file = "/path/to/Font.ttf" # custom TTF/OTF; overrides embedded JetBrains Mono
# fallbacks = [...]           # see Optional Fonts page

[window]
columns = 120    # initial terminal width in character columns
rows    = 35     # initial terminal height in character rows
padding = 4      # pixels inside each pane edge

[shell]
program = ""       # shell binary; empty = read from $SHELL, fallback /bin/zsh
args    = ["-l"]   # arguments passed to the shell

[scrollback]
lines = 10000   # scrollback buffer size per pane

[scroll]
wheel_lines_per_tick = 3   # lines scrolled per mouse wheel tick

[performance]
tps        = 30     # Ebitengine tick rate (Update calls/sec); lower = less idle CPU
auto_idle  = true   # reduce TPS when unfocused to save CPU; false = keep rendering
pprof      = false  # enable net/http/pprof endpoint on localhost for profiling
pprof_port = 6060   # port for pprof HTTP server (localhost only)

[input]
double_click_ms = 300     # max ms between clicks to register as double-click
cursor_blink    = false   # true = blinking cursor at 530 ms; false = steady

[tabs]
max_width_chars = 24      # maximum tab label width in character cells
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
show_cwd             = true    # current working directory
show_git             = true    # git branch name
show_clock           = false   # forces a redraw every second
show_process         = true    # foreground process name (polls every 1s)
show_commit          = true    # short commit hash next to branch name
show_dirty           = true    # modified (~N) and staged (+N) file counts
show_ahead_behind    = true    # commits ahead/behind upstream (N^ Nv)
branch_prefix        = ""      # set to " " with a Nerd Font
segment_separator    = " · "   # separator between status bar segments
separator_height_px  = 1       # height of top border line (0 = hidden)
padding_px           = 4       # top padding above text

[help]
enabled       = true    # all in-app help surfaces
context_menu  = true    # right-click opens context menu (set false for PTY apps that use right-click)
close_confirm = true    # ask before closing a tab or pane

[session]
enabled           = true    # save/restore session on quit/launch
restore_on_launch = true    # reopen tabs with last CWDs and titles
auto_save         = false   # automatically save session on quit

[file_explorer]
enabled   = true       # file explorer sidebar
side      = "left"     # "left" or "right"
width_pct = 35         # panel width as percent of screen width

[blocks]
enabled       = false        # OSC 133 command blocks; toggle at runtime with Cmd+B (requires shell hooks)
show_duration = true         # show elapsed execution time
show_border   = true         # draw 4-sided border + bg tint (false = badges + hover buttons only)
border_width  = 3            # width in pixels of the left accent stripe
border_color  = "#1C1C2E"    # border color when exit status is unknown
success_color = "#34D399"    # border color for exit code 0
fail_color    = "#F87171"    # border color for non-zero exit codes
bg_color      = ""           # optional hex background tint (empty = none)
bg_alpha      = 0.0          # opacity of background tint (0.0-1.0)
max_history   = 1000         # max completed blocks retained per pane (0 = unlimited)

[bell]
style       = "visual"   # "visual" = screen flash; "none" = disabled
sound       = true        # play system beep sound (NSBeep)
duration_ms = 150         # flash duration in ms
color       = "#F59E0B"   # hex color for the visual flash

[vault]
enabled          = false        # encrypted local command history + ghost suggestions
ghost_text       = true         # show inline ghost suggestions while typing; false = keep history without suggestions
history_path     = ""           # path to zsh history file; empty = ~/.zsh_history
vault_path       = ""           # encrypted vault file path; empty = ~/.config/zurm/vault.enc
ignore_prefix    = " "          # commands starting with a space are never stored (type " ssh ..." to hide it)
suggestion_color = "#555570"    # ghost text color for inline suggestions
max_entries      = 0            # cap on stored commands; 0 = unlimited
sync_interval    = 0            # seconds between zsh history re-imports; 0 = import once at startup
# Encryption key: ~/.config/zurm/vault.key (auto-generated, 32-byte AES-256 key, mode 0600)
# Right arrow accepts the ghost suggestion; space-prefix keeps commands private.

[server]
address = ""      # Unix socket path of zurm-server; empty = ~/.config/zurm/server.sock
binary  = ""      # path to zurm-server binary; empty = look next to zurm binary, then PATH
# Use Cmd+Shift+B to open a server-backed tab on demand. zurm-server is
# auto-started when needed — no manual launch or enabled flag required.

[theme]
name = ""   # theme filename without .toml (e.g. "dark"); empty = no theme
            # themes live in ~/.config/zurm/themes/

[colors]
background = "#0F0F18"
foreground = "#E8E8F0"
cursor     = "#A855F7"
border     = "#1C1C2E"
separator  = "#555570"   # shared separator line color for tab bar and status bar

# 16-color ANSI palette
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
```

## Shell Hooks

Command blocks (OSC 133) require shell hooks. Install via the command palette: `Cmd+P` then search "Install shell hooks".

## Themes

External TOML theme files live in `~/.config/zurm/themes/`. Set `[theme] name` to the filename without `.toml`. A theme overrides any `[colors]` values it defines.

## Keybindings

All keybindings are discoverable in-app via `Cmd+/` (help overlay) or `Cmd+P` (command palette).
