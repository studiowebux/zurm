package terminal

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Parser state machine states following the VT500 parser model.
type parserState int

const (
	stateGround       parserState = iota
	stateEscape                   // received ESC
	stateEscapeInterm             // received ESC + intermediate byte
	stateCSIEntry                 // received ESC [
	stateCSIParam                 // collecting CSI parameters
	stateCSIInterm                // CSI intermediate byte
	stateOSC                      // received ESC ]
	stateDCS                      // received ESC P (device control string — ignored)
	stateIgnore                   // collecting ignored sequence
)

// Parser processes raw PTY bytes and applies them to a ScreenBuffer.
// All methods must be called with the ScreenBuffer write lock held.
type Parser struct {
	sb      *ScreenBuffer
	palette [16]color.RGBA

	state      parserState
	params     []int
	paramStr   string
	oscBuf  strings.Builder
	titleCh chan<- string // optional: notified on OSC 0/2 title changes
	cwdCh   chan<- string // optional: notified on OSC 7 CWD changes

	// Tab stops (every 8 columns by default).
	tabStops []bool

	// UTF-8 carry buffer — accumulates bytes of a multi-byte sequence.
	utf8Buf [4]byte
	utf8Len int

	// lastChar is the most recently printed rune, used by the REP sequence (CSI b).
	lastChar rune

	// csiIntermByte holds the intermediate byte (0x20–0x2F) seen in a CSI sequence.
	// Used to dispatch DECSCUSR (CSI <n> SP q) and other intermediate-byte sequences.
	csiIntermByte byte

	// dcsBuf collects DCS string content (between ESC P and ST).
	// Dispatched by dispatchDCS() on string termination.
	dcsBuf strings.Builder
}

// NewParser creates a parser attached to the given buffer.
func NewParser(sb *ScreenBuffer, titleCh chan<- string, cwdCh chan<- string) *Parser {
	p := &Parser{
		sb:      sb,
		palette: sb.Palette,
		titleCh: titleCh,
		cwdCh:   cwdCh,
	}
	p.resetTabStops()
	return p
}

func (p *Parser) resetTabStops() {
	p.tabStops = make([]bool, p.sb.Cols)
	for i := 8; i < len(p.tabStops); i += 8 {
		p.tabStops[i] = true
	}
}

// Feed processes a slice of bytes from the PTY.
func (p *Parser) Feed(data []byte) {
	for _, b := range data {
		p.consume(b)
	}
}

func (p *Parser) consume(b byte) {
	switch p.state {
	case stateGround:
		p.ground(b)
	case stateEscape:
		p.escape(b)
	case stateEscapeInterm:
		p.escapeInterm(b)
	case stateCSIEntry:
		p.csiEntry(b)
	case stateCSIParam:
		p.csiParam(b)
	case stateCSIInterm:
		p.csiInterm(b)
	case stateOSC:
		p.osc(b)
	case stateDCS:
		// Collect DCS string content into dcsBuf until ST.
		if b == 0x07 || b == 0x9C { // BEL or C1 ST
			p.dispatchDCS()
			p.state = stateGround
		} else if b == 0x1B { // ESC — start of ESC \ (7-bit ST); dispatch now, consume '\' next
			p.dispatchDCS()
			p.state = stateIgnore
		} else {
			p.dcsBuf.WriteByte(b)
		}
	case stateIgnore:
		// Consume the trailing '\' of a 7-bit ST (ESC \) and return to ground.
		// Dispatch was already called before transitioning here.
		p.state = stateGround
	}
}

func (p *Parser) ground(b byte) {
	switch {
	case b == 0x1B:
		p.utf8Len = 0 // discard any incomplete multi-byte sequence
		p.state = stateEscape
	case b == 0x07: // BEL — ignore
		p.utf8Len = 0
	case b == 0x08: // BS
		p.utf8Len = 0
		if p.sb.CursorCol > 0 {
			p.sb.CursorCol--
		}
	case b == 0x09: // HT — tab
		p.utf8Len = 0
		p.advanceTab()
	case b == 0x0A || b == 0x0B || b == 0x0C: // LF/VT/FF
		p.utf8Len = 0
		p.sb.LineFeed()
	case b == 0x0D: // CR
		p.utf8Len = 0
		p.sb.CursorCol = 0
	case b == 0x0E || b == 0x0F: // SO/SI — charset switching, ignored
		p.utf8Len = 0
	case b >= 0x80: // high byte — accumulate UTF-8 multi-byte sequence
		p.utf8Buf[p.utf8Len] = b
		p.utf8Len++
		if utf8.FullRune(p.utf8Buf[:p.utf8Len]) {
			r, size := utf8.DecodeRune(p.utf8Buf[:p.utf8Len])
			p.utf8Len = 0
			// size==1 with RuneError means invalid byte — discard.
			if !(r == utf8.RuneError && size == 1) {
				p.lastChar = r
				p.sb.PutChar(r)
			}
		} else if p.utf8Len >= 4 {
			p.utf8Len = 0 // overlong / invalid sequence — discard
		}
	case b >= 0x20: // printable ASCII (0x20–0x7F)
		p.utf8Len = 0
		p.lastChar = rune(b)
		p.sb.PutChar(rune(b))
	}
}

func (p *Parser) advanceTab() {
	col := p.sb.CursorCol + 1
	for col < p.sb.Cols {
		if col < len(p.tabStops) && p.tabStops[col] {
			break
		}
		col++
	}
	if col >= p.sb.Cols {
		col = p.sb.Cols - 1
	}
	p.sb.CursorCol = col
}

func (p *Parser) escape(b byte) {
	switch b {
	case '[': // CSI
		p.paramStr = ""
		p.params = nil
		p.csiIntermByte = 0
		p.state = stateCSIEntry
	case ']': // OSC
		p.oscBuf.Reset()
		p.state = stateOSC
	case 'P': // DCS — collect string content, dispatch on ST
		p.dcsBuf.Reset()
		p.state = stateDCS
	case 'c': // RIS — full reset
		p.fullReset()
		p.state = stateGround
	case 'D': // IND — index (line feed)
		p.sb.LineFeed()
		p.state = stateGround
	case 'M': // RI — reverse index (scroll down)
		if p.sb.CursorRow == p.sb.ScrollTop {
			p.sb.ScrollDown(1)
		} else if p.sb.CursorRow > 0 {
			p.sb.CursorRow--
		}
		p.state = stateGround
	case 'E': // NEL — next line
		p.sb.CursorCol = 0
		p.sb.LineFeed()
		p.state = stateGround
	case '7': // DECSC — save cursor
		p.sb.savedCursorRow = p.sb.CursorRow
		p.sb.savedCursorCol = p.sb.CursorCol
		p.state = stateGround
	case '8': // DECRC — restore cursor
		p.sb.CursorRow = p.sb.savedCursorRow
		p.sb.CursorCol = p.sb.savedCursorCol
		p.state = stateGround
	case '(', ')': // Charset designation — consume next byte
		p.state = stateEscapeInterm
	default:
		p.state = stateGround
	}
}

func (p *Parser) escapeInterm(b byte) {
	// Charset designation byte — ignore content, just return to ground.
	p.state = stateGround
}

func (p *Parser) csiEntry(b byte) {
	switch {
	case b >= '0' && b <= '9' || b == ';' || b == ':':
		p.paramStr += string(rune(b))
		p.state = stateCSIParam
	case b == '?': // private mode prefix
		p.paramStr = "?"
		p.state = stateCSIParam
	case b == '>': // secondary DA / Kitty push prefix
		p.paramStr = ">"
		p.state = stateCSIParam
	case b == '<': // Kitty keyboard pop prefix
		p.paramStr = "<"
		p.state = stateCSIParam
	case b >= 0x20 && b <= 0x2F: // intermediate byte (e.g. SP before 'q' in DECSCUSR)
		p.csiIntermByte = b
		p.state = stateCSIInterm
	case b >= 0x40 && b <= 0x7E: // final byte
		p.params = parseParams(p.paramStr)
		p.dispatchCSI(b, false, false, false)
		p.state = stateGround
	default:
		p.state = stateGround
	}
}

func (p *Parser) csiParam(b byte) {
	switch {
	case b >= '0' && b <= '9' || b == ';' || b == ':':
		p.paramStr += string(rune(b))
	case b == '?' || b == '>' || b == '<':
		// prefix already recorded in paramStr; skip
	case b >= 0x20 && b <= 0x2F: // intermediate byte
		p.csiIntermByte = b
		p.state = stateCSIInterm
	case b >= 0x40 && b <= 0x7E: // final byte
		isPrivate := strings.HasPrefix(p.paramStr, "?")
		isSecondary := strings.HasPrefix(p.paramStr, ">")
		isKittyPop := strings.HasPrefix(p.paramStr, "<")
		rawParams := p.paramStr
		if isPrivate || isSecondary || isKittyPop {
			rawParams = rawParams[1:]
		}
		p.params = parseParams(rawParams)
		p.dispatchCSI(b, isPrivate, isSecondary, isKittyPop)
		p.state = stateGround
	default:
		p.state = stateGround
	}
}

func (p *Parser) csiInterm(b byte) {
	if b >= 0x20 && b <= 0x2F {
		p.csiIntermByte = b // update with latest intermediate byte
		return
	}
	if b >= 0x40 && b <= 0x7E {
		// DECSCUSR: CSI <n> SP q — set cursor style.
		if p.csiIntermByte == 0x20 && b == 'q' {
			n := 0
			if len(p.params) > 0 {
				n = p.params[0]
			}
			p.sb.CursorStyleCode = n
		}
		p.csiIntermByte = 0
		p.state = stateGround
	}
}

func (p *Parser) osc(b byte) {
	switch b {
	case 0x07: // BEL terminates OSC
		p.dispatchOSC(p.oscBuf.String())
		p.state = stateGround
	case 0x1B: // start of ESC \ (ST); dispatch now, consume '\' next
		p.dispatchOSC(p.oscBuf.String())
		p.state = stateIgnore
	default:
		p.oscBuf.WriteByte(b)
	}
}

func (p *Parser) dispatchOSC(s string) {
	idx := strings.IndexByte(s, ';')
	if idx < 0 {
		return
	}
	code := s[:idx]
	value := s[idx+1:]
	switch code {
	case "0", "2": // set window title
		if p.titleCh != nil {
			select {
			case p.titleCh <- value:
			default:
			}
		}
	case "7": // working directory — OSC 7 ; file://hostname/path
		if p.cwdCh != nil {
			if path := parseOSC7Path(value); path != "" {
				select {
				case p.cwdCh <- path:
				default:
				}
			}
		}
	case "52": // clipboard — OSC 52 ; Pc ; Pd
		// value = "Pc;Pd": Pc = target ("c" = clipboard), Pd = base64 data or "?"
		sep := strings.IndexByte(value, ';')
		if sep < 0 {
			return
		}
		pd := value[sep+1:]
		if pd == "?" {
			p.sb.PendingClipboardQuery = true
		} else if len(pd) > 0 {
			data, err := base64.StdEncoding.DecodeString(pd)
			if err != nil {
				// Try raw (no padding) encoding as some apps omit padding.
				data, err = base64.RawStdEncoding.DecodeString(pd)
			}
			if err == nil && len(data) > 0 {
				p.sb.PendingClipboardWrite = append(p.sb.PendingClipboardWrite, data)
			}
		}
	case "10": // foreground color query
		if value == "?" {
			fg := p.sb.DefaultFG
			resp := fmt.Sprintf("\x1B]10;rgb:%04x/%04x/%04x\x1B\\",
				uint16(fg.R)<<8|uint16(fg.R),
				uint16(fg.G)<<8|uint16(fg.G),
				uint16(fg.B)<<8|uint16(fg.B))
			p.sb.PendingDCSResponses = append(p.sb.PendingDCSResponses, []byte(resp))
		}
	case "11": // background color query — nvim uses this to adapt theme colors
		if value == "?" {
			bg := p.sb.DefaultBG
			resp := fmt.Sprintf("\x1B]11;rgb:%04x/%04x/%04x\x1B\\",
				uint16(bg.R)<<8|uint16(bg.R),
				uint16(bg.G)<<8|uint16(bg.G),
				uint16(bg.B)<<8|uint16(bg.B))
			p.sb.PendingDCSResponses = append(p.sb.PendingDCSResponses, []byte(resp))
		}
	case "133": // FinalTerm / iTerm2 shell integration
		// value is one of: "A" (prompt start), "B" (prompt end),
		// "C" (pre-execution), "D;exit_code" (post-execution).
		if len(value) == 0 {
			return
		}
		kind := rune(value[0])
		exitCode := -1
		if kind == 'D' {
			if semi := strings.IndexByte(value, ';'); semi >= 0 {
				if n, err := strconv.Atoi(value[semi+1:]); err == nil {
					exitCode = n
				}
			}
		}
		p.sb.applyBlockEvent(kind, exitCode)
	}
}

// parseOSC7Path extracts the filesystem path from an OSC 7 URI value.
// The value has the form "file://hostname/path" or "file:///path".
// Returns an empty string if the value is not a recognisable file URI.
func parseOSC7Path(value string) string {
	const prefix = "file://"
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	rest := value[len(prefix):]
	// rest is "hostname/path" or "/path" — skip to the first '/'
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	return rest[slash:]
}

// dispatchDCS handles a complete DCS string (content in dcsBuf).
// Currently handles XTGETTCAP (+q queries) by responding "not found".
func (p *Parser) dispatchDCS() {
	s := p.dcsBuf.String()
	p.dcsBuf.Reset()
	if len(s) < 2 {
		return
	}
	// XTGETTCAP: DCS +q <hex-encoded-name> ST
	// Respond: DCS 0 +r <hex-encoded-name> ST  (not found)
	if s[0] == '+' && s[1] == 'q' {
		resp := "\x1BP0+r" + s[2:] + "\x1B\\"
		p.sb.PendingDCSResponses = append(p.sb.PendingDCSResponses, []byte(resp))
	}
}

// dispatchCSI routes a complete CSI sequence to the appropriate handler.
func (p *Parser) dispatchCSI(final byte, private, secondary, kittyPop bool) {
	sb := p.sb
	params := p.params
	param := func(i, def int) int {
		if i < len(params) && params[i] != 0 {
			return params[i]
		}
		return def
	}

	// Kitty keyboard protocol: CSI < n u — pop n entries from stack.
	if kittyPop {
		if final == 'u' {
			n := param(0, 1)
			if n >= len(sb.kittyStack) {
				sb.kittyStack = sb.kittyStack[:0]
			} else {
				sb.kittyStack = sb.kittyStack[:len(sb.kittyStack)-n]
			}
		}
		return
	}

	// Secondary prefix: CSI > ...
	if secondary {
		switch final {
		case 'c': // DA2 — secondary device attributes
			sb.PendingDA2 = true
		case 'u': // Kitty push: CSI > flags ; mode u
			flags := param(0, 0)
			mode := param(1, 1)
			switch mode {
			case 1: // set
				sb.kittyStack = append(sb.kittyStack, flags)
			case 2: // or
				if len(sb.kittyStack) > 0 {
					sb.kittyStack[len(sb.kittyStack)-1] |= flags
				} else {
					sb.kittyStack = append(sb.kittyStack, flags)
				}
			case 3: // and-not
				if len(sb.kittyStack) > 0 {
					sb.kittyStack[len(sb.kittyStack)-1] &^= flags
				}
			}
		}
		return
	}

	if private {
		// Private mode sequences: ?<param>h / ?<param>l
		switch final {
		case 'h': // set private mode
			for _, v := range params {
				p.setPrivateMode(v, true)
			}
		case 'l': // reset private mode
			for _, v := range params {
				p.setPrivateMode(v, false)
			}
		case 'u': // Kitty keyboard query: CSI ? u → respond with current flags
			sb.PendingKittyQuery = true
		}
		return
	}

	switch final {
	case 'A': // CUU — cursor up
		n := param(0, 1)
		sb.CursorRow -= n
		if sb.CursorRow < 0 {
			sb.CursorRow = 0
		}
	case 'B': // CUD — cursor down
		n := param(0, 1)
		sb.CursorRow += n
		if sb.CursorRow >= sb.Rows {
			sb.CursorRow = sb.Rows - 1
		}
	case 'C': // CUF — cursor forward
		n := param(0, 1)
		sb.CursorCol += n
		if sb.CursorCol >= sb.Cols {
			sb.CursorCol = sb.Cols - 1
		}
	case 'D': // CUB — cursor back
		n := param(0, 1)
		sb.CursorCol -= n
		if sb.CursorCol < 0 {
			sb.CursorCol = 0
		}
	case 'E': // CNL — cursor next line
		n := param(0, 1)
		sb.CursorRow += n
		if sb.CursorRow >= sb.Rows {
			sb.CursorRow = sb.Rows - 1
		}
		sb.CursorCol = 0
	case 'F': // CPL — cursor previous line
		n := param(0, 1)
		sb.CursorRow -= n
		if sb.CursorRow < 0 {
			sb.CursorRow = 0
		}
		sb.CursorCol = 0
	case 'G': // CHA — cursor horizontal absolute
		col := param(0, 1) - 1
		if col < 0 {
			col = 0
		}
		if col >= sb.Cols {
			col = sb.Cols - 1
		}
		sb.CursorCol = col
	case 'H', 'f': // CUP / HVP — cursor position
		row := param(0, 1) - 1
		col := param(1, 1) - 1
		if row < 0 {
			row = 0
		}
		if col < 0 {
			col = 0
		}
		// DECOM origin mode: coordinates are relative to the scroll region.
		if sb.OriginMode {
			row += sb.ScrollTop
			if row > sb.ScrollBottom {
				row = sb.ScrollBottom
			}
		}
		if row >= sb.Rows {
			row = sb.Rows - 1
		}
		if col >= sb.Cols {
			col = sb.Cols - 1
		}
		sb.CursorRow = row
		sb.CursorCol = col
	case 'I': // CHT — cursor horizontal tab
		n := param(0, 1)
		for i := 0; i < n; i++ {
			p.advanceTab()
		}
	case 'J': // ED — erase in display
		sb.EraseInDisplay(param(0, 0))
	case 'K': // EL — erase in line
		sb.EraseInLine(param(0, 0))
	case 'L': // IL — insert lines
		sb.InsertLines(param(0, 1))
	case 'M': // DL — delete lines
		sb.DeleteLines(param(0, 1))
	case 'P': // DCH — delete characters
		sb.DeleteChars(param(0, 1))
	case 'S': // SU — scroll up
		sb.ScrollUp(param(0, 1))
	case 'T': // SD — scroll down
		sb.ScrollDown(param(0, 1))
	case 'X': // ECH — erase characters
		n := param(0, 1)
		for i := 0; i < n; i++ {
			sb.SetCell(sb.CursorRow, sb.CursorCol+i, Cell{
				Char: ' ', FG: sb.DefaultFG, BG: sb.DefaultBG,
			})
		}
		sb.dirty[sb.CursorRow] = true
	case '@': // ICH — insert characters
		sb.InsertChars(param(0, 1))
	case 'd': // VPA — vertical line position absolute
		row := param(0, 1) - 1
		if row < 0 {
			row = 0
		}
		if row >= sb.Rows {
			row = sb.Rows - 1
		}
		sb.CursorRow = row
	case 'm': // SGR — select graphic rendition
		p.applySGR(params)
	case 'b': // REP — repeat last printed character
		n := param(0, 1)
		for i := 0; i < n; i++ {
			p.sb.PutChar(p.lastChar)
		}
	case 'c': // DA1 — primary device attributes
		sb.PendingDA1 = true
	case 'n': // DSR — device status report
		if param(0, 0) == 6 {
			p.sb.PendingCPR = true
		}
	case 'r': // DECSTBM — set scroll region
		top := param(0, 1)
		bot := param(1, sb.Rows)
		sb.SetScrollRegion(top, bot)
	case 's': // SCP — save cursor position
		sb.savedCursorRow = sb.CursorRow
		sb.savedCursorCol = sb.CursorCol
	case 'u': // RCP — restore cursor position
		sb.CursorRow = sb.savedCursorRow
		sb.CursorCol = sb.savedCursorCol
	case 'h': // SM — set mode (public)
		// e.g. CSI 4 h (insert mode) — mostly ignored for now
	case 'l': // RM — reset mode (public)
	}
}

func (p *Parser) setPrivateMode(mode int, enable bool) {
	switch mode {
	case 1049: // alternate screen buffer
		if enable {
			p.sb.EnableAltScreen()
		} else {
			p.sb.DisableAltScreen()
		}
	case 1: // application cursor keys — \x1BOA/B/C/D instead of \x1B[A/B/C/D
		p.sb.AppCursorKeys = enable
	case 7: // auto-wrap mode
		p.sb.AutoWrap = enable
	case 25: // cursor visible
		p.sb.CursorVisible = enable
	case 2004: // bracketed paste
		p.sb.BracketedPaste = enable
	case 1000, 1002, 1003: // mouse button/motion tracking
		if enable {
			p.sb.MouseMode = mode
		} else if p.sb.MouseMode == mode {
			p.sb.MouseMode = 0
		}
	case 1006: // SGR extended mouse coordinates
		p.sb.SgrMouse = enable
	case 1004: // focus events — send \x1B[I on focus-in, \x1B[O on focus-out
		p.sb.FocusEvents = enable
	case 6: // DECOM — origin mode: CUP coords relative to scroll region
		p.sb.OriginMode = enable
		// Cursor moves to home position on mode change.
		if enable {
			p.sb.CursorRow = p.sb.ScrollTop
		} else {
			p.sb.CursorRow = 0
		}
		p.sb.CursorCol = 0
	}
}

// applySGR processes SGR parameters and updates the buffer's SGR state.
func (p *Parser) applySGR(params []int) {
	if len(params) == 0 {
		params = []int{0}
	}
	sb := p.sb

	i := 0
	for i < len(params) {
		v := params[i]
		switch {
		case v == 0: // reset
			sb.SGR = SGRState{FG: sb.DefaultFG, BG: sb.DefaultBG}
		case v == 1:
			sb.SGR.Bold = true
		case v == 3:
			sb.SGR.Italic = true
		case v == 4:
			sb.SGR.Underline = true
		case v == 7:
			sb.SGR.Inverse = true
		case v == 9:
			sb.SGR.Strikethrough = true
		case v == 22:
			sb.SGR.Bold = false
		case v == 23:
			sb.SGR.Italic = false
		case v == 24:
			sb.SGR.Underline = false
		case v == 27:
			sb.SGR.Inverse = false
		case v == 29:
			sb.SGR.Strikethrough = false
		case v >= 30 && v <= 37: // standard fg
			sb.SGR.FG = p.palette[v-30]
		case v == 38: // extended fg
			c, skip := p.parseExtendedColor(params, i+1)
			sb.SGR.FG = c
			i += skip
		case v == 39: // default fg
			sb.SGR.FG = sb.DefaultFG
		case v >= 40 && v <= 47: // standard bg
			sb.SGR.BG = p.palette[v-40]
		case v == 48: // extended bg
			c, skip := p.parseExtendedColor(params, i+1)
			sb.SGR.BG = c
			i += skip
		case v == 49: // default bg
			sb.SGR.BG = sb.DefaultBG
		case v >= 90 && v <= 97: // bright fg
			sb.SGR.FG = p.palette[v-90+8]
		case v >= 100 && v <= 107: // bright bg
			sb.SGR.BG = p.palette[v-100+8]
		case v == 58: // underline color
			c, skip := p.parseExtendedColor(params, i+1)
			sb.SGR.UnderlineColor = c
			sb.SGR.HasUnderlineColor = true
			i += skip
		case v == 59: // reset underline color
			sb.SGR.UnderlineColor = color.RGBA{}
			sb.SGR.HasUnderlineColor = false
		}
		i++
	}
}

// parseExtendedColor parses 256-color or 24-bit true-color SGR params.
// Returns the color and the number of additional params consumed.
func (p *Parser) parseExtendedColor(params []int, start int) (color.RGBA, int) {
	if start >= len(params) {
		return p.sb.DefaultFG, 0
	}
	switch params[start] {
	case 5: // 256-color: 38;5;N
		if start+1 >= len(params) {
			return p.sb.DefaultFG, 1
		}
		return p.color256(params[start+1]), 2
	case 2: // 24-bit: 38;2;R;G;B
		if start+3 >= len(params) {
			return p.sb.DefaultFG, 1
		}
		return color.RGBA{
			R: uint8(params[start+1]), // #nosec G115 — ANSI SGR color component, 0-255 by protocol
			G: uint8(params[start+2]), // #nosec G115
			B: uint8(params[start+3]), // #nosec G115
			A: 255,
		}, 4
	}
	return p.sb.DefaultFG, 0
}

// SetPalette replaces the parser's 16-color ANSI palette.
// Called during config hot-reload so indices 0-15 use the new theme colors.
func (p *Parser) SetPalette(palette [16]color.RGBA) {
	p.palette = palette
}

// color256 maps a 256-color index to RGBA.
// Indices 0-15 use the parser's configured palette instead of the hardcoded fallback.
func (p *Parser) color256(n int) color.RGBA {
	if n < 0 || n > 255 {
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	if n < 16 {
		return p.palette[n]
	}
	if n >= 232 { // grayscale ramp
		v := uint8(8 + (n-232)*10) // #nosec G115 — n in [232,255], max value = 8+23*10 = 238
		return color.RGBA{R: v, G: v, B: v, A: 255}
	}
	// 6x6x6 color cube: indices 16–231.
	n -= 16
	b := n % 6
	n /= 6
	g := n % 6
	r := n / 6
	toVal := func(x int) uint8 {
		if x == 0 {
			return 0
		}
		return uint8(55 + x*40) // #nosec G115 — x is 1-5 (6x6x6 cube), max = 55+5*40 = 255
	}
	return color.RGBA{R: toVal(r), G: toVal(g), B: toVal(b), A: 255}
}

func (p *Parser) fullReset() {
	sb := p.sb
	if sb.altActive {
		sb.DisableAltScreen()
	}
	sb.SGR = SGRState{FG: sb.DefaultFG, BG: sb.DefaultBG}
	sb.CursorRow = 0
	sb.CursorCol = 0
	sb.ScrollTop = 0
	sb.ScrollBottom = sb.Rows - 1
	sb.EraseInDisplay(2)
	p.resetTabStops()
}

// parseParams splits a CSI parameter string on ';' and converts to ints.
func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			out = append(out, 0)
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			v = 0
		}
		out = append(out, v)
	}
	return out
}

