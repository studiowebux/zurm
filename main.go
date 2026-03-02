package main

import (
	_ "embed"
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/fileexplorer"
	"github.com/studiowebux/zurm/help"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/session"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/terminal"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Defaults to "dev" for local builds.
var version = "dev"

// Key repeat parameters matching typical OS terminal behavior.
const (
	keyRepeatDelay    = 500 * time.Millisecond
	keyRepeatInterval = 33 * time.Millisecond // ~30 repeats/sec
)

//go:embed assets/fonts/JetBrainsMono-Regular.ttf
var jetbrainsMono []byte

//go:embed assets/fonts/NotoEmoji-Regular.ttf
var notoEmoji []byte

// Game implements ebiten.Game.
// Pattern: game loop — Update handles logic, Draw handles rendering.
type Game struct {
	tabs      []*tab.Tab
	activeTab int

	// Convenience pointers into tabs[activeTab] — kept in sync via
	// updateLayout / setFocus / syncActive.
	layout  *pane.LayoutNode
	focused *pane.Pane

	renderer *renderer.Renderer
	font     *renderer.FontRenderer
	cfg      *config.Config

	winW, winH int

	// zoomed is true when the focused pane is temporarily fullscreened (Cmd+Z).
	zoomed bool

	// prevKeys tracks which keys were pressed last frame (for edge detection).
	prevKeys map[ebiten.Key]bool
	// prevMouseButtons tracks mouse button state last frame.
	prevMouseButtons map[ebiten.MouseButton]bool
	// prevMX/prevMY track last cursor position for block hover detection.
	prevMX, prevMY int

	// dpi is the device pixel ratio (2.0 on Retina).
	dpi float64

	// prevFocused tracks window focus state for mode-1004 focus events.
	prevFocused bool

	// Key repeat state for special keys.
	repeatKey    ebiten.Key
	repeatSeq    []byte // exact bytes to resend on repeat; nil uses KeyEventToBytes
	repeatActive bool
	repeatStart  time.Time
	repeatLast   time.Time

	// Selection drag state.
	selDragging bool

	// PTY mouse motion tracking for modes 1002/1003.
	lastMouseCol int // last col sent to PTY (1-based)
	lastMouseRow int // last row sent to PTY (1-based)
	mouseHeldBtn int // button currently held (-1 = none, 0=left, 1=mid, 2=right)

	// scrollAccum accumulates fractional trackpad wheel deltas so no input is lost.
	scrollAccum float64

	// Click timing for double/triple click word/line select.
	lastClickTime time.Time
	lastClickRow  int
	lastClickCol  int
	clickCount    int

	// Context menu state.
	menuState renderer.MenuState

	// Keybinding overlay state.
	overlayState renderer.OverlayState

	// Close-confirmation dialog state.
	confirmState         renderer.ConfirmState
	confirmPendingAction func()

	// In-buffer search state (Cmd+F).
	searchState     renderer.SearchState
	lastSearchQuery string // detects query change to avoid recomputing every frame

	// Status bar state.
	statusBarState renderer.StatusBarState
	gitBranchCh    chan string // receives async git branch results

	// Tab switcher overlay state (pin-style).
	tabSwitcherState renderer.TabSwitcherState

	// Command palette state (Cmd+P).
	paletteState   renderer.PaletteState
	paletteEntries []renderer.PaletteEntry
	paletteActions []func()

	// pinMode is true after Cmd+Space, waiting for a home-row slot keypress.
	pinMode bool

	// File explorer sidebar state (Cmd+E).
	fileExplorerState renderer.FileExplorerState

	// Key repeat state for file explorer navigation (arrow keys).
	explorerRepeatKey    ebiten.Key
	explorerRepeatActive bool
	explorerRepeatStart  time.Time
	explorerRepeatLast   time.Time

	// Key repeat state for command palette navigation (arrow keys).
	paletteRepeatKey    ebiten.Key
	paletteRepeatActive bool
	paletteRepeatStart  time.Time
	paletteRepeatLast   time.Time

	// Dirty-render state — screen is only redrawn when something changes.
	screenDirty  bool
	lastPtySeq   uint64
	lastClockSec int64

	// blocksEnabled is the runtime toggle for command block rendering.
	// Initialized from cfg.Blocks.Enabled; toggled via command palette.
	blocksEnabled bool

	// flashExpiry is when statusBarState.FlashMessage should be cleared.
	flashExpiry time.Time
}

func main() {
	noRestore := flag.Bool("no-restore", false, "skip session restore on launch")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("zurm %s\n", version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load warning: %v (using defaults)", err)
	}

	dpi := ebiten.Monitor().DeviceScaleFactor()
	fontBytes := jetbrainsMono
	if cfg.Font.File != "" {
		if data, err := os.ReadFile(cfg.Font.File); err == nil {
			fontBytes = data
		} else {
			log.Printf("font file %q not found, using embedded font: %v", cfg.Font.File, err)
		}
	}
	fontR, err := renderer.NewFontRenderer(fontBytes, cfg.Font.Size*dpi)
	if err != nil {
		log.Fatalf("font load: %v", err)
	}
	if err := fontR.LoadEmojiFont(notoEmoji); err != nil {
		log.Printf("emoji font load failed (emoji will render as boxes): %v", err)
	}

	// Compute tab bar and status bar heights first so they're included in the window size budget.
	rend := renderer.NewRenderer(fontR, cfg)
	tabBarH := rend.TabBarHeight()
	statusBarH := rend.StatusBarHeight()

	winW, winH := fontR.WindowSize(cfg.Window.Columns, cfg.Window.Rows, cfg.Window.Padding)
	winH += tabBarH + statusBarH // reserve space for tab bar (top) and status bar (bottom)
	logW := int(float64(winW) / dpi)
	logH := int(float64(winH) / dpi)

	rend.SetSize(winW, winH)

	paneRect := image.Rect(0, tabBarH, winW, winH-statusBarH)

	// Attempt session restore; fall back to a single fresh tab.
	var initialTabs []*tab.Tab
	var initialActive int
	if sess, loadErr := session.Load(cfg); loadErr == nil && sess != nil && !*noRestore && len(sess.Tabs) > 0 {
		for _, td := range sess.Tabs {
			t, tErr := tab.New(cfg, paneRect, fontR.CellW, fontR.CellH, td.Cwd)
			if tErr != nil {
				log.Printf("session restore: tab new: %v", tErr)
				continue
			}
			t.Title = td.Title
			t.UserRenamed = td.UserRenamed
			if len(td.PinnedSlot) > 0 {
				t.PinnedSlot = []rune(td.PinnedSlot)[0]
			}
			if leaf := t.Layout.Leaves(); len(leaf) > 0 {
				// Pre-populate Cwd so saveSession() has a valid value before
				// lsof/OSC 7 fires. Without this, quitting quickly overwrites
				// the session with empty CWDs.
				leaf[0].Pane.Term.Cwd = td.Cwd
			}
			initialTabs = append(initialTabs, t)
		}
		if len(initialTabs) > 0 {
			initialActive = sess.ActiveTab
			if initialActive >= len(initialTabs) {
				initialActive = len(initialTabs) - 1
			}
		}
	}
	if len(initialTabs) == 0 {
		firstTab, tErr := tab.New(cfg, paneRect, fontR.CellW, fontR.CellH, "")
		if tErr != nil {
			log.Fatalf("tab new: %v", tErr)
		}
		initialTabs = []*tab.Tab{firstTab}
		initialActive = 0
	}

	game := &Game{
		tabs:             initialTabs,
		activeTab:        initialActive,
		layout:           initialTabs[initialActive].Layout,
		focused:          initialTabs[initialActive].Focused,
		renderer:         rend,
		font:             fontR,
		cfg:              cfg,
		winW:             logW,
		winH:             logH,
		dpi:              dpi,
		prevKeys:         make(map[ebiten.Key]bool),
		prevMouseButtons: make(map[ebiten.MouseButton]bool),
		mouseHeldBtn:     -1,
		blocksEnabled:    cfg.Blocks.Enabled,
	}

	game.renderer.BlocksEnabled = game.blocksEnabled
	game.buildPalette()

	ebiten.SetWindowSize(logW, logH)
	ebiten.SetWindowTitle("zurm")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetScreenClearedEveryFrame(false) // we manage redraws via dirty flag
	ebiten.SetTPS(cfg.Performance.TPS)

	if err := ebiten.RunGame(game); err != nil && err != ebiten.Termination {
		log.Fatalf("ebiten: %v", err)
	}
	// Save session after the game loop exits, regardless of how it was terminated
	// (Cmd+Q, red X button, last tab closed, or OS-level quit signal).
	game.saveSession()
}

// Update is called at 60 TPS by Ebitengine.
func (g *Game) Update() error {
	if len(g.tabs) == 0 {
		g.saveSession()
		return ebiten.Termination
	}

	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	if (meta || ctrl) && ebiten.IsKeyPressed(ebiten.KeyQ) {
		g.saveSession()
		for _, t := range g.tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.Close()
			}
		}
		return ebiten.Termination
	}

	// Clear transient flash message once its expiry has passed.
	if g.statusBarState.FlashMessage != "" && time.Now().After(g.flashExpiry) {
		g.statusBarState.FlashMessage = ""
		g.screenDirty = true
	}

	// Update cursor blink for all panes in the active tab.
	// Update() returns true when blinkOn toggles — mark dirty so the frame redraws.
	for _, leaf := range g.layout.Leaves() {
		if leaf.Pane.Term.Cursor.Update() {
			g.screenDirty = true
			// Bump per-pane gen so the pane-skip logic redraws the cursor row.
			leaf.Pane.Term.Buf.BumpRenderGen()
		}
	}

	// Check for dead panes (non-blocking).
	for _, leaf := range g.layout.Leaves() {
		select {
		case <-leaf.Pane.Term.Dead():
			if len(g.layout.Leaves()) <= 1 {
				g.closeActiveTab()
			} else {
				g.closePane(leaf.Pane)
			}
		default:
		}
	}
	if len(g.tabs) == 0 {
		return ebiten.Termination
	}

	g.handleMouse()
	g.handleInput()
	if len(g.tabs) == 0 {
		return ebiten.Termination
	}
	g.handleResize()
	g.handleFocus()
	g.drainTitle()
	g.drainCwd()
	g.drainGitBranch()
	g.drainForeground()
	g.recomputeSearch()

	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.SendCPRResponse()
		leaf.Pane.Term.SendDA1Response()
		leaf.Pane.Term.SendDA2Response()
		leaf.Pane.Term.SendPendingResponses()
		leaf.Pane.Term.SyncCursorStyle()
	}
	g.focused.Term.SendClipboardResponses()

	return nil
}

// needsRender reports whether the offscreen must be redrawn this frame.
func (g *Game) needsRender() bool {
	if g.screenDirty {
		return true
	}
	// PTY output in any pane.
	if seq := terminal.RenderSeq(); seq != g.lastPtySeq {
		return true
	}
	// Clock ticks once per second — only relevant when enabled.
	if g.cfg.StatusBar.ShowClock && time.Now().Unix() != g.lastClockSec {
		return true
	}
	return false
}

// Draw is called each frame by Ebitengine.
// SetScreenClearedEveryFrame(false) means the screen retains its content between frames,
// so we only need to draw when something actually changed.
func (g *Game) Draw(screen *ebiten.Image) {
	if !g.needsRender() {
		return
	}
	// Sync transient status bar fields from live game state before rendering.
	g.statusBarState.Zoomed = g.zoomed
	g.statusBarState.PinMode = g.pinMode
	g.statusBarState.BlocksEnabled = g.blocksEnabled
	if g.focused != nil {
		g.focused.Term.Buf.RLock()
		g.statusBarState.ScrollOffset = g.focused.Term.Buf.ViewOffset
		if g.blocksEnabled {
			g.statusBarState.BlockCount = len(g.focused.Term.Buf.Blocks)
		}
		g.focused.Term.Buf.RUnlock()
	}
	g.renderer.DrawAll(screen, g.tabs, g.activeTab, g.focused, g.zoomed,
		&g.menuState, &g.overlayState, &g.confirmState, &g.searchState, &g.statusBarState, &g.tabSwitcherState,
		&g.paletteState, g.paletteEntries, &g.fileExplorerState)
	g.screenDirty = false
	g.lastPtySeq = terminal.RenderSeq()
	g.lastClockSec = time.Now().Unix()
}

// Layout returns the physical screen size for HiDPI rendering.
func (g *Game) Layout(outsideW, outsideH int) (int, int) {
	return int(float64(outsideW) * g.dpi), int(float64(outsideH) * g.dpi)
}

// syncActive loads g.layout and g.focused from the active tab.
func (g *Game) syncActive() {
	if g.activeTab >= len(g.tabs) {
		return
	}
	t := g.tabs[g.activeTab]
	g.layout = t.Layout
	g.focused = t.Focused
}

// updateLayout writes a new layout to both g.layout and the active tab.
func (g *Game) updateLayout(n *pane.LayoutNode) {
	g.layout = n
	g.tabs[g.activeTab].Layout = n
}

func (g *Game) handleInput() {
	// Tab rename mode intercepts all input.
	if g.renamingTabIdx() >= 0 {
		g.screenDirty = true
		g.handleRenameInput()
		return
	}

	// File explorer has second-highest priority so ESC always reaches it cleanly.
	if g.fileExplorerState.Open {
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

	// pin mode: waiting for a home-row slot keypress after Cmd+Space.
	if g.pinMode {
		g.screenDirty = true
		g.handlePinInput()
		return
	}

	// When the overlay is open, route all keyboard input to overlay handling.
	if g.overlayState.Open {
		g.screenDirty = true
		g.handleOverlayInput()
		return
	}

	// When the command palette is open, route input to palette handling.
	if g.paletteState.Open {
		g.screenDirty = true
		g.handlePaletteInput()
		return
	}

	// When search is open, route input to search handling.
	if g.searchState.Open {
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

	allKeys := []ebiten.Key{
		ebiten.KeyA, ebiten.KeyB, ebiten.KeyC, ebiten.KeyD, ebiten.KeyE,
		ebiten.KeyF, ebiten.KeyG, ebiten.KeyH, ebiten.KeyI, ebiten.KeyJ,
		ebiten.KeyK, ebiten.KeyL, ebiten.KeyM, ebiten.KeyN, ebiten.KeyO,
		ebiten.KeyP, ebiten.KeyQ, ebiten.KeyR, ebiten.KeyS, ebiten.KeyT,
		ebiten.KeyU, ebiten.KeyV, ebiten.KeyW, ebiten.KeyX, ebiten.KeyY,
		ebiten.KeyZ,
		ebiten.Key0, ebiten.Key1, ebiten.Key2, ebiten.Key3, ebiten.Key4,
		ebiten.Key5, ebiten.Key6, ebiten.Key7, ebiten.Key8, ebiten.Key9,
		ebiten.KeyEnter, ebiten.KeyNumpadEnter, ebiten.KeyBackspace,
		ebiten.KeyTab, ebiten.KeyEscape, ebiten.KeySpace,
		ebiten.KeyArrowUp, ebiten.KeyArrowDown, ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
		ebiten.KeyHome, ebiten.KeyEnd, ebiten.KeyPageUp, ebiten.KeyPageDown,
		ebiten.KeyInsert, ebiten.KeyDelete,
		ebiten.KeyF1, ebiten.KeyF2, ebiten.KeyF3, ebiten.KeyF4,
		ebiten.KeyF5, ebiten.KeyF6, ebiten.KeyF7, ebiten.KeyF8,
		ebiten.KeyF9, ebiten.KeyF10, ebiten.KeyF11, ebiten.KeyF12,
		ebiten.KeyMinus, ebiten.KeyEqual, ebiten.KeyBracketLeft, ebiten.KeyBracketRight,
		ebiten.KeyBackslash, ebiten.KeySemicolon, ebiten.KeyApostrophe,
		ebiten.KeyComma, ebiten.KeyPeriod, ebiten.KeySlash, ebiten.KeyBackquote,
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
	scrolled := false
	for _, key := range allKeys {
		if !ebiten.IsKeyPressed(key) || g.prevKeys[key] {
			continue
		}
		switch {
		case key == ebiten.KeyPageUp:
			g.focused.Term.Buf.Lock()
			g.focused.Term.Buf.ScrollViewUp(halfPage)
			g.focused.Term.Buf.Unlock()
			scrolled = true
		case key == ebiten.KeyPageDown:
			g.focused.Term.Buf.Lock()
			g.focused.Term.Buf.ScrollViewDown(halfPage)
			g.focused.Term.Buf.Unlock()
			scrolled = true
		case (meta || ctrl) && key == ebiten.KeyK:
			g.focused.Term.Buf.Lock()
			g.focused.Term.Buf.ClearScrollback()
			g.focused.Term.Buf.Unlock()
			scrolled = true
		}
	}

	_, wy := ebiten.Wheel()
	if wy != 0 {
		g.focused.Term.Buf.RLock()
		mouseMode := g.focused.Term.Buf.MouseMode
		g.focused.Term.Buf.RUnlock()
		if mouseMode == 0 {
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
			}
			scrolled = true
		}
	}

	if scrolled {
		for _, key := range allKeys {
			g.prevKeys[key] = ebiten.IsKeyPressed(key)
		}
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

			case meta && key == ebiten.KeyV:
				g.handlePaste()
				sentToPTY = true

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

			case meta && key == ebiten.KeyF:
				// Cmd+F — open in-buffer search.
				g.openSearch()

			case meta && key == ebiten.KeyB:
				// Cmd+B — toggle command blocks.
				g.blocksEnabled = !g.blocksEnabled
				g.renderer.BlocksEnabled = g.blocksEnabled
				if g.blocksEnabled {
					g.flashStatus("Command blocks: on")
				} else {
					g.flashStatus("Command blocks: off")
				}

			case meta && key == ebiten.KeyE:
				// Cmd+E — toggle file explorer.
				if g.fileExplorerState.Open {
					g.closeFileExplorer()
				} else if g.cfg.FileExplorer.Enabled {
					g.openFileExplorer()
				}

			// Tab management.
			case meta && shift && key == ebiten.KeyT:
				g.openTabSwitcher()
			case meta && key == ebiten.KeyG:
				g.pinMode = true
				g.screenDirty = true
			case meta && key == ebiten.KeyT:
				g.newTab()
			case meta && shift && key == ebiten.KeyR:
				g.startRenameTab(g.activeTab)
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
			case meta && !shift && key == ebiten.KeyD:
				g.splitH()
			case meta && shift && key == ebiten.KeyD:
				g.splitV()
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
			case meta && key == ebiten.KeyArrowLeft:
				g.focusDir(-1, 0)
			case meta && key == ebiten.KeyArrowRight:
				g.focusDir(1, 0)
			case meta && key == ebiten.KeyArrowUp:
				g.focusDir(0, -1)
			case meta && key == ebiten.KeyArrowDown:
				g.focusDir(0, 1)

			// Left Option as Meta — specific sequences with repeat support.
			case alt && key == ebiten.KeyBackspace:
				seq := []byte("\x1b\x7f")
				g.focused.Term.SendBytes(seq)
				sentToPTY = true
				g.repeatKey = key
				g.repeatSeq = seq
				g.repeatActive = true
				now := time.Now()
				g.repeatStart = now
				g.repeatLast = now
			case alt && key == ebiten.KeyArrowLeft:
				seq := []byte("\x1bb")
				g.focused.Term.SendBytes(seq)
				sentToPTY = true
				g.repeatKey = key
				g.repeatSeq = seq
				g.repeatActive = true
				now := time.Now()
				g.repeatStart = now
				g.repeatLast = now
			case alt && key == ebiten.KeyArrowRight:
				seq := []byte("\x1bf")
				g.focused.Term.SendBytes(seq)
				sentToPTY = true
				g.repeatKey = key
				g.repeatSeq = seq
				g.repeatActive = true
				now := time.Now()
				g.repeatStart = now
				g.repeatLast = now

			// alt + symbol/digit keys: send ESC + ASCII.
			// Needed for keys whose Option+key is a macOS dead key (e.g. Option+`)
			// or produces a composed char not in the optionToBase IME map.
			// This catches everything the MetaFromChar path misses.
			case alt:
				if seq := altPrintableSeq(key); seq != nil {
					g.focused.Term.SendBytes(seq)
					sentToPTY = true
				}

			case ctrl || isSpecialKey(key):
				g.focused.Term.Buf.RLock()
				appCursor := g.focused.Term.Buf.AppCursorKeys
				g.focused.Term.Buf.RUnlock()
				if seq := terminal.KeyEventToBytes(key, appCursor); seq != nil {
					g.focused.Term.SendBytes(seq)
					sentToPTY = true
					g.repeatKey = key
					g.repeatSeq = seq
					g.repeatActive = true
					now := time.Now()
					g.repeatStart = now
					g.repeatLast = now
				}
			}
			_ = shift
		} else if !pressed && g.repeatActive && g.repeatKey == key {
			g.repeatActive = false
		}
		g.prevKeys[key] = pressed
	}

	if sentToPTY {
		g.focused.Term.Buf.Lock()
		g.focused.Term.Buf.ResetView() // snap back to live output on keystroke
		g.focused.Term.Buf.ClearSelection()
		g.focused.Term.Buf.Unlock()
	}

	if g.repeatActive && ebiten.IsKeyPressed(g.repeatKey) {
		now := time.Now()
		if now.Sub(g.repeatStart) >= keyRepeatDelay && now.Sub(g.repeatLast) >= keyRepeatInterval {
			if g.repeatSeq != nil {
				g.focused.Term.SendBytes(g.repeatSeq)
			}
			g.repeatLast = now
		}
	} else if g.repeatActive && !ebiten.IsKeyPressed(g.repeatKey) {
		g.repeatActive = false
	}
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
func (g *Game) handleFocus() {
	focused := ebiten.IsFocused()
	if focused != g.prevFocused {
		g.prevFocused = focused
		g.focused.Term.SendFocusEvent(focused)
	}
}

func (g *Game) handleResize() {
	w, h := ebiten.WindowSize()
	if w == g.winW && h == g.winH {
		return
	}
	g.winW = w
	g.winH = h
	physW := int(float64(w) * g.dpi)
	physH := int(float64(h) * g.dpi)
	g.renderer.SetSize(physW, physH)
	g.renderer.SetLayoutDirty()

	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	// Recompute rects for every tab's layout.
	for _, t := range g.tabs {
		t.Layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
		}
	}
	g.syncActive()
}

func (g *Game) drainTitle() {
	select {
	case title := <-g.focused.Term.TitleCh:
		clean := sanitizeTitle(title) // SEC-003
		// Do not overwrite a user-set tab name with OSC 0/2 from the shell.
		if !g.tabs[g.activeTab].UserRenamed {
			g.tabs[g.activeTab].Title = clean
		}
		ebiten.SetWindowTitle(clean)
		g.screenDirty = true
	default:
	}
}

// drainCwd reads the latest CWD from the focused pane's OSC 7 channel.
// When the CWD changes it kicks off an async git branch lookup.
func (g *Game) drainCwd() {
	select {
	case cwd := <-g.focused.Term.CwdCh:
		if cwd != g.statusBarState.Cwd {
			g.statusBarState.Cwd = cwd
			g.focused.Term.Cwd = cwd
			g.statusBarState.GitBranch = "" // clear until new result arrives
			if g.cfg.StatusBar.ShowGit {
				g.gitBranchCh = make(chan string, 1)
				ch := g.gitBranchCh
				go func() {
					out, err := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output() // #nosec G204 — fixed binary, user CWD only
					if err != nil {
						ch <- ""
						return
					}
					ch <- strings.TrimSpace(string(out))
				}()
			}
		g.screenDirty = true
		}
	default:
	}
}

// drainGitBranch reads a completed async git branch result when available.
func (g *Game) drainGitBranch() {
	if g.gitBranchCh == nil {
		return
	}
	select {
	case branch := <-g.gitBranchCh:
		g.statusBarState.GitBranch = branch
		g.gitBranchCh = nil
		g.screenDirty = true
	default:
	}
}

// drainForeground reads the latest foreground process name from the focused pane.
func (g *Game) drainForeground() {
	if !g.cfg.StatusBar.ShowProcess {
		return
	}
	select {
	case name := <-g.focused.Term.ForegroundProcCh:
		if name != g.statusBarState.ForegroundProc {
			g.statusBarState.ForegroundProc = name
			g.screenDirty = true
		}
	default:
	}
}

// --- In-buffer search (Cmd+F) ---

// openSearch opens the search bar for the focused pane.
func (g *Game) openSearch() {
	g.searchState.Open = true
	g.screenDirty = true
	if g.focused != nil {
		g.focused.Term.Buf.BumpRenderGen()
	}
}

// closeSearch clears all search state including matches and query.
// Called on Esc to fully exit search mode.
func (g *Game) closeSearch() {
	g.searchState = renderer.SearchState{}
	g.lastSearchQuery = ""
	g.screenDirty = true
	if g.focused != nil {
		g.focused.Term.Buf.BumpRenderGen()
	}
}

// recomputeSearch re-runs SearchAll whenever the query changes.
// Called every Update so the match list stays fresh as the user types.
func (g *Game) recomputeSearch() {
	if !g.searchState.Open || g.searchState.Query == g.lastSearchQuery {
		return
	}
	g.lastSearchQuery = g.searchState.Query
	g.focused.Term.Buf.RLock()
	matches := g.focused.Term.Buf.SearchAll(g.searchState.Query)
	g.focused.Term.Buf.RUnlock()
	g.searchState.Matches = matches
	g.searchState.Current = 0
	if len(matches) > 0 {
		g.jumpToMatch(0)
	}
	g.focused.Term.Buf.BumpRenderGen()
	g.screenDirty = true
}

// jumpToMatch scrolls the focused pane so match i is centered on screen.
func (g *Game) jumpToMatch(i int) {
	if i < 0 || i >= len(g.searchState.Matches) {
		return
	}
	g.searchState.Current = i
	m := g.searchState.Matches[i]
	g.focused.Term.Buf.RLock()
	sbLen := g.focused.Term.Buf.ScrollbackLen()
	rows := g.focused.Term.Buf.Rows
	g.focused.Term.Buf.RUnlock()

	viewOffset := sbLen - m.AbsRow + rows/2
	if viewOffset < 0 {
		viewOffset = 0
	}
	if viewOffset > sbLen {
		viewOffset = sbLen
	}
	g.focused.Term.Buf.Lock()
	g.focused.Term.Buf.SetViewOffset(viewOffset)
	g.focused.Term.Buf.Unlock()
	g.screenDirty = true
}

// searchNext advances to the next match, wrapping around.
func (g *Game) searchNext() {
	if len(g.searchState.Matches) == 0 {
		return
	}
	g.jumpToMatch((g.searchState.Current + 1) % len(g.searchState.Matches))
}

// searchPrev retreats to the previous match, wrapping around.
func (g *Game) searchPrev() {
	if len(g.searchState.Matches) == 0 {
		return
	}
	n := len(g.searchState.Matches)
	g.jumpToMatch((g.searchState.Current - 1 + n) % n)
}

// handleSearchInput routes keyboard events while the search bar is open.
func (g *Game) handleSearchInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeSearch()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	edgeKeys := []ebiten.Key{
		ebiten.KeyBackspace,
		ebiten.KeyArrowDown, ebiten.KeyArrowUp,
	}
	for _, key := range edgeKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch {
			case key == ebiten.KeyArrowDown:
				g.searchNext()
			case key == ebiten.KeyArrowUp:
				g.searchPrev()
			case key == ebiten.KeyBackspace && !meta:
				runes := []rune(g.searchState.Query)
				if len(runes) > 0 {
					g.searchState.Query = string(runes[:len(runes)-1])
					g.screenDirty = true
				}
			}
		}
		g.prevKeys[key] = pressed
	}

	// Printable character input.
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= 0x20 && r != 0x7f {
				g.searchState.Query += string(r)
				g.screenDirty = true
			}
		}
	}
}

// --- File explorer (Cmd+E) ---

// openFileExplorer opens the file explorer sidebar rooted at the focused pane's CWD.
func (g *Game) openFileExplorer() {
	root := g.focused.Term.Cwd
	if root == "" {
		root = os.Getenv("HOME")
	}
	entries, err := fileexplorer.BuildTree(root)
	if err != nil {
		return
	}
	g.fileExplorerState = renderer.FileExplorerState{
		Open:    true,
		Root:    root,
		Entries: entries,
		Side:    g.cfg.FileExplorer.Side,
	}
	g.closePalette()
	g.overlayState = renderer.OverlayState{}
	g.closeMenu()
	g.closeSearch()

	// Reset prevKeys for all explorer-relevant keys to the CURRENT pressed state.
	// This prevents stale "was pressed" state from prior handlers causing missed
	// or double-fired edge detection on the first explorer frame.
	for _, k := range explorerInputKeys {
		g.prevKeys[k] = ebiten.IsKeyPressed(k)
	}
	g.explorerRepeatActive = false
	g.screenDirty = true
}

// explorerInputKeys is the set of keys the file explorer handles.
// Declared here so openFileExplorer and handleFileExplorerInput share the same set.
var explorerInputKeys = []ebiten.Key{
	ebiten.KeyEnter, ebiten.KeyBackspace,
	ebiten.KeyArrowUp, ebiten.KeyArrowDown, ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
	ebiten.KeyN, ebiten.KeyR, ebiten.KeyD,
	ebiten.KeyC, ebiten.KeyX, ebiten.KeyP,
	ebiten.KeyO, ebiten.KeyE, ebiten.KeyY,
}

// closeFileExplorer closes the file explorer sidebar.
func (g *Game) closeFileExplorer() {
	g.fileExplorerState = renderer.FileExplorerState{}
	g.screenDirty = true
}

// reloadExplorerTree rebuilds the entry list from the current root.
// It preserves: scroll position, cursor (by path), and the expanded state of
// every directory that was open before the reload.
func (g *Game) reloadExplorerTree() {
	st := &g.fileExplorerState

	// Snapshot state before rebuild.
	var cursorPath string
	if st.Cursor >= 0 && st.Cursor < len(st.Entries) {
		cursorPath = st.Entries[st.Cursor].Path
	}
	expandedPaths := make(map[string]bool)
	for _, e := range st.Entries {
		if e.Expanded {
			expandedPaths[e.Path] = true
		}
	}

	entries, err := fileexplorer.BuildTree(st.Root)
	if err != nil {
		return
	}

	// Replay expansions. Iterating forward is correct: ExpandAt inserts
	// children immediately after the parent, so the loop naturally visits
	// them and can re-expand nested dirs too.
	for i := 0; i < len(entries); i++ {
		if entries[i].IsDir && expandedPaths[entries[i].Path] {
			if expanded, err := fileexplorer.ExpandAt(entries, i); err == nil {
				entries = expanded
			}
		}
	}

	// Restore cursor to the same path; fall back to 0 if gone.
	cursor := 0
	if cursorPath != "" {
		if idx := fileexplorer.FindIdx(entries, cursorPath); idx >= 0 {
			cursor = idx
		}
	}

	st.Entries = entries
	st.Cursor = cursor
	g.screenDirty = true
}

// handleFileExplorerInput routes keyboard events while the file explorer is open.
func (g *Game) handleFileExplorerInput() {
	st := &g.fileExplorerState
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// ESC closes the panel immediately on every physical key-down event.
	// inpututil.IsKeyJustPressed fires exactly once per press regardless of
	// how briefly the key is held — polling-based IsKeyPressed can miss
	// fast taps that start and end between two Update frames.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeFileExplorer()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Cmd+E toggle close (separate from ESC so the E key is tracked independently).
	{
		ePressed := ebiten.IsKeyPressed(ebiten.KeyE)
		eWas := g.prevKeys[ebiten.KeyE]
		g.prevKeys[ebiten.KeyE] = ePressed
		if meta && ePressed && !eWas {
			g.closeFileExplorer()
			return
		}
	}

	// Confirm dialog: only Enter / Y continue; ESC already handled above.
	if st.ConfirmOpen {
		g.handleExplorerConfirmInput()
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
	if g.updateExplorerRepeat(ebiten.KeyArrowUp, now) {
		g.explorerMoveUp()
	}
	if g.updateExplorerRepeat(ebiten.KeyArrowDown, now) {
		g.explorerMoveDown()
	}

	// Non-repeating action keys — edge-triggered only.
	actionKeys := []ebiten.Key{
		ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
		ebiten.KeyEnter,
		ebiten.KeyN, ebiten.KeyR, ebiten.KeyD,
		ebiten.KeyC, ebiten.KeyX, ebiten.KeyP,
		ebiten.KeyO,
	}
	for _, key := range actionKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if !pressed || wasPressed {
			continue
		}

		switch {
		case key == ebiten.KeyArrowRight:
			if st.Cursor < len(st.Entries) && st.Entries[st.Cursor].IsDir && !st.Entries[st.Cursor].Expanded {
				entries, err := fileexplorer.ExpandAt(st.Entries, st.Cursor)
				if err == nil {
					st.Entries = entries
					// Do NOT advance cursor — user stays on the dir they just opened.
				}
			}

		case key == ebiten.KeyArrowLeft:
			if st.Cursor < len(st.Entries) && st.Entries[st.Cursor].IsDir && st.Entries[st.Cursor].Expanded {
				st.Entries = fileexplorer.CollapseAt(st.Entries, st.Cursor)
			}

		case key == ebiten.KeyEnter:
			if st.Cursor >= len(st.Entries) {
				break
			}
			e := st.Entries[st.Cursor]
			if e.IsDir {
				if e.Expanded {
					st.Entries = fileexplorer.CollapseAt(st.Entries, st.Cursor)
				} else {
					entries, err := fileexplorer.ExpandAt(st.Entries, st.Cursor)
					if err == nil {
						st.Entries = entries
					}
				}
			} else {
				// Insert path into focused PTY.
				g.focused.Term.SendBytes([]byte(e.Path))
				g.closeFileExplorer()
				return
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
				st.StatusTimer = 60
			}
			g.reloadExplorerTree()

		case key == ebiten.KeyD && !meta:
			if st.Cursor < len(st.Entries) {
				path := st.Entries[st.Cursor].Path
				name := st.Entries[st.Cursor].Name
				st.ConfirmMsg = "Delete " + name + "?"
				captured := path
				st.ConfirmAction = func() {
					_ = fileexplorer.DeletePath(captured)
					g.reloadExplorerTree()
				}
				st.ConfirmOpen = true
				// Reset confirm keys so edge detection fires cleanly on next frame.
				g.prevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
				g.prevKeys[ebiten.KeyY] = ebiten.IsKeyPressed(ebiten.KeyY)
			}

		case key == ebiten.KeyR && !meta:
			if st.Cursor < len(st.Entries) {
				st.Mode = renderer.ExplorerModeRename
				st.InputLabel = "Rename:"
				st.InputText = st.Entries[st.Cursor].Name
				g.prevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
				g.prevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)
			}

		case key == ebiten.KeyN && !meta && !shift:
			st.Mode = renderer.ExplorerModeNewFile
			st.InputLabel = "New file:"
			st.InputText = ""
			g.prevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
			g.prevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)

		case key == ebiten.KeyN && shift:
			st.Mode = renderer.ExplorerModeNewDir
			st.InputLabel = "New dir:"
			st.InputText = ""
			g.prevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
			g.prevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)

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
				_ = cmd.Start()
			}
		}
	}
}

// updateExplorerRepeat handles a navigation key with repeat semantics.
// Returns true if the action should fire this frame (initial press or repeat tick).
func (g *Game) updateExplorerRepeat(key ebiten.Key, now time.Time) bool {
	pressed := ebiten.IsKeyPressed(key)
	was := g.prevKeys[key]
	g.prevKeys[key] = pressed

	if !pressed {
		if g.explorerRepeatActive && g.explorerRepeatKey == key {
			g.explorerRepeatActive = false
		}
		return false
	}
	if !was {
		// Initial press — fire immediately and start repeat timer.
		g.explorerRepeatKey = key
		g.explorerRepeatActive = true
		g.explorerRepeatStart = now
		g.explorerRepeatLast = now
		return true
	}
	// Held — fire only after delay + interval.
	if g.explorerRepeatActive && g.explorerRepeatKey == key &&
		now.Sub(g.explorerRepeatStart) >= keyRepeatDelay &&
		now.Sub(g.explorerRepeatLast) >= keyRepeatInterval {
		g.explorerRepeatLast = now
		return true
	}
	return false
}

// updatePaletteRepeat handles Up/Down arrow keys with repeat semantics in the
// command palette. Returns true when the action should fire this frame.
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

// explorerMoveUp moves the cursor up one entry.
func (g *Game) explorerMoveUp() {
	st := &g.fileExplorerState
	if st.Cursor > 0 {
		st.Cursor--
		g.explorerEnsureVisible()
	}
}

// explorerMoveDown moves the cursor down one entry.
func (g *Game) explorerMoveDown() {
	st := &g.fileExplorerState
	if st.Cursor < len(st.Entries)-1 {
		st.Cursor++
		g.explorerEnsureVisible()
	}
}

// handleExplorerConfirmInput handles Enter/Y in the confirm dialog.
// ESC is handled at the top of handleFileExplorerInput before this is called.
func (g *Game) handleExplorerConfirmInput() {
	st := &g.fileExplorerState
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyY} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if pressed && !wasPressed {
			if st.ConfirmAction != nil {
				st.ConfirmAction()
			}
			st.ConfirmOpen = false
			return
		}
	}
}

// handleExplorerInputMode handles text input for rename/new-file/new-dir modes.
// ESC is handled at the top of handleFileExplorerInput before this is called.
func (g *Game) handleExplorerInputMode() {
	st := &g.fileExplorerState
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)

	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyBackspace} {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if !pressed || wasPressed {
			continue
		}
		switch key {
		case ebiten.KeyBackspace:
			if !meta {
				runes := []rune(st.InputText)
				if len(runes) > 0 {
					st.InputText = string(runes[:len(runes)-1])
				}
			}
		case ebiten.KeyEnter:
			g.executeExplorerInputMode()
		}
	}

	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= 0x20 && r != 0x7f {
				st.InputText += string(r)
			}
		}
	}
}

// executeExplorerInputMode commits the rename/new-file/new-dir operation.
func (g *Game) executeExplorerInputMode() {
	st := &g.fileExplorerState
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
			newPath, err := fileexplorer.RenamePath(oldPath, name)
			if err != nil {
				st.StatusMsg = "Error: " + err.Error()
				st.StatusTimer = 60
			} else {
				_ = newPath
			}
		}
	case renderer.ExplorerModeNewFile:
		_, err := fileexplorer.CreateFile(dstDir, name)
		if err != nil {
			st.StatusMsg = "Error: " + err.Error()
			st.StatusTimer = 60
		}
	case renderer.ExplorerModeNewDir:
		_, err := fileexplorer.CreateDir(dstDir, name)
		if err != nil {
			st.StatusMsg = "Error: " + err.Error()
			st.StatusTimer = 60
		}
	}

	st.Mode = renderer.ExplorerModeNormal
	st.InputText = ""
	g.reloadExplorerTree()
}

// explorerEnsureVisible adjusts ScrollOffset so the cursor row is in view.
func (g *Game) explorerEnsureVisible() {
	st := &g.fileExplorerState
	if st.RowH <= 0 {
		return
	}
	rowTop := st.Cursor * st.RowH
	rowBot := rowTop + st.RowH
	if rowTop < st.ScrollOffset {
		st.ScrollOffset = rowTop
	}
	if rowBot > st.ScrollOffset+st.MaxScroll+st.RowH {
		st.ScrollOffset = rowBot - st.MaxScroll - st.RowH
		if st.ScrollOffset < 0 {
			st.ScrollOffset = 0
		}
	}
}

// handleExplorerClick moves the cursor to the clicked row or toggles dir expand.
func (g *Game) handleExplorerClick(mx, my int, panelRect image.Rectangle) {
	st := &g.fileExplorerState
	if st.RowH <= 0 {
		return
	}
	contentTop := panelRect.Min.Y + g.font.CellH + 6 // header height
	relY := my - contentTop + st.ScrollOffset
	if relY < 0 {
		return
	}
	idx := relY / st.RowH
	if idx < 0 || idx >= len(st.Entries) {
		return
	}
	if idx == st.Cursor && st.Entries[idx].IsDir {
		if st.Entries[idx].Expanded {
			st.Entries = fileexplorer.CollapseAt(st.Entries, idx)
		} else {
			entries, err := fileexplorer.ExpandAt(st.Entries, idx)
			if err == nil {
				st.Entries = entries
			}
		}
	}
	st.Cursor = idx
	g.screenDirty = true
}

// sanitizeTitle strips control characters and caps length (SEC-003).
func sanitizeTitle(s string) string {
	out := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
	if len(out) > 256 {
		out = out[:256]
	}
	return out
}

// handlePaste reads the system clipboard and sends it to the focused PTY.
func (g *Game) handlePaste() {
	out, err := exec.Command("pbpaste").Output()
	if err != nil || len(out) == 0 {
		return
	}
	g.focused.Term.Buf.RLock()
	bracketed := g.focused.Term.Buf.BracketedPaste
	g.focused.Term.Buf.RUnlock()
	if bracketed {
		g.focused.Term.SendBytes([]byte("\x1B[200~"))
		g.focused.Term.SendBytes(out)
		g.focused.Term.SendBytes([]byte("\x1B[201~"))
	} else {
		g.focused.Term.SendBytes(out)
	}
}

// handleMouse dispatches mouse events.
func (g *Game) handleMouse() {
	mx, my := ebiten.CursorPosition()
	pad := g.cfg.Window.Padding
	tabBarH := g.renderer.TabBarHeight()

	leftPressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	leftWas := g.prevMouseButtons[ebiten.MouseButtonLeft]
	rightPressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight)
	rightWas := g.prevMouseButtons[ebiten.MouseButtonRight]

	// Any mouse button state change or scroll makes the frame dirty.
	if leftPressed != leftWas || rightPressed != rightWas {
		g.screenDirty = true
	}

	// When blocks are enabled, cursor movement may change hover state.
	// Signal a redraw so the blocks layer is updated this frame.
	if g.blocksEnabled && (mx != g.prevMX || my != g.prevMY) {
		g.screenDirty = true
	}
	g.prevMX = mx
	g.prevMY = my
	_, scrollY := ebiten.Wheel()
	if scrollY != 0 {
		g.screenDirty = true
	}

	// Block copy buttons — left click while blocks are enabled.
	if g.blocksEnabled && !leftWas && leftPressed {
		if h := g.renderer.BlockHover; h.Active && h.CopyTarget != renderer.CopyNone {
			var copyText, label string
			h.Buf.RLock()
			switch h.CopyTarget {
			case renderer.CopyCmdText:
				// Command text lives on the prompt row; strip the prompt prefix.
				raw := h.Buf.TextRange(h.AbsStart, h.AbsStart)
				copyText = renderer.StripPrompt(raw)
				label = "Command copied"
			case renderer.CopyOutput:
				// AbsCmdRow is the first output row (cursor position when C fires).
				if h.AbsCmdRow >= 0 && h.AbsEnd >= h.AbsCmdRow {
					copyText = h.Buf.TextRange(h.AbsCmdRow, h.AbsEnd)
					label = "Output copied"
				}
			case renderer.CopyAll:
				copyText = h.Buf.TextRange(h.AbsStart, h.AbsEnd)
				label = "Block copied"
			}
			h.Buf.RUnlock()
			if copyText != "" {
				cmd := exec.Command("pbcopy")
				cmd.Stdin = strings.NewReader(copyText)
				_ = cmd.Run()
				g.flashStatus(label)
			}
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	// Palette dismisses on any click.
	if g.paletteState.Open {
		if leftPressed && !leftWas {
			g.closePalette()
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// File explorer: wheel scrolls content, left-click outside panel closes it.
	if g.fileExplorerState.Open {
		panelW := g.renderer.FileExplorerPanelWidth()
		var panelX int
		physW := int(float64(g.winW) * g.dpi)
		if g.fileExplorerState.Side == "right" {
			panelX = physW - panelW
		}
		panelRect := image.Rect(panelX, tabBarH, panelX+panelW, int(float64(g.winH)*g.dpi))

		if scrollY != 0 && g.fileExplorerState.RowH > 0 {
			step := int(-scrollY*float64(g.fileExplorerState.RowH)*3 + 0.5)
			g.fileExplorerState.ScrollOffset += step
			if g.fileExplorerState.ScrollOffset < 0 {
				g.fileExplorerState.ScrollOffset = 0
			}
			if g.fileExplorerState.ScrollOffset > g.fileExplorerState.MaxScroll {
				g.fileExplorerState.ScrollOffset = g.fileExplorerState.MaxScroll
			}
		}
		if leftPressed && !leftWas {
			if image.Pt(mx, my).In(panelRect) {
				g.handleExplorerClick(mx, my, panelRect)
			} else {
				g.closeFileExplorer()
			}
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Overlay: wheel scrolls, click dismisses.
	if g.overlayState.Open {
		if scrollY != 0 && g.overlayState.RowH > 0 {
			step := int(-scrollY*float64(g.overlayState.RowH)*3 + 0.5)
			g.overlayState.ScrollOffset += step
			if g.overlayState.ScrollOffset < 0 {
				g.overlayState.ScrollOffset = 0
			}
			if g.overlayState.ScrollOffset > g.overlayState.MaxScroll {
				g.overlayState.ScrollOffset = g.overlayState.MaxScroll
			}
		}
		if leftPressed && !leftWas {
			g.overlayState = renderer.OverlayState{}
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// ? button in status bar toggles the keybinding overlay.
	if leftPressed && !leftWas && image.Pt(mx, my).In(g.statusBarState.HelpBtnRect) {
		g.toggleOverlay()
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Tab switcher dismisses on any click.
	if g.tabSwitcherState.Open {
		if leftPressed && !leftWas {
			g.tabSwitcherState.Open = false
			g.screenDirty = true
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Context menu takes priority: route all mouse events to menu handling.
	if g.menuState.Open {
		g.updateMenuHover(mx, my)

		if leftPressed && !leftWas {
			if g.menuState.SubOpen && image.Pt(mx, my).In(g.menuState.SubRect) {
				if g.menuState.SubHoverIdx >= 0 {
					item := g.menuState.SubItems[g.menuState.SubHoverIdx]
					if item.Action != nil {
						g.closeMenu()
						item.Action()
					}
				}
			} else if g.menuState.HoverIdx >= 0 &&
				len(g.menuState.Items[g.menuState.HoverIdx].Children) == 0 {
				item := g.menuState.Items[g.menuState.HoverIdx]
				if item.Action != nil {
					g.closeMenu()
					item.Action()
				} else {
					g.closeMenu()
				}
			} else if !image.Pt(mx, my).In(g.menuState.Rect) &&
				(!g.menuState.SubOpen || !image.Pt(mx, my).In(g.menuState.SubRect)) {
				g.closeMenu()
			}
		}

		if rightPressed && !rightWas {
			// Right-click while menu is open: reposition to new cursor location.
			g.openContextMenu(mx, my)
		}

		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Click in tab bar — switch tab or rename on double-click.
	// tabW must match the renderer's cap (24 * CellW) so click regions align with drawn tabs.
	if my < tabBarH {
		if leftPressed && !leftWas {
			physW := int(float64(g.winW) * g.dpi)
			numTabs := len(g.tabs)
			if numTabs > 0 {
				maxTabW := g.cfg.Tabs.MaxWidthChars * g.font.CellW
				tabW := physW / numTabs
				if tabW > maxTabW {
					tabW = maxTabW
				}
				if tabW > 0 {
					clicked := mx / tabW
					if clicked >= 0 && clicked < numTabs {
						now := time.Now()
						if clicked == g.activeTab && now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
							// Double-click on the active tab → rename.
							g.startRenameTab(clicked)
						} else {
							g.switchTab(clicked)
						}
						g.lastClickTime = now
					}
				}
			}
		} else if rightPressed && !rightWas {
			// Right-click in tab bar → show tab context menu.
			g.openTabContextMenu(mx, my)
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	g.focused.Term.Buf.RLock()
	mouseMode := g.focused.Term.Buf.MouseMode
	sgrMouse := g.focused.Term.Buf.SgrMouse
	g.focused.Term.Buf.RUnlock()

	// Terminal-level events always take priority over PTY mouse passthrough.

	// Right-click opens context menu regardless of PTY mouse mode.
	if rightPressed && !rightWas && g.cfg.Help.ContextMenu {
		g.openContextMenu(mx, my)
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Click on an inactive pane always switches focus, regardless of PTY mouse mode.
	if leftPressed && !leftWas {
		if clicked := g.layout.PaneAt(mx, my); clicked != nil && clicked != g.focused {
			g.setFocus(clicked)
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	if mouseMode == 0 {
		col := (mx - g.focused.Rect.Min.X - pad) / g.font.CellW
		row := (my - g.focused.Rect.Min.Y - pad) / g.font.CellH

		g.focused.Term.Buf.RLock()
		maxRow := g.focused.Term.Buf.Rows - 1
		maxCol := g.focused.Term.Buf.Cols - 1
		g.focused.Term.Buf.RUnlock()

		if col < 0 {
			col = 0
		} else if col > maxCol {
			col = maxCol
		}
		if row < 0 {
			row = 0
		} else if row > maxRow {
			row = maxRow
		}

		if leftPressed && !leftWas {
			now := time.Now()
			sameCell := row == g.lastClickRow && col == g.lastClickCol
			if sameCell && now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
				g.clickCount++
			} else {
				g.clickCount = 1
			}
			g.lastClickTime = now
			g.lastClickRow = row
			g.lastClickCol = col

			g.focused.Term.Buf.Lock()
			switch g.clickCount {
			case 1:
				g.selDragging = true
				g.focused.Term.Buf.Selection = terminal.Selection{
					Active:   true,
					StartRow: row, StartCol: col,
					EndRow:   row, EndCol: col,
				}
			case 2:
				g.selDragging = false
				g.focused.Term.Buf.Selection = g.wordSelection(row, col)
			default:
				g.selDragging = false
				g.focused.Term.Buf.Selection = terminal.Selection{
					Active:   true,
					StartRow: row, StartCol: 0,
					EndRow:   row, EndCol: g.focused.Term.Buf.Cols - 1,
				}
				g.clickCount = 0
			}
			g.focused.Term.Buf.BumpRenderGen()
			g.focused.Term.Buf.Unlock()
		} else if leftPressed && leftWas && g.selDragging {
			g.focused.Term.Buf.Lock()
			g.focused.Term.Buf.Selection.EndRow = row
			g.focused.Term.Buf.Selection.EndCol = col
			g.focused.Term.Buf.BumpRenderGen()
			g.focused.Term.Buf.Unlock()
			g.screenDirty = true
		} else if !leftPressed && leftWas {
			if g.selDragging {
				g.selDragging = false
				g.focused.Term.Buf.Lock()
				sel := g.focused.Term.Buf.Selection.Normalize()
				if sel.StartRow == sel.EndRow && sel.StartCol == sel.EndCol {
					g.focused.Term.Buf.Selection = terminal.Selection{}
				}
				g.focused.Term.Buf.Unlock()
			}
		}

		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// PTY mouse mode.
	col := (mx-g.focused.Rect.Min.X-pad)/g.font.CellW + 1
	row := (my-g.focused.Rect.Min.Y-pad)/g.font.CellH + 1
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}

	type btnEntry struct {
		btn    ebiten.MouseButton
		btnNum int
	}
	buttons := []btnEntry{
		{ebiten.MouseButtonLeft, 0},
		{ebiten.MouseButtonMiddle, 1},
		{ebiten.MouseButtonRight, 2},
	}
	for _, b := range buttons {
		pressed := ebiten.IsMouseButtonPressed(b.btn)
		was := g.prevMouseButtons[b.btn]
		if pressed && !was {
			g.mouseHeldBtn = b.btnNum
			g.sendMouseEvent(b.btnNum, col, row, true, sgrMouse)
		} else if !pressed && was {
			if g.mouseHeldBtn == b.btnNum {
				g.mouseHeldBtn = -1
			}
			g.sendMouseEvent(b.btnNum, col, row, false, sgrMouse)
		}
		g.prevMouseButtons[b.btn] = pressed
	}

	// Send motion events for mode 1002 (button-tracking: only while a button is held)
	// and mode 1003 (any-motion: always). The motion button code = held button + 32,
	// or 35 when no button is held (mode 1003 only).
	if mouseMode >= 1002 && (col != g.lastMouseCol || row != g.lastMouseRow) {
		if mouseMode == 1003 || g.mouseHeldBtn >= 0 {
			motionBtn := 35 // no button held
			if g.mouseHeldBtn >= 0 {
				motionBtn = g.mouseHeldBtn + 32
			}
			g.sendMouseMotion(motionBtn, col, row, sgrMouse)
		}
		g.lastMouseCol = col
		g.lastMouseRow = row
	}

	_, wy := ebiten.Wheel()
	if wy != 0 {
		// Shift+scroll bypasses PTY mouse mode and scrolls the terminal's own
		// scrollback buffer (standard behaviour in iTerm2, kitty, etc.).
		if ebiten.IsKeyPressed(ebiten.KeyShift) {
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
			}
		} else {
			btn := 64
			if wy < 0 {
				btn = 65
			}
			g.sendMouseEvent(btn, col, row, true, sgrMouse)
		}
	}
}

// wordSelection returns a Selection covering the word at (row, col).
// Must be called with Buf write lock held.
func (g *Game) wordSelection(row, col int) terminal.Selection {
	buf := g.focused.Term.Buf
	isWordChar := func(r rune) bool {
		return r != ' ' && r != 0 &&
			(unicode.IsLetter(r) || unicode.IsDigit(r) ||
				r == '_' || r == '.' || r == '/')
	}

	cell := buf.GetDisplayCell(row, col)
	if !isWordChar(cell.Char) {
		return terminal.Selection{Active: true, StartRow: row, StartCol: col, EndRow: row, EndCol: col}
	}

	startCol := col
	for startCol > 0 {
		if !isWordChar(buf.GetDisplayCell(row, startCol-1).Char) {
			break
		}
		startCol--
	}

	endCol := col
	for endCol < buf.Cols-1 {
		if !isWordChar(buf.GetDisplayCell(row, endCol+1).Char) {
			break
		}
		endCol++
	}

	return terminal.Selection{Active: true, StartRow: row, StartCol: startCol, EndRow: row, EndCol: endCol}
}

// copySelection copies the current selection text to the clipboard via pbcopy.
func (g *Game) copySelection() {
	g.focused.Term.Buf.RLock()
	sel := g.focused.Term.Buf.Selection
	rows := g.focused.Term.Buf.Rows
	cols := g.focused.Term.Buf.Cols

	if !sel.Active {
		g.focused.Term.Buf.RUnlock()
		return
	}

	norm := sel.Normalize()
	lines := make([]string, 0, norm.EndRow-norm.StartRow+1)

	for r := norm.StartRow; r <= norm.EndRow && r < rows; r++ {
		colStart := 0
		colEnd := cols - 1
		if r == norm.StartRow {
			colStart = norm.StartCol
		}
		if r == norm.EndRow {
			colEnd = norm.EndCol
		}

		var sb strings.Builder
		for c := colStart; c <= colEnd && c < cols; c++ {
			ch := g.focused.Term.Buf.GetDisplayCell(r, c).Char
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(ch)
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	g.focused.Term.Buf.RUnlock()

	text := strings.Join(lines, "\n")
	if text == "" {
		return
	}

	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

// sendMouseEvent encodes and sends a single mouse event to the focused PTY.
func (g *Game) sendMouseEvent(btn, col, row int, press bool, sgr bool) {
	if sgr {
		final := 'M'
		if !press && btn < 64 {
			final = 'm'
		}
		seq := fmt.Sprintf("\x1B[<%d;%d;%d%c", btn, col, row, final)
		g.focused.Term.SendBytes([]byte(seq))
	} else {
		if col > 222 || row > 222 {
			return
		}
		b := byte(btn + 32) // #nosec G115 — btn is 0-4; col/row guarded ≤222 above; all fit byte
		if !press {
			b = 35
		}
		g.focused.Term.SendBytes([]byte{0x1B, '[', 'M', b, byte(col + 32), byte(row + 32)}) // #nosec G115
	}
}

// sendMouseMotion encodes and sends a motion event to the focused PTY.
// btn is the motion button code (held button + 32, or 35 for no-button).
func (g *Game) sendMouseMotion(btn, col, row int, sgr bool) {
	if sgr {
		seq := fmt.Sprintf("\x1B[<%d;%d;%dM", btn, col, row)
		g.focused.Term.SendBytes([]byte(seq))
	} else {
		if col > 222 || row > 222 {
			return
		}
		g.focused.Term.SendBytes([]byte{0x1B, '[', 'M', byte(btn), byte(col + 32), byte(row + 32)}) // #nosec G115 — col/row guarded ≤222 above
	}
}

// --- Tab management ---

// newTab creates a new tab and switches to it.
// The starting directory is controlled by cfg.Tabs.NewTabDir:
//   - "cwd"  → inherit the active tab's current working directory
//   - "home" → always open in $HOME
func (g *Game) newTab() {
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	var dir string
	switch g.cfg.Tabs.NewTabDir {
	case "home":
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	default: // "cwd"
		dir = g.statusBarState.Cwd
	}

	t, err := tab.New(g.cfg, paneRect, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
}

// closeActiveTab closes all panes in the active tab and removes it.
func (g *Game) closeActiveTab() {
	for _, leaf := range g.tabs[g.activeTab].Layout.Leaves() {
		leaf.Pane.Term.Close()
	}
	g.tabs = append(g.tabs[:g.activeTab], g.tabs[g.activeTab+1:]...)
	if len(g.tabs) == 0 {
		g.layout = nil
		g.focused = nil
		return
	}
	if g.activeTab >= len(g.tabs) {
		g.activeTab = len(g.tabs) - 1
	}
	g.renderer.SetLayoutDirty()
	g.syncActive()
}

// switchTab activates the tab at index i.
func (g *Game) switchTab(i int) {
	if i < 0 || i >= len(g.tabs) {
		return
	}
	g.zoomed = false
	g.activeTab = i
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
	g.syncActive()
	g.selDragging = false
	g.statusBarState.ForegroundProc = ""
	g.focused.Term.RefreshForeground()
	if g.searchState.Open {
		g.closeSearch()
	}
	g.screenDirty = true
}

// nextTab cycles to the next tab.
func (g *Game) nextTab() {
	g.switchTab((g.activeTab + 1) % len(g.tabs))
}

// prevTab cycles to the previous tab.
func (g *Game) prevTab() {
	g.switchTab((g.activeTab - 1 + len(g.tabs)) % len(g.tabs))
}

// --- Tab pinning & switcher ---


// pinnedTab returns the index of the tab pinned to the given slot rune, or -1.
func (g *Game) pinnedTab(slot rune) int {
	for i, t := range g.tabs {
		if t.PinnedSlot == slot {
			return i
		}
	}
	return -1
}

// pinTab pins the active tab to the given home-row slot, evicting any previous
// occupant. Calling again with the same slot while already pinned there unpins.
func (g *Game) pinTab(slot rune) {
	active := g.tabs[g.activeTab]
	if active.PinnedSlot == slot {
		active.PinnedSlot = 0 // toggle off
		g.screenDirty = true
		return
	}
	// Evict any tab currently holding this slot.
	for _, t := range g.tabs {
		if t.PinnedSlot == slot {
			t.PinnedSlot = 0
		}
	}
	active.PinnedSlot = slot
	g.screenDirty = true
}

// switchToSlot activates the tab pinned to the given home-row slot.
// Does nothing if no tab is pinned there.
func (g *Game) switchToSlot(slot rune) {
	if idx := g.pinnedTab(slot); idx >= 0 {
		g.switchTab(idx)
	}
}

// openTabSwitcher opens (or closes) the tab switcher overlay.
func (g *Game) openTabSwitcher() {
	if g.tabSwitcherState.Open {
		g.tabSwitcherState.Open = false
	} else {
		g.tabSwitcherState.Open = true
		g.tabSwitcherState.Cursor = g.activeTab
	}
	g.screenDirty = true
}

// handlePinInput processes the second keypress of the Cmd+Space chord.
// A home-row letter jumps to that slot; Shift+letter pins the active tab there.
// Any other keypress cancels the mode without action.
func (g *Game) handlePinInput() {
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	type slotKey struct {
		key  ebiten.Key
		slot rune
	}
	keys := []slotKey{
		{ebiten.KeyA, 'a'}, {ebiten.KeyS, 's'}, {ebiten.KeyD, 'd'},
		{ebiten.KeyF, 'f'}, {ebiten.KeyG, 'g'}, {ebiten.KeyH, 'h'},
		{ebiten.KeyJ, 'j'}, {ebiten.KeyK, 'k'}, {ebiten.KeyL, 'l'},
	}

	for _, hr := range keys {
		pressed := ebiten.IsKeyPressed(hr.key)
		wasPressed := g.prevKeys[hr.key]
		g.prevKeys[hr.key] = pressed
		if pressed && !wasPressed {
			if shift {
				g.pinTab(hr.slot)
			} else {
				g.switchToSlot(hr.slot)
			}
			g.pinMode = false
			g.screenDirty = true
			return
		}
	}

	// ESC cancels immediately. inpututil catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.pinMode = false
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Any other keypress (Space, Enter) cancels the mode.
	cancelKeys := []ebiten.Key{
		ebiten.KeySpace, ebiten.KeyEnter, ebiten.KeyNumpadEnter,
	}
	for _, key := range cancelKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if pressed && !wasPressed {
			g.pinMode = false
			g.screenDirty = true
			return
		}
	}
	// Cancel on any printable char not in home row.
	if len(ebiten.AppendInputChars(nil)) > 0 {
		g.pinMode = false
		g.screenDirty = true
	}
}

// reorderTab moves the tab at index from to index to, keeping activeTab correct.
func (g *Game) reorderTab(from, to int) {
	n := len(g.tabs)
	if from == to || from < 0 || to < 0 || from >= n || to >= n {
		return
	}

	t := g.tabs[from]

	// Build new slice without the tab at from, then insert at to.
	without := make([]*tab.Tab, 0, n-1)
	without = append(without, g.tabs[:from]...)
	without = append(without, g.tabs[from+1:]...)

	result := make([]*tab.Tab, 0, n)
	result = append(result, without[:to]...)
	result = append(result, t)
	result = append(result, without[to:]...)
	g.tabs = result

	// Keep activeTab pointing at the same tab after the move.
	if g.activeTab == from {
		g.activeTab = to
	} else if from < to && g.activeTab > from && g.activeTab <= to {
		g.activeTab--
	} else if from > to && g.activeTab < from && g.activeTab >= to {
		g.activeTab++
	}

	g.tabSwitcherState.Cursor = to
	g.syncActive()
	g.screenDirty = true
}

// handleTabSwitcherInput processes keyboard events while the tab switcher is open.
func (g *Game) handleTabSwitcherInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.tabSwitcherState.Open = false
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	keys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyEnter, ebiten.KeyNumpadEnter,
		ebiten.KeyT,
	}
	for _, key := range keys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch {
			case meta && shift && key == ebiten.KeyT:
				g.tabSwitcherState.Open = false
			case key == ebiten.KeyArrowUp && !shift:
				if g.tabSwitcherState.Cursor > 0 {
					g.tabSwitcherState.Cursor--
				}
			case key == ebiten.KeyArrowDown && !shift:
				if g.tabSwitcherState.Cursor < len(g.tabs)-1 {
					g.tabSwitcherState.Cursor++
				}
			case key == ebiten.KeyArrowUp && shift:
				c := g.tabSwitcherState.Cursor
				if c > 0 {
					g.reorderTab(c, c-1)
				}
			case key == ebiten.KeyArrowDown && shift:
				c := g.tabSwitcherState.Cursor
				if c < len(g.tabs)-1 {
					g.reorderTab(c, c+1)
				}
			case key == ebiten.KeyEnter || key == ebiten.KeyNumpadEnter:
				g.switchTab(g.tabSwitcherState.Cursor)
				g.tabSwitcherState.Open = false
			}
		}
		g.prevKeys[key] = pressed
	}
	g.screenDirty = true
}

// --- Pane management ---

// splitH splits the focused pane horizontally (Cmd+D).
func (g *Game) splitH() {
	g.zoomed = false
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	newRoot, newPane, err := g.layout.SplitH(g.focused, g.cfg, g.font.CellW, g.font.CellH)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.setFocus(newPane)
}

// splitV splits the focused pane vertically (Cmd+Shift+D).
func (g *Game) splitV() {
	g.zoomed = false
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	newRoot, newPane, err := g.layout.SplitV(g.focused, g.cfg, g.font.CellW, g.font.CellH)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.setFocus(newPane)
}

// closePane removes a pane. Focuses the nearest remaining pane.
func (g *Game) closePane(p *pane.Pane) {
	g.zoomed = false
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	var nextFocus *pane.Pane
	if p == g.focused {
		nextFocus = g.layout.NextLeaf(p)
	}

	newRoot := g.layout.Remove(p)
	if newRoot == nil {
		return
	}
	g.updateLayout(newRoot)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}

	g.renderer.SetLayoutDirty()
	if nextFocus != nil && nextFocus != p {
		g.setFocus(nextFocus)
	} else if len(g.layout.Leaves()) > 0 {
		g.setFocus(g.layout.Leaves()[0].Pane)
	}
}

// setFocus updates the focused pane in both Game and the active tab.
func (g *Game) setFocus(p *pane.Pane) {
	g.focused = p
	g.tabs[g.activeTab].Focused = p
	g.selDragging = false
	g.scrollAccum = 0
	g.mouseHeldBtn = -1
	g.lastMouseCol = 0
	g.lastMouseRow = 0
	g.statusBarState.ForegroundProc = ""
	p.Term.RefreshForeground()
	if g.searchState.Open {
		g.closeSearch()
	}
	// Force both old and new pane borders to redraw; the pane cache
	// does not track focus state, so clearing it ensures correct borders.
	g.renderer.ClearPaneCache()
	g.screenDirty = true
}

// focusDir moves focus to the nearest pane in direction (dx, dy).
func (g *Game) focusDir(dx, dy int) {
	if p := g.layout.NeighborInDir(g.focused, dx, dy); p != nil {
		g.setFocus(p)
	}
}

// openTabContextMenu shows a small tab-specific context menu (Rename, Close).
func (g *Game) openTabContextMenu(px, py int) {
	if g.menuState.Open {
		g.renderer.ClearPaneCache()
	}
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	items := []help.MenuItem{
		{Label: "Rename Tab", Action: func() { g.startRenameTab(g.activeTab) }},
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

// --- Tab renaming ---

// startRenameTab enters inline rename mode for tab at index idx.
func (g *Game) startRenameTab(idx int) {
	if idx < 0 || idx >= len(g.tabs) {
		return
	}
	// Cancel any existing rename first.
	g.cancelRename()
	g.tabs[idx].Renaming = true
	g.tabs[idx].RenameText = g.tabs[idx].Title
}

// commitRename applies the rename text and exits rename mode.
func (g *Game) commitRename() {
	for _, t := range g.tabs {
		if t.Renaming {
			t.Title = sanitizeTitle(t.RenameText)
			t.UserRenamed = true
			t.Renaming = false
			t.RenameText = ""
			break
		}
	}
}

// cancelRename exits rename mode without applying changes.
func (g *Game) cancelRename() {
	for _, t := range g.tabs {
		if t.Renaming {
			t.Renaming = false
			t.RenameText = ""
			break
		}
	}
}

// renamingTabIdx returns the index of the tab currently being renamed, or -1.
func (g *Game) renamingTabIdx() int {
	for i, t := range g.tabs {
		if t.Renaming {
			return i
		}
	}
	return -1
}

// handleRenameInput processes keyboard input while a tab rename is in progress.
func (g *Game) handleRenameInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.cancelRename()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	edgeKeys := []ebiten.Key{
		ebiten.KeyEnter,
		ebiten.KeyNumpadEnter,
		ebiten.KeyBackspace,
		ebiten.KeyV,
	}
	for _, key := range edgeKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch key {
			case ebiten.KeyEnter, ebiten.KeyNumpadEnter:
				g.commitRename()
			case ebiten.KeyBackspace:
				idx := g.renamingTabIdx()
				if idx >= 0 {
					runes := []rune(g.tabs[idx].RenameText)
					if len(runes) > 0 {
						g.tabs[idx].RenameText = string(runes[:len(runes)-1])
					}
				}
			case ebiten.KeyV:
				if meta {
					if out, err := exec.Command("pbpaste").Output(); err == nil {
						idx := g.renamingTabIdx()
						if idx >= 0 {
							for _, r := range string(out) {
								if r >= 0x20 && r != 0x7f {
									g.tabs[idx].RenameText += string(r)
								}
							}
						}
					}
				}
			}
		}
		g.prevKeys[key] = pressed
	}
	g.prevKeys[ebiten.KeyMeta] = meta

	if !meta {
		idx := g.renamingTabIdx()
		if idx >= 0 {
			for _, r := range ebiten.AppendInputChars(nil) {
				if r >= 0x20 && r != 0x7f {
					g.tabs[idx].RenameText += string(r)
				}
			}
		}
	}
}

// toggleZoom fullscreens the focused pane (Cmd+Z). Calling again restores the layout.
func (g *Game) toggleZoom() {
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	fullRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	g.zoomed = !g.zoomed
	g.screenDirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache() // force all panes to redraw after zoom in/out
	if g.zoomed {
		p := g.focused
		p.Rect = fullRect
		cols := (fullRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW
		rows := (fullRect.Dy() - g.cfg.Window.Padding*2) / g.font.CellH
		if cols < 1 {
			cols = 1
		}
		if rows < 1 {
			rows = 1
		}
		p.Cols = cols
		p.Rows = rows
		p.Term.Resize(cols, rows)
	} else {
		g.layout.ComputeRects(fullRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
		for _, leaf := range g.layout.Leaves() {
			leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
		}
	}
}

// showConfirm opens the close-confirmation dialog with the given message and
// registers the action to execute if the user confirms.
func (g *Game) showConfirm(msg string, action func()) {
	g.confirmState = renderer.ConfirmState{Open: true, Message: msg}
	g.confirmPendingAction = action
}

// handleConfirmInput processes keyboard input while the confirm dialog is open.
// Enter or Y executes the pending action; Escape or N cancels.
func (g *Game) handleConfirmInput() {
	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyN) {
		g.confirmState = renderer.ConfirmState{}
		g.confirmPendingAction = nil
		g.prevKeys[ebiten.KeyEscape] = true
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
			return
		}
	}
}

// toggleOverlay opens the overlay if closed, closes it if open.
func (g *Game) toggleOverlay() {
	if g.overlayState.Open {
		g.overlayState = renderer.OverlayState{}
	} else {
		g.overlayState = renderer.OverlayState{Open: true}
		g.closeMenu()
		g.closePalette()
	}
}

// openPalette opens the command palette, closing any conflicting surfaces.
func (g *Game) openPalette() {
	g.paletteState = renderer.PaletteState{Open: true}
	g.overlayState = renderer.OverlayState{}
	g.closeMenu()
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
		// Panes
		g.splitH,
		g.splitV,
		func() { g.focusDir(-1, 0) },
		func() { g.focusDir(1, 0) },
		func() { g.focusDir(0, -1) },
		func() { g.focusDir(0, 1) },
		g.toggleZoom,
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
		g.openSearch,
		// File Explorer
		g.openFileExplorer,
		// Pins
		func() { g.pinMode = true; g.statusBarState.PinMode = true },
		// Tab Switcher
		g.openTabSwitcher,
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
		// Help
		g.toggleOverlay,
		g.openPalette,
		// App
		func() { os.Exit(0) },
	}

	g.paletteEntries = entries
	g.paletteActions = actions
}

// handlePaletteInput processes keyboard input while the command palette is open.
func (g *Game) handlePaletteInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)

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
		} else {
			g.closePalette()
		}
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// Single-shot keys — edge-triggered only.
	edgeKeys := []ebiten.Key{
		ebiten.KeyBackspace,
		ebiten.KeyEnter,
		ebiten.KeyP,
	}
	for _, key := range edgeKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		g.prevKeys[key] = pressed
		if !pressed || wasPressed {
			continue
		}
		switch {
		case key == ebiten.KeyBackspace && !meta:
			runes := []rune(g.paletteState.Query)
			if len(runes) > 0 {
				g.paletteState.Query = string(runes[:len(runes)-1])
				g.paletteState.Cursor = 0
			}
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

	// Printable characters append to the query; reset cursor on any query change.
	if !meta {
		before := g.paletteState.Query
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= 0x20 && r != 0x7f {
				g.paletteState.Query += string(r)
			}
		}
		if g.paletteState.Query != before {
			g.paletteState.Cursor = 0
		}
	}
}

// handleOverlayInput processes keyboard input while the overlay is open.
// Printable characters update the search query; Backspace removes the last
// character; Escape clears the query or closes the overlay; Cmd+/ closes it.
func (g *Game) handleOverlayInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)

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

	edgeKeys := []ebiten.Key{
		ebiten.KeyBackspace,
		ebiten.KeySlash,
		ebiten.KeyArrowUp,
		ebiten.KeyArrowDown,
		ebiten.KeyPageUp,
		ebiten.KeyPageDown,
	}
	prevQuery := g.overlayState.SearchQuery
	for _, key := range edgeKeys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch {
			case key == ebiten.KeyBackspace && !meta:
				runes := []rune(g.overlayState.SearchQuery)
				if len(runes) > 0 {
					g.overlayState.SearchQuery = string(runes[:len(runes)-1])
				}
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

	// Printable character input goes to the search query.
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= 0x20 && r != 0x7f {
				g.overlayState.SearchQuery += string(r)
			}
		}
	}
}

// saveSession persists the current tab state to the session file.
// Called before every exit path. Errors are logged but do not block exit.
func (g *Game) saveSession() {
	if !g.cfg.Session.Enabled {
		return
	}
	data := &session.SessionData{
		Version:   1,
		ActiveTab: g.activeTab,
	}
	for _, t := range g.tabs {
		leaves := t.Layout.Leaves()
		if len(leaves) == 0 {
			continue
		}
		term := leaves[0].Pane.Term
		td := session.TabData{
			Cwd:         term.Cwd,
			Title:       t.Title,
			UserRenamed: t.UserRenamed,
		}
		if t.PinnedSlot != 0 {
			td.PinnedSlot = string(t.PinnedSlot)
		}
		data.Tabs = append(data.Tabs, td)
	}
	if err := session.Save(data, g.cfg); err != nil {
		log.Printf("zurm: session save: %v", err)
	}
}

// flashStatus shows msg in the status bar for 3 seconds.
func (g *Game) flashStatus(msg string) {
	g.statusBarState.FlashMessage = msg
	g.flashExpiry = time.Now().Add(3 * time.Second)
	g.screenDirty = true
}

// installShellHooks appends OSC 133 shell integration hooks to ~/.zshrc or
// ~/.bashrc, guarded by idempotency markers so repeated calls are safe.
func (g *Game) installShellHooks() {
	shell := g.cfg.Shell.Program
	if shell == "" {
		shell = os.Getenv("SHELL")
	}

	const markerStart = "# zurm-hooks-start"
	const markerEnd = "# zurm-hooks-end"

	var rcFile, hooks string
	switch {
	case strings.HasSuffix(shell, "zsh"):
		rcFile = filepath.Join(os.Getenv("HOME"), ".zshrc")
		// _zurm_cmd_started guards against emitting D before the first command.
		hooks = markerStart + `
_zurm_precmd() {
  local _code=$?
  [[ -n $_zurm_cmd_started ]] && printf '\033]133;D;%d\007' "$_code"
  unset _zurm_cmd_started
  printf '\033]133;A\007'
}
_zurm_preexec() { _zurm_cmd_started=1; printf '\033]133;C\007'; }
precmd_functions+=(_zurm_precmd)
preexec_functions+=(_zurm_preexec)
` + markerEnd + "\n"
	case strings.HasSuffix(shell, "bash"):
		rcFile = filepath.Join(os.Getenv("HOME"), ".bashrc")
		hooks = markerStart + `
PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }printf '\033]133;D;%s\007' \"$?\"; printf '\033]133;A\007'"
` + markerEnd + "\n"
	default:
		g.flashStatus("Auto-hooks not supported for: " + filepath.Base(shell))
		return
	}

	existing, err := os.ReadFile(rcFile) // #nosec G304 G703 — rcFile is ~/.zshrc derived from os.UserHomeDir()
	if err != nil && !os.IsNotExist(err) {
		g.flashStatus("Shell hooks: cannot read " + filepath.Base(rcFile))
		return
	}
	if strings.Contains(string(existing), markerStart) {
		g.flashStatus("Shell hooks already installed in " + filepath.Base(rcFile))
		return
	}

	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // #nosec G304 G302 G703 — path from UserHomeDir; 0644 is correct for shell RC files
	if err != nil {
		g.flashStatus("Shell hooks: cannot write " + filepath.Base(rcFile))
		return
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + hooks); err != nil {
		g.flashStatus("Shell hooks: write error")
		return
	}
	g.flashStatus("Shell hooks installed — restart your shell")
}
