#ifndef STT_BRIDGE_H
#define STT_BRIDGE_H

void stt_init(void);
void stt_request_authorization(void);
void stt_start_listening(void);
void stt_stop_listening(void);
int  stt_is_listening(void);
int  stt_is_authorized(void);
int  stt_is_mic_authorized(void);
const char *stt_get_transcript(void);
int  stt_is_final(void);
int  stt_has_new_transcript(void);
void stt_set_locale(const char *locale);

#endif
