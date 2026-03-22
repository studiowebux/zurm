package main

import (
	"image"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/fileexplorer"
	"github.com/studiowebux/zurm/renderer"
)

// explorerInputKeys is the set of keys the file explorer handles.
// Declared here so openFileExplorer and handleFileExplorerInput share the same set.
var explorerInputKeys = []ebiten.Key{
	ebiten.KeyEnter, ebiten.KeyBackspace,
	ebiten.KeyArrowUp, ebiten.KeyArrowDown, ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
	ebiten.KeyN, ebiten.KeyR, ebiten.KeyD,
	ebiten.KeyC, ebiten.KeyX, ebiten.KeyP,
	ebiten.KeyO, ebiten.KeyE, ebiten.KeyY,
}

// openFileExplorer opens the file explorer sidebar rooted at the focused pane's CWD.
func (g *Game) openFileExplorer() {
	root := g.activeFocused().Term.Cwd
	if root == "" {
		root = os.Getenv("HOME")
	}
	entries, err := fileexplorer.BuildTree(root)
	if err != nil {
		return
	}
	g.explorer.State = renderer.FileExplorerState{
		Open:    true,
		Root:    root,
		Entries: entries,
		Side:    g.cfg.FileExplorer.Side,
	}
	g.closePalette()
	g.overlays.Help = renderer.OverlayState{}
	g.closeMenu()
	g.closeSearchOverlay()

	// Reset prevKeys for all explorer-relevant keys to the CURRENT pressed state.
	// This prevents stale "was pressed" state from prior handlers causing missed
	// or double-fired edge detection on the first explorer frame.
	for _, k := range explorerInputKeys {
		g.input.PrevKeys[k] = ebiten.IsKeyPressed(k)
	}
	g.explorer.repeat.Reset()
	g.render.Dirty = true
}

// closeFileExplorer closes the file explorer sidebar.
func (g *Game) closeFileExplorer() {
	g.explorer.Close()
	g.render.Dirty = true
}

// reloadExplorerTree rebuilds the entry list from the current root.
func (g *Game) reloadExplorerTree() {
	g.explorer.ReloadTree()
	g.render.Dirty = true
}

// handleFileExplorerInput routes keyboard events while the file explorer is open.
func (g *Game) handleFileExplorerInput() {
	st := &g.explorer.State
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// Confirm dialog has highest priority — ESC dismisses the confirm, not the explorer.
	if st.ConfirmOpen {
		if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
			st.ConfirmOpen = false
			g.input.PrevKeys[ebiten.KeyEscape] = true
			return
		}
		g.handleExplorerConfirmInput()
		return
	}

	// ESC handling
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		// In search mode, ESC clears search or exits search mode
		if st.SearchMode {
			st.SearchMode = false
			if st.SearchQuery == "" {
				// If no query, close the explorer
				g.closeFileExplorer()
			}
		} else if st.SearchQuery != "" {
			// Clear search filter but stay in explorer
			st.SearchQuery = ""
			st.SearchCursorPos = 0
			st.FilteredIndices = nil
		} else {
			// Normal close
			g.closeFileExplorer()
		}
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	// Cmd+E toggle close (separate from ESC so the E key is tracked independently).
	{
		ePressed := ebiten.IsKeyPressed(ebiten.KeyE)
		eWas := g.input.PrevKeys[ebiten.KeyE]
		g.input.PrevKeys[ebiten.KeyE] = ePressed
		if meta && ePressed && !eWas {
			g.closeFileExplorer()
			return
		}
	}

	// Search mode handling
	if st.SearchMode {
		g.handleExplorerSearchInput()
		return
	}

	// Input mode (rename / new file / new dir).
	if st.Mode != renderer.ExplorerModeNormal {
		g.handleExplorerInputMode()
		return
	}

	// Status timer countdown.
	if st.StatusTimer > 0 {
		st.StatusTimer--
	}

	// Arrow keys with key-repeat (same parameters as PTY key repeat).
	now := time.Now()
	upPressed := ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	downPressed := ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	if g.explorer.repeat.Update(ebiten.KeyArrowUp, upPressed, g.input.PrevKeys[ebiten.KeyArrowUp], now) {
		g.explorer.Move(-1)
	}
	if g.explorer.repeat.Update(ebiten.KeyArrowDown, downPressed, g.input.PrevKeys[ebiten.KeyArrowDown], now) {
		g.explorer.Move(1)
	}
	g.input.PrevKeys[ebiten.KeyArrowUp] = upPressed
	g.input.PrevKeys[ebiten.KeyArrowDown] = downPressed

	// Non-repeating action keys — edge-triggered only.
	actionKeys := []ebiten.Key{
		ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
		ebiten.KeyEnter,
		ebiten.KeyN, ebiten.KeyR, ebiten.KeyD,
		ebiten.KeyC, ebiten.KeyX, ebiten.KeyP,
		ebiten.KeyO, ebiten.KeySlash,
	}
	for _, key := range actionKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		g.input.PrevKeys[key] = pressed
		if !pressed || wasPressed {
			continue
		}

		switch {
		case key == ebiten.KeyArrowRight:
			if st.Cursor < len(st.Entries) {
				e := st.Entries[st.Cursor]
				// Handle special entries
				if e.Name == "." {
					// Current directory - insert path
					g.activeFocused().Term.SendBytes([]byte(e.Path))
					g.closeFileExplorer()
					return
				} else if e.Name == ".." {
					// Navigate to parent
					st.Root = e.Path
					entries, err := fileexplorer.BuildTree(e.Path)
					if err == nil {
						st.Entries = entries
						st.Cursor = 0
						st.ScrollOffset = 0
					}
				} else if e.IsDir && !e.Expanded {
					entries, err := fileexplorer.ExpandAt(st.Entries, st.Cursor)
					if err == nil {
						st.Entries = entries
						// Do NOT advance cursor — user stays on the dir they just opened.
					}
				}
			}

		case key == ebiten.KeyArrowLeft:
			if st.Cursor < len(st.Entries) && st.Entries[st.Cursor].IsDir && st.Entries[st.Cursor].Expanded {
				st.Entries = fileexplorer.CollapseAt(st.Entries, st.Cursor)
			}

		case key == ebiten.KeyEnter:
			// Resolve the selected entry from search results or normal list.
			var selected *fileexplorer.Entry
			isSearch := len(st.SearchResults) > 0
			if isSearch {
				if st.Cursor >= 0 && st.Cursor < len(st.SearchResults) {
					selected = &st.SearchResults[st.Cursor]
				}
			} else if st.Cursor < len(st.Entries) {
				selected = &st.Entries[st.Cursor]
			}
			if selected == nil {
				break
			}

			// "." — send path, close. File — send path, close.
			if selected.Name == "." || !selected.IsDir {
				g.activeFocused().Term.SendBytes([]byte(selected.Path))
				g.closeFileExplorer()
				return
			}

			// ".." or directory in search results — navigate into it.
			if selected.Name == ".." || isSearch {
				st.Root = selected.Path
				entries, err := fileexplorer.BuildTree(selected.Path)
				if err == nil {
					st.Entries = entries
					st.SearchResults = nil
					st.SearchQuery = ""
					st.Cursor = 0
					st.ScrollOffset = 0
				}
				if isSearch {
					break
				}
			} else {
				// Normal mode directory — expand/collapse toggle.
				if selected.Expanded {
					st.Entries = fileexplorer.CollapseAt(st.Entries, st.Cursor)
				} else {
					entries, err := fileexplorer.ExpandAt(st.Entries, st.Cursor)
					if err == nil {
						st.Entries = entries
					}
				}
			}

		case key == ebiten.KeyC && !meta:
			if st.Cursor < len(st.Entries) {
				st.Clipboard = &fileexplorer.Clipboard{Op: "copy", Path: st.Entries[st.Cursor].Path}
			}

		case key == ebiten.KeyX && !meta:
			if st.Cursor < len(st.Entries) {
				st.Clipboard = &fileexplorer.Clipboard{Op: "cut", Path: st.Entries[st.Cursor].Path}
			}

		case key == ebiten.KeyP && !meta:
			if st.Clipboard == nil {
				break
			}
			dstDir := fileexplorer.CurrentDir(st.Entries, st.Cursor)
			var opErr error
			if st.Clipboard.Op == "cut" {
				opErr = fileexplorer.MovePath(st.Clipboard.Path, dstDir)
				if opErr == nil {
					st.Clipboard = nil
				}
			} else {
				opErr = fileexplorer.CopyPath(st.Clipboard.Path, dstDir)
			}
			if opErr != nil {
				st.StatusMsg = "Error: " + opErr.Error()
				st.StatusTimer = statusMessageFrames
			}
			g.reloadExplorerTree()

		case key == ebiten.KeyD && !meta:
			if st.Cursor < len(st.Entries) {
				path := st.Entries[st.Cursor].Path
				name := st.Entries[st.Cursor].Name
				st.ConfirmMsg = "Delete " + name + "?"
				captured := path
				st.ConfirmAction = func() {
					if err := fileexplorer.DeletePath(captured); err != nil {
						st.StatusMsg = "Error: " + err.Error()
						st.StatusTimer = statusMessageFrames
					}
					g.reloadExplorerTree()
				}
				st.ConfirmOpen = true
				// Reset confirm keys so edge detection fires cleanly on next frame.
				g.input.PrevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
				g.input.PrevKeys[ebiten.KeyY] = ebiten.IsKeyPressed(ebiten.KeyY)
			}

		case key == ebiten.KeyR && !meta:
			if st.Cursor < len(st.Entries) {
				st.Mode = renderer.ExplorerModeRename
				st.InputLabel = "Rename:"
				st.InputText = st.Entries[st.Cursor].Name
				st.InputCursorPos = len([]rune(st.InputText))
				g.input.PrevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
				g.input.PrevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)
			}

		case key == ebiten.KeyN && !meta && !shift:
			st.Mode = renderer.ExplorerModeNewFile
			st.InputLabel = "New file:"
			st.InputText = ""
			st.InputCursorPos = 0
			g.input.PrevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
			g.input.PrevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)

		case key == ebiten.KeyN && shift:
			st.Mode = renderer.ExplorerModeNewDir
			st.InputLabel = "New dir:"
			st.InputText = ""
			st.InputCursorPos = 0
			g.input.PrevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
			g.input.PrevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)

		case key == ebiten.KeyO && !meta:
			if st.Cursor < len(st.Entries) {
				e := st.Entries[st.Cursor]
				var cmd *exec.Cmd
				if e.IsDir {
					// Open directory directly in Finder.
					cmd = exec.Command("open", e.Path) // #nosec G204 — macOS open(1), path from file explorer tree
				} else {
					// Reveal file in Finder with parent selected.
					cmd = exec.Command("open", "-R", e.Path) // #nosec G204
				}
				if err := cmd.Start(); err != nil {
					log.Printf("explorer: open %s: %v", e.Path, err)
				}
			}

		case key == ebiten.KeySlash && !meta:
			// Enter search mode — position cursor at end of any existing query.
			st.SearchMode = true
			st.SearchCursorPos = len([]rune(st.SearchQuery))
			g.input.PrevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
			g.input.PrevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)
		}
	}
}

// handleExplorerConfirmInput handles Enter/Y in the confirm dialog.
// ESC is handled at the top of handleFileExplorerInput before this is called.
func (g *Game) handleExplorerConfirmInput() {
	st := &g.explorer.State
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyY} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		g.input.PrevKeys[key] = pressed
		if pressed && !wasPressed {
			if st.ConfirmAction != nil {
				st.ConfirmAction()
			}
			st.ConfirmOpen = false
			return
		}
	}
}

// handleExplorerSearchInput handles text input while in search mode.
func (g *Game) handleExplorerSearchInput() {
	st := &g.explorer.State
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)
	prevQuery := st.SearchQuery

	ti := &TextInput{Text: st.SearchQuery, CursorPos: st.SearchCursorPos}

	// Enter — accept and exit search mode.
	enterPressed := ebiten.IsKeyPressed(ebiten.KeyEnter)
	enterWas := g.input.PrevKeys[ebiten.KeyEnter]
	g.input.PrevKeys[ebiten.KeyEnter] = enterPressed
	if enterPressed && !enterWas {
		st.SearchMode = false
		st.SearchCursorPos = ti.CursorPos
		if len(st.SearchResults) > 0 && st.Cursor >= 0 && st.Cursor < len(st.SearchResults) {
			selected := st.SearchResults[st.Cursor]
			g.activeFocused().Term.SendBytes([]byte(selected.Path))
			g.closeFileExplorer()
		}
		return
	}

	ti.Update(&g.explorer.inputRepeat, meta, alt)

	st.SearchQuery = ti.Text
	st.SearchCursorPos = ti.CursorPos

	// If query changed, re-run search.
	if st.SearchQuery != prevQuery {
		if st.SearchQuery == "" {
			st.SearchResults = nil
			st.SearchCursorPos = 0
			st.Cursor = 0
		} else {
			st.SearchResults = fileexplorer.SearchCurrentLevel(st.Root, st.SearchQuery)
			st.Cursor = 0
		}
		st.ScrollOffset = 0
	}
}

// handleExplorerInputMode handles text input for rename/new-file/new-dir modes.
// ESC is handled at the top of handleFileExplorerInput before this is called.
func (g *Game) handleExplorerInputMode() {
	st := &g.explorer.State
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	ti := &TextInput{Text: st.InputText, CursorPos: st.InputCursorPos}

	// Enter — commit the operation.
	enterPressed := ebiten.IsKeyPressed(ebiten.KeyEnter)
	enterWas := g.input.PrevKeys[ebiten.KeyEnter]
	g.input.PrevKeys[ebiten.KeyEnter] = enterPressed
	if enterPressed && !enterWas {
		st.InputText = ti.Text
		st.InputCursorPos = ti.CursorPos
		g.executeExplorerInputMode()
		return
	}

	ti.Update(&g.explorer.inputRepeat, meta, alt)

	st.InputText = ti.Text
	st.InputCursorPos = ti.CursorPos
}

// executeExplorerInputMode commits the rename/new-file/new-dir operation.
func (g *Game) executeExplorerInputMode() {
	st := &g.explorer.State
	name := st.InputText
	if name == "" {
		st.Mode = renderer.ExplorerModeNormal
		return
	}
	dstDir := fileexplorer.CurrentDir(st.Entries, st.Cursor)

	switch st.Mode {
	case renderer.ExplorerModeRename:
		if st.Cursor < len(st.Entries) {
			oldPath := st.Entries[st.Cursor].Path
			_, err := fileexplorer.RenamePath(oldPath, name)
			if err != nil {
				st.StatusMsg = "Error: " + err.Error()
				st.StatusTimer = statusMessageFrames
			}
		}
	case renderer.ExplorerModeNewFile:
		_, err := fileexplorer.CreateFile(dstDir, name)
		if err != nil {
			st.StatusMsg = "Error: " + err.Error()
			st.StatusTimer = statusMessageFrames
		}
	case renderer.ExplorerModeNewDir:
		_, err := fileexplorer.CreateDir(dstDir, name)
		if err != nil {
			st.StatusMsg = "Error: " + err.Error()
			st.StatusTimer = statusMessageFrames
		}
	}

	st.Mode = renderer.ExplorerModeNormal
	st.InputText = ""
	g.reloadExplorerTree()
}

// handleExplorerClick moves the cursor to the clicked row or toggles dir expand.
func (g *Game) handleExplorerClick(mx, my int, panelRect image.Rectangle) {
	st := &g.explorer.State
	if st.RowH <= 0 {
		return
	}
	// Adjust header height when search is shown
	headerHeight := g.font.CellH + 6
	if st.SearchMode || st.SearchQuery != "" {
		headerHeight = g.font.CellH*2 + 8
	}
	contentTop := panelRect.Min.Y + headerHeight
	relY := my - contentTop + st.ScrollOffset
	if relY < 0 {
		return
	}
	visualIdx := relY / st.RowH

	// When search results are visible, visual rows map to SearchResults.
	if len(st.SearchResults) > 0 {
		if visualIdx < 0 || visualIdx >= len(st.SearchResults) {
			return
		}
		st.Cursor = visualIdx
		g.render.Dirty = true
		return
	}

	// Convert visual index to actual index when filtering
	actualIdx := visualIdx
	if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		if visualIdx >= 0 && visualIdx < len(st.FilteredIndices) {
			actualIdx = st.FilteredIndices[visualIdx]
		} else {
			return
		}
	} else if actualIdx < 0 || actualIdx >= len(st.Entries) {
		return
	}

	if actualIdx == st.Cursor && st.Entries[actualIdx].IsDir {
		if st.Entries[actualIdx].Expanded {
			st.Entries = fileexplorer.CollapseAt(st.Entries, actualIdx)
		} else {
			entries, err := fileexplorer.ExpandAt(st.Entries, actualIdx)
			if err == nil {
				st.Entries = entries
			}
		}
		if st.SearchQuery != "" {
			st.SearchQuery = ""
			st.FilteredIndices = nil
		}
	}
	st.Cursor = actualIdx
	g.render.Dirty = true
}
