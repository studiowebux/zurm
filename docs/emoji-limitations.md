# Emoji Rendering Limitations

## Summary

Emoji characters will render as boxes (tofu) in zurm. This is a **fundamental limitation** of the underlying libraries and cannot be fixed without major architectural changes.

## Technical Background

### Why Emoji Doesn't Work

1. **Ebiten Limitation**: The Ebiten game engine doesn't support color fonts ([Issue #2649](https://github.com/hajimehoshi/ebiten/issues/2649))
2. **Go Font Library**: The `golang.org/x/image/font` package returns `ErrColoredGlyph` when encountering emoji
3. **No Native Bridge**: Pure Go programs cannot easily access platform-native text rendering (CoreText on macOS, DirectWrite on Windows)

### What We Tried

1. **NotoEmoji-Regular.ttf** (2MB) - Monochrome emoji font, loads but shows boxes
2. **NotoColorEmoji.ttf** (10MB) - Color emoji font, loads but Ebiten can't render color glyphs
3. **Apple Color Emoji.ttc** - TrueType Collection format not supported by Go font libraries
4. **go-text/typesetting** - Has partial SVG support but broken for Noto fonts ([Issue #191](https://github.com/go-text/typesetting/issues/191))

### How Other Terminals Do It

Professional terminal emulators use platform-native text rendering:
- **macOS**: CoreText API (Objective-C/Swift)
- **Windows**: DirectWrite API (C++)
- **Linux**: Pango/Cairo (C)

These native APIs handle color emoji, complex text layout, and font fallback chains properly.

## Workarounds

### For Users
- Use ASCII emoticons: `:-)` `:-P` `:-D`
- Use Unicode symbols that aren't emoji: `✓` `✗` `→` `♥`
- Copy/paste emoji will insert them (they just won't display correctly)

### For Developers
If emoji is critical, options include:
1. Create an emoji sprite atlas (PNG images) for common emoji
2. Use CGO to bridge to native text rendering (loses cross-platform simplicity)
3. Switch to a different framework that supports native text rendering

## Decision

We've removed emoji font support to save 10MB in binary size. The code remains in git history if future Ebiten versions add color font support.

## References

- [Ebiten Issue #2649 - Emoji Support](https://github.com/hajimehoshi/ebiten/issues/2649)
- [go-text/typesetting Issue #191 - Color Emoji](https://github.com/go-text/typesetting/issues/191)
- [OpenType Spec - SVG Table](https://docs.microsoft.com/en-us/typography/opentype/spec/svg)