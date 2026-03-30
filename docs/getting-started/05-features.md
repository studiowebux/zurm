---
title: Features Guide
description: Detailed walkthrough of zurm's features and how to use them.
---

# Features Guide

This guide explains zurm's features beyond the basics. All keybindings are discoverable in-app via `Cmd+/` (help overlay) or `Cmd+P` (command palette).

## Tabs

Create tabs with `Cmd+T`. Each tab has its own pane layout tree. Switch between tabs with `Cmd+1` through `Cmd+9`, or `Cmd+Shift+[` / `Cmd+Shift+]` to cycle.

**Rename a tab:** `Cmd+Shift+R` opens inline rename. Press Enter to confirm, Escape to cancel.

**Tab notes:** `Cmd+Shift+N` lets you attach a short note to any tab. Tabs with notes show a `*` suffix in the tab bar.

**Move tabs:** `Cmd+Shift+←/→` reorders tabs, or drag them with the mouse.

**Activity indicator:** A purple dot appears on background tabs when their terminal produces new output. This helps you notice when a long-running command completes.

## Pin Tabs

Pin frequently-used tabs to keyboard slots for instant switching.

1. Press `Cmd+G` to enter pin mode
2. Press `Shift+A` through `Shift+L` to assign the active tab to that slot
3. Press `A` through `L` to jump to a pinned tab
4. Press the same `Shift+` key again to unpin

Pinned tabs show a `·X` badge (where X is the slot letter) in the tab bar, and `[X]` in the tab switcher/search.

## Tab Parking

Park tabs you want to keep running but don't need in the tab bar right now. Parked tabs are hidden from the tab bar — their PTY stays alive, shell state is preserved.

**Park a tab:** `Cmd+Shift+K` parks the active tab. You cannot park the last visible tab.

**Find parked tabs:** `Cmd+J` (tab search) shows all tabs — visible and parked. Parked entries show a `[P]` badge and dimmed title. Select one to unpark and activate it.

**Pin slot jump:** If a parked tab has a pin slot, jumping to that slot (`Cmd+G` then the letter) unparks it automatically.

**Tab bar badge:** When parked tabs exist, a `[N↓]` count badge appears at the right edge of the tab bar.

**Session restore:** Parked tabs are saved and restored across sessions.

**Max open tabs:** The `max_open` config key caps the number of visible tabs (default 10, `0` = unlimited). When the limit is reached, `Cmd+T` shows a status flash — park or close a tab first.

## Tab Switcher & Search

**Tab switcher** (`Cmd+Shift+T`): Shows visible tabs with pin badges. Arrow keys to select, Enter to switch.

**Tab search** (`Cmd+J`): Type to search all tabs (visible + parked) by title or working directory. Results ranked by match position. Parked tabs show a `[P]` badge — selecting one unparks and activates it.

## Panes

Split the current tab into multiple panes:

- `Cmd+D` — horizontal split (side by side)
- `Cmd+Shift+D` — vertical split (top and bottom)

**Navigate panes:** `Cmd+Arrow` moves focus to an adjacent pane. `Cmd+[` / `Cmd+]` cycles through panes.

**Resize:** `Cmd+Opt+Arrow` adjusts the split ratio, or drag the divider with the mouse.

**Zoom:** `Cmd+Z` maximizes the focused pane (hides all others). Press again to restore.

**Rename a pane:** Double-click the pane header label.

**Close:** `Cmd+W` closes the focused pane. If it's the last pane in a tab, the tab closes.

**Detach/move panes:** Use the command palette (`Cmd+P`) to detach a pane to a new tab, or move it to the next/previous tab.

## Focus History

`Cmd+;` navigates back through your focus history — the last 50 panes you focused. Useful when you split multiple panes and want to quickly return to where you were.

## In-Buffer Search

`Cmd+F` opens the search bar at the bottom of the terminal. Type to search the scrollback buffer.

- `Enter` / `↓` — next match
- `Shift+Enter` / `↑` — previous match
- `Escape` — close search

The current match is highlighted in yellow, other matches in blue. The match counter shows `N of M`.

## File Explorer

`Cmd+E` toggles the file explorer sidebar.

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate entries |
| `Enter` / `→` | Expand directory or open file in viewer |
| `←` | Collapse directory |
| `n` | New file |
| `Shift+N` | New directory |
| `r` | Rename |
| `d` | Delete (with confirmation) |
| `c` | Copy path to clipboard |
| `o` | Open in Finder |
| `/` | Search within explorer |
| `Esc` | Close explorer |

Configure side and width in `[file_explorer]` config section.

## Command Blocks (OSC 133)

When shell hooks are installed, zurm detects command boundaries and renders visual blocks around each command and its output.

**Setup:** `Cmd+P` → search "Install shell hooks" → Enter. This adds OSC 133 sequences to your shell prompt.

**Toggle:** `Cmd+B` enables/disables block rendering at runtime.

**Features when enabled:**
- Left accent stripe colored by exit status (green = success, red = failure)
- Elapsed execution time badge (e.g., "1.2s")
- Hover any block to see copy buttons: `cmd` (command only), `out` (output only), `all` (both)
- Optional background tint (`bg_color` + `bg_alpha` in config)

Configure in `[blocks]` config section.

## Command Palette

`Cmd+P` opens the command palette — a searchable list of all available actions. Type to filter, arrow keys to select, Enter to execute.

The palette includes all tab, pane, search, recording, and configuration commands.

## Markdown Reader Mode

`Cmd+Shift+M` opens the markdown viewer, which renders `.md` files with:
- Headings, bold, italic, strikethrough
- Code blocks with syntax-aware coloring
- Tables with border rendering
- Inline code highlighting
- Search within the document (`/` to search, `n`/`N` to navigate matches)

Colors are customizable via the `md_*` fields in `[colors]` config section.

## llms.txt Browser

`Cmd+L` opens the llms.txt browser — a text-mode web browser for reading `llms.txt` files (AI-readable documentation).

| Key | Action |
|-----|--------|
| `f` | Enter hint mode (follow links) |
| `a`-`z` | Select a link badge |
| `Backspace` / `H` | Navigate back |
| `L` | Navigate forward |
| `Cmd+Enter` | Send current content to the terminal pane |

## Vault (Command History)

Encrypted local command history with ghost text suggestions.

**Enable:** Set `vault.enabled = true` in config.

**How it works:**
1. zurm imports your shell history on startup
2. As you type, a ghost suggestion appears inline (dimmed text)
3. Press `→` (right arrow) to accept the suggestion
4. Commands prefixed with a space are never stored (privacy)

**Encryption:** Commands are stored in `~/.config/zurm/vault.enc` using AES-256. The key is auto-generated at `~/.config/zurm/vault.key` (mode 0600).

Configure in `[vault]` config section.

## Recording

**Screenshot:** `Cmd+Shift+S` captures the current terminal as a PNG file.

**Screen recording:** `Cmd+Shift+.` starts/stops recording. Output is saved as MP4.

Files are saved to the current working directory.

## Tab Hover Minimap

Hover over a background tab in the tab bar to see a minimap preview of its content. The preview appears after a configurable delay.

Configure in `[tabs.hover]`: `enabled`, `delay_ms`, `width`, `height`.

## Stats Overlay

`Cmd+I` toggles a small stats panel in the top-right corner showing:

- **TPS / FPS** — Ebiten tick rate and frame rate
- **Goroutines** — number of active Go goroutines
- **Heap** — allocated / system heap memory
- **GC** — garbage collection count and last pause duration
- **Tabs / Panes** — count of open tabs and panes
- **Buffer** — focused pane dimensions and scrollback depth

## Server Mode (Mode B)

zurm-server provides persistent PTY sessions that survive app restarts.

**Create a server tab:** `Cmd+Shift+B`

**Split with server pane:** `Cmd+Shift+H` (horizontal) or `Cmd+Shift+V` (vertical)

**Attach to existing session:** `Cmd+P` → "Attach to Server Session"

Server sessions persist even when zurm is closed. Reconnecting replays the last 64KB of output.

Configure in `[server]` config section. See the [Server Mode guide](../getting-started/04-server-mode.md) for setup details.

## URL Clicking

`Cmd+Click` on a URL in the terminal opens it in your default browser. URLs are detected automatically as you hover (highlighted with an underline).

## Bell

When a terminal sends a bell character (BEL / `\a`), zurm can:
- Flash the pane border in a configurable color (`style = "visual"`)
- Play the system beep sound (`sound = true`)

A bell icon appears briefly on the affected tab. Configure in `[bell]` config section.

## Hot Reload

Press `Cmd+,` to reload `~/.config/zurm/config.toml` without restarting. Colors, status bar settings, and most UI options update immediately.

## Session Persistence

When `session.enabled = true`, zurm saves your tab layout (CWDs, titles, pin slots, notes, pane splits) on quit and restores it on launch.

Configure in `[session]` config section.
