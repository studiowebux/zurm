package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // #nosec G108 — opt-in via config, localhost-only
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	// Root context — cancelled on app exit. Derived contexts scope goroutines
	// to their parent lifecycle (fetch, search, clipboard, etc.).
	ctx    context.Context
	cancel context.CancelFunc

	tabMgr *TabManager

	// layout and focused are computed on-demand from tabMgr via activeLayout()/activeFocused().
	// No cached pointers — eliminates the sync invariant entirely.

	renderer *renderer.Renderer
	font     *renderer.FontRenderer
	cfg      *config.Config

	winW, winH int

	// zoomed is true when the focused pane is temporarily fullscreened (Cmd+Z).
	zoomed bool

	// Input tracking — edge detection, drag, click, scroll, mouse state.
	input inputTracker

	// dpi is the device pixel ratio (2.0 on Retina).
	dpi float64

	// prevFocused tracks window focus state for mode-1004 focus events.
	prevFocused bool

	// Idle suspension — reduce TPS when window is unfocused to save CPU/memory.
	unfocusedAt time.Time // when focus was lost; zero = focused
	suspended   bool      // true when TPS has been reduced

	// URL hover state — detected URLs in the focused pane's visible buffer.
	urlHover urlHoverState

	// Context menu state.
	menuState renderer.MenuState

	// Keybinding overlay state.
	overlayState renderer.OverlayState

	// Close-confirmation dialog state.
	confirmState         renderer.ConfirmState
	confirmPendingAction func()

	// In-buffer search controller (Cmd+F).
	search *SearchController

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

	// Key repeat state for all text input fields and tab search navigation.
	repeats inputRepeats

	// Command palette controller (Cmd+P).
	palette *PaletteController

	// File explorer controller (Cmd+E).
	explorer *ExplorerController

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
	rec recState

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

	// llms.txt browser state (Cmd+L).
	llms llmsState


	// Command vault — encrypted history with ghost text suggestions.
	vlt vaultState

}

// buildRenderConfig extracts the subset of config the renderer needs,
// decoupling the renderer package from the config package.
func buildRenderConfig(cfg *config.Config) *renderer.RenderConfig {
	return &renderer.RenderConfig{
		Colors: renderer.RenderColorConfig{
			Background:    cfg.Colors.Background,
			Foreground:    cfg.Colors.Foreground,
			Cursor:        cfg.Colors.Cursor,
			Border:        cfg.Colors.Border,
			BrightBlack:   cfg.Colors.BrightBlack,
			BrightMagenta: cfg.Colors.BrightMagenta,
			Yellow:        cfg.Colors.Yellow,
			Red:           cfg.Colors.Red,
			Blue:          cfg.Colors.Blue,
			Cyan:          cfg.Colors.Cyan,
			Separator:     cfg.Colors.Separator,
			MdBold:        cfg.Colors.MdBold,
			MdHeading:     cfg.Colors.MdHeading,
			MdCode:        cfg.Colors.MdCode,
			MdCodeBorder:  cfg.Colors.MdCodeBorder,
			MdTableBorder: cfg.Colors.MdTableBorder,
			MdMatchBg:     cfg.Colors.MdMatchBg,
			MdMatchCurBg:  cfg.Colors.MdMatchCurBg,
			MdBadgeBg:     cfg.Colors.MdBadgeBg,
			MdBadgeFg:     cfg.Colors.MdBadgeFg,
		},
		Window: renderer.RenderWindowConfig{
			Padding: cfg.Window.Padding,
		},
		Tabs: renderer.RenderTabsConfig{
			MaxWidthChars: cfg.Tabs.MaxWidthChars,
		},
		StatusBar: renderer.RenderStatusBarConfig{
			Enabled:           cfg.StatusBar.Enabled,
			PaddingPx:         cfg.StatusBar.PaddingPx,
			SeparatorHeightPx: cfg.StatusBar.SeparatorHeightPx,
			ShowClock:         cfg.StatusBar.ShowClock,
			ShowGit:           cfg.StatusBar.ShowGit,
			BranchPrefix:      cfg.StatusBar.BranchPrefix,
			ShowCommit:        cfg.StatusBar.ShowCommit,
			ShowDirty:         cfg.StatusBar.ShowDirty,
			ShowAheadBehind:   cfg.StatusBar.ShowAheadBehind,
			ShowProcess:       cfg.StatusBar.ShowProcess,
			ShowCwd:           cfg.StatusBar.ShowCwd,
			SegmentSeparator:  cfg.StatusBar.SegmentSeparator,
		},
		Blocks: renderer.RenderBlocksConfig{
			Enabled:      cfg.Blocks.Enabled,
			ShowDuration: cfg.Blocks.ShowDuration,
			BorderWidth:  cfg.Blocks.BorderWidth,
			BorderColor:  cfg.Blocks.BorderColor,
			SuccessColor: cfg.Blocks.SuccessColor,
			FailColor:    cfg.Blocks.FailColor,
			ShowBorder:   cfg.Blocks.ShowBorder,
			BgColor:      cfg.Blocks.BgColor,
			BgAlpha:      cfg.Blocks.BgAlpha,
		},
		Bell: renderer.RenderBellConfig{
			Color: cfg.Bell.Color,
		},
		Vault: renderer.RenderVaultConfig{
			SuggestionColor: cfg.Vault.SuggestionColor,
		},
	}
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
	rend := renderer.NewRenderer(fontR, buildRenderConfig(cfg))
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

	ctx, cancel := context.WithCancel(context.Background())
	game := &Game{
		ctx:              ctx,
		cancel:           cancel,
		renderer:         rend,
		font:             fontR,
		cfg:              cfg,
		winW:             logW,
		winH:             logH,
		dpi:              dpi,
		input: inputTracker{
			PrevKeys:         make(map[ebiten.Key]bool),
			PrevMouseButtons: make(map[ebiten.MouseButton]bool),
			MouseHeldBtn:     -1,
		},
		blocksEnabled:    cfg.Blocks.Enabled,
		rec: recState{
			Recorder: recorder.New(winW, winH),
			Done:     make(chan string, 1),
		},
		screenshotDone:   make(chan string, 1),
		tabHoverState:    renderer.TabHoverState{TabIdx: -1},
		tabMgr:           NewTabManager(),
		explorer:         NewExplorerController(),
		poller:           NewStatusPoller(),
		search:           NewSearchController(),
		palette:          NewPaletteController(),
		clipboardCh:      make(chan string, 1),
	}

	game.tabMgr.Tabs = initialTabs
	game.tabMgr.ActiveIdx = initialActive
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
		game.vlt.Vault = vault.Init(config.ConfigDir(), histPath, cfg.Vault.IgnorePrefix, cfg.Vault.MaxEntries, syncInterval)
	}

	// Seed focus history with the initial tab so Cmd+; can return to it.
	game.tabMgr.FocusHistory = []focusEntry{{tabIdx: initialActive, pane: initialTabs[initialActive].Focused}}
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
	if game.vlt.Vault != nil {
		if err := game.vlt.Vault.Save(); err != nil {
			log.Printf("vault save: %v", err)
		}
	}
}

// Update is called at 60 TPS by Ebitengine.
func (g *Game) Update() error {
	if len(g.tabMgr.Tabs) == 0 {
		g.cancel()
		g.saveSession()
		return ebiten.Termination
	}

	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	// Intercept window close button (red X) and Cmd+Q — show confirmation.
	wantQuit := (meta || ctrl) && ebiten.IsKeyPressed(ebiten.KeyQ)
	if (wantQuit || ebiten.IsWindowBeingClosed()) && !g.confirmState.Open {
		g.showConfirm("Quit zurm? All sessions will be closed.", func() {
			g.cancel()
			g.saveSession()
			for _, t := range g.tabMgr.Tabs {
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
	for _, leaf := range g.activeLayout().Leaves() {
		if !leaf.Pane.BellUntil.IsZero() {
			if now.After(leaf.Pane.BellUntil) {
				leaf.Pane.BellUntil = time.Time{}
			}
			g.screenDirty = true
		}
	}

	// Drain recording-done channel (background goroutine sends flash message).
	select {
	case msg := <-g.rec.Done:
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
	if g.rec.Recorder.Active() {
		g.statusBarState.Recording = true
		g.statusBarState.RecordingMode = g.rec.Recorder.OutputMode()
		now := time.Now()
		if now.Unix() != g.rec.LastStatusSec {
			g.rec.LastStatusSec = now.Unix()
			g.statusBarState.RecordingDuration = now.Sub(g.rec.Recorder.StartTime())
			g.statusBarState.RecordingBytes = g.rec.Recorder.OutputSize()
			g.screenDirty = true
		}
	} else if g.statusBarState.Recording {
		g.statusBarState.Recording = false
		g.screenDirty = true
	}

	// Update cursor blink for all panes in the active tab.
	// Update() returns true when blinkOn toggles — mark dirty so the frame redraws.
	for _, leaf := range g.activeLayout().Leaves() {
		if leaf.Pane.Term.Cursor.Update() {
			g.screenDirty = true
			// Bump per-pane gen so the pane-skip logic redraws the cursor row.
			leaf.Pane.Term.Buf.BumpRenderGen()
		}
	}

	// Check background tabs for PTY activity (sets HasActivity indicator).
	for i, t := range g.tabMgr.Tabs {
		if i != g.tabMgr.ActiveIdx {
			had := t.HasActivity
			t.CheckActivity()
			if t.HasActivity != had {
				g.screenDirty = true
			}
		}
	}

	// Check for dead panes (non-blocking). Close at most one per frame to avoid
	// Close at most one per frame to keep the layout consistent.
	closedDead := false
	for _, leaf := range g.activeLayout().Leaves() {
		if closedDead {
			break
		}
		select {
		case <-leaf.Pane.Term.Dead():
			if len(g.activeLayout().Leaves()) <= 1 {
				g.closeActiveTab()
			} else {
				g.closePane(leaf.Pane)
			}
			closedDead = true // layout changed — remaining leaves are stale
		default:
		}
	}
	if len(g.tabMgr.Tabs) == 0 {
		return ebiten.Termination
	}

	g.handleMouse()
	g.handleInput()
	if len(g.tabMgr.Tabs) == 0 {
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
	if g.activeFocused() != nil {
		if g.search.Recompute(g.activeFocused().Term.Buf) {
			g.screenDirty = true
		}
	}

	for _, leaf := range g.activeLayout().Leaves() {
		leaf.Pane.Term.SendCPRResponse()
		leaf.Pane.Term.SendDA1Response()
		leaf.Pane.Term.SendDA2Response()
		leaf.Pane.Term.SendPendingResponses()
		leaf.Pane.Term.SyncCursorStyle()
	}
	if g.activeFocused() != nil {
		g.activeFocused().Term.SendClipboardResponses()
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
	if g.rec.Recorder.Active() {
		now := time.Now()
		if now.Sub(g.rec.LastFrame) >= recorder.FrameDuration {
			g.rec.LastFrame = now
			needed := screen.Bounds().Dx() * screen.Bounds().Dy() * 4
			if len(g.rec.Buf) != needed {
				g.rec.Buf = make([]byte, needed)
			}
			screen.ReadPixels(g.rec.Buf)
			frame := make([]byte, needed)
			copy(frame, g.rec.Buf)
			g.rec.Recorder.AddFrame(frame)
		}
	}

	if !g.needsRender() {
		return
	}
	// Sync transient status bar fields from live game state before rendering.
	g.statusBarState.Zoomed = g.zoomed
	g.statusBarState.PinMode = g.tabMgr.PinMode
	g.statusBarState.BlocksEnabled = g.blocksEnabled
	g.statusBarState.ServerSession = g.activeFocused() != nil && g.activeFocused().ServerSessionID != ""
	var srvCount int
	for _, t := range g.tabMgr.Tabs {
		for _, leaf := range t.Layout.Leaves() {
			if leaf.Pane.ServerSessionID != "" {
				srvCount++
			}
		}
	}
	g.statusBarState.ServerSessionCount = srvCount
	if g.activeFocused() != nil {
		g.activeFocused().Term.Buf.RLock()
		g.statusBarState.ScrollOffset = g.activeFocused().Term.Buf.ViewOffset
		if g.blocksEnabled {
			g.statusBarState.BlockCount = len(g.activeFocused().Term.Buf.Blocks)
		}
		g.activeFocused().Term.Buf.RUnlock()
	}
	// Snapshot renderSeq BEFORE DrawAll so that any PTY output arriving
	// during rendering (e.g. shell responds to resize) bumps the seq above
	// our snapshot and triggers a redraw on the next frame.
	g.lastPtySeq = terminal.RenderSeq()

	g.renderer.HoveredURL = g.urlHover.HoveredURL
	g.renderer.VaultSuggestion = g.vlt.Suggest
	if g.tabMgr.ActiveIdx >= 0 && g.tabMgr.ActiveIdx < len(g.tabMgr.Tabs) {
		g.statusBarState.TabNote = g.tabMgr.Tabs[g.tabMgr.ActiveIdx].Note
	}
	// Hint mode: show tab number badges when Cmd is held and no modal is active.
	hintMode := ebiten.IsKeyPressed(ebiten.KeyMeta) &&
		!g.overlayState.Open && !g.palette.State.Open && !g.confirmState.Open &&
		!g.mdViewerState.Open && !g.llms.URLInput.Open && !g.tabSwitcherState.Open &&
		!g.tabSearchState.Open && !g.search.State.Open &&
		!g.menuState.Open && !g.explorer.State.Open
	g.renderer.DrawAll(renderer.DrawState{
		Screen:         screen,
		Tabs:           g.tabMgr.Tabs,
		ActiveTab:      g.tabMgr.ActiveIdx,
		Focused:        g.activeFocused(),
		Zoomed:         g.zoomed,
		Menu:           &g.menuState,
		Overlay:        &g.overlayState,
		Confirm:        &g.confirmState,
		Search:         &g.search.State,
		StatusBar:      &g.statusBarState,
		TabSwitcher:    &g.tabSwitcherState,
		Palette:        &g.palette.State,
		PaletteEntries: g.palette.Entries,
		FileExplorer:   &g.explorer.State,
		TabSearch:      &g.tabSearchState,
		Stats:          &g.statsState,
		TabHover:       &g.tabHoverState,
		MdViewer:       &g.mdViewerState,
		URLInput:       &g.llms.URLInput,
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
		ctx := g.ctx
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
			case <-ctx.Done():
			}
		}()
	}
}

// Layout returns the physical screen size for HiDPI rendering.
func (g *Game) Layout(outsideW, outsideH int) (int, int) {
	return int(float64(outsideW) * g.dpi), int(float64(outsideH) * g.dpi)
}

// activeLayout returns the layout tree of the active tab, or nil if no tabs exist.
func (g *Game) activeLayout() *pane.LayoutNode {
	if t := g.tabMgr.Active(); t != nil {
		return t.Layout
	}
	return nil
}

// activeFocused returns the focused pane of the active tab, or nil if no tabs exist.
func (g *Game) activeFocused() *pane.Pane {
	if t := g.tabMgr.Active(); t != nil {
		return t.Focused
	}
	return nil
}

// setActiveLayout writes a new layout to the active tab.
func (g *Game) setActiveLayout(n *pane.LayoutNode) {
	if t := g.tabMgr.Active(); t != nil {
		t.Layout = n
	}
}

// setActiveFocused writes the focused pane to the active tab.
func (g *Game) setActiveFocused(p *pane.Pane) {
	if t := g.tabMgr.Active(); t != nil {
		t.Focused = p
	}
}

// syncActive is a no-op — layout and focused are read from the active tab on demand.
// Retained for test compatibility; callers should use activeLayout()/activeFocused() directly.
func (g *Game) syncActive() {}

// updateLayout writes a new layout to the active tab.
func (g *Game) updateLayout(n *pane.LayoutNode) {
	g.setActiveLayout(n)
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
		_ = os.Setenv("LANG", "en_US.UTF-8")
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
		_ = os.Setenv("PATH", currentPath+":"+strings.Join(added, ":"))
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






