package main

import (
	"fmt"
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
	if g.overlays.Menu.Open {
		g.renderer.ClearPaneCache()
	}
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)

	// Determine which tab was right-clicked.
	clickedTab := g.tabMgr.ActiveIdx
	numTabs := len(g.tabMgr.Tabs)
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

	items := []renderer.OverlayMenuItem{
		{Label: "Rename Tab", Action: func() { g.startRenameTab(clickedTab) }},
		{Label: "Edit Tab Note", Shortcut: "Cmd+Shift+N", Action: func() { g.startNoteEdit(clickedTab) }},
		{Label: "New Tab", Shortcut: "Cmd+T", Action: g.newTab},
		{Separator: true},
		{Label: "Close Tab", Shortcut: "Cmd+W", Action: g.closeActiveTab},
	}
	rect := g.renderer.BuildMenuRect(items, px, py, physW, physH)
	g.overlays.Menu = renderer.MenuState{
		Open:         true,
		Items:        items,
		Rect:         rect,
		HoverIdx:     -1,
		SubParentIdx: -1,
		SubHoverIdx:  -1,
	}
	g.render.Dirty = true
}

// --- Context menu ---

// buildContextMenu returns the default top-level menu items with action closures.
func (g *Game) buildContextMenu() []renderer.OverlayMenuItem {
	return []renderer.OverlayMenuItem{
		{Label: "Copy", Shortcut: "Cmd+C", Action: g.copySelection},
		{Label: "Paste", Shortcut: "Cmd+V", Action: g.handlePaste},
		{Separator: true},
		{Label: "Panes", Children: []renderer.OverlayMenuItem{
			{Label: "Split Horizontal", Shortcut: "Cmd+D", Action: g.splitH},
			{Label: "Split Vertical", Shortcut: "Cmd+Shift+D", Action: g.splitV},
			{Label: "Close Pane", Shortcut: "Cmd+W", Action: func() {
				if len(g.activeLayout().Leaves()) <= 1 {
					g.closeActiveTab()
				} else {
					g.closePane(g.activeFocused())
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
		{Label: "Scroll", Children: []renderer.OverlayMenuItem{
			{Label: "Scroll Up", Shortcut: "Shift+PgUp", Action: func() {
				half := g.cfg.Window.Rows / 2
				if half < 1 {
					half = 1
				}
				g.activeFocused().Term.Buf.Lock()
				g.activeFocused().Term.Buf.ScrollViewUp(half)
				g.activeFocused().Term.Buf.Unlock()
			}},
			{Label: "Scroll Down", Shortcut: "Shift+PgDn", Action: func() {
				half := g.cfg.Window.Rows / 2
				if half < 1 {
					half = 1
				}
				g.activeFocused().Term.Buf.Lock()
				g.activeFocused().Term.Buf.ScrollViewDown(half)
				g.activeFocused().Term.Buf.Unlock()
			}},
			{Label: "Clear Scrollback", Shortcut: "Cmd+K", Action: func() {
				g.activeFocused().Term.Buf.Lock()
				g.activeFocused().Term.Buf.ClearScrollback()
				g.activeFocused().Term.Buf.Unlock()
			}},
		}},
		{Separator: true},
		{Label: "Rename Tab", Action: func() { g.startRenameTab(g.tabMgr.ActiveIdx) }},
		{Label: "Edit Tab Note", Shortcut: "Cmd+Shift+N", Action: func() { g.startNoteEdit(g.tabMgr.ActiveIdx) }},
		{Separator: true},
		{Label: "Pin Mode", Shortcut: "Cmd+G", Action: func() { g.tabMgr.PinMode = true; g.status.Bar.PinMode = true }},
		{Label: "Show Keybindings", Shortcut: "Cmd+/", Action: g.toggleOverlay},
		{Label: "Command Palette", Shortcut: "Cmd+P", Action: g.openPalette},
	}
}

// openContextMenu opens the context menu at physical pixel position (px, py).
func (g *Game) openContextMenu(px, py int) {
	// If menu was already open, the old pixels on offscreen must be erased.
	if g.overlays.Menu.Open {
		g.renderer.ClearPaneCache()
	}
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	items := g.buildContextMenu()
	rect := g.renderer.BuildMenuRect(items, px, py, physW, physH)
	g.overlays.Menu = renderer.MenuState{
		Open:         true,
		Items:        items,
		Rect:         rect,
		HoverIdx:     -1,
		SubParentIdx: -1,
		SubHoverIdx:  -1,
	}
	g.render.Dirty = true
}

// closeMenu resets all menu state and forces pane pixels under the menu to be redrawn.
func (g *Game) closeMenu() {
	g.overlays.Menu = renderer.MenuState{}
	g.renderer.ClearPaneCache()
	g.render.Dirty = true
}

// updateMenuHover computes which menu item is under the cursor at (px, py)
// and updates submenu state accordingly.
func (g *Game) updateMenuHover(px, py int) {
	idx := g.renderer.MenuItemAt(&g.overlays.Menu, px, py)
	g.overlays.Menu.HoverIdx = idx

	if idx >= 0 && len(g.overlays.Menu.Items[idx].Children) > 0 {
		// Cursor on a parent item: open or refresh the submenu.
		if !g.overlays.Menu.SubOpen || g.overlays.Menu.SubParentIdx != idx {
			physW := int(float64(g.winW) * g.dpi)
			physH := int(float64(g.winH) * g.dpi)
			subRect := g.renderer.BuildSubRect(&g.overlays.Menu, idx, physW, physH)
			g.overlays.Menu.SubOpen = true
			g.overlays.Menu.SubItems = g.overlays.Menu.Items[idx].Children
			g.overlays.Menu.SubRect = subRect
			g.overlays.Menu.SubParentIdx = idx
			g.overlays.Menu.SubHoverIdx = -1
		}
		return
	}

	if g.overlays.Menu.SubOpen {
		if image.Pt(px, py).In(g.overlays.Menu.SubRect) {
			// Cursor moved into the submenu panel: update sub-hover.
			g.overlays.Menu.SubHoverIdx = g.renderer.SubItemAt(&g.overlays.Menu, px, py)
		} else if idx >= 0 {
			// Cursor moved to a non-parent top-level item: close submenu.
			g.overlays.Menu.SubOpen = false
		}
		// If idx == -1 (gap/padding), keep submenu visible.
	}
}

// handleMenuKeys processes keyboard input when the context menu is open.
func (g *Game) handleMenuKeys() {
	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeMenu()
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	menuKeys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
		ebiten.KeyEnter, ebiten.KeyNumpadEnter,
	}
	for _, key := range menuKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		if pressed && !wasPressed {
			switch key {
			case ebiten.KeyArrowUp:
				if g.overlays.Menu.SubOpen {
					g.overlays.Menu.SubHoverIdx = g.nextNonSep(g.overlays.Menu.SubItems, g.overlays.Menu.SubHoverIdx, -1)
				} else {
					g.overlays.Menu.HoverIdx = g.nextNonSep(g.overlays.Menu.Items, g.overlays.Menu.HoverIdx, -1)
				}
			case ebiten.KeyArrowDown:
				if g.overlays.Menu.SubOpen {
					g.overlays.Menu.SubHoverIdx = g.nextNonSep(g.overlays.Menu.SubItems, g.overlays.Menu.SubHoverIdx, +1)
				} else {
					g.overlays.Menu.HoverIdx = g.nextNonSep(g.overlays.Menu.Items, g.overlays.Menu.HoverIdx, +1)
				}
			case ebiten.KeyArrowRight:
				if !g.overlays.Menu.SubOpen && g.overlays.Menu.HoverIdx >= 0 &&
					len(g.overlays.Menu.Items[g.overlays.Menu.HoverIdx].Children) > 0 {
					physW := int(float64(g.winW) * g.dpi)
					physH := int(float64(g.winH) * g.dpi)
					idx := g.overlays.Menu.HoverIdx
					subRect := g.renderer.BuildSubRect(&g.overlays.Menu, idx, physW, physH)
					g.overlays.Menu.SubOpen = true
					g.overlays.Menu.SubItems = g.overlays.Menu.Items[idx].Children
					g.overlays.Menu.SubRect = subRect
					g.overlays.Menu.SubParentIdx = idx
					g.overlays.Menu.SubHoverIdx = g.nextNonSep(g.overlays.Menu.SubItems, -1, +1)
				}
			case ebiten.KeyArrowLeft:
				if g.overlays.Menu.SubOpen {
					g.overlays.Menu.SubOpen = false
				}
			case ebiten.KeyEnter, ebiten.KeyNumpadEnter:
				g.menuExecute()
			}
		}
		g.input.PrevKeys[key] = pressed
	}
}

// menuExecute runs the action for the currently highlighted menu item.
func (g *Game) menuExecute() {
	if g.overlays.Menu.SubOpen && g.overlays.Menu.SubHoverIdx >= 0 {
		item := g.overlays.Menu.SubItems[g.overlays.Menu.SubHoverIdx]
		if item.Action != nil {
			g.closeMenu()
			item.Action()
		}
		return
	}
	if g.overlays.Menu.HoverIdx >= 0 {
		item := g.overlays.Menu.Items[g.overlays.Menu.HoverIdx]
		if item.Action != nil {
			g.closeMenu()
			item.Action()
		}
	}
}

// nextNonSep returns the next non-separator item index in direction dir (+1/-1),
// starting from cur (-1 means before the first element).
func (g *Game) nextNonSep(items []renderer.OverlayMenuItem, cur, dir int) int {
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

// convertBindings converts help.KeyBinding slices to renderer.OverlayKeyBinding.
func convertBindings(src []help.KeyBinding) []renderer.OverlayKeyBinding {
	out := make([]renderer.OverlayKeyBinding, len(src))
	for i, b := range src {
		out[i] = renderer.OverlayKeyBinding{Category: b.Category, Key: b.Key, Description: b.Description}
	}
	return out
}

// toggleOverlay opens the overlay if closed, closes it if open.
func (g *Game) toggleOverlay() {
	if g.overlays.Help.Open {
		g.overlays.Help = renderer.OverlayState{}
	} else {
		all := convertBindings(help.AllBindings())
		g.overlays.Help = renderer.OverlayState{Open: true, AllBindings: all, FilteredBindings: all}
		g.closeMenu()
		g.closePalette()
	}
	g.render.Dirty = true
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
		if g.overlays.Help.SearchQuery != "" {
			g.overlays.Help.SearchQuery = ""
			g.overlays.Help.ScrollOffset = 0
		} else {
			g.overlays.Help = renderer.OverlayState{}
		}
		g.render.Dirty = true
		g.input.PrevKeys[ebiten.KeyEscape] = true
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
	prevQuery := g.overlays.Help.SearchQuery
	for _, key := range scrollKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		if pressed && !wasPressed {
			switch {
			case key == ebiten.KeySlash && meta:
				g.overlays.Help = renderer.OverlayState{}
			case key == ebiten.KeyArrowUp:
				g.overlays.Help.ScrollOffset -= g.overlays.Help.RowH
			case key == ebiten.KeyArrowDown:
				g.overlays.Help.ScrollOffset += g.overlays.Help.RowH
			case key == ebiten.KeyPageUp:
				g.overlays.Help.ScrollOffset -= 10 * g.overlays.Help.RowH
			case key == ebiten.KeyPageDown:
				g.overlays.Help.ScrollOffset += 10 * g.overlays.Help.RowH
			}
		}
		g.input.PrevKeys[key] = pressed
	}
	g.input.PrevKeys[ebiten.KeyMeta] = meta

	ti := &TextInput{Text: g.overlays.Help.SearchQuery, CursorPos: g.overlays.Help.SearchCursorPos}
	ti.Update(&g.repeats.Overlay, meta, alt)
	g.overlays.Help.SearchQuery = ti.Text
	g.overlays.Help.SearchCursorPos = ti.CursorPos

	// Reset scroll and re-filter when search query changes.
	if g.overlays.Help.SearchQuery != prevQuery {
		g.overlays.Help.ScrollOffset = 0
		if g.overlays.Help.SearchQuery == "" {
			g.overlays.Help.FilteredBindings = g.overlays.Help.AllBindings
		} else {
			g.overlays.Help.FilteredBindings = convertBindings(help.FilterBindings(g.overlays.Help.SearchQuery))
		}
	}

	// Clamp scroll offset.
	if g.overlays.Help.ScrollOffset < 0 {
		g.overlays.Help.ScrollOffset = 0
	}
	if g.overlays.Help.ScrollOffset > g.overlays.Help.MaxScroll {
		g.overlays.Help.ScrollOffset = g.overlays.Help.MaxScroll
	}
}

// --- Command palette ---

// openPalette opens the command palette, closing any conflicting surfaces.
func (g *Game) openPalette() {
	g.palette.Open()
	g.overlays.Help = renderer.OverlayState{}
	g.closeMenu()
	g.input.PrevKeys[ebiten.KeyArrowUp] = ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	g.input.PrevKeys[ebiten.KeyArrowDown] = ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	g.render.Dirty = true
}

// closePalette closes the command palette.
func (g *Game) closePalette() {
	g.palette.Close()
	g.render.Dirty = true
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
		g.parkActiveTab,
		g.closeActiveTab,
		g.nextTab,
		g.prevTab,
		func() { g.switchTab(0) },
		func() { g.switchTab(1) },
		func() { g.switchTab(2) },
		func() { g.startNoteEdit(g.tabMgr.ActiveIdx) },
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
			if g.activeFocused() == nil {
				return
			}
			g.activeFocused().Term.Buf.Lock()
			vo := g.activeFocused().Term.Buf.ViewOffset - g.activeFocused().Rows/2
			if vo < 0 {
				vo = 0
			}
			g.activeFocused().Term.Buf.SetViewOffset(vo)
			g.activeFocused().Term.Buf.Unlock()
		},
		func() {
			if g.activeFocused() == nil {
				return
			}
			g.activeFocused().Term.Buf.Lock()
			g.activeFocused().Term.Buf.SetViewOffset(g.activeFocused().Term.Buf.ViewOffset + g.activeFocused().Rows/2)
			g.activeFocused().Term.Buf.Unlock()
		},
		func() {
			if g.activeFocused() == nil {
				return
			}
			g.activeFocused().Term.Buf.Lock()
			g.activeFocused().Term.Buf.ClearScrollback()
			g.activeFocused().Term.Buf.Unlock()
		},
		// Copy / Paste
		g.copySelection,
		g.handlePaste,
		// Search
		g.openSearchOverlay,
		// File Explorer
		g.openFileExplorer,
		// Pins
		func() { g.tabMgr.PinMode = true; g.status.Bar.PinMode = true },
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
			g.overlays.Stats.Open = !g.overlays.Stats.Open
			if g.overlays.Stats.Open {
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
		func() { g.screenshot.Pending = true; g.render.Dirty = true },
		g.toggleRecording,
		// Help
		g.toggleOverlay,
		g.openPalette,
		g.openMarkdownViewer,
		g.openURLInput,
		g.sendViewerToPane,
		// Config
		g.reloadConfig,
		g.forceRefresh,
		// App
		func() { os.Exit(0) },
	}

	if len(entries) != len(actions) {
		panic(fmt.Sprintf("palette: entries (%d) and actions (%d) count mismatch", len(entries), len(actions)))
	}

	// Append dynamic theme entries from discovered theme files.
	for _, name := range config.ListThemes() {
		themeName := name // capture for closure
		entries = append(entries, renderer.PaletteEntry{Name: "Theme: " + themeName, Shortcut: ""})
		actions = append(actions, func() { g.switchTheme(themeName) })
	}

	g.palette.Entries = entries
	g.palette.Actions = actions
}

// handlePaletteInput processes keyboard input while the command palette is open.
func (g *Game) handlePaletteInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	filtered, origIdx := renderer.FilterPalette(g.palette.Entries, g.palette.State.Query)

	now := time.Now()

	// Arrow keys with OS-style repeat (delay then interval).
	upPressed := ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	downPressed := ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	if g.palette.repeat.Update(ebiten.KeyArrowUp, upPressed, g.input.PrevKeys[ebiten.KeyArrowUp], now) && g.palette.State.Cursor > 0 {
		g.palette.State.Cursor--
	}
	if g.palette.repeat.Update(ebiten.KeyArrowDown, downPressed, g.input.PrevKeys[ebiten.KeyArrowDown], now) && g.palette.State.Cursor < len(filtered)-1 {
		g.palette.State.Cursor++
	}
	g.input.PrevKeys[ebiten.KeyArrowUp] = upPressed
	g.input.PrevKeys[ebiten.KeyArrowDown] = downPressed

	// ESC: clear query if non-empty, otherwise close. Uses inpututil to catch
	// sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if g.palette.State.Query != "" {
			g.palette.State.Query = ""
			g.palette.State.Cursor = 0
			g.palette.State.CursorPos = 0
		} else {
			g.closePalette()
		}
		g.input.PrevKeys[ebiten.KeyEscape] = true
		return
	}

	// Enter and Cmd+P — edge-triggered only.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyP} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.input.PrevKeys[key]
		g.input.PrevKeys[key] = pressed
		if !pressed || wasPressed {
			continue
		}
		switch {
		case key == ebiten.KeyEnter:
			if g.palette.State.Cursor < len(origIdx) {
				orig := origIdx[g.palette.State.Cursor]
				if orig < len(g.palette.Actions) && g.palette.Actions[orig] != nil {
					g.closePalette()
					g.palette.Actions[orig]()
				}
			}
		case key == ebiten.KeyP && meta:
			g.closePalette()
		}
	}
	g.input.PrevKeys[ebiten.KeyMeta] = meta

	prevQuery := g.palette.State.Query
	ti := &TextInput{Text: g.palette.State.Query, CursorPos: g.palette.State.CursorPos}
	ti.Update(&g.palette.inputRepeat, meta, alt)
	g.palette.State.Query = ti.Text
	g.palette.State.CursorPos = ti.CursorPos
	if g.palette.State.Query != prevQuery {
		g.palette.State.Cursor = 0
	}
}

// --- Confirm dialog ---

// showConfirm opens the close-confirmation dialog with the given message and
// registers the action to execute if the user confirms.
func (g *Game) showConfirm(msg string, action func()) {
	g.overlays.Confirm = renderer.ConfirmState{Open: true, Message: msg}
	g.overlays.ConfirmAction = action
	g.render.Dirty = true
}

// handleConfirmInput processes keyboard input while the confirm dialog is open.
// Enter or Y executes the pending action; Escape or N cancels.
func (g *Game) handleConfirmInput() {
	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyN) {
		g.overlays.Confirm = renderer.ConfirmState{}
		g.overlays.ConfirmAction = nil
		g.input.PrevKeys[ebiten.KeyEscape] = true
		g.render.Dirty = true
		return
	}

	for _, key := range []ebiten.Key{
		ebiten.KeyEnter, ebiten.KeyNumpadEnter, ebiten.KeyY,
	} {
		pressed := ebiten.IsKeyPressed(key)
		was := g.input.PrevKeys[key]
		g.input.PrevKeys[key] = pressed
		if pressed && !was {
			if g.overlays.ConfirmAction != nil {
				g.overlays.ConfirmAction()
			}
			g.overlays.Confirm = renderer.ConfirmState{}
			g.overlays.ConfirmAction = nil
			g.render.Dirty = true
			return
		}
	}
}
