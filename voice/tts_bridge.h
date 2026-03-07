#ifndef TTS_BRIDGE_H
#define TTS_BRIDGE_H

void tts_init(void);
void tts_speak(const char *text, const char *voiceID, float rate, float pitch, float volume);
void tts_stop(void);
void tts_pause(void);
void tts_continue(void);
int  tts_is_speaking(void);
int  tts_is_paused(void);
int  tts_voice_count(void);
const char *tts_voice_name(int index);
const char *tts_voice_id(int index);
const char *tts_voice_language(int index);

#endif
