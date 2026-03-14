package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // #nosec G108 — opt-in via config, localhost-only
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/fileexplorer"
	"github.com/studiowebux/zurm/help"
	"github.com/studiowebux/zurm/markdown"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/recorder"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/session"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/terminal"
	"github.com/studiowebux/zurm/vault"
	"github.com/studiowebux/zurm/voice"
	"github.com/studiowebux/zurm/zserver"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Defaults to "dev" for local builds.
var version = "dev"

// keyRepeatDelay and keyRepeatInterval are set from config at startup.
var (
	keyRepeatDelay    = 500 * time.Millisecond
	keyRepeatInterval = 50 * time.Millisecond
)

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
	searchState     renderer.SearchState
	lastSearchQuery string // detects query change to avoid recomputing every frame

	// Status bar state.
	statusBarState renderer.StatusBarState
	gitInfoCh     chan gitInfoResult  // persistent channel — receives async git status results
	gitInfoGen    uint64             // incremented each query; stale results are discarded
	gitInfoCancel context.CancelFunc // cancels the previous git info goroutine

	// Tab switcher overlay state (pin-style).
	tabSwitcherState renderer.TabSwitcherState

	// Tab search overlay state (Cmd+J).
	tabSearchState renderer.TabSearchState

	// Key repeat state for tab search navigation (arrow keys).
	tabSearchRepeatKey    ebiten.Key
	tabSearchRepeatActive bool
	tabSearchRepeatStart  time.Time
	tabSearchRepeatLast   time.Time

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

	// Key repeat state for command palette navigation (arrow keys).
	paletteRepeatKey    ebiten.Key
	paletteRepeatActive bool
	paletteRepeatStart  time.Time
	paletteRepeatLast   time.Time

	// Key repeat state for tab rename text input (backspace).
	renameRepeatKey    ebiten.Key
	renameRepeatActive bool
	renameRepeatStart  time.Time
	renameRepeatLast   time.Time
	renameRepeatAlt    bool // Remember if alt was pressed when repeat started

	// Dirty-render state — screen is only redrawn when something changes.
	screenDirty  bool
	lastPtySeq   uint64
	lastClockSec int64

	// Event-driven status bar polling — replaces fixed-interval tickers.
	// Polls only fire when PTY output arrives and enough time has passed.
	lastPollSeq  uint64
	lastCwdPoll  time.Time
	lastFgPoll   time.Time

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

	// Text-to-speech via macOS AVSpeechSynthesizer.
	speaker voice.Speaker

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

	// Key repeat state for URL input backspace.
	urlRepeatKey    ebiten.Key
	urlRepeatActive bool
	urlRepeatStart  time.Time
	urlRepeatLast   time.Time
	urlRepeatAlt    bool

	// Command vault — encrypted history with ghost text suggestions.
	vault          *vault.Vault
	vaultSuggest   string // current ghost text (completion tail)
	vaultLineCache string // last line used for suggestion (avoids recomputing every frame)
	vaultSkip      int    // Tab cycles through matches: 0=most recent, 1=next, etc.

	// Speech-to-text via macOS SFSpeechRecognizer.
	listener              voice.Listener
	dictationState        renderer.DictationState
	dictationLastChange   time.Time // last time transcript text changed
	dictationLastText     string    // previous transcript for change detection
	dictationEnterAt      time.Time // when to send deferred Enter after text injection
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
	if sess, loadErr := session.Load(cfg); loadErr == nil && sess != nil && !*noRestore && len(sess.Tabs) > 0 {
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
		gitInfoCh:        make(chan gitInfoResult, 1),
	}

	game.renderer.BlocksEnabled = game.blocksEnabled
	game.statusBarState.Version = version
	game.speaker.Init()
	game.listener.SetLocale(cfg.Voice.Locale)
	game.listener.InitListener()
	game.buildPalette()

	// Initialize command vault (encrypted local history + ghost suggestions).
	if cfg.Vault.Enabled {
		histPath := cfg.Vault.HistoryPath
		if histPath == "" {
			if home, err := os.UserHomeDir(); err == nil {
				histPath = filepath.Join(home, ".zsh_history")
			}
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
	// Keep the flash alive while STT is listening (persistent indicator).
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

	// Poll STT transcript and update dictation overlay.
	if sttText, sttFinal, sttNew := g.listener.GetTranscript(); sttNew && sttText != "" {
		if g.dictationState.Open {
			g.dictationState.Transcript = sttText
			if sttFinal {
				g.injectDictation(sttText)
			} else {
				g.dictationState.Status = "Listening…"
				if sttText != g.dictationLastText {
					g.dictationLastText = sttText
					g.dictationLastChange = now
				}
			}
		}
		g.screenDirty = true
	}
	// Silence timeout: auto-inject after 2 s of unchanged transcript.
	if g.dictationState.Open && g.dictationState.Transcript != "" &&
		!g.dictationLastChange.IsZero() && now.Sub(g.dictationLastChange) >= 2*time.Second {
		g.injectDictation(g.dictationState.Transcript)
		g.screenDirty = true
	}
	// Deferred Enter after dictation text injection (debounce).
	if !g.dictationEnterAt.IsZero() && now.After(g.dictationEnterAt) {
		if g.focused != nil {
			g.focused.Term.SendBytes([]byte("\r"))
		}
		g.dictationEnterAt = time.Time{}
	}
	listening := g.listener.IsListening()
	g.statusBarState.Listening = listening
	if listening && now.Unix() != g.recLastStatusSec {
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
	g.pollStatusOnOutput()
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
		if now.Sub(g.recLastFrame) >= 33*time.Millisecond {
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
		!g.tabSearchState.Open && !g.searchState.Open && !g.dictationState.Open &&
		!g.menuState.Open && !g.fileExplorerState.Open
	g.renderer.DrawAll(screen, g.tabs, g.activeTab, g.focused, g.zoomed,
		&g.menuState, &g.overlayState, &g.confirmState, &g.searchState, &g.statusBarState, &g.tabSwitcherState,
		&g.paletteState, g.paletteEntries, &g.fileExplorerState, &g.tabSearchState, &g.statsState, &g.tabHoverState,
		&g.dictationState, &g.mdViewerState, &g.urlInputState, hintMode)
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
	g.tabs[g.activeTab].Layout = n
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

	// When the dictation overlay is open, route input to dictation handling.
	if g.dictationState.Open {
		g.screenDirty = true
		g.handleDictationInput()
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
	// Block scrollback when alternate screen is active — TUI apps (Claude Code,
	// nvim, htop) own the full viewport and scrollback makes no sense.
	g.focused.Term.Buf.RLock()
	altActive := g.focused.Term.Buf.IsAltActive()
	g.focused.Term.Buf.RUnlock()

	scrolled := false
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
				scrolled = true
			case key == ebiten.KeyPageDown:
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ScrollViewDown(halfPage)
				g.focused.Term.Buf.Unlock()
				scrolled = true
			case (meta || ctrl) && key == ebiten.KeyK:
				g.focused.Term.Buf.Lock()
				g.focused.Term.Buf.ClearScrollback()
				g.focused.Term.Buf.ClearSelection()
				g.focused.Term.Buf.Unlock()
				scrolled = true
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
				scrolled = true
			}
		}
	}

	if scrolled {
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
			// Stop active TTS on any keypress.
			if g.speaker.Active() {
				g.speaker.Stop()
			}

			switch {
			case meta && key == ebiten.KeyC:
				g.copySelection()

			case meta && !shift && key == ebiten.KeyV:
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
			case meta && shift && key == ebiten.KeySpace:
				// Cmd+Shift+Space — start dictation (STT).
				g.startDictation()

			case meta && shift && key == ebiten.KeyU:
				// Cmd+Shift+U — read selection aloud (TTS).
				g.speakSelection()

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
	focused := ebiten.IsFocused()

	// Idle suspension: reduce TPS after 5 seconds unfocused (when auto_idle is enabled).
	if g.cfg.Performance.AutoIdle && !focused && !g.suspended && !g.unfocusedAt.IsZero() &&
		time.Since(g.unfocusedAt) > 5*time.Second {
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
			// Wake from suspension.
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

			// Force full redraw on focus regain. After macOS sleep/wake the
			// process resumes but needsRender() returns false because no PTY
			// output arrived yet — the screen appears frozen without this.
			g.screenDirty = true
			g.renderer.SetLayoutDirty()
			for _, t := range g.tabs {
				for _, leaf := range t.Layout.Leaves() {
					leaf.Pane.Term.Buf.Lock()
					leaf.Pane.Term.Buf.MarkAllDirty()
					leaf.Pane.Term.Buf.Unlock()
				}
			}
		} else {
			// Record when focus was lost.
			g.unfocusedAt = time.Now()
		}
		g.prevFocused = focused
		g.focused.Term.SendFocusEvent(focused)
	}

	// Emergency recovery for systems where IsFocused() doesn't reliably update
	// after sleep/wake (e.g. work machines with screen lock or MDM policies).
	// If still suspended but the user is clicking, unsuspend immediately without
	// waiting for a focus-state transition. The click was already dispatched to
	// the PTY by handleMouse(); this just lifts the paused flag so the PTY
	// reader can deliver the shell response.
	if g.suspended && (ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight)) {
		ebiten.SetTPS(g.cfg.Performance.TPS)
		g.suspended = false
		g.unfocusedAt = time.Time{}
		for _, t := range g.tabs {
			for _, leaf := range t.Layout.Leaves() {
				leaf.Pane.Term.SetPaused(false)
			}
		}
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
	physW := int(float64(w) * g.dpi)
	physH := int(float64(h) * g.dpi)
	g.renderer.SetSize(physW, physH)
	g.renderer.SetLayoutDirty()
	g.recorder.Resize(physW, physH)

	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

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

// gitInfo holds all git status data gathered asynchronously.
type gitInfo struct {
	Branch  string
	Commit  string
	Dirty   int
	Staged  int
	Ahead   int
	Behind  int
}

// gitInfoResult pairs a query generation counter with the git status result.
// drainGitBranch discards results whose gen no longer matches g.gitInfoGen,
// preventing stale goroutines from overwriting a newer query's output.
type gitInfoResult struct {
	gen  uint64
	info gitInfo
}

// llmsFetchResult is the result of an async llms.txt HTTP fetch.
// Both files are fetched in parallel; either may be empty if unavailable.
type llmsFetchResult struct {
	Short  string // /llms.txt content (may be empty)
	Full   string // /llms-full.txt content (may be empty)
	Domain string
	Err    error
}

// llmsHistoryEntry captures one visited page for back/forward navigation.
type llmsHistoryEntry struct {
	Domain       string
	Short        string
	Full         string
	ViewingFull  bool
	ScrollOffset int
}

// drainCwd reads the latest CWD from the focused pane's OSC 7 channel.
// When the CWD changes it kicks off an async git status lookup.
func (g *Game) drainCwd() {
	select {
	case cwd := <-g.focused.Term.CwdCh:
		if cwd != g.statusBarState.Cwd {
			g.statusBarState.Cwd = cwd
			g.focused.Term.Cwd = cwd
			g.statusBarState.GitBranch = "" // clear until new result arrives
			g.statusBarState.GitCommit = ""
			g.statusBarState.GitDirty = 0
			g.statusBarState.GitStaged = 0
			g.statusBarState.GitAhead = 0
			g.statusBarState.GitBehind = 0
			if g.cfg.StatusBar.ShowGit {
				// Cancel any in-flight git query and drain its stale result.
				if g.gitInfoCancel != nil {
					g.gitInfoCancel()
				}
				select {
				case <-g.gitInfoCh:
				default:
				}
				g.gitInfoGen++
				gen := g.gitInfoGen
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				g.gitInfoCancel = cancel
				ch := g.gitInfoCh // persistent channel, never replaced
				go func() {
					defer cancel()
					info := gitInfo{}

					// Branch name.
					if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil { // #nosec G204
						info.Branch = strings.TrimSpace(string(out))
					} else {
						select {
						case ch <- gitInfoResult{gen: gen, info: info}:
						default:
						}
						return
					}

					// Short commit hash.
					if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--short", "HEAD").Output(); err == nil { // #nosec G204
						info.Commit = strings.TrimSpace(string(out))
					}

					// Dirty and staged counts from porcelain status.
					if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain").Output(); err == nil { // #nosec G204
						for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
							if len(line) < 2 {
								continue
							}
							idx := line[0]
							wt := line[1]
							if idx != ' ' && idx != '?' {
								info.Staged++
							}
							if wt != ' ' && wt != '?' {
								info.Dirty++
							}
						}
					}

					// Ahead/behind upstream.
					if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-list", "--left-right", "--count", "HEAD...@{upstream}").Output(); err == nil { // #nosec G204
						parts := strings.Fields(strings.TrimSpace(string(out)))
						if len(parts) == 2 {
							fmt.Sscanf(parts[0], "%d", &info.Ahead)
							fmt.Sscanf(parts[1], "%d", &info.Behind)
						}
					}

					select {
					case ch <- gitInfoResult{gen: gen, info: info}:
					default:
					}
				}()
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

	// Active tab panes — visual border flash + TTS on bell.
	for _, leaf := range g.layout.Leaves() {
		select {
		case <-leaf.Pane.Term.BellCh:
			if g.cfg.Bell.Style != "none" {
				leaf.Pane.BellUntil = now.Add(dur)
			}
			// TTS: speak recent output on bell (e.g., Claude Code waiting for input).
			if g.cfg.Voice.Enabled && leaf.Pane == g.focused {
				text := g.getRecentBufferText(g.cfg.Voice.ReadLines)
				text = strings.TrimSpace(text)
				if text != "" {
					g.speaker.Speak(text, g.cfg.Voice.VoiceID, g.cfg.Voice.Rate, g.cfg.Voice.Pitch, g.cfg.Voice.Volume)
				}
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
	if now.Sub(g.lastBellSound) < 500*time.Millisecond {
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
// When TTS is enabled, speaks output from the focused pane aloud.
// Background tab channels are drained silently to prevent buildup.
func (g *Game) drainBlockDone() {
	// Drain all active tab panes — speak focused pane output if TTS enabled,
	// and capture completed commands for the vault.
	for _, leaf := range g.layout.Leaves() {
		select {
		case text := <-leaf.Pane.Term.Buf.BlockDoneCh:
			if g.cfg.Voice.Enabled && leaf.Pane == g.focused {
				trimmed := strings.TrimSpace(text)
				if trimmed != "" {
					g.speaker.Speak(trimmed, g.cfg.Voice.VoiceID, g.cfg.Voice.Rate, g.cfg.Voice.Pitch, g.cfg.Voice.Volume)
				}
			}
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

// drainGitBranch reads a completed async git info result when available.
// Results from cancelled goroutines are discarded via generation check.
func (g *Game) drainGitBranch() {
	select {
	case res := <-g.gitInfoCh:
		if res.gen != g.gitInfoGen {
			return // stale result from a superseded query — discard
		}
		g.statusBarState.GitBranch = res.info.Branch
		g.statusBarState.GitCommit = res.info.Commit
		g.statusBarState.GitDirty = res.info.Dirty
		g.statusBarState.GitStaged = res.info.Staged
		g.statusBarState.GitAhead = res.info.Ahead
		g.statusBarState.GitBehind = res.info.Behind
		g.screenDirty = true
	default:
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

// pollStatusOnOutput triggers CWD and foreground process queries when PTY
// output arrives, replacing the old fixed-interval ticker goroutines.
// CWD polls at most every 2s, foreground at most every 1s.
func (g *Game) pollStatusOnOutput() {
	seq := terminal.RenderSeq()
	if seq == g.lastPollSeq {
		return
	}
	g.lastPollSeq = seq
	now := time.Now()

	if now.Sub(g.lastCwdPoll) >= 2*time.Second {
		g.lastCwdPoll = now
		if g.focused != nil {
			go g.focused.Term.QueryCWD()
		}
	}

	if g.cfg.StatusBar.ShowProcess && now.Sub(g.lastFgPoll) >= 1*time.Second {
		g.lastFgPoll = now
		for _, leaf := range g.tabs[g.activeTab].Layout.Leaves() {
			go leaf.Pane.Term.QueryForeground()
		}
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
				ti := &TextInput{Text: g.searchState.Query}
				if ebiten.IsKeyPressed(ebiten.KeyAlt) {
					ti.DeleteWord()
				} else {
					ti.DeleteLastChar()
				}
				g.searchState.Query = ti.Text
				g.screenDirty = true
			}
		}
		g.prevKeys[key] = pressed
	}

	// Cmd+V — paste clipboard into search query.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		if out, err := exec.Command("pbpaste").Output(); err == nil {
			line := strings.TrimSpace(strings.SplitN(strings.ToValidUTF8(string(out), ""), "\n", 2)[0])
			if line != "" {
				g.searchState.Query += line
				g.screenDirty = true
			}
		}
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
			st.FilteredIndices = nil
		} else {
			// Normal close
			g.closeFileExplorer()
		}
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
		ebiten.KeyO, ebiten.KeySlash,
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
			if st.Cursor < len(st.Entries) {
				e := st.Entries[st.Cursor]
				// Handle special entries
				if e.Name == "." {
					// Current directory - insert path
					g.focused.Term.SendBytes([]byte(e.Path))
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
			// Handle search results differently
			if len(st.SearchResults) > 0 {
				if st.Cursor >= 0 && st.Cursor < len(st.SearchResults) {
					selected := st.SearchResults[st.Cursor]

					// Special handling for . and ..
					if selected.Name == "." {
						// Insert current directory path
						g.focused.Term.SendBytes([]byte(selected.Path))
						g.closeFileExplorer()
						return
					} else if selected.Name == ".." {
						// Navigate to parent directory
						st.Root = selected.Path
						entries, err := fileexplorer.BuildTree(selected.Path)
						if err == nil {
							st.Entries = entries
							st.SearchResults = nil
							st.SearchQuery = ""
							st.Cursor = 0
							st.ScrollOffset = 0
						}
					} else if selected.IsDir {
						// Navigate into the directory
						st.Root = selected.Path
						entries, err := fileexplorer.BuildTree(selected.Path)
						if err == nil {
							st.Entries = entries
							st.SearchResults = nil
							st.SearchQuery = ""
							st.Cursor = 0
							st.ScrollOffset = 0
						}
					} else {
						// File - insert path
						g.focused.Term.SendBytes([]byte(selected.Path))
						g.closeFileExplorer()
						return
					}
				}
				break
			}

			// Normal mode (not searching)
			if st.Cursor >= len(st.Entries) {
				break
			}
			e := st.Entries[st.Cursor]

			// Handle special entries
			if e.Name == "." {
				// Insert current directory path
				g.focused.Term.SendBytes([]byte(e.Path))
				g.closeFileExplorer()
				return
			} else if e.Name == ".." {
				// Navigate to parent directory
				st.Root = e.Path
				entries, err := fileexplorer.BuildTree(e.Path)
				if err == nil {
					st.Entries = entries
					st.Cursor = 0
					st.ScrollOffset = 0
				}
			} else if e.IsDir {
				if e.Expanded {
					st.Entries = fileexplorer.CollapseAt(st.Entries, st.Cursor)
				} else {
					entries, err := fileexplorer.ExpandAt(st.Entries, st.Cursor)
					if err == nil {
						st.Entries = entries
					}
				}
			} else {
				// Insert file path into focused PTY.
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

		case key == ebiten.KeySlash && !meta:
			// Enter search mode
			st.SearchMode = true
			// Don't clear existing search query - allow editing it
			g.prevKeys[ebiten.KeyEnter] = ebiten.IsKeyPressed(ebiten.KeyEnter)
			g.prevKeys[ebiten.KeyBackspace] = ebiten.IsKeyPressed(ebiten.KeyBackspace)
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

	// If we have search results, navigate within them
	if len(st.SearchResults) > 0 {
		if st.Cursor > 0 {
			st.Cursor--
		}
	} else if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		// Legacy filtering support
		// Find current position in filtered list
		currentFilterIdx := -1
		for i, idx := range st.FilteredIndices {
			if idx == st.Cursor {
				currentFilterIdx = i
				break
			}
		}

		// Move to previous filtered item if possible
		if currentFilterIdx > 0 {
			st.Cursor = st.FilteredIndices[currentFilterIdx-1]
		} else if currentFilterIdx == -1 && len(st.FilteredIndices) > 0 {
			// Not on a filtered item, jump to last filtered item
			st.Cursor = st.FilteredIndices[len(st.FilteredIndices)-1]
		}
	} else {
		// Normal navigation
		if st.Cursor > 0 {
			st.Cursor--
		}
	}
	g.explorerEnsureVisible()
}

// explorerMoveDown moves the cursor down one entry.
func (g *Game) explorerMoveDown() {
	st := &g.fileExplorerState

	// If we have search results, navigate within them
	if len(st.SearchResults) > 0 {
		if st.Cursor < len(st.SearchResults)-1 {
			st.Cursor++
		}
	} else if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		// Legacy filtering support
		// Find current position in filtered list
		currentFilterIdx := -1
		for i, idx := range st.FilteredIndices {
			if idx == st.Cursor {
				currentFilterIdx = i
				break
			}
		}

		// Move to next filtered item if possible
		if currentFilterIdx >= 0 && currentFilterIdx < len(st.FilteredIndices)-1 {
			st.Cursor = st.FilteredIndices[currentFilterIdx+1]
		} else if currentFilterIdx == -1 && len(st.FilteredIndices) > 0 {
			// Not on a filtered item, jump to first filtered item
			st.Cursor = st.FilteredIndices[0]
		}
	} else {
		// Normal navigation
		if st.Cursor < len(st.Entries)-1 {
			st.Cursor++
		}
	}
	g.explorerEnsureVisible()
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

// handleExplorerSearchInput handles text input while in search mode.
func (g *Game) handleExplorerSearchInput() {
	st := &g.fileExplorerState
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	prevQuery := st.SearchQuery

	// Handle Enter to accept search and exit search mode
	enterPressed := ebiten.IsKeyPressed(ebiten.KeyEnter)
	enterWas := g.prevKeys[ebiten.KeyEnter]
	g.prevKeys[ebiten.KeyEnter] = enterPressed
	if enterPressed && !enterWas {
		st.SearchMode = false
		// If we have search results and selected one, insert its path
		if len(st.SearchResults) > 0 && st.Cursor >= 0 && st.Cursor < len(st.SearchResults) {
			selected := st.SearchResults[st.Cursor]
			g.focused.Term.SendBytes([]byte(selected.Path))
			g.closeFileExplorer()
		}
		return
	}

	// Handle Backspace with alt+backspace support
	backspacePressed := ebiten.IsKeyPressed(ebiten.KeyBackspace)
	backspaceWas := g.prevKeys[ebiten.KeyBackspace]
	g.prevKeys[ebiten.KeyBackspace] = backspacePressed
	if backspacePressed && !backspaceWas && !meta {
		ti := &TextInput{Text: st.SearchQuery}
		if ebiten.IsKeyPressed(ebiten.KeyAlt) {
			ti.DeleteWord()
		} else {
			ti.DeleteLastChar()
		}
		st.SearchQuery = ti.Text
	}

	// Handle text input
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= 0x20 && r != 0x7f {
				st.SearchQuery += string(r)
			}
		}
	}

	// If query changed, perform search of current directory only
	if st.SearchQuery != prevQuery {
		if st.SearchQuery == "" {
			st.SearchResults = nil
			st.Cursor = 0
		} else {
			// Search only current directory level (fast and safe)
			st.SearchResults = fileexplorer.SearchCurrentLevel(st.Root, st.SearchQuery)
			st.Cursor = 0
		}
		st.ScrollOffset = 0
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
				ti := &TextInput{Text: st.InputText}
				if ebiten.IsKeyPressed(ebiten.KeyAlt) {
					ti.DeleteWord()
				} else {
					ti.DeleteLastChar()
				}
				st.InputText = ti.Text
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

	// Calculate the visual row index based on filtering
	visualIdx := st.Cursor
	if len(st.SearchResults) > 0 {
		// When showing search results, cursor is already the visual index
		visualIdx = st.Cursor
	} else if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		// Legacy filtering support
		// Find the position of the cursor in the filtered list
		for i, idx := range st.FilteredIndices {
			if idx == st.Cursor {
				visualIdx = i
				break
			}
		}
	}

	rowTop := visualIdx * st.RowH
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

	// Convert visual index to actual index when filtering
	actualIdx := visualIdx
	if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		if visualIdx >= 0 && visualIdx < len(st.FilteredIndices) {
			actualIdx = st.FilteredIndices[visualIdx]
		} else {
			return // Click was outside filtered results
		}
	} else {
		// No filtering, check bounds on full list
		if actualIdx < 0 || actualIdx >= len(st.Entries) {
			return
		}
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
		// After expand/collapse, clear search to show the new structure
		if st.SearchQuery != "" {
			st.SearchQuery = ""
			st.FilteredIndices = nil
		}
	}
	st.Cursor = actualIdx
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
// Line endings are normalized to \r because the TTY expects carriage return
// for newlines (keyboard Enter sends \r, the line discipline converts it).
func (g *Game) handlePaste() {
	out, err := exec.Command("pbpaste").Output()
	if err != nil || len(out) == 0 {
		return
	}
	// NFC normalize — macOS clipboard uses NFD (decomposed accents).
	// Terminal programs expect precomposed characters (NFC).
	out = norm.NFC.Bytes(out)

	// Normalize line endings: \r\n → \r, then remaining \n → \r.
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

	absRow := buf.DisplayToAbsRow(row)

	// Snap to parent cell if clicking on a continuation cell.
	cell := buf.GetDisplayCell(row, col)
	if cell.Width == 0 && col > 0 {
		col--
		cell = buf.GetDisplayCell(row, col)
	}

	if !isWordChar(cell.Char) {
		return terminal.Selection{Active: true, StartRow: absRow, StartCol: col, EndRow: absRow, EndCol: col}
	}

	startCol := col
	for startCol > 0 {
		prev := buf.GetDisplayCell(row, startCol-1)
		if prev.Width == 0 {
			startCol--
			continue
		}
		if !isWordChar(prev.Char) {
			break
		}
		startCol--
	}

	endCol := col
	for endCol < buf.Cols-1 {
		next := buf.GetDisplayCell(row, endCol+1)
		if next.Width == 0 {
			endCol++
			continue
		}
		if !isWordChar(next.Char) {
			break
		}
		endCol++
	}

	return terminal.Selection{Active: true, StartRow: absRow, StartCol: startCol, EndRow: absRow, EndCol: endCol}
}

// copySelection copies the current selection text to the clipboard via pbcopy.
func (g *Game) copySelection() {
	result := g.extractSelectedText()
	if result == "" {
		return
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(result)
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

	// Sanitize the directory in case it's inside a .app bundle
	dir = sanitizeDirectory(dir)

	t, err := tab.New(g.cfg, paneRect, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	t.Title = fmt.Sprintf("tab %d", len(g.tabs)+1)
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
}

// newServerTab creates a new tab whose root pane is backed by zurm-server (Mode B).
// If the server binary is not found or the connection fails, the pane falls back
// to a local PTY — the tab is always created.
func (g *Game) newServerTab() {
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
	dir = sanitizeDirectory(dir)

	p, err := pane.NewServer(g.cfg, paneRect, g.font.CellW, g.font.CellH, dir, "")
	if err != nil {
		return
	}
	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   fmt.Sprintf("tab %d", len(g.tabs)+1),
	}
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
}

// closeActiveTab closes all panes in the active tab and removes it.
func (g *Game) closeActiveTab() {
	g.dismissTabHover()
	for _, leaf := range g.tabs[g.activeTab].Layout.Leaves() {
		leaf.Pane.Term.Close()
	}
	old := g.tabs
	g.tabs = append(g.tabs[:g.activeTab], g.tabs[g.activeTab+1:]...)
	old[len(old)-1] = nil // zero trailing slot to release Tab for GC
	if len(g.tabs) == 0 {
		g.layout = nil
		g.focused = nil
		return
	}
	if g.activeTab >= len(g.tabs) {
		g.activeTab = len(g.tabs) - 1
	}
	g.renderer.ClearPaneCache()
	g.renderer.SetLayoutDirty()
	g.syncActive()
}

// dismissTabHover clears the tab hover popover state and marks the screen dirty.
func (g *Game) dismissTabHover() {
	if g.tabHoverState.TabIdx >= 0 || g.tabHoverState.Active {
		renderer.DismissTabHover(&g.tabHoverState)
		g.screenDirty = true
	}
}

// updateTabHover tracks which tab the mouse is hovering over and manages the
// popover lifecycle (delay, activation, cache invalidation).
func (g *Game) updateTabHover(mx, my int) {
	if !g.cfg.Tabs.Hover.Enabled {
		return
	}

	tabBarH := g.renderer.TabBarHeight()
	numTabs := len(g.tabs)

	// Dismiss conditions: single tab, overlays open, dragging, cursor outside tab bar.
	if numTabs <= 1 || g.tabDragging || g.menuState.Open || g.overlayState.Open ||
		g.confirmState.Open || g.searchState.Open || g.paletteState.Open ||
		g.fileExplorerState.Open || g.tabSwitcherState.Open || g.tabSearchState.Open {
		g.dismissTabHover()
		return
	}

	if my < 0 || my >= tabBarH {
		g.dismissTabHover()
		return
	}

	// Compute which tab the cursor is over (same width calc as tab click handler).
	physW := int(float64(g.winW) * g.dpi)
	maxTabW := g.cfg.Tabs.MaxWidthChars * g.font.CellW
	tabW := physW / numTabs
	if tabW > maxTabW {
		tabW = maxTabW
	}
	if tabW <= 0 {
		g.dismissTabHover()
		return
	}

	hoverIdx := mx / tabW
	if hoverIdx < 0 || hoverIdx >= numTabs {
		g.dismissTabHover()
		return
	}

	// Skip the active tab — user already sees it.
	if hoverIdx == g.activeTab {
		g.dismissTabHover()
		return
	}

	// Tab changed — reset hover timer.
	if hoverIdx != g.tabHoverState.TabIdx {
		g.dismissTabHover()
		g.tabHoverState.TabIdx = hoverIdx
		g.tabHoverState.HoverStart = time.Now()
		return
	}

	// Check if delay has elapsed.
	delay := time.Duration(g.cfg.Tabs.Hover.DelayMs) * time.Millisecond
	if !g.tabHoverState.Active && time.Since(g.tabHoverState.HoverStart) < delay {
		return
	}

	// Activate the popover.
	if !g.tabHoverState.Active {
		g.tabHoverState.Active = true

		// Compute popover position (centered below the hovered tab).
		popW := int(float64(g.cfg.Tabs.Hover.Width) * g.dpi)
		popH := int(float64(g.cfg.Tabs.Hover.Height) * g.dpi)
		tabCenterX := hoverIdx*tabW + tabW/2
		popX := tabCenterX - popW/2
		popY := tabBarH + 4 // small gap below tab bar

		g.tabHoverState.PopoverX = popX
		g.tabHoverState.PopoverY = popY
		g.tabHoverState.PopoverW = popW
		g.tabHoverState.PopoverH = popH
		g.screenDirty = true
	}

	// Check cache validity and regenerate thumbnail if stale.
	hoveredTab := g.tabs[hoverIdx]
	cacheKey := renderer.TabHoverCacheKey(hoveredTab)
	if cacheKey != g.tabHoverState.CacheKey || g.tabHoverState.Thumbnail == nil {
		if g.tabHoverState.Thumbnail != nil {
			g.tabHoverState.Thumbnail.Deallocate()
		}
		contentRect := g.renderer.ComputeContentRect(hoveredTab)
		g.tabHoverState.Thumbnail = g.renderer.RenderTabThumbnail(hoveredTab, contentRect)
		g.tabHoverState.CacheKey = cacheKey
		g.screenDirty = true
	}
}

// focusEntry records a tab+pane focus state for the history stack.
type focusEntry struct {
	tabIdx int
	pane   *pane.Pane
}

// pushFocus records the current focus state before changing it.
func (g *Game) pushFocus() {
	if len(g.tabs) == 0 || g.focused == nil {
		return
	}
	e := focusEntry{tabIdx: g.activeTab, pane: g.focused}
	// Deduplicate: skip if top of stack is the same location.
	if n := len(g.focusHistory); n > 0 && g.focusHistory[n-1] == e {
		return
	}
	g.focusHistory = append(g.focusHistory, e)
	if len(g.focusHistory) > 50 {
		g.focusHistory = g.focusHistory[1:]
	}
}

// goBack pops the focus history stack and navigates to the previous location.
func (g *Game) goBack() {
	for len(g.focusHistory) > 0 {
		e := g.focusHistory[len(g.focusHistory)-1]
		g.focusHistory = g.focusHistory[:len(g.focusHistory)-1]
		// Skip stale entries (tab removed or pane closed).
		if e.tabIdx < 0 || e.tabIdx >= len(g.tabs) {
			continue
		}
		// Verify the pane still exists in that tab.
		found := false
		for _, leaf := range g.tabs[e.tabIdx].Layout.Leaves() {
			if leaf.Pane == e.pane {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		// Skip if it's the current location.
		if e.tabIdx == g.activeTab && e.pane == g.focused {
			continue
		}
		if e.tabIdx != g.activeTab {
			g.switchTabNoHistory(e.tabIdx)
		}
		g.setFocusNoHistory(e.pane)
		return
	}
}

// switchTab activates the tab at index i, recording focus history.
func (g *Game) switchTab(i int) {
	g.pushFocus()
	g.switchTabNoHistory(i)
}

// switchTabNoHistory activates the tab at index i without recording history.
// Used by goBack to avoid polluting the stack.
func (g *Game) switchTabNoHistory(i int) {
	if i < 0 || i >= len(g.tabs) {
		return
	}
	// Snapshot the outgoing tab's render generation so that UI-only bumps
	// (selection, search, cursor blink) do not trigger a false activity dot.
	g.tabs[g.activeTab].SnapshotGen()

	// Restore pane rects before leaving a zoomed tab so the layout is
	// correct when switching back later.
	if g.zoomed {
		g.unzoom()
	}
	g.activeTab = i
	g.tabs[i].SnapshotGen()
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
	g.dismissTabHover()
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

// moveTabLeft moves the active tab one position to the left.
func (g *Game) moveTabLeft() {
	if g.activeTab > 0 {
		g.reorderTab(g.activeTab, g.activeTab-1)
	}
}

// moveTabRight moves the active tab one position to the right.
func (g *Game) moveTabRight() {
	if g.activeTab < len(g.tabs)-1 {
		g.reorderTab(g.activeTab, g.activeTab+1)
	}
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

// --- Tab search (Cmd+J) ---

// openTabSearch opens the tab search overlay, closing conflicting surfaces.
func (g *Game) openTabSearch() {
	g.tabSearchState = renderer.TabSearchState{Open: true}
	g.tabSwitcherState = renderer.TabSwitcherState{}
	g.paletteState = renderer.PaletteState{}
	g.overlayState = renderer.OverlayState{}
	g.closeMenu()
	g.tabSearchRepeatActive = false
	g.prevKeys[ebiten.KeyArrowUp] = ebiten.IsKeyPressed(ebiten.KeyArrowUp)
	g.prevKeys[ebiten.KeyArrowDown] = ebiten.IsKeyPressed(ebiten.KeyArrowDown)
	g.screenDirty = true
}

// closeTabSearch closes the tab search overlay.
func (g *Game) closeTabSearch() {
	g.tabSearchState = renderer.TabSearchState{}
	g.tabSearchRepeatActive = false
	g.screenDirty = true
}

// updateTabSearchRepeat handles key repeat for arrow keys in the tab search overlay.
func (g *Game) updateTabSearchRepeat(key ebiten.Key, now time.Time) bool {
	pressed := ebiten.IsKeyPressed(key)
	wasPressed := g.prevKeys[key]
	g.prevKeys[key] = pressed

	if !pressed {
		if g.tabSearchRepeatActive && g.tabSearchRepeatKey == key {
			g.tabSearchRepeatActive = false
		}
		return false
	}

	keyRepeatDelay := time.Duration(g.cfg.Keyboard.RepeatDelayMs) * time.Millisecond
	keyRepeatInterval := time.Duration(g.cfg.Keyboard.RepeatIntervalMs) * time.Millisecond

	if !wasPressed {
		g.tabSearchRepeatKey = key
		g.tabSearchRepeatActive = true
		g.tabSearchRepeatStart = now
		g.tabSearchRepeatLast = now
		return true
	}
	if g.tabSearchRepeatActive && g.tabSearchRepeatKey == key &&
		now.Sub(g.tabSearchRepeatStart) >= keyRepeatDelay &&
		now.Sub(g.tabSearchRepeatLast) >= keyRepeatInterval {
		g.tabSearchRepeatLast = now
		return true
	}
	return false
}

// handleTabSearchInput processes keyboard input while the tab search overlay is open.
func (g *Game) handleTabSearchInput() {
	now := time.Now()

	filtered := renderer.FilterTabSearch(g.tabs, g.tabSearchState.Query)

	if g.updateTabSearchRepeat(ebiten.KeyArrowUp, now) && g.tabSearchState.Cursor > 0 {
		g.tabSearchState.Cursor--
	}
	if g.updateTabSearchRepeat(ebiten.KeyArrowDown, now) && g.tabSearchState.Cursor < len(filtered)-1 {
		g.tabSearchState.Cursor++
	}

	// ESC: clear query if non-empty, otherwise close.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if g.tabSearchState.Query != "" {
			g.tabSearchState.Query = ""
			g.tabSearchState.Cursor = 0
		} else {
			g.closeTabSearch()
		}
		g.prevKeys[ebiten.KeyEscape] = true
		g.screenDirty = true
		return
	}

	// Cmd+J toggles off.
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyJ) {
		g.closeTabSearch()
		g.prevKeys[ebiten.KeyJ] = true
		return
	}

	keys := []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter, ebiten.KeyBackspace}
	for _, key := range keys {
		pressed := ebiten.IsKeyPressed(key)
		wasPressed := g.prevKeys[key]
		if pressed && !wasPressed {
			switch key {
			case ebiten.KeyEnter, ebiten.KeyNumpadEnter:
				if len(filtered) > 0 && g.tabSearchState.Cursor < len(filtered) {
					g.switchTab(filtered[g.tabSearchState.Cursor].OrigIdx)
					g.closeTabSearch()
				}
				g.prevKeys[key] = pressed
				return
			case ebiten.KeyBackspace:
				if g.tabSearchState.Query != "" {
					r := []rune(g.tabSearchState.Query)
					if ebiten.IsKeyPressed(ebiten.KeyAlt) {
						// Alt+Backspace: delete word.
						ti := &TextInput{Text: g.tabSearchState.Query}
						ti.DeleteWord()
						g.tabSearchState.Query = ti.Text
					} else {
						g.tabSearchState.Query = string(r[:len(r)-1])
					}
					g.tabSearchState.Cursor = 0
				}
			}
		}
		g.prevKeys[key] = pressed
	}

	// Typing — append printable runes.
	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= 32 {
			g.tabSearchState.Query += string(r)
			g.tabSearchState.Cursor = 0
		}
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

	dir := sanitizeDirectory(g.statusBarState.Cwd)
	newRoot, newPane, err := g.layout.SplitH(g.focused, g.cfg, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.layout, g.font.CellH)
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

	dir := sanitizeDirectory(g.statusBarState.Cwd)
	newRoot, newPane, err := g.layout.SplitV(g.focused, g.cfg, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.setFocus(newPane)
}

// splitHServer splits the focused pane horizontally with a server-backed pane (Cmd+Shift+H).
func (g *Game) splitHServer() {
	g.zoomed = false
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	dir := sanitizeDirectory(g.statusBarState.Cwd)
	newRoot, newPane, err := g.layout.SplitHServer(g.focused, g.cfg, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.setFocus(newPane)
}

// splitVServer splits the focused pane vertically with a server-backed pane (Cmd+Shift+V).
func (g *Game) splitVServer() {
	g.zoomed = false
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	dir := sanitizeDirectory(g.statusBarState.Cwd)
	newRoot, newPane, err := g.layout.SplitVServer(g.focused, g.cfg, g.font.CellW, g.font.CellH, dir)
	if err != nil {
		return
	}
	g.updateLayout(newRoot)
	setPaneHeaders(g.layout, g.font.CellH)
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
	setPaneHeaders(g.layout, g.font.CellH)
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

// detachPaneToTab extracts the focused pane into a new tab.
// Only works when the current tab has multiple panes.
func (g *Game) detachPaneToTab() {
	leaves := g.layout.Leaves()
	if len(leaves) <= 1 {
		g.flashStatus("Only one pane — nothing to detach")
		return
	}
	g.zoomed = false

	// Remember the pane to detach.
	p := g.focused

	// Focus the next pane before detaching.
	nextFocus := g.layout.NextLeaf(p)

	// Detach from the current layout (does NOT close the terminal).
	newRoot := g.layout.Detach(p)
	if newRoot == nil {
		return
	}
	g.updateLayout(newRoot)
	if nextFocus != nil && nextFocus != p {
		g.setFocusNoHistory(nextFocus)
	} else if len(g.layout.Leaves()) > 0 {
		g.setFocusNoHistory(g.layout.Leaves()[0].Pane)
	}
	g.recomputeLayout()

	// Create a new tab with the detached pane.
	newTab := &tab.Tab{
		Layout:  pane.NewLeaf(p),
		Focused: p,
		Title:   p.CustomName,
	}
	// Insert the new tab right after the active tab.
	insertIdx := g.activeTab + 1
	g.tabs = append(g.tabs, nil)
	copy(g.tabs[insertIdx+1:], g.tabs[insertIdx:])
	g.tabs[insertIdx] = newTab
	g.switchTab(insertIdx)
	setPaneHeaders(g.layout, g.font.CellH)
	g.recomputeLayout()
}

// mergePaneToTab moves the focused pane into the target tab as a horizontal split.
// If the current tab becomes empty, it is removed.
func (g *Game) mergePaneToTab(targetIdx int) {
	if targetIdx < 0 || targetIdx >= len(g.tabs) || targetIdx == g.activeTab {
		return
	}
	g.zoomed = false

	p := g.focused
	srcIdx := g.activeTab
	singlePane := len(g.layout.Leaves()) <= 1

	if singlePane {
		// Single-pane tab: move the whole tab's pane into the target.
		// Detach the pane from its layout.
		targetTab := g.tabs[targetIdx]
		targetTab.Layout = targetTab.Layout.AttachH(targetTab.Focused, p)

		// Remove the source tab (don't close the terminal).
		g.tabs = append(g.tabs[:srcIdx], g.tabs[srcIdx+1:]...)
		if len(g.tabs) == 0 {
			return
		}
		// Adjust target index if source was before it.
		if srcIdx < targetIdx {
			targetIdx--
		}
		// Clamp activeTab to prevent out-of-bounds access inside switchTabNoHistory.
		if g.activeTab >= len(g.tabs) {
			g.activeTab = len(g.tabs) - 1
		}
		g.switchTabNoHistory(targetIdx)
		// Recompute the target tab's layout.
		setPaneHeaders(g.layout, g.font.CellH)
		g.recomputeLayout()
		g.setFocus(p)
	} else {
		// Multi-pane tab: detach focused pane and move it.
		nextFocus := g.layout.NextLeaf(p)
		newRoot := g.layout.Detach(p)
		if newRoot == nil {
			return
		}
		g.updateLayout(newRoot)
		if nextFocus != nil && nextFocus != p {
			g.setFocusNoHistory(nextFocus)
		} else if len(g.layout.Leaves()) > 0 {
			g.setFocusNoHistory(g.layout.Leaves()[0].Pane)
		}
		g.recomputeLayout()

		// Attach into target tab.
		targetTab := g.tabs[targetIdx]
		targetTab.Layout = targetTab.Layout.AttachH(targetTab.Focused, p)
		g.switchTab(targetIdx)
		setPaneHeaders(g.layout, g.font.CellH)
		g.recomputeLayout()
		g.setFocus(p)
	}
	g.flashStatus("Pane moved to tab")
}

// mergePaneToNextTab moves the focused pane into the next tab.
func (g *Game) mergePaneToNextTab() {
	target := (g.activeTab + 1) % len(g.tabs)
	if target == g.activeTab {
		g.flashStatus("Only one tab")
		return
	}
	g.mergePaneToTab(target)
}

// mergePaneToPrevTab moves the focused pane into the previous tab.
func (g *Game) mergePaneToPrevTab() {
	target := (g.activeTab - 1 + len(g.tabs)) % len(g.tabs)
	if target == g.activeTab {
		g.flashStatus("Only one tab")
		return
	}
	g.mergePaneToTab(target)
}

// setFocus updates the focused pane in both Game and the active tab.
func (g *Game) setFocus(p *pane.Pane) {
	g.pushFocus()
	g.setFocusNoHistory(p)
}

// setFocusNoHistory sets focus without recording history.
// Used by goBack to avoid polluting the stack.
func (g *Game) setFocusNoHistory(p *pane.Pane) {
	g.focused = p
	g.tabs[g.activeTab].Focused = p
	g.selDragging = false
	g.hoveredURL = nil
	g.urlMatches = nil
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

// resizePane adjusts the split ratio of the parent split containing the focused pane.
// dx/dy indicate direction: dx=-1 shrinks left, dx=1 grows right, etc.
func (g *Game) resizePane(dx, dy int) {
	if g.zoomed {
		return
	}
	parent, isLeft := g.layout.FindParent(g.focused)
	if parent == nil {
		return
	}
	step := 0.05
	switch parent.Kind {
	case pane.HSplit:
		if dx != 0 {
			delta := step * float64(dx)
			if !isLeft {
				delta = -delta
			}
			parent.Ratio += delta
		}
	case pane.VSplit:
		if dy != 0 {
			delta := step * float64(dy)
			if !isLeft {
				delta = -delta
			}
			parent.Ratio += delta
		}
	}
	if parent.Ratio < 0.1 {
		parent.Ratio = 0.1
	} else if parent.Ratio > 0.9 {
		parent.Ratio = 0.9
	}
	g.recomputeLayout()
	g.screenDirty = true
}

// startRenamePane enters inline rename mode for the focused pane.
func (g *Game) startRenamePane() {
	if g.focused == nil {
		return
	}
	// Cancel any active tab rename first.
	g.cancelRename()
	g.focused.Renaming = true
	g.focused.RenameText = g.focused.CustomName
	g.screenDirty = true
}

// commitPaneRename applies the pane rename text.
func (g *Game) commitPaneRename() {
	if g.focused != nil && g.focused.Renaming {
		g.focused.CustomName = sanitizeTitle(g.focused.RenameText)
		g.focused.Renaming = false
		g.focused.RenameText = ""
		g.screenDirty = true
	}
}

// cancelPaneRename exits pane rename mode without applying changes.
func (g *Game) cancelPaneRename() {
	if g.focused != nil && g.focused.Renaming {
		g.focused.Renaming = false
		g.focused.RenameText = ""
		g.screenDirty = true
	}
}

// renamingPane returns true if the focused pane is being renamed.
func (g *Game) renamingPane() bool {
	return g.focused != nil && g.focused.Renaming
}

// openTabContextMenu shows a small tab-specific context menu (Rename, Close).
// Actions target the tab under the cursor, not necessarily the active tab.
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

// --- Tab notes ---

// startNoteEdit enters inline note editing mode for tab at index idx.
func (g *Game) startNoteEdit(idx int) {
	if idx < 0 || idx >= len(g.tabs) {
		return
	}
	g.cancelNote()
	g.cancelRename()
	g.tabs[idx].Noting = true
	g.tabs[idx].NoteText = g.tabs[idx].Note
}

// commitNote applies the note text and exits note editing mode.
func (g *Game) commitNote() {
	for _, t := range g.tabs {
		if t.Noting {
			t.Note = strings.TrimSpace(t.NoteText)
			t.Noting = false
			t.NoteText = ""
			break
		}
	}
}

// cancelNote exits note editing mode without applying changes.
func (g *Game) cancelNote() {
	for _, t := range g.tabs {
		if t.Noting {
			t.Noting = false
			t.NoteText = ""
			break
		}
	}
}

// notingTabIdx returns the index of the tab currently in note editing mode, or -1.
func (g *Game) notingTabIdx() int {
	for i, t := range g.tabs {
		if t.Noting {
			return i
		}
	}
	return -1
}

// handleNoteInput processes keyboard input while a tab note edit is in progress.
func (g *Game) handleNoteInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.cancelNote()
		g.prevKeys[ebiten.KeyEscape] = true
		g.renameRepeatActive = false
		return
	}

	idx := g.notingTabIdx()
	if idx < 0 {
		return
	}

	ti := &TextInput{Text: g.tabs[idx].NoteText}

	// Backspace with repeat support (reuses rename repeat state).
	backspacePressed := ebiten.IsKeyPressed(ebiten.KeyBackspace)
	if backspacePressed {
		now := time.Now()
		if !g.renameRepeatActive || g.renameRepeatKey != ebiten.KeyBackspace {
			g.renameRepeatActive = true
			g.renameRepeatKey = ebiten.KeyBackspace
			g.renameRepeatStart = now
			g.renameRepeatLast = now
			g.renameRepeatAlt = alt
			if alt {
				ti.DeleteWord()
			} else {
				ti.DeleteLastChar()
			}
			g.tabs[idx].NoteText = ti.Text
		} else if now.Sub(g.renameRepeatStart) >= keyRepeatDelay &&
			now.Sub(g.renameRepeatLast) >= keyRepeatInterval {
			g.renameRepeatLast = now
			if g.renameRepeatAlt {
				ti.DeleteWord()
			} else {
				ti.DeleteLastChar()
			}
			g.tabs[idx].NoteText = ti.Text
		}
	} else {
		if g.renameRepeatKey == ebiten.KeyBackspace {
			g.renameRepeatActive = false
		}
	}

	// Edge-triggered: Enter commits.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		if pressed && !g.prevKeys[key] {
			g.commitNote()
			g.renameRepeatActive = false
		}
		g.prevKeys[key] = pressed
	}
	g.prevKeys[ebiten.KeyMeta] = meta
	g.prevKeys[ebiten.KeyAlt] = alt

	// Cmd+V — paste.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		if out, err := exec.Command("pbpaste").Output(); err == nil {
			ti.AddString(strings.ToValidUTF8(string(out), ""))
			g.tabs[idx].NoteText = ti.Text
		}
	}

	// Printable characters.
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			ti.AddChar(r)
		}
		g.tabs[idx].NoteText = ti.Text
	}
}

// handleRenameInput processes keyboard input while a tab rename is in progress.
func (g *Game) handleRenameInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// inpututil.IsKeyJustPressed catches sub-frame taps that polling misses.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.cancelRename()
		g.prevKeys[ebiten.KeyEscape] = true
		g.renameRepeatActive = false
		return
	}

	idx := g.renamingTabIdx()
	if idx < 0 {
		return
	}

	// Create TextInput wrapper for the rename text
	ti := &TextInput{Text: g.tabs[idx].RenameText}

	// Handle backspace with repeat support
	backspacePressed := ebiten.IsKeyPressed(ebiten.KeyBackspace)
	if backspacePressed {
		now := time.Now()

		// Check for initial press
		if !g.renameRepeatActive || g.renameRepeatKey != ebiten.KeyBackspace {
			g.renameRepeatActive = true
			g.renameRepeatKey = ebiten.KeyBackspace
			g.renameRepeatStart = now
			g.renameRepeatLast = now
			g.renameRepeatAlt = alt  // Store whether alt was pressed at start

			// Perform first action immediately
			if alt {
				ti.DeleteWord()
			} else {
				ti.DeleteLastChar()
			}
			g.tabs[idx].RenameText = ti.Text
		} else if now.Sub(g.renameRepeatStart) >= keyRepeatDelay &&
				   now.Sub(g.renameRepeatLast) >= keyRepeatInterval {
			// Repeat action
			g.renameRepeatLast = now
			if g.renameRepeatAlt {
				ti.DeleteWord()
			} else {
				ti.DeleteLastChar()
			}
			g.tabs[idx].RenameText = ti.Text
		}
	} else {
		// Reset repeat state when backspace is released
		if g.renameRepeatKey == ebiten.KeyBackspace {
			g.renameRepeatActive = false
		}
	}

	// Edge-triggered: Enter commits.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		if pressed && !g.prevKeys[key] {
			g.commitRename()
			g.renameRepeatActive = false
		}
		g.prevKeys[key] = pressed
	}
	g.prevKeys[ebiten.KeyMeta] = meta
	g.prevKeys[ebiten.KeyAlt] = alt

	// Cmd+V — paste.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		if out, err := exec.Command("pbpaste").Output(); err == nil {
			ti.AddString(strings.ToValidUTF8(string(out), ""))
			g.tabs[idx].RenameText = ti.Text
		}
	}

	// Handle regular text input
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			ti.AddChar(r)
		}
		g.tabs[idx].RenameText = ti.Text
	}
}

// handlePaneRenameInput processes keyboard input while a pane rename is in progress.
func (g *Game) handlePaneRenameInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)

	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.cancelPaneRename()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	ti := &TextInput{Text: g.focused.RenameText}

	// Backspace
	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		if ebiten.IsKeyPressed(ebiten.KeyAlt) {
			ti.DeleteWord()
		} else {
			ti.DeleteLastChar()
		}
		g.focused.RenameText = ti.Text
	}

	// Enter — commit
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter) {
		g.commitPaneRename()
		return
	}

	// Cmd+V — paste
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		if out, err := exec.Command("pbpaste").Output(); err == nil {
			ti.AddString(strings.ToValidUTF8(string(out), ""))
			g.focused.RenameText = ti.Text
		}
	}

	// Regular text input
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			ti.AddChar(r)
		}
		g.focused.RenameText = ti.Text
	}
}

// toggleZoom fullscreens the focused pane (Cmd+Z). Calling again restores the layout.
func (g *Game) toggleZoom() {
	if g.zoomed {
		g.unzoom()
		return
	}
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	fullRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	g.zoomed = true
	g.screenDirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
	p := g.focused
	p.HeaderH = 0 // zoomed pane has no header — only one pane visible
	p.Rect = fullRect
	cols := (fullRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW
	rows := (fullRect.Dy() - g.cfg.Window.Padding) / g.font.CellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	p.Cols = cols
	p.Rows = rows
	p.Term.Resize(cols, rows)
}

// recomputeLayout recalculates rects and resizes all pane terminals.
// Call after any layout mutation (split ratio change, split/close, zoom toggle).
func (g *Game) recomputeLayout() {
	g.dismissTabHover()
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	fullRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(fullRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()
}

// setPaneHeaders sets HeaderH on every leaf pane in the layout. When there are
// multiple panes, each gets a header bar; single-pane layouts get no header.
// Must be called before ComputeRects so the row calculation accounts for header space.
func setPaneHeaders(layout *pane.LayoutNode, cellH int) {
	leaves := layout.Leaves()
	headerH := 0
	if len(leaves) > 1 {
		headerH = cellH + 4
	}
	for _, leaf := range leaves {
		leaf.Pane.HeaderH = headerH
	}
}

// unzoom restores pane rects to the normal layout. Called by toggleZoom and
// switchTab so the layout is always consistent when leaving zoom state.
func (g *Game) unzoom() {
	g.zoomed = false
	g.screenDirty = true
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()

	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	fullRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	setPaneHeaders(g.layout, g.font.CellH)
	g.layout.ComputeRects(fullRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range g.layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
}

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

// adjustFontSize changes the font size by delta points and reloads the font.
// Clamped to [6, 72]. Skipped when recording is active.
func (g *Game) adjustFontSize(delta float64) {
	if g.recorder != nil && g.recorder.Active() {
		g.flashStatus("Cannot resize font while recording")
		return
	}
	newSize := g.cfg.Font.Size + delta
	if newSize < 6 {
		newSize = 6
	}
	if newSize > 72 {
		newSize = 72
	}
	if newSize == g.cfg.Font.Size {
		return
	}

	fontBytes := jetbrainsMono
	if g.cfg.Font.File != "" {
		if data, loadErr := os.ReadFile(g.cfg.Font.File); loadErr == nil {
			fontBytes = data
		}
	}
	fbSlice := loadFontFallbacks(g.cfg.Font)
	fontR, err := renderer.NewFontRenderer(fontBytes, newSize*g.dpi, fbSlice...)
	if err != nil {
		g.flashStatus("Font resize failed: " + err.Error())
		return
	}

	g.cfg.Font.Size = newSize
	g.font = fontR
	g.renderer.SetFont(fontR)
	g.renderer.SetSize(int(float64(g.winW)*g.dpi), int(float64(g.winH)*g.dpi))
	for _, t := range g.tabs {
		g.layout = t.Layout
		g.recomputeLayout()
	}
	g.layout = g.tabs[g.activeTab].Layout
	g.screenDirty = true
	g.flashStatus(fmt.Sprintf("Font size: %.0fpt", newSize))
}

// reloadConfig re-reads config.toml and propagates all changes at runtime.
func (g *Game) reloadConfig() {
	g.dismissTabHover()
	newCfg, meta, err := config.LoadWithMeta()
	if err != nil {
		g.flashStatus("Config reload failed: " + err.Error())
		return
	}
	config.ApplyTheme(newCfg, meta)

	oldFont := g.cfg.Font
	g.cfg = newCfg

	// Propagate colors to renderer and all terminal panes.
	g.renderer.ReloadColors(newCfg)
	for _, t := range g.tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.UpdateColors(newCfg)
		}
	}

	// Font reload — skip if recording is active (dimensions would become stale).
	fontChanged := newCfg.Font.Size != oldFont.Size ||
		newCfg.Font.File != oldFont.File ||
		newCfg.Font.Fallback != oldFont.Fallback ||
		!slices.Equal(newCfg.Font.Fallbacks, oldFont.Fallbacks)
	if fontChanged && (g.recorder == nil || !g.recorder.Active()) {
		fontBytes := jetbrainsMono
		if newCfg.Font.File != "" {
			if data, loadErr := os.ReadFile(newCfg.Font.File); loadErr == nil {
				fontBytes = data
			}
		}
		fbSlice := loadFontFallbacks(newCfg.Font)
		fontR, fontErr := renderer.NewFontRenderer(fontBytes, newCfg.Font.Size*g.dpi, fbSlice...)
		if fontErr == nil {
			g.font = fontR
			g.renderer.SetFont(fontR)
			g.renderer.SetSize(int(float64(g.winW)*g.dpi), int(float64(g.winH)*g.dpi))
			// Recompute all tab layouts with new cell dimensions.
			for _, t := range g.tabs {
				g.layout = t.Layout
				g.recomputeLayout()
			}
			// Restore active tab layout pointer.
			g.layout = g.tabs[g.activeTab].Layout
		} else {
			// Rollback font config to match the actual font renderer.
			g.cfg.Font = oldFont
		}
	}

	// Update runtime settings.
	if newCfg.Keyboard.RepeatDelayMs > 0 {
		keyRepeatDelay = time.Duration(newCfg.Keyboard.RepeatDelayMs) * time.Millisecond
	}
	if newCfg.Keyboard.RepeatIntervalMs > 0 {
		keyRepeatInterval = time.Duration(newCfg.Keyboard.RepeatIntervalMs) * time.Millisecond
	}
	ebiten.SetTPS(newCfg.Performance.TPS)
	g.blocksEnabled = newCfg.Blocks.Enabled
	g.renderer.BlocksEnabled = g.blocksEnabled

	// Always recompute layout — status bar height, padding, or divider width
	// may have changed, which shifts pane rects.
	for _, t := range g.tabs {
		g.layout = t.Layout
		g.recomputeLayout()
	}
	g.layout = g.tabs[g.activeTab].Layout

	// Rebuild palette to pick up new theme files.
	g.buildPalette()

	// Vault enable/disable: nil out vault and clear ghost when disabled.
	if !g.cfg.Vault.Enabled && g.vault != nil {
		g.vault = nil
		g.vaultSuggest = ""
		g.vaultLineCache = ""
		g.vaultSkip = 0
	}

	g.screenDirty = true
	g.flashStatus("Config reloaded")
}

// switchTheme applies a theme by name at runtime.
// When the user explicitly picks a theme from the palette, the theme colors
// are applied directly — the meta-merge (user-explicit overrides) only applies
// during reloadConfig / ApplyTheme for partial config.toml customization.
func (g *Game) switchTheme(name string) {
	themeColors, err := config.LoadTheme(name)
	if err != nil {
		g.flashStatus("Theme not found: " + name)
		return
	}

	g.cfg.Theme.Name = name
	g.cfg.Colors = themeColors

	// Propagate.
	g.renderer.ReloadColors(g.cfg)
	for _, t := range g.tabs {
		for _, leaf := range t.Layout.Leaves() {
			leaf.Pane.Term.UpdateColors(g.cfg)
		}
	}

	g.screenDirty = true
	g.flashStatus("Theme: " + name)
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
		g.openSearch,
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
		// Speech
		g.speakSelection,
		g.stopSpeaking,
		g.pauseSpeaking,
		g.continueSpeaking,
		g.openVoiceSelector,
		// Dictation
		g.startDictation,
		g.stopDictation,
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

	// Append dynamic voice entries from system voices.
	for _, v := range g.speaker.ListVoices() {
		vi := v // capture for closure
		entries = append(entries, renderer.PaletteEntry{Name: "Voice: " + vi.Name + " (" + vi.Language + ")", Shortcut: ""})
		actions = append(actions, func() { g.selectVoice(vi) })
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
		case key == ebiten.KeyBackspace:
			if !meta {
				ti := &TextInput{Text: g.paletteState.Query}
				if ebiten.IsKeyPressed(ebiten.KeyAlt) {
					ti.DeleteWord()
				} else {
					ti.DeleteLastChar()
				}
				g.paletteState.Query = ti.Text
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
				ti := &TextInput{Text: g.overlayState.SearchQuery}
				if ebiten.IsKeyPressed(ebiten.KeyAlt) {
					ti.DeleteWord()
				} else {
					ti.DeleteLastChar()
				}
				g.overlayState.SearchQuery = ti.Text
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

// collectStats populates the stats overlay with current runtime metrics.
func (g *Game) collectStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	g.statsState.TPS = ebiten.ActualTPS()
	g.statsState.FPS = ebiten.ActualFPS()
	g.statsState.Goroutines = runtime.NumGoroutine()
	g.statsState.HeapAlloc = m.HeapAlloc
	g.statsState.HeapSys = m.HeapSys
	g.statsState.NumGC = m.NumGC
	if m.NumGC > 0 {
		g.statsState.GCPauseNs = m.PauseNs[(m.NumGC+255)%256]
	}
	g.statsState.TabCount = len(g.tabs)
	paneCount := 0
	for _, t := range g.tabs {
		paneCount += len(t.Layout.Leaves())
	}
	g.statsState.PaneCount = paneCount
	if g.focused != nil {
		g.focused.Term.Buf.RLock()
		g.statsState.BufRows = g.focused.Term.Buf.Rows
		g.statsState.BufCols = g.focused.Term.Buf.Cols
		g.statsState.Scrollback = g.focused.Term.Buf.ScrollbackLen()
		g.focused.Term.Buf.RUnlock()
	}
	g.statsLastTick = time.Now()
	g.screenDirty = true
}

// saveSession persists the current tab state to the session file.
// Called before every exit path. Errors are logged but do not block exit.
// Only saves if AutoSave is enabled in config.
func (g *Game) saveSession() {
	if !g.cfg.Session.Enabled || !g.cfg.Session.AutoSave {
		return
	}
	g.doSaveSession()
}

// manualSaveSession saves the session regardless of AutoSave setting.
// Called from command palette for explicit user saves.
func (g *Game) manualSaveSession() {
	if !g.cfg.Session.Enabled {
		g.flashStatus("Session saving is disabled in config")
		return
	}
	g.doSaveSession()
	g.flashStatus("Session saved")
}

// doSaveSession performs the actual session save operation.
func (g *Game) doSaveSession() {
	data := &session.SessionData{
		Version:   1,
		ActiveTab: g.activeTab,
	}
	for _, t := range g.tabs {
		leaves := t.Layout.Leaves()
		if len(leaves) == 0 {
			continue
		}
		// Use first pane's CWD as fallback for tab CWD
		term := leaves[0].Pane.Term
		td := session.TabData{
			Cwd:         term.Cwd,
			Title:       t.Title,
			UserRenamed: t.UserRenamed,
			Note:        t.Note,
			Layout:      serializePaneLayout(t.Layout), // Save the pane layout
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

// serializePaneLayout converts a pane.LayoutNode tree to session.PaneLayout for persistence.
func serializePaneLayout(node *pane.LayoutNode) *session.PaneLayout {
	if node == nil {
		return nil
	}

	layout := &session.PaneLayout{
		Ratio: node.Ratio,
	}

	switch node.Kind {
	case pane.Leaf:
		layout.Kind = "leaf"
		if node.Pane != nil && node.Pane.Term != nil {
			layout.Cwd = node.Pane.Term.Cwd
		}
		if node.Pane != nil {
			layout.CustomName = node.Pane.CustomName
			layout.ServerSessionID = node.Pane.ServerSessionID
		}
	case pane.HSplit:
		layout.Kind = "hsplit"
		layout.Left = serializePaneLayout(node.Left)
		layout.Right = serializePaneLayout(node.Right)
	case pane.VSplit:
		layout.Kind = "vsplit"
		layout.Left = serializePaneLayout(node.Left)
		layout.Right = serializePaneLayout(node.Right)
	}

	return layout
}

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

// deserializePaneLayout reconstructs a pane.LayoutNode tree from saved session.PaneLayout.
func deserializePaneLayout(cfg *config.Config, rect image.Rectangle, cellW, cellH int, layout *session.PaneLayout) (*pane.LayoutNode, error) {
	if layout == nil {
		return nil, fmt.Errorf("nil layout")
	}

	switch layout.Kind {
	case "leaf":
		dir := sanitizeDirectory(layout.Cwd)
		var p *pane.Pane
		var err error
		if layout.ServerSessionID != "" {
			// Reconnect to the zurm-server session (Mode B).
			// Falls back to local PTY if the server or session is gone.
			p, err = pane.NewServer(cfg, rect, cellW, cellH, dir, layout.ServerSessionID)
		} else {
			p, err = pane.New(cfg, rect, cellW, cellH, dir)
		}
		if err != nil {
			return nil, err
		}
		p.CustomName = layout.CustomName
		return pane.NewLeaf(p), nil

	case "hsplit", "vsplit":
		// Recursively deserialize children
		left, err := deserializePaneLayout(cfg, rect, cellW, cellH, layout.Left)
		if err != nil {
			return nil, err
		}
		right, err := deserializePaneLayout(cfg, rect, cellW, cellH, layout.Right)
		if err != nil {
			return nil, err
		}

		kind := pane.HSplit
		if layout.Kind == "vsplit" {
			kind = pane.VSplit
		}

		node := &pane.LayoutNode{
			Kind:  kind,
			Left:  left,
			Right: right,
			Ratio: layout.Ratio,
		}

		// Ensure ratio is valid
		if node.Ratio <= 0 || node.Ratio >= 1 {
			node.Ratio = 0.5
		}

		return node, nil

	default:
		return nil, fmt.Errorf("unknown layout kind: %s", layout.Kind)
	}
}

// updateURLHover rescans URLs in the focused pane and updates hover state.
func (g *Game) updateURLHover(mx, my, pad int) {
	if g.focused == nil {
		return
	}
	// Convert pixel to cell coordinates within the focused pane.
	col := (mx - g.focused.Rect.Min.X - pad) / g.font.CellW
	row := (my - g.focused.Rect.Min.Y - pad - g.focused.HeaderH) / g.font.CellH
	if col < 0 || row < 0 || col >= g.focused.Cols || row >= g.focused.Rows {
		if g.hoveredURL != nil {
			g.hoveredURL = nil
			g.screenDirty = true
		}
		return
	}

	// Rescan URLs from the buffer.
	g.focused.Term.Buf.RLock()
	g.urlMatches = g.focused.Term.Buf.DetectURLs()
	g.focused.Term.Buf.RUnlock()

	hit := terminal.URLAt(g.urlMatches, row, col)
	if hit != g.hoveredURL {
		// Pointer comparison is sufficient — URLAt returns a pointer into urlMatches.
		g.hoveredURL = hit
		g.screenDirty = true
	}
}

// flashStatus shows msg in the status bar for 3 seconds.
func (g *Game) flashStatus(msg string) {
	g.statusBarState.FlashMessage = msg
	g.flashExpiry = time.Now().Add(3 * time.Second)
	g.screenDirty = true
}

// speakSelection extracts the current selection (or visible buffer if no
// selection) and reads it aloud via AVSpeechSynthesizer.
func (g *Game) speakSelection() {
	if !g.cfg.Voice.Enabled {
		return
	}

	text := g.extractSelectedText()
	if text == "" {
		text = g.getRecentBufferText(g.cfg.Voice.ReadLines)
	}
	if text == "" {
		g.flashStatus("Nothing to speak")
		return
	}

	g.speaker.Speak(text, g.cfg.Voice.VoiceID, g.cfg.Voice.Rate, g.cfg.Voice.Pitch, g.cfg.Voice.Volume)
	g.flashStatus("Speaking…")
}

// stopSpeaking kills any active TTS process.
func (g *Game) stopSpeaking() {
	g.speaker.Stop()
	g.flashStatus("Speech stopped")
}

// pauseSpeaking pauses active speech.
func (g *Game) pauseSpeaking() {
	g.speaker.Pause()
	g.flashStatus("Speech paused")
}

// continueSpeaking resumes paused speech.
func (g *Game) continueSpeaking() {
	g.speaker.Continue()
	g.flashStatus("Speech resumed")
}

// selectVoice sets the active voice by identifier.
func (g *Game) selectVoice(v voice.VoiceInfo) {
	g.cfg.Voice.VoiceID = v.ID
	g.flashStatus("Voice: " + v.Name)
}

// openVoiceSelector opens the command palette pre-filtered to voice entries.
func (g *Game) openVoiceSelector() {
	g.paletteState.Open = true
	g.paletteState.Query = "Voice: "
	g.paletteState.Cursor = 0
	g.screenDirty = true
}

// injectDictation sends the transcript to the focused PTY and closes the overlay.
func (g *Game) injectDictation(text string) {
	if g.focused != nil && text != "" {
		g.focused.Term.SendBytes([]byte(text))
		g.dictationEnterAt = time.Now().Add(100 * time.Millisecond)
	}
	g.stopDictation()
}

// startDictation opens the dictation overlay and begins STT speech recognition.
func (g *Game) startDictation() {
	if g.dictationState.Open {
		return
	}
	g.dictationState = renderer.DictationState{
		Open:   true,
		Status: "Initializing…",
	}
	g.dictationLastText = ""
	g.dictationLastChange = time.Time{}
	g.screenDirty = true

	if !g.listener.IsAuthorized() {
		g.dictationState.Status = "Requesting permission… grant both Speech and Microphone, then retry"
		g.listener.RequestAuthorization()
		return
	}
	g.listener.SetLocale(g.cfg.Voice.Locale)
	g.listener.StartListening()
	g.dictationState.Status = "Listening… speak now"
}

// stopDictation stops STT and closes the dictation overlay.
func (g *Game) stopDictation() {
	if g.listener.IsListening() {
		g.listener.StopListening()
	}
	g.dictationState = renderer.DictationState{}
	g.dictationLastText = ""
	g.dictationLastChange = time.Time{}
	g.screenDirty = true
}

// handleDictationInput processes keyboard input while the dictation overlay is open.
func (g *Game) handleDictationInput() {
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.stopDictation()
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		if g.dictationState.Transcript != "" {
			g.injectDictation(g.dictationState.Transcript)
			return
		}
		// Re-trigger start if permission was just granted or recognizer stopped.
		if !g.listener.IsListening() && g.listener.IsAuthorized() {
			g.listener.SetLocale(g.cfg.Voice.Locale)
			g.listener.StartListening()
			g.dictationState.Status = "Listening… speak now"
			g.dictationState.Transcript = ""
			g.dictationLastText = ""
			g.dictationLastChange = time.Time{}
		}
	}
}

// extractSelectedText returns the selected text from the focused pane,
// or empty string if no selection is active.
func (g *Game) extractSelectedText() string {
	g.focused.Term.Buf.RLock()
	sel := g.focused.Term.Buf.Selection
	cols := g.focused.Term.Buf.Cols

	if !sel.Active {
		g.focused.Term.Buf.RUnlock()
		return ""
	}

	norm := sel.Normalize()
	maxAbsRow := g.focused.Term.Buf.ScrollbackLen() + g.focused.Term.Buf.Rows - 1
	if norm.StartRow < 0 {
		norm.StartRow = 0
	}
	if norm.EndRow > maxAbsRow {
		norm.EndRow = maxAbsRow
	}

	var text strings.Builder
	for r := norm.StartRow; r <= norm.EndRow; r++ {
		if r > norm.StartRow && !g.focused.Term.Buf.IsAbsRowWrapped(r) {
			text.WriteByte('\n')
		}
		colStart := 0
		colEnd := cols - 1
		if r == norm.StartRow {
			colStart = norm.StartCol
		}
		if r == norm.EndRow {
			colEnd = norm.EndCol
		}
		var line strings.Builder
		for c := colStart; c <= colEnd && c < cols; c++ {
			cell := g.focused.Term.Buf.GetAbsCell(r, c)
			if cell.Width == 0 {
				continue
			}
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		text.WriteString(strings.TrimRight(line.String(), " "))
	}
	g.focused.Term.Buf.RUnlock()
	return text.String()
}

// getRecentBufferText returns the last maxLines non-empty lines from the
// bottom of the visible buffer. Useful for bell-triggered TTS where only
// the recent output matters (e.g., Claude Code's question).
func (g *Game) getRecentBufferText(maxLines int) string {
	g.focused.Term.Buf.RLock()
	defer g.focused.Term.Buf.RUnlock()

	rows := g.focused.Term.Buf.Rows
	cols := g.focused.Term.Buf.Cols

	// Collect non-empty lines from the bottom up.
	var lines []string
	for r := rows - 1; r >= 0 && len(lines) < maxLines; r-- {
		absRow := g.focused.Term.Buf.DisplayToAbsRow(r)
		var line strings.Builder
		for c := 0; c < cols; c++ {
			cell := g.focused.Term.Buf.GetAbsCell(absRow, c)
			if cell.Width == 0 {
				continue
			}
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		trimmed := strings.TrimRight(line.String(), " ")
		if trimmed != "" && hasAlphanumeric(trimmed) {
			lines = append(lines, trimmed)
		}
	}

	// Reverse to restore top-to-bottom order.
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

// hasAlphanumeric returns true if s contains at least one ASCII letter or digit.
func hasAlphanumeric(s string) bool {
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// toggleRecording starts or stops a screen recording (FFMPEG → MP4).
// Stop runs in a background goroutine because ffmpeg finalization can block
// for several seconds. Completion is communicated via g.recDone.
func (g *Game) toggleRecording() {
	if g.recorder.Active() {
		g.flashStatus("Saving recording…")
		go func() {
			path, err := g.recorder.Stop()
			var msg string
			if err != nil {
				msg = "Recording failed: " + err.Error()
			} else {
				msg = "Saved: " + filepath.Base(path)
			}
			// Non-blocking send: if a previous message hasn't been drained
			// yet, this goroutine still exits cleanly (no leak).
			select {
			case g.recDone <- msg:
			default:
			}
		}()
		return
	}
	// Sync recorder dimensions to match what Layout() produces — the DPI
	// round-trip (physical→logical→physical) can lose a pixel, which would
	// cause AddFrame's size check to silently drop every frame.
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	g.recorder.Resize(physW, physH)
	if err := g.recorder.Start(); err != nil {
		g.flashStatus("Record error: " + err.Error())
		return
	}
	g.flashStatus("Recording (" + g.recorder.OutputMode() + ") — Cmd+Shift+. to stop")
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
# Emit B (prompt end) via zle-line-init — fires after the prompt is drawn,
# works with any prompt tool (starship, p10k, oh-my-zsh).
_zurm_line_init() { print -n '\033]133;B\007'; }
zle -N zle-line-init _zurm_line_init
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
	var result [][]byte
	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p) // #nosec G304 -- desktop app loading user-configured font paths
		if err != nil {
			log.Printf("fallback font %q not found, skipping: %v", p, err)
			continue
		}
		result = append(result, data)
	}
	return result
}

// openMarkdownViewer captures terminal content and opens the markdown viewer overlay.
func (g *Game) openMarkdownViewer() {
	content := g.captureMarkdownContent()
	if content == "" {
		g.flashStatus("No content to render")
		return
	}
	g.openMarkdownViewerWithContent(content, "Markdown Viewer")
}

// openMarkdownViewerWithContent opens the markdown viewer with arbitrary content.
// Reuse point for future llms.txt browser.
func (g *Game) openMarkdownViewerWithContent(content, title string) {
	// Derive wrap columns from the actual panel pixel width and cell width.
	// Panel is 80% of window width; subtract padding (2 * cellW) and scrollbar (4px).
	physW := int(float64(g.winW) * g.dpi)
	panelW := physW * 80 / 100
	cw := g.font.CellW
	wrapCols := (panelW - 2*cw - 4) / cw
	if wrapCols < 40 {
		wrapCols = 40
	}

	lines := markdown.Parse(content, wrapCols)
	g.mdViewerState = renderer.MarkdownViewerState{
		Open:  true,
		Title: title,
		Lines: lines,
	}
	g.screenDirty = true
}

// openURLInput opens the llms.txt URL input overlay.
func (g *Game) openURLInput() {
	g.urlInputState = renderer.URLInputState{Open: true}
	g.screenDirty = true
}

// handleURLInputInput processes keyboard input while the URL input overlay is open.
func (g *Game) handleURLInputInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// ESC: close overlay (also cancels any pending fetch).
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.urlInputState = renderer.URLInputState{}
		g.llmsFetchCh = nil
		g.urlRepeatActive = false
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	// While loading, ignore all other input.
	if g.urlInputState.Loading {
		return
	}

	ti := &TextInput{Text: g.urlInputState.Query}

	// Backspace with key repeat (same pattern as rename/note input).
	backspacePressed := ebiten.IsKeyPressed(ebiten.KeyBackspace)
	if backspacePressed && !meta {
		now := time.Now()
		if !g.urlRepeatActive || g.urlRepeatKey != ebiten.KeyBackspace {
			g.urlRepeatActive = true
			g.urlRepeatKey = ebiten.KeyBackspace
			g.urlRepeatStart = now
			g.urlRepeatLast = now
			g.urlRepeatAlt = alt
			if alt {
				ti.DeleteWord()
			} else {
				ti.DeleteLastChar()
			}
			g.urlInputState.Query = ti.Text
		} else if now.Sub(g.urlRepeatStart) >= keyRepeatDelay &&
			now.Sub(g.urlRepeatLast) >= keyRepeatInterval {
			g.urlRepeatLast = now
			if g.urlRepeatAlt {
				ti.DeleteWord()
			} else {
				ti.DeleteLastChar()
			}
			g.urlInputState.Query = ti.Text
		}
	} else {
		if g.urlRepeatKey == ebiten.KeyBackspace {
			g.urlRepeatActive = false
		}
	}

	// Edge-triggered: Enter submits.
	for _, key := range []ebiten.Key{ebiten.KeyEnter, ebiten.KeyNumpadEnter} {
		pressed := ebiten.IsKeyPressed(key)
		if pressed && !g.prevKeys[key] {
			q := strings.TrimSpace(g.urlInputState.Query)
			if q != "" {
				g.startLLMSFetch(q)
			}
		}
		g.prevKeys[key] = pressed
	}

	// Cmd+V — paste (first line only).
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		if out, err := exec.Command("pbpaste").Output(); err == nil && len(out) > 0 {
			line := strings.TrimSpace(strings.SplitN(strings.ToValidUTF8(string(out), ""), "\n", 2)[0])
			g.urlInputState.Query += line
		}
	}

	// Printable character input.
	if !meta {
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= 0x20 && r != 0x7f {
				g.urlInputState.Query += string(r)
			}
		}
	}
}

// startLLMSFetch initiates an async HTTP fetch for both /llms.txt and /llms-full.txt
// from the given domain. Both are fetched in parallel; either may be empty.
func (g *Game) startLLMSFetch(domain string) {
	// Strip protocol prefix, known paths, and trailing slash.
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimRight(domain, "/")
	domain = strings.TrimSuffix(domain, "/llms.txt")
	domain = strings.TrimSuffix(domain, "/llms-full.txt")

	g.urlInputState.Loading = true
	ch := make(chan llmsFetchResult, 1)
	g.llmsFetchCh = ch

	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		type partial struct {
			body string
			ok   bool
		}

		fetch := func(path string) partial {
			resp, err := client.Get("https://" + domain + path) // #nosec G107 — user-provided URL, intentional
			if err != nil {
				return partial{}
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return partial{}
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return partial{}
			}
			return partial{body: string(body), ok: true}
		}

		shortCh := make(chan partial, 1)
		fullCh := make(chan partial, 1)
		go func() { shortCh <- fetch("/llms.txt") }()
		go func() { fullCh <- fetch("/llms-full.txt") }()

		short := <-shortCh
		full := <-fullCh

		if !short.ok && !full.ok {
			ch <- llmsFetchResult{Err: fmt.Errorf("no llms.txt found on %s", domain), Domain: domain}
			return
		}

		ch <- llmsFetchResult{Short: short.body, Full: full.body, Domain: domain}
	}()
}

// drainLLMSFetch reads a completed async llms.txt fetch result when available.
func (g *Game) drainLLMSFetch() {
	if g.llmsFetchCh == nil {
		return
	}
	select {
	case result := <-g.llmsFetchCh:
		g.urlInputState = renderer.URLInputState{}
		g.llmsFetchCh = nil
		g.screenDirty = true

		if result.Err != nil {
			g.flashStatus("llms.txt: " + result.Err.Error())
			return
		}

		// Cache both results for Tab switching.
		g.llmsShort = result.Short
		g.llmsFull = result.Full
		g.llmsDomain = result.Domain
		g.llmsViewingFull = false

		// Show /llms.txt if available, otherwise /llms-full.txt.
		content := result.Short
		title := "llms.txt — " + result.Domain
		if content == "" {
			content = result.Full
			title = "llms-full.txt — " + result.Domain
			g.llmsViewingFull = true
		}
		g.openMarkdownViewerWithContent(content, title)
		g.mdViewerState.HasAlt = result.Short != "" && result.Full != ""
		g.mdViewerState.IsLLMS = true
		g.mdViewerState.HistoryLen = len(g.llmsHistory)
		g.mdViewerState.ForwardLen = len(g.llmsForward)
	default:
	}
}

// captureMarkdownContent extracts text from the terminal for markdown viewing.
// Priority: last block output > active selection > visible screen.
func (g *Game) captureMarkdownContent() string {
	if g.focused == nil {
		return ""
	}

	buf := g.focused.Term.Buf
	buf.RLock()
	defer buf.RUnlock()

	// Priority 1: last completed block output.
	// Blocks are always tracked by the parser regardless of blocksEnabled.
	if len(buf.Blocks) > 0 {
		// Find the last block with valid output range.
		for i := len(buf.Blocks) - 1; i >= 0; i-- {
			b := buf.Blocks[i]
			if b.AbsCmdRow >= 0 && b.AbsEndRow > b.AbsCmdRow {
				return buf.TextRange(b.AbsCmdRow, b.AbsEndRow)
			}
		}
	}

	// Priority 2: active selection.
	sel := buf.Selection
	if sel.Active {
		norm := sel.Normalize()
		return buf.TextRange(norm.StartRow, norm.EndRow)
	}

	// Priority 3: if scrolled back, capture from scroll position to end
	// of primary screen (user positioned viewport at start of content).
	// Otherwise, capture primary screen only.
	absStart := buf.DisplayToAbsRow(0)
	absEnd := buf.ScrollbackLen() + buf.Rows - 1
	return buf.TextRange(absStart, absEnd)
}

// handleMarkdownViewerInput processes keyboard input while the markdown viewer is open.
func (g *Game) handleMarkdownViewerInput() {
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// Follow-link mode: letter keys follow, Esc cancels.
	if g.mdViewerState.FollowMode {
		if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
			g.mdViewerState.FollowMode = false
			g.mdViewerState.LinkHints = nil
			g.screenDirty = true
			return
		}
		// Check for letter key press (a-z). Only the first input char matters.
		if chars := ebiten.AppendInputChars(nil); len(chars) > 0 {
			r := chars[0]
			if r >= 'a' && r <= 'z' {
				for _, hint := range g.mdViewerState.LinkHints {
					if hint.Label == r {
						g.mdViewerState.FollowMode = false
						g.mdViewerState.LinkHints = nil
						g.llmsFollowLink(hint.URL)
						return
					}
				}
			}
			// Any non-matching key cancels follow mode.
			g.mdViewerState.FollowMode = false
			g.mdViewerState.LinkHints = nil
			g.screenDirty = true
			return
		}
		return
	}

	// Search mode: text input takes priority.
	if g.mdViewerState.SearchOpen {
		g.handleMarkdownSearchInput()
		return
	}

	// ESC or Cmd+Shift+M: close viewer.
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.mdViewerState = renderer.MarkdownViewerState{}
		g.renderer.SetLayoutDirty()
		g.renderer.ClearPaneCache()
		g.screenDirty = true
		g.prevKeys[ebiten.KeyEscape] = true
		return
	}

	if meta && shift && inpututil.IsKeyJustPressed(ebiten.KeyM) {
		g.mdViewerState = renderer.MarkdownViewerState{}
		g.renderer.SetLayoutDirty()
		g.renderer.ClearPaneCache()
		g.screenDirty = true
		return
	}

	// Cmd+Enter — send viewer content to a pane.
	if meta && inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		g.sendViewerToPane()
		return
	}

	// f — enter follow-link mode.
	if !meta && !shift && inpututil.IsKeyJustPressed(ebiten.KeyF) {
		hints := g.collectVisibleLinkHints()
		if len(hints) > 0 {
			g.mdViewerState.FollowMode = true
			g.mdViewerState.LinkHints = hints
			g.screenDirty = true
		}
		return
	}

	// Tab — switch between llms.txt and llms-full.txt when both are available.
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) && g.llmsShort != "" && g.llmsFull != "" {
		g.llmsViewingFull = !g.llmsViewingFull
		if g.llmsViewingFull {
			g.openMarkdownViewerWithContent(g.llmsFull, "llms-full.txt — "+g.llmsDomain)
		} else {
			g.openMarkdownViewerWithContent(g.llmsShort, "llms.txt — "+g.llmsDomain)
		}
		g.mdViewerState.HasAlt = true
		g.mdViewerState.IsLLMS = true
		g.mdViewerState.HistoryLen = len(g.llmsHistory)
		g.mdViewerState.ForwardLen = len(g.llmsForward)
		return
	}

	// Backspace or Shift+H — navigate back in llms.txt history.
	if g.mdViewerState.IsLLMS && len(g.llmsHistory) > 0 {
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) || (shift && inpututil.IsKeyJustPressed(ebiten.KeyH)) {
			g.llmsNavigateBack()
			return
		}
	}

	// Shift+L — navigate forward in llms.txt history.
	if g.mdViewerState.IsLLMS && len(g.llmsForward) > 0 {
		if shift && inpututil.IsKeyJustPressed(ebiten.KeyL) {
			g.llmsNavigateForward()
			return
		}
	}

	// Cmd+F or / — open search.
	if (meta && inpututil.IsKeyJustPressed(ebiten.KeyF)) || (!meta && inpututil.IsKeyJustPressed(ebiten.KeySlash)) {
		g.mdViewerState.SearchOpen = true
		g.mdViewerState.SearchQuery = ""
		g.mdViewerState.SearchMatches = nil
		g.mdViewerState.SearchIdx = -1
		g.screenDirty = true
		return
	}

	// n/N — jump to next/previous match (when matches exist from a previous search).
	if len(g.mdViewerState.SearchMatches) > 0 {
		if !meta && inpututil.IsKeyJustPressed(ebiten.KeyN) {
			if shift {
				g.mdViewerSearchPrev()
			} else {
				g.mdViewerSearchNext()
			}
			return
		}
	}

	rowH := g.mdViewerState.RowH
	if rowH == 0 {
		rowH = 16
	}

	// Keyboard scroll with key-repeat support.
	// Initial delay: 20 frames (~333ms at 60fps), repeat every 3 frames (~50ms).
	const repeatDelay = 20
	const repeatInterval = 3

	scrollKeys := []ebiten.Key{
		ebiten.KeyArrowUp, ebiten.KeyArrowDown,
		ebiten.KeyJ, ebiten.KeyK,
		ebiten.KeyPageUp, ebiten.KeyPageDown,
		ebiten.KeyHome, ebiten.KeyEnd,
	}

	for _, key := range scrollKeys {
		dur := inpututil.KeyPressDuration(key)
		if dur == 0 {
			continue
		}
		fire := dur == 1 || (dur >= repeatDelay && (dur-repeatDelay)%repeatInterval == 0)
		if !fire {
			continue
		}
		switch key {
		case ebiten.KeyArrowUp, ebiten.KeyK:
			g.mdViewerState.ScrollOffset -= rowH
		case ebiten.KeyArrowDown, ebiten.KeyJ:
			g.mdViewerState.ScrollOffset += rowH
		case ebiten.KeyPageUp:
			g.mdViewerState.ScrollOffset -= 10 * rowH
		case ebiten.KeyPageDown:
			g.mdViewerState.ScrollOffset += 10 * rowH
		case ebiten.KeyHome:
			g.mdViewerState.ScrollOffset = 0
		case ebiten.KeyEnd:
			g.mdViewerState.ScrollOffset = g.mdViewerState.MaxScroll
		}
		g.screenDirty = true
	}

	// Vim motions: gg (top), G (bottom), Ctrl+d (half-page down), Ctrl+u (half-page up).
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	if ctrl && inpututil.IsKeyJustPressed(ebiten.KeyD) {
		g.mdViewerState.ScrollOffset += 15 * rowH
		g.screenDirty = true
	}
	if ctrl && inpututil.IsKeyJustPressed(ebiten.KeyU) {
		g.mdViewerState.ScrollOffset -= 15 * rowH
		g.screenDirty = true
	}
	if !ctrl && inpututil.IsKeyJustPressed(ebiten.KeyG) {
		if shift {
			// Shift+G → bottom
			g.mdViewerState.ScrollOffset = g.mdViewerState.MaxScroll
			g.screenDirty = true
		} else {
			// gg detection: two 'g' presses within 500ms.
			now := time.Now()
			if now.Sub(g.mdViewerLastG) < 500*time.Millisecond {
				g.mdViewerState.ScrollOffset = 0
				g.screenDirty = true
				g.mdViewerLastG = time.Time{} // reset
			} else {
				g.mdViewerLastG = now
			}
		}
	}

	// Mouse wheel scroll.
	_, wy := ebiten.Wheel()
	if wy != 0 {
		g.mdViewerState.ScrollOffset -= int(wy * float64(rowH) * 3)
		g.screenDirty = true
	}

	g.clampMdViewerScroll()
}

// handleMarkdownSearchInput processes keyboard input while the search bar is active.
func (g *Game) handleMarkdownSearchInput() {
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)

	// ESC — close search bar (matches stay visible for n/N in normal mode).
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.mdViewerState.SearchOpen = false
		g.screenDirty = true
		return
	}

	// Enter/n — next match; Shift+Enter/N — previous match.
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyN) {
		if shift {
			g.mdViewerSearchPrev()
		} else {
			g.mdViewerSearchNext()
		}
		return
	}

	// Backspace — delete last character.
	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		if len(g.mdViewerState.SearchQuery) > 0 {
			runes := []rune(g.mdViewerState.SearchQuery)
			g.mdViewerState.SearchQuery = string(runes[:len(runes)-1])
			g.mdViewerUpdateSearch()
		}
		g.screenDirty = true
		return
	}

	// Text input — filter out n/N when navigating matches to avoid typing them.
	runes := ebiten.AppendInputChars(nil)
	if len(runes) > 0 {
		for _, r := range runes {
			// Skip 'n' and 'N' when they were consumed by match navigation above.
			if (r == 'n' || r == 'N') && len(g.mdViewerState.SearchMatches) > 0 {
				continue
			}
			if r >= 0x20 && r != 0x7f {
				g.mdViewerState.SearchQuery += string(r)
			}
		}
		g.mdViewerUpdateSearch()
		g.screenDirty = true
	}
}

// mdViewerUpdateSearch rebuilds the match list for the current search query.
func (g *Game) mdViewerUpdateSearch() {
	q := strings.ToLower(g.mdViewerState.SearchQuery)
	g.mdViewerState.SearchMatches = nil
	g.mdViewerState.SearchIdx = -1

	if q == "" {
		return
	}

	qLen := len([]rune(q))
	for lineIdx, line := range g.mdViewerState.Lines {
		// Concatenate span text to find matches across span boundaries.
		col := 0
		for _, span := range line.Spans {
			lower := strings.ToLower(span.Text)
			runes := []rune(lower)
			for j := 0; j <= len(runes)-qLen; j++ {
				if strings.ToLower(string(runes[j:j+qLen])) == q {
					g.mdViewerState.SearchMatches = append(g.mdViewerState.SearchMatches, renderer.SearchMatch{
						LineIdx: lineIdx,
						Col:     col + j,
						Len:     qLen,
					})
				}
			}
			col += len(runes)
		}
	}

	// Auto-scroll to first match.
	if len(g.mdViewerState.SearchMatches) > 0 {
		g.mdViewerState.SearchIdx = 0
		g.mdViewerScrollToMatch()
	}
}

// mdViewerSearchNext jumps to the next search match.
func (g *Game) mdViewerSearchNext() {
	if len(g.mdViewerState.SearchMatches) == 0 {
		return
	}
	g.mdViewerState.SearchIdx = (g.mdViewerState.SearchIdx + 1) % len(g.mdViewerState.SearchMatches)
	g.mdViewerScrollToMatch()
	g.screenDirty = true
}

// mdViewerSearchPrev jumps to the previous search match.
func (g *Game) mdViewerSearchPrev() {
	if len(g.mdViewerState.SearchMatches) == 0 {
		return
	}
	g.mdViewerState.SearchIdx--
	if g.mdViewerState.SearchIdx < 0 {
		g.mdViewerState.SearchIdx = len(g.mdViewerState.SearchMatches) - 1
	}
	g.mdViewerScrollToMatch()
	g.screenDirty = true
}

// mdViewerScrollToMatch scrolls the viewer so the current match is visible.
func (g *Game) mdViewerScrollToMatch() {
	if g.mdViewerState.SearchIdx < 0 || g.mdViewerState.SearchIdx >= len(g.mdViewerState.SearchMatches) {
		return
	}
	rowH := g.mdViewerState.RowH
	if rowH == 0 {
		rowH = 16
	}
	m := g.mdViewerState.SearchMatches[g.mdViewerState.SearchIdx]
	targetOffset := m.LineIdx * rowH
	g.mdViewerState.ScrollOffset = targetOffset
	g.clampMdViewerScroll()
}

// clampMdViewerScroll keeps the scroll offset within valid bounds.
func (g *Game) clampMdViewerScroll() {
	if g.mdViewerState.ScrollOffset < 0 {
		g.mdViewerState.ScrollOffset = 0
	}
	if g.mdViewerState.MaxScroll > 0 && g.mdViewerState.ScrollOffset > g.mdViewerState.MaxScroll {
		g.mdViewerState.ScrollOffset = g.mdViewerState.MaxScroll
	}
}

// spanToANSI converts a markdown span to ANSI-colored text for terminal display.
func spanToANSI(span markdown.Span) string {
	const reset = "\033[0m"
	switch span.Style {
	case markdown.StyleHeading1:
		return "\033[1;97m" + span.Text + reset // bold bright white
	case markdown.StyleHeading2:
		return "\033[1;36m" + span.Text + reset // bold cyan
	case markdown.StyleHeading3:
		return "\033[2;37m" + span.Text + reset // dim white
	case markdown.StyleBold:
		return "\033[1;97m" + span.Text + reset // bold bright white
	case markdown.StyleItalic:
		return "\033[3;37m" + span.Text + reset // italic dim
	case markdown.StyleInlineCode:
		return "\033[7;33m" + span.Text + reset // reverse yellow
	case markdown.StyleCodeBlock:
		return "\033[32m" + span.Text + reset // green
	case markdown.StyleLink:
		if span.Extra != "" {
			return "\033[4;36m" + span.Text + reset + "\033[2m (" + span.Extra + ")" + reset
		}
		return "\033[4;36m" + span.Text + reset // underline cyan
	case markdown.StyleBlockquote:
		return "\033[2;37m" + span.Text + reset // dim
	case markdown.StyleListItem:
		return "\033[36m" + span.Text + reset // cyan marker
	case markdown.StyleTableHeader:
		return "\033[1;97m" + span.Text + reset // bold white
	case markdown.StyleTableCell:
		return span.Text
	case markdown.StyleTableSeparator, markdown.StyleHRule:
		return "\033[2m────────────────────────────────────────" + reset
	case markdown.StyleStrikethrough:
		return "\033[9;2m" + span.Text + reset // strikethrough dim
	case markdown.StyleImage:
		return "\033[35m" + span.Text + reset // magenta
	case markdown.StyleCheckboxChecked:
		return "\033[32m" + span.Text + reset // green
	case markdown.StyleCheckboxUnchecked:
		return "\033[2m" + span.Text + reset // dim
	default:
		return span.Text
	}
}

// collectVisibleLinkHints scans visible lines for StyleLink spans and assigns
// letter badges (a-z). Returns at most 26 hints.
func (g *Game) collectVisibleLinkHints() []renderer.LinkHint {
	rowH := g.mdViewerState.RowH
	if rowH == 0 {
		rowH = 16
	}

	// Approximate visible area height from panel dimensions (80% of window height).
	physH := int(float64(g.winH) * g.dpi)
	visibleH := physH * 80 / 100

	var hints []renderer.LinkHint
	label := 'a'
	for lineIdx, line := range g.mdViewerState.Lines {
		lineY := lineIdx*rowH - g.mdViewerState.ScrollOffset
		// Only include lines visible in the content area.
		if lineY+rowH < 0 {
			continue
		}
		if lineY > visibleH {
			break // past visible area
		}

		for spanIdx, span := range line.Spans {
			if span.Style == markdown.StyleLink && span.Extra != "" {
				hints = append(hints, renderer.LinkHint{
					LineIdx: lineIdx,
					SpanIdx: spanIdx,
					URL:     span.Extra,
					Label:   label,
				})
				label++
				if label > 'z' {
					return hints
				}
			}
		}
	}
	return hints
}

// llmsPushHistory saves the current llms state onto the back stack.
func (g *Game) llmsPushHistory() {
	g.llmsHistory = append(g.llmsHistory, llmsHistoryEntry{
		Domain:       g.llmsDomain,
		Short:        g.llmsShort,
		Full:         g.llmsFull,
		ViewingFull:  g.llmsViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
}

// llmsNavigateBack pops from the back stack and pushes current to forward.
func (g *Game) llmsNavigateBack() {
	if len(g.llmsHistory) == 0 {
		return
	}
	// Push current to forward stack.
	g.llmsForward = append(g.llmsForward, llmsHistoryEntry{
		Domain:       g.llmsDomain,
		Short:        g.llmsShort,
		Full:         g.llmsFull,
		ViewingFull:  g.llmsViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
	// Pop from history.
	entry := g.llmsHistory[len(g.llmsHistory)-1]
	g.llmsHistory = g.llmsHistory[:len(g.llmsHistory)-1]
	g.llmsRestoreEntry(entry)
}

// llmsNavigateForward pops from the forward stack and pushes current to back.
func (g *Game) llmsNavigateForward() {
	if len(g.llmsForward) == 0 {
		return
	}
	// Push current to back stack.
	g.llmsHistory = append(g.llmsHistory, llmsHistoryEntry{
		Domain:       g.llmsDomain,
		Short:        g.llmsShort,
		Full:         g.llmsFull,
		ViewingFull:  g.llmsViewingFull,
		ScrollOffset: g.mdViewerState.ScrollOffset,
	})
	// Pop from forward.
	entry := g.llmsForward[len(g.llmsForward)-1]
	g.llmsForward = g.llmsForward[:len(g.llmsForward)-1]
	g.llmsRestoreEntry(entry)
}

// llmsRestoreEntry restores the viewer to a history entry's state.
func (g *Game) llmsRestoreEntry(entry llmsHistoryEntry) {
	g.llmsDomain = entry.Domain
	g.llmsShort = entry.Short
	g.llmsFull = entry.Full
	g.llmsViewingFull = entry.ViewingFull

	content := entry.Short
	title := "llms.txt — " + entry.Domain
	if entry.ViewingFull {
		content = entry.Full
		title = "llms-full.txt — " + entry.Domain
	}
	g.openMarkdownViewerWithContent(content, title)
	g.mdViewerState.HasAlt = entry.Short != "" && entry.Full != ""
	g.mdViewerState.IsLLMS = true
	g.mdViewerState.ScrollOffset = entry.ScrollOffset
	g.mdViewerState.HistoryLen = len(g.llmsHistory)
	g.mdViewerState.ForwardLen = len(g.llmsForward)
}

// llmsFollowLink handles following a link from the markdown viewer.
// If the URL looks like an llms.txt-capable domain, fetch it; otherwise open in browser.
func (g *Game) llmsFollowLink(url string) {
	// Check if this is an HTTP(S) URL we can try to fetch llms.txt from.
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		// Push current state to history and clear forward stack.
		g.llmsPushHistory()
		g.llmsForward = nil
		g.startLLMSFetch(url)
		return
	}
	// Non-HTTP URL or relative path — open in system browser.
	exec.Command("open", url).Start() // #nosec G204 — user-initiated URL open
}

// sendViewerToPane writes the current viewer content to a temp file and opens
// it in a new pane running `less`.
func (g *Game) sendViewerToPane() {
	if !g.mdViewerState.Open {
		return
	}

	// Capture state before clearing.
	lines := g.mdViewerState.Lines
	paneName := g.mdViewerState.Title
	if g.llmsDomain != "" {
		paneName = "llms — " + g.llmsDomain
	}

	// Build ANSI-colored text from styled lines for rich rendering in less -R.
	var buf strings.Builder
	for _, line := range lines {
		// Indent.
		for i := 0; i < line.Indent; i++ {
			buf.WriteByte(' ')
		}
		for _, span := range line.Spans {
			buf.WriteString(spanToANSI(span))
		}
		// Heading underlines.
		if len(line.Spans) > 0 {
			switch line.Spans[0].Style {
			case markdown.StyleHeading1:
				textLen := 0
				for _, s := range line.Spans {
					textLen += len([]rune(s.Text))
				}
				buf.WriteByte('\n')
				buf.WriteString("\033[97m")
				buf.WriteString(strings.Repeat("━", textLen))
				buf.WriteString("\033[0m")
			case markdown.StyleHeading2:
				textLen := 0
				for _, s := range line.Spans {
					textLen += len([]rune(s.Text))
				}
				buf.WriteByte('\n')
				buf.WriteString("\033[36m")
				buf.WriteString(strings.Repeat("─", textLen))
				buf.WriteString("\033[0m")
			}
		}
		buf.WriteByte('\n')
	}

	// Write to temp file.
	tmpFile, err := os.CreateTemp("", "zurm-llms-*.md")
	if err != nil {
		g.flashStatus("Failed to create temp file: " + err.Error())
		return
	}
	if _, err := tmpFile.WriteString(buf.String()); err != nil {
		tmpFile.Close()
		g.flashStatus("Failed to write temp file: " + err.Error())
		return
	}
	tmpFile.Close()

	// Close the viewer.
	g.mdViewerState = renderer.MarkdownViewerState{}
	g.renderer.SetLayoutDirty()
	g.renderer.ClearPaneCache()

	// Create a new tab with a pane running `less -R <tmpfile>`.
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	term := terminal.New(g.cfg)
	term.StartCmd("less", []string{"-R", tmpFile.Name()}, "")
	p := &pane.Pane{
		Term:       term,
		Rect:       paneRect,
		Cols:       (paneRect.Dx() - g.cfg.Window.Padding*2) / g.font.CellW,
		Rows:       (paneRect.Dy() - g.cfg.Window.Padding*2) / g.font.CellH,
		CustomName: paneName,
	}

	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	term.Resize(p.Cols, p.Rows)

	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   p.CustomName,
	}
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
	g.screenDirty = true
}

// fetchSessions connects to zurm-server and returns the list of active sessions.
func fetchSessions(addr string) ([]zserver.SessionInfo, error) {
	conn, err := net.Dial("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to zurm-server at %s: %w", addr, err)
	}
	defer conn.Close()

	if err := zserver.WriteMessage(conn, zserver.MsgListSessions, nil); err != nil {
		return nil, fmt.Errorf("send list request: %w", err)
	}

	msg, err := zserver.ReadMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if msg.Type != zserver.MsgSessionList {
		return nil, fmt.Errorf("unexpected response type 0x%02x", msg.Type)
	}

	var sessions []zserver.SessionInfo
	if len(msg.Payload) > 0 {
		if err := json.Unmarshal(msg.Payload, &sessions); err != nil {
			return nil, fmt.Errorf("decode session list: %w", err)
		}
	}
	return sessions, nil
}

// resolveSessionPrefix matches a short prefix (like Docker short IDs) against
// active server sessions. Returns the full ID or an error if zero or multiple
// sessions match.
func resolveSessionPrefix(addr, prefix string) (string, error) {
	sessions, err := fetchSessions(addr)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, s := range sessions {
		if len(s.ID) >= len(prefix) && s.ID[:len(prefix)] == prefix {
			matches = append(matches, s.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matching prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches %d sessions: %v", prefix, len(matches), matches)
	}
}

// runListSessions connects to zurm-server, fetches the session list, prints a
// table to stdout, and returns. Called by the --list-sessions / -ls flag before
// the GUI starts.
func runListSessions() error {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load warning: %v (using defaults)", err)
	}

	addr := zserver.ResolveSocket(cfg.Server.Address)
	sessions, err := fetchSessions(addr)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		fmt.Println("No active server sessions.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPID\tSIZE\tDIR")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%d\t%dx%d\t%s\n", s.ID, s.PID, s.Cols, s.Rows, s.Dir)
	}
	w.Flush()
	return nil
}

// attachServerSession connects to zurm-server, lists active sessions, and
// populates the command palette with an entry per session. Selecting an entry
// opens a new server-backed tab attached to that session.
// Called from the "Attach to Server Session" palette action.
func (g *Game) attachServerSession() {
	addr := zserver.ResolveSocket(g.cfg.Server.Address)
	sessions, err := fetchSessions(addr)
	if err != nil {
		g.flashStatus("zurm-server unreachable")
		return
	}

	if len(sessions) == 0 {
		g.flashStatus("No active server sessions")
		return
	}

	// Append per-session palette entries. The base palette is restored on the
	// next buildPalette call (theme switch, config reload, etc.).
	for _, s := range sessions {
		si := s // capture for closure
		label := fmt.Sprintf("Attach: %s (pid %d, %dx%d, %s)", si.ID, si.PID, si.Cols, si.Rows, si.Dir)
		g.paletteEntries = append(g.paletteEntries, renderer.PaletteEntry{Name: label})
		g.paletteActions = append(g.paletteActions, func() {
			g.openServerTabForSession(si.ID)
		})
	}

	// Open palette pre-filtered to the injected entries.
	g.paletteState.Open = true
	g.paletteState.Query = "Attach: "
	g.screenDirty = true
}

// openServerTabForSession opens a new tab backed by an existing zurm-server
// session identified by sessionID.
func (g *Game) openServerTabForSession(sessionID string) {
	physW := int(float64(g.winW) * g.dpi)
	physH := int(float64(g.winH) * g.dpi)
	tabBarH := g.renderer.TabBarHeight()
	statusBarH := g.renderer.StatusBarHeight()
	paneRect := image.Rect(0, tabBarH, physW, physH-statusBarH)

	p, err := pane.NewServer(g.cfg, paneRect, g.font.CellW, g.font.CellH, "", sessionID)
	if err != nil {
		g.flashStatus("Attach failed: " + err.Error())
		return
	}
	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   fmt.Sprintf("tab %d", len(g.tabs)+1),
	}
	g.tabs = append(g.tabs, t)
	g.switchTab(len(g.tabs) - 1)
}
