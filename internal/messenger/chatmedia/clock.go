// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import "time"

// frameTick is the clock every animation shares. Pictures change only on
// its ticks, so that the animations on screen change together, and the
// window draws one frame for them all instead of one for each.
const frameTick = time.Second / 60

// ticksPer is every how many ticks an animation whose own frames last
// period shows a picture: every second, 30 a second, as Telegram Desktop
// plays animations by default; every one, 60, for a faster animation where
// fullRate asks for it, as it plays stickers in messages.
func ticksPer(period time.Duration, fullRate bool) int64 {
	if fullRate && period < 2*frameTick {
		return 1
	}
	return 2
}

// tickCeil is the first of every k-th tick at or after t.
func tickCeil(t time.Time, k int64) time.Time {
	unit := int64(frameTick) * k
	return time.Unix(0, (t.UnixNano()+unit-1)/unit*unit)
}

// tickFloor is the last of every k-th tick at or before t.
func tickFloor(t time.Time, k int64) time.Time {
	unit := int64(frameTick) * k
	return time.Unix(0, t.UnixNano()/unit*unit)
}

// A picture due at d shows on the first tick from half a tick before d:
// the frames of animations, of whole milliseconds or nanoseconds, never fall
// on the ticks exactly, and rounding is not to hold a picture a tick more.
// pictureAt is the picture shown elapsed into an animation whose pictures
// last step, and pictureTick the tick, of every k-th, of picture i.
func pictureAt(elapsed, step time.Duration) int64 {
	return int64((elapsed + frameTick/2) / step)
}

func pictureTick(start time.Time, i int64, step time.Duration, k int64) time.Time {
	return tickCeil(start.Add(step*time.Duration(i)-frameTick/2), k)
}
