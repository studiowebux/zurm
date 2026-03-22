package terminal

import (
	"image/color"
	"sync"
	"sync/atomic"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Cell represents a single character cell in the terminal grid.
// Width encodes display width: 1 = normal, 2 = wide (CJK/emoji first cell),
// 0 = continuation (second cell of a wide character).
type Cell struct {
	Char      rune
	Width     uint8 // 0=continuation, 1=normal, 2=wide
	FG        color.RGBA
	BG        color.RGBA
	Bold      bool
	Italic    bool
	Underline bool
	Inverse   bool
	Strikethrough    bool
	UnderlineColor   color.RGBA // SGR 58
	HasUnderlineColor bool      // true when SGR 58 is active
}

// SGRState holds the current SGR (Select Graphic Rendition) attributes.
type SGRState struct {
	FG        color.RGBA
	BG        color.RGBA
	Bold      bool
	Italic    bool
	Underline bool
	Inverse   bool
	Strikethrough    bool
	UnderlineColor   color.RGBA
	HasUnderlineColor bool
}

func (s SGRState) toCell(ch rune) Cell {
	return Cell{
		Char:              ch,
		Width:             1,
		FG:                s.FG,
		BG:                s.BG,
		Bold:              s.Bold,
		Italic:            s.Italic,
		Underline:         s.Underline,
		Inverse:           s.Inverse,
		Strikethrough:     s.Strikethrough,
		UnderlineColor:    s.UnderlineColor,
		HasUnderlineColor: s.HasUnderlineColor,
	}
}

// ScreenBuffer is the terminal character grid.
// Access is protected by RWMutex: the PTY reader goroutine writes,
// the Ebitengine render loop reads.
//
// Pattern: Reader/Writer lock on shared mutable state.
type ScreenBuffer struct {
	mu sync.RWMutex

	Cells     [][]Cell
	Rows      int
	Cols      int
	CursorRow int
	CursorCol int

	// Scroll region (inclusive, 0-indexed).
	ScrollTop    int
	ScrollBottom int

	// Default colors used for blank cells.
	DefaultFG color.RGBA
	DefaultBG color.RGBA

	// Current SGR state.
	SGR SGRState

	// Alternate screen support.
	altActive        bool
	altCells         [][]Cell
	altWrapped       []bool
	altCursorRow     int
	altCursorCol     int
	savedCursorRow   int
	savedCursorCol   int
	savedScrollTop   int
	savedScrollBot   int

	// 16-color ANSI palette.
	Palette [16]color.RGBA

	// Dirty tracking: which rows changed since last render.
	dirty []bool

	// Wrapped tracks whether each row was produced by soft-wrap (AutoWrap).
	// true = this row continues from the previous row (no hard newline).
	// Used by copySelection to avoid inserting spurious \n on copy.
	wrapped []bool

	// renderGen is incremented by the PTY goroutine after each output batch.
	// The renderer reads this atomically to detect per-pane changes without
	// needing to scan the dirty array.
	renderGen atomic.Uint64

	// Scrollback ring buffer — lines evicted from the top of the primary screen.
	// Fixed-size backing array indexed via scrollHead/scrollCount.
	// Logical index 0 is oldest, scrollCount-1 is most recent.
	scrollback        [][]Cell // pre-allocated to maxScrollback
	scrollbackWrapped []bool   // parallel ring: soft-wrap flag per scrollback line
	scrollHead        int      // physical index of oldest entry
	scrollCount       int      // number of valid entries
	maxScrollback     int

	// ViewOffset > 0 means the user has scrolled back.
	// 0 = live view, N = viewing N rows above the current live top.
	ViewOffset int

	// Terminal mode flags — set by the escape sequence parser.
	CursorVisible  bool // mode 25 (default true)
	AutoWrap       bool // mode 7  (default true)
	AppCursorKeys  bool // mode 1  — application cursor sequences (\x1BOA etc.)
	BracketedPaste bool // mode 2004
	MouseMode      int  // 0=off, 1000=normal, 1002=button, 1003=any
	SgrMouse       bool // mode 1006 — SGR extended mouse coordinates
	FocusEvents    bool // mode 1004 — send \x1B[I/O on focus change
	OriginMode     bool // DECSET ?6h — CUP coords relative to scroll region

	// Kitty keyboard protocol flag stack (CSI > flags;mode u push, CSI < n u pop).
	kittyStack []int

	// Pending responses — set by parser, consumed by Terminal methods each frame.
	PendingCPR        bool // CSI 6 n → send \x1B[row;colR
	PendingDA1        bool // CSI c   → send primary device attributes
	PendingDA2        bool // CSI > c → send secondary device attributes
	PendingKittyQuery bool // CSI ? u → send \x1B[?<flags>u

	// PendingDCSResponses is a queue of raw byte sequences to write to the PTY.
	// The parser appends responses (e.g., XTGETTCAP not-found); the game loop drains.
	PendingDCSResponses [][]byte

	// CursorStyleCode holds the last DECSCUSR value (CSI <n> SP q).
	// The game loop syncs this to Cursor.SetStyle when it changes.
	CursorStyleCode int

	// Selection holds the current mouse text selection (display coordinates).
	Selection Selection

	// PendingClipboardWrite is a queue of decoded text to write to the system
	// clipboard via OSC 52. Drained by Terminal.SendClipboardResponses() each frame.
	PendingClipboardWrite [][]byte

	// PendingClipboardQuery is set when OSC 52 requests a clipboard read ("?").
	// Terminal.SendClipboardResponses() responds with the current clipboard content.
	PendingClipboardQuery bool

	// Command block tracking — populated by OSC 133 (FinalTerm / iTerm2 protocol).
	// Blocks holds completed blocks; activeBlock is the one currently in progress.
	// Both are cleared when the alternate screen activates (TUI apps must not
	// accumulate stale block markers).
	Blocks      []BlockBoundary
	activeBlock *BlockBoundary
	maxBlocks   int // 0 = unlimited

	// BlockDoneCh receives the output text of completed command blocks (OSC 133 D).
	// The game loop drains this for completed block notifications. Buffered to avoid blocking the parser.
	BlockDoneCh chan string
}

// NewScreenBuffer allocates a grid with the given dimensions.
// maxScrollback is the maximum number of lines to keep in scrollback history.
// maxBlocks caps the number of completed command blocks retained (0 = unlimited).
func NewScreenBuffer(rows, cols, maxScrollback, maxBlocks int, fg, bg color.RGBA, palette [16]color.RGBA) *ScreenBuffer {
	sb := &ScreenBuffer{
		Rows:          rows,
		Cols:          cols,
		DefaultFG:     fg,
		DefaultBG:     bg,
		Palette:       palette,
		ScrollTop:     0,
		ScrollBottom:  rows - 1,
		maxScrollback: maxScrollback,
		maxBlocks:     maxBlocks,
		CursorVisible: true,
		AutoWrap:      true,
		BlockDoneCh:   make(chan string, 4),
	}
	if maxScrollback > 0 {
		sb.scrollback = make([][]Cell, maxScrollback)
		sb.scrollbackWrapped = make([]bool, maxScrollback)
	}
	sb.SGR.FG = fg
	sb.SGR.BG = bg
	sb.Cells = makeCells(rows, cols, fg, bg)
	sb.dirty = make([]bool, rows)
	sb.wrapped = make([]bool, rows)
	for i := range sb.dirty {
		sb.dirty[i] = true
	}
	return sb
}

func makeCells(rows, cols int, fg, bg color.RGBA) [][]Cell {
	cells := make([][]Cell, rows)
	for r := range cells {
		cells[r] = make([]Cell, cols)
		for c := range cells[r] {
			cells[r][c] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
		}
	}
	return cells
}

// Lock / Unlock for writes (PTY goroutine).
func (sb *ScreenBuffer) Lock()   { sb.mu.Lock() }
func (sb *ScreenBuffer) Unlock() { sb.mu.Unlock() }

// RLock / RUnlock for reads (render loop).
func (sb *ScreenBuffer) RLock()   { sb.mu.RLock() }
func (sb *ScreenBuffer) RUnlock() { sb.mu.RUnlock() }

// cells returns the active cell slice (normal or alternate).
func (sb *ScreenBuffer) cells() [][]Cell {
	if sb.altActive {
		return sb.altCells
	}
	return sb.Cells
}

// SetCell writes a cell at the given row/col. Caller must hold write lock.
func (sb *ScreenBuffer) SetCell(row, col int, c Cell) {
	if row < 0 || row >= sb.Rows || col < 0 || col >= sb.Cols {
		return
	}
	sb.cells()[row][col] = c
	sb.dirty[row] = true
}

// GetCell returns the cell at row/col. Caller must hold at least read lock.
func (sb *ScreenBuffer) GetCell(row, col int) Cell {
	if row < 0 || row >= sb.Rows || col < 0 || col >= sb.Cols {
		return Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	return sb.cells()[row][col]
}

// PutChar writes the current SGR-attributed rune at the cursor and advances.
// Combining characters (Unicode Mn/Mc/Me) are merged with the preceding cell
// via NFC normalization instead of occupying their own cell.
// Wide characters (CJK, fullwidth, emoji) occupy 2 columns: the first cell
// gets Width=2, the second gets Width=0 (continuation).
// Caller must hold write lock.
func (sb *ScreenBuffer) PutChar(ch rune) {
	// Combining character — merge with the previous cell via NFC.
	if unicode.In(ch, unicode.Mn, unicode.Mc, unicode.Me) {
		prevCol := sb.CursorCol - 1
		prevRow := sb.CursorRow
		if prevCol < 0 {
			return // no previous cell on this row — discard
		}
		prev := sb.GetCell(prevRow, prevCol)
		if prev.Char != 0 {
			composed := norm.NFC.String(string(prev.Char) + string(ch))
			runes := []rune(composed)
			if len(runes) == 1 {
				prev.Char = runes[0]
				sb.SetCell(prevRow, prevCol, prev)
				return
			}
		}
		return // can't compose — discard the combining mark
	}

	w := RuneWidth(ch)

	if sb.CursorCol >= sb.Cols {
		if !sb.AutoWrap {
			sb.CursorCol = sb.Cols - 1 // clamp: overwrite last column
		} else {
			sb.CursorCol = 0
			sb.LineFeed()
			// Mark the new row as a soft-wrap continuation.
			sb.wrapped[sb.CursorRow] = true
		}
	}

	// Wide char at last column: can't fit both cells. Fill with space and wrap.
	if w == 2 && sb.CursorCol == sb.Cols-1 {
		sb.clearWideOverlap(sb.CursorRow, sb.CursorCol)
		sb.SetCell(sb.CursorRow, sb.CursorCol, Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG})
		if sb.AutoWrap {
			sb.CursorCol = 0
			sb.LineFeed()
			sb.wrapped[sb.CursorRow] = true
		} else {
			// No wrap — can't place the wide char at all; drop it.
			return
		}
	}

	sb.clearWideOverlap(sb.CursorRow, sb.CursorCol)
	cell := sb.SGR.toCell(ch)
	cell.Width = uint8(w) // #nosec G115 -- runewidth returns 0-2, fits in uint8
	sb.SetCell(sb.CursorRow, sb.CursorCol, cell)
	sb.CursorCol++

	// Place continuation cell for wide characters.
	if w == 2 {
		sb.clearWideOverlap(sb.CursorRow, sb.CursorCol)
		sb.SetCell(sb.CursorRow, sb.CursorCol, Cell{Width: 0, FG: cell.FG, BG: cell.BG})
		sb.CursorCol++
	}
}

// clearWideOverlap fixes orphaned wide character halves when a cell is about
// to be overwritten. Must be called before SetCell on any cell that might be
// part of a wide character pair.
// Caller must hold write lock.
func (sb *ScreenBuffer) clearWideOverlap(row, col int) {
	if row < 0 || row >= sb.Rows || col < 0 || col >= sb.Cols {
		return
	}
	cells := sb.cells()
	c := cells[row][col]

	// Overwriting a continuation cell (second half of wide char) —
	// clear the parent wide char at col-1.
	if c.Width == 0 && col > 0 {
		cells[row][col-1] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
		sb.dirty[row] = true
	}

	// Overwriting the first half of a wide char — clear its continuation at col+1.
	if c.Width == 2 && col+1 < sb.Cols {
		cells[row][col+1] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
		sb.dirty[row] = true
	}
}

// LineFeed moves the cursor down one row, scrolling if at the bottom of the scroll region.
// Caller must hold write lock.
func (sb *ScreenBuffer) LineFeed() {
	if sb.CursorRow == sb.ScrollBottom {
		sb.ScrollUp(1)
	} else if sb.CursorRow < sb.Rows-1 {
		sb.CursorRow++
	}
	// Clear wrapped on the new row — PutChar re-sets it for soft wraps.
	sb.wrapped[sb.CursorRow] = false
}

// ScrollUp scrolls the scroll region up by n lines, adding blank lines at the bottom.
// Caller must hold write lock.
func (sb *ScreenBuffer) ScrollUp(n int) {
	cells := sb.cells()
	top := sb.ScrollTop
	bot := sb.ScrollBottom
	for i := 0; i < n; i++ {
		// Push evicted line into scrollback — only for the primary screen
		// scrolling from the very top (top==0). Application scroll regions
		// (top>0) do not contribute to scrollback history.
		if top == 0 && !sb.altActive && sb.maxScrollback > 0 {
			evicted := make([]Cell, sb.Cols)
			copy(evicted, cells[top])
			sb.scrollbackPush(evicted, sb.wrapped[top])
		}
		copy(cells[top:bot], cells[top+1:bot+1])
		copy(sb.wrapped[top:bot], sb.wrapped[top+1:bot+1])
		cells[bot] = blankRow(sb.Cols, sb.DefaultFG, sb.DefaultBG)
		sb.wrapped[bot] = false
	}
	for r := top; r <= bot; r++ {
		sb.dirty[r] = true
	}
}

// ScrollDown scrolls the scroll region down by n lines, adding blank lines at the top.
// Caller must hold write lock.
func (sb *ScreenBuffer) ScrollDown(n int) {
	cells := sb.cells()
	top := sb.ScrollTop
	bot := sb.ScrollBottom
	for i := 0; i < n; i++ {
		copy(cells[top+1:bot+1], cells[top:bot])
		copy(sb.wrapped[top+1:bot+1], sb.wrapped[top:bot])
		cells[top] = blankRow(sb.Cols, sb.DefaultFG, sb.DefaultBG)
		sb.wrapped[top] = false
	}
	for r := top; r <= bot; r++ {
		sb.dirty[r] = true
	}
}

func blankRow(cols int, fg, bg color.RGBA) []Cell {
	row := make([]Cell, cols)
	for i := range row {
		row[i] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	}
	return row
}

// EraseInDisplay erases part of the display. mode: 0=below, 1=above, 2=all, 3=scrollback+all.
// Per VT spec, erase fills with the current SGR background color, not the default.
// Caller must hold write lock.
func (sb *ScreenBuffer) EraseInDisplay(mode int) {
	cells := sb.cells()
	fg, bg := sb.DefaultFG, sb.SGR.BG
	switch mode {
	case 0: // erase from cursor to end of screen
		clearRowFrom(cells[sb.CursorRow], sb.CursorCol, sb.Cols, fg, bg)
		sb.dirty[sb.CursorRow] = true
		for r := sb.CursorRow + 1; r < sb.Rows; r++ {
			cells[r] = blankRow(sb.Cols, fg, bg)
			sb.dirty[r] = true
			sb.wrapped[r] = false
		}
	case 1: // erase from start of screen to cursor
		for r := 0; r < sb.CursorRow; r++ {
			cells[r] = blankRow(sb.Cols, fg, bg)
			sb.dirty[r] = true
			sb.wrapped[r] = false
		}
		clearRowTo(cells[sb.CursorRow], 0, sb.CursorCol+1, fg, bg)
		sb.dirty[sb.CursorRow] = true
	case 2: // erase all visible cells
		for r := 0; r < sb.Rows; r++ {
			cells[r] = blankRow(sb.Cols, fg, bg)
			sb.dirty[r] = true
			sb.wrapped[r] = false
		}
	case 3: // erase all visible cells + clear scrollback
		for r := 0; r < sb.Rows; r++ {
			cells[r] = blankRow(sb.Cols, fg, bg)
			sb.dirty[r] = true
			sb.wrapped[r] = false
		}
		sb.ClearScrollback()
	}
}

// EraseInLine erases part of the current line. mode: 0=right, 1=left, 2=all.
// Per VT spec, erase fills with the current SGR background color, not the default.
// Caller must hold write lock.
func (sb *ScreenBuffer) EraseInLine(mode int) {
	cells := sb.cells()
	fg, bg := sb.DefaultFG, sb.SGR.BG
	switch mode {
	case 0: // erase from cursor to end of line
		clearRowFrom(cells[sb.CursorRow], sb.CursorCol, sb.Cols, fg, bg)
	case 1: // erase from start of line to cursor
		clearRowTo(cells[sb.CursorRow], 0, sb.CursorCol+1, fg, bg)
	case 2: // erase entire line
		cells[sb.CursorRow] = blankRow(sb.Cols, fg, bg)
		sb.wrapped[sb.CursorRow] = false
	}
	sb.dirty[sb.CursorRow] = true
}

func clearRowFrom(row []Cell, from, to int, fg, bg color.RGBA) {
	// If erase starts on a continuation cell, also clear the parent wide char.
	if from > 0 && from < len(row) && row[from].Width == 0 {
		row[from-1] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	}
	for i := from; i < to && i < len(row); i++ {
		row[i] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	}
}

func clearRowTo(row []Cell, from, to int, fg, bg color.RGBA) {
	for i := from; i < to && i < len(row); i++ {
		row[i] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	}
	// If erase ends on the first half of a wide char, also clear the continuation.
	if to > 0 && to < len(row) && row[to].Width == 0 {
		row[to] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	}
}

// InsertChars inserts n blank characters at the cursor, shifting existing content right.
// Caller must hold write lock.
func (sb *ScreenBuffer) InsertChars(n int) {
	row := sb.cells()[sb.CursorRow]
	col := sb.CursorCol
	if col >= sb.Cols {
		return
	}
	end := sb.Cols
	if n > end-col {
		n = end - col
	}
	// Fix wide char split at insertion point.
	if col > 0 && col < len(row) && row[col].Width == 0 {
		row[col-1] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	copy(row[col+n:end], row[col:end-n])
	for i := col; i < col+n; i++ {
		row[i] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	// Fix wide char split at the right edge after shift.
	fixWideSplit(row, end-n, sb.DefaultFG, sb.DefaultBG)
	sb.dirty[sb.CursorRow] = true
}

// DeleteChars deletes n characters at the cursor, shifting content left.
// Caller must hold write lock.
func (sb *ScreenBuffer) DeleteChars(n int) {
	row := sb.cells()[sb.CursorRow]
	col := sb.CursorCol
	if col >= sb.Cols {
		return
	}
	end := sb.Cols
	if n > end-col {
		n = end - col
	}
	// Fix wide char split at deletion point.
	if col > 0 && col < len(row) && row[col].Width == 0 {
		row[col-1] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	// Fix wide char split at the far edge of deletion.
	if col+n < len(row) && row[col+n].Width == 0 {
		row[col+n] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	copy(row[col:end-n], row[col+n:end])
	for i := end - n; i < end; i++ {
		row[i] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	sb.dirty[sb.CursorRow] = true
}

// fixWideSplit repairs a wide character that was split at a boundary col.
// If row[col] is a continuation cell (Width=0) its parent is gone — replace with space.
// If row[col-1] is a wide char (Width=2) whose continuation was lost — replace with space.
func fixWideSplit(row []Cell, col int, fg, bg color.RGBA) {
	if col >= 0 && col < len(row) && row[col].Width == 0 {
		row[col] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
	}
	if col > 0 && col-1 < len(row) && row[col-1].Width == 2 {
		if col >= len(row) || row[col].Width != 0 {
			row[col-1] = Cell{Char: ' ', Width: 1, FG: fg, BG: bg}
		}
	}
}

// InsertLines inserts n blank lines at the cursor row within the scroll region.
// Caller must hold write lock.
func (sb *ScreenBuffer) InsertLines(n int) {
	cells := sb.cells()
	row := sb.CursorRow
	bot := sb.ScrollBottom
	if row > bot {
		return
	}
	if n > bot-row+1 {
		n = bot - row + 1
	}
	copy(cells[row+n:bot+1], cells[row:bot+1-n])
	for i := row; i < row+n; i++ {
		cells[i] = blankRow(sb.Cols, sb.DefaultFG, sb.DefaultBG)
	}
	for r := row; r <= bot; r++ {
		sb.dirty[r] = true
	}
}

// DeleteLines deletes n lines at the cursor row within the scroll region.
// Caller must hold write lock.
func (sb *ScreenBuffer) DeleteLines(n int) {
	cells := sb.cells()
	row := sb.CursorRow
	bot := sb.ScrollBottom
	if row > bot {
		return
	}
	if n > bot-row+1 {
		n = bot - row + 1
	}
	copy(cells[row:bot+1-n], cells[row+n:bot+1])
	for i := bot + 1 - n; i <= bot; i++ {
		cells[i] = blankRow(sb.Cols, sb.DefaultFG, sb.DefaultBG)
	}
	for r := row; r <= bot; r++ {
		sb.dirty[r] = true
	}
}

// SetScrollRegion sets the scroll region (1-indexed input, stored 0-indexed).
// Caller must hold write lock.
func (sb *ScreenBuffer) SetScrollRegion(top, bottom int) {
	// Convert from 1-indexed to 0-indexed.
	top--
	bottom--
	if top < 0 {
		top = 0
	}
	if bottom >= sb.Rows {
		bottom = sb.Rows - 1
	}
	if top >= bottom {
		return
	}
	sb.ScrollTop = top
	sb.ScrollBottom = bottom
	// DECOM: cursor homes to scroll region origin, not absolute (0,0).
	if sb.OriginMode {
		sb.CursorRow = top
	} else {
		sb.CursorRow = 0
	}
	sb.CursorCol = 0
}

// EnableAltScreen activates the alternate screen buffer.
// Caller must hold write lock.
func (sb *ScreenBuffer) EnableAltScreen() {
	if sb.altActive {
		return
	}
	sb.savedCursorRow = sb.CursorRow
	sb.savedCursorCol = sb.CursorCol
	sb.savedScrollTop = sb.ScrollTop
	sb.savedScrollBot = sb.ScrollBottom
	sb.altCells = makeCells(sb.Rows, sb.Cols, sb.DefaultFG, sb.DefaultBG)
	sb.altWrapped = make([]bool, len(sb.wrapped))
	copy(sb.altWrapped, sb.wrapped)
	sb.wrapped = make([]bool, sb.Rows)
	sb.altCursorRow = 0
	sb.altCursorCol = 0
	sb.altActive = true
	sb.ViewOffset = 0 // TUI apps need full viewport — no stale scroll position
	sb.CursorRow = 0
	sb.CursorCol = 0
	sb.ScrollTop = 0
	sb.ScrollBottom = sb.Rows - 1
	for r := range sb.dirty {
		sb.dirty[r] = true
	}
	// TUI apps must not accumulate stale block markers.
	sb.Blocks = sb.Blocks[:0]
	sb.activeBlock = nil
}

// DisableAltScreen deactivates the alternate screen buffer.
// Caller must hold write lock.
func (sb *ScreenBuffer) DisableAltScreen() {
	if !sb.altActive {
		return
	}
	sb.altActive = false
	sb.wrapped = sb.altWrapped
	sb.altWrapped = nil
	sb.CursorRow = sb.savedCursorRow
	sb.CursorCol = sb.savedCursorCol
	// Clamp cursor after restore — a resize while alt screen was active
	// may have changed dimensions since the cursor was saved.
	if sb.CursorRow >= sb.Rows {
		sb.CursorRow = sb.Rows - 1
	}
	if sb.CursorCol >= sb.Cols {
		sb.CursorCol = sb.Cols - 1
	}
	// Restore primary scroll region, clamped to current dimensions.
	sb.ScrollTop = sb.savedScrollTop
	sb.ScrollBottom = sb.savedScrollBot
	if sb.ScrollBottom >= sb.Rows {
		sb.ScrollBottom = sb.Rows - 1
	}
	if sb.ScrollTop >= sb.ScrollBottom {
		sb.ScrollTop = 0
	}
	for r := range sb.dirty {
		sb.dirty[r] = true
	}
}

// IsAltActive reports whether the alternate screen is active.
func (sb *ScreenBuffer) IsAltActive() bool {
	return sb.altActive
}

// Resize resizes the screen buffer, preserving content where possible.
// Caller must hold write lock.
func (sb *ScreenBuffer) Resize(rows, cols int) {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	if rows == sb.Rows && cols == sb.Cols {
		return
	}
	copyRows := rows
	if sb.Rows < copyRows {
		copyRows = sb.Rows
	}
	copyCols := cols
	if sb.Cols < copyCols {
		copyCols = sb.Cols
	}

	resizeBuf := func(old [][]Cell) [][]Cell {
		n := makeCells(rows, cols, sb.DefaultFG, sb.DefaultBG)
		for r := 0; r < copyRows; r++ {
			copy(n[r][:copyCols], old[r][:copyCols])
			// Fix wide char truncation at the new right edge.
			lastCol := copyCols - 1
			if lastCol >= 0 && lastCol < cols {
				// Wide char truncated: first half is at lastCol but continuation is outside the new grid.
				// Only applies on shrink — on grow, fresh blank cells at lastCol+1 are not continuations.
				if n[r][lastCol].Width == 2 && lastCol+1 >= cols {
					n[r][lastCol] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
				}
				// Orphaned continuation at first column (from a resize that cut the parent).
				if n[r][0].Width == 0 {
					n[r][0] = Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
				}
			}
		}
		return n
	}

	sb.Cells = resizeBuf(sb.Cells)
	if sb.altCells != nil {
		sb.altCells = resizeBuf(sb.altCells)
	}
	sb.Rows = rows
	sb.Cols = cols
	sb.ScrollTop = 0
	sb.ScrollBottom = rows - 1
	sb.dirty = make([]bool, rows)
	oldWrapped := sb.wrapped
	sb.wrapped = make([]bool, rows)
	for r := 0; r < copyRows; r++ {
		sb.wrapped[r] = oldWrapped[r]
	}
	// Resize the saved alt-screen wrapped slice so DisableAltScreen
	// restores a correctly sized slice.
	if sb.altWrapped != nil {
		oldAltW := sb.altWrapped
		sb.altWrapped = make([]bool, rows)
		altCopy := copyRows
		if len(oldAltW) < altCopy {
			altCopy = len(oldAltW)
		}
		for r := 0; r < altCopy; r++ {
			sb.altWrapped[r] = oldAltW[r]
		}
	}
	for r := range sb.dirty {
		sb.dirty[r] = true
	}
	if sb.CursorRow >= rows {
		sb.CursorRow = rows - 1
	}
	if sb.CursorCol >= cols {
		sb.CursorCol = cols - 1
	}

	// Invalidate completed blocks — without reflow, their absolute row indices
	// become unreliable after a resize. Keep activeBlock alive so the current
	// command sequence (A→B→C→D) completes; its positions may be approximate
	// but losing it entirely drops the first post-resize command.
	sb.Blocks = sb.Blocks[:0]
}

// UpdateColors replaces the default FG/BG, palette, and SGR defaults.
// Repaints existing cells that use the old defaults so the terminal content
// visually updates immediately (not just new output).
// Caller must hold write lock.
func (sb *ScreenBuffer) UpdateColors(fg, bg color.RGBA, palette [16]color.RGBA) {
	oldFG := sb.DefaultFG
	oldBG := sb.DefaultBG
	oldPalette := sb.Palette
	sb.DefaultFG = fg
	sb.DefaultBG = bg
	sb.Palette = palette
	sb.SGR.FG = fg
	sb.SGR.BG = bg

	// Build a color replacement map: old default + old palette → new values.
	colorMap := make(map[color.RGBA]color.RGBA)
	colorMap[oldFG] = fg
	colorMap[oldBG] = bg
	for i := 0; i < 16; i++ {
		if oldPalette[i] != palette[i] {
			colorMap[oldPalette[i]] = palette[i]
		}
	}

	repaintRow := func(row []Cell) {
		for i := range row {
			if newC, ok := colorMap[row[i].FG]; ok {
				row[i].FG = newC
			}
			if newC, ok := colorMap[row[i].BG]; ok {
				row[i].BG = newC
			}
		}
	}

	for _, row := range sb.Cells {
		repaintRow(row)
	}
	for _, row := range sb.altCells {
		repaintRow(row)
	}
	for _, row := range sb.scrollback {
		repaintRow(row)
	}
	sb.MarkAllDirty()
}

// MarkAllDirty marks every row as dirty.
func (sb *ScreenBuffer) MarkAllDirty() {
	for i := range sb.dirty {
		sb.dirty[i] = true
	}
}

// IsDirty reports whether a row has changed since last ClearDirty call.
func (sb *ScreenBuffer) IsDirty(row int) bool {
	if row < 0 || row >= len(sb.dirty) {
		return false
	}
	return sb.dirty[row]
}

// ClearDirty marks a row as clean.
func (sb *ScreenBuffer) ClearDirty(row int) {
	if row >= 0 && row < len(sb.dirty) {
		sb.dirty[row] = false
	}
}

// BumpRenderGen increments the per-buffer render generation counter.
// Called by the PTY goroutine after each output batch, and by the game loop
// for state changes that affect appearance without PTY output (blink, selection).
func (sb *ScreenBuffer) BumpRenderGen() { sb.renderGen.Add(1) }

// RenderGen returns the current render generation counter.
// The renderer reads this atomically to decide whether a pane needs redrawing.
func (sb *ScreenBuffer) RenderGen() uint64 { return sb.renderGen.Load() }

// GetDisplayCell returns the cell for display row r, accounting for ViewOffset.
// When ViewOffset == 0 this is identical to GetCell.
// Caller must hold at least a read lock.
func (sb *ScreenBuffer) GetDisplayCell(row, col int) Cell {
	if sb.ViewOffset == 0 {
		return sb.GetCell(row, col)
	}
	// primaryRow < 0 means we need to reach into scrollback.
	primaryRow := row - sb.ViewOffset
	if primaryRow >= 0 {
		return sb.GetCell(primaryRow, col)
	}
	// Map into scrollback: the most recent scrollback line sits just above
	// primary row 0, so scrollback index = len - ViewOffset + row.
	sbIdx := sb.scrollCount - sb.ViewOffset + row
	if sbIdx < 0 || sbIdx >= sb.scrollCount {
		return Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	sbRow := sb.scrollbackGet(sbIdx)
	if col < 0 || col >= len(sbRow) {
		return Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	return sbRow[col]
}

// IsDisplayRowWrapped reports whether the given display row is a soft-wrap
// continuation of the previous row. Accounts for ViewOffset and scrollback.
// Caller must hold at least a read lock.
func (sb *ScreenBuffer) IsDisplayRowWrapped(row int) bool {
	primaryRow := row - sb.ViewOffset
	if primaryRow >= 0 {
		if primaryRow < len(sb.wrapped) {
			return sb.wrapped[primaryRow]
		}
		return false
	}
	sbIdx := sb.scrollCount - sb.ViewOffset + row
	if sbIdx < 0 || sbIdx >= sb.scrollCount {
		return false
	}
	return sb.scrollbackWrapped[(sb.scrollHead+sbIdx)%sb.maxScrollback]
}

// ScrollViewUp scrolls the viewport n rows toward scrollback history.
// Caller must hold write lock.
func (sb *ScreenBuffer) ScrollViewUp(n int) {
	sb.ViewOffset += n
	max := sb.scrollCount
	if sb.ViewOffset > max {
		sb.ViewOffset = max
	}
	sb.MarkAllDirty()
}

// ScrollViewDown scrolls the viewport n rows back toward live output.
// Caller must hold write lock.
func (sb *ScreenBuffer) ScrollViewDown(n int) {
	sb.ViewOffset -= n
	if sb.ViewOffset < 0 {
		sb.ViewOffset = 0
	}
	sb.MarkAllDirty()
}

// ResetView snaps back to live output. No-op if already live.
// Caller must hold write lock.
func (sb *ScreenBuffer) ResetView() {
	if sb.ViewOffset != 0 {
		sb.ViewOffset = 0
		sb.MarkAllDirty()
	}
}

// ScrollbackLen returns the number of lines currently stored in scrollback.
func (sb *ScreenBuffer) ScrollbackLen() int {
	return sb.scrollCount
}

// scrollbackGet returns the row at logical index i (0 = oldest).
// Caller must ensure 0 <= i < scrollCount.
func (sb *ScreenBuffer) scrollbackGet(i int) []Cell {
	return sb.scrollback[(sb.scrollHead+i)%sb.maxScrollback]
}

// scrollbackPush appends a row to the ring buffer, evicting the oldest entry
// when at capacity. Evicted rows are zeroed for GC (SEC-002).
func (sb *ScreenBuffer) scrollbackPush(row []Cell, wrapped bool) {
	if sb.maxScrollback <= 0 {
		return
	}
	if sb.scrollCount < sb.maxScrollback {
		// Scrollback is growing — bump ViewOffset to keep the user's view pinned.
		if sb.ViewOffset > 0 {
			sb.ViewOffset++
		}
		sb.scrollback[sb.scrollCount] = row
		sb.scrollbackWrapped[sb.scrollCount] = wrapped
		sb.scrollCount++
	} else {
		// Evict oldest — zero cells for GC (SEC-002).
		evicted := sb.scrollback[sb.scrollHead]
		for j := range evicted {
			evicted[j] = Cell{}
		}
		sb.scrollback[sb.scrollHead] = row
		sb.scrollbackWrapped[sb.scrollHead] = wrapped
		sb.scrollHead = (sb.scrollHead + 1) % sb.maxScrollback
		// Do NOT decrement ViewOffset here. A net evict+push leaves the scrollback
		// depth unchanged, so the user's distance from the bottom is unchanged.
		// The old decrement caused the pinned view to slide toward the bottom during
		// sustained output (e.g. Claude running), forcing an unwanted snap-to-bottom.
	}
}


// ClearSelection clears any active mouse selection. Caller must hold write lock.
func (sb *ScreenBuffer) ClearSelection() {
	sb.Selection = Selection{}
}

// SetViewOffset sets the viewport offset and marks all rows dirty.
// Caller must hold write lock.
func (sb *ScreenBuffer) SetViewOffset(n int) {
	sb.ViewOffset = n
	sb.MarkAllDirty()
}

// DisplayToAbsRow converts a display row (0 = top of visible area) to an
// absolute row in the concatenated [scrollback | primary] space.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) DisplayToAbsRow(displayRow int) int {
	return sb.scrollCount - sb.ViewOffset + displayRow
}

// AbsToDisplayRow converts an absolute row to a display row.
// A negative result or one >= Rows means the row is off-screen.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) AbsToDisplayRow(absRow int) int {
	return absRow - sb.scrollCount + sb.ViewOffset
}

// GetAbsCell returns the cell at an absolute row and column.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) GetAbsCell(absRow, col int) Cell {
	sbLen := sb.scrollCount
	if absRow < sbLen {
		row := sb.scrollbackGet(absRow)
		if absRow < 0 || col < 0 || row == nil || col >= len(row) {
			return Cell{Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG}
		}
		return row[col]
	}
	return sb.GetCell(absRow-sbLen, col)
}

// IsAbsRowWrapped reports whether the absolute row is a soft-wrap continuation.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) IsAbsRowWrapped(absRow int) bool {
	sbLen := sb.scrollCount
	if absRow < sbLen {
		if absRow < 0 || absRow >= sb.scrollCount {
			return false
		}
		return sb.scrollbackWrapped[(sb.scrollHead+absRow)%sb.maxScrollback]
	}
	screenRow := absRow - sbLen
	if screenRow < 0 || screenRow >= len(sb.wrapped) {
		return false
	}
	return sb.wrapped[screenRow]
}

