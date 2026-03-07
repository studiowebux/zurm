//go:build darwin

package voice

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Speech -framework AVFoundation
#include <stdlib.h>
#include "stt_bridge.h"
*/
import "C"

import "unsafe"

// Listener wraps SFSpeechRecognizer via the ObjC bridge.
// State lives entirely on the ObjC side (singleton).
type Listener struct{}

// InitListener initialises the SFSpeechRecognizer and checks authorization.
// Must be called once at startup.
func (l *Listener) InitListener() {
	C.stt_init()
}

// RequestAuthorization triggers the system permission dialog for speech recognition.
func (l *Listener) RequestAuthorization() {
	C.stt_request_authorization()
}

// StartListening begins audio capture and speech recognition.
func (l *Listener) StartListening() {
	C.stt_start_listening()
}

// StopListening stops audio capture and finalises the recognition task.
func (l *Listener) StopListening() {
	C.stt_stop_listening()
}

// IsListening returns true if speech recognition is actively running.
func (l *Listener) IsListening() bool {
	return C.stt_is_listening() != 0
}

// IsAuthorized returns true if the user has granted speech recognition permission.
func (l *Listener) IsAuthorized() bool {
	return C.stt_is_authorized() != 0
}

// GetTranscript returns the latest transcript text, whether it is final,
// and whether new transcript data is available since the last call.
// The hasNew flag is cleared on read (consumed).
func (l *Listener) GetTranscript() (text string, isFinal bool, hasNew bool) {
	hasNew = C.stt_has_new_transcript() != 0
	if !hasNew {
		return "", false, false
	}
	cStr := C.stt_get_transcript()
	text = C.GoString(cStr)
	C.free(unsafe.Pointer(cStr))
	isFinal = C.stt_is_final() != 0
	return text, isFinal, true
}

// SetLocale sets the recognition language (e.g. "en-US", "fr-CA").
// Must be called before StartListening to take effect.
func (l *Listener) SetLocale(locale string) {
	cLocale := C.CString(locale)
	defer C.free(unsafe.Pointer(cLocale))
	C.stt_set_locale(cLocale)
}
