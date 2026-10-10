// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package voice

import (
	"context"
	"testing"
	"time"
)

// TestMediaKitRecords records a second from the system's audio input, as a
// voice message does on Haiku, and encodes it. A machine without an input
// may give silence, which is still sound recorded.
func TestMediaKitRecords(t *testing.T) {
	path := ffmpeg(t)
	r := Start(context.Background(), path)
	deadline := time.Now().Add(10 * time.Second)
	for r.Duration() < time.Second {
		if err := r.Failed(); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v recorded in 10 s", r.Duration())
		}
		time.Sleep(50 * time.Millisecond)
	}
	start := time.Now()
	pcm, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Stop took %v", took)
	}
	t.Logf("%v recorded, level %.3f", Duration(pcm), r.Level())
	if err := Encode(context.Background(), path, pcm, t.TempDir()+"/voice.ogg"); err != nil {
		t.Fatal(err)
	}
}
