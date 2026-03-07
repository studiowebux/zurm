#import <Speech/Speech.h>
#import <AVFoundation/AVFoundation.h>
#include "stt_bridge.h"

// Dedicated lock object — never reassigned.
static NSObject *_sttLock = nil;

// Singleton recognizer, audio engine, and recognition state.
// Created lazily on first stt_start_listening() call (on main thread).
static SFSpeechRecognizer *_recognizer = nil;
static AVAudioEngine *_audioEngine = nil;
static SFSpeechAudioBufferRecognitionRequest *_request = nil;
static SFSpeechRecognitionTask *_task = nil;

// Thread-safe transcript state (guarded by @synchronized(_sttLock)).
static NSString *_transcript = @"";
static BOOL _isFinal = NO;
static BOOL _hasNew = NO;
static BOOL _speechAuthorized = NO;
static BOOL _micAuthorized = NO;
static BOOL _listening = NO;
static BOOL _initialized = NO;
static BOOL _tapInstalled = NO;
static NSString *_locale = @"en-US";

static void _stt_set_error(NSString *msg) {
    @synchronized(_sttLock) {
        _transcript = [msg copy];
        _hasNew = YES;
        _isFinal = YES;
        _listening = NO;
    }
}

static void _stt_remove_tap(void) {
    if (!_tapInstalled || _audioEngine == nil) return;
    @try {
        [[_audioEngine inputNode] removeTapOnBus:0];
    } @catch (NSException *e) {
        // Already removed — ignore.
    }
    _tapInstalled = NO;
}

void stt_init(void) {
    _sttLock = [[NSObject alloc] init];

    // Check speech recognition authorization (class method, safe off main thread).
    dispatch_async(dispatch_get_main_queue(), ^{
        @try {
            SFSpeechRecognizerAuthorizationStatus status =
                [SFSpeechRecognizer authorizationStatus];
            @synchronized(_sttLock) {
                _speechAuthorized = (status == SFSpeechRecognizerAuthorizationStatusAuthorized);
            }
        } @catch (NSException *e) {
            @synchronized(_sttLock) {
                _speechAuthorized = NO;
            }
        }

        // Check microphone authorization.
        AVAuthorizationStatus micStatus =
            [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
        @synchronized(_sttLock) {
            _micAuthorized = (micStatus == AVAuthorizationStatusAuthorized);
        }
    });
}

void stt_request_authorization(void) {
    // Request speech recognition permission.
    [SFSpeechRecognizer requestAuthorization:^(SFSpeechRecognizerAuthorizationStatus status) {
        @synchronized(_sttLock) {
            _speechAuthorized = (status == SFSpeechRecognizerAuthorizationStatusAuthorized);
        }
    }];

    // Request microphone permission (separate TCC entry).
    [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio
        completionHandler:^(BOOL granted) {
        @synchronized(_sttLock) {
            _micAuthorized = granted;
        }
    }];
}

void stt_start_listening(void) {
    @synchronized(_sttLock) {
        if (_listening) return;
        if (!_speechAuthorized) return;
        if (!_micAuthorized) return;
    }

    // All AVAudioEngine and SFSpeechRecognizer operations MUST run on the main thread.
    dispatch_async(dispatch_get_main_queue(), ^{
        @try {
            // Lazy init — create framework objects on main thread.
            if (!_initialized) {
                _initialized = YES;
                _recognizer = [[SFSpeechRecognizer alloc] initWithLocale:
                    [NSLocale localeWithLocaleIdentifier:_locale]];
                _audioEngine = [[AVAudioEngine alloc] init];
            }

            // Cancel any existing task.
            if (_task != nil) {
                [_task cancel];
                _task = nil;
            }

            // Stop and reset the audio engine if it was left running.
            if ([_audioEngine isRunning]) {
                [_audioEngine stop];
            }
            _stt_remove_tap();

            _request = [[SFSpeechAudioBufferRecognitionRequest alloc] init];
            _request.shouldReportPartialResults = YES;
            _request.requiresOnDeviceRecognition = YES;

            // Recreate recognizer with current locale.
            NSString *loc;
            @synchronized(_sttLock) {
                loc = [_locale copy];
            }
            _recognizer = [[SFSpeechRecognizer alloc] initWithLocale:
                [NSLocale localeWithLocaleIdentifier:loc]];

            if (_recognizer == nil || ![_recognizer isAvailable]) {
                _stt_set_error(@"Speech recognition unavailable for this locale");
                return;
            }

            AVAudioInputNode *inputNode = [_audioEngine inputNode];
            AVAudioFormat *format = [inputNode outputFormatForBus:0];

            if (format == nil || [format channelCount] == 0) {
                _stt_set_error(@"Microphone not available — check System Settings > Privacy");
                return;
            }

            _task = [_recognizer recognitionTaskWithRequest:_request
                resultHandler:^(SFSpeechRecognitionResult * _Nullable result,
                                NSError * _Nullable error) {
                @synchronized(_sttLock) {
                    if (result != nil) {
                        _transcript = [result.bestTranscription.formattedString copy];
                        _isFinal = result.isFinal;
                        _hasNew = YES;
                    }
                    if (error != nil) {
                        NSString *desc = error.localizedDescription;
                        NSString *msg;
                        if ([desc containsString:@"Dictation"] ||
                            [desc containsString:@"Siri"]) {
                            msg = @"Speech recognition unavailable — enable Dictation in System Settings > Keyboard";
                        } else if ([desc containsString:@"offline"] ||
                                   [desc containsString:@"device"]) {
                            msg = @"On-device speech model not installed — download it in System Settings > Keyboard > Dictation";
                        } else {
                            msg = [NSString stringWithFormat:@"Speech recognition error: %@", desc];
                        }
                        _transcript = [msg copy];
                        _isFinal = YES;
                        _hasNew = YES;
                        dispatch_async(dispatch_get_main_queue(), ^{
                            [_audioEngine stop];
                            _stt_remove_tap();
                        });
                        _listening = NO;
                        _request = nil;
                        _task = nil;
                    } else if (result != nil && result.isFinal) {
                        dispatch_async(dispatch_get_main_queue(), ^{
                            [_audioEngine stop];
                            _stt_remove_tap();
                        });
                        _listening = NO;
                        _request = nil;
                        _task = nil;
                    }
                }
            }];

            [inputNode installTapOnBus:0 bufferSize:1024 format:format
                block:^(AVAudioPCMBuffer * _Nonnull buffer,
                        AVAudioTime * _Nonnull when) {
                if (_request != nil) {
                    [_request appendAudioPCMBuffer:buffer];
                }
            }];
            _tapInstalled = YES;

            [_audioEngine prepare];
            NSError *engineErr = nil;
            [_audioEngine startAndReturnError:&engineErr];
            if (engineErr != nil) {
                _stt_remove_tap();
                _stt_set_error([NSString stringWithFormat:
                    @"Audio engine error: %@", engineErr.localizedDescription]);
                return;
            }

            @synchronized(_sttLock) {
                _listening = YES;
                _transcript = @"";
                _isFinal = NO;
                _hasNew = NO;
            }
        } @catch (NSException *e) {
            _stt_set_error([NSString stringWithFormat:@"STT error: %@", e.reason]);
        }
    });
}

void stt_stop_listening(void) {
    @synchronized(_sttLock) {
        if (!_listening) return;
        _listening = NO;
    }

    dispatch_async(dispatch_get_main_queue(), ^{
        @try {
            [_audioEngine stop];
            _stt_remove_tap();
            if (_request != nil) {
                [_request endAudio];
            }
        } @catch (NSException *e) {
            // Best-effort cleanup.
        }
    });
}

int stt_is_listening(void) {
    @synchronized(_sttLock) {
        return _listening ? 1 : 0;
    }
}

int stt_is_authorized(void) {
    @synchronized(_sttLock) {
        return (_speechAuthorized && _micAuthorized) ? 1 : 0;
    }
}

int stt_is_mic_authorized(void) {
    @synchronized(_sttLock) {
        return _micAuthorized ? 1 : 0;
    }
}

const char *stt_get_transcript(void) {
    @synchronized(_sttLock) {
        if (_transcript == nil) return strdup("");
        return strdup([_transcript UTF8String]);
    }
}

int stt_is_final(void) {
    @synchronized(_sttLock) {
        return _isFinal ? 1 : 0;
    }
}

int stt_has_new_transcript(void) {
    @synchronized(_sttLock) {
        BOOL had = _hasNew;
        _hasNew = NO;
        return had ? 1 : 0;
    }
}

void stt_set_locale(const char *locale) {
    if (locale == NULL) return;
    @synchronized(_sttLock) {
        _locale = [NSString stringWithUTF8String:locale];
    }
}
