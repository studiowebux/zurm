package main

import (
	"strings"
	"unicode"
)

// TextInput provides reusable text editing functionality with word deletion support
type TextInput struct {
	Text string
}

// DeleteLastChar removes the last character from the text
func (t *TextInput) DeleteLastChar() {
	runes := []rune(t.Text)
	if len(runes) > 0 {
		t.Text = string(runes[:len(runes)-1])
	}
}

// DeleteWord removes the last word from the text (alt+backspace behavior)
func (t *TextInput) DeleteWord() {
	if t.Text == "" {
		return
	}

	runes := []rune(t.Text)
	n := len(runes)

	// Skip trailing spaces
	i := n - 1
	for i >= 0 && unicode.IsSpace(runes[i]) {
		i--
	}

	// If we're at a word boundary, delete the word
	if i >= 0 {
		// Check what kind of character we're at
		if unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) {
			// Delete alphanumeric word
			for i >= 0 && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
				i--
			}
		} else {
			// Delete sequence of same punctuation/symbols
			char := runes[i]
			for i >= 0 && runes[i] == char && !unicode.IsSpace(runes[i]) {
				i--
			}
		}
	}

	t.Text = string(runes[:i+1])
}

// Clear empties the text
func (t *TextInput) Clear() {
	t.Text = ""
}

// AddChar adds a character to the text
func (t *TextInput) AddChar(r rune) {
	if r >= 0x20 && r != 0x7f {
		t.Text += string(r)
	}
}

// AddString adds a string to the text (for paste operations)
func (t *TextInput) AddString(s string) {
	// Filter out control characters
	var result strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			result.WriteRune(r)
		}
	}
	t.Text += result.String()
}

