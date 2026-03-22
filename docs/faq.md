---
title: FAQ
description: Frequently asked questions about zurm.
---

# FAQ

## How do I customize colors?

Two options:

1. **Use a theme:** Set `[theme] name = "dark"` (or `"light"`, or a custom theme name) in `~/.config/zurm/config.toml`. See the [Themes guide](getting-started/06-themes.md).
2. **Edit colors directly:** Uncomment and modify values in the `[colors]` section of your config. Colors set here override the active theme.

Hot-reload with `Cmd+,` to preview changes instantly.

## Why don't emojis render in color?

Ebitengine (the GPU engine zurm uses) does not support color font rendering. Monochrome emoji outlines are available via the font fallback chain. See [Emoji Limitations](reference/emoji-limitations.md) for details and setup instructions.

## How do I change the font?

Edit `~/.config/zurm/config.toml`:

```toml
[font]
family = "JetBrains Mono"
size   = 15
file   = "/Users/you/Library/Fonts/MyFont-Regular.ttf"  # custom TTF/OTF
```

The `file` field overrides the embedded JetBrains Mono. See the [Optional Fonts guide](getting-started/03-optional-fonts.md) for fallback font setup.

## Can I use zurm with tmux?

Yes, tmux works inside zurm. However, zurm has built-in tabs, panes, and splits — so tmux is redundant for most use cases. If you use tmux for session persistence, consider zurm's [Server Mode](getting-started/04-server-mode.md) instead.

## How do I install shell hooks?

Open the command palette (`Cmd+P`), search "Install shell hooks", and press Enter. This adds OSC 133 sequences to your shell prompt, enabling command blocks (`Cmd+B`), command duration display, and exit status indicators.

## What do the colored dots on tabs mean?

A purple dot on a background tab indicates **activity** — the terminal in that tab produced new output since you last looked at it. The dot disappears when you switch to that tab.

A bell icon appears briefly when a terminal sends a BEL character.

## How do I back up my config?

Your config and data live in `~/.config/zurm/`:

```
~/.config/zurm/
├── config.toml       # main configuration
├── themes/           # theme files (dark.toml, light.toml, custom)
├── session.json      # saved tab/pane layout
├── vault.enc         # encrypted command history (if vault enabled)
└── vault.key         # vault encryption key
```

Back up this entire directory to preserve your settings.

## Does zurm support ligatures?

No. Ligature rendering requires complex text shaping (OpenType GSUB table processing) which is not currently implemented. Each glyph is rendered independently in the cell grid.

## How do I use server mode?

`Cmd+Shift+B` creates a server-backed tab. The terminal session persists even when zurm is closed. See the [Server Mode guide](getting-started/04-server-mode.md) for full setup instructions.

## How do I report a bug?

Open an issue at [github.com/studiowebux/zurm/issues](https://github.com/studiowebux/zurm/issues). Include:
- zurm version (`zurm --version`)
- macOS version
- Steps to reproduce
- Terminal output or screenshots if applicable

## Where is the config file?

`~/.config/zurm/config.toml` — created automatically on first launch with all defaults and comments.

## How do I reset to default config?

Delete or rename `~/.config/zurm/config.toml` and relaunch zurm. A fresh config with all defaults will be written.
