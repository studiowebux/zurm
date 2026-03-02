package terminal

import (
	"github.com/hajimehoshi/ebiten/v2"
)

// KeyToBytes maps an Ebitengine key event to the byte sequence sent to the PTY.
// Returns nil if the key produces no output (e.g. modifier-only keys).
func KeyToBytes(key ebiten.Key, mods ebiten.Key) []byte {
	return nil // handled by KeyEventToBytes below
}

// KeyEventToBytes converts the current key state into PTY input bytes.
// appCursor selects application cursor key sequences (DECCKM mode 1).
// Called once per Ebitengine Update() frame when a key is newly pressed.
func KeyEventToBytes(key ebiten.Key, appCursor bool) []byte {
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	// alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

	// Ctrl + letter
	if ctrl && !shift {
		if seq := ctrlKey(key); seq != nil {
			return seq
		}
	}

	switch key {
	case ebiten.KeyEnter, ebiten.KeyNumpadEnter:
		return []byte{'\r'}
	case ebiten.KeyBackspace:
		return []byte{0x7F}
	case ebiten.KeyTab:
		if shift {
			return []byte{0x1B, '[', 'Z'} // backtab
		}
		return []byte{'\t'}
	case ebiten.KeyEscape:
		return []byte{0x1B}
	case ebiten.KeySpace:
		if ctrl {
			return []byte{0x00} // Ctrl+Space → NUL
		}
		return []byte{' '}
	case ebiten.KeyArrowUp:
		if appCursor {
			return []byte{0x1B, 'O', 'A'}
		}
		return []byte{0x1B, '[', 'A'}
	case ebiten.KeyArrowDown:
		if appCursor {
			return []byte{0x1B, 'O', 'B'}
		}
		return []byte{0x1B, '[', 'B'}
	case ebiten.KeyArrowRight:
		if appCursor {
			return []byte{0x1B, 'O', 'C'}
		}
		return []byte{0x1B, '[', 'C'}
	case ebiten.KeyArrowLeft:
		if appCursor {
			return []byte{0x1B, 'O', 'D'}
		}
		return []byte{0x1B, '[', 'D'}
	case ebiten.KeyHome:
		if appCursor {
			return []byte{0x1B, 'O', 'H'}
		}
		return []byte{0x1B, '[', 'H'}
	case ebiten.KeyEnd:
		if appCursor {
			return []byte{0x1B, 'O', 'F'}
		}
		return []byte{0x1B, '[', 'F'}
	case ebiten.KeyPageUp:
		return []byte{0x1B, '[', '5', '~'}
	case ebiten.KeyPageDown:
		return []byte{0x1B, '[', '6', '~'}
	case ebiten.KeyInsert:
		return []byte{0x1B, '[', '2', '~'}
	case ebiten.KeyDelete:
		return []byte{0x1B, '[', '3', '~'}
	case ebiten.KeyF1:
		return []byte{0x1B, 'O', 'P'}
	case ebiten.KeyF2:
		return []byte{0x1B, 'O', 'Q'}
	case ebiten.KeyF3:
		return []byte{0x1B, 'O', 'R'}
	case ebiten.KeyF4:
		return []byte{0x1B, 'O', 'S'}
	case ebiten.KeyF5:
		return []byte{0x1B, '[', '1', '5', '~'}
	case ebiten.KeyF6:
		return []byte{0x1B, '[', '1', '7', '~'}
	case ebiten.KeyF7:
		return []byte{0x1B, '[', '1', '8', '~'}
	case ebiten.KeyF8:
		return []byte{0x1B, '[', '1', '9', '~'}
	case ebiten.KeyF9:
		return []byte{0x1B, '[', '2', '0', '~'}
	case ebiten.KeyF10:
		return []byte{0x1B, '[', '2', '1', '~'}
	case ebiten.KeyF11:
		return []byte{0x1B, '[', '2', '3', '~'}
	case ebiten.KeyF12:
		return []byte{0x1B, '[', '2', '4', '~'}
	}

	return nil
}

// optionToBase maps US keyboard Option+key composed characters to the base
// character the key produces without Option. Used to implement left-Option-as-Meta
// on macOS, where Option+letter routes through the IME and arrives as a composed
// character via AppendInputChars rather than as a raw key press.
//
// Dead keys (Option+E acute, Option+I circumflex, Option+N tilde, Option+U umlaut)
// do not produce a single character and are omitted — they require a follow-up key.
var optionToBase = map[rune]rune{
	'å': 'a', '∫': 'b', 'ç': 'c', '∂': 'd',
	'ƒ': 'f', '©': 'g', '˙': 'h', '∆': 'j',
	'˚': 'k', '¬': 'l', 'µ': 'm', 'ø': 'o',
	'π': 'p', 'œ': 'q', '®': 'r', 'ß': 's',
	'†': 't', '√': 'v', '∑': 'w', '≈': 'x',
	'¥': 'y', 'Ω': 'z',
	'¡': '1', '™': '2', '£': '3', '¢': '4',
	'∞': '5', '§': '6', '¶': '7', '•': '8',
	'ª': '9', 'º': '0',
	'…': '.', '–': '-', '≠': '=', '÷': '/',
}

// MetaFromChar maps a character produced by AppendInputChars while left Option
// is held to the ESC-prefixed Meta sequence for the underlying key.
// Returns nil when the character has no mapping (dead-key follow-up, non-US layout,
// or characters with no meaningful Meta binding).
func MetaFromChar(r rune) []byte {
	if base, ok := optionToBase[r]; ok {
		return []byte{0x1b, byte(base)} // #nosec G115 — base is an ASCII control code (0-127)
	}
	return nil
}

// ctrlKey returns the control-character byte for a letter key, or nil.
func ctrlKey(key ebiten.Key) []byte {
	letterMap := map[ebiten.Key]byte{
		ebiten.KeyA: 0x01,
		ebiten.KeyB: 0x02,
		ebiten.KeyC: 0x03,
		ebiten.KeyD: 0x04,
		ebiten.KeyE: 0x05,
		ebiten.KeyF: 0x06,
		ebiten.KeyG: 0x07,
		ebiten.KeyH: 0x08,
		ebiten.KeyI: 0x09, // also Tab
		ebiten.KeyJ: 0x0A,
		ebiten.KeyK: 0x0B,
		ebiten.KeyL: 0x0C,
		ebiten.KeyM: 0x0D, // also Enter
		ebiten.KeyN: 0x0E,
		ebiten.KeyO: 0x0F,
		ebiten.KeyP: 0x10,
		ebiten.KeyQ: 0x11,
		ebiten.KeyR: 0x12,
		ebiten.KeyS: 0x13,
		ebiten.KeyT: 0x14,
		ebiten.KeyU: 0x15,
		ebiten.KeyV: 0x16,
		ebiten.KeyW: 0x17,
		ebiten.KeyX: 0x18,
		ebiten.KeyY: 0x19,
		ebiten.KeyZ: 0x1A,
		ebiten.KeyBackslash:  0x1C,
		ebiten.KeyBracketRight: 0x1D,
	}
	if b, ok := letterMap[key]; ok {
		return []byte{b}
	}
	return nil
}
