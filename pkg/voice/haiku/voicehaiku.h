// SPDX-License-Identifier: Unlicense OR MIT

// libvoicehaiku: the microphone on Haiku, through the Media Kit, whose
// sound input ffmpeg cannot read there. A BMediaRecorder connected to the
// system's audio input keeps what it records, made mono, until vh_read
// takes it. The program opens the library with dlopen (../input_haiku.go).

#ifndef VOICEHAIKU_H
#define VOICEHAIKU_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// VH_ABI changes with the interface: the program checks vh_abi against it.
#define VH_ABI 1

int32_t vh_abi(void);

// vh_open connects to the system's audio input and starts recording. It
// returns the recording, or NULL with *status a Haiku status_t.
void *vh_open(int32_t *status);

// vh_read waits up to timeout microseconds for sound, and copies up to max
// samples of it into buf: mono, from -1 to 1, at *rate frames a second. It
// returns how many, 0 when none came, or a negative status_t.
int32_t vh_read(void *r, float *buf, int32_t max, int64_t timeout, int32_t *rate);

// vh_close stops the recording and frees it.
void vh_close(void *r);

#ifdef __cplusplus
}
#endif

#endif
