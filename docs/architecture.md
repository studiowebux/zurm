---
title: Architecture
description: High-level architecture overview for contributors.
---

# Architecture

zurm is a GPU-accelerated terminal emulator for macOS, built on [Ebiten](https://ebitengine.org/) (OpenGL game engine) and Go's `os/exec` for PTY management.

## System Overview

```
  ┌──────────────────────────────────────────────────────────┐
  │                     Ebiten Game Loop                     │
  │                                                          │
  │  Update()          Draw()              Layout()          │
  │  ─ input routing   ─ Renderer.DrawAll  ─ DPI scaling    │
  │  ─ channel drains  ─ layer compositing                  │
  │  ─ state updates   ─ cache skip logic                   │
  └──────┬─────────────────────┬─────────────────────────────┘
         │                     │
         ▼                     ▼
  ┌─────────────┐     ┌──────────────────┐
  │  Terminal    │     │    Renderer      │
  │  ─ Buffer    │◄────│  ─ offscreen     │  RLock
  │  ─ Parser    │     │  ─ blocksLayer   │
  │  ─ Cursor    │     │  ─ modalLayer    │
  └──────┬───────┘     └──────────────────┘
         │ Lock
         ▼
  ┌─────────────┐
  │ PtyBackend  │ ◄── Strategy Pattern
  │  (interface)│
  ├─────────────┤
  │ PTYManager  │  Mode A: local PTY (os/exec + pty)
  │ ServerBack. │  Mode B: zurm-server over HTTP
  └─────────────┘
```

## Data Flow

```
PTY output → PtyBackend.StartReader() goroutine
           → Parser.Feed() [under write lock]
           → ScreenBuffer mutation (cells, cursor, scrollback)
           → RenderGen++ (atomic)

Game.Draw() → Renderer.DrawAll(DrawState)
            → per-pane cache check (skip if gen unchanged)
            → ScreenBuffer.RLock → read cells → RUnlock
            → draw to offscreen → composite layers → GPU
```

## Package Responsibilities

| Package | Responsibility |
|---------|---------------|
| `main` (root) | Game struct, Update/Draw/Layout, decomposed into 19 focused files (see below) |
| `terminal/` | VT500 parser, screen buffer (cells + scrollback), cursor state, PTY backend interface |
| `renderer/` | GPU rendering via Ebiten — decomposed into 17+ files (see below) |
| `pane/` | Binary tree layout, pane view (`NewPane` DI constructor), factory for terminal creation |
| `tab/` | Tab management, layout node ownership, activity detection |
| `config/` | TOML parsing, defaults, hot-reload, theme merge |
| `session/` | Session save/restore (JSON) — tab CWDs, titles, layout tree |
| `zserver/` | Persistent PTY server — session management, ring buffer replay, subscriber pattern |
| `fileexplorer/` | File tree domain logic — pure functions, no rendering dependency |
| `vault/` | Encrypted command history, ghost text suggestions, AES-256 encryption |
| `markdown/` | Markdown AST parser — headings, code blocks, tables, bold/italic |
| `help/` | Keybinding definitions, command palette entries |
| `recorder/` | Screenshot (PNG) and screen recording |

### Main Package File Structure

The root `main` package was decomposed from a single `main.go` into focused files:

| File | Responsibility |
|------|---------------|
| `main.go` | Game struct, Update/Draw/Layout, init, entry point |
| `game_input.go` | Keyboard routing, overlay priority cascade |
| `game_mouse.go` | Mouse dispatch, selection, divider drag, URL click |
| `game_drain.go` | PTY drains, polling, clipboard, paste |
| `game_lifecycle.go` | Focus, resize, suspend, dropped files |
| `game_tabs.go` | Tab create/close/switch/pin/search/switcher |
| `game_panes.go` | Pane splits, focus, resize, zoom |
| `game_overlays.go` | Menus, palette, confirm, overlay handlers |
| `game_search.go` | SearchController + search input |
| `game_explorer.go` | File explorer input handlers |
| `game_viewer.go` | Markdown viewer, llms.txt, URL input |
| `game_server.go` | Server session management |
| `game_misc.go` | Rename, notes, session, config reload, shell hooks |
| `tab_manager.go` | TabManager — owns tab slice, active index, focus history, drag/pin state |
| `palette_controller.go` | PaletteController — filter state, selection |
| `explorer_controller.go` | ExplorerController — tree state, expand/collapse |
| `mouse_state.go` | SelectionDragger + DividerDragHandler value types |
| `key_repeat.go` | Shared KeyRepeatHandler for auto-repeat across controllers |
| `status_poller.go` | Background git status + foreground process polling |

### Renderer Package File Structure

The `renderer/` package was decomposed into focused sub-renderers:

| File | Responsibility |
|------|---------------|
| `renderer.go` | Renderer struct, DrawAll orchestration, layer compositing, cache logic |
| `pane_render.go` | DrawPane, drawPaneTo, drawDividers |
| `tabbar.go` | drawTabBar |
| `tabhover.go` | Tab hover thumbnails, cache keys |
| `tabswitcher.go` | Tab switcher overlay |
| `tabsearch.go` | Tab search overlay |
| `statusbar.go` | Status bar rendering |
| `overlay.go` | Help overlay, markdown viewer overlay |
| `palette.go` | Command palette rendering |
| `search.go` | In-buffer search bar |
| `fileexplorer.go` | File explorer panel |
| `menu.go` | Context menu rendering |
| `blocks.go` | OSC 133 command block decorations |
| `stats.go` | Debug stats overlay |
| `font.go` | Font renderer, glyph drawing, display width |
| `renderconfig.go` | RenderConfig — decouples renderer from config package |
| `state.go` | All 15 UI state types consolidated (DrawState, SearchState, etc.) |
| `helptypes.go` | OverlayMenuItem, OverlayKeyBinding — decouples from help package |
| `mdtypes.go` | MdSpanStyle, MdSpan, MdStyledLine — decouples from markdown package |
| `uicolors.go` | UIColors derivation, color helpers |

## Key Design Patterns

### Composite — Pane Layout Tree

`LayoutNode` is a binary tree. Each node is a `Leaf` (holds a `Pane`), `HSplit` (left | right), or `VSplit` (top / bottom). `Ratio` controls the split proportion.

```
         HSplit (0.5)
        /            \
   Leaf(pane1)    VSplit(0.6)
                  /          \
             Leaf(pane2)  Leaf(pane3)
```

`ComputeRects()` recursively assigns pixel rectangles. `Leaves()` returns all panes in DFS order.

Pane creation uses DI constructors in `pane/factory.go`: `NewLocal()` and `NewServer()` build terminals with the correct backend, while `NewPane()` wraps a terminal into a `Pane` view without importing `config`.

### Strategy — PTY Backend

`PtyBackend` interface abstracts local vs. remote terminal I/O:

```go
type PtyBackend interface {
    Write(p []byte) (int, error)
    Resize(cols, rows int) error
    Dead() <-chan struct{}
    Close()
    Pid() int
    ForegroundPgid() (int, error)
    StartReader(parser *Parser, buf *ScreenBuffer, paused *atomic.Bool)
}
```

- **PTYManager** (Mode A) — local shell via `os/exec` + `creack/pty`
- **ServerBackend** (Mode B) — connects to zurm-server over HTTP, PTY persists across app restarts

### State Machine — VT500 Parser

The parser has 8 states dispatched via method calls:

| State | Trigger | Handles |
|-------|---------|---------|
| `Ground` | default | printable characters, C0 controls |
| `Escape` | ESC | escape sequence start |
| `EscapeInterm` | ESC + intermediate | two-byte escape sequences |
| `CSIEntry` | ESC [ | control sequence introducer |
| `CSIParam` | digits/semicolons | CSI parameter collection |
| `CSIInterm` | intermediate in CSI | CSI with intermediate bytes |
| `OSC` | ESC ] | operating system commands (title, CWD, shell integration) |
| `DCS` | ESC P | device control strings (ignored) |

`Parser.Feed()` processes raw PTY bytes and mutates the `ScreenBuffer` under the caller's write lock.

### Observer — Server Session Subscriptions

`zserver.Session` uses a pub-sub pattern for multi-client output:

- PTY reader writes output to all subscriber channels
- `subscribe()` returns a buffered `chan []byte`
- Slow clients drop packets (non-blocking send with `select/default`)
- `ringBuf` (64KB circular buffer) stores recent output for replay on reconnect

### Snapshot — Lock Minimization

The renderer minimizes lock duration by taking snapshots:

1. `DrawState` is built in `Game.Draw()` with current state
2. `blockSnap` copies block data under `RLock`, then renders without holding the lock
3. `paneCacheEntry` tracks `renderGen` — unchanged panes skip `DrawPane` entirely

## Concurrency Model

```
Main Thread (Ebiten)
├── Game.Update()  — input handling, state mutations
├── Game.Draw()    — renderer calls (single-threaded GPU)
│
PTY Reader Goroutines (one per pane)
├── Read PTY bytes
├── Lock ScreenBuffer
├── Parser.Feed() — mutates cells, cursor, scrollback
├── Unlock ScreenBuffer
│
Async Goroutines
├── Git status polling (background, result via channel)
├── Clipboard operations (paste via channel)
├── Screenshot encoding (PNG in background)
└── CWD/foreground process detection
```

**Synchronization:**
- `sync.RWMutex` on `ScreenBuffer` — parser writes, renderer reads
- `atomic.Bool` for `paused` flag — spin-wait during resize (avoids race between resize and parse)
- `atomic.Uint64` for `renderGen` — activity detection without locking
- `atomic.Bool` for `osc7Active`, `osc133Active` — feature detection flags
- Buffered channels for async results (clipboard, git, screenshots)

## Rendering Pipeline

Three composited layers:

1. **offscreen** — pane cell grids, tab bar, status bar, pane labels
2. **blocksLayer** — OSC 133 command block decorations (borders, duration badges, copy buttons)
3. **modalLayer** — overlays (search bar, palette, file explorer, help, confirm dialogs)

Final composite: `offscreen` → `blocksLayer` (alpha blend) → `modalLayer` (alpha blend) → screen.

**Performance optimizations:**
- Per-pane render cache skips unchanged panes (tracks renderGen, viewOffset, cursor position)
- `layoutDirty` flag triggers full offscreen clear only when layout changes
- `overlayBg` (1×1 backdrop image) created once, scaled to screen size
- Auto-idle reduces TPS when window is unfocused
- `allKeys` computed once at package init (avoids per-frame allocation)

## Session Persistence

```
~/.config/zurm/session.json
├── version: 1
├── activeTab: int
└── tabs[]
    ├── cwd, title, pinnedSlot, note
    └── layout (recursive PaneLayout tree)
        ├── kind: "leaf" | "hsplit" | "vsplit"
        ├── ratio: float64
        ├── cwd, customName, serverSessionID
        └── left, right (children)
```

Saved on quit (if `session.enabled`), restored on launch (if `restore_on_launch`).

## Configuration

- `config.Load()` reads `~/.config/zurm/config.toml`, starts from `Defaults`, overlays user values
- `config.LoadWithMeta()` re-reads and returns TOML MetaData for theme merge
- Theme merge: `MergeColorsWithMeta()` uses TOML metadata to determine user-explicit vs. theme-default colors
- Hot-reload via `Cmd+,` — calls `LoadWithMeta()`, applies theme, updates renderer colors, clears pane cache
