# zurm

> **zurm** /zɜːrm/
>
> *noun* — The low, satisfying hum of a terminal session where everything just works. A state of flow achieved when your shell config is finally dialed in.
>
> *verb* — To move through command-line tasks with effortless speed. *"I zurmed through the deploy in under a minute."*
>
> *origin* — Onomatopoeia. The sound a cursor makes if you really listen. A blend of zoom and whirr, with the grounding weight of firm.

---

*zurm — feel the hum.*

---

A GPU-rendered terminal emulator for macOS, written in Go.

## Why this project exists?

A curiosity project: how far can vibe-coded development go when the target is a tool you actually use every day? zurm is the answer so far — a GPU-rendered terminal built entirely through AI-assisted coding, no prior Ebitengine or terminal emulator experience required. It handles everything I need from a terminal. The experiment is ongoing.

Bug tracker: https://github.com/studiowebux/zurm/issues
<br>
Discord: https://discord.gg/BG5Erm9fNv

## Funding

[Buy Me a Coffee](https://buymeacoffee.com/studiowebux)
<br>
[GitHub Sponsors](https://github.com/sponsors/studiowebux)
<br>
[Patreon](https://patreon.com/studiowebux)

## Features

### Rendering

| Feature | Description |
|---|---|
| **GPU rendering** | Ebitengine offscreen compositing at native HiDPI resolution |
| **xterm-256color** | Full 256-color and truecolor support; configurable 16-color ANSI palette |
| **Wide character support** | CJK characters render with correct column widths. Emoji displays as boxes due to Ebiten limitations (see [docs/emoji-limitations.md](docs/emoji-limitations.md)) |
| **Dirty-flag rendering** | Only redraws when state changes; suspends game loop after 5s unfocused (TPS drops to 1) |

### Terminal

| Feature | Description |
|---|---|
| **Independent PTYs** | Each pane owns its shell, buffer, and cursor |
| **TUI compatibility** | Mouse reporting (X10 + SGR), alternate screen, focus events, Kitty keyboard protocol |
| **Scrollback** | Configurable ring buffer; Shift+PgUp/Down, mouse wheel, Shift+Wheel to override PTY mouse mode |
| **In-buffer search** | Cmd+F — incremental search across scrollback and primary screen with match navigation |
| **Clickable URLs** | Cmd+click opens URLs detected in terminal output |
| **Command blocks** | OSC 133 prompt/output tracking; hover to copy a command's output with one click |
| **Text selection** | Click, double-click (word), triple-click (line), drag with auto-scroll; absolute buffer coordinates survive scrolling |
| **Copy / paste** | Cmd+C copies selection; Cmd+V pastes with bracketed paste and NFC normalization |

### Panes

| Feature | Description |
|---|---|
| **Pane splits** | Binary tree layout — Cmd+D horizontal, Cmd+Shift+D vertical, any depth |
| **Pane resize** | Drag dividers with the mouse or Cmd+Opt+Arrow keys (5% step) |
| **Pane rename** | Double-click header, right-click menu, or command palette; names persist across sessions |
| **Pane headers** | Name labels with scroll position indicator when scrolled back |
| **Zoom pane** | Cmd+Z temporarily fullscreens the focused pane |
| **Pane navigation** | Cmd+Arrow to focus adjacent pane; Cmd+[ / ] to cycle |
| **Detach / move pane** | Detach pane to new tab, or move to next/previous tab (command palette or right-click menu) |

### Tabs

| Feature | Description |
|---|---|
| **Multi-tab workspace** | OSC 0/2 title updates; rename via double-click or Cmd+Shift+R |
| **Tab pins** | Pin tabs to home-row slots (a–l) and jump instantly with Cmd+G → key |
| **Tab switcher** | Fuzzy overlay to switch tabs by name (Cmd+Shift+T) |
| **Tab search** | Cmd+J — fuzzy filter by tab name or CWD |
| **Tab notes** | Cmd+Shift+N — attach a persistent text note to any tab; `*` indicator on tab bar, note shown in status bar |
| **Tab reorder** | Cmd+Shift+←/→ or mouse drag (8px threshold) |
| **Tab activity indicator** | Purple dot on background tabs with unseen PTY output |
| **Tab hover popover** | Minimap preview when hovering background tabs (configurable delay and size) |
| **Focus history** | Cmd+; — jump back through previously viewed tabs and panes (stack of 50) |
| **Close confirmation** | Optional dialog before closing a tab or pane (configurable) |

### File Explorer

| Feature | Description |
|---|---|
| **Sidebar tree browser** | Cmd+E — `.` and `..` entries, create/rename/delete/copy operations, Finder reveal |
| **Search filter** | `/` to filter entries (Telescope-style); flat filtered list with keyboard navigation |
| **File drag-and-drop** | Drop files from Finder to paste shell-escaped paths into the terminal |

### Overlays

| Feature | Description |
|---|---|
| **Command palette** | Cmd+P — searchable list of all commands and shortcuts |
| **Help overlay** | Cmd+/ — all keybindings grouped by category |
| **Markdown viewer** | Cmd+Shift+M — reader mode for terminal markdown content with goldmark rendering, Cmd+F search with match highlighting, vim motions (gg, G, Ctrl+d/u, j/k) |
| **Stats overlay** | Cmd+I — live TPS/FPS, goroutines, heap memory, GC pauses, tab/pane count, buffer dimensions |
| **Right-click menu** | Context menu for copy/paste, pane management, scroll, and more |

### Recording

| Feature | Description |
|---|---|
| **Screenshot** | Cmd+Shift+S — one-shot PNG capture to ~/Pictures/zurm-screenshots/ |
| **Screen recording** | Cmd+Shift+. — FFMPEG pipe to MP4 at 30fps to ~/Movies/zurm-recordings/; status bar indicator with elapsed time and file size |

### Speech

| Feature | Description |
|---|---|
| **Text-to-speech** | Cmd+Shift+U — read selection (or visible buffer) aloud via macOS `say`; auto-speak mode for command output; configurable voice and rate |
| **Speech-to-text** | Cmd+Shift+Space — dictation overlay via macOS SFSpeechRecognizer; transcribed text sent to focused PTY |

### Configuration

| Feature | Description |
|---|---|
| **TOML config** | `~/.config/zurm/config.toml` — font, colors, shell, padding, scrollback, keyboard, status bar, session, voice |
| **Config auto-bootstrap** | Writes a fully documented config on first launch with all keys and defaults |
| **Hot-reload** | Cmd+, — reload config without restarting |
| **Theme system** | External TOML themes in `~/.config/zurm/themes/`; switch via config reload |
| **Font size** | Cmd+= / Cmd+- to adjust font size on the fly |

### Session & Status

| Feature | Description |
|---|---|
| **Session persistence** | Saves tab CWDs, titles, pin slots, notes, pane names, and complete pane layout (splits and ratios). Restores on relaunch with fresh shells. `--no-restore` to skip. |
| **Status bar** | Live CWD, git branch, foreground process, scroll offset, zoom indicator, version, recording status, tab notes |

## Installation

### Binary

Download the latest release from https://github.com/studiowebux/zurm/releases

**`zurm-macos-arm64.dmg`** — .app bundle, drag to `/Applications` and launch normally.

**`zurm`** — raw binary, run directly from the terminal.

The app is not notarized yet. On first launch macOS Gatekeeper will block it. To allow it:

```bash
# For the .app bundle
xattr -d com.apple.quarantine zurm.app

# For the raw binary
xattr -d com.apple.quarantine zurm
chmod +x zurm
```

Or: right-click → Open → Open anyway.

### From source

```bash
git clone https://github.com/studiowebux/zurm
cd zurm
go build -o zurm .
```

## Usage

```bash
./zurm
./zurm --no-restore   # skip session restore, open a single fresh tab
```

## Getting Started

Config file: `~/.config/zurm/config.toml` — created automatically on first launch.

```toml
[font]
size = 15

[window]
columns = 120
rows = 35
padding = 4

[colors]
background = "#0F0F18"
foreground = "#E8E8F0"
cursor     = "#A855F7"
border     = "#1C1C2E"

[shell]
program = "/bin/zsh"
args    = ["-l"]

[scrollback]
lines = 10000

[keyboard]
left_option_as_meta = true  # left Option sends ESC sequences; right Option composes macOS chars

[session]
enabled           = true   # save/restore session on quit/launch
restore_on_launch = true
auto_save         = false  # automatically save session on quit (set true to enable)
```

Shell hooks for command blocks (OSC 133) are installed via the command palette: `Cmd+P` → "Install shell hooks".

All keybindings are discoverable in-app via `Cmd+/` (help overlay) or `Cmd+P` (command palette).

## Documentation

https://zurm.dev

## Contributions

1. Fork the repository
2. Create a branch: `git checkout -b feat/your-feature`
3. Commit your changes
4. Open a pull request

Open an issue before starting significant work.

## Assets

Font embedded in the binary:

| Font | Source | License |
|------|--------|---------|
| JetBrains Mono Regular | https://github.com/JetBrains/JetBrainsMono | SIL Open Font License 1.1 |

## License

[MIT](LICENSE)

## Contact

[Studio Webux](https://studiowebux.com)
<br>
tommy@studiowebux.com
<br>
[Discord](https://discord.gg/BG5Erm9fNv)
