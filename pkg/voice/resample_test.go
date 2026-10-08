// SPDX-License-Identifier: Unlicense OR MIT

package voice

import "testing"

// TestResamplerAcrossCalls checks that samples of another rate, given in
// pieces as a microphone gives them, come out at Rate as they would have
// all at once: a ramp, which linear interpolation keeps exact, sampled at
// the output's times.
func TestResamplerAcrossCalls(t *testing.T) {
	for _, rate := range []int{44100, 96000, 16000} {
		const n = 20000
		ramp := func(x float64) float32 { return float32(x/n*1.8 - 0.9) }
		in := make([]float32, n)
		for i := range in {
			in[i] = ramp(float64(i))
		}
		var s resampler
		var out []int16
		for at, size := 0, 1; at < n; at, size = at+size, size%997+13 {
			out = append(out, s.to16(in[at:min(n, at+size)], rate)...)
		}
		step := float64(rate) / Rate
		if want := int(float64(n-1)/step) + 1; len(out) < want-1 || len(out) > want {
			t.Errorf("%d Hz: %d samples, want %d", rate, len(out), want)
		}
		for k, got := range out {
			want := pcm16(ramp(float64(k) * step))
			if d := int(got) - int(want); d < -2 || d > 2 {
				t.Fatalf("%d Hz: sample %d is %d, want %d", rate, k, got, want)
			}
		}
	}
}
