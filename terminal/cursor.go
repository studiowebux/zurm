package terminal

import "time"

// CursorStyle defines the visual shape of the cursor.
type CursorStyle int

const (
	CursorBlock     CursorStyle = iota // filled block (default)
	CursorUnderline                    // underline bar
	CursorBar                          // vertical bar (I-beam)
)

const cursorBlinkPeriod = 530 * time.Millisecond

// Cursor tracks cursor visibility, style, and blink state.
type Cursor struct {
	Style   CursorStyle
	Visible bool

	blinkOn      bool
	blinkPeriod  time.Duration
	lastToggle   time.Time
}

// NewCursor returns a visible steady block cursor (no blink).
func NewCursor() *Cursor {
	return &Cursor{
		Style:      CursorBlock,
		Visible:    true,
		blinkOn:    true,
		lastToggle: time.Now(),
		// blinkPeriod 0 = steady; call EnableBlink() to turn on blinking.
	}
}

// EnableBlink turns on cursor blinking at the standard 530 ms period.
func (c *Cursor) EnableBlink() {
	c.blinkPeriod = cursorBlinkPeriod
}

// SetBlink enables or disables cursor blinking.
func (c *Cursor) SetBlink(on bool) {
	if on {
		c.blinkPeriod = cursorBlinkPeriod
	} else {
		c.blinkPeriod = 0
		c.blinkOn = true
	}
}

// Update advances the blink animation. Call once per Ebitengine Update().
// Returns true when blinkOn toggled — caller should mark the screen dirty.
func (c *Cursor) Update() bool {
	if c.blinkPeriod == 0 {
		return false // steady cursor — never blinks
	}
	if time.Since(c.lastToggle) >= c.blinkPeriod {
		c.blinkOn = !c.blinkOn
		c.lastToggle = time.Now()
		return true
	}
	return false
}

// IsVisible reports whether the cursor should be drawn this frame.
func (c *Cursor) IsVisible() bool {
	return c.Visible && c.blinkOn
}

// SetStyle applies a DECSCUSR cursor style code (CSI <n> SP q).
//   0,1 → blinking block
//   2   → steady block
//   3   → blinking underline
//   4   → steady underline
//   5   → blinking bar
//   6   → steady bar
func (c *Cursor) SetStyle(n int) {
	blink := true
	switch n {
	case 0, 1:
		c.Style = CursorBlock
	case 2:
		c.Style = CursorBlock
		blink = false
	case 3:
		c.Style = CursorUnderline
	case 4:
		c.Style = CursorUnderline
		blink = false
	case 5:
		c.Style = CursorBar
	case 6:
		c.Style = CursorBar
		blink = false
	default:
		c.Style = CursorBlock
	}
	if blink {
		c.blinkPeriod = cursorBlinkPeriod
	} else {
		c.blinkPeriod = 0
		c.blinkOn = true // always visible for steady cursor
	}
}
