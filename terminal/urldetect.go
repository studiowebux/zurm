package terminal

import (
	"regexp"
	"strings"
)

// URLMatch represents a detected URL in the visible buffer.
// Row/Col are display-space coordinates (accounting for ViewOffset).
type URLMatch struct {
	StartRow, StartCol int
	EndRow, EndCol     int
	Text               string
}

// urlPattern matches common URLs in terminal output.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\x00-\x1f]+`)

// trimTrailingPunct strips trailing punctuation that is typically not part of the URL
// but gets captured when URLs appear in prose or markdown. Parentheses are only stripped
// when unbalanced (preserves Wikipedia-style URLs like https://en.wikipedia.org/wiki/Go_(programming_language)).
func trimTrailingPunct(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]
		switch last {
		case '.', ',', ';', ':', '!', '?', '\'', '"':
			u = u[:len(u)-1]
		case ')':
			if strings.Count(u, "(") < strings.Count(u, ")") {
				u = u[:len(u)-1]
			} else {
				return u
			}
		case ']':
			if strings.Count(u, "[") < strings.Count(u, "]") {
				u = u[:len(u)-1]
			} else {
				return u
			}
		default:
			return u
		}
	}
	return u
}

// DetectURLs scans the visible buffer rows and returns all URL matches
// with display-space coordinates. Consecutive soft-wrapped physical rows are
// merged into a single logical line before the regex runs, so URLs that wrap
// across row boundaries are detected correctly.
// Continuation cells (Width=0) are skipped; rowMap/colMap convert rune
// positions back to (display row, display column) pairs.
// Caller must hold at least an RLock.
func (sb *ScreenBuffer) DetectURLs() []URLMatch {
	var matches []URLMatch
	row := 0
	for row < sb.Rows {
		// Find the last physical row of this logical line.
		// A row R is a continuation when IsDisplayRowWrapped(R) is true.
		last := row
		for last+1 < sb.Rows && sb.IsDisplayRowWrapped(last+1) {
			last++
		}

		// Build combined text and per-rune coordinate maps for rows [row..last].
		var runes []rune
		var rowMap []int // rowMap[runeIdx] = display row
		var colMap []int // colMap[runeIdx] = display column
		for r := row; r <= last; r++ {
			for col := 0; col < sb.Cols; col++ {
				cell := sb.GetDisplayCell(r, col)
				if cell.Width == 0 {
					continue
				}
				ch := cell.Char
				if ch == 0 {
					ch = ' '
				}
				runes = append(runes, ch)
				rowMap = append(rowMap, r)
				colMap = append(colMap, col)
			}
		}

		text := string(runes)
		locs := urlPattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			raw := text[loc[0]:loc[1]]
			cleaned := trimTrailingPunct(raw)
			if cleaned == "" {
				continue
			}
			// Convert byte offsets to rune indices, then to (row, col) via maps.
			runeStart := len([]rune(text[:loc[0]]))
			runeEnd := runeStart + len([]rune(cleaned)) - 1
			if runeStart >= len(colMap) || runeEnd >= len(colMap) {
				continue
			}
			startRow := rowMap[runeStart]
			startCol := colMap[runeStart]
			endRow := rowMap[runeEnd]
			endCol := colMap[runeEnd]
			// Extend endCol to include continuation cell of a trailing wide char.
			endCell := sb.GetDisplayCell(endRow, endCol)
			if endCell.Width == 2 && endCol+1 < sb.Cols {
				endCol++
			}
			matches = append(matches, URLMatch{
				StartRow: startRow, StartCol: startCol,
				EndRow: endRow, EndCol: endCol,
				Text: cleaned,
			})
		}

		row = last + 1
	}
	return matches
}

// URLAt returns the URL match at the given display row/col, or nil.
func URLAt(matches []URLMatch, row, col int) *URLMatch {
	for i := range matches {
		m := &matches[i]
		if m.StartRow == m.EndRow {
			// Single-row match: col must be within [StartCol, EndCol].
			if row == m.StartRow && col >= m.StartCol && col <= m.EndCol {
				return m
			}
			continue
		}
		// Multi-row match: start row (any col from StartCol onward), middle
		// rows (any col), end row (any col up to EndCol).
		if row == m.StartRow && col >= m.StartCol {
			return m
		}
		if row > m.StartRow && row < m.EndRow {
			return m
		}
		if row == m.EndRow && col <= m.EndCol {
			return m
		}
	}
	return nil
}

// ContainsCell returns true if the cell at (row, col) is within this URL match.
func (m *URLMatch) ContainsCell(row, col int) bool {
	if row < m.StartRow || row > m.EndRow {
		return false
	}
	if row == m.StartRow && col < m.StartCol {
		return false
	}
	if row == m.EndRow && col > m.EndCol {
		return false
	}
	return true
}
