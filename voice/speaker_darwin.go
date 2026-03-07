//go:build darwin

package voice

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AVFoundation
#include <stdlib.h>
#include "tts_bridge.h"
*/
import "C"

import "unsafe"

// VoiceInfo describes an available system voice.
type VoiceInfo struct {
	Name     string
	ID       string
	Language string
}

// Speaker wraps AVSpeechSynthesizer via the ObjC bridge.
// State lives entirely on the ObjC side (singleton).
type Speaker struct{}

// Init initialises the AVSpeechSynthesizer and caches the voice list.
// Must be called once at startup.
func (s *Speaker) Init() {
	C.tts_init()
}

// Speak reads text aloud. Stops any current speech first.
// voiceID is the AVSpeechSynthesisVoice identifier (empty = system default).
// rate: 0.0–1.0 (AVSpeechUtteranceDefaultSpeechRate ~0.5).
// pitch: 0.5–2.0 (1.0 = normal).
// volume: 0.0–1.0 (1.0 = full).
func (s *Speaker) Speak(text, voiceID string, rate, pitch, volume float64) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	var cVoice *C.char
	if voiceID != "" {
		cVoice = C.CString(voiceID)
		defer C.free(unsafe.Pointer(cVoice))
	}
	C.tts_speak(cText, cVoice, C.float(rate), C.float(pitch), C.float(volume))
}

// Stop cancels any active speech immediately.
func (s *Speaker) Stop() {
	C.tts_stop()
}

// Pause pauses active speech at the current word boundary.
func (s *Speaker) Pause() {
	C.tts_pause()
}

// Continue resumes paused speech.
func (s *Speaker) Continue() {
	C.tts_continue()
}

// Active returns true if speech is in progress (including paused).
func (s *Speaker) Active() bool {
	return C.tts_is_speaking() != 0
}

// Paused returns true if speech is paused.
func (s *Speaker) Paused() bool {
	return C.tts_is_paused() != 0
}

// ListVoices returns all available system voices.
func (s *Speaker) ListVoices() []VoiceInfo {
	n := int(C.tts_voice_count())
	voices := make([]VoiceInfo, 0, n)
	for i := 0; i < n; i++ {
		ci := C.int(i)
		voices = append(voices, VoiceInfo{
			Name:     C.GoString(C.tts_voice_name(ci)),
			ID:       C.GoString(C.tts_voice_id(ci)),
			Language: C.GoString(C.tts_voice_language(ci)),
		})
	}
	return voices
}
