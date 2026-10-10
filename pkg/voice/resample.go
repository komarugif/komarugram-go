// SPDX-License-Identifier: Unlicense OR MIT

package voice

import "math"

// resampler makes samples of any rate Rate's 16-bit ones, by linear
// interpolation, over successive calls: a microphone of 44.1 kHz then
// records as one of 48 kHz.
type resampler struct {
	// pos is where the next output sample falls, in input samples from the
	// first of the next call's; last is the previous call's last input.
	pos  float64
	last float32
}

func (s *resampler) to16(in []float32, rate int) []int16 {
	if len(in) == 0 || rate <= 0 {
		return nil
	}
	if rate == Rate {
		out := make([]int16, len(in))
		for i, v := range in {
			out[i] = pcm16(v)
		}
		return out
	}
	step := float64(rate) / Rate
	out := make([]int16, 0, int(float64(len(in))/step)+1)
	for ; s.pos < float64(len(in)-1); s.pos += step {
		i := int(math.Floor(s.pos))
		var a float32
		if i < 0 {
			a = s.last
		} else {
			a = in[i]
		}
		b := in[i+1]
		f := float32(s.pos - float64(i))
		out = append(out, pcm16(a+(b-a)*f))
	}
	s.pos -= float64(len(in) - 1)
	// The next call's samples start one after this one's last, at -1.
	s.pos--
	s.last = in[len(in)-1]
	return out
}

// pcm16 makes v, from -1 to 1, a 16-bit sample.
func pcm16(v float32) int16 {
	switch {
	case v >= 1:
		return math.MaxInt16
	case v <= -1:
		return math.MinInt16
	}
	return int16(v * 32767)
}
