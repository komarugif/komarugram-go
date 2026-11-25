// SPDX-License-Identifier: Unlicense OR MIT

package notify

import (
	"log"
	"sync"
)

// balloon shows notifications through the tray. Only the last one shows,
// so a click opens what it was about.
type balloon struct {
	tray Balloon
	mu   sync.Mutex
	open func(string)
}

func newBalloon(tray Balloon) *balloon { return &balloon{tray: tray} }

func (b *balloon) Show(n Notification) {
	if b.tray == nil {
		return
	}
	b.mu.Lock()
	b.open = n.Open
	b.mu.Unlock()
	// The balloon's fields hold 63 and 255 characters.
	if err := b.tray.Notify(clip(n.Title, 60), clip(n.Body, 250), n.Sound, n.Icon); err != nil {
		log.Printf("notify: %v", err)
	}
}

func (b *balloon) Clicked(token string) {
	b.mu.Lock()
	open := b.open
	b.mu.Unlock()
	if open != nil {
		go open(token)
	}
}

func (b *balloon) Close() {}
