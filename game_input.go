package main

import (
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/terminal"
)

func (g *Game) handleInput() {
	// Track meta key state for hint mode (tab number badges).
	// Must run before any early return so release is always detected.
	metaNow := ebiten.IsKeyPressed(ebiten.KeyMeta)
	if metaNow != g.prevKeys[ebiten.KeyMeta] {
		g.screenDirty = true
		g.prevKeys[ebiten.KeyMeta] = metaNow
	}

	// Pane rename mode intercepts all input (highest priority).
	if g.renamingPane() {
		g.screenDirty = true
		g.handlePaneRenameInput()
		return
	}

	// Tab rename mode intercepts all input.
	if g.renamingTabIdx() >= 0 {
		g.screenDirty = true
		g.handleRenameInput()
		return
	}

	// Tab note edit mode intercepts all input.
	if g.notingTabIdx() >= 0 {
		g.screenDirty = true
		g.handleNoteInput()
		return
	}

	// File explorer has second-highest priority so ESC always reaches it cleanly.
	if g.explorer.State.Open {
		g.screenDirty = true
		g.handleFileExplorerInput()
		return
	}

	// When the confirm dialog is open, route input to confirm handling.
	if g.confirmState.Open {
		g.screenDirty = true
		g.handleConfirmInput()
		return
	}

	// When the tab switcher is open, route all keyboard input to tab switcher handling.
	if g.tabSwitcherState.Open {
		g.screenDirty = true
		g.handleTabSwitcherInput()
		return
	}

	// When the tab search is open, route input to tab search handling.
	if g.tabSearchState.Open {
		g.screenDirty = true
		g.handleTabSearchInput()
		return
	}

	// pin mode: waiting for a home-row slot keypress after Cmd+Space.
	if g.tabMgr.PinMode {
		g.screenDirty = true
		g.handlePinInput()
		return
	}

	// When the markdown viewer is open, route input to markdown viewer handling.
	if g.mdViewerState.Open {
		g.screenDirty = true
		g.handleMarkdownViewerInput()
		return
	}

	// When the URL input overlay is open, route input to URL input handling.
	if g.urlInputState.Open {
		g.screenDirty = true
		g.handleURLInputInput()
		return
	}

	// When the overlay is open, route all keyboard input to overlay handling.
	if g.overlayState.Open {
		g.screenDirty = true
		g.handleOverlayInput()
		return
	}

	// When the command palette is open, route input to palette handling.
	if g.palette.State.Open {
		g.screenDirty = true
		g.handlePaletteInput()
		return
	}

	// When search is open, route input to search handling.
	if g.search.State.Open {
		g.screenDirty = true
		g.handleSearchInput()
		return
	}

	// When the context menu is open, consume keyboard events for menu navigation
	// and prevent them from reaching the PTY.
	if g.menuState.Open {
		g.screenDirty = true
		g.handleMenuKeys()
		return
	}

	if g.focused == nil {
		return
	}

	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	// alt is true only for the left Option key when left_option_as_meta is enabled.
	// Right Option is left alone so macOS can still compose characters (ð, ™, etc.).
	alt := g.cfg.Keyboard.LeftOptionAsMeta && ebiten.IsKeyPressed(ebiten.KeyAltLeft)

	// Scroll keys — handled before forwarding to PTY.
	halfPage := g.cfg.Window.Rows / 2
	if halfPage < 1 {
		halfPage = 1
	}
	// Block scrollback when alternate screen is active — TUI apps (Claude Code,
	// nvim, htop) own the full viewport and scrollback makes no sense.
	g.focused.Term.Buf.RLock()
	altActive := g.focused.Term.Buf.IsAltActive()
	g.focused.Term.Buf.RUnlock()

	// keyScrolled is true only when an explicit keyboard scroll key (PageUp/Down/Ctrl+K)
	// was pressed. This causes an early return to prevent the key from leaking into the
	// PTY input path. Mouse wheel scroll does NOT set this flag — keyboard input must
	// always be processed even when trackpad momentum keeps the wheel delta non-zero.
	keyScrolled := false
	if !altActive {
		for _, key := range allKeys {
			if !ebiten.IsKeyPressed(key) || g.prevKeys[key] {
				continue
			}
			switch {
			case key == ebiten.KeyPageUp:
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ScrollViewUp(halfPage)
				g.focused.Term.Buf.Unlock()
				keyScrolled = true
			case key == ebiten.KeyPageDown:
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ScrollViewDown(halfPage)
				g.focused.Term.Buf.Unlock()
				keyScrolled = true
			case (meta || ctrl) && key == ebiten.KeyK:
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ClearScrollback()
				g.focused.Term.Buf.ClearSelection()
				g.focused.Term.Buf.Unlock()
				keyScrolled = true
			}
		}
	}

	_, wy := ebiten.Wheel()
	if wy != 0 {
		g.focused.Term.Buf.RLock()
		mouseMode := g.focused.Term.Buf.MouseMode
		g.focused.Term.Buf.RUnlock()
		if mouseMode == 0 && !altActive {
			// Accumulate fractional trackpad deltas — int truncation drops sub-pixel
			// input and makes smooth-scroll feel janky.
			g.scrollAccum += wy * float64(g.cfg.Scroll.WheelLinesPerTick)
			lines := int(g.scrollAccum)
			if lines != 0 {
				g.scrollAccum -= float64(lines)
				g.focused.Term.Buf.Lock()
				if lines > 0 {
					g.focused.Term.Buf.ScrollViewUp(lines)
				} else {
					g.focused.Term.Buf.ScrollViewDown(-lines)
				}
				g.focused.Term.Buf.Unlock()
				// Do NOT set keyScrolled here. Trackpad momentum keeps wy non-zero
				// for several frames after the finger lifts; suppressing keyboard input
				// during that window causes keystrokes to be silently dropped.
				// handleMouse() already sets screenDirty for wheel events.
			}
		}
	}

	if keyScrolled {
		return
	}

	var sentToPTY bool

	// Handle printable rune input via InputChars (handles shift, compose, IME).
	// On macOS, Option+letter arrives here as a composed char (∫, ∂, etc.) because
	// the IME intercepts keyDown before GLFW can report it via IsKeyPressed.
	// When left-Option-as-Meta is active, map the composed char to ESC+base_char.
	if !ctrl && !meta {
		runes := ebiten.AppendInputChars(nil)
		for _, r := range runes {
			if alt {
				if seq := terminal.MetaFromChar(r); seq != nil {
					g.focused.Term.SendBytes(seq)
					sentToPTY = true
				}
				// else: dead-key or non-US layout char — ignore
			} else {
				g.focused.Term.SendBytes([]byte(string(r)))
				sentToPTY = true
			}
		}
	}

	for _, key := range allKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch {
			case meta && key == ebiten.KeyC:
				g.copySelection()

			case meta && !shift && key == ebiten.KeyV:
				g.handlePaste()

			case meta && key == ebiten.KeySlash:
				// Cmd+/ — toggle keybindings help overlay.
				if g.cfg.Help.Enabled {
					g.toggleOverlay()
				}

			case meta && key == ebiten.KeyP:
				// Cmd+P — open command palette.
				if g.cfg.Help.Enabled {
					g.openPalette()
				}

			case meta && key == ebiten.KeyJ:
				// Cmd+J — open tab search.
				g.openTabSearch()

			case meta && key == ebiten.KeyF:
				// Cmd+F — open in-buffer search.
				g.openSearchOverlay()

			case meta && !shift && key == ebiten.KeyB:
				// Cmd+B — toggle command blocks.
				g.blocksEnabled = !g.blocksEnabled
				g.renderer.BlocksEnabled = g.blocksEnabled
				if g.blocksEnabled {
					g.flashStatus("Command blocks: on")
				} else {
					g.flashStatus("Command blocks: off")
				}

			case meta && key == ebiten.KeyI:
				// Cmd+I — toggle stats overlay.
				g.statsState.Open = !g.statsState.Open
				if g.statsState.Open {
					g.collectStats()
					g.flashStatus("Stats: on")
				} else {
					g.renderer.SetLayoutDirty()
					g.renderer.ClearPaneCache()
					g.flashStatus("Stats: off")
				}

			case meta && !shift && key == ebiten.KeyEqual:
				// Cmd+= (plus) — increase font size.
				g.adjustFontSize(1)

			case meta && !shift && key == ebiten.KeyMinus:
				// Cmd+- — decrease font size.
				g.adjustFontSize(-1)

			case meta && key == ebiten.KeyComma:
				// Cmd+, — reload config.
				g.reloadConfig()

			case meta && key == ebiten.KeyE:
				// Cmd+E — toggle file explorer.
				if g.explorer.State.Open {
					g.closeFileExplorer()
				} else if g.cfg.FileExplorer.Enabled {
					g.openFileExplorer()
				}

			case meta && shift && key == ebiten.KeyS:
				// Cmd+Shift+S — take screenshot.
				g.screenshotPending = true
				g.screenDirty = true

			case meta && shift && key == ebiten.KeyPeriod:
				// Cmd+Shift+. — toggle screen recording.
				g.toggleRecording()
			case meta && shift && key == ebiten.KeyM:
				// Cmd+Shift+M — markdown reader mode.
				g.openMarkdownViewer()

			case meta && !shift && key == ebiten.KeyL:
				// Cmd+L — open llms.txt browser.
				g.openURLInput()

			// Tab management.
			case meta && shift && key == ebiten.KeyT:
				g.openTabSwitcher()
			case meta && key == ebiten.KeyG:
				g.tabMgr.PinMode = true
				g.screenDirty = true
			case meta && key == ebiten.KeyT:
				g.newTab()
			case meta && shift && key == ebiten.KeyB:
				// Cmd+Shift+B — new server-backed tab (Mode B); falls back to local PTY.
				g.newServerTab()
			case meta && shift && key == ebiten.KeyR:
				g.startRenameTab(g.tabMgr.ActiveIdx)
			case meta && shift && key == ebiten.KeyN:
				g.startNoteEdit(g.tabMgr.ActiveIdx)
			case meta && key == ebiten.KeySemicolon:
				g.goBack()
			case meta && shift && key == ebiten.KeyBracketLeft:
				g.prevTab()
			case meta && shift && key == ebiten.KeyBracketRight:
				g.nextTab()
				// Cmd+1-9: switch to tab at position N (normal navigation).
			case meta && key == ebiten.Key1:
				g.switchTab(0)
			case meta && key == ebiten.Key2:
				g.switchTab(1)
			case meta && key == ebiten.Key3:
				g.switchTab(2)
			case meta && key == ebiten.Key4:
				g.switchTab(3)
			case meta && key == ebiten.Key5:
				g.switchTab(4)
			case meta && key == ebiten.Key6:
				g.switchTab(5)
			case meta && key == ebiten.Key7:
				g.switchTab(6)
			case meta && key == ebiten.Key8:
				g.switchTab(7)
			case meta && key == ebiten.Key9:
				g.switchTab(8)

			// Pane management.
			case meta && key == ebiten.KeyZ:
				g.toggleZoom()
			case meta && shift && key == ebiten.KeyD:
				g.splitV()
			case meta && shift && key == ebiten.KeyH:
				g.splitHServer()
			case meta && shift && key == ebiten.KeyV:
				g.splitVServer()
			case meta && !shift && key == ebiten.KeyD:
				g.splitH()
			case meta && key == ebiten.KeyW:
				// Close pane if 2+ panes in tab; close tab if last pane.
				if g.cfg.Help.CloseConfirm {
					if len(g.layout.Leaves()) <= 1 {
						g.showConfirm("Close tab?", g.closeActiveTab)
					} else {
						pane := g.focused
						g.showConfirm("Close pane?", func() { g.closePane(pane) })
					}
				} else {
					if len(g.layout.Leaves()) <= 1 {
						g.closeActiveTab()
					} else {
						g.closePane(g.focused)
					}
				}
			case meta && key == ebiten.KeyBracketLeft:
				if p := g.layout.PrevLeaf(g.focused); p != nil {
					g.setFocus(p)
				}
			case meta && key == ebiten.KeyBracketRight:
				if p := g.layout.NextLeaf(g.focused); p != nil {
					g.setFocus(p)
				}
			case meta && shift && key == ebiten.KeyArrowLeft:
				g.moveTabLeft()
			case meta && shift && key == ebiten.KeyArrowRight:
				g.moveTabRight()

			// Cmd+Option+Arrow — resize focused pane's split.
			case meta && ebiten.IsKeyPressed(ebiten.KeyAlt) && key == ebiten.KeyArrowLeft:
				g.resizePane(-1, 0)
			case meta && ebiten.IsKeyPressed(ebiten.KeyAlt) && key == ebiten.KeyArrowRight:
				g.resizePane(1, 0)
			case meta && ebiten.IsKeyPressed(ebiten.KeyAlt) && key == ebiten.KeyArrowUp:
				g.resizePane(0, -1)
			case meta && ebiten.IsKeyPressed(ebiten.KeyAlt) && key == ebiten.KeyArrowDown:
				g.resizePane(0, 1)

			case meta && !shift && key == ebiten.KeyArrowLeft:
				g.focusDir(-1, 0)
			case meta && !shift && key == ebiten.KeyArrowRight:
				g.focusDir(1, 0)
			case meta && !shift && key == ebiten.KeyArrowUp:
				g.focusDir(0, -1)
			case meta && !shift && key == ebiten.KeyArrowDown:
				g.focusDir(0, 1)

			// Left Option as Meta — specific sequences with repeat support.
			case alt && key == ebiten.KeyBackspace:
				g.sendWithRepeat(key, []byte("\x1b\x7f"))
				sentToPTY = true
			case alt && key == ebiten.KeyArrowLeft:
				g.sendWithRepeat(key, []byte("\x1bb"))
				sentToPTY = true
			case alt && key == ebiten.KeyArrowRight:
				g.sendWithRepeat(key, []byte("\x1bf"))
				sentToPTY = true

			// alt + symbol/digit keys: send ESC + ASCII.
			// Needed for keys whose Option+key is a macOS dead key (e.g. Option+`)
			// or produces a composed char not in the optionToBase IME map.
			// This catches everything the MetaFromChar path misses.
			case alt:
				if seq := altPrintableSeq(key); seq != nil {
					g.focused.Term.SendBytes(seq)
					sentToPTY = true
				}

			// Vault ghost accept: right-arrow accepts the current suggestion.
			case !ctrl && !alt && !meta && key == ebiten.KeyArrowRight && g.vaultSuggest != "":
				g.focused.Term.SendBytes([]byte(g.vaultSuggest))
				g.vaultSuggest = ""
				g.vaultLineCache = ""
				g.vaultSkip = 0
				sentToPTY = true

			case ctrl || isSpecialKey(key):
				g.focused.Term.Buf.RLock()
				appCursor := g.focused.Term.Buf.AppCursorKeys
				g.focused.Term.Buf.RUnlock()
				if seq := terminal.KeyEventToBytes(key, appCursor); seq != nil {
					g.sendWithRepeat(key, seq)
					sentToPTY = true
				}
			}
		} else if !pressed && g.ptyRepeat.active && g.ptyRepeat.key == key {
			g.ptyRepeat.Reset()
		}
		g.prevKeys[key] = pressed
	}

	// Consume async clipboard result for terminal paste (from Cmd+V last frame).
	// No overlays are consuming the clipboard at this point, so terminal owns it.
	if g.drainTerminalPaste() {
		sentToPTY = true
	}

	if sentToPTY {
		g.focused.Term.Buf.Lock()
		g.focused.Term.Buf.ResetView() // snap back to live output on keystroke
		g.focused.Term.Buf.ClearSelection()
		g.focused.Term.Buf.Unlock()
		g.screenDirty = true // ensure snap-back renders immediately without waiting for PTY output
	}

	// Vault suggestion update — extract current line from buffer and query vault.
	g.updateVaultSuggestion()

	if g.ptyRepeat.active && ebiten.IsKeyPressed(g.ptyRepeat.key) {
		now := time.Now()
		if now.Sub(g.ptyRepeat.start) >= keyRepeatDelay && now.Sub(g.ptyRepeat.last) >= keyRepeatInterval {
			if g.repeatSeq != nil {
				g.focused.Term.SendBytes(g.repeatSeq)
			}
			g.ptyRepeat.last = now
		}
	} else if g.ptyRepeat.active {
		g.ptyRepeat.Reset()
	}
}

// sendWithRepeat sends seq to the focused PTY and starts key repeat tracking.
func (g *Game) sendWithRepeat(key ebiten.Key, seq []byte) {
	g.focused.Term.SendBytes(seq)
	g.repeatSeq = seq
	now := time.Now()
	g.ptyRepeat.key = key
	g.ptyRepeat.active = true
	g.ptyRepeat.start = now
	g.ptyRepeat.last = now
}

func isSpecialKey(key ebiten.Key) bool {
	switch key {
	case ebiten.KeyEnter, ebiten.KeyNumpadEnter, ebiten.KeyBackspace,
		ebiten.KeyTab, ebiten.KeyEscape,
		ebiten.KeyArrowUp, ebiten.KeyArrowDown, ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
		ebiten.KeyHome, ebiten.KeyEnd, ebiten.KeyPageUp, ebiten.KeyPageDown,
		ebiten.KeyInsert, ebiten.KeyDelete,
		ebiten.KeyF1, ebiten.KeyF2, ebiten.KeyF3, ebiten.KeyF4,
		ebiten.KeyF5, ebiten.KeyF6, ebiten.KeyF7, ebiten.KeyF8,
		ebiten.KeyF9, ebiten.KeyF10, ebiten.KeyF11, ebiten.KeyF12:
		return true
	}
	return false
}

// altPrintableSeq returns the ESC-prefixed Meta sequence for alt+key combinations
// that macOS Option doesn't deliver via AppendInputChars (dead keys like Option+`,
// and symbol/digit keys whose Option+key produces a char not in optionToBase).
// Returns nil when the key has no direct ASCII representation.
func altPrintableSeq(key ebiten.Key) []byte {
	var ch byte
	switch key {
	// Digits
	case ebiten.Key0:
		ch = '0'
	case ebiten.Key1:
		ch = '1'
	case ebiten.Key2:
		ch = '2'
	case ebiten.Key3:
		ch = '3'
	case ebiten.Key4:
		ch = '4'
	case ebiten.Key5:
		ch = '5'
	case ebiten.Key6:
		ch = '6'
	case ebiten.Key7:
		ch = '7'
	case ebiten.Key8:
		ch = '8'
	case ebiten.Key9:
		ch = '9'
	// Symbols
	case ebiten.KeyBackquote:
		ch = '`'
	case ebiten.KeyMinus:
		ch = '-'
	case ebiten.KeyEqual:
		ch = '='
	case ebiten.KeyBracketLeft:
		ch = '['
	case ebiten.KeyBracketRight:
		ch = ']'
	case ebiten.KeyBackslash:
		ch = '\\'
	case ebiten.KeySemicolon:
		ch = ';'
	case ebiten.KeyApostrophe:
		ch = '\''
	case ebiten.KeyComma:
		ch = ','
	case ebiten.KeyPeriod:
		ch = '.'
	case ebiten.KeySlash:
		ch = '/'
	case ebiten.KeySpace:
		ch = ' '
	default:
		return nil
	}
	return []byte{0x1b, ch}
}

// handleFocus sends mode-1004 focus events when the window focus state changes.
// On focus regain, resets input state so stale prevKeys/prevMouseButtons don't swallow
// the first events. Also manages idle suspension: after 5 seconds unfocused, TPS drops
// to 5 and terminal polling goroutines are paused to minimize CPU/allocation pressure.
// TPS=5 (not 1) ensures clicks and keystrokes that complete within the frame interval
