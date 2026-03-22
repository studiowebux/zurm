package main

import (
	"github.com/studiowebux/zurm/renderer"
)

// PaletteController owns command palette state (Cmd+P) and navigation logic.
// Game holds a *PaletteController and delegates palette operations to it.
type PaletteController struct {
	State   renderer.PaletteState
	Entries []renderer.PaletteEntry
	Actions []func()

	// Key repeat for arrow navigation.
	repeat KeyRepeatHandler

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
	pc.repeat.Reset()
}

// Close deactivates the palette and clears state.
func (pc *PaletteController) Close() {
	pc.State = renderer.PaletteState{}
}
