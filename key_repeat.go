package main

import (
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// KeyRepeatHandler implements OS-style key repeat: fire on initial press,
// then after keyRepeatDelay fire every keyRepeatInterval while held.
// Shared by PaletteController, ExplorerController, and tab search.
type KeyRepeatHandler struct {
	key    ebiten.Key
	active bool
	start  time.Time
	last   time.Time
}

// Update checks whether the key should fire this frame.
// pressed/wasPressed are the current and previous frame states.
// Returns true on initial press or repeat tick.
func (kr *KeyRepeatHandler) Update(key ebiten.Key, pressed, wasPressed bool, now time.Time) bool {
	if !pressed {
		if kr.active && kr.key == key {
			kr.active = false
		}
		return false
	}
	if !wasPressed {
		kr.key = key
		kr.active = true
		kr.start = now
		kr.last = now
		return true
	}
	if kr.active && kr.key == key &&
		now.Sub(kr.start) >= keyRepeatDelay &&
		now.Sub(kr.last) >= keyRepeatInterval {
		kr.last = now
		return true
	}
	return false
}

// Reset clears the repeat state.
func (kr *KeyRepeatHandler) Reset() {
	kr.active = false
}
