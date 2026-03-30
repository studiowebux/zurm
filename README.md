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

| Category | Feature | Details |
|---|---|---|
| **Rendering** | GPU compositing | Ebitengine offscreen rendering at native resolution |
| | HiDPI / Retina | Physical-pixel layout with automatic scale factor detection |
| | Dirty-flag redraw | Only renders when state changes; TPS drops to 1 after 5s unfocused |
| | Wide characters | CJK characters render with correct double-width columns |
| | Font fallback chain | Multiple fonts tried in order; covers Latin, CJK, symbols |
| **Terminal** | xterm-256color | Full 256-color and truecolor (24-bit) support |
| | ANSI palette | Configurable 16-color palette via TOML |
| | PTY per pane | Each pane owns its shell, buffer, and cursor independently |
| | VT parser | Handles CSI, OSC, DCS, alternate screen, scroll regions |
| | Kitty keyboard | Extended key protocol for unambiguous modifier detection |
| | Mouse modes | X10 + SGR mouse reporting, focus events |
| **Panes** | Binary tree splits | Cmd+D horizontal, Cmd+Shift+D vertical, any depth |
| | Resize drag | Mouse drag on dividers or Cmd+Opt+Arrow (5% step) |
| | Zoom | Cmd+Z temporarily fullscreens the focused pane |
| | Detach / move | Detach pane to new tab or move between tabs |
| | Headers | Name labels with scroll position indicator |
| | Rename | Double-click header, right-click menu, or command palette |
| | Persist | Pane names and layout survive session save/restore |
| **Tabs** | Parking | Cmd+Shift+K — hide tab from bar, keep PTY alive; Cmd+J to find/unpark |
| | Max open | `[tabs] max_open = 10` caps visible tabs; parked tabs are unlimited |
| | Switcher | Cmd+Shift+T — fuzzy overlay to switch by name |
| | Search | Cmd+J — filter all tabs (visible + parked) by name or CWD |
| | Pins | Cmd+G → home-row key (a–l) for instant tab jump; works with parked tabs |
| | Notes | Cmd+Shift+N — persistent text note per tab; shown in status bar |
| | Reorder | Cmd+Shift+←/→ or mouse drag |
| | Activity indicator | Purple dot on tabs with unseen PTY output; badge glow for parked |
| | Hover preview | Minimap popover on background tab hover |
| | Focus history | Cmd+; — stack of 50 previously viewed tabs/panes |
| **File Explorer** | Tree sidebar | Cmd+E — browse, create, rename, delete, copy path |
| | Finder reveal | Open file location in macOS Finder |
| | Search filter | / to filter entries (Telescope-style flat list) |
| **Selection** | Click / word / line | Click, double-click, triple-click with drag |
| | Auto-scroll | Selection drag past viewport edges scrolls automatically |
| | Wide char aware | Selection handles double-width characters correctly |
| | Cmd+C / Cmd+V | Copy selection, paste with bracketed paste + NFC normalization |
| **Scrollback** | Ring buffer | Configurable size (default 10,000 lines) |
| | Search | Cmd+F — incremental search across scrollback + primary screen |
| | Mouse wheel | Scroll through history; Shift+Wheel overrides PTY mouse mode |
| **URLs & Blocks** | Cmd+click URLs | Opens detected URLs in the default browser |
| | OSC 133 blocks | Command prompt/output tracking via shell hooks |
| | Block hover copy | Hover a command block to copy its output; elapsed timer shown |
| **Markdown** | Reader mode | Cmd+Shift+M — renders terminal content as markdown via goldmark |
| | Search | Cmd+F with match highlighting inside the viewer |
| | Vim motions | gg, G, Ctrl+d/u, j/k navigation |
| **llms.txt** | Browser | Cmd+L — fetch and display llms.txt from any domain |
| | Hint-mode links | Follow links by label, keyboard-driven navigation |
| | History nav | Back/forward through browsed pages |
| | Send to pane | Pipe content from llms.txt browser to the focused terminal |
| **Server (Mode B)** | Session persistence | Optional `zurm-server` daemon manages PTY sessions in the background |
| | Auto-start | Server spawns on demand when you create a server pane — no manual setup |
| | Per-pane opt-in | Cmd+Shift+B (tab), Cmd+Shift+H/V (split) — local panes unaffected |
| | Reattach | Cmd+P → "Attach to Server Session" or `zurm -a <id>` (prefix match) |
| | CLI | `zurm -ls` lists sessions; `zurm -a 7ff` attaches by short ID |
| | Status indicator | [SERVER] badge in status bar when focused pane is server-backed |
| **Vault** | Command history | Encrypted local command vault; imports ~/.zsh_history on first run |
| | Ghost suggestions | Fish-style inline ghost text as you type; right arrow to accept |
| | Privacy | Space-prefixed commands excluded; AES-256-GCM encryption at rest |
| **Recording** | Screenshot | Cmd+Shift+S — PNG capture to ~/Pictures/zurm-screenshots/ |
| | Screen recording | Cmd+Shift+. — FFMPEG MP4 at 30fps to ~/Movies/zurm-recordings/ |
| | Status indicator | Elapsed time and file size shown in status bar while recording |
| **Config** | TOML | `~/.config/zurm/config.toml` — auto-generated with defaults on first launch |
| | Hot-reload | Cmd+, — reload config without restarting |
| | Theme system | External TOML themes in `~/.config/zurm/themes/` |
| | Font size adjust | Cmd+= / Cmd+- on the fly |
| **Session** | Save / restore | Tabs, panes, layout, CWDs, pins, notes — all persisted. `--no-restore` to skip |
| | Auto-save | Optional automatic session save on quit |
| **Status Bar** | Live indicators | CWD, git branch, process, scroll offset, zoom, recording, version |
| **macOS** | .app bundle | ARM64 build via GitHub Actions |
| | DMG distribution | Drag-to-install disk image |
| | Gatekeeper | `xattr -d com.apple.quarantine` or right-click → Open |
| | Left Option as Meta | Configurable; right Option composes macOS characters |

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
./zurm -ls            # list active zurm-server sessions
./zurm -a 7ff         # attach to a server session by short ID prefix
```

## Server Mode (Mode B)

zurm can delegate PTY sessions to a background daemon (`zurm-server`) so they survive GUI restarts. This is opt-in per pane — local panes are never affected.

### Quick start

1. Build: `make build && make build-server`
2. Open zurm and press **Cmd+Shift+B** to create a server-backed tab
3. zurm-server auto-starts in the background (no manual setup)
4. Quit zurm, relaunch — the server pane reconnects with output preserved

### Keybindings

| Key | Action |
|-----|--------|
| Cmd+Shift+B | New server tab |
| Cmd+Shift+H | Split horizontal (server pane) |
| Cmd+Shift+V | Split vertical (server pane) |
| Cmd+P → "Attach" | Attach to an existing server session |

### CLI

```bash
zurm -ls              # list active sessions (ID, PID, size, dir)
zurm -a <id>          # attach by full or short ID (Docker-style prefix matching)
```

### How it works

- `zurm-server` is a headless daemon that owns PTY sessions
- Each server pane connects over a Unix socket (`~/.config/zurm/server.sock`)
- Sessions persist after zurm closes; reattach restores from a 64KB output replay buffer
- Server auto-starts on first Cmd+Shift+B and stays alive in the background
- `[SERVER]` indicator appears in the status bar for server-backed panes
- If the server is unreachable, panes fall back to a local PTY silently

### Config

```toml
[server]
address = ""   # Unix socket path; empty = ~/.config/zurm/server.sock
binary  = ""   # zurm-server binary path; empty = next to zurm or PATH
```

## Getting Started

Config file: `~/.config/zurm/config.toml` — created automatically on first launch with all defaults.

A full annotated example is available at [`config.example.toml`](config.example.toml).

Shell hooks for command blocks (OSC 133) are installed via the command palette: `Cmd+P` → "Install shell hooks".

All keybindings are discoverable in-app via `Cmd+/` (help overlay) or `Cmd+P` (command palette).

## Documentation

https://zurm.dev

## Optional Fonts

zurm ships with JetBrains Mono embedded. For CJK characters, emoji, Nerd Font symbols, and braille, add fallback fonts to the `[font]` section in your config:

```toml
[font]
fallbacks = [
  "/Users/you/Library/Fonts/NotoSansMonoCJKsc-Regular.otf",
  "/Users/you/Library/Fonts/NotoEmoji-Regular.ttf",
  "/Users/you/Library/Fonts/SymbolsNerdFontMono-Regular.ttf",
  "/Users/you/Library/Fonts/NotoSansSymbols2-Regular.ttf",
]
```

Fonts are tried in order when a glyph is missing from the primary font. Download them once:

```bash
# CJK (~16 MB)
curl -sL "https://github.com/googlefonts/noto-cjk/raw/main/Sans/Mono/NotoSansMonoCJKsc-Regular.otf" \
  -o ~/Library/Fonts/NotoSansMonoCJKsc-Regular.otf

# Monochrome emoji (~2 MB)
curl -sL "https://github.com/google/fonts/raw/main/ofl/notoemoji/NotoEmoji%5Bwght%5D.ttf" \
  -o ~/Library/Fonts/NotoEmoji-Regular.ttf

# Nerd Font symbols (~2.4 MB)
curl -sL "https://github.com/ryanoasis/nerd-fonts/releases/download/v3.4.0/NerdFontsSymbolsOnly.tar.xz" \
  | tar xJ -C ~/Library/Fonts/ SymbolsNerdFontMono-Regular.ttf

# Braille + extra symbols (~1.2 MB)
curl -sL "https://github.com/google/fonts/raw/main/ofl/notosanssymbols2/NotoSansSymbols2-Regular.ttf" \
  -o ~/Library/Fonts/NotoSansSymbols2-Regular.ttf
```

Color emoji (Apple Color Emoji, Twemoji) are not supported — Ebitengine does not render color font formats. Monochrome emoji via Noto Emoji work fine.

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
