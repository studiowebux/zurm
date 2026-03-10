# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.4] - 2026-03-09

### Fixed

- Clipboard encoding in .app bundles: set LANG=en_US.UTF-8 at startup so pbcopy/pbpaste handle multi-byte UTF-8 correctly (em dash, CJK, etc. no longer paste as mojibake)

## [1.0.3] - 2026-03-09

### Fixed

- Resize lock starvation during heavy PTY output (concurrent RLock/Lock ordering in pty.go)
- Paste and typing dropped on focus regain (prevKeys reset on focus transition)
- Light theme white-on-white text (bright_white adjusted to #E8E8F0)
- Font fallback not applying on config hot-reload (gate on loaded flag)
- Recorder nil pointer on resize before first recording
- Missing screenDirty on overlay, menu, and confirm state changes (dirty-flag audit)
- Guard resize resume against idle suspension

## [1.0.2] - 2026-03-08

### Fixed

- C1 control byte handler (0x80-0x9F) intercepting UTF-8 continuation bytes mid-sequence, breaking all multi-byte Unicode rendering (icons, arrows, CJK, Nerd Font glyphs)

## [1.0.0] - 2026-03-08

### Fixed

- Parser oscBuf/dcsBuf capped at 4096 bytes to prevent unbounded growth
- CSI params clamped to 65535 (VT spec ceiling) instead of silently overflowing
- Scroll early-return no longer drops simultaneous keystrokes
- closeActiveTab zeroes trailing slice slot to prevent Tab/pane GC leak
- CUU/CUD respect scroll region boundaries (clamp to ScrollTop/ScrollBottom)
- SearchAll searches active screen (alt when active) instead of always primary

### Changed

- Deduplicated copySelection/extractSelectedText into single helper

## [0.20.1] - 2026-03-08

### Fixed

- Zombie process leak: PTY reader now reaps child process on exit
- Screenshot goroutine data race eliminated with channel pattern
- Incorrect double-width rendering for U+2600-U+27BF symbol range removed
- pprof server gracefully skips when port is in use
- resizeBuf no longer falsely clears wide chars on grid grow
- VT parser: stateIgnore byte drop, C1 control handling, stale CSI params
- DECOM origin mode scroll region, DECRC/RCP cursor clamp, ECH wide char overlap
- Primary scroll region preserved across alt screen toggle (no longer lost after TUI exit)
- Alt screen wrapped slice deep-copied instead of aliased
- Gray background on window resize (missing screenDirty flag)
- GPU image leak on resize — old offscreen/blocksLayer/modalLayer now deallocated
- Pane cache leak on tab close — closed pane references now released
- Config reload rollback on failed font load prevents inconsistent state

## [0.20.0] - 2026-03-08

### Added

- Multiple font fallback chain: configure `fallbacks` in config.toml with ordered list of fonts (CJK, emoji, Nerd Font)
- Wide character support: CJK, fullwidth forms, and wide emoji render correctly across 2 terminal columns
- Cell Width field (0=continuation, 1=normal, 2=wide) for precise wide character tracking
- `terminal.RuneWidth()` — single source of truth for character width calculation
- 15 table-driven tests for wide char placement, erase, resize, and width detection

### Fixed

- Erase operations (EraseInLine, EraseInDisplay, InsertChars, DeleteChars) handle wide char boundaries correctly
- Selection copy skips continuation cells — no duplicate characters in clipboard
- Mouse click and drag snap to parent cell when landing on wide char continuation
- Word selection expands correctly across wide characters
- Search highlights span the correct columns for wide characters (colMap pattern)
- URL detection handles wide characters in surrounding text
- Resize fixes truncated wide chars at boundaries and orphaned continuation cells

## [0.19.0] - 2026-03-08

### Added

- llms.txt browser: link following hint mode (f key) — letter badges on visible links, follows llms.txt links inline or opens external URLs in system browser
- llms.txt browser: navigation history — Backspace/H to go back, L to go forward, breadcrumb indicator in title bar
- llms.txt browser: Cmd+Enter sends viewer content to a persistent pane running less
- llms.txt browser: "Send Viewer to Pane" command palette entry
- Browser category in keybindings overlay with all new shortcuts
- Markdown viewer: improved styling for h1/h2/h3 headings, code blocks (green + border), table row borders
- ANSI-styled output for send-to-pane (renders with less -R)
- Precise x/y search match highlighting in markdown viewer

### Fixed

- Search highlights drawn over text making it unreadable — now uses three-layer approach (backgrounds → highlights → text)
- Search highlight misaligned on code blocks (missing +cw offset)
- Panic in mergePaneToTab when source is the last tab

## [0.17.0] - 2026-03-08

### Added

- Markdown viewer: Cmd+F and / to open search bar with match highlighting
- Markdown viewer: n/N to jump between search matches, Enter/Shift+Enter in search mode
- Markdown viewer: match count indicator and "no matches" feedback

### Changed

- Status bar CWD and foreground process polling replaced with event-driven updates — polls only fire when PTY output arrives, throttled to 2s (CWD) and 1s (foreground). Zero polling during idle.

### Fixed

- FFMPEG recorder missing last frames on stop — replaced mutex-guarded writes with buffered channel + dedicated writer goroutine that flushes before closing stdin
- FFMPEG recording playback speed too fast — duplicate frames inserted to fill timing gaps when capture intervals exceed 33ms, keeping playback aligned with wall-clock time
- Explicit output frame rate (-r 30) added to ffmpeg command

## [0.16.2] - 2026-03-08

### Changed

- Markdown parser replaced with goldmark (GFM extension) for robust AST-based rendering
- Table columns are now aligned with computed max-width padding

### Added

- Markdown viewer: mouse wheel scrolling
- Markdown viewer: key repeat on j/k and arrow keys for continuous scrolling
- Markdown viewer: vim motions — gg (top), G (bottom), Ctrl+d (half-page down), Ctrl+u (half-page up)
- Markdown viewer: strikethrough, image, task list checkbox, and table rendering styles

### Fixed

- Markdown viewer close leaving grey screen — added ClearPaneCache on dismiss
- Markdown viewer content capture uses full block output range instead of visible viewport

## [0.16.1] - 2026-03-07

### Fixed

- Unbounded memory growth in command blocks slice — capped with copy+reslice eviction
- Scrollback buffer O(n) shifting replaced with pre-allocated ring buffer
- Per-frame allocations reduced — cached Leaves() on LayoutNode, ASCII rune-to-string lookup table
- Stale git status goroutines leak on CWD change — now cancelled with context + 5s timeout
- Upgrade Go from 1.25.8 to 1.26.1 (fixes GO-2026-4599 and GO-2026-4600 in crypto/x509)

### Added

- Optional pprof HTTP endpoint for runtime profiling (`pprof.enabled = true`)
- Suspend game loop after 5s unfocused — TPS drops to 1, terminal polling paused

## [0.16.0] - 2026-03-07

### Added

- Markdown viewer overlay (Cmd+Shift+M) — reader mode for terminal markdown content

### Fixed

- Markdown viewer not redrawing on close (missing screenDirty flag)
- Markdown viewer wrap columns derived from panel pixel width instead of hardcoded value

## [0.15.0] - 2026-03-07

### Fixed

- Arrow key repeat firing inconsistently in terminal
- Block rendering improvements for command output regions

## [0.14.0] - 2026-03-07

### Added

- Text-to-speech: read selection aloud via macOS `say` command (Cmd+Shift+U)
- TTS auto-speak: automatically reads command output aloud when enabled (requires OSC 133 shell hooks)
- Bell-triggered TTS and stop-on-keypress
- `[voice]` config section: `enabled`, `voice`, `rate` (words per minute)
- Falls back to visible buffer text when no selection is active
- Speech-to-text: dictation overlay via macOS SFSpeechRecognizer
- Git branch display in status bar

### Fixed

- Default `voice.enabled` to false (opt-in)
- Remove unused functions flagged by staticcheck

## [0.12.0] - 2026-03-06

### Added

- Tab hover popover: minimap preview when hovering background tabs (configurable delay, size)

## [0.11.0] - 2026-03-06

### Added

- Stats overlay (Cmd+I): live TPS/FPS, goroutines, heap memory, GC pauses, tab/pane count, buffer dimensions
- "Toggle Stats Overlay" command palette entry

## [0.10.0] - 2026-03-06

### Added

- Tab notes/annotations: attach a persistent text note to any tab (Cmd+Shift+N or command palette)
- Note indicator (`*`) shown on tab bar for tabs with annotations
- Active tab note displayed in status bar as `[note text]` segment
- Tab notes persist across sessions via session.json
- "Edit Tab Note" in right-click context menus (tab bar and pane)
- File drag-and-drop: drop files from Finder to paste shell-escaped paths into the terminal
- Detach pane to new tab (command palette or right-click menu)
- Move pane to next/previous tab (command palette or right-click menu)
- TCC privacy usage descriptions in Info.plist for Documents, Desktop, Downloads, removable volumes, and network volumes

### Fixed

- Paste now NFC-normalizes clipboard content (fixes accented characters from macOS NFD clipboard)
- Paste normalizes line endings to `\r` (fixes multi-line paste producing weird characters)
- Combining Unicode characters (accents, diacritics) merge with the preceding cell instead of occupying their own cell
- Right-click tab context menu now targets the clicked tab, not the active tab
- Scrollback scrolling blocked when alternate screen is active (fixes broken scrolling in TUI apps like Claude Code, nvim, htop)
- Alt screen modes `?47` and `?1047` now supported alongside `?1049`
- Zoomed pane now clears HeaderH so PTY gets correct row count (fixes Helix :q hidden behind status bar)
- Pane row calculation uses single top padding instead of double, recovering ~1 wasted row at pane bottom
- Config reload now recomputes layout for all tabs (fixes stale status bar height after config change)

### Changed

- Go version bumped from 1.25.5 to 1.25.8 (fixes govulncheck GO-2026-4602)

## [0.8.2] - 2026-03-06

### Added

- Configurable `separator` color in `[colors]` (shared by tab bar and status bar)
- Configurable `separator_height_px` and `padding_px` in `[status_bar]`

### Changed

- Tab bar uses darkened background with visible divider lines between tabs
- Tab bar has 1px bottom border separating it from pane content
- Status bar separator and padding are now configurable with sensible defaults

### Fixed

- Help button no longer covers the status bar top separator line

## [0.8.1] - 2026-03-06

### Fixed

- ffmpeg not found when running as macOS .app bundle — login shell PATH is now resolved at startup

## [0.8.0] - 2026-03-06

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

[Unreleased]: https://github.com/studiowebux/zurm/compare/v1.0.2...HEAD
[1.0.2]: https://github.com/studiowebux/zurm/compare/v1.0.0...v1.0.2
[0.16.1]: https://github.com/studiowebux/zurm/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/studiowebux/zurm/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/studiowebux/zurm/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/studiowebux/zurm/compare/v0.12.0...v0.14.0
[0.12.0]: https://github.com/studiowebux/zurm/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/studiowebux/zurm/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/studiowebux/zurm/compare/v0.9.0...v0.10.0
[0.8.2]: https://github.com/studiowebux/zurm/compare/v0.8.1...v0.8.2
[0.8.1]: https://github.com/studiowebux/zurm/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/studiowebux/zurm/compare/v0.7.0...v0.8.0
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
