package terminal

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync/atomic"
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
	stateDCS                      // received ESC P (device control string — collected and dispatched)
	stateIgnore                   // collecting ignored sequence
)

const (
	maxDCSBuffer = 4096 // maximum bytes collected in a DCS string
	maxOSCBuffer = 4096 // maximum bytes collected in an OSC string
)

// formatOSCColor formats an OSC 10/11 color query response.
func formatOSCColor(code int, c color.RGBA) []byte {
	return []byte(fmt.Sprintf("\x1B]%d;rgb:%04x/%04x/%04x\x1B\\",
		code,
		uint16(c.R)<<8|uint16(c.R),
		uint16(c.G)<<8|uint16(c.G),
		uint16(c.B)<<8|uint16(c.B)))
}

// Parser processes raw PTY bytes and applies them to a ScreenBuffer.
// All methods must be called with the ScreenBuffer write lock held.
type Parser struct {
	sb      *ScreenBuffer
	palette [16]color.RGBA

	state      parserState
	params     []int
	paramStr   string
	oscBuf  strings.Builder
	titleCh      chan<- string       // optional: notified on OSC 0/2 title changes
	cwdCh        chan<- string       // optional: notified on OSC 7 CWD changes
	bellCh       chan<- struct{}     // optional: notified on BEL (0x07) in ground state
	shellIntCh   chan<- byte         // optional: notified on OSC 133 shell integration (A/B/C/D)
	osc7Active   *atomic.Bool       // optional: set true on first OSC 7 receipt; disables lsof fallback
	osc133Active *atomic.Bool       // optional: set true on first OSC 133 receipt; disables ps polling

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
// osc7Active is optional (may be nil); when non-nil it is set to true on the
// first OSC 7 receipt, signalling that lsof-based CWD polling can be skipped.
func NewParser(sb *ScreenBuffer, titleCh chan<- string, cwdCh chan<- string, bellCh chan<- struct{}, shellIntCh chan<- byte, osc7Active, osc133Active *atomic.Bool) *Parser {
	p := &Parser{
		sb:           sb,
		palette:      sb.Palette,
		titleCh:      titleCh,
		cwdCh:        cwdCh,
		bellCh:       bellCh,
		shellIntCh:   shellIntCh,
		osc7Active:   osc7Active,
		osc133Active: osc133Active,
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
		} else if p.dcsBuf.Len() < maxDCSBuffer {
			p.dcsBuf.WriteByte(b)
		}
	case stateIgnore:
		// Expected: trailing '\' of a 7-bit ST (ESC \). Dispatch was already called.
		// If it IS '\', consume it. Otherwise the ESC started a new sequence —
		// return to ground and re-process the byte so it isn't dropped.
		p.state = stateGround
		if b != '\\' {
			p.consume(b)
			return
		}
	}
}

func (p *Parser) ground(b byte) {
	switch {
	case b == 0x1B:
		p.utf8Len = 0 // discard any incomplete multi-byte sequence
		p.state = stateEscape
	case b == 0x07: // BEL
		p.utf8Len = 0
		if p.bellCh != nil {
			select {
			case p.bellCh <- struct{}{}:
			default:
			}
		}
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
	case b >= 0x80 && b <= 0x9F && p.utf8Len == 0: // C1 control bytes — only when NOT mid-UTF-8
		switch b {
		case 0x84: // IND — index (same as ESC D)
			p.sb.LineFeed()
		case 0x85: // NEL — next line (same as ESC E)
			p.sb.CursorCol = 0
			p.sb.LineFeed()
		case 0x8D: // RI — reverse index (same as ESC M)
			if p.sb.CursorRow == p.sb.ScrollTop {
				p.sb.ScrollDown(1)
			} else if p.sb.CursorRow > 0 {
				p.sb.CursorRow--
			}
		case 0x90: // DCS — device control string
			p.dcsBuf.Reset()
			p.state = stateDCS
		case 0x9B: // CSI — same as ESC [
			p.paramStr = ""
			p.params = nil
			p.csiIntermByte = 0
			p.state = stateCSIEntry
		case 0x9C: // ST — string terminator (terminates OSC/DCS)
			// No-op in ground state; handled inline for OSC/DCS.
		case 0x9D: // OSC — same as ESC ]
			p.oscBuf.Reset()
			p.state = stateOSC
		}
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
	case '8': // DECRC — restore cursor (clamp to current grid after resize)
		p.sb.CursorRow = p.sb.savedCursorRow
		p.sb.CursorCol = p.sb.savedCursorCol
		if p.sb.CursorRow >= p.sb.Rows {
			p.sb.CursorRow = p.sb.Rows - 1
		}
		if p.sb.CursorCol >= p.sb.Cols {
			p.sb.CursorCol = p.sb.Cols - 1
		}
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
		// Parse params now — they weren't parsed on transition to stateCSIInterm.
		p.params = parseParams(p.paramStr)
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
		if p.oscBuf.Len() < maxOSCBuffer {
			p.oscBuf.WriteByte(b)
		}
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
		// Mark shell as OSC 7-capable even if this particular URI fails to parse —
		// the shell supports the protocol, so lsof polling is unnecessary.
		if p.osc7Active != nil {
			p.osc7Active.Store(true)
		}
		if path := parseOSC7Path(value); path != "" {
			if p.cwdCh != nil {
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
			p.sb.PendingDCSResponses = append(p.sb.PendingDCSResponses, formatOSCColor(10, p.sb.DefaultFG))
		}
	case "11": // background color query — nvim uses this to adapt theme colors
		if value == "?" {
			p.sb.PendingDCSResponses = append(p.sb.PendingDCSResponses, formatOSCColor(11, p.sb.DefaultBG))
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
		// Signal shell integration event to the game loop so it can update
		// the foreground process name without polling.
		if p.osc133Active != nil {
			p.osc133Active.Store(true)
		}
		if p.shellIntCh != nil && (kind == 'A' || kind == 'C' || kind == 'D') {
			select {
			case p.shellIntCh <- byte(kind):
			default:
			}
		}
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

// csiParam returns the i-th CSI parameter, or def if absent or zero.
func csiParam(params []int, i, def int) int {
	if i < len(params) && params[i] != 0 {
		return params[i]
	}
	return def
}

// dispatchCSI routes a complete CSI sequence to the appropriate handler.
func (p *Parser) dispatchCSI(final byte, private, secondary, kittyPop bool) {
	sb := p.sb
	params := p.params

	if kittyPop {
		p.dispatchCSIKittyPop(final, params)
		return
	}
	if secondary {
		p.dispatchCSISecondary(final, params)
		return
	}
	if private {
		p.dispatchCSIPrivate(final, params)
		return
	}

	switch final {
	case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'f', 'I', 'd':
		p.dispatchCSICursor(final, params)
	case 'J', 'K', 'L', 'M', 'P', 'S', 'T', 'X', '@':
		p.dispatchCSIEdit(final, params)
	case 'm': // SGR — select graphic rendition
		p.applySGR(params)
	case 'b': // REP — repeat last printed character
		n := csiParam(params, 0, 1)
		for i := 0; i < n; i++ {
			p.sb.PutChar(p.lastChar)
		}
	case 'c': // DA1 — primary device attributes
		sb.PendingDA1 = true
	case 'n': // DSR — device status report
		if csiParam(params, 0, 0) == 6 {
			sb.PendingCPR = true
		}
	case 'r': // DECSTBM — set scroll region
		top := csiParam(params, 0, 1)
		bot := csiParam(params, 1, sb.Rows)
		sb.SetScrollRegion(top, bot)
	case 's': // SCP — save cursor position
		sb.savedCursorRow = sb.CursorRow
		sb.savedCursorCol = sb.CursorCol
	case 'u': // RCP — restore cursor position (clamp to current grid after resize)
		sb.CursorRow = sb.savedCursorRow
		sb.CursorCol = sb.savedCursorCol
		if sb.CursorRow >= sb.Rows {
			sb.CursorRow = sb.Rows - 1
		}
		if sb.CursorCol >= sb.Cols {
			sb.CursorCol = sb.Cols - 1
		}
	case 'h': // SM — set mode (public)
		// e.g. CSI 4 h (insert mode) — mostly ignored for now
	case 'l': // RM — reset mode (public)
	}
}

// dispatchCSIKittyPop handles CSI < n u — pop n entries from the Kitty keyboard stack.
func (p *Parser) dispatchCSIKittyPop(final byte, params []int) {
	if final != 'u' {
		return
	}
	n := csiParam(params, 0, 1)
	if n >= len(p.sb.kittyStack) {
		p.sb.kittyStack = p.sb.kittyStack[:0]
	} else {
		p.sb.kittyStack = p.sb.kittyStack[:len(p.sb.kittyStack)-n]
	}
}

// dispatchCSISecondary handles CSI > ... sequences (DA2, Kitty push).
func (p *Parser) dispatchCSISecondary(final byte, params []int) {
	sb := p.sb
	switch final {
	case 'c': // DA2 — secondary device attributes
		sb.PendingDA2 = true
	case 'u': // Kitty push: CSI > flags ; mode u
		flags := csiParam(params, 0, 0)
		mode := csiParam(params, 1, 1)
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
}

// dispatchCSIPrivate handles CSI ? ... sequences (private mode set/reset, Kitty query).
func (p *Parser) dispatchCSIPrivate(final byte, params []int) {
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
		p.sb.PendingKittyQuery = true
	}
}

// dispatchCSICursor handles cursor-positioning CSI sequences (CUU/CUD/CUF/CUB/CNL/CPL/CHA/CUP/HVP/CHT/VPA).
func (p *Parser) dispatchCSICursor(final byte, params []int) {
	sb := p.sb
	switch final {
	case 'A': // CUU — cursor up (clamp to scroll top when inside region)
		n := csiParam(params, 0, 1)
		top := 0
		if sb.CursorRow >= sb.ScrollTop && sb.CursorRow <= sb.ScrollBottom {
			top = sb.ScrollTop
		}
		sb.CursorRow -= n
		if sb.CursorRow < top {
			sb.CursorRow = top
		}
	case 'B': // CUD — cursor down (clamp to scroll bottom when inside region)
		n := csiParam(params, 0, 1)
		bot := sb.Rows - 1
		if sb.CursorRow >= sb.ScrollTop && sb.CursorRow <= sb.ScrollBottom {
			bot = sb.ScrollBottom
		}
		sb.CursorRow += n
		if sb.CursorRow > bot {
			sb.CursorRow = bot
		}
	case 'C': // CUF — cursor forward
		n := csiParam(params, 0, 1)
		sb.CursorCol += n
		if sb.CursorCol >= sb.Cols {
			sb.CursorCol = sb.Cols - 1
		}
	case 'D': // CUB — cursor back
		n := csiParam(params, 0, 1)
		sb.CursorCol -= n
		if sb.CursorCol < 0 {
			sb.CursorCol = 0
		}
	case 'E': // CNL — cursor next line
		n := csiParam(params, 0, 1)
		sb.CursorRow += n
		if sb.CursorRow >= sb.Rows {
			sb.CursorRow = sb.Rows - 1
		}
		sb.CursorCol = 0
	case 'F': // CPL — cursor previous line
		n := csiParam(params, 0, 1)
		sb.CursorRow -= n
		if sb.CursorRow < 0 {
			sb.CursorRow = 0
		}
		sb.CursorCol = 0
	case 'G': // CHA — cursor horizontal absolute
		col := csiParam(params, 0, 1) - 1
		if col < 0 {
			col = 0
		}
		if col >= sb.Cols {
			col = sb.Cols - 1
		}
		sb.CursorCol = col
	case 'H', 'f': // CUP / HVP — cursor position
		row := csiParam(params, 0, 1) - 1
		col := csiParam(params, 1, 1) - 1
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
		n := csiParam(params, 0, 1)
		for i := 0; i < n; i++ {
			p.advanceTab()
		}
	case 'd': // VPA — vertical line position absolute
		row := csiParam(params, 0, 1) - 1
		if row < 0 {
			row = 0
		}
		if row >= sb.Rows {
			row = sb.Rows - 1
		}
		sb.CursorRow = row
	}
}

// dispatchCSIEdit handles editing CSI sequences (ED/EL/IL/DL/DCH/SU/SD/ECH/ICH).
func (p *Parser) dispatchCSIEdit(final byte, params []int) {
	sb := p.sb
	switch final {
	case 'J': // ED — erase in display
		sb.EraseInDisplay(csiParam(params, 0, 0))
	case 'K': // EL — erase in line
		sb.EraseInLine(csiParam(params, 0, 0))
	case 'L': // IL — insert lines
		sb.InsertLines(csiParam(params, 0, 1))
	case 'M': // DL — delete lines
		sb.DeleteLines(csiParam(params, 0, 1))
	case 'P': // DCH — delete characters
		sb.DeleteChars(csiParam(params, 0, 1))
	case 'S': // SU — scroll up
		sb.ScrollUp(csiParam(params, 0, 1))
	case 'T': // SD — scroll down
		sb.ScrollDown(csiParam(params, 0, 1))
	case 'X': // ECH — erase characters
		n := csiParam(params, 0, 1)
		for i := 0; i < n; i++ {
			col := sb.CursorCol + i
			if col >= sb.Cols {
				break
			}
			sb.clearWideOverlap(sb.CursorRow, col)
			sb.SetCell(sb.CursorRow, col, Cell{
				Char: ' ', Width: 1, FG: sb.DefaultFG, BG: sb.DefaultBG,
			})
		}
		sb.dirty[sb.CursorRow] = true
	case '@': // ICH — insert characters
		sb.InsertChars(csiParam(params, 0, 1))
	}
}

func (p *Parser) setPrivateMode(mode int, enable bool) {
	switch mode {
	case 47, 1047, 1049: // alternate screen buffer
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
		if v > 65535 {
			v = 65535
		}
		out = append(out, v)
	}
	return out
}

