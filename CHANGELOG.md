# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Cmd+J tab search overlay with fuzzy filtering by tab name or CWD
- Tab search accessible via command palette ("Tab Search")
- Session config template now includes `auto_save` option for discoverability

## [0.7.0] - 2026-03-06

### Added

- Resize pane splits by dragging the divider with the mouse (4px hit zone)
- Resize pane splits via Cmd+Option+Arrow keys (5% step per press)
- Mouse drag to reorder tabs in the tab bar (8px threshold to distinguish from click)
- Rename focused pane via context menu (Panes > Rename Pane) or command palette
- Double-click on pane header to rename (mirrors tab rename UX)
- Custom pane names persist across session save/restore

## [0.6.0] - 2026-03-06

### Changed

- Info.plist version management — replaced hardcoded version with `__VERSION__`/`__BUILD__` placeholders injected at build time
- Distribution format — replaced ZIP with DMG via `hdiutil create`
- Makefile build flags — added `-trimpath -ldflags="-s -w"` to match CI

### Added

- Ad-hoc code signing (`codesign --sign -`) in both Makefile and CI — Gatekeeper no longer blocks the app
- `make dmg` target for local DMG creation
- `LSMultipleInstancesProhibited` in Info.plist to allow `open -n zurm.app`

## [0.5.3] - 2026-03-06

### Fixed

- Screen recording plays back at extreme speed — frame capture now runs before the dirty-flag gate so idle frames are duplicated at 30fps
- Clear command (CSI 3J) leaves residual line in scrollback — mode 3 now properly calls ClearScrollback() instead of sharing the mode 2 code path
- App appears unresponsive after macOS sleep/lock — force full redraw on focus regain across all tabs and panes

## [0.5.2] - 2026-03-06

### Fixed

- FFMPEG recording unplayable — dimension rounding mismatch caused all frames to be silently dropped; replaced with ffmpeg crop filter and synced dims before Start()
- Status bar flash message overlapping git branch/process segments — flash now takes full ownership of the left side
- Cmd+Shift+S screenshot not firing when no PTY activity — missing screenDirty flag in keyboard path
- Panic in EraseInDisplay after zoom + TUI exit — Resize() now also resizes altWrapped; DisableAltScreen clamps restored cursor

## [0.5.1] - 2026-03-06

### Added

- Pane name labels — each pane displays its name in the header bar
- Scroll position indicator in pane header when scrolled back in history

## [0.5.0] - 2026-03-05

### Added

- Clickable URLs — Cmd+click opens URLs detected in terminal output
- Selection auto-scroll — dragging selection past viewport edges scrolls automatically
- Absolute buffer coordinates for selection — selections survive scrolling without drifting

## [0.4.1] - 2026-03-05

### Added

- Display app version in the status bar (right side, before the help button)

### Fixed

- Resizing the window while a pane is zoomed no longer resets it to split dimensions
- PTY size mismatch after window resize in zoom mode

## [0.4.0] - 2026-03-05

### Added

- Reorder tabs with Cmd+Shift+Left/Right
- Focus history navigation with Cmd+; — jump back to previously viewed tab or pane (stack of 50 entries)
- Tab activity indicator — purple dot on background tabs with unseen PTY output
- Window close button (red X) now shows quit confirmation modal, matching Cmd+Q behavior

### Fixed

- Tab bar artifacts when reordering tabs — full clear before redraw
- Tabs losing displayed name after reorder — stable default title assigned at creation
- Initial tab now seeded into focus history on startup

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

[Unreleased]: https://github.com/studiowebux/zurm/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/studiowebux/zurm/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/studiowebux/zurm/compare/v0.5.3...v0.6.0
[0.5.3]: https://github.com/studiowebux/zurm/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/studiowebux/zurm/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/studiowebux/zurm/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/studiowebux/zurm/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/studiowebux/zurm/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/studiowebux/zurm/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/studiowebux/zurm/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/studiowebux/zurm/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/studiowebux/zurm/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/studiowebux/zurm/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/studiowebux/zurm/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/studiowebux/zurm/releases/tag/v0.1.0
