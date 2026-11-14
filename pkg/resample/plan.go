// SPDX-License-Identifier: Unlicense OR MIT

package resample

// Plan is how VideoScaler.ResizeInto scales an opaque 4:2:0 frame, for a
// scaler elsewhere (a GPU's, pkg/hwvideo) to do the same: what it decides
// from the sizes and its settings, here, and the filters it is to repeat,
// described in its fields.
type Plan struct {
	// Same is set when the frame is not scaled: each pixel takes the chroma
	// sample it lies on, nothing is filtered.
	Same bool
	// BlockX and BlockY are the blocks every plane is first averaged in, at
	// its own resolution: the mean of the samples of a block that lie in the
	// plane, rounded to a byte. 1 by 1 is no averaging.
	BlockX, BlockY int
	// Linear filters what is left between the two samples a pixel lies
	// between; otherwise with Catmull-Rom, widened by the scale when
	// shrinking. Either way a plane of n samples is filtered to the d pixels
	// of the destination with pixel i centred on sample (i+.5)·n/d-.5, the
	// weights of the samples that lie inside the plane normalized to 1
	// (weights, linearWeights), horizontally and then vertically, in float32.
	Linear bool
	// Colors are the colours of the frame, which the factors below are of:
	// for a scaler that takes them so (a video processor).
	Colors Colors
	// Gain, Black, RV, GU, GV and BU convert the filtered planes to RGB:
	// L = (Y-Black)·Gain, U = Cb-128, V = Cr-128, then R = L+RV·V,
	// G = L-GU·U-GV·V, B = L+BU·U, each clamped to 0…255 and rounded half up.
	Gain, Black, RV, GU, GV, BU float32
}

// Plan is what ResizeInto does with an opaque 4:2:0 frame of sw×sh scaled
// to w×h.
func (s *VideoScaler) Plan(sw, sh, w, h int) Plan {
	c := s.Colors.conversion()
	p := Plan{BlockX: 1, BlockY: 1, Colors: s.Colors, Gain: c.gain, Black: c.black, RV: c.rv, GU: c.gu, GV: c.gv, BU: c.bu}
	if w == sw && h == sh {
		p.Same = true
		return p
	}
	p.BlockX, p.BlockY = blocks(sw, w), blocks(sh, h)
	p.Linear = s.Rough && (p.BlockX > 1 || p.BlockY > 1)
	return p
}
