package main

import (
	"image"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/help"
	"github.com/studiowebux/zurm/renderer"
)

// --- Tab context menu ---

func (g *Game) openTabContextMenu(px, py int) {
	if g.menuState.Open {
		g.renderer.ClearPaneCache()
	}
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)

	// Determine which tab was right-clicked.
	clickedTab := g.activeTab
	numTabs := len(g.tabs)
	if numTabs > 0 {
		maxTabW := g.cfg.Tabs.MaxWidthChars * g.font.CellW
		tabW := physW / numTabs
		if tabW > maxTabW {
			tabW = maxTabW
		}
		if tabW > 0 {
			idx := px / tabW
			if idx >= 0 && idx < numTabs {
				clickedTab = idx
			}
		}
	}

	items := []help.MenuItem{
		{Label: "Rename Tab", Action: func() { g.startRenameTab(clickedTab) }},
		{Label: "Edit Tab Note", Shortcut: "Cmd+Shift+N", Action: func() { g.startNoteEdit(clickedTab) }},
		{Label: "New Tab", Shortcut: "Cmd+T", Action: g.newTab},
		{Separator: true},
		{Label: "Close Tab", Shortcut: "Cmd+W", Action: g.closeActiveTab},
	}
	rect := g.renderer.BuildMenuRect(items, px, py, physW, physH)
	g.menuState = renderer.MenuState{
		Open:         true,
		Items:        items,
		Rect:         rect,
		HoverIdx:     -1,
		SubParentIdx: -1,
		SubHoverIdx:  -1,
	}
	g.screenDirty = true
}

// --- Context menu ---

// buildContextMenu returns the default top-level menu items with action closures.
func (g *Game) buildContextMenu() []help.MenuItem {
	return []help.MenuItem{
		{Label: "Copy", Shortcut: "Cmd+C", Action: g.copySelection},
		{Label: "Paste", Shortcut: "Cmd+V", Action: g.handlePaste},
		{Separator: true},
		{Label: "Panes", Children: []help.MenuItem{
			{Label: "Split Horizontal", Shortcut: "Cmd+D", Action: g.splitH},
			{Label: "Split Vertical", Shortcut: "Cmd+Shift+D", Action: g.splitV},
			{Label: "Close Pane", Shortcut: "Cmd+W", Action: func() {
				if len(g.layout.Leaves()) <= 1 {
					g.closeActiveTab()
				} else {
					g.closePane(g.focused)
				}
			}},
			{Label: "Rename Pane", Action: g.startRenamePane},
			{Label: "Detach Pane to Tab", Action: g.detachPaneToTab},
			{Label: "Move Pane to Next Tab", Action: g.mergePaneToNextTab},
			{Label: "Move Pane to Previous Tab", Action: g.mergePaneToPrevTab},
			{Separator: true},
			{Label: "Focus Left", Shortcut: "Cmd+\u2190", Action: func() { g.focusDir(-1, 0) }},
			{Label: "Focus Right", Shortcut: "Cmd+\u2192", Action: func() { g.focusDir(1, 0) }},
			{Label: "Focus Up", Shortcut: "Cmd+\u2191", Action: func() { g.focusDir(0, -1) }},
			{Label: "Focus Down", Shortcut: "Cmd+\u2193", Action: func() { g.focusDir(0, 1) }},
		}},
		{Separator: true},
		{Label: "Scroll", Children: []help.MenuItem{
			{Label: "Scroll Up", Shortcut: "Shift+PgUp", Action: func() {
				half := g.cfg.Window.Rows / 2
				if half < 1 {
					half = 1
				}
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ScrollViewUp(half)
				g.focused.Term.Buf.Unlock()
			}},
			{Label: "Scroll Down", Shortcut: "Shift+PgDn", Action: func() {
				half := g.cfg.Window.Rows / 2
				if half < 1 {
					half = 1
				}
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ScrollViewDown(half)
				g.focused.Term.Buf.Unlock()
			}},
			{Label: "Clear Scrollback", Shortcut: "Cmd+K", Action: func() {
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ClearScrollback()
				g.focused.Term.Buf.Unlock()
			}},
		}},
		{Separator: true},
		{Label: "Rename Tab", Action: func() { g.startRenameTab(g.activeTab) }},
		{Label: "Edit Tab Note", Shortcut: "Cmd+Shift+N", Action: func() { g.startNoteEdit(g.activeTab) }},
		{Separator: true},
		{Label: "Pin Mode", Shortcut: "Cmd+G", Action: func() { g.pinMode = true; g.statusBarState.PinMode = true }},
		{Label: "Show Keybindings", Shortcut: "Cmd+/", Action: g.toggleOverlay},
		{Label: "Command Palette", Shortcut: "Cmd+P", Action: g.openPalette},
	}
}

// openContextMenu opens the context menu at physical pixel position (px, py).
func (g *Game) openContextMenu(px, py int) {
	// If menu was already open, the old pixels on offscreen must be erased.
	if g.menuState.Open {
		g.renderer.ClearPaneCache()
	}
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	items := g.buildContextMenu()
	rect := g.renderer.BuildMenuRect(items, px, py, physW, physH)
	g.menuState = renderer.MenuState{
		Open:         true,
		Items:        items,
		Rect:         rect,
		HoverIdx:     -1,
		SubParentIdx: -1,
		SubHoverIdx:  -1,
	}
	g.screenDirty = true
}

// closeMenu resets all menu state and forces pane pixels under the menu to be redrawn.
func (g *Game) closeMenu() {
	g.menuState = renderer.MenuState{}
	g.renderer.ClearPaneCache()
	g.screenDirty = true
}

// updateMenuHover computes which menu item is under the cursor at (px, py)
// and updates submenu state accordingly.
func (g *Game) updateMenuHover(px, py int) {
	idx := g.renderer.MenuItemAt(&g.menuState, px, py)
	g.menuState.HoverIdx = idx

	if idx >= 0 && len(g.menuState.Items[idx].Children) > 0 {
		// Cursor on a parent item: open or refresh the submenu.
		if !g.menuState.SubOpen || g.menuState.SubParentIdx != idx {
			physW := int(float64(g.winW) * g.dpi)
			physH := int(float64(g.winH) * g.dpi)
			subRect := g.renderer.BuildSubRect(&g.menuState, idx, physW, physH)
			g.menuState.SubOpen = true
			g.menuState.SubItems = g.menuState.Items[idx].Children
			g.menuState.SubRect = subRect
			g.menuState.SubParentIdx = idx
			g.menuState.SubHoverIdx = -1
		}
		return
	}

	if g.menuState.SubOpen {
		if image.Pt(px, py).In(g.menuState.SubRect) {
			// Cursor moved into the submenu panel: update sub-hover.
			g.menuState.SubHoverIdx = g.renderer.SubItemAt(&g.menuState, px, py)
		} else if idx >= 0 {
			// Cursor moved to a non-parent top-level item: close submenu.
			g.menuState.SubOpen = false
		}
		// If idx == -1 (gap/padding), keep submenu visible.
	}
}

// handleMenuKeys processes keyboard input when the context menu is open.
func (g *Game) handleMenuKeys() {
	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeMenu()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	menuKeys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
		ebiten.KeyEnter, ebiten.KeyNumpadEnter,
	}
	for _, key := range menuKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch key {
			case ebiten.KeyArrowUp:
				if g.menuState.SubOpen {
					g.menuState.SubHoverIdx = g.nextNonSep(g.menuState.SubItems, g.menuState.SubHoverIdx, -1)
				} else {
					g.menuState.HoverIdx = g.nextNonSep(g.menuState.Items, g.menuState.HoverIdx, -1)
				}
			case ebiten.KeyArrowDown:
				if g.menuState.SubOpen {
					g.menuState.SubHoverIdx = g.nextNonSep(g.menuState.SubItems, g.menuState.SubHoverIdx, +1)
				} else {
					g.menuState.HoverIdx = g.nextNonSep(g.menuState.Items, g.menuState.HoverIdx, +1)
				}
			case ebiten.KeyArrowRight:
				if !g.menuState.SubOpen && g.menuState.HoverIdx >= 0 &&
					len(g.menuState.Items[g.menuState.HoverIdx].Children) > 0 {
					physW := int(float64(g.winW) * g.dpi)
					physH := int(float64(g.winH) * g.dpi)
					idx := g.menuState.HoverIdx
					subRect := g.renderer.BuildSubRect(&g.menuState, idx, physW, physH)
					g.menuState.SubOpen = true
					g.menuState.SubItems = g.menuState.Items[idx].Children
					g.menuState.SubRect = subRect
					g.menuState.SubParentIdx = idx
					g.menuState.SubHoverIdx = g.nextNonSep(g.menuState.SubItems, -1, +1)
				}
			case ebiten.KeyArrowLeft:
				if g.menuState.SubOpen {
					g.menuState.SubOpen = false
				}
			case ebiten.KeyEnter, ebiten.KeyNumpadEnter:
				g.menuExecute()
			}
		}
		g.prevKeys[key] = pressed
	}
}

// menuExecute runs the action for the currently highlighted menu item.
func (g *Game) menuExecute() {
	if g.menuState.SubOpen && g.menuState.SubHoverIdx >= 0 {
		item := g.menuState.SubItems[g.menuState.SubHoverIdx]
		if item.Action != nil {
			g.closeMenu()
			item.Action()
		}
		return
	}
	if g.menuState.HoverIdx >= 0 {
		item := g.menuState.Items[g.menuState.HoverIdx]
		if item.Action != nil {
			g.closeMenu()
			item.Action()
		}
	}
}

// nextNonSep returns the next non-separator item index in direction dir (+1/-1),
// starting from cur (-1 means before the first element).
func (g *Game) nextNonSep(items []help.MenuItem, cur, dir int) int {
	n := len(items)
	if n == 0 {
		return -1
	}
	start := cur
	if start == -1 {
		if dir > 0 {
			start = n - 1 // wrap: -1 + 1 = 0 after mod
		} else {
			start = 0
		}
	}
	for i := 1; i <= n; i++ {
		idx := ((start + dir*i) % n + n) % n
		if !items[idx].Separator {
			return idx
		}
	}
	return cur
}

// --- Keybinding overlay ---

// toggleOverlay opens the overlay if closed, closes it if open.
func (g *Game) toggleOverlay() {
	if g.overlayState.Open {
		g.overlayState = renderer.OverlayState{}
	} else {
		g.overlayState = renderer.OverlayState{Open: true}
		g.closeMenu()
		g.closePalette()
	}
	g.screenDirty = true
}

// handleOverlayInput processes keyboard input while the overlay is open.
// Printable characters update the search query; Backspace removes the last
// character; Escape clears the query or closes the overlay; Cmd+/ closes it.
func (g *Game) handleOverlayInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// ESC: clear search query if non-empty, otherwise close. Uses inpututil to
	// catch sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if g.overlayState.SearchQuery != "" {
			g.overlayState.SearchQuery = ""
			g.overlayState.ScrollOffset = 0
		} else {
			g.overlayState = renderer.OverlayState{}
		}
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Scroll and Cmd+/ — edge-triggered only.
	scrollKeys := []ebiten.Key{
		ebiten.KeySlash,
		ebiten.KeyArrowUp,
		ebiten.KeyArrowDown,
		ebiten.KeyPageUp,
		ebiten.KeyPageDown,
	}
	prevQuery := g.overlayState.SearchQuery
	for _, key := range scrollKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch {
			case key == ebiten.KeySlash && meta:
				g.overlayState = renderer.OverlayState{}
			case key == ebiten.KeyArrowUp:
				g.overlayState.ScrollOffset -= g.overlayState.RowH
			case key == ebiten.KeyArrowDown:
				g.overlayState.ScrollOffset += g.overlayState.RowH
			case key == ebiten.KeyPageUp:
				g.overlayState.ScrollOffset -= 10 * g.overlayState.RowH
			case key == ebiten.KeyPageDown:
				g.overlayState.ScrollOffset += 10 * g.overlayState.RowH
			}
		}
		g.prevKeys[key] = pressed
	}
	g.prevKeys[ebiten.KeyMeta] = meta

	ti := &TextInput{Text: g.overlayState.SearchQuery, CursorPos: g.overlayState.SearchCursorPos}
	ti.Update(&g.overlayRepeat, meta, alt)
	g.overlayState.SearchQuery = ti.Text
	g.overlayState.SearchCursorPos = ti.CursorPos

	// Reset scroll when search query changes so new results start at top.
	if g.overlayState.SearchQuery != prevQuery {
		g.overlayState.ScrollOffset = 0
	}

	// Clamp scroll offset.
	if g.overlayState.ScrollOffset < 0 {
		g.overlayState.ScrollOffset = 0
	}
	if g.overlayState.ScrollOffset > g.overlayState.MaxScroll {
		g.overlayState.ScrollOffset = g.overlayState.MaxScroll
	}
}

// --- Command palette ---

// openPalette opens the command palette, closing any conflicting surfaces.
func (g *Game) openPalette() {
	g.paletteState = renderer.PaletteState{Open: true}
	g.overlayState = renderer.OverlayState{}
	g.closeMenu()
	g.paletteRepeatActive = false
	g.prevKeys[ebiten.KeyArrowUp] = ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	g.prevKeys[ebiten.KeyArrowDown] = ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	g.screenDirty = true
}

// closePalette closes the command palette.
func (g *Game) closePalette() {
	g.paletteState = renderer.PaletteState{}
	g.screenDirty = true
}

// buildPalette constructs the parallel entries and actions slices for the command palette.
// Called once after the Game is initialized so actions can reference g methods.
func (g *Game) buildPalette() {
	cmds := help.AllCommands()
	entries := make([]renderer.PaletteEntry, len(cmds))
	for i, c := range cmds {
		entries[i] = renderer.PaletteEntry{Name: c.Name, Shortcut: c.Shortcut}
	}

	actions := []func(){
		// Tabs
		g.newTab,
		g.closeActiveTab,
		g.nextTab,
		g.prevTab,
		func() { g.switchTab(0) },
		func() { g.switchTab(1) },
		func() { g.switchTab(2) },
		func() { g.startNoteEdit(g.activeTab) },
		g.detachPaneToTab,
		g.mergePaneToNextTab,
		g.mergePaneToPrevTab,
		// Panes
		g.splitH,
		g.splitV,
		func() { g.focusDir(-1, 0) },
		func() { g.focusDir(1, 0) },
		func() { g.focusDir(0, -1) },
		func() { g.focusDir(0, 1) },
		g.toggleZoom,
		func() { g.resizePane(-1, 0) },
		func() { g.resizePane(1, 0) },
		func() { g.resizePane(0, -1) },
		func() { g.resizePane(0, 1) },
		g.startRenamePane,
		// Scroll
		func() {
			g.focused.Term.Buf.Lock()
			vo := g.focused.Term.Buf.ViewOffset - g.focused.Rows/2
			if vo < 0 {
				vo = 0
			}
			g.focused.Term.Buf.SetViewOffset(vo)
			g.focused.Term.Buf.Unlock()
		},
		func() {
			g.focused.Term.Buf.Lock()
			g.focused.Term.Buf.SetViewOffset(g.focused.Term.Buf.ViewOffset + g.focused.Rows/2)
			g.focused.Term.Buf.Unlock()
		},
		func() {
			g.focused.Term.Buf.Lock()
			g.focused.Term.Buf.ClearScrollback()
			g.focused.Term.Buf.Unlock()
		},
		// Copy / Paste
		g.copySelection,
		g.handlePaste,
		// Search
		func() {
			g.search.Open()
			g.screenDirty = true
			if g.focused != nil {
				g.focused.Term.Buf.BumpRenderGen()
			}
		},
		// File Explorer
		g.openFileExplorer,
		// Pins
		func() { g.pinMode = true; g.statusBarState.PinMode = true },
		// Tab Switcher
		g.openTabSwitcher,
		// Tab Search
		g.openTabSearch,
		// Blocks
		func() {
			g.blocksEnabled = !g.blocksEnabled
			g.renderer.BlocksEnabled = g.blocksEnabled
			if g.blocksEnabled {
				g.flashStatus("Command blocks: on")
			} else {
				g.flashStatus("Command blocks: off")
			}
		},
		g.installShellHooks,
		// Stats
		func() {
			g.statsState.Open = !g.statsState.Open
			if g.statsState.Open {
				g.collectStats()
				g.flashStatus("Stats: on")
			} else {
				g.renderer.SetLayoutDirty()
				g.renderer.ClearPaneCache()
				g.flashStatus("Stats: off")
			}
		},
		// Session
		g.manualSaveSession,
		// Server (Mode B)
		g.newServerTab,
		g.splitHServer,
		g.splitVServer,
		g.attachServerSession,
		// Recording
		func() { g.screenshotPending = true; g.screenDirty = true },
		g.toggleRecording,
		// Help
		g.toggleOverlay,
		g.openPalette,
		g.openMarkdownViewer,
		g.openURLInput,
		g.sendViewerToPane,
		// Config
		g.reloadConfig,
		// App
		func() { os.Exit(0) },
	}

	// Append dynamic theme entries from discovered theme files.
	for _, name := range config.ListThemes() {
		themeName := name // capture for closure
		entries = append(entries, renderer.PaletteEntry{Name: "Theme: " + themeName, Shortcut: ""})
		actions = append(actions, func() { g.switchTheme(themeName) })
	}

	g.paletteEntries = entries
	g.paletteActions = actions
}

// handlePaletteInput processes keyboard input while the command palette is open.
func (g *Game) handlePaletteInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	filtered, origIdx := renderer.FilterPalette(g.paletteEntries, g.paletteState.Query)

	now := time.Now()

	// Arrow keys with OS-style repeat (delay then interval).
	if g.updatePaletteRepeat(ebiten.KeyArrowUp, now) && g.paletteState.Cursor > 0 {
		g.paletteState.Cursor--
	}
	if g.updatePaletteRepeat(ebiten.KeyArrowDown, now) && g.paletteState.Cursor < len(filtered)-1 {
		g.paletteState.Cursor++
	}

	// ESC: clear query if non-empty, otherwise close. Uses inpututil to catch
	// sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if g.paletteState.Query != "" {
			g.paletteState.Query = ""
			g.paletteState.Cursor = 0
			g.paletteState.CursorPos = 0
		} else {
			g.closePalette()
		}
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Enter and Cmd+P — edge-triggered only.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyP} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if !pressed || wasPressed {
			continue
		}
		switch {
		case key == ebiten.KeyEnter:
			if g.paletteState.Cursor < len(origIdx) {
				orig := origIdx[g.paletteState.Cursor]
				if orig < len(g.paletteActions) && g.paletteActions[orig] != nil {
					g.closePalette()
					g.paletteActions[orig]()
				}
			}
		case key == ebiten.KeyP && meta:
			g.closePalette()
		}
	}
	g.prevKeys[ebiten.KeyMeta] = meta

	prevQuery := g.paletteState.Query
	ti := &TextInput{Text: g.paletteState.Query, CursorPos: g.paletteState.CursorPos}
	ti.Update(&g.paletteInputRepeat, meta, alt)
	g.paletteState.Query = ti.Text
	g.paletteState.CursorPos = ti.CursorPos
	if g.paletteState.Query != prevQuery {
		g.paletteState.Cursor = 0
	}
}

// updatePaletteRepeat implements OS-style key repeat for the command palette arrow keys.
func (g *Game) updatePaletteRepeat(key ebiten.Key, now time.Time) bool {
	pressed := ebiten.IsKeyPressed(key)
	was := g.prevKeys[key]
	g.prevKeys[key] = pressed

	if !pressed {
		if g.paletteRepeatActive && g.paletteRepeatKey == key {
			g.paletteRepeatActive = false
		}
		return false
	}
	if !was {
		g.paletteRepeatKey = key
		g.paletteRepeatActive = true
		g.paletteRepeatStart = now
		g.paletteRepeatLast = now
		return true
	}
	if g.paletteRepeatActive && g.paletteRepeatKey == key &&
		now.Sub(g.paletteRepeatStart) >= keyRepeatDelay &&
		now.Sub(g.paletteRepeatLast) >= keyRepeatInterval {
		g.paletteRepeatLast = now
		return true
	}
	return false
}

// --- Confirm dialog ---

// showConfirm opens the close-confirmation dialog with the given message and
// registers the action to execute if the user confirms.
func (g *Game) showConfirm(msg string, action func()) {
	g.confirmState = renderer.ConfirmState{Open: true, Message: msg}
	g.confirmPendingAction = action
	g.screenDirty = true
}

// handleConfirmInput processes keyboard input while the confirm dialog is open.
// Enter or Y executes the pending action; Escape or N cancels.
func (g *Game) handleConfirmInput() {
	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyN) {
		g.confirmState = renderer.ConfirmState{}
		g.confirmPendingAction = nil
		g.prevKeys[ebiten.KeyEscape] = true
		g.screenDirty = true
		return
	}

	for _, key := range []ebiten.Key{
		ebiten.KeyEnter, ebiten.KeyNumpadEnter, ebiten.KeyY,
	} {
		pressed := ebiten.IsKeyPressed(key)
		was := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if pressed && !was {
			if g.confirmPendingAction != nil {
				g.confirmPendingAction()
			}
			g.confirmState = renderer.ConfirmState{}
			g.confirmPendingAction = nil
			g.screenDirty = true
			return
		}
	}
}
