package main

import (
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/renderer"
)

// PaletteController owns command palette state (Cmd+P) and navigation logic.
// Game holds a *PaletteController and delegates palette operations to it.
type PaletteController struct {
	State   renderer.PaletteState
	Entries []renderer.PaletteEntry
	Actions []func()

	// Key repeat for arrow navigation.
	repeatKey    ebiten.Key
	repeatActive bool
	repeatStart  time.Time
	repeatLast   time.Time

	// Text input repeat for backspace/cursor in the query field.
	inputRepeat TextInputRepeat
}

// NewPaletteController creates a ready-to-use palette controller.
func NewPaletteController() *PaletteController {
	return &PaletteController{}
}

// Open activates the palette and resets query state.
func (pc *PaletteController) Open() {
	pc.State = renderer.PaletteState{Open: true}
	pc.repeatActive = false
}

// Close deactivates the palette and clears state.
func (pc *PaletteController) Close() {
	pc.State = renderer.PaletteState{}
}

// UpdateRepeat implements OS-style key repeat for palette arrow keys.
// Returns true when the key should fire (initial press or repeat tick).
func (pc *PaletteController) UpdateRepeat(key ebiten.Key, pressed, wasPressed bool, now time.Time) bool {
	if !pressed {
		if pc.repeatActive && pc.repeatKey == key {
			pc.repeatActive = false
		}
		return false
	}
	if !wasPressed {
		pc.repeatKey = key
		pc.repeatActive = true
		pc.repeatStart = now
		pc.repeatLast = now
		return true
	}
	if pc.repeatActive && pc.repeatKey == key &&
		now.Sub(pc.repeatStart) >= keyRepeatDelay &&
		now.Sub(pc.repeatLast) >= keyRepeatInterval {
		pc.repeatLast = now
		return true
	}
	return false
}
