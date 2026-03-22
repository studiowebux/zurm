package terminal

import (
	"strings"
	"time"
	"unicode"
)

// BlockBoundary marks the extent of one shell command tracked via OSC 133
// (FinalTerm / iTerm2 shell integration protocol). Row indices are stored as
// absolute line numbers (ScrollbackLen + screen row at event time) so they
// remain meaningful after the screen has scrolled.
type BlockBoundary struct {
	AbsPromptRow  int           // absolute row of OSC 133;A (prompt start)
	AbsCmdRow     int           // absolute row of OSC 133;C (pre-execution); -1 if not yet fired
	AbsEndRow     int           // absolute row of OSC 133;D (post-execution); -1 if still running
	CmdCol        int           // column where user input starts (from OSC 133;B); -1 if unknown
	ExitCode      int           // exit code from D;N; -1 = still running / unknown
	CommandText   string        // text on the row at the time C fired
	StartTime     time.Time     // when A was received (prompt appeared)
	ExecStartTime time.Time     // when C was received (user pressed Enter)
	Duration      time.Duration // execution time (C→D); zero while running
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
// (case-insensitive, non-overlapping). Wide character continuation cells are
// skipped when building the search text; a colMap translates rune indices back
// to column positions so highlights cover the correct columns.
// Caller must hold at least read lock.
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
	total := sb.scrollCount + sb.Rows
	for absRow := 0; absRow < total; absRow++ {
		var row []Cell
		if absRow < sb.scrollCount {
			row = sb.scrollbackGet(absRow)
		} else {
			row = sb.cells()[absRow-sb.scrollCount]
		}

		// Build text and colMap skipping continuation cells.
		var text []rune
		var colMap []int // colMap[runeIdx] = column index
		for col := 0; col < len(row); col++ {
			if row[col].Width == 0 {
				continue
			}
			c := row[col].Char
			if c == 0 {
				c = ' '
			}
			text = append(text, c)
			colMap = append(colMap, col)
		}

		ri := 0
		for ri <= len(text)-qlen {
			ok := true
			for i := 0; i < qlen; i++ {
				if unicode.ToLower(text[ri+i]) != qrunes[i] {
					ok = false
					break
				}
			}
			if ok {
				startCol := colMap[ri]
				endCol := colMap[ri+qlen-1]
				// Span includes the continuation cell of a trailing wide char.
				endCell := row[endCol]
				spanLen := endCol - startCol + 1
				if endCell.Width == 2 {
					spanLen++
				}
				matches = append(matches, SearchMatch{AbsRow: absRow, Col: startCol, Len: spanLen})
				ri += qlen
			} else {
				ri++
			}
		}
	}
	return matches
}

// ClearScrollback zeroes and discards all scrollback history (SEC-002).
// Caller must hold write lock.
func (sb *ScreenBuffer) ClearScrollback() {
	for i := 0; i < sb.scrollCount; i++ {
		row := sb.scrollbackGet(i)
		for j := range row {
			row[j] = Cell{}
		}
		sb.scrollback[(sb.scrollHead+i)%sb.maxScrollback] = nil
	}
	sb.scrollHead = 0
	sb.scrollCount = 0
	sb.ViewOffset = 0
	sb.MarkAllDirty()
}

// ActiveBlock returns the in-progress block, or nil when no command is running.
// Caller must hold at least read lock.
func (sb *ScreenBuffer) ActiveBlock() *BlockBoundary {
	return sb.activeBlock
}

// appendBlock adds a completed block and evicts the oldest entry when maxBlocks
// is exceeded. Uses copy+reslice to avoid the [1:] backing-array leak pattern.
// Caller must hold write lock.
func (sb *ScreenBuffer) appendBlock(b BlockBoundary) {
	sb.Blocks = append(sb.Blocks, b)
	if sb.maxBlocks > 0 && len(sb.Blocks) > sb.maxBlocks {
		copy(sb.Blocks, sb.Blocks[1:])
		sb.Blocks[len(sb.Blocks)-1] = BlockBoundary{} // zero for GC
		sb.Blocks = sb.Blocks[:len(sb.Blocks)-1]
	}
}

// applyBlockEvent processes one OSC 133 marker. Called by the parser while the
// buffer write lock is held.
//
//	kind='A'  prompt start
//	kind='B'  prompt end (no-op for now; used for future prompt-col tracking)
//	kind='C'  pre-execution — captures command text from the current row
//	kind='D'  post-execution — closes the active block with exitCode
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
			sb.appendBlock(*sb.activeBlock)
		}
		sb.activeBlock = &BlockBoundary{
			AbsPromptRow: absRow,
			AbsCmdRow:    -1,
			AbsEndRow:    -1,
			CmdCol:       -1,
			ExitCode:     -1,
			StartTime:    time.Now(),
		}
	case 'B':
		// Prompt end — cursor is now at the first column of user input.
		if sb.activeBlock == nil {
			return
		}
		sb.activeBlock.CmdCol = sb.CursorCol
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
			sb.appendBlock(*sb.activeBlock)

			// Send output text (rows between command and end) for block-done notifications.
			// C fires after the shell echoes the newline (pre-execution), so AbsCmdRow
			// is the first row where command output appears — use it directly.
			if sb.activeBlock.AbsCmdRow >= 0 {
				outStart := sb.activeBlock.AbsCmdRow
				outEnd := sb.activeBlock.AbsEndRow
				if outStart <= outEnd {
					text := sb.TextRange(outStart, outEnd)
					select {
					case sb.BlockDoneCh <- text:
					default: // drop if channel full
					}
				}
			}
			sb.activeBlock = nil
		}
	}
}

// TextRange extracts plain text from absolute rows [absStart, absEnd] inclusive.
// Reads from scrollback and the primary screen. Caller must hold at least read lock.
func (sb *ScreenBuffer) TextRange(absStart, absEnd int) string {
	sbLen := sb.scrollCount
	var out strings.Builder
	for abs := absStart; abs <= absEnd; abs++ {
		var row []Cell
		if abs < sbLen {
			row = sb.scrollbackGet(abs)
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
			// Skip continuation cells to avoid duplicate chars.
			if c.Width == 0 {
				continue
			}
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
		if c.Width == 0 {
			continue
		}
		if c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return strings.TrimRight(b.String(), " \t")
}
