# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.2] - 2026-03-05

### Fixed

- Typing after trackpad scroll not registering — sub-pixel momentum deltas blocked keyboard input
- First keystrokes lost after Cmd+Tab — stale prevKeys from blur caused missed edge detection
- Long line overflow overlapping at beginning of line — terminal/PTY not resized after ComputeRects in tab creation and session restore
- Non-ASCII paste in tab rename — added UTF-8 validation for clipboard data

### Changed

- Info.plist updated with full macOS app metadata (category, dark mode, architecture, copyright)

## [0.3.1] - 2026-03-05

### Fixed

- Cmd+D / Cmd+Shift+D keybinding ordering — shift-specific case now listed first
- Selection persists as white box after scrolling — cleared on all scroll paths
- Backspace key repeat speed too fast — configurable via `repeat_delay_ms` and `repeat_interval_ms`
- Scrolling up jumps to bottom on new output — viewport pinned when scrolled back
- Multiline copy adds unwanted newlines — soft-wrap tracking prevents spurious line breaks

### Added

- Configurable key repeat: `[keyboard]` section with `repeat_delay_ms` (default 500) and `repeat_interval_ms` (default 50)

## [0.3.0] - 2026-03-02

### Added

- Alt+Backspace word deletion in all text inputs (tab rename, search, palette, file explorer)
- Manual session save with configurable auto-save (defaults to false to prevent accidental overwrites)
- "Save Session" command in command palette for explicit session snapshots
- File explorer search/filter with Telescope-style filtering (press "/" to search)
- Pane layout persistence — saves and restores complete pane tree structure with splits and ratios
- macOS .app bundle detection — automatically uses home directory instead of bundle internals
- Text input utilities with continuous backspace support and proper key repeat
- File explorer `.` (current directory) entry — pressing Enter inserts the current path
- File explorer `..` (parent directory) entry — always visible as first item for easy navigation

### Changed

- Session auto-save now defaults to false (set `session.auto_save = true` in config to enable)
- File explorer shows only matching entries when searching (like Telescope)
- Each pane's working directory is now saved and restored in sessions
- File explorer search now filters current directory only (non-recursive) for better performance
- File explorer Enter key behavior — directories navigate into them, `.` inserts current path
- File explorer hint bar shows `../` for parent navigation instead of confusing `h/⌫`

### Fixed

- Initial directory when launching from .app bundle now correctly defaults to home
- File explorer search navigation and scrolling when filtering
- File explorer search rendering now shows a flat filtered list
- Prompt arrow and UI symbols incorrectly treated as emoji characters

### Removed

- Emoji font support (saves 10MB binary size) — Ebiten doesn't support color fonts (see docs/emoji-limitations.md)

## [0.2.1] - 2026-03-03

### Fixed

- Tab stops not rebuilt on terminal resize — tabs beyond the original column count jumped to the last column, causing `ls` and other tab-using output to wrap incorrectly after resizing the window

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

[Unreleased]: https://github.com/studiowebux/zurm/compare/v0.3.2...HEAD
[0.3.2]: https://github.com/studiowebux/zurm/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/studiowebux/zurm/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/studiowebux/zurm/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/studiowebux/zurm/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/studiowebux/zurm/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/studiowebux/zurm/releases/tag/v0.1.0
