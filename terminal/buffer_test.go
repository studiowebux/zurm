package terminal

import (
	"image/color"
	"testing"
)

var (
	testFG      = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	testBG      = color.RGBA{A: 255}
	testPalette [16]color.RGBA
)

func newTestBuffer(rows, cols int) *ScreenBuffer {
	return NewScreenBuffer(rows, cols, 0, 0, testFG, testBG, testPalette)
}

func TestPutChar_NarrowChar(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('A')

	cell := buf.GetCell(0, 0)
	if cell.Char != 'A' || cell.Width != 1 {
		t.Errorf("narrow char: got Char=%c Width=%d, want A/1", cell.Char, cell.Width)
	}
	if buf.CursorCol != 1 {
		t.Errorf("cursor after narrow: got %d, want 1", buf.CursorCol)
	}
}

func TestPutChar_WideChar(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('中') // CJK — width 2

	cell := buf.GetCell(0, 0)
	if cell.Char != '中' || cell.Width != 2 {
		t.Errorf("wide char first cell: got Char=%c Width=%d, want 中/2", cell.Char, cell.Width)
	}

	cont := buf.GetCell(0, 1)
	if cont.Char != 0 || cont.Width != 0 {
		t.Errorf("wide char continuation: got Char=%c Width=%d, want 0/0", cont.Char, cont.Width)
	}

	if buf.CursorCol != 2 {
		t.Errorf("cursor after wide: got %d, want 2", buf.CursorCol)
	}
}

func TestPutChar_WideAtLastColumn(t *testing.T) {
	buf := newTestBuffer(2, 5)
	buf.CursorCol = 4 // last column

	buf.PutChar('中') // can't fit — should wrap

	// Last column of row 0 should be space.
	cell := buf.GetCell(0, 4)
	if cell.Char != ' ' || cell.Width != 1 {
		t.Errorf("last col fill: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}

	// Wide char should be at row 1, col 0-1.
	cell = buf.GetCell(1, 0)
	if cell.Char != '中' || cell.Width != 2 {
		t.Errorf("wrapped wide first cell: got Char=%c Width=%d, want 中/2", cell.Char, cell.Width)
	}
	cont := buf.GetCell(1, 1)
	if cont.Width != 0 {
		t.Errorf("wrapped wide continuation: got Width=%d, want 0", cont.Width)
	}
}

func TestPutChar_OverwriteWideWithNarrow(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('中') // cols 0-1
	buf.CursorCol = 0
	buf.PutChar('A') // overwrite first half

	cell := buf.GetCell(0, 0)
	if cell.Char != 'A' || cell.Width != 1 {
		t.Errorf("overwrite first half: got Char=%c Width=%d, want A/1", cell.Char, cell.Width)
	}
	// Continuation at col 1 should be cleared to space.
	cont := buf.GetCell(0, 1)
	if cont.Char != ' ' || cont.Width != 1 {
		t.Errorf("orphaned continuation: got Char=%c Width=%d, want ' '/1", cont.Char, cont.Width)
	}
}

func TestPutChar_OverwriteContinuationWithNarrow(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('中') // cols 0-1
	buf.CursorCol = 1
	buf.PutChar('B') // overwrite continuation

	// Parent wide char at col 0 should be cleared.
	cell := buf.GetCell(0, 0)
	if cell.Char != ' ' || cell.Width != 1 {
		t.Errorf("orphaned parent: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}
	// Col 1 should now be 'B'.
	cell = buf.GetCell(0, 1)
	if cell.Char != 'B' || cell.Width != 1 {
		t.Errorf("overwrite continuation: got Char=%c Width=%d, want B/1", cell.Char, cell.Width)
	}
}

func TestPutChar_OverwriteNarrowWithWide(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('A') // col 0
	buf.PutChar('B') // col 1
	buf.CursorCol = 0
	buf.PutChar('中') // overwrites A and B

	cell := buf.GetCell(0, 0)
	if cell.Char != '中' || cell.Width != 2 {
		t.Errorf("wide overwrite: got Char=%c Width=%d, want 中/2", cell.Char, cell.Width)
	}
	cont := buf.GetCell(0, 1)
	if cont.Width != 0 {
		t.Errorf("wide overwrite continuation: got Width=%d, want 0", cont.Width)
	}
}

func TestPutChar_WideSequence(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('中') // cols 0-1
	buf.PutChar('文') // cols 2-3

	if buf.CursorCol != 4 {
		t.Errorf("cursor after two wide: got %d, want 4", buf.CursorCol)
	}
	cell := buf.GetCell(0, 2)
	if cell.Char != '文' || cell.Width != 2 {
		t.Errorf("second wide: got Char=%c Width=%d, want 文/2", cell.Char, cell.Width)
	}
}

func TestEraseInLine_WideCharBoundary(t *testing.T) {
	buf := newTestBuffer(1, 10)
	buf.PutChar('中') // cols 0-1
	buf.PutChar('文') // cols 2-3
	buf.CursorCol = 1

	// Erase from cursor (col 1 = continuation cell) to end of line.
	buf.EraseInLine(0)

	// Parent wide char at col 0 should be cleared (boundary hit continuation).
	cell := buf.GetCell(0, 0)
	if cell.Char != ' ' || cell.Width != 1 {
		t.Errorf("erase boundary parent: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}
	// Col 1 onward should be blank.
	for c := 1; c < 10; c++ {
		cell = buf.GetCell(0, c)
		if cell.Char != ' ' || cell.Width != 1 {
			t.Errorf("col %d after erase: got Char=%c Width=%d, want ' '/1", c, cell.Char, cell.Width)
		}
	}
}

func TestEraseInLine_LeftEraseHitsWideChar(t *testing.T) {
	buf := newTestBuffer(1, 10)
	buf.PutChar('A')  // col 0
	buf.PutChar('中') // cols 1-2
	buf.PutChar('B')  // col 3
	buf.CursorCol = 1

	// Erase from start of line to cursor (col 0..1). Col 1 is the first half
	// of wide char, col 2 is continuation — continuation should be cleaned.
	buf.EraseInLine(1)

	cell := buf.GetCell(0, 2)
	if cell.Char != ' ' || cell.Width != 1 {
		t.Errorf("continuation after left erase: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}
}

func TestDeleteChars_WideCharSplit(t *testing.T) {
	buf := newTestBuffer(1, 10)
	buf.PutChar('A')  // col 0
	buf.PutChar('中') // cols 1-2
	buf.PutChar('B')  // col 3
	buf.CursorCol = 1

	// Delete 1 char at col 1 (first half of wide char). Should also fix
	// the continuation that gets orphaned.
	buf.DeleteChars(1)

	// After deletion, col 1 should have been the continuation (now shifted).
	// The continuation at col+n=2 was Width=0, so it gets replaced with space.
	cell := buf.GetCell(0, 1)
	if cell.Width == 0 {
		t.Errorf("orphaned continuation at col 1 after delete: got Width=0")
	}
}

func TestInsertChars_WideCharSplit(t *testing.T) {
	buf := newTestBuffer(1, 10)
	buf.PutChar('中') // cols 0-1
	buf.PutChar('中') // cols 2-3
	buf.CursorCol = 1

	// Insert 1 char at col 1 (continuation of first wide char).
	// Should clear parent at col 0.
	buf.InsertChars(1)

	cell := buf.GetCell(0, 0)
	if cell.Char != ' ' || cell.Width != 1 {
		t.Errorf("parent after insert at continuation: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}
}

func TestResize_TruncatesWideChar(t *testing.T) {
	buf := newTestBuffer(2, 6)
	buf.PutChar('中') // cols 0-1
	buf.PutChar('文') // cols 2-3
	buf.PutChar('A')  // col 4

	// Shrink to 4 cols — '文' at col 2-3 fits, but verify no orphans.
	buf.Resize(2, 4)
	cell := buf.GetCell(0, 0)
	if cell.Char != '中' || cell.Width != 2 {
		t.Errorf("resize: col 0 got Char=%c Width=%d, want 中/2", cell.Char, cell.Width)
	}
	cell = buf.GetCell(0, 2)
	if cell.Char != '文' || cell.Width != 2 {
		t.Errorf("resize: col 2 got Char=%c Width=%d, want 文/2", cell.Char, cell.Width)
	}
}

func TestResize_TruncatesWideCharAtBoundary(t *testing.T) {
	buf := newTestBuffer(2, 6)
	buf.PutChar('中') // cols 0-1
	buf.PutChar('文') // cols 2-3

	// Shrink to 3 cols — '文' at cols 2-3 is split (first half at col 2, continuation at col 3 lost).
	buf.Resize(2, 3)
	cell := buf.GetCell(0, 2)
	if cell.Width == 2 {
		t.Errorf("resize truncated wide char should be replaced: got Width=2")
	}
	if cell.Char != ' ' || cell.Width != 1 {
		t.Errorf("resize truncated: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}
}

func TestResize_OrphanedContinuationAtCol0(t *testing.T) {
	buf := newTestBuffer(2, 6)
	// Manually place an orphaned continuation at col 0 to simulate edge case.
	buf.Lock()
	buf.Cells[0][0] = Cell{Width: 0, FG: testFG, BG: testBG}
	buf.Unlock()

	buf.Resize(2, 4)
	cell := buf.GetCell(0, 0)
	if cell.Width != 1 || cell.Char != ' ' {
		t.Errorf("orphan at col 0 after resize: got Char=%c Width=%d, want ' '/1", cell.Char, cell.Width)
	}
}

func TestPutChar_Emoji(t *testing.T) {
	buf := newTestBuffer(2, 10)
	buf.PutChar('😀') // SMP emoji — width 2

	cell := buf.GetCell(0, 0)
	if cell.Char != '😀' || cell.Width != 2 {
		t.Errorf("emoji first cell: got Char=%U Width=%d, want U+1F600/2", cell.Char, cell.Width)
	}
	cont := buf.GetCell(0, 1)
	if cont.Width != 0 {
		t.Errorf("emoji continuation: got Width=%d, want 0", cont.Width)
	}
	if buf.CursorCol != 2 {
		t.Errorf("cursor after emoji: got %d, want 2", buf.CursorCol)
	}
}

func TestTextRange_SkipsContinuation(t *testing.T) {
	buf := newTestBuffer(1, 10)
	buf.PutChar('A')
	buf.PutChar('中')
	buf.PutChar('B')

	text := buf.TextRange(0, 0)
	want := "A中B\n"
	if text != want {
		t.Errorf("TextRange got %q, want %q", text, want)
	}
}

func TestSearchAll_WideChar(t *testing.T) {
	buf := newTestBuffer(1, 10)
	buf.PutChar('A')
	buf.PutChar('中') // cols 1-2
	buf.PutChar('B')  // col 3

	matches := buf.SearchAll("中")
	if len(matches) != 1 {
		t.Fatalf("SearchAll: got %d matches, want 1", len(matches))
	}
	m := matches[0]
	if m.Col != 1 {
		t.Errorf("SearchAll: match col = %d, want 1", m.Col)
	}
	// Span should cover cols 1-2 (the wide char + continuation).
	if m.Len != 2 {
		t.Errorf("SearchAll: match len = %d, want 2", m.Len)
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		want int
	}{
		{"ASCII", 'A', 1},
		{"CJK ideograph", '中', 2},
		{"Fullwidth Latin", 'Ａ', 2},
		{"Emoji SMP", '😀', 2},
		{"Misc symbol", '☀', 2},
		{"ZWJ", '\u200D', 0},
		{"Variation selector", '\uFE0F', 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RuneWidth(tc.r)
			if got != tc.want {
				t.Errorf("RuneWidth(%U) = %d, want %d", tc.r, got, tc.want)
			}
		})
	}
}
