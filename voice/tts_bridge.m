#import <AVFoundation/AVFoundation.h>
#include "tts_bridge.h"

// Singleton synthesizer and cached voice list.
static AVSpeechSynthesizer *_synth = nil;
static NSArray<AVSpeechSynthesisVoice *> *_voices = nil;

void tts_init(void) {
    // Synchronous — must complete before buildPalette enumerates voices.
    _synth = [[AVSpeechSynthesizer alloc] init];
    _voices = [AVSpeechSynthesisVoice speechVoices];
}

void tts_speak(const char *text, const char *voiceID, float rate, float pitch, float volume) {
    NSString *nsText = [NSString stringWithUTF8String:text];
    NSString *nsVoiceID = nil;
    if (voiceID != NULL && voiceID[0] != '\0') {
        nsVoiceID = [NSString stringWithUTF8String:voiceID];
    }
    float r = rate;
    float p = pitch;
    float v = volume;

    dispatch_async(dispatch_get_main_queue(), ^{
        if (_synth == nil) return;

        if ([_synth isSpeaking]) {
            [_synth stopSpeakingAtBoundary:AVSpeechBoundaryImmediate];
        }

        AVSpeechUtterance *utterance = [[AVSpeechUtterance alloc] initWithString:nsText];
        if (nsVoiceID != nil) {
            AVSpeechSynthesisVoice *voice = [AVSpeechSynthesisVoice voiceWithIdentifier:nsVoiceID];
            if (voice != nil) {
                utterance.voice = voice;
            }
        }
        utterance.rate = r;
        utterance.pitchMultiplier = p;
        utterance.volume = v;

        [_synth speakUtterance:utterance];
    });
}

void tts_stop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (_synth != nil && [_synth isSpeaking]) {
            [_synth stopSpeakingAtBoundary:AVSpeechBoundaryImmediate];
        }
    });
}

void tts_pause(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (_synth != nil && [_synth isSpeaking]) {
            [_synth pauseSpeakingAtBoundary:AVSpeechBoundaryImmediate];
        }
    });
}

void tts_continue(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (_synth != nil && [_synth isPaused]) {
            [_synth continueSpeaking];
        }
    });
}

int tts_is_speaking(void) {
    // Direct read — AVSpeechSynthesizer properties are main-thread safe for reads.
    if (_synth == nil) return 0;
    return [_synth isSpeaking] ? 1 : 0;
}

int tts_is_paused(void) {
    if (_synth == nil) return 0;
    return [_synth isPaused] ? 1 : 0;
}

int tts_voice_count(void) {
    if (_voices == nil) return 0;
    return (int)[_voices count];
}

const char *tts_voice_name(int index) {
    if (_voices == nil || index < 0 || index >= (int)[_voices count]) return "";
    return [[_voices[index] name] UTF8String];
}

const char *tts_voice_id(int index) {
    if (_voices == nil || index < 0 || index >= (int)[_voices count]) return "";
    return [[_voices[index] identifier] UTF8String];
}

const char *tts_voice_language(int index) {
    if (_voices == nil || index < 0 || index >= (int)[_voices count]) return "";
    return [[_voices[index] language] UTF8String];
}
