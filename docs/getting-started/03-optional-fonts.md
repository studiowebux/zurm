---
title: Optional Fonts
description: How to set up fallback fonts for CJK, emoji, and Nerd Font symbols.
---

# Optional Fonts

zurm ships with JetBrains Mono embedded. For CJK characters, emoji, Nerd Font symbols, and braille, add fallback fonts to the `[font]` section in your config.

## Setup

Add the `fallbacks` list to `~/.config/zurm/config.toml`:

```toml
[font]
fallbacks = [
  "/Users/you/Library/Fonts/NotoSansMonoCJKsc-Regular.otf",
  "/Users/you/Library/Fonts/NotoEmoji-Regular.ttf",
  "/Users/you/Library/Fonts/SymbolsNerdFontMono-Regular.ttf",
  "/Users/you/Library/Fonts/NotoSansSymbols2-Regular.ttf",
]
```

Fonts are tried in order when a glyph is missing from the primary font. The first font that contains the glyph is used.

## Download

Run these commands once to install the recommended fallback fonts:

```bash
# CJK (~16 MB) — Chinese, Japanese, Korean characters
curl -sL "https://github.com/googlefonts/noto-cjk/raw/main/Sans/Mono/NotoSansMonoCJKsc-Regular.otf" \
  -o ~/Library/Fonts/NotoSansMonoCJKsc-Regular.otf

# Monochrome emoji (~2 MB)
curl -sL "https://github.com/google/fonts/raw/main/ofl/notoemoji/NotoEmoji%5Bwght%5D.ttf" \
  -o ~/Library/Fonts/NotoEmoji-Regular.ttf

# Nerd Font symbols (~2.4 MB) — powerline glyphs, devicons
curl -sL "https://github.com/ryanoasis/nerd-fonts/releases/download/v3.4.0/NerdFontsSymbolsOnly.tar.xz" \
  | tar xJ -C ~/Library/Fonts/ SymbolsNerdFontMono-Regular.ttf

# Braille + extra symbols (~1.2 MB)
curl -sL "https://github.com/google/fonts/raw/main/ofl/notosanssymbols2/NotoSansSymbols2-Regular.ttf" \
  -o ~/Library/Fonts/NotoSansSymbols2-Regular.ttf
```

## What Each Font Covers

| Font | Glyphs | Size |
|------|--------|------|
| Noto Sans Mono CJK SC | Chinese, Japanese, Korean ideographs | ~16 MB |
| Noto Emoji | Monochrome emoji outlines | ~2 MB |
| Symbols Nerd Font Mono | Powerline, devicons, file type icons | ~2.4 MB |
| Noto Sans Symbols 2 | Braille, mathematical symbols, extra Unicode | ~1.2 MB |

## Limitations

Color emoji (Apple Color Emoji, Twemoji) are not supported. Ebitengine does not render color font formats. Monochrome emoji via Noto Emoji work fine. See [Emoji Limitations](/reference/emoji-limitations.html) for technical details.
