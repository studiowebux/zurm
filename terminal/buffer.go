package terminal

import (
	"image/color"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Cell represents a single character cell in the terminal grid.
type Cell struct {
	Char      rune
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

// BlockBoundary marks the extent of one shell command tracked via OSC 133
// (FinalTerm / iTerm2 shell integration protocol). Row indices are stored as
// absolute line numbers (ScrollbackLen + screen row at event time) so they
// remain meaningful after the screen has scrolled.
type BlockBoundary struct {
	AbsPromptRow int           // absolute row of OSC 133;A (prompt start)
	AbsCmdRow    int           // absolute row of OSC 133;C (pre-execution); -1 if not yet fired
	AbsEndRow    int           // absolute row of OSC 133;D (post-execution); -1 if still running
	ExitCode     int           // exit code from D;N; -1 = still running / unknown
	CommandText  string        // text on the row at the time C fired
	StartTime    time.Time     // when A was received (prompt appeared)
	ExecStartTime time.Time   // when C was received (user pressed Enter)
	Duration     time.Duration // execution time (C→D); zero while running
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
	altActive      bool
	altCells       [][]Cell
	altWrapped     []bool
	altCursorRow   int
	altCursorCol   int
	savedCursorRow int
	savedCursorCol int

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
	// Index 0 is oldest, len-1 is most recent.
	scrollback        [][]Cell
	scrollbackWrapped []bool
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
}

// NewScreenBuffer allocates a grid with the given dimensions.
// maxScrollback is the maximum number of lines to keep in scrollback history.
func NewScreenBuffer(rows, cols, maxScrollback int, fg, bg color.RGBA, palette [16]color.RGBA) *ScreenBuffer {
	sb := &ScreenBuffer{
		Rows:          rows,
		Cols:          cols,
		DefaultFG:     fg,
		DefaultBG:     bg,
		Palette:       palette,
		ScrollTop:     0,
		ScrollBottom:  rows - 1,
		maxScrollback: maxScrollback,
		CursorVisible: true,
		AutoWrap:      true,
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
			cells[r][c] = Cell{Char: ' ', FG: fg, BG: bg}
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
		return Cell{Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	return sb.cells()[row][col]
}

// PutChar writes the current SGR-attributed rune at the cursor and advances.
// Combining characters (Unicode Mn/Mc/Me) are merged with the preceding cell
// via NFC normalization instead of occupying their own cell.
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
	sb.SetCell(sb.CursorRow, sb.CursorCol, sb.SGR.toCell(ch))
	sb.CursorCol++
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
			sb.scrollback = append(sb.scrollback, evicted)
			sb.scrollbackWrapped = append(sb.scrollbackWrapped, sb.wrapped[top])
			// Keep the user's view pinned when scrolled up.
			if sb.ViewOffset > 0 {
				sb.ViewOffset++
			}
			if len(sb.scrollback) > sb.maxScrollback {
				// Drop oldest line — zero it first (SEC-002).
				for j := range sb.scrollback[0] {
					sb.scrollback[0][j] = Cell{}
				}
				sb.scrollback = sb.scrollback[1:]
				sb.scrollbackWrapped = sb.scrollbackWrapped[1:]
				// Adjust ViewOffset for the dropped line.
				if sb.ViewOffset > 0 {
					sb.ViewOffset--
				}
			}
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
		row[i] = Cell{Char: ' ', FG: fg, BG: bg}
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
	for i := from; i < to && i < len(row); i++ {
		row[i] = Cell{Char: ' ', FG: fg, BG: bg}
	}
}

func clearRowTo(row []Cell, from, to int, fg, bg color.RGBA) {
	for i := from; i < to && i < len(row); i++ {
		row[i] = Cell{Char: ' ', FG: fg, BG: bg}
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
	copy(row[col+n:end], row[col:end-n])
	for i := col; i < col+n; i++ {
		row[i] = Cell{Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
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
	copy(row[col:end-n], row[col+n:end])
	for i := end - n; i < end; i++ {
		row[i] = Cell{Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	sb.dirty[sb.CursorRow] = true
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
	sb.CursorRow = 0
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
	sb.altCells = makeCells(sb.Rows, sb.Cols, sb.DefaultFG, sb.DefaultBG)
	sb.altWrapped = sb.wrapped
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
	sb.ScrollTop = 0
	sb.ScrollBottom = sb.Rows - 1
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
	sbIdx := len(sb.scrollback) - sb.ViewOffset + row
	if sbIdx < 0 || sbIdx >= len(sb.scrollback) {
		return Cell{Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	if col < 0 || col >= len(sb.scrollback[sbIdx]) {
		return Cell{Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG}
	}
	return sb.scrollback[sbIdx][col]
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
	sbIdx := len(sb.scrollback) - sb.ViewOffset + row
	if sbIdx < 0 || sbIdx >= len(sb.scrollbackWrapped) {
		return false
	}
	return sb.scrollbackWrapped[sbIdx]
}

// ScrollViewUp scrolls the viewport n rows toward scrollback history.
// Caller must hold write lock.
func (sb *ScreenBuffer) ScrollViewUp(n int) {
	sb.ViewOffset += n
	max := len(sb.scrollback)
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
	return len(sb.scrollback)
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
	return len(sb.scrollback) - sb.ViewOffset + displayRow
}

// AbsToDisplayRow converts an absolute row to a display row.
// A negative result or one >= Rows means the row is off-screen.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) AbsToDisplayRow(absRow int) int {
	return absRow - len(sb.scrollback) + sb.ViewOffset
}

// GetAbsCell returns the cell at an absolute row and column.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) GetAbsCell(absRow, col int) Cell {
	sbLen := len(sb.scrollback)
	if absRow < sbLen {
		if absRow < 0 || col < 0 || col >= len(sb.scrollback[absRow]) {
			return Cell{Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG}
		}
		return sb.scrollback[absRow][col]
	}
	return sb.GetCell(absRow-sbLen, col)
}

// IsAbsRowWrapped reports whether the absolute row is a soft-wrap continuation.
// Caller must hold at least RLock.
func (sb *ScreenBuffer) IsAbsRowWrapped(absRow int) bool {
	sbLen := len(sb.scrollback)
	if absRow < sbLen {
		if absRow < 0 || absRow >= len(sb.scrollbackWrapped) {
			return false
		}
		return sb.scrollbackWrapped[absRow]
	}
	screenRow := absRow - sbLen
	if screenRow < 0 || screenRow >= len(sb.wrapped) {
		return false
	}
	return sb.wrapped[screenRow]
}

// SearchMatch describes one occurrence of a search query in the buffer.
// AbsRow is the index into the concatenated [scrollback..., primary...] space:
// AbsRow 0 = oldest scrollback line; AbsRow ScrollbackLen() = primary row 0.
type SearchMatch struct {
	AbsRow int
	Col    int
	Len    int
}

// SearchAll searches the entire scrollback + primary screen for query
// (case-insensitive, non-overlapping). Caller must hold at least read lock.
func (sb *ScreenBuffer) SearchAll(query string) []SearchMatch {
	if query == "" {
		return nil
	}
	qrunes := []rune(query)
	for i, r := range qrunes {
		qrunes[i] = unicode.ToLower(r)
	}
	qlen := len(qrunes)

	var matches []SearchMatch
	total := len(sb.scrollback) + sb.Rows
	for absRow := 0; absRow < total; absRow++ {
		var row []Cell
		if absRow < len(sb.scrollback) {
			row = sb.scrollback[absRow]
		} else {
			row = sb.Cells[absRow-len(sb.scrollback)]
		}
		col := 0
		for col <= len(row)-qlen {
			ok := true
			for i := 0; i < qlen; i++ {
				c := row[col+i].Char
				if c == 0 {
					c = ' '
				}
				if unicode.ToLower(c) != qrunes[i] {
					ok = false
					break
				}
			}
			if ok {
				matches = append(matches, SearchMatch{AbsRow: absRow, Col: col, Len: qlen})
				col += qlen
			} else {
				col++
			}
		}
	}
	return matches
}

// ClearScrollback zeroes and discards all scrollback history (SEC-002).
// Caller must hold write lock.
func (sb *ScreenBuffer) ClearScrollback() {
	for i := range sb.scrollback {
		for j := range sb.scrollback[i] {
			sb.scrollback[i][j] = Cell{}
		}
	}
	sb.scrollback = sb.scrollback[:0]
	sb.scrollbackWrapped = sb.scrollbackWrapped[:0]
	sb.ViewOffset = 0
	sb.MarkAllDirty()
}

// ActiveBlock returns the in-progress block, or nil when no command is running.
// Caller must hold at least read lock.
func (sb *ScreenBuffer) ActiveBlock() *BlockBoundary {
	return sb.activeBlock
}

// applyBlockEvent processes one OSC 133 marker. Called by the parser while the
// buffer write lock is held.
//
//   kind='A'  prompt start
//   kind='B'  prompt end (no-op for now; used for future prompt-col tracking)
//   kind='C'  pre-execution — captures command text from the current row
//   kind='D'  post-execution — closes the active block with exitCode
func (sb *ScreenBuffer) applyBlockEvent(kind rune, exitCode int) {
	absRow := sb.ScrollbackLen() + sb.CursorRow
	switch kind {
	case 'A':
		// If a previous block was open (no D received), auto-close it now.
		// This handles shells that only emit A and C but not D.
		if sb.activeBlock != nil && sb.activeBlock.AbsCmdRow >= 0 && sb.activeBlock.AbsEndRow < 0 {
			sb.activeBlock.AbsEndRow = absRow
			if !sb.activeBlock.ExecStartTime.IsZero() {
				sb.activeBlock.Duration = time.Since(sb.activeBlock.ExecStartTime)
			} else {
				sb.activeBlock.Duration = time.Since(sb.activeBlock.StartTime)
			}
			// ExitCode stays -1 (unknown — no D was received).
			sb.Blocks = append(sb.Blocks, *sb.activeBlock)
		}
		sb.activeBlock = &BlockBoundary{
			AbsPromptRow: absRow,
			AbsCmdRow:    -1,
			AbsEndRow:    -1,
			ExitCode:     -1,
			StartTime:    time.Now(),
		}
	case 'B':
		// Prompt end — reserved for prompt-column tracking in a future pass.
	case 'C':
		if sb.activeBlock != nil {
			sb.activeBlock.AbsCmdRow = absRow
			sb.activeBlock.CommandText = sb.rowText(sb.CursorRow)
			sb.activeBlock.ExecStartTime = time.Now()
		}
	case 'D':
		if sb.activeBlock != nil {
			// D fires at the next prompt row; step back one to exclude it.
			end := absRow - 1
			if end < sb.activeBlock.AbsCmdRow {
				end = sb.activeBlock.AbsCmdRow
			}
			sb.activeBlock.AbsEndRow = end
			sb.activeBlock.ExitCode = exitCode
			// Measure execution time from C (pre-exec) when available,
			// falling back to A (prompt start) if C was never received.
			if !sb.activeBlock.ExecStartTime.IsZero() {
				sb.activeBlock.Duration = time.Since(sb.activeBlock.ExecStartTime)
			} else {
				sb.activeBlock.Duration = time.Since(sb.activeBlock.StartTime)
			}
			sb.Blocks = append(sb.Blocks, *sb.activeBlock)
			sb.activeBlock = nil
		}
	}
}

// TextRange extracts plain text from absolute rows [absStart, absEnd] inclusive.
// Reads from scrollback and the primary screen. Caller must hold at least read lock.
func (sb *ScreenBuffer) TextRange(absStart, absEnd int) string {
	sbLen := len(sb.scrollback)
	var out strings.Builder
	for abs := absStart; abs <= absEnd; abs++ {
		var row []Cell
		if abs < sbLen {
			row = sb.scrollback[abs]
		} else {
			screenRow := abs - sbLen
			cells := sb.cells()
			if screenRow >= 0 && screenRow < len(cells) {
				row = cells[screenRow]
			}
		}
		if row == nil {
			continue
		}
		var line []rune
		for _, c := range row {
			ch := c.Char
			if ch == 0 {
				ch = ' '
			}
			line = append(line, ch)
		}
		// Trim trailing spaces from the line.
		end := len(line)
		for end > 0 && line[end-1] == ' ' {
			end--
		}
		out.WriteString(string(line[:end]))
		out.WriteByte('\n')
	}
	return out.String()
}

// rowText returns the visible text content of a primary-screen row, with
// trailing whitespace stripped. Caller must hold at least read lock.
func (sb *ScreenBuffer) rowText(row int) string {
	cells := sb.cells()
	if row < 0 || row >= len(cells) {
		return ""
	}
	var b strings.Builder
	for _, c := range cells[row] {
		if c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " \t")
}
