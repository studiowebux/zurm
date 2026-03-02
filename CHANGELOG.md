# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-03-03

### Added

- One-shot screenshot capture to PNG (Cmd+Shift+S) — saves to ~/Pictures/zurm-screenshots/
- Screen recording via FFMPEG pipe to MP4 (Cmd+Shift+.) — saves to ~/Movies/zurm-recordings/
- Recording status bar indicator with blinking dot, elapsed time, and output file size
- Quit confirmation dialog on Cmd+Q
- Makefile with build, bundle, install, and clean targets
- macOS .app bundle packaging (Info.plist, icon, install to /Applications)
- New tab defaults to $HOME on first launch

## [0.1.0] - 2026-03-02

### Added

- GPU-rendered terminal using Ebitengine with offscreen compositing at native HiDPI resolution
- Full PTY integration with xterm-256color, truecolor, and configurable 16-color ANSI palette
- Pane splits — binary tree layout engine supporting horizontal and vertical splits at any depth
- Each pane owns an independent PTY, shell, buffer, and cursor
- Multi-tab workspace with OSC 0/2 title updates, user rename via double-click or Cmd+Shift+R
- Tab pins — bind tabs to home-row slots (a–l) and jump instantly with Ctrl+Space
- Tab switcher — fuzzy overlay to switch tabs by name (Cmd+Shift+T)
- Text selection — click, double-click (word), triple-click (line), drag
- Copy/paste via Cmd+C/Cmd+V with bracketed paste support
- Configurable scrollback buffer with Shift+PgUp/Down navigation
- In-buffer incremental search across scrollback and primary screen (Cmd+F)
- TUI compatibility — X10 and SGR mouse reporting, alternate screen, focus events, Kitty keyboard protocol
- Mouse drag selection in PTY mouse mode (e.g. Helix/Neovim)
- Status bar showing live CWD, git branch, foreground process name, scroll offset, and zoom mode
- Zoom pane — temporarily fullscreen the focused pane (Cmd+Z)
- Command palette — searchable list of all commands and shortcuts (Cmd+P)
- Help overlay — all keybindings grouped by category (Cmd+/)
- Right-click context menu for copy/paste, pane management, scroll, and more
- Session persistence — saves tab CWDs, titles, and pin slots on quit; restores on relaunch
- Optional close confirmation dialog before closing a tab or pane
- File explorer sidebar — tree browser with create, rename, delete, copy path, and Finder reveal (Cmd+E)
- Command blocks — OSC 133 prompt/output tracking with hover-to-copy
- Emoji and CJK wide-char support in tab names with correct column widths
- Left Option as Meta key (configurable — right Option retains macOS character composition)
- Word delete with Opt+Backspace
- New tab inherits CWD of the active pane (configurable)
- Config auto-bootstrap — writes a fully documented `~/.config/zurm/config.toml` on first launch with all keys and defaults
- Install shell hooks command in palette — writes OSC 133 zsh integration to `.zshrc`
- Blit optimization — partial screen redraw on scroll; only dirty regions re-render
- Blocks layer decoupled from main DrawPane — hover state managed independently to avoid per-frame lock contention
- Block timer sourced from OSC C (command enter) for accurate execution duration
- Block background tint uses premultiplied alpha for correct Ebitengine blending

[Unreleased]: https://github.com/studiowebux/zurm/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/studiowebux/zurm/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/studiowebux/zurm/releases/tag/v0.1.0
