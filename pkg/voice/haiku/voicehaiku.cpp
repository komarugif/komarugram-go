// SPDX-License-Identifier: Unlicense OR MIT

#include "voicehaiku.h"

#include <Locker.h>
#include <MediaDefs.h>
#include <MediaRecorder.h>
#include <MediaRoster.h>
#include <OS.h>

#include <deque>

namespace {

// kKeep is how much sound waits for vh_read at most, in samples: what is
// older goes.
const size_t kKeep = 48000 * 10;

struct Recording {
	BMediaRecorder *recorder = NULL;
	BLocker lock;
	sem_id ready = -1;
	std::deque<float> samples;
	int32 rate = 0;
};

// sample reads the sample at p of a raw audio format, from -1 to 1.
float sample(const uint8 *p, uint32 format) {
	switch (format) {
	case media_raw_audio_format::B_AUDIO_FLOAT:
		return *(const float *)p;
	case media_raw_audio_format::B_AUDIO_DOUBLE:
		return (float)*(const double *)p;
	case media_raw_audio_format::B_AUDIO_INT:
		return *(const int32 *)p / 2147483648.0f;
	case media_raw_audio_format::B_AUDIO_SHORT:
		return *(const int16 *)p / 32768.0f;
	case media_raw_audio_format::B_AUDIO_CHAR:
		return *(const int8 *)p / 128.0f;
	case media_raw_audio_format::B_AUDIO_UCHAR:
		return (*p - 128) / 128.0f;
	}
	return 0;
}

// Process takes a buffer the input recorded, in the input's own format.
void Process(void *cookie, bigtime_t, void *data, size_t size, const media_format &format) {
	Recording *r = (Recording *)cookie;
	const media_raw_audio_format &f = format.u.raw_audio;
	size_t width = f.format & media_raw_audio_format::B_AUDIO_SIZE_MASK;
	size_t channels = f.channel_count > 0 ? f.channel_count : 1;
	if (width == 0 || f.frame_rate <= 0)
		return;
	size_t frames = size / (width * channels);
	const uint8 *p = (const uint8 *)data;
	r->lock.Lock();
	r->rate = (int32)f.frame_rate;
	for (size_t i = 0; i < frames; i++) {
		float sum = 0;
		for (size_t c = 0; c < channels; c++, p += width)
			sum += sample(p, f.format);
		r->samples.push_back(sum / channels);
	}
	while (r->samples.size() > kKeep)
		r->samples.pop_front();
	r->lock.Unlock();
	release_sem_etc(r->ready, 1, B_DO_NOT_RESCHEDULE);
}

} // namespace

extern "C" {

int32_t vh_abi(void) { return VH_ABI; }

void *vh_open(int32_t *status) {
	Recording *r = new Recording;
	r->ready = create_sem(0, "voice ready");
	r->recorder = new BMediaRecorder("KomaruGram Go", B_MEDIA_RAW_AUDIO);
	status_t st = r->ready < 0 ? r->ready : r->recorder->InitCheck();
	if (st == B_OK)
		st = r->recorder->SetHooks(Process, NULL, r);
	// BMediaRecorder::Connect(format) connects to the system's mixer, what
	// it plays; the microphone is the audio input's node.
	media_node input;
	if (st == B_OK)
		st = BMediaRoster::Roster()->GetAudioInput(&input);
	if (st == B_OK) {
		// Any raw audio: the input's own, made mono and floats in Process.
		media_format format;
		format.type = B_MEDIA_RAW_AUDIO;
		format.u.raw_audio = media_raw_audio_format::wildcard;
		st = r->recorder->Connect(input, NULL, &format);
	}
	if (st == B_OK)
		st = r->recorder->Start();
	// The input sends nothing until its node runs. It is left running,
	// as other programs may record from it too.
	if (st == B_OK)
		st = BMediaRoster::Roster()->StartNode(input, 0);
	if (st != B_OK) {
		*status = st;
		vh_close(r);
		return NULL;
	}
	*status = B_OK;
	return r;
}

int32_t vh_read(void *h, float *buf, int32_t max, int64_t timeout, int32_t *rate) {
	Recording *r = (Recording *)h;
	r->lock.Lock();
	bool empty = r->samples.empty();
	r->lock.Unlock();
	if (empty) {
		status_t st = acquire_sem_etc(r->ready, 1, B_RELATIVE_TIMEOUT, timeout);
		if (st == B_TIMED_OUT || st == B_WOULD_BLOCK)
			return 0;
		if (st != B_OK)
			return st;
	}
	r->lock.Lock();
	int32_t n = 0;
	while (n < max && !r->samples.empty()) {
		buf[n++] = r->samples.front();
		r->samples.pop_front();
	}
	*rate = r->rate;
	r->lock.Unlock();
	return n;
}

void vh_close(void *h) {
	Recording *r = (Recording *)h;
	if (r->recorder->IsRunning())
		r->recorder->Stop();
	if (r->recorder->IsConnected())
		r->recorder->Disconnect();
	delete r->recorder;
	if (r->ready >= 0)
		delete_sem(r->ready);
	delete r;
}

} // extern "C"
