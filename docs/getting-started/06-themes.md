---
title: Themes
description: How to use and create custom color themes for zurm.
---

# Themes

zurm supports external TOML theme files for full color customization.

## Built-in Themes

Two themes ship with zurm and are written to `~/.config/zurm/themes/` on first launch:

- **dark** — purple-accent dark palette (default)
- **light** — high-contrast light palette

Activate a theme in your config:

```toml
[theme]
name = "dark"   # or "light", or any custom theme name
```

Set `name = ""` to disable themes and use `[colors]` directly.

## Creating a Custom Theme

1. Create a TOML file in `~/.config/zurm/themes/`:

```bash
touch ~/.config/zurm/themes/my-theme.toml
```

2. Add a `[colors]` section with any colors you want to override:

```toml
# ~/.config/zurm/themes/my-theme.toml
# Only include colors you want to change — unset colors fall back to defaults.

[colors]
background = "#1A1B26"
foreground = "#C0CAF5"
cursor     = "#F7768E"
border     = "#292E42"
separator  = "#3B4261"

black          = "#15161E"
red            = "#F7768E"
green          = "#9ECE6A"
yellow         = "#E0AF68"
blue           = "#7AA2F7"
magenta        = "#BB9AF7"
cyan           = "#7DCFFF"
white          = "#A9B1D6"
bright_black   = "#414868"
bright_red     = "#F7768E"
bright_green   = "#9ECE6A"
bright_yellow  = "#E0AF68"
bright_blue    = "#7AA2F7"
bright_magenta = "#BB9AF7"
bright_cyan    = "#7DCFFF"
bright_white   = "#C0CAF5"

# Optional: customize markdown viewer colors
md_bold             = "#C0CAF5"
md_heading          = "#7AA2F7"
md_code             = "#9ECE6A"
md_code_border      = "#414868"
md_table_border     = "#3B4261"
md_match_bg         = "#E0AF68"
md_match_current_bg = "#F7768E"
md_badge_bg         = "#E0AF68"
md_badge_fg         = "#1A1B26"
```

3. Activate it:

```toml
[theme]
name = "my-theme"
```

4. Hot-reload with `Cmd+,`.

## Theme + Config Interaction

When a theme is active, colors are resolved in this priority:

1. **User-explicit `[colors]` in config.toml** — highest priority. If you uncomment a color in your config, it always wins.
2. **Theme file** — colors defined in the theme file.
3. **Built-in defaults** — colors not set by either source.

This means you can use a theme as a base and override individual colors:

```toml
[theme]
name = "dark"

[colors]
# Override just the cursor color from the dark theme
cursor = "#FF0000"
```

## Complete Color Reference

All available color fields for themes and `[colors]` config:

### Terminal Colors

| Field | Description | Default |
|-------|-------------|---------|
| `background` | Terminal background | `#0F0F18` |
| `foreground` | Terminal foreground text | `#E8E8F0` |
| `cursor` | Cursor color | `#A855F7` |
| `border` | UI borders (panes, panels) | `#1C1C2E` |
| `separator` | Tab bar and status bar separator line | `#555570` |

### 16-Color ANSI Palette

| Field | Default |
|-------|---------|
| `black` | `#555570` |
| `red` | `#F87171` |
| `green` | `#34D399` |
| `yellow` | `#F59E0B` |
| `blue` | `#7C3AED` |
| `magenta` | `#C084FC` |
| `cyan` | `#67E8F9` |
| `white` | `#8888A8` |
| `bright_black` | `#555570` |
| `bright_red` | `#F87171` |
| `bright_green` | `#34D399` |
| `bright_yellow` | `#F59E0B` |
| `bright_blue` | `#A855F7` |
| `bright_magenta` | `#C084FC` |
| `bright_cyan` | `#67E8F9` |
| `bright_white` | `#E8E8F0` |

### Markdown Viewer Colors

| Field | Description | Default |
|-------|-------------|---------|
| `md_bold` | Bold text | `#FFFFFF` |
| `md_heading` | Heading text (# ## ###) | `#FFFFFF` |
| `md_code` | Code blocks and inline code | `#A0D080` |
| `md_code_border` | Border around code blocks | `#606060` |
| `md_table_border` | Table grid lines | `#505050` |
| `md_match_bg` | Search match highlight | `#808000` |
| `md_match_current_bg` | Current search match | `#FFCC00` |
| `md_badge_bg` | Badge background (line count) | `#FFCC00` |
| `md_badge_fg` | Badge text color | `#000000` |

## Tips

- All colors are `#RRGGBB` hex format
- You don't need to include every color in a theme — only the ones you want to change
- Copy `~/.config/zurm/themes/dark.toml` as a starting point for custom themes
- Use `Cmd+,` to preview changes instantly
