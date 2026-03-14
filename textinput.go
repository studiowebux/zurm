package main

import (
	"strings"
	"unicode"
)

// TextInput provides reusable text editing functionality with a moveable
// cursor. CursorPos is a rune index into Text (0 = before first char,
// len(runes) = after last char). All edit operations work at CursorPos.
type TextInput struct {
	Text      string
	CursorPos int // rune index; clamped to [0, len(runes)] on every op
}

// clampCursor keeps CursorPos within valid bounds.
func (t *TextInput) clampCursor() {
	runes := []rune(t.Text)
	if t.CursorPos < 0 {
		t.CursorPos = 0
	}
	if t.CursorPos > len(runes) {
		t.CursorPos = len(runes)
	}
}

// DeleteLastChar removes the character immediately before the cursor.
func (t *TextInput) DeleteLastChar() {
	runes := []rune(t.Text)
	t.clampCursor()
	if t.CursorPos == 0 {
		return
	}
	t.Text = string(runes[:t.CursorPos-1]) + string(runes[t.CursorPos:])
	t.CursorPos--
}

// DeleteWord removes the word (and any preceding spaces) immediately before
// the cursor — Alt+Backspace behaviour.
func (t *TextInput) DeleteWord() {
	if t.Text == "" {
		return
	}

	runes := []rune(t.Text)
	t.clampCursor()
	i := t.CursorPos

	// Skip trailing spaces
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}

	if i > 0 {
		if unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1]) {
			for i > 0 && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				i--
			}
		} else {
			ch := runes[i-1]
			for i > 0 && runes[i-1] == ch && !unicode.IsSpace(runes[i-1]) {
				i--
			}
		}
	}

	t.Text = string(runes[:i]) + string(runes[t.CursorPos:])
	t.CursorPos = i
}

// Clear empties the text and resets the cursor.
func (t *TextInput) Clear() {
	t.Text = ""
	t.CursorPos = 0
}

// AddChar inserts a printable rune at the cursor position.
func (t *TextInput) AddChar(r rune) {
	if r < 0x20 || r == 0x7f {
		return
	}
	runes := []rune(t.Text)
	t.clampCursor()
	runes = append(runes[:t.CursorPos], append([]rune{r}, runes[t.CursorPos:]...)...)
	t.Text = string(runes)
	t.CursorPos++
}

// AddString inserts a filtered string (control chars stripped) at the cursor.
func (t *TextInput) AddString(s string) {
	var result strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			result.WriteRune(r)
		}
	}
	filtered := result.String()
	if filtered == "" {
		return
	}
	runes := []rune(t.Text)
	t.clampCursor()
	insert := []rune(filtered)
	runes = append(runes[:t.CursorPos], append(insert, runes[t.CursorPos:]...)...)
	t.Text = string(runes)
	t.CursorPos += len(insert)
}

// MoveLeft moves the cursor one rune to the left.
func (t *TextInput) MoveLeft() {
	t.clampCursor()
	if t.CursorPos > 0 {
		t.CursorPos--
	}
}

// MoveRight moves the cursor one rune to the right.
func (t *TextInput) MoveRight() {
	t.clampCursor()
	if t.CursorPos < len([]rune(t.Text)) {
		t.CursorPos++
	}
}

// MoveToStart moves the cursor to the beginning of the text.
func (t *TextInput) MoveToStart() {
	t.CursorPos = 0
}

// MoveToEnd moves the cursor to the end of the text.
func (t *TextInput) MoveToEnd() {
	t.CursorPos = len([]rune(t.Text))
}

// WithCursor returns the text with a block cursor character (|) inserted at
// the cursor position. Use this for rendering input fields.
func (t *TextInput) WithCursor() string {
	runes := []rune(t.Text)
	pos := t.CursorPos
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	return string(runes[:pos]) + "|" + string(runes[pos:])
}
