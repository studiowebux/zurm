# Zurm Server/Client Architecture Specification

## Status: PLANNED (Not Implemented)

This is a future architecture to enable persistent sessions, multiple profiles, and detach/reattach capability.

## Current Features (Work Without This Architecture)

All recently implemented features are **independent** of this architecture:
- ✅ Alt+Backspace word deletion
- ✅ Manual session save with auto_save config
- ✅ .app bundle directory fixes
- ✅ File explorer search/filter
- ✅ Pane layout persistence in session.json

These work fine in the current monolithic architecture.

## Future Architecture Overview

```
zurmd (daemon) <---> Unix Socket/gRPC <---> zurm (UI client)
```

### Core Concept
- **zurmd**: Background daemon managing PTYs and sessions
- **zurm**: Lightweight UI client that connects to daemon
- **Sessions**: Named persistent workspaces (like tmux sessions)
- **Profiles**: Saved session templates with config

### Key Benefits
1. **True Persistence**: Close UI without killing terminals
2. **Multiple Sessions**: Run many named sessions concurrently
3. **Hot Switching**: Instant switch between profiles/sessions
4. **Remote Capability**: Connect to zurmd over network

### Implementation Phases

#### Phase 1: Core Daemon
- Extract PTY management to separate process
- Unix domain socket communication
- Basic attach/detach

#### Phase 2: Session Management
- Multiple named sessions
- Session persistence between daemon restarts
- Buffer management for detached sessions

#### Phase 3: Profile System
- Profile = config + session template
- Quick profile switching
- Profile marketplace/sharing

### Technical Decisions

1. **IPC Method**: gRPC with protobuf (efficient, streaming support)
2. **Buffer Storage**: Ring buffer with configurable history
3. **State Persistence**: JSON snapshots + WAL for changes
4. **Security**: Unix socket permissions initially, optional TLS later

### Migration Path

1. **v0.x**: Current monolithic (stable)
2. **v1.0**: Optional daemon mode with fallback
3. **v2.0**: Daemon-only with migration tool

### Files to Create When Implementing

```
zurm/
├── zurmd/                  # Daemon package
│   ├── daemon.go
│   ├── session_manager.go
│   ├── pty_manager.go
│   └── buffer.go
├── proto/                  # gRPC definitions
│   └── zurm.proto
├── client/                 # Refactored UI as client
│   └── client.go
└── cmd/
    ├── zurm/              # UI client entry
    └── zurmd/             # Daemon entry
```

## Not Required For Current Features

The current implementation works perfectly without this architecture. This is a future enhancement for v2.0+.