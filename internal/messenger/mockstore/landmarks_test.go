// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"testing"
	"time"
)

// Every landmark is in the demo's history: one that is not would have
// -demo-open find nothing.
func TestLandmarks(t *testing.T) {
	s := New(time.Now(), 0)
	for _, name := range Landmarks() {
		if key, ok := s.Landmark(name); !ok || key.ChatID != LandmarkChat || key.MessageID <= 0 {
			t.Errorf("%s: %+v, %v", name, key, ok)
		}
	}
	if _, ok := s.Landmark("nothing"); ok {
		t.Error("an unknown name found a message")
	}
}
