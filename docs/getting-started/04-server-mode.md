---
title: Server Mode (Mode B)
---

# Server Mode (Mode B)

zurm can delegate PTY sessions to a background daemon (`zurm-server`) so they survive GUI restarts. This is opt-in per pane. Local panes are never affected.

## Prerequisites

Build both binaries:

```bash
make build
make build-server
```

Place `zurm-server` next to `zurm` or anywhere on your PATH.

## Creating server panes

| Method | Description |
|--------|-------------|
| Cmd+Shift+B | New tab with a server-backed pane |
| Cmd+Shift+H | Split current pane horizontally; new half is server-backed |
| Cmd+Shift+V | Split current pane vertically; new half is server-backed |
| Cmd+P | Command palette: "New Server Tab", "Split Horizontal (Server)", "Split Vertical (Server)" |

On first use, zurm auto-starts `zurm-server` in the background. No manual launch required.

## Identifying server panes

Server-backed panes show `[SERVER]` in cyan in the status bar when focused.

## Session persistence

1. Create a server pane (Cmd+Shift+B)
2. Run commands, start processes
3. Quit zurm (Cmd+Q)
4. Relaunch zurm

Server panes reconnect automatically. Recent output is replayed from a 64KB ring buffer so you see context immediately. If the server or session is gone, the pane falls back to a local shell.

## Attaching to existing sessions

### From inside zurm

Cmd+P, type "attach" to find "Attach to Server Session". This lists all live sessions on the server. Pick one to open it in a new tab.

### From the command line

```bash
# List active sessions
zurm -ls

# Output:
# ID                PID    SIZE       DIR
# 7ff2aced69fc24a5  90626  120x35     /Users/you/projects

# Attach by full ID
zurm -a 7ff2aced69fc24a5

# Attach by short prefix (Docker-style matching)
zurm -a 7ff
```

Short prefix matching works as long as the prefix is unambiguous. If multiple sessions match, zurm reports which ones matched.

## Server lifecycle

- Auto-starts when a server pane is created and the server is not running
- Runs as a detached background process (survives zurm close)
- Logs to `~/.config/zurm/server.log`
- Sessions are removed automatically when the shell exits
- To stop the server: `pkill zurm-server`

## Configuration

```toml
[server]
address = ""   # Unix socket path; empty = ~/.config/zurm/server.sock
binary  = ""   # zurm-server binary path; empty = next to zurm, then PATH
```

## Resource usage

Each live session uses approximately 64KB (output replay buffer) plus a PTY file descriptor. Sessions are cleaned up when the shell exits. The server has no Ebitengine dependency and minimal memory footprint.
