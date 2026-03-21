// Package renderer — state.go consolidates all UI overlay state types.
// These types are stored on Game (main package) and passed to DrawAll.
// Grouped here for discoverability; rendering logic lives in dedicated files.
package renderer

import (
	"image"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/fileexplorer"
	"github.com/studiowebux/zurm/help"
	"github.com/studiowebux/zurm/markdown"
	"github.com/studiowebux/zurm/terminal"
)

// --- In-buffer search (Cmd+F) ---

// SearchState holds the live state of the in-buffer search (Cmd+F).
type SearchState struct {
	Open      bool
	Query     string
	CursorPos int // rune index of the text cursor within Query
	Matches   []terminal.SearchMatch
	Current   int // index of the active (highlighted) match
}

// --- Command palette (Cmd+P) ---

// PaletteEntry is a single command shown in the palette.
// Actions are not stored here — main.go holds a parallel []func() slice.
type PaletteEntry struct {
	Name     string
	Shortcut string
}

// PaletteState is the rendering + interaction state for the command palette.
type PaletteState struct {
	Open      bool
	Query     string
	CursorPos int // rune index of the text cursor within Query
	Cursor    int // index into the filtered list
}

// --- Context menu ---

// MenuState holds the full rendering and hit-test state for a context menu.
// Stored on Game; passed by pointer to DrawAll.
type MenuState struct {
	Open     bool
	Items    []help.MenuItem
	Rect     image.Rectangle // bounding rect of main menu (physical px)
	HoverIdx int             // -1 = none

	SubOpen      bool
	SubItems     []help.MenuItem
	SubRect      image.Rectangle
	SubHoverIdx  int
	SubParentIdx int // index in Items that owns this submenu
}

// --- Keybinding overlay ---

// OverlayState holds the rendering state for the keybinding overlay.
type OverlayState struct {
	Open            bool
	SearchQuery     string
	SearchCursorPos int // rune index of the text cursor within SearchQuery
	ScrollOffset    int
	MaxScroll       int // written by renderer each frame
	RowH            int // written by renderer each frame
}

// --- Confirm dialog ---

// ConfirmState holds the rendering state for the close-confirmation dialog.
type ConfirmState struct {
	Open    bool
	Message string // e.g. "Close pane?" or "Close tab?"
}

// --- URL input ---

// URLInputState holds the rendering state for the llms.txt URL input overlay.
type URLInputState struct {
	Open      bool
	Query     string
	CursorPos int // rune index of the text cursor within Query
	Loading   bool
}

// --- Markdown viewer ---

// SearchMatch stores the position of a search hit within a styled line.
type SearchMatch struct {
	LineIdx int // which StyledLine
	Col     int // rune offset within the concatenated line text
	Len     int // rune length of the match
}

// LinkHint is a follow-mode badge mapping a letter to a link span.
type LinkHint struct {
	LineIdx int
	SpanIdx int
	URL     string
	Label   rune
}

// MarkdownViewerState holds the rendering state for the markdown reader overlay.
type MarkdownViewerState struct {
	Open         bool
	Title        string
	Lines        []markdown.StyledLine
	ScrollOffset int
	MaxScroll    int // written by renderer each frame
	RowH         int // written by renderer each frame

	// Search state (Cmd+F / /).
	SearchOpen      bool
	SearchQuery     string
	SearchCursorPos int // rune index of the text cursor within SearchQuery
	SearchMatches   []SearchMatch // matches with position info
	SearchIdx       int           // current match index (-1 = none)

	// HasAlt is true when an alternate view is available (e.g. llms.txt ↔ llms-full.txt).
	// When set, the hint bar shows "Tab switch".
	HasAlt bool

	// Follow-link mode (f key).
	FollowMode bool
	LinkHints  []LinkHint

	// IsLLMS marks this viewer as browsing llms.txt content (enables history nav).
	IsLLMS bool

	// HistoryLen / ForwardLen control hint bar display of back/forward availability.
	HistoryLen int
	ForwardLen int
}

// --- Status bar ---

// StatusBarState holds the data the renderer needs to draw one frame of the bar.
type StatusBarState struct {
	Cwd             string
	GitBranch       string
	GitCommit       string // short commit hash (7 chars)
	GitDirty        int    // modified file count
	GitStaged       int    // staged file count
	GitAhead        int    // commits ahead of upstream
	GitBehind       int    // commits behind upstream
	ForegroundProc  string          // foreground process name, "" when shell is foreground
	ScrollOffset    int             // buf.ViewOffset of the focused pane; 0 = live output
	Zoomed          bool            // true when a pane is fullscreened via Cmd+Z
	PinMode         bool            // true while waiting for a pin slot keypress
	HelpBtnRect     image.Rectangle // set during draw; used by main.go for click detection
	FlashMessage    string          // transient message shown in place of cwd; cleared by Game.Update
	BlocksEnabled   bool            // show block indicator when true
	BlockCount      int             // number of completed blocks in focused pane
	Recording         bool          // true while a screen recording is in progress
	RecordingDuration time.Duration // elapsed recording time
	RecordingBytes    int64         // output MP4 file size on disk
	RecordingMode     string        // "MP4"
	Version           string        // app version, e.g. "v0.4.1"
	TabNote           string        // active tab's annotation, shown as a middle segment
	ServerSession      bool           // true when the focused pane is backed by zurm-server
	ServerSessionCount int           // number of open panes backed by zurm-server; 0 in local mode
}

// --- Tab switcher (Cmd+Space) ---

// TabSwitcherState holds the rendering and interaction state for the tab switcher overlay.
type TabSwitcherState struct {
	Open   bool
	Cursor int // index of the highlighted row
}

// --- Tab search (Cmd+J) ---

// TabSearchState holds rendering and interaction state for the Cmd+J tab search overlay.
type TabSearchState struct {
	Open      bool
	Query     string
	CursorPos int // rune index of the text cursor within Query
	Cursor    int // index into the filtered list
}

// --- Tab hover minimap ---

// TabHoverState tracks the popover state for tab hover minimap preview.
type TabHoverState struct {
	Active     bool
	TabIdx     int       // hovered tab index (-1 = none)
	HoverStart time.Time // when cursor entered this tab

	PopoverX int // physical pixel position
	PopoverY int
	PopoverW int // physical pixel size (after DPI scaling)
	PopoverH int

	Thumbnail *ebiten.Image // cached scaled snapshot
	CacheKey  uint64        // sum of RenderGen for invalidation
}

// --- Stats overlay ---

// StatsState holds runtime metrics for the stats overlay.
// Populated by the game loop on a 1-second timer.
type StatsState struct {
	Open bool

	// Ebitengine
	TPS float64
	FPS float64

	// Go runtime
	Goroutines int
	HeapAlloc  uint64 // bytes
	HeapSys    uint64 // bytes
	GCPauseNs  uint64 // last GC pause in nanoseconds
	NumGC      uint32

	// App
	TabCount  int
	PaneCount int
	BufRows   int // focused pane rows
	BufCols   int // focused pane cols
	Scrollback int // focused pane scrollback depth
}

// --- File explorer (Cmd+E) ---

// ExplorerMode controls what the file explorer input box is doing.
type ExplorerMode int

const (
	ExplorerModeNormal  ExplorerMode = iota
	ExplorerModeRename               // waiting for new name
	ExplorerModeNewFile              // waiting for new file name
	ExplorerModeNewDir               // waiting for new dir name
)

// FileExplorerState holds all rendering and interaction state for the file explorer sidebar.
type FileExplorerState struct {
	Open         bool
	Root         string
	Entries      []fileexplorer.Entry
	Cursor       int
	ScrollOffset int
	MaxScroll    int // written by renderer each frame
	RowH         int // written by renderer each frame

	Mode            ExplorerMode
	InputText       string
	InputLabel      string
	InputCursorPos  int // rune index of text cursor within InputText

	Clipboard *fileexplorer.Clipboard

	ConfirmOpen   bool
	ConfirmMsg    string
	ConfirmAction func()

	StatusMsg   string
	StatusTimer int

	Side string // "left" or "right"

	// Search/filter functionality
	SearchQuery     string
	SearchCursorPos int                   // rune index of text cursor within SearchQuery
	SearchMode      bool                  // true when search input is active
	FilteredIndices []int                 // indices of entries matching search
	SearchResults   []fileexplorer.Entry  // Full recursive search results
}

// --- Block hover ---

// BlockHoverState describes the block currently under the cursor.
type BlockHoverState struct {
	Active    bool
	Buf       *terminal.ScreenBuffer // the pane's buffer (for TextRange)
	AbsStart  int                    // AbsPromptRow of the hovered block
	AbsCmdRow int                    // AbsCmdRow of the hovered block
	AbsEnd    int                    // AbsEndRow of the hovered block
	CmdCol    int                    // column where user input starts (from OSC 133;B); -1 if unknown
	CopyTarget CopyTarget            // which copy button is under cursor
	CmdRect   image.Rectangle        // hit rect of "cmd" button
	OutRect   image.Rectangle        // hit rect of "out" button
	AllRect   image.Rectangle        // hit rect of "all" button
	blockIdx  int                    // index in buf.Blocks (-1 = activeBlock)
}
