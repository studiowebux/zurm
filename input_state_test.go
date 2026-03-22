package main

import (
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// --- KeyRepeatHandler ---

func TestKeyRepeatHandler_InitialPress(t *testing.T) {
	kr := KeyRepeatHandler{}
	now := time.Now()
	got := kr.Update(ebiten.KeyA, true, false, now)
	if !got {
		t.Error("should fire on initial press")
	}
	if !kr.active {
		t.Error("should be active after press")
	}
}

func TestKeyRepeatHandler_HoldNoRepeatBeforeDelay(t *testing.T) {
	kr := KeyRepeatHandler{}
	now := time.Now()
	kr.Update(ebiten.KeyA, true, false, now) // initial press

	// Hold for less than delay — should NOT fire
	got := kr.Update(ebiten.KeyA, true, true, now.Add(keyRepeatDelay-time.Millisecond))
	if got {
		t.Error("should not fire before delay elapses")
	}
}

func TestKeyRepeatHandler_HoldRepeatsAfterDelay(t *testing.T) {
	kr := KeyRepeatHandler{}
	now := time.Now()
	kr.Update(ebiten.KeyA, true, false, now)

	// Hold past delay — should fire
	got := kr.Update(ebiten.KeyA, true, true, now.Add(keyRepeatDelay+keyRepeatInterval))
	if !got {
		t.Error("should fire after delay + interval")
	}
}

func TestKeyRepeatHandler_Release(t *testing.T) {
	kr := KeyRepeatHandler{}
	now := time.Now()
	kr.Update(ebiten.KeyA, true, false, now)

	got := kr.Update(ebiten.KeyA, false, true, now.Add(10*time.Millisecond))
	if got {
		t.Error("should not fire on release")
	}
	if kr.active {
		t.Error("should be inactive after release")
	}
}

func TestKeyRepeatHandler_DifferentKeyResets(t *testing.T) {
	kr := KeyRepeatHandler{}
	now := time.Now()
	kr.Update(ebiten.KeyA, true, false, now)

	// Press B while A is active — B fires as initial press
	got := kr.Update(ebiten.KeyB, true, false, now.Add(10*time.Millisecond))
	if !got {
		t.Error("new key should fire on initial press")
	}
	if kr.key != ebiten.KeyB {
		t.Errorf("key = %v, want KeyB", kr.key)
	}
}

func TestKeyRepeatHandler_Reset(t *testing.T) {
	kr := KeyRepeatHandler{}
	now := time.Now()
	kr.Update(ebiten.KeyA, true, false, now)
	kr.Reset()

	if kr.active {
		t.Error("should be inactive after Reset")
	}
}

// --- TextInput ---

func TestTextInput_AddChar(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 1}
	ti.AddChar('X')
	if ti.Text != "aXbc" {
		t.Errorf("Text = %q, want %q", ti.Text, "aXbc")
	}
	if ti.CursorPos != 2 {
		t.Errorf("CursorPos = %d, want 2", ti.CursorPos)
	}
}

func TestTextInput_AddChar_ControlCharRejected(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 3}
	ti.AddChar(0x01) // SOH control char
	if ti.Text != "abc" {
		t.Errorf("control char should be rejected, got %q", ti.Text)
	}
}

func TestTextInput_AddChar_AtStart(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 0}
	ti.AddChar('Z')
	if ti.Text != "Zabc" {
		t.Errorf("Text = %q, want %q", ti.Text, "Zabc")
	}
}

func TestTextInput_AddChar_AtEnd(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 3}
	ti.AddChar('Z')
	if ti.Text != "abcZ" {
		t.Errorf("Text = %q, want %q", ti.Text, "abcZ")
	}
}

func TestTextInput_DeleteLastChar(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 2}
	ti.DeleteLastChar()
	if ti.Text != "ac" {
		t.Errorf("Text = %q, want %q", ti.Text, "ac")
	}
	if ti.CursorPos != 1 {
		t.Errorf("CursorPos = %d, want 1", ti.CursorPos)
	}
}

func TestTextInput_DeleteLastChar_AtStart(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 0}
	ti.DeleteLastChar()
	if ti.Text != "abc" {
		t.Errorf("should not delete at start, got %q", ti.Text)
	}
}

func TestTextInput_DeleteWord(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		cursor    int
		wantText  string
		wantPos   int
	}{
		{"word at end", "hello world", 11, "hello ", 6},
		{"word after spaces", "hello  world", 12, "hello  ", 7},
		{"single word", "hello", 5, "", 0},
		{"empty string", "", 0, "", 0},
		{"symbols", "hello---", 8, "hello", 5},
		{"mid word", "hello world", 7, "hello orld", 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ti := TextInput{Text: tt.text, CursorPos: tt.cursor}
			ti.DeleteWord()
			if ti.Text != tt.wantText {
				t.Errorf("Text = %q, want %q", ti.Text, tt.wantText)
			}
			if ti.CursorPos != tt.wantPos {
				t.Errorf("CursorPos = %d, want %d", ti.CursorPos, tt.wantPos)
			}
		})
	}
}

func TestTextInput_MoveLeft(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 2}
	ti.MoveLeft()
	if ti.CursorPos != 1 {
		t.Errorf("CursorPos = %d, want 1", ti.CursorPos)
	}
}

func TestTextInput_MoveLeft_AtStart(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 0}
	ti.MoveLeft()
	if ti.CursorPos != 0 {
		t.Errorf("CursorPos should stay 0, got %d", ti.CursorPos)
	}
}

func TestTextInput_MoveRight(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 1}
	ti.MoveRight()
	if ti.CursorPos != 2 {
		t.Errorf("CursorPos = %d, want 2", ti.CursorPos)
	}
}

func TestTextInput_MoveRight_AtEnd(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 3}
	ti.MoveRight()
	if ti.CursorPos != 3 {
		t.Errorf("CursorPos should stay 3, got %d", ti.CursorPos)
	}
}

func TestTextInput_MoveToStart(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 2}
	ti.MoveToStart()
	if ti.CursorPos != 0 {
		t.Errorf("CursorPos = %d, want 0", ti.CursorPos)
	}
}

func TestTextInput_MoveToEnd(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 0}
	ti.MoveToEnd()
	if ti.CursorPos != 3 {
		t.Errorf("CursorPos = %d, want 3", ti.CursorPos)
	}
}

func TestTextInput_AddString(t *testing.T) {
	ti := TextInput{Text: "ac", CursorPos: 1}
	ti.AddString("XY")
	if ti.Text != "aXYc" {
		t.Errorf("Text = %q, want %q", ti.Text, "aXYc")
	}
	if ti.CursorPos != 3 {
		t.Errorf("CursorPos = %d, want 3", ti.CursorPos)
	}
}

func TestTextInput_AddString_ControlCharsStripped(t *testing.T) {
	ti := TextInput{Text: "", CursorPos: 0}
	ti.AddString("a\x01b\x7fc")
	if ti.Text != "abc" {
		t.Errorf("Text = %q, want %q (control chars stripped)", ti.Text, "abc")
	}
}

func TestTextInput_Clear(t *testing.T) {
	ti := TextInput{Text: "hello", CursorPos: 3}
	ti.Clear()
	if ti.Text != "" || ti.CursorPos != 0 {
		t.Errorf("Clear: Text=%q CursorPos=%d, want empty/0", ti.Text, ti.CursorPos)
	}
}

func TestTextInput_WithCursor(t *testing.T) {
	tests := []struct {
		text   string
		cursor int
		want   string
	}{
		{"abc", 0, "|abc"},
		{"abc", 1, "a|bc"},
		{"abc", 3, "abc|"},
		{"", 0, "|"},
	}
	for _, tt := range tests {
		ti := TextInput{Text: tt.text, CursorPos: tt.cursor}
		got := ti.WithCursor()
		if got != tt.want {
			t.Errorf("WithCursor(%q, %d) = %q, want %q", tt.text, tt.cursor, got, tt.want)
		}
	}
}

func TestTextInput_ClampCursor_Negative(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: -5}
	ti.AddChar('X')
	if ti.CursorPos < 0 {
		t.Errorf("cursor should be clamped to >=0, got %d", ti.CursorPos)
	}
}

func TestTextInput_ClampCursor_Overflow(t *testing.T) {
	ti := TextInput{Text: "abc", CursorPos: 100}
	ti.DeleteLastChar()
	// Should clamp to len(runes) before deleting
	if ti.Text != "ab" {
		t.Errorf("Text = %q, want %q (cursor clamped to end before delete)", ti.Text, "ab")
	}
}

func TestTextInput_Unicode(t *testing.T) {
	ti := TextInput{Text: "héllo", CursorPos: 2}
	ti.AddChar('X')
	if ti.Text != "héXllo" {
		t.Errorf("Text = %q, want %q", ti.Text, "héXllo")
	}
}

// --- SelectionDragger ---

func TestSelectionDragger_Lifecycle(t *testing.T) {
	sd := SelectionDragger{}
	if sd.Active {
		t.Error("should start inactive")
	}
	sd.Active = true
	if !sd.Active {
		t.Error("should be active after set")
	}
	sd.Active = false
	if sd.Active {
		t.Error("should be inactive after clear")
	}
}

// --- Scroll Accumulation ---
// Tests the fractional accumulation pattern used in handleMouse and handleInput.

func TestScrollAccumulation(t *testing.T) {
	var accum float64
	linesPerTick := 3.0

	// Small delta — not enough for a full line
	accum += 0.3 * linesPerTick // 0.9
	lines := int(accum)
	if lines != 0 {
		t.Errorf("should not scroll yet, got %d lines", lines)
	}

	// Another small delta — crosses threshold
	accum += 0.1 * linesPerTick // 0.9 + 0.3 = 1.2
	lines = int(accum)
	if lines != 1 {
		t.Errorf("should scroll 1 line, got %d", lines)
	}
	accum -= float64(lines) // 0.2 remainder preserved

	if accum < 0.1 || accum > 0.3 {
		t.Errorf("remainder should be ~0.2, got %f", accum)
	}

	// Large delta
	accum += 5.0 * linesPerTick // 0.2 + 15 = 15.2
	lines = int(accum)
	if lines != 15 {
		t.Errorf("should scroll 15 lines, got %d", lines)
	}
}

func TestScrollAccumulation_NegativeDirection(t *testing.T) {
	var accum float64
	linesPerTick := 3.0

	accum += -0.5 * linesPerTick // -1.5
	lines := int(accum)          // -1 (truncation toward zero)
	if lines != -1 {
		t.Errorf("should scroll -1 line, got %d", lines)
	}
	accum -= float64(lines) // -0.5 remainder

	if accum > -0.4 || accum < -0.6 {
		t.Errorf("remainder should be ~-0.5, got %f", accum)
	}
}

// --- Click Detection Pattern ---
// Tests the double/triple click detection pattern used in handleMouseSelection.

// simulateClick applies the click-detection logic and returns the updated state.
func simulateClick(row, col int, now time.Time, lastRow, lastCol int, lastTime time.Time, count int, timeout time.Duration) (int, int, time.Time, int) {
	sameCell := row == lastRow && col == lastCol
	if sameCell && now.Sub(lastTime) <= timeout {
		count++
	} else {
		count = 1
	}
	return row, col, now, count
}

func TestClickDetection_SingleClick(t *testing.T) {
	timeout := 300 * time.Millisecond
	now := time.Now()
	_, _, _, count := simulateClick(5, 10, now, 0, 0, time.Time{}, 0, timeout)
	if count != 1 {
		t.Errorf("first click: count = %d, want 1", count)
	}
}

func TestClickDetection_DoubleClick(t *testing.T) {
	timeout := 300 * time.Millisecond
	now := time.Now()

	r, c, lt, count := simulateClick(5, 10, now, 0, 0, time.Time{}, 0, timeout)
	_, _, _, count = simulateClick(5, 10, now.Add(100*time.Millisecond), r, c, lt, count, timeout)

	if count != 2 {
		t.Errorf("double click: count = %d, want 2", count)
	}
}

func TestClickDetection_TripleClick(t *testing.T) {
	timeout := 300 * time.Millisecond
	now := time.Now()

	r, c, lt, count := simulateClick(5, 10, now, 0, 0, time.Time{}, 0, timeout)
	r, c, lt, count = simulateClick(5, 10, now.Add(50*time.Millisecond), r, c, lt, count, timeout)
	_, _, _, count = simulateClick(5, 10, now.Add(100*time.Millisecond), r, c, lt, count, timeout)

	if count != 3 {
		t.Errorf("triple click: count = %d, want 3", count)
	}
}

func TestClickDetection_DifferentCellResets(t *testing.T) {
	timeout := 300 * time.Millisecond
	now := time.Now()

	r, c, lt, count := simulateClick(5, 10, now, 0, 0, time.Time{}, 0, timeout)
	_, _, _, count = simulateClick(6, 10, now.Add(50*time.Millisecond), r, c, lt, count, timeout)

	if count != 1 {
		t.Errorf("different cell: count = %d, want 1 (reset)", count)
	}
}

func TestClickDetection_TimeoutResets(t *testing.T) {
	timeout := 300 * time.Millisecond
	now := time.Now()

	r, c, lt, count := simulateClick(5, 10, now, 0, 0, time.Time{}, 0, timeout)
	_, _, _, count = simulateClick(5, 10, now.Add(500*time.Millisecond), r, c, lt, count, timeout)

	if count != 1 {
		t.Errorf("timeout: count = %d, want 1 (reset)", count)
	}
}
