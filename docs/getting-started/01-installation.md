---
title: Installation
description: How to install zurm on macOS.
---

# Installation

## Binary Release

Download the latest release from [GitHub Releases](https://github.com/studiowebux/zurm/releases).

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

Or: right-click the app, select Open, then Open anyway.

## From Source

Requires Go >= 1.26.1.

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
