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

| Feature | Description |
|---|---|
| **GPU rendering** | Ebitengine offscreen compositing at native HiDPI resolution |
| **xterm-256color** | Full 256-color and truecolor support; configurable 16-color ANSI palette |
| **Pane splits** | Binary tree layout — horizontal and vertical splits, any depth |
| **Independent PTYs** | Each pane owns its shell, buffer, and cursor |
| **Tabs** | Multi-tab workspace; OSC 0/2 title updates; user rename via double-click or Cmd+Shift+R |
| **Tab pins** | Pin tabs to home-row slots (a–l) and jump to them instantly with Ctrl+Space |
| **Tab switcher** | Fuzzy overlay to switch tabs by name (Cmd+Shift+T) |
| **Text selection** | Click, double-click (word), triple-click (line), drag |
| **Copy / paste** | Cmd+C copies selection; Cmd+V pastes with bracketed paste support |
| **Scrollback** | Configurable scrollback buffer; Shift+PgUp/Down to scroll |
| **In-buffer search** | Cmd+F opens incremental search across scrollback and primary screen |
| **TUI compatibility** | Mouse reporting (X10 + SGR), alternate screen, focus events, Kitty keyboard protocol |
| **Status bar** | Live CWD, git branch, foreground process name, scroll offset, zoom mode indicator |
| **Zoom pane** | Cmd+Z temporarily fullscreens the focused pane |
| **Command palette** | Cmd+P opens a searchable list of all commands and shortcuts |
| **Help overlay** | Cmd+/ shows all keybindings grouped by category |
| **Right-click menu** | Context menu for copy/paste, pane management, scroll, and more |
| **Session persistence** | On quit, saves each tab's CWD, title, pin slot, and complete pane layout (splits and ratios). On relaunch, restores tabs and pane structure with fresh shells. Running processes are not preserved. |
| **Close confirmation** | Optional dialog before closing a tab or pane (configurable) |
| **File explorer** | Cmd+E opens a sidebar tree browser with `.` and `..` entries, create/rename/delete/copy operations, search filter (`/`), and Finder reveal |
| **Command blocks** | OSC 133 prompt/output tracking; hover to copy a command's output with one click |
| **Wide character support** | CJK characters render with correct column widths. Note: Emoji displays as boxes due to Ebiten limitations (see [docs/emoji-limitations.md](docs/emoji-limitations.md)) |
| **Config auto-bootstrap** | Writes a fully documented `~/.config/zurm/config.toml` on first launch |
| **Configurable** | TOML config: font, colors, shell, padding, scrollback, keyboard, status bar, session |

## Installation

### Binary

Download the latest release from https://github.com/studiowebux/zurm/releases

**`zurm-macos-arm64.zip`** — .app bundle, drag to `/Applications` and launch normally.

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
