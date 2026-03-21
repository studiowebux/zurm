---
title: Emoji Limitations
description: Technical details on emoji rendering support and limitations.
---

# Emoji Rendering Limitations

## Summary

Full-color emoji rendering is not supported in zurm due to Ebitengine limitations. However, **monochrome emoji outlines** are now available via the font fallback chain using Noto Emoji.

## What Works

- **Monochrome emoji** via `NotoEmoji-Regular.ttf` as a fallback font — renders black-and-white outlines
- **CJK characters** via Noto Sans Mono CJK fallback
- **Nerd Font symbols** (powerline, devicons) via Symbols Nerd Font fallback
- Multiple fallbacks can be chained in config.toml `[font] fallbacks`

## What Doesn't Work

- **Color emoji** (multi-colored glyphs like 😀🎉🔥) — Ebitengine cannot render color fonts
- **Apple Color Emoji** — TrueType Collection format not supported by Go font libraries
- **ZWJ sequences** (👨‍👩‍👧‍👦, 🏳️‍🌈) — require complex text shaping not available in Go
- **Skin tone modifiers** (👋🏽) — modifier codepoints not combined
- **Flag sequences** (🇨🇦, 🇺🇸) — regional indicator pairs not rendered as flags

## Technical Background

1. **Ebiten Limitation**: The Ebiten game engine doesn't support color fonts ([Issue #2649](https://github.com/hajimehoshi/ebiten/issues/2649))
2. **Go Font Library**: The `golang.org/x/image/font` package returns `ErrColoredGlyph` when encountering color glyphs
3. **No Native Bridge**: Pure Go programs cannot easily access platform-native text rendering (CoreText on macOS, DirectWrite on Windows)

## Workarounds

- Use **Nerd Font** symbols for icons in your prompt — they render perfectly as single-codepoint glyphs
- Use monochrome emoji (outlined/black-and-white) via NotoEmoji fallback font
- For programs that display emoji (e.g., `npm`, `yarn`), the outlines are readable even without color

## Setup

Add fallback fonts to `~/.config/zurm/config.toml`:

```toml
[font]
fallbacks = [
  "~/Library/Fonts/NotoSansMonoCJKsc-Regular.otf",   # CJK
  "~/Library/Fonts/NotoEmoji-Regular.ttf",           # monochrome emoji
  "~/Library/Fonts/SymbolsNerdFontMono-Regular.ttf", # powerline + devicons
]
```

Download commands are in the config template comments.

## References

- [Ebiten Issue #2649 - Emoji Support](https://github.com/hajimehoshi/ebiten/issues/2649)
- [go-text/typesetting Issue #191 - Color Emoji](https://github.com/go-text/typesetting/issues/191)
- [OpenType Spec - SVG Table](https://docs.microsoft.com/en-us/typography/opentype/spec/svg)
