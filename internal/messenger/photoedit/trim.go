// SPDX-License-Identifier: Unlicense OR MIT

package photoedit

import "time"

// MinTrim is the shortest part of a video to keep.
const MinTrim = 200 * time.Millisecond

// Trim is the part of a video kept: from From up to To, 0 for its end.
type Trim struct {
	From, To time.Duration
}

// Set reports whether it keeps less than all of the video.
func (t Trim) Set() bool { return t.From > 0 || t.To > 0 }

// Clamped is the trim within a video of length, keeping at least MinTrim of
// it, and with To 0 when it reaches the end.
func (t Trim) Clamped(length time.Duration) Trim {
	if length <= 0 {
		return Trim{}
	}
	to := t.To
	if to <= 0 || to > length {
		to = length
	}
	from := max(0, min(t.From, length))
	if to-from < MinTrim {
		if from+MinTrim <= length {
			to = from + MinTrim
		} else {
			to, from = length, max(0, length-MinTrim)
		}
	}
	if to == length {
		to = 0
	}
	return Trim{From: from, To: to}
}

// Length is how long what is kept of a video of length lasts.
func (t Trim) Length(length time.Duration) time.Duration {
	t = t.Clamped(length)
	if t.To == 0 {
		return length - t.From
	}
	return t.To - t.From
}
