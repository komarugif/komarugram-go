// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"testing"
	"time"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// -demo-open takes a chat, a chat and a message, or a landmark's name.
func TestDemoMessage(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	album, _ := store.Landmark("album")
	for value, want := range map[string]model.MessageKey{
		"":      {},
		"3":     {ChatID: 3},
		"3/150": {ChatID: 3, MessageID: 150},
		"album": album,
	} {
		if got, err := demoMessage(store, value); err != nil || got != want {
			t.Errorf("%q: %+v, %v; want %+v", value, got, err, want)
		}
	}
	for _, value := range []string{"3/x", "3/0", "nothing"} {
		if _, err := demoMessage(store, value); err == nil {
			t.Errorf("%q taken", value)
		}
	}
}
