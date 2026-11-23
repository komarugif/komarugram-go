// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestMain has the store's waits shortened: the history's pages paced
// without the waits between them (the pace is reckoned as ever, and the
// tests of it, and of what waits for it, set a sleep of their own:
// history_pace_test.go, archive_test.go), and the retries and the storage's
// channel waiting a hundredth as long.
func TestMain(m *testing.M) {
	paceSleep = func(context.Context, time.Duration) error { return nil }
	// Retries, and the channel of the storage's pace, a hundredth as long:
	// their order and their number are as ever.
	outgoingBackoff = 10 * time.Millisecond
	storagePace, storageDebounce = storagePace/100, storageDebounce/100
	// A scroll that goes on asks within a tenth of the time.
	pageActiveGap /= 10
	os.Exit(m.Run())
}
