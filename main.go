package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"io/fs"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // #nosec G108 — opt-in via config, localhost-only
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/recorder"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/session"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/terminal"
	"github.com/studiowebux/zurm/vault"
	"github.com/studiowebux/zurm/zserver"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Defaults to "dev" for local builds.
var version = "dev"

// Internal timing constants — not user-configurable.
const (
	unfocusSuspendDelay = 5 * time.Second       // idle before reducing TPS when unfocused
	bellDebounce        = 500 * time.Millisecond // min interval between bell sounds
	llmsFetchTimeout    = 10 * time.Second       // HTTP client timeout for llms.txt fetch
	statusMessageFrames = 60                     // status message display duration in frames (~1s at 60fps)
	paneHeaderPadding   = 4                      // extra pixels added to cellH for pane header bars

	// X10/SGR mouse protocol constants.
	mouseScrollUp      = 64  // button code for scroll up
	mouseScrollDown    = 65  // button code for scroll down
	mouseX10CoordMax   = 222 // max column/row encodable in X10 mode
	mouseX10Offset     = 32  // added to button/coordinate in X10 encoding
	mouseX10Release    = 35  // X10 button-release code (3 + offset)
)

// keyRepeatDelay and keyRepeatInterval are set from config at startup.
var (
	keyRepeatDelay    = 500 * time.Millisecond
	keyRepeatInterval = 50 * time.Millisecond
)

// allKeys is the set of keyboard keys polled each frame for terminal input.
var allKeys = []ebiten.Key{
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

// defaultHistoryPath returns the shell history file path based on the configured shell.
func defaultHistoryPath(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	base := filepath.Base(shell)
	switch {
	case strings.Contains(base, "bash"):
		return filepath.Join(home, ".bash_history")
	case strings.Contains(base, "fish"):
		return filepath.Join(home, ".local", "share", "fish", "fish_history")
	default: // zsh and others
		return filepath.Join(home, ".zsh_history")
	}
}

//go:embed assets/fonts/JetBrainsMono-Regular.ttf
var jetbrainsMono []byte

// Emoji fonts are not supported - see docs/emoji-limitations.md
// //go:embed assets/fonts/NotoColorEmoji.ttf
// var notoEmoji []byte

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

	// Idle suspension — reduce TPS when window is unfocused to save CPU/memory.
	unfocusedAt time.Time // when focus was lost; zero = focused
	suspended   bool      // true when TPS has been reduced

	// Key repeat state for special keys.
	repeatKey    ebiten.Key
	repeatSeq    []byte // exact bytes to resend on repeat; nil uses KeyEventToBytes
	repeatActive bool
	repeatStart  time.Time
	repeatLast   time.Time

	// Selection drag state.
	selDragging bool

	// Divider drag state (pane resize).
	dividerDragging bool
	dragSplit       *pane.LayoutNode // split node being resized

	// Tab drag state (mouse reorder).
	tabDragging    bool
	dragFromTabIdx int
	dragTabStartX  int

	// URL hover state — detected URLs in the focused pane's visible buffer.
	hoveredURL *terminal.URLMatch // URL under the cursor, nil if none
	urlMatches []terminal.URLMatch // cached URL matches for the focused pane

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
	searchState      renderer.SearchState
	lastSearchQuery  string                 // detects query change to avoid recomputing every frame
	searchResultCh   chan searchResult       // receives async SearchAll results
	searchGen        uint64                 // incremented each query; stale results discarded

	// Async clipboard — requestClipboard() spawns pbpaste in a goroutine,
	// result arrives on clipboardCh. Each handler's Cmd+V triggers a request;
	// the active handler consumes the result next frame via non-blocking receive.
	clipboardCh chan string

	// Status bar state.
	statusBarState renderer.StatusBarState
	poller *StatusPoller // async git status queries and poll intervals

	// Tab switcher overlay state (pin-style).
	tabSwitcherState renderer.TabSwitcherState

	// Tab search overlay state (Cmd+J).
	tabSearchState renderer.TabSearchState

	// Key repeat state for tab search navigation (arrow keys) and text input.
	tabSearchRepeatKey    ebiten.Key
	tabSearchRepeatActive bool
	tabSearchRepeatStart  time.Time
	tabSearchRepeatLast   time.Time
	tabSearchInputRepeat  TextInputRepeat // backspace/cursor in the search text field

	// Command palette state (Cmd+P).
	paletteState   renderer.PaletteState
	paletteEntries []renderer.PaletteEntry
	paletteActions []func()

	// pinMode is true after Cmd+Space, waiting for a home-row slot keypress.
	pinMode bool

	// focusHistory is a stack of (tab index, pane pointer) pairs for Cmd+` navigation.
	// The most recent entry is at the end. Max 50 entries.
	focusHistory []focusEntry

	// File explorer sidebar state (Cmd+E).
	fileExplorerState renderer.FileExplorerState

	// Key repeat state for file explorer navigation (arrow keys).
	explorerRepeatKey    ebiten.Key
	explorerRepeatActive bool
	explorerRepeatStart  time.Time
	explorerRepeatLast   time.Time

	// Key repeat state for file explorer text inputs (search / rename / new).
	explorerInputRepeat TextInputRepeat

	// Key repeat state for command palette navigation (arrow keys) and text input.
	paletteRepeatKey    ebiten.Key
	paletteRepeatActive bool
	paletteRepeatStart  time.Time
	paletteRepeatLast   time.Time
	paletteInputRepeat  TextInputRepeat // backspace/cursor in the query field

	// Key repeat state for text input overlays.
	renameRepeat     TextInputRepeat // tab rename
	noteRepeat       TextInputRepeat // tab note
	paneRenameRepeat TextInputRepeat // pane rename
	searchRepeat     TextInputRepeat // in-buffer search (Cmd+F)
	overlayRepeat    TextInputRepeat // help overlay search
	mdSearchRepeat   TextInputRepeat // markdown viewer search

	// Dirty-render state — screen is only redrawn when something changes.
	screenDirty  bool
	lastPtySeq   uint64
	lastClockSec int64


	// blocksEnabled is the runtime toggle for command block rendering.
	// Initialized from cfg.Blocks.Enabled; toggled via command palette.
	blocksEnabled bool

	// screenshotPending is set by Cmd+Shift+S; consumed by Draw() to capture a PNG.
	screenshotPending bool
	screenshotDone    chan string // receives flash message when background PNG encode completes

	// Screen recording state (FFMPEG pipe → MP4).
	recorder        *recorder.Recorder
	recDone         chan string // receives flash message when background Stop() completes
	recBuf          []byte     // reusable pixel buffer for frame capture (avoids per-frame alloc)
	recLastFrame    time.Time  // last frame capture time (throttle to 30fps)
	recLastStatusSec int64    // unix second of last status bar update (throttle os.Stat + blink)

	// Stats overlay state (Cmd+I).
	statsState     renderer.StatsState
	statsLastTick  time.Time // last stats collection time

	// Tab hover popover state (minimap preview on background tab hover).
	tabHoverState renderer.TabHoverState

	// flashExpiry is when statusBarState.FlashMessage should be cleared.
	flashExpiry time.Time

	// lastBellSound debounces system sound + dock badge to avoid spamming.
	lastBellSound time.Time

	// Markdown viewer overlay state (Cmd+Shift+M).
	mdViewerState  renderer.MarkdownViewerState
	mdViewerLastG  time.Time // timestamp of last 'g' press for gg detection

	// llms.txt URL input overlay state (Cmd+L).
	urlInputState    renderer.URLInputState
	llmsFetchCh      chan llmsFetchResult
	llmsShort        string // cached /llms.txt content
	llmsFull         string // cached /llms-full.txt content
	llmsDomain       string // domain of the last fetch
	llmsViewingFull  bool   // true when showing /llms-full.txt
	llmsHistory      []llmsHistoryEntry // back stack
	llmsForward      []llmsHistoryEntry // forward stack

	// Key repeat state for URL input text field.
	urlRepeat TextInputRepeat

	// Command vault — encrypted history with ghost text suggestions.
	vault          *vault.Vault
	vaultSuggest   string // current ghost text (completion tail)
	vaultLineCache string // last line used for suggestion (avoids recomputing every frame)
	vaultSkip      int    // Tab cycles through matches: 0=most recent, 1=next, etc.

}

func main() {
	noRestore := flag.Bool("no-restore", false, "skip session restore on launch")
	showVersion := flag.Bool("version", false, "print version and exit")
	listSessions := flag.Bool("list-sessions", false, "list active zurm-server sessions and exit")
	flag.BoolVar(listSessions, "ls", false, "list active zurm-server sessions and exit (shorthand)")
	attachID := flag.String("attach", "", "start zurm attached to the given server session ID")
	flag.StringVar(attachID, "a", "", "start zurm attached to the given server session ID (shorthand)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("zurm %s\n", version)
		return
	}

	if *listSessions {
		if err := runListSessions(); err != nil {
			fmt.Fprintf(os.Stderr, "zurm: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Resolve the initial directory from Apple Event (Finder "Open With") or CLI arg.
	openDir := drainOpenWithEvents()
	if openDir == "" {
		if args := flag.Args(); len(args) > 0 {
			openDir = args[0]
		}
	}

	resolveShellPath()

	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load warning: %v (using defaults)", err)
	}

	if cfg.Keyboard.RepeatDelayMs > 0 {
		keyRepeatDelay = time.Duration(cfg.Keyboard.RepeatDelayMs) * time.Millisecond
	}
	if cfg.Keyboard.RepeatIntervalMs > 0 {
		keyRepeatInterval = time.Duration(cfg.Keyboard.RepeatIntervalMs) * time.Millisecond
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
	fallbackSlice := loadFontFallbacks(cfg.Font)
	fontR, err := renderer.NewFontRenderer(fontBytes, cfg.Font.Size*dpi, fallbackSlice...)
	if err != nil {
		log.Fatalf("font load: %v", err)
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
	sess, loadErr := session.Load()
	if loadErr == nil && sess != nil && cfg.Session.Enabled && cfg.Session.RestoreOnLaunch && !*noRestore && len(sess.Tabs) > 0 {
		for _, td := range sess.Tabs {
			var t *tab.Tab
			var tErr error

			// Try to restore the saved layout if available
			if td.Layout != nil {
				t, tErr = restoreTabWithLayout(cfg, paneRect, fontR.CellW, fontR.CellH, td)
				if tErr != nil {
					log.Printf("session restore: failed to restore tab layout: %v", tErr)
				}
			}

			// Fall back to creating a single pane if layout restore failed
			if t == nil {
				// Sanitize the directory in case it's inside a .app bundle
				sanitizedDir := sanitizeDirectory(td.Cwd)
				t, tErr = tab.New(cfg, paneRect, fontR.CellW, fontR.CellH, sanitizedDir)
				if tErr != nil {
					log.Printf("session restore: tab new: %v", tErr)
					continue
				}
				if leaf := t.Layout.Leaves(); len(leaf) > 0 {
					// Pre-populate Cwd so saveSession() has a valid value before
					// lsof/OSC 7 fires. Without this, quitting quickly overwrites
					// the session with empty CWDs.
					leaf[0].Pane.Term.Cwd = td.Cwd
				}
			}

			t.Title = td.Title
			if t.Title == "" {
				t.Title = fmt.Sprintf("tab %d", len(initialTabs)+1)
			}
			t.UserRenamed = td.UserRenamed
			t.Note = td.Note
			if len(td.PinnedSlot) > 0 {
				t.PinnedSlot = []rune(td.PinnedSlot)[0]
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
	if *attachID != "" {
		// --attach: resolve prefix (like Docker short IDs), then open a single
		// server-backed tab for the matched session.
		addr := zserver.ResolveSocket(cfg.Server.Address)
		fullID, resolveErr := resolveSessionPrefix(addr, *attachID)
		if resolveErr != nil {
			log.Fatalf("attach: %v", resolveErr)
		}
		p, aErr := pane.NewServer(cfg, paneRect, fontR.CellW, fontR.CellH, "", fullID)
		if aErr != nil {
			log.Fatalf("attach: %v", aErr)
		}
		layout := pane.NewLeaf(p)
		layout.ComputeRects(paneRect, fontR.CellW, fontR.CellH, cfg.Window.Padding, cfg.Panes.DividerWidthPixels)
		for _, leaf := range layout.Leaves() {
			leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
		}
		attachTab := &tab.Tab{Layout: layout, Focused: p, Title: "tab 1"}
		initialTabs = []*tab.Tab{attachTab}
		initialActive = 0
	}

	if len(initialTabs) == 0 {
		// Use sanitized directory (handles .app bundles correctly)
		initialDir := getInitialDirectory(openDir)
		firstTab, tErr := tab.New(cfg, paneRect, fontR.CellW, fontR.CellH, initialDir)
		if tErr != nil {
			log.Fatalf("tab new: %v", tErr)
		}
		firstTab.Title = "tab 1"
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
		recorder:         recorder.New(winW, winH),
		recDone:          make(chan string, 1),
		screenshotDone:   make(chan string, 1),
		tabHoverState:    renderer.TabHoverState{TabIdx: -1},
		poller:           NewStatusPoller(),
		searchResultCh:   make(chan searchResult, 1),
		clipboardCh:      make(chan string, 1),
	}

	game.renderer.BlocksEnabled = game.blocksEnabled
	game.statusBarState.Version = version
	game.buildPalette()

	// Initialize command vault (encrypted local history + ghost suggestions).
	if cfg.Vault.Enabled {
		histPath := cfg.Vault.HistoryPath
		if histPath == "" {
			histPath = defaultHistoryPath(cfg.Shell.Program)
		}
		syncInterval := time.Duration(cfg.Vault.SyncIntervalSecs) * time.Second
		game.vault = vault.Init(config.ConfigDir(), histPath, cfg.Vault.IgnorePrefix, cfg.Vault.MaxEntries, syncInterval)
	}

	// Seed focus history with the initial tab so Cmd+; can return to it.
	game.focusHistory = []focusEntry{{tabIdx: initialActive, pane: initialTabs[initialActive].Focused}}
	initialTabs[initialActive].SnapshotGen()

	// Start pprof server for runtime memory profiling when enabled.
	if cfg.Performance.Pprof {
		addr := fmt.Sprintf("localhost:%d", cfg.Performance.PprofPort)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("pprof: cannot bind %s: %v (skipping)", addr, err)
		} else {
			log.Printf("pprof: http://%s/debug/pprof/", addr)
			go func() {
				if err := http.Serve(ln, nil); err != nil { // #nosec G114 — pprof is localhost-only, no timeout needed
					log.Printf("pprof server: %v", err)
				}
			}()
		}
	}

	ebiten.SetWindowSize(logW, logH)
	ebiten.SetWindowTitle("zurm")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetScreenClearedEveryFrame(false) // we manage redraws via dirty flag
	ebiten.SetTPS(cfg.Performance.TPS)
	ebiten.SetWindowClosingHandled(true) // intercept red X — handled in Update

	if err := ebiten.RunGame(game); err != nil && err != ebiten.Termination {
		log.Fatalf("ebiten: %v", err)
	}
	// Save session and vault after the game loop exits, regardless of how it was
	// terminated (Cmd+Q, red X button, last tab closed, or OS-level quit signal).
	game.saveSession()
	if game.vault != nil {
		if err := game.vault.Save(); err != nil {
			log.Printf("vault save: %v", err)
		}
	}
}

// Update is called at 60 TPS by Ebitengine.
func (g *Game) Update() error {
	if len(g.tabs) == 0 {
		g.saveSession()
		return ebiten.Termination
	}

	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	// Intercept window close button (red X) and Cmd+Q — show confirmation.
	wantQuit := (meta || ctrl) && ebiten.IsKeyPressed(ebiten.KeyQ)
	if (wantQuit || ebiten.IsWindowBeingClosed()) && !g.confirmState.Open {
		g.showConfirm("Quit zurm? All sessions will be closed.", func() {
			g.saveSession()
			for _, t := range g.tabs {
				for _, leaf := range t.Layout.Leaves() {
					leaf.Pane.Term.Close()
				}
			}
			os.Exit(0)
		})
	}

	// Clear transient flash message once its expiry has passed.
	if g.statusBarState.FlashMessage != "" && time.Now().After(g.flashExpiry) {
		g.statusBarState.FlashMessage = ""
		g.screenDirty = true
	}

	// Expire visual bell flashes — keep redrawing while any pane is flashing.
	now := time.Now()
	for _, leaf := range g.layout.Leaves() {
		if !leaf.Pane.BellUntil.IsZero() {
			if now.After(leaf.Pane.BellUntil) {
				leaf.Pane.BellUntil = time.Time{}
			}
			g.screenDirty = true
		}
	}

	// Drain recording-done channel (background goroutine sends flash message).
	select {
	case msg := <-g.recDone:
		g.flashStatus(msg)
	default:
	}

	// Drain screenshot-done channel (background PNG encode sends flash message).
	select {
	case msg := <-g.screenshotDone:
		g.flashStatus(msg)
	default:
	}

	// Update recording status bar fields once per second (blink + file size).
	if g.recorder.Active() {
		g.statusBarState.Recording = true
		g.statusBarState.RecordingMode = g.recorder.OutputMode()
		now := time.Now()
		if now.Unix() != g.recLastStatusSec {
			g.recLastStatusSec = now.Unix()
			g.statusBarState.RecordingDuration = now.Sub(g.recorder.StartTime())
			g.statusBarState.RecordingBytes = g.recorder.OutputSize()
			g.screenDirty = true
		}
	} else if g.statusBarState.Recording {
		g.statusBarState.Recording = false
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

	// Check background tabs for PTY activity (sets HasActivity indicator).
	for i, t := range g.tabs {
		if i != g.activeTab {
			had := t.HasActivity
			t.CheckActivity()
			if t.HasActivity != had {
				g.screenDirty = true
			}
		}
	}

	// Check for dead panes (non-blocking). Close at most one per frame to avoid
	// nil-dereffing g.layout or g.focused after a close mutates both.
	closedDead := false
	for _, leaf := range g.layout.Leaves() {
		if closedDead {
			break
		}
		select {
		case <-leaf.Pane.Term.Dead():
			if len(g.layout.Leaves()) <= 1 {
				g.closeActiveTab()
			} else {
				g.closePane(leaf.Pane)
			}
			closedDead = true // layout changed — remaining leaves are stale
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
	g.handleDroppedFiles()
	g.handleResize()
	g.handleFocus()
	g.drainTitle()
	g.drainCwd()
	g.drainBell()
	g.drainBlockDone()
	g.drainGitBranch()
	g.drainLLMSFetch()
	g.drainForeground()
	g.drainShellIntegration()
	g.pollStatusOnOutput()
	g.recomputeSearch()

	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.SendCPRResponse()
		leaf.Pane.Term.SendDA1Response()
		leaf.Pane.Term.SendDA2Response()
		leaf.Pane.Term.SendPendingResponses()
		leaf.Pane.Term.SyncCursorStyle()
	}
	if g.focused != nil {
		g.focused.Term.SendClipboardResponses()
	}

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
	// Stats overlay refreshes once per second when open.
	if g.statsState.Open && time.Since(g.statsLastTick) >= time.Second {
		g.collectStats()
		return true
	}
	// Tab hover popover: trigger redraw when delay has elapsed.
	if g.cfg.Tabs.Hover.Enabled && g.tabHoverState.TabIdx >= 0 && !g.tabHoverState.Active {
		delay := time.Duration(g.cfg.Tabs.Hover.DelayMs) * time.Millisecond
		if time.Since(g.tabHoverState.HoverStart) >= delay {
			return true
		}
	}
	return false
}

// Draw is called each frame by Ebitengine.
// SetScreenClearedEveryFrame(false) means the screen retains its content between frames,
// so we only need to draw when something actually changed.
func (g *Game) Draw(screen *ebiten.Image) {
	// Recording: capture frame for FFMPEG pipe when active.
	// Runs BEFORE needsRender() so that idle frames (no PTY output, no input)
	// still produce duplicate frames at 30fps. The screen retains its content
	// between frames (SetScreenClearedEveryFrame(false)), so ReadPixels returns
	// the last painted content even when nothing new was drawn.
	if g.recorder.Active() {
		now := time.Now()
		if now.Sub(g.recLastFrame) >= recorder.FrameDuration {
			g.recLastFrame = now
			needed := screen.Bounds().Dx() * screen.Bounds().Dy() * 4
			if len(g.recBuf) != needed {
				g.recBuf = make([]byte, needed)
			}
			screen.ReadPixels(g.recBuf)
			frame := make([]byte, needed)
			copy(frame, g.recBuf)
			g.recorder.AddFrame(frame)
		}
	}

	if !g.needsRender() {
		return
	}
	// Sync transient status bar fields from live game state before rendering.
	g.statusBarState.Zoomed = g.zoomed
	g.statusBarState.PinMode = g.pinMode
	g.statusBarState.BlocksEnabled = g.blocksEnabled
	g.statusBarState.ServerSession = g.focused != nil && g.focused.ServerSessionID != ""
	var srvCount int
	for _, t := range g.tabs {
		for _, leaf := range t.Layout.Leaves() {
			if leaf.Pane.ServerSessionID != "" {
				srvCount++
			}
		}
	}
	g.statusBarState.ServerSessionCount = srvCount
	if g.focused != nil {
		g.focused.Term.Buf.RLock()
		g.statusBarState.ScrollOffset = g.focused.Term.Buf.ViewOffset
		if g.blocksEnabled {
			g.statusBarState.BlockCount = len(g.focused.Term.Buf.Blocks)
		}
		g.focused.Term.Buf.RUnlock()
	}
	// Snapshot renderSeq BEFORE DrawAll so that any PTY output arriving
	// during rendering (e.g. shell responds to resize) bumps the seq above
	// our snapshot and triggers a redraw on the next frame.
	g.lastPtySeq = terminal.RenderSeq()

	g.renderer.HoveredURL = g.hoveredURL
	g.renderer.VaultSuggestion = g.vaultSuggest
	if g.activeTab >= 0 && g.activeTab < len(g.tabs) {
		g.statusBarState.TabNote = g.tabs[g.activeTab].Note
	}
	// Hint mode: show tab number badges when Cmd is held and no modal is active.
	hintMode := ebiten.IsKeyPressed(ebiten.KeyMeta) &&
		!g.overlayState.Open && !g.paletteState.Open && !g.confirmState.Open &&
		!g.mdViewerState.Open && !g.urlInputState.Open && !g.tabSwitcherState.Open &&
		!g.tabSearchState.Open && !g.searchState.Open &&
		!g.menuState.Open && !g.fileExplorerState.Open
	g.renderer.DrawAll(renderer.DrawState{
		Screen:         screen,
		Tabs:           g.tabs,
		ActiveTab:      g.activeTab,
		Focused:        g.focused,
		Zoomed:         g.zoomed,
		Menu:           &g.menuState,
		Overlay:        &g.overlayState,
		Confirm:        &g.confirmState,
		Search:         &g.searchState,
		StatusBar:      &g.statusBarState,
		TabSwitcher:    &g.tabSwitcherState,
		Palette:        &g.paletteState,
		PaletteEntries: g.paletteEntries,
		FileExplorer:   &g.fileExplorerState,
		TabSearch:      &g.tabSearchState,
		Stats:          &g.statsState,
		TabHover:       &g.tabHoverState,
		MdViewer:       &g.mdViewerState,
		URLInput:       &g.urlInputState,
		HintMode:       hintMode,
	})
	g.screenDirty = false
	g.lastClockSec = time.Now().Unix()

	// Screenshot capture: one-shot, triggered by Cmd+Shift+S.
	// ReadPixels must run on the main thread (GPU access); PNG encoding runs in background.
	if g.screenshotPending {
		g.screenshotPending = false
		bounds := screen.Bounds()
		raw := make([]byte, bounds.Dx()*bounds.Dy()*4)
		screen.ReadPixels(raw)
		go func() {
			var msg string
			path, err := recorder.SavePNG(raw, bounds)
			if err != nil {
				msg = "Screenshot failed: " + err.Error()
			} else {
				msg = "Screenshot: " + filepath.Base(path)
			}
			select {
			case g.screenshotDone <- msg:
			default:
			}
		}()
	}
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
	if g.activeTab >= 0 && g.activeTab < len(g.tabs) {
		g.tabs[g.activeTab].Layout = n
	}
}

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

	// When the tab search is open, route input to tab search handling.
	if g.tabSearchState.Open {
		g.screenDirty = true
		g.handleTabSearchInput()
		return
	}

	// pin mode: waiting for a home-row slot keypress after Cmd+Space.
	if g.pinMode {
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
				g.openSearch()

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
				if g.fileExplorerState.Open {
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
				g.pinMode = true
				g.screenDirty = true
			case meta && key == ebiten.KeyT:
				g.newTab()
			case meta && shift && key == ebiten.KeyB:
				// Cmd+Shift+B — new server-backed tab (Mode B); falls back to local PTY.
				g.newServerTab()
			case meta && shift && key == ebiten.KeyR:
				g.startRenameTab(g.activeTab)
			case meta && shift && key == ebiten.KeyN:
				g.startNoteEdit(g.activeTab)
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
		} else if !pressed && g.repeatActive && g.repeatKey == key {
			g.repeatActive = false
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
// On focus regain, resets input state so stale prevKeys/prevMouseButtons don't swallow
// the first events. Also manages idle suspension: after 5 seconds unfocused, TPS drops
// to 5 and terminal polling goroutines are paused to minimize CPU/allocation pressure.
// TPS=5 (not 1) ensures clicks and keystrokes that complete within the frame interval
// are not silently dropped — at 5fps the worst-case input latency is 200ms.
func (g *Game) handleFocus() {
	// NSWorkspaceDidWakeNotification: macOS fires this exactly once on system wake.
	// Trigger unsuspend and full repaint immediately, before any focus-state
	// transition, which is unreliable after sleep/wake on some macOS configurations.
	if consumeWake() {
		g.unsuspendAndRedraw()
	}

	focused := ebiten.IsFocused()

	// Idle suspension: reduce TPS after 5 seconds unfocused (when auto_idle is enabled).
	if g.cfg.Performance.AutoIdle && !focused && !g.suspended && !g.unfocusedAt.IsZero() &&
		time.Since(g.unfocusedAt) > unfocusSuspendDelay {
		ebiten.SetTPS(5)
		g.suspended = true
		for _, t := range g.tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(true)
			}
		}
	}

	if focused != g.prevFocused {
		if focused {
			// Unsuspend, zero unfocusedAt, and force full repaint.
			g.unsuspendAndRedraw()

			// Reset edge-detection state: only snapshot modifier keys so that
			// Cmd held from Cmd+Tab doesn't appear as a stale press. Non-modifier
			// keys start as "not pressed" so the first real keystroke or paste
			// after focus regain fires its leading edge correctly.
			for k := ebiten.Key(0); k <= ebiten.KeyMax; k++ {
				switch k {
				case ebiten.KeyMeta, ebiten.KeyMetaLeft, ebiten.KeyMetaRight,
					ebiten.KeyControl, ebiten.KeyControlLeft, ebiten.KeyControlRight,
					ebiten.KeyShift, ebiten.KeyShiftLeft, ebiten.KeyShiftRight,
					ebiten.KeyAlt, ebiten.KeyAltLeft, ebiten.KeyAltRight:
					g.prevKeys[k] = ebiten.IsKeyPressed(k)
				default:
					g.prevKeys[k] = false
				}
			}
			// Reset mouse button edge-detection state on focus gain, matching
			// prevKeys reset above. Stale prevMouseButtons[left]=true from the
			// last interaction before focus loss would cause the first click to
			// be silently skipped (pressed==was → no edge detected).
			for btn := range g.prevMouseButtons {
				g.prevMouseButtons[btn] = false
			}
			g.repeatActive = false
			g.scrollAccum = 0

			// Clear dock badge when window regains focus.
			clearDockBadge()
		} else {
			// Record when focus was lost.
			g.unfocusedAt = time.Now()
		}
		g.prevFocused = focused
		g.focused.Term.SendFocusEvent(focused)
	}

	// Emergency recovery for systems where IsFocused() doesn't reliably update
	// after sleep/wake (e.g. work machines with screen lock or MDM policies).
	// If still suspended but the user interacts (click or keystroke), unsuspend
	// immediately without waiting for a focus-state transition.
	if g.suspended && (ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) ||
		len(ebiten.AppendInputChars(nil)) > 0) {
		g.unsuspendAndRedraw()
	}
}

// unsuspendAndRedraw lifts idle suspension (if active) and forces a full repaint.
// Called from handleFocus (focus-gain, wake notification, and emergency recovery).
func (g *Game) unsuspendAndRedraw() {
	if g.suspended {
		ebiten.SetTPS(g.cfg.Performance.TPS)
		g.suspended = false
		for _, t := range g.tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(false)
			}
		}
	}
	g.unfocusedAt = time.Time{}
	g.screenDirty = true
	g.renderer.SetLayoutDirty()
	for _, t := range g.tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.Buf.Lock()
			leaf.Pane.Term.Buf.MarkAllDirty()
			leaf.Pane.Term.Buf.Unlock()
		}
	}
}

// physSize returns the physical pixel dimensions of the window.
func (g *Game) physSize() (int, int) {
	return int(float64(g.winW) * g.dpi), int(float64(g.winH) * g.dpi)
}

// contentRect returns the pane content area: full window minus tab bar and status bar.
func (g *Game) contentRect() image.Rectangle {
	physW, physH := g.physSize()
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	return image.Rect(0, tabBarH, physW, physH-statusBarH)
}

// handleDroppedFiles checks for files dropped onto the window and sends their
// paths to the focused PTY as space-separated, shell-escaped strings.
func (g *Game) handleDroppedFiles() {
	dropped := ebiten.DroppedFiles()
	if dropped == nil {
		return
	}
	entries, err := fs.ReadDir(dropped, ".")
	if err != nil || len(entries) == 0 {
		return
	}
	var paths []string
	for _, e := range entries {
		// Open the entry to get the real *os.File with the full path.
		// Ebitengine's VirtualFS wraps os.Open on the original absolute path.
		f, fErr := dropped.Open(e.Name())
		if fErr != nil {
			continue
		}
		if osFile, ok := f.(*os.File); ok {
			paths = append(paths, shellEscape(osFile.Name()))
		} else {
			paths = append(paths, shellEscape(e.Name()))
		}
		f.Close()
	}
	if len(paths) == 0 {
		return
	}
	text := strings.Join(paths, " ")
	g.focused.Term.SendBytes([]byte(text))
	g.screenDirty = true
}

// shellEscape wraps a path in single quotes for safe shell insertion.
// Interior single quotes are escaped as '\''.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// If the string has no special characters, return as-is.
	needsQuote := false
	for _, r := range s {
		if r == ' ' || r == '\'' || r == '"' || r == '\\' || r == '(' || r == ')' ||
			r == '&' || r == '|' || r == ';' || r == '$' || r == '`' || r == '!' ||
			r == '*' || r == '?' || r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '<' || r == '>' || r == '#' || r == '~' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (g *Game) handleResize() {
	w, h := ebiten.WindowSize()
	if w == g.winW && h == g.winH {
		return
	}
	g.winW = w
	g.winH = h
	physW, physH := g.physSize()
	g.renderer.SetSize(physW, physH)
	g.renderer.SetLayoutDirty()
	g.recorder.Resize(physW, physH)

	paneRect := g.contentRect()

	// Pause all PTY readers before resizing to avoid lock starvation.
	// Without this, heavy PTY output (e.g. Claude Code streaming) continuously
	// holds the buffer write lock, preventing Resize from acquiring it.
	for _, t := range g.tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.SetPaused(true)
		}
	}

	// Recompute rects for every tab's layout.
	for _, t := range g.tabs {
		setPaneHeaders(t.Layout, g.font.CellH)
		t.Layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
		}
	}

	// Resume PTY readers after all resizes are complete.
	// Skip if window is idle-suspended — readers should stay paused.
	if !g.suspended {
		for _, t := range g.tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(false)
			}
		}
	}

	// When zoomed, the focused pane must fill the entire pane area.
	// ComputeRects above set it to the normal split rect — override it.
	// Clear HeaderH — zoomed pane has no header (only one visible pane).
	if g.zoomed && g.focused != nil {
		g.focused.HeaderH = 0
		g.focused.Rect = paneRect
		cols := (paneRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW
		rows := (paneRect.Dy() - g.cfg.Window.Padding) / g.font.CellH
		if cols < 1 {
			cols = 1
		}
		if rows < 1 {
			rows = 1
		}
		g.focused.Cols = cols
		g.focused.Rows = rows
		g.focused.Term.Resize(cols, rows)
	}

	g.syncActive()
	g.screenDirty = true
}

func (g *Game) drainTitle() {
	if g.focused == nil || g.activeTab >= len(g.tabs) {
		return
	}
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


// searchResult delivers async SearchAll results to the game loop.
// drainCwd reads the latest CWD from the focused pane's OSC 7 channel.
// When the CWD changes it kicks off an async git status lookup via the poller.
func (g *Game) drainCwd() {
	if g.focused == nil {
		return
	}
	select {
	case cwd := <-g.focused.Term.CwdCh:
		if cwd != g.statusBarState.Cwd {
			g.statusBarState.Cwd = cwd
			g.focused.Term.Cwd = cwd
			g.statusBarState.GitBranch = ""
			g.statusBarState.GitCommit = ""
			g.statusBarState.GitDirty = 0
			g.statusBarState.GitStaged = 0
			g.statusBarState.GitAhead = 0
			g.statusBarState.GitBehind = 0
			if g.cfg.StatusBar.ShowGit {
				g.poller.StartGitQuery(cwd)
			}
			g.screenDirty = true
		}
	default:
	}
}

// drainBell reads BEL events from all panes and triggers visual/audio/dock feedback.
func (g *Game) drainBell() {
	dur := time.Duration(g.cfg.Bell.DurationMs) * time.Millisecond
	now := time.Now()
	fired := false

	// Active tab panes — visual border flash on bell.
	for _, leaf := range g.layout.Leaves() {
		select {
		case <-leaf.Pane.Term.BellCh:
			if g.cfg.Bell.Style != "none" {
				leaf.Pane.BellUntil = now.Add(dur)
			}
			fired = true
			g.screenDirty = true
		default:
		}
	}

	// Background tabs — mark tab activity on bell.
	for i, t := range g.tabs {
		if i == g.activeTab {
			continue
		}
		for _, leaf := range t.Layout.Leaves() {
			select {
			case <-leaf.Pane.Term.BellCh:
				t.HasActivity = true
				t.HasBell = true
				fired = true
				g.screenDirty = true
			default:
			}
		}
	}

	if !fired {
		return
	}

	// Debounce sound + dock notifications (500ms).
	if now.Sub(g.lastBellSound) < bellDebounce {
		return
	}
	g.lastBellSound = now

	if g.cfg.Bell.Sound {
		go playBellSound()
	}

	// Dock badge + bounce only when the window is not focused.
	if !ebiten.IsFocused() {
		setDockBadge()
		requestDockAttention()
	}
}

// drainBlockDone reads completed command block output from all panes.
// Background tab channels are drained silently to prevent buildup.
func (g *Game) drainBlockDone() {
	// Drain all active tab panes and capture completed commands for the vault.
	for _, leaf := range g.layout.Leaves() {
		select {
		case <-leaf.Pane.Term.Buf.BlockDoneCh:
			// Capture the command text from the completed block for the vault.
			if g.vault != nil {
				leaf.Pane.Term.Buf.RLock()
				if ab := leaf.Pane.Term.Buf.ActiveBlock(); ab == nil {
					// Active block is nil after D fires — check the most recent completed block.
					blocks := leaf.Pane.Term.Buf.Blocks
					if len(blocks) > 0 {
						cmd := strings.TrimSpace(blocks[len(blocks)-1].CommandText)
						if cmd != "" {
							g.vault.Add(cmd)
						}
					}
				}
				leaf.Pane.Term.Buf.RUnlock()
			}
		default:
		}
	}

	// Drain background tabs silently.
	for i, t := range g.tabs {
		if i == g.activeTab {
			continue
		}
		for _, leaf := range t.Layout.Leaves() {
			select {
			case <-leaf.Pane.Term.Buf.BlockDoneCh:
			default:
			}
		}
	}
}

// updateVaultSuggestion extracts the current line from the focused pane's buffer
// and queries the vault for a prefix-matched suggestion. The result is stored in
// g.vaultSuggest for the renderer to draw as ghost text.
func (g *Game) updateVaultSuggestion() {
	if g.vault == nil || !g.cfg.Vault.GhostText {
		g.vaultSuggest = ""
		return
	}

	buf := g.focused.Term.Buf
	buf.RLock()
	// No suggestions when scrolled back, in alt screen, or cursor is hidden.
	if buf.ViewOffset != 0 || buf.IsAltActive() || !buf.CursorVisible {
		buf.RUnlock()
		g.vaultSuggest = ""
		return
	}

	// Extract the text on the cursor row up to the cursor column.
	row := buf.CursorRow
	col := buf.CursorCol
	cells := buf.Cells
	if row < 0 || row >= len(cells) || col <= 0 {
		buf.RUnlock()
		g.vaultSuggest = ""
		return
	}

	var line strings.Builder
	for c := 0; c < col && c < len(cells[row]); c++ {
		cell := cells[row][c]
		if cell.Width == 0 {
			continue // skip continuation cells
		}
		ch := cell.Char
		if ch == 0 {
			ch = ' '
		}
		line.WriteRune(ch)
	}
	buf.RUnlock()

	lineStr := line.String()
	if lineStr == g.vaultLineCache {
		return // no change — keep current suggestion
	}
	g.vaultLineCache = lineStr
	g.vaultSkip = 0
	g.vaultSuggest = g.vault.Suggest(lineStr, g.vaultSkip)
}

// drainGitBranch reads a completed async git info result from the poller.
func (g *Game) drainGitBranch() {
	if info, ok := g.poller.DrainGit(); ok {
		g.statusBarState.GitBranch = info.Branch
		g.statusBarState.GitCommit = info.Commit
		g.statusBarState.GitDirty = info.Dirty
		g.statusBarState.GitStaged = info.Staged
		g.statusBarState.GitAhead = info.Ahead
		g.statusBarState.GitBehind = info.Behind
		g.screenDirty = true
	}
}

// drainForeground reads the latest foreground process name from all visible panes
// and updates ProcName on each. The focused pane's name also feeds the status bar.
func (g *Game) drainForeground() {
	if !g.cfg.StatusBar.ShowProcess {
		return
	}
	if g.activeTab >= len(g.tabs) {
		return
	}
	for _, leaf := range g.tabs[g.activeTab].Layout.Leaves() {
		p := leaf.Pane
		select {
		case name := <-p.Term.ForegroundProcCh:
			if name != p.ProcName {
				p.ProcName = name
				g.screenDirty = true
				if p == g.focused {
					g.statusBarState.ForegroundProc = name
				}
			}
		default:
		}
	}
}

// drainShellIntegration reads OSC 133 shell integration events from all visible
// panes and updates the foreground process name event-driven (no polling).
// A/D = shell is at prompt → clear proc name. C = command starting → query once.
func (g *Game) drainShellIntegration() {
	if !g.cfg.StatusBar.ShowProcess {
		return
	}
	if g.activeTab >= len(g.tabs) {
		return
	}
	for _, leaf := range g.tabs[g.activeTab].Layout.Leaves() {
		p := leaf.Pane
		select {
		case kind := <-p.Term.ShellIntCh:
			switch kind {
			case 'A', 'D':
				// Shell at prompt — clear foreground process.
				if p.ProcName != "" {
					p.ProcName = ""
					g.screenDirty = true
					if p == g.focused {
						g.statusBarState.ForegroundProc = ""
					}
				}
			case 'C':
				// Command about to execute — one-shot query for foreground name.
				go p.Term.QueryForeground()
			}
		default:
		}
	}
}

// pollStatusOnOutput triggers CWD and foreground process queries when PTY
// output arrives. Poll intervals are managed by the StatusPoller.
func (g *Game) pollStatusOnOutput() {
	seq := terminal.RenderSeq()

	if g.poller.ShouldPollCwd(seq) {
		if g.focused != nil {
			go g.focused.Term.QueryCWD()
		}
	}

	if g.cfg.StatusBar.ShowProcess && g.poller.ShouldPollFg(seq) && g.activeTab < len(g.tabs) {
		for _, leaf := range g.tabs[g.activeTab].Layout.Leaves() {
			if !leaf.Pane.Term.HasOSC133() {
				go leaf.Pane.Term.QueryForeground()
			}
		}
	}
}


// sanitizeTitle strips control characters and caps length (SEC-003).
func sanitizeTitle(s string) string {
	out := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
	if r := []rune(out); len(r) > 256 {
		out = string(r[:256])
	}
	return out
}

// requestClipboard spawns a background goroutine that reads the system clipboard
// via pbpaste and sends the result to clipboardCh. Non-blocking: if a previous
// request is still pending, the new result replaces it.
func (g *Game) requestClipboard() {
	go func() {
		out, err := exec.Command("pbpaste").Output() // #nosec G204 — fixed binary
		if err != nil || len(out) == 0 {
			return
		}
		clip := strings.ToValidUTF8(string(out), "")
		select {
		case g.clipboardCh <- clip:
		default:
		}
	}()
}

// handlePaste triggers an async clipboard read. The result is consumed by
// drainTerminalPaste on the next frame. Called from Cmd+V in the main input
// handler, context menu, and palette.
func (g *Game) handlePaste() {
	g.requestClipboard()
}

// drainTerminalPaste consumes a pending clipboard result and sends it to the
// focused PTY with NFC normalization, line-ending conversion, and bracketed
// paste wrapping. Called every frame from the main input path when no overlay
// is consuming the clipboard.
func (g *Game) drainTerminalPaste() bool {
	select {
	case clip := <-g.clipboardCh:
		if g.focused == nil {
			return false
		}
		out := norm.NFC.Bytes([]byte(clip))
		out = bytes.ReplaceAll(out, []byte("\r\n"), []byte("\r"))
		out = bytes.ReplaceAll(out, []byte("\n"), []byte("\r"))

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
		return true
	default:
		return false
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

	// URL hover detection — update when cursor moves over the focused pane.
	if mx != g.prevMX || my != g.prevMY {
		g.updateURLHover(mx, my, pad)
	}

	g.prevMX = mx
	g.prevMY = my

	// Tab hover popover tracking — update before processing clicks.
	g.updateTabHover(mx, my)

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
				// Extract only the user-typed command, excluding the prompt.
				// CmdCol (from OSC 133;B) gives the exact column where user input starts.
				// Fall back to StripPrompt pattern matching when B was not received.
				raw := h.Buf.TextRange(h.AbsStart, h.AbsStart)
				if h.CmdCol >= 0 {
					runes := []rune(raw)
					if h.CmdCol < len(runes) {
						copyText = strings.TrimSpace(string(runes[h.CmdCol:]))
					}
				}
				if copyText == "" {
					copyText = renderer.StripPrompt(raw)
				}
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
				if err := cmd.Run(); err != nil {
					log.Printf("pbcopy (block): %v", err)
				}
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

	// Tab search dismisses on any click.
	if g.tabSearchState.Open {
		if leftPressed && !leftWas {
			g.closeTabSearch()
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

	// Click in tab bar — switch tab, rename on double-click, or drag to reorder.
	// During selection drag, skip the tab bar handler so auto-scroll can run.
	// tabW must match the renderer's cap (24 * CellW) so click regions align with drawn tabs.
	// Dismiss tab hover popover on any click.
	if (leftPressed && !leftWas) || (rightPressed && !rightWas) {
		g.dismissTabHover()
	}
	if my < tabBarH && !g.selDragging {
		physW := int(float64(g.winW) * g.dpi)
		numTabs := len(g.tabs)
		maxTabW := g.cfg.Tabs.MaxWidthChars * g.font.CellW
		tabW := 0
		if numTabs > 0 {
			tabW = physW / numTabs
			if tabW > maxTabW {
				tabW = maxTabW
			}
		}

		// Continue tab drag.
		if g.tabDragging && leftPressed {
			if tabW > 0 {
				overIdx := mx / tabW
				if overIdx < 0 {
					overIdx = 0
				} else if overIdx >= numTabs {
					overIdx = numTabs - 1
				}
				if overIdx != g.activeTab {
					g.reorderTab(g.activeTab, overIdx)
					g.dragFromTabIdx = overIdx
				}
			}
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			g.screenDirty = true
			return
		}

		// End tab drag on release.
		if g.tabDragging && !leftPressed {
			g.tabDragging = false
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}

		if leftPressed && !leftWas {
			if numTabs > 0 && tabW > 0 {
				clicked := mx / tabW
				if clicked >= 0 && clicked < numTabs {
					now := time.Now()
					if clicked == g.activeTab && now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
						// Double-click on the active tab → rename.
						g.startRenameTab(clicked)
					} else {
						g.switchTab(clicked)
						// Record drag start position.
						g.dragFromTabIdx = clicked
						g.dragTabStartX = mx
					}
					g.lastClickTime = now
				}
			}
		} else if leftPressed && leftWas && !g.tabDragging {
			// Initiate drag after 8px threshold.
			dx := mx - g.dragTabStartX
			if dx < 0 {
				dx = -dx
			}
			if dx >= 8 {
				g.tabDragging = true
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

	// Divider drag — resize pane splits by dragging the divider.
	if g.dividerDragging {
		if leftPressed {
			// Continue drag: update ratio based on cursor position.
			switch g.dragSplit.Kind {
			case pane.HSplit:
				newRatio := float64(mx-g.dragSplit.Rect.Min.X) / float64(g.dragSplit.Rect.Dx())
				if newRatio < 0.1 {
					newRatio = 0.1
				} else if newRatio > 0.9 {
					newRatio = 0.9
				}
				g.dragSplit.Ratio = newRatio
			case pane.VSplit:
				newRatio := float64(my-g.dragSplit.Rect.Min.Y) / float64(g.dragSplit.Rect.Dy())
				if newRatio < 0.1 {
					newRatio = 0.1
				} else if newRatio > 0.9 {
					newRatio = 0.9
				}
				g.dragSplit.Ratio = newRatio
			}
			g.recomputeLayout()
			g.screenDirty = true
		} else {
			// Release: stop dragging.
			g.dividerDragging = false
			g.dragSplit = nil
		}
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	// Start divider drag on click — 4px hit margin around the 1px divider.
	if leftPressed && !leftWas && !g.zoomed {
		if split := g.layout.SplitAt(mx, my, 4); split != nil {
			g.dividerDragging = true
			g.dragSplit = split
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	// Click on an inactive pane always switches focus, regardless of PTY mouse mode.
	// When zoomed, only the focused pane is visible — skip pane-switch hit test.
	if leftPressed && !leftWas && !g.zoomed {
		if clicked := g.layout.PaneAt(mx, my); clicked != nil && clicked != g.focused {
			g.setFocus(clicked)
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
	}

	// Double-click on pane header area → rename pane (mirrors tab double-click).
	if leftPressed && !leftWas && g.focused.HeaderH > 0 &&
		my >= g.focused.Rect.Min.Y && my < g.focused.Rect.Min.Y+g.focused.HeaderH {
		now := time.Now()
		if now.Sub(g.lastClickTime) <= time.Duration(g.cfg.Input.DoubleClickMs)*time.Millisecond {
			g.startRenamePane()
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
		}
		g.lastClickTime = now
		g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
		g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
		return
	}

	if mouseMode == 0 {
		col := (mx - g.focused.Rect.Min.X - pad) / g.font.CellW
		row := (my - g.focused.Rect.Min.Y - pad - g.focused.HeaderH) / g.font.CellH

		g.focused.Term.Buf.RLock()
		maxRow := g.focused.Term.Buf.Rows - 1
		maxCol := g.focused.Term.Buf.Cols - 1
		g.focused.Term.Buf.RUnlock()

		// Save unclamped row for auto-scroll during selection drag.
		rawRow := row

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

		// Cmd+click opens the URL under the cursor in the default browser.
		if leftPressed && !leftWas && ebiten.IsKeyPressed(ebiten.KeyMeta) {
			if g.hoveredURL != nil {
				exec.Command("open", g.hoveredURL.Text).Start() // #nosec G204 — opens user-visible URL in default browser
				g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
				g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
				return
			}
		}

		// Shift+click extends the current selection to the clicked cell.
		if leftPressed && !leftWas && ebiten.IsKeyPressed(ebiten.KeyShift) {
			g.focused.Term.Buf.Lock()
			if g.focused.Term.Buf.Selection.Active {
				absRow := g.focused.Term.Buf.DisplayToAbsRow(row)
				snapCol := col
				if snapCol >= 0 && snapCol < g.focused.Term.Buf.Cols &&
					g.focused.Term.Buf.GetDisplayCell(row, snapCol).Width == 0 && snapCol > 0 {
					snapCol--
				}
				g.focused.Term.Buf.Selection.EndRow = absRow
				g.focused.Term.Buf.Selection.EndCol = snapCol
				g.focused.Term.Buf.BumpRenderGen()
			}
			g.focused.Term.Buf.Unlock()
			g.prevMouseButtons[ebiten.MouseButtonLeft] = leftPressed
			g.prevMouseButtons[ebiten.MouseButtonRight] = rightPressed
			return
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
			absRow := g.focused.Term.Buf.DisplayToAbsRow(row)
			// Snap col to parent cell if clicking on a wide char continuation.
			snapCol := col
			if snapCol >= 0 && snapCol < g.focused.Term.Buf.Cols && g.focused.Term.Buf.GetDisplayCell(row, snapCol).Width == 0 && snapCol > 0 {
				snapCol--
			}
			switch g.clickCount {
			case 1:
				g.selDragging = true
				g.focused.Term.Buf.Selection = terminal.Selection{
					Active:   true,
					StartRow: absRow, StartCol: snapCol,
					EndRow:   absRow, EndCol: snapCol,
				}
			case 2:
				g.selDragging = false
				g.focused.Term.Buf.Selection = g.wordSelection(row, col)
			default:
				g.selDragging = false
				g.focused.Term.Buf.Selection = terminal.Selection{
					Active:   true,
					StartRow: absRow, StartCol: 0,
					EndRow:   absRow, EndCol: g.focused.Term.Buf.Cols - 1,
				}
				g.clickCount = 0
			}
			g.focused.Term.Buf.BumpRenderGen()
			g.focused.Term.Buf.Unlock()
		} else if leftPressed && leftWas && g.selDragging {
			g.focused.Term.Buf.Lock()
			// Auto-scroll when dragging past the pane edges.
			// Selection uses absolute rows, so StartRow stays stable across
			// ViewOffset changes — no adjustment needed.
			if rawRow < 0 {
				vo := g.focused.Term.Buf.ViewOffset + 1
				maxVO := g.focused.Term.Buf.ScrollbackLen()
				if vo > maxVO {
					vo = maxVO
				}
				g.focused.Term.Buf.SetViewOffset(vo)
			} else if rawRow > maxRow {
				vo := g.focused.Term.Buf.ViewOffset - 1
				if vo < 0 {
					vo = 0
				}
				g.focused.Term.Buf.SetViewOffset(vo)
			}
			g.focused.Term.Buf.Selection.EndRow = g.focused.Term.Buf.DisplayToAbsRow(row)
			// Snap to parent cell if dragging onto a continuation cell.
			dragCol := col
			if dragCol >= 0 && dragCol < g.focused.Term.Buf.Cols && g.focused.Term.Buf.GetDisplayCell(row, dragCol).Width == 0 && dragCol > 0 {
				dragCol--
			}
			g.focused.Term.Buf.Selection.EndCol = dragCol
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
	row := (my-g.focused.Rect.Min.Y-pad-g.focused.HeaderH)/g.font.CellH + 1
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
		// Blocked in alt screen — TUI apps own the viewport.
		g.focused.Term.Buf.RLock()
		altShift := g.focused.Term.Buf.IsAltActive()
		g.focused.Term.Buf.RUnlock()
		if ebiten.IsKeyPressed(ebiten.KeyShift) && !altShift {
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
			btn := mouseScrollUp
			if wy < 0 {
				btn = mouseScrollDown
			}
			g.sendMouseEvent(btn, col, row, true, sgrMouse)
		}
	}
}

// wordSelection returns a Selection covering the word at (row, col).
// Scans across soft-wrap boundaries so that a word split across two or more
// display rows is selected in full.
// Must be called with Buf write lock held.
func (g *Game) wordSelection(row, col int) terminal.Selection {
	buf := g.focused.Term.Buf
	isWordChar := func(r rune) bool {
		return r != ' ' && r != 0 &&
			(unicode.IsLetter(r) || unicode.IsDigit(r) ||
				r == '_' || r == '.' || r == '/')
	}

	// Snap to parent cell if clicking on a continuation cell.
	cell := buf.GetDisplayCell(row, col)
	if cell.Width == 0 && col > 0 {
		col--
		cell = buf.GetDisplayCell(row, col)
	}

	absRow := buf.DisplayToAbsRow(row)
	if !isWordChar(cell.Char) {
		return terminal.Selection{Active: true, StartRow: absRow, StartCol: col, EndRow: absRow, EndCol: col}
	}

	startRow, startCol := row, col
scanBackward:
	for {
		for startCol > 0 {
			prev := buf.GetDisplayCell(startRow, startCol-1)
			if prev.Width == 0 {
				startCol--
				continue
			}
			if !isWordChar(prev.Char) {
				break scanBackward
			}
			startCol--
		}
		// Reached column 0. Cross soft-wrap boundary to the previous row.
		if startRow > 0 && buf.IsDisplayRowWrapped(startRow) {
			// Peek at the last usable cell of the previous row (skip trailing continuation).
			peekCol := buf.Cols - 1
			if buf.GetDisplayCell(startRow-1, peekCol).Width == 0 && peekCol > 0 {
				peekCol--
			}
			if !isWordChar(buf.GetDisplayCell(startRow-1, peekCol).Char) {
				break scanBackward
			}
			startRow--
			startCol = peekCol
			// Inner loop continues scanning leftward from peekCol.
		} else {
			break scanBackward
		}
	}

	endRow, endCol := row, col
scanForward:
	for {
		for endCol < buf.Cols-1 {
			next := buf.GetDisplayCell(endRow, endCol+1)
			if next.Width == 0 {
				endCol++
				continue
			}
			if !isWordChar(next.Char) {
				break scanForward
			}
			endCol++
		}
		// Reached last column. Cross soft-wrap boundary to the next row.
		if endRow+1 < buf.Rows && buf.IsDisplayRowWrapped(endRow+1) {
			// Peek at the first usable cell of the next row (skip leading continuation).
			peekCol := 0
			if buf.GetDisplayCell(endRow+1, peekCol).Width == 0 && buf.Cols > 1 {
				peekCol++
			}
			if !isWordChar(buf.GetDisplayCell(endRow+1, peekCol).Char) {
				break scanForward
			}
			endRow++
			endCol = peekCol
			// Inner loop continues scanning rightward from peekCol.
		} else {
			break scanForward
		}
	}

	return terminal.Selection{
		Active:   true,
		StartRow: buf.DisplayToAbsRow(startRow), StartCol: startCol,
		EndRow:   buf.DisplayToAbsRow(endRow), EndCol: endCol,
	}
}

// copySelection copies the current selection text to the clipboard via pbcopy.
func (g *Game) copySelection() {
	result := g.extractSelectedText()
	if result == "" {
		return
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(result)
	if err := cmd.Run(); err != nil {
		log.Printf("pbcopy (selection): %v", err)
	}
}

// sendMouseEvent encodes and sends a single mouse event to the focused PTY.
func (g *Game) sendMouseEvent(btn, col, row int, press bool, sgr bool) {
	if sgr {
		final := 'M'
		if !press && btn < mouseScrollUp {
			final = 'm'
		}
		seq := fmt.Sprintf("\x1B[<%d;%d;%d%c", btn, col, row, final)
		g.focused.Term.SendBytes([]byte(seq))
	} else {
		if col > mouseX10CoordMax || row > mouseX10CoordMax {
			return
		}
		b := byte(btn + mouseX10Offset) // #nosec G115 — btn is 0-4; col/row guarded above; all fit byte
		if !press {
			b = mouseX10Release
		}
		g.focused.Term.SendBytes([]byte{0x1B, '[', 'M', b, byte(col + mouseX10Offset), byte(row + mouseX10Offset)}) // #nosec G115
	}
}

// sendMouseMotion encodes and sends a motion event to the focused PTY.
// btn is the motion button code (held button + 32, or 35 for no-button).
func (g *Game) sendMouseMotion(btn, col, row int, sgr bool) {
	if sgr {
		seq := fmt.Sprintf("\x1B[<%d;%d;%dM", btn, col, row)
		g.focused.Term.SendBytes([]byte(seq))
	} else {
		if col > mouseX10CoordMax || row > mouseX10CoordMax {
			return
		}
		g.focused.Term.SendBytes([]byte{0x1B, '[', 'M', byte(btn), byte(col + mouseX10Offset), byte(row + mouseX10Offset)}) // #nosec G115 — col/row guarded above
	}
}

// --- Tab management ---

// openTabContextMenu shows a small tab-specific context menu (Rename, Close).
// Actions target the tab under the cursor, not necessarily the active tab.












// restoreTabWithLayout creates a new tab with the saved pane layout restored.
func restoreTabWithLayout(cfg *config.Config, rect image.Rectangle, cellW, cellH int, td session.TabData) (*tab.Tab, error) {
	// Deserialize the layout tree
	layout, err := deserializePaneLayout(cfg, rect, cellW, cellH, td.Layout)
	if err != nil {
		return nil, err
	}

	// Create the tab with the restored layout
	t := &tab.Tab{
		Layout:      layout,
		Title:       td.Title,
		UserRenamed: td.UserRenamed,
		Note:        td.Note,
	}

	// Find the first pane to set as focused (leftmost leaf)
	leaves := layout.Leaves()
	if len(leaves) > 0 {
		t.Focused = leaves[0].Pane
	}

	// Recompute all pane rects and sync terminal/PTY dimensions.
	setPaneHeaders(layout, cellH)
	layout.ComputeRects(rect, cellW, cellH, cfg.Window.Padding, cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}

	return t, nil
}



// resolveShellPath augments the process PATH with the user's login shell PATH
// and ensures UTF-8 locale is set for subprocesses.
// macOS .app bundles receive a minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin) that
// excludes Homebrew and other user-installed tool directories. This function
// spawns a login shell to resolve the full PATH and merges it into the process
// environment so exec.Command("ffmpeg") and similar calls find user-installed
// binaries. No-op if the shell probe fails.
//
// It also sets LANG=en_US.UTF-8 when LANG is unset or non-UTF-8. Without this,
// pbcopy/pbpaste interpret stdin/stdout as Mac Roman in .app bundles, causing
// multi-byte UTF-8 characters (em dash, CJK, etc.) to paste as mojibake.
func resolveShellPath() {
	// Ensure UTF-8 locale for subprocesses (pbcopy, pbpaste, ffmpeg, etc.).
	lang := os.Getenv("LANG")
	if lang == "" || !strings.Contains(strings.ToUpper(lang), "UTF-8") {
		os.Setenv("LANG", "en_US.UTF-8")
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	out, err := exec.Command(shell, "-lc", "echo $PATH").Output() // #nosec G204 G702 — shell from SHELL env var; -lc is fixed
	if err != nil {
		return
	}
	shellPath := strings.TrimSpace(string(out))
	if shellPath == "" {
		return
	}
	currentPath := os.Getenv("PATH")
	// Build a set of directories already in PATH to avoid duplicates.
	existing := make(map[string]bool)
	for _, d := range strings.Split(currentPath, ":") {
		existing[d] = true
	}
	var added []string
	for _, d := range strings.Split(shellPath, ":") {
		if d != "" && !existing[d] {
			added = append(added, d)
			existing[d] = true
		}
	}
	if len(added) > 0 {
		os.Setenv("PATH", currentPath+":"+strings.Join(added, ":"))
	}
}

// loadFontFallbacks reads all configured fallback font files from a FontConfig.
// It merges the legacy single Fallback field with the Fallbacks list,
// returning a slice of raw font bytes suitable for NewFontRenderer.
func loadFontFallbacks(fc config.FontConfig) [][]byte {
	var paths []string
	if fc.Fallback != "" {
		paths = append(paths, fc.Fallback)
	}
	paths = append(paths, fc.Fallbacks...)
	seen := make(map[string]bool, len(paths))
	var result [][]byte
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		data, err := os.ReadFile(p) // #nosec G304 -- desktop app loading user-configured font paths
		if err != nil {
			log.Printf("fallback font %q not found, skipping: %v", p, err)
			continue
		}
		result = append(result, data)
	}
	return result
}






