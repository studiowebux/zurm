package terminal

import (
	"testing"
)

// newTestParser creates a parser with a test buffer and no channels.
func newTestParser(rows, cols int) (*Parser, *ScreenBuffer) {
	buf := newTestBuffer(rows, cols)
	p := NewParser(buf, nil, nil, nil, nil, nil, nil)
	return p, buf
}

// feed is a shorthand for p.Feed([]byte(s)).
func feed(p *Parser, s string) {
	p.Feed([]byte(s))
}

// rowText extracts the visible text from a buffer row (trimmed trailing spaces).
func rowText(buf *ScreenBuffer, row int) string {
	var runes []rune
	for col := 0; col < buf.Cols; col++ {
		c := buf.GetCell(row, col)
		if c.Width == 0 {
			continue // skip wide char continuation
		}
		runes = append(runes, c.Char)
	}
	// Trim trailing spaces.
	for len(runes) > 0 && runes[len(runes)-1] == ' ' {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

// --- Plain Text ---

func TestFeed_PlainASCII(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "Hello")
	if got := rowText(buf, 0); got != "Hello" {
		t.Errorf("got %q, want %q", got, "Hello")
	}
	if buf.CursorCol != 5 {
		t.Errorf("cursor col = %d, want 5", buf.CursorCol)
	}
}

func TestFeed_Newline(t *testing.T) {
	p, buf := newTestParser(24, 80)
	// LF moves cursor down but not to column 0 (no implicit CR).
	feed(p, "AB\nCD")
	if got := rowText(buf, 0); got != "AB" {
		t.Errorf("row 0: got %q, want %q", got, "AB")
	}
	// 'C' and 'D' appear at cols 2,3 (cursor stayed at col 2 after LF).
	if c := buf.GetCell(1, 2).Char; c != 'C' {
		t.Errorf("row 1 col 2: got %c, want C", c)
	}
	if d := buf.GetCell(1, 3).Char; d != 'D' {
		t.Errorf("row 1 col 3: got %c, want D", d)
	}
}

func TestFeed_CarriageReturn(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "ABCD\rXY")
	if got := rowText(buf, 0); got != "XYCD" {
		t.Errorf("got %q, want %q", got, "XYCD")
	}
}

func TestFeed_Backspace(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "ABC\b\bXY")
	// Backspace moves cursor left; X overwrites B, Y overwrites C.
	if got := rowText(buf, 0); got != "AXY" {
		t.Errorf("got %q, want %q", got, "AXY")
	}
}

func TestFeed_Tab(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "A\tB")
	// Default tab stops at 8, so cursor goes to col 8.
	if buf.CursorCol != 9 { // col 8 for tab stop + 1 for 'B'
		t.Errorf("cursor col = %d, want 9", buf.CursorCol)
	}
	if got := buf.GetCell(0, 8).Char; got != 'B' {
		t.Errorf("char at col 8 = %c, want B", got)
	}
}

func TestFeed_LineWrap(t *testing.T) {
	p, buf := newTestParser(24, 5)
	feed(p, "ABCDEFGH")
	if got := rowText(buf, 0); got != "ABCDE" {
		t.Errorf("row 0: got %q, want %q", got, "ABCDE")
	}
	if got := rowText(buf, 1); got != "FGH" {
		t.Errorf("row 1: got %q, want %q", got, "FGH")
	}
}

func TestFeed_UTF8(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "café")
	if got := rowText(buf, 0); got != "café" {
		t.Errorf("got %q, want %q", got, "café")
	}
}

// --- Cursor Movement (CSI) ---

func TestCSI_CUP(t *testing.T) {
	p, buf := newTestParser(24, 80)
	// CUP: ESC[row;colH (1-based)
	feed(p, "\x1b[5;10H")
	if buf.CursorRow != 4 || buf.CursorCol != 9 {
		t.Errorf("cursor = (%d,%d), want (4,9)", buf.CursorRow, buf.CursorCol)
	}
}

func TestCSI_CUP_Default(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "XXX")
	feed(p, "\x1b[H") // default = 1;1
	if buf.CursorRow != 0 || buf.CursorCol != 0 {
		t.Errorf("cursor = (%d,%d), want (0,0)", buf.CursorRow, buf.CursorCol)
	}
}

func TestCSI_CUU(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[10;5H") // row 10, col 5
	feed(p, "\x1b[3A")    // up 3
	if buf.CursorRow != 6 {
		t.Errorf("cursor row = %d, want 6", buf.CursorRow)
	}
}

func TestCSI_CUU_Clamp(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[2;1H") // row 2
	feed(p, "\x1b[99A")  // up 99 — should clamp to row 0
	if buf.CursorRow != 0 {
		t.Errorf("cursor row = %d, want 0", buf.CursorRow)
	}
}

func TestCSI_CUD(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1;1H") // row 1
	feed(p, "\x1b[5B")   // down 5
	if buf.CursorRow != 5 {
		t.Errorf("cursor row = %d, want 5", buf.CursorRow)
	}
}

func TestCSI_CUD_Clamp(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[99B") // down 99 — clamp to row 23
	if buf.CursorRow != 23 {
		t.Errorf("cursor row = %d, want 23", buf.CursorRow)
	}
}

func TestCSI_CUF(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[10C") // forward 10
	if buf.CursorCol != 10 {
		t.Errorf("cursor col = %d, want 10", buf.CursorCol)
	}
}

func TestCSI_CUF_Clamp(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[999C") // clamp to 79
	if buf.CursorCol != 79 {
		t.Errorf("cursor col = %d, want 79", buf.CursorCol)
	}
}

func TestCSI_CUB(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1;20H") // col 20
	feed(p, "\x1b[5D")    // back 5
	if buf.CursorCol != 14 {
		t.Errorf("cursor col = %d, want 14", buf.CursorCol)
	}
}

func TestCSI_CUB_Clamp(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1;5H") // col 5
	feed(p, "\x1b[99D")  // back 99 — clamp to 0
	if buf.CursorCol != 0 {
		t.Errorf("cursor col = %d, want 0", buf.CursorCol)
	}
}

func TestCSI_CHA(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[15G") // horizontal absolute col 15
	if buf.CursorCol != 14 {
		t.Errorf("cursor col = %d, want 14", buf.CursorCol)
	}
}

func TestCSI_VPA(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[10d") // vertical absolute row 10
	if buf.CursorRow != 9 {
		t.Errorf("cursor row = %d, want 9", buf.CursorRow)
	}
}

func TestCSI_CNL(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1;10H") // row 1, col 10
	feed(p, "\x1b[3E")    // next line × 3
	if buf.CursorRow != 3 || buf.CursorCol != 0 {
		t.Errorf("cursor = (%d,%d), want (3,0)", buf.CursorRow, buf.CursorCol)
	}
}

func TestCSI_CPL(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[10;10H") // row 10, col 10
	feed(p, "\x1b[3F")     // prev line × 3
	if buf.CursorRow != 6 || buf.CursorCol != 0 {
		t.Errorf("cursor = (%d,%d), want (6,0)", buf.CursorRow, buf.CursorCol)
	}
}

// --- Erase ---

func TestCSI_ED_Below(t *testing.T) {
	p, buf := newTestParser(5, 10)
	feed(p, "AAAAAAAAAA") // fill row 0
	feed(p, "BBBBBBBBBB") // fill row 1
	feed(p, "\x1b[1;5H")  // row 1, col 5
	feed(p, "\x1b[0J")    // erase below (from cursor to end)
	// Row 0 cols 0-3 should be 'A', col 4 onward cleared.
	if got := rowText(buf, 0); got != "AAAA" {
		t.Errorf("row 0: got %q, want %q", got, "AAAA")
	}
	// Row 1 should be cleared.
	if got := rowText(buf, 1); got != "" {
		t.Errorf("row 1: got %q, want empty", got)
	}
}

func TestCSI_ED_Full(t *testing.T) {
	p, buf := newTestParser(3, 10)
	feed(p, "AAAAAAAAAA")
	feed(p, "BBBBBBBBBB")
	feed(p, "\x1b[2J") // erase entire display
	for row := 0; row < 3; row++ {
		if got := rowText(buf, row); got != "" {
			t.Errorf("row %d: got %q, want empty", row, got)
		}
	}
}

func TestCSI_EL(t *testing.T) {
	p, buf := newTestParser(24, 10)
	feed(p, "ABCDEFGHIJ")
	feed(p, "\x1b[1;5H") // col 5 (0-based col 4)
	feed(p, "\x1b[0K")   // erase to right
	if got := rowText(buf, 0); got != "ABCD" {
		t.Errorf("got %q, want %q", got, "ABCD")
	}
}

func TestCSI_EL_ToLeft(t *testing.T) {
	p, buf := newTestParser(24, 10)
	feed(p, "ABCDEFGHIJ")
	feed(p, "\x1b[1;5H") // col 5 (0-based col 4)
	feed(p, "\x1b[1K")   // erase to left (inclusive)
	// Cols 0-4 cleared, cols 5-9 remain.
	if got := rowText(buf, 0); got != "     FGHIJ" {
		t.Errorf("got %q, want %q", got, "     FGHIJ")
	}
}

// --- SGR (Select Graphic Rendition) ---

func TestCSI_SGR_Bold(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1mB\x1b[0mN")
	bold := buf.GetCell(0, 0)
	normal := buf.GetCell(0, 1)
	if !bold.Bold {
		t.Error("expected bold on first char")
	}
	if normal.Bold {
		t.Error("expected no bold on second char")
	}
}

func TestCSI_SGR_Italic(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[3mI\x1b[23mN")
	if !buf.GetCell(0, 0).Italic {
		t.Error("expected italic")
	}
	if buf.GetCell(0, 1).Italic {
		t.Error("expected no italic")
	}
}

func TestCSI_SGR_Underline(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[4mU\x1b[24mN")
	if !buf.GetCell(0, 0).Underline {
		t.Error("expected underline")
	}
	if buf.GetCell(0, 1).Underline {
		t.Error("expected no underline")
	}
}

func TestCSI_SGR_Strikethrough(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[9mS\x1b[29mN")
	if !buf.GetCell(0, 0).Strikethrough {
		t.Error("expected strikethrough")
	}
	if buf.GetCell(0, 1).Strikethrough {
		t.Error("expected no strikethrough")
	}
}

func TestCSI_SGR_FG_8Color(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[31mR") // red foreground
	cell := buf.GetCell(0, 0)
	if cell.FG != testPalette[1] && cell.FG.R == 0 {
		t.Errorf("expected red FG, got %v", cell.FG)
	}
}

func TestCSI_SGR_256Color(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[38;5;196mR") // 256-color: color 196 (bright red)
	cell := buf.GetCell(0, 0)
	// Color 196 in 256-palette = rgb(255,0,0)
	if cell.FG.R != 255 || cell.FG.G != 0 || cell.FG.B != 0 {
		t.Errorf("expected red (255,0,0), got (%d,%d,%d)", cell.FG.R, cell.FG.G, cell.FG.B)
	}
}

func TestCSI_SGR_TrueColor(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[38;2;100;200;50mX") // 24-bit: rgb(100,200,50)
	cell := buf.GetCell(0, 0)
	if cell.FG.R != 100 || cell.FG.G != 200 || cell.FG.B != 50 {
		t.Errorf("expected (100,200,50), got (%d,%d,%d)", cell.FG.R, cell.FG.G, cell.FG.B)
	}
}

func TestCSI_SGR_BG_TrueColor(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[48;2;10;20;30mX") // 24-bit BG
	cell := buf.GetCell(0, 0)
	if cell.BG.R != 10 || cell.BG.G != 20 || cell.BG.B != 30 {
		t.Errorf("expected BG (10,20,30), got (%d,%d,%d)", cell.BG.R, cell.BG.G, cell.BG.B)
	}
}

func TestCSI_SGR_Reset(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1;3;4mX\x1b[0mY")
	x := buf.GetCell(0, 0)
	y := buf.GetCell(0, 1)
	if !x.Bold || !x.Italic || !x.Underline {
		t.Error("X should have bold+italic+underline")
	}
	if y.Bold || y.Italic || y.Underline {
		t.Error("Y should have no attributes after reset")
	}
}

func TestCSI_SGR_Inverse(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[7mI\x1b[27mN")
	if !buf.GetCell(0, 0).Inverse {
		t.Error("expected inverse")
	}
	if buf.GetCell(0, 1).Inverse {
		t.Error("expected no inverse")
	}
}

// --- Insert / Delete ---

func TestCSI_DCH(t *testing.T) {
	p, buf := newTestParser(24, 10)
	feed(p, "ABCDEFGHIJ")
	feed(p, "\x1b[1;3H") // col 3 (0-based 2)
	feed(p, "\x1b[2P")   // delete 2 chars
	if got := rowText(buf, 0); got != "ABEFGHIJ" {
		t.Errorf("got %q, want %q", got, "ABEFGHIJ")
	}
}

func TestCSI_ICH(t *testing.T) {
	p, buf := newTestParser(24, 10)
	feed(p, "ABCDE")
	feed(p, "\x1b[1;3H") // col 3 (0-based 2)
	feed(p, "\x1b[2@")   // insert 2 blanks
	if got := rowText(buf, 0); got != "AB  CDE" {
		t.Errorf("got %q, want %q", got, "AB  CDE")
	}
}

// --- Scroll Region ---

func TestCSI_DECSTBM(t *testing.T) {
	p, buf := newTestParser(10, 10)
	feed(p, "\x1b[3;7r") // scroll region rows 3-7
	if buf.ScrollTop != 2 || buf.ScrollBottom != 6 {
		t.Errorf("scroll region = [%d,%d], want [2,6]", buf.ScrollTop, buf.ScrollBottom)
	}
}

// --- Save / Restore Cursor ---

func TestCSI_SaveRestoreCursor(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[5;10H") // move to row 5, col 10
	feed(p, "\x1b[s")     // save
	feed(p, "\x1b[1;1H")  // move to 1,1
	feed(p, "\x1b[u")     // restore
	if buf.CursorRow != 4 || buf.CursorCol != 9 {
		t.Errorf("cursor = (%d,%d), want (4,9)", buf.CursorRow, buf.CursorCol)
	}
}

// --- REP (Repeat) ---

func TestCSI_REP(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "A\x1b[3b") // print 'A' then repeat 3 times
	if got := rowText(buf, 0); got != "AAAA" {
		t.Errorf("got %q, want %q", got, "AAAA")
	}
}

// --- ECH (Erase Characters) ---

func TestCSI_ECH(t *testing.T) {
	p, buf := newTestParser(24, 10)
	feed(p, "ABCDEFGHIJ")
	feed(p, "\x1b[1;3H") // col 3 (0-based 2)
	feed(p, "\x1b[4X")   // erase 4 chars from cursor
	if got := rowText(buf, 0); got != "AB    GHIJ" {
		t.Errorf("got %q, want %q", got, "AB    GHIJ")
	}
}

// --- Scroll ---

func TestCSI_ScrollUp(t *testing.T) {
	p, buf := newTestParser(5, 10)
	// Use CR+LF to place each letter at col 0.
	feed(p, "A\r\nB\r\nC\r\nD\r\nE")
	// Rows: A, B, C, D, E — each at col 0.
	feed(p, "\x1b[1S") // scroll up 1
	if got := buf.GetCell(0, 0).Char; got != 'B' {
		t.Errorf("row 0 col 0: got %c, want B", got)
	}
	if got := buf.GetCell(3, 0).Char; got != 'E' {
		t.Errorf("row 3 col 0: got %c, want E", got)
	}
	if got := rowText(buf, 4); got != "" {
		t.Errorf("row 4: got %q, want empty", got)
	}
}

// --- OSC ---

func TestOSC_Title(t *testing.T) {
	titleCh := make(chan string, 1)
	buf := newTestBuffer(24, 80)
	p := NewParser(buf, titleCh, nil, nil, nil, nil, nil)
	feed(p, "\x1b]0;My Title\x07")
	select {
	case title := <-titleCh:
		if title != "My Title" {
			t.Errorf("title = %q, want %q", title, "My Title")
		}
	default:
		t.Error("expected title on channel")
	}
}

func TestOSC_Title_ST(t *testing.T) {
	titleCh := make(chan string, 1)
	buf := newTestBuffer(24, 80)
	p := NewParser(buf, titleCh, nil, nil, nil, nil, nil)
	feed(p, "\x1b]2;Title Two\x1b\\")
	select {
	case title := <-titleCh:
		if title != "Title Two" {
			t.Errorf("title = %q, want %q", title, "Title Two")
		}
	default:
		t.Error("expected title on channel")
	}
}

func TestOSC_CWD(t *testing.T) {
	cwdCh := make(chan string, 1)
	buf := newTestBuffer(24, 80)
	p := NewParser(buf, nil, cwdCh, nil, nil, nil, nil)
	feed(p, "\x1b]7;file:///Users/test/project\x07")
	select {
	case cwd := <-cwdCh:
		if cwd != "/Users/test/project" {
			t.Errorf("cwd = %q, want %q", cwd, "/Users/test/project")
		}
	default:
		t.Error("expected cwd on channel")
	}
}

func TestOSC_Bell(t *testing.T) {
	bellCh := make(chan struct{}, 1)
	buf := newTestBuffer(24, 80)
	p := NewParser(buf, nil, nil, bellCh, nil, nil, nil)
	feed(p, "\x07") // BEL
	select {
	case <-bellCh:
		// ok
	default:
		t.Error("expected bell signal")
	}
}

// --- Partial / Split Sequences ---

func TestFeed_SplitEscape(t *testing.T) {
	p, buf := newTestParser(24, 80)
	// Feed an escape sequence in two parts.
	feed(p, "\x1b")
	feed(p, "[5;10H")
	if buf.CursorRow != 4 || buf.CursorCol != 9 {
		t.Errorf("cursor = (%d,%d), want (4,9)", buf.CursorRow, buf.CursorCol)
	}
}

func TestFeed_SplitUTF8(t *testing.T) {
	p, buf := newTestParser(24, 80)
	// é = 0xC3 0xA9 — feed one byte at a time.
	p.Feed([]byte{0xC3})
	p.Feed([]byte{0xA9})
	if got := rowText(buf, 0); got != "é" {
		t.Errorf("got %q, want %q", got, "é")
	}
}

// --- Alternate Screen ---

func TestDECSET_AlternateScreen(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "Main screen")
	feed(p, "\x1b[?1049h") // switch to alternate
	if !buf.altActive {
		t.Error("expected alternate screen active")
	}
	feed(p, "\x1b[?1049l") // switch back
	if buf.altActive {
		t.Error("expected main screen active")
	}
	if got := rowText(buf, 0); got != "Main screen" {
		t.Errorf("main screen content: got %q, want %q", got, "Main screen")
	}
}

// --- Cursor Visibility ---

func TestDECSET_CursorVisibility(t *testing.T) {
	p, buf := newTestParser(24, 80)
	if !buf.CursorVisible {
		t.Error("cursor should be visible by default")
	}
	feed(p, "\x1b[?25l") // hide
	if buf.CursorVisible {
		t.Error("cursor should be hidden")
	}
	feed(p, "\x1b[?25h") // show
	if !buf.CursorVisible {
		t.Error("cursor should be visible again")
	}
}

// --- Combined Sequences ---

func TestCombined_TextAndCursor(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "Hello\x1b[2;1HWorld")
	if got := rowText(buf, 0); got != "Hello" {
		t.Errorf("row 0: got %q, want %q", got, "Hello")
	}
	if got := rowText(buf, 1); got != "World" {
		t.Errorf("row 1: got %q, want %q", got, "World")
	}
}

func TestCombined_ClearAndWrite(t *testing.T) {
	p, buf := newTestParser(5, 10)
	feed(p, "XXXXXXXXXX")
	feed(p, "\x1b[2J")    // clear screen
	feed(p, "\x1b[1;1H")  // home
	feed(p, "Clean")
	if got := rowText(buf, 0); got != "Clean" {
		t.Errorf("got %q, want %q", got, "Clean")
	}
}

func TestCombined_SGRAndText(t *testing.T) {
	p, buf := newTestParser(24, 80)
	feed(p, "\x1b[1;31mERROR\x1b[0m: ok")
	// 'E' should be bold + red palette
	e := buf.GetCell(0, 0)
	if !e.Bold {
		t.Error("E should be bold")
	}
	// ':' should not be bold
	colon := buf.GetCell(0, 5)
	if colon.Bold {
		t.Error(": should not be bold")
	}
}
