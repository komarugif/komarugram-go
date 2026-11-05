// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png" // what FFmpeg gives a picture in
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"komarugram/pkg/program"
)

// A video edited is encoded again: what is drawn on it goes over each frame.
// The frames go through RGB for that, so that a color drawn comes out as it is,
// and the matrix each way is stated: left to the conversions, the one back is
// BT.601, while a player reads an HD video as BT.709.

const (
	editCRF    = 20
	editPreset = "medium"
	// editFilterMemory is what the filters may take a pixel: a picture or two
	// in RGB for each pass.
	editFilterMemory = 32
	// frameMemory bounds an FFmpeg asked for a single picture, over decoding.
	frameMemory = 64 << 20
)

// EditStep is one pass over the frames: a picture with alpha laid over them,
// or, with Radius set, a blur of them where a gray picture is white.
type EditStep struct {
	// Path is a PNG of the frames' size: of Media.EditSize.
	Path string
	// Radius is the blur's, 0 for laying the picture over.
	Radius int
}

// EditPlan is what a video becomes.
type EditPlan struct {
	Media Media
	// Steps come first, in order, then the crop, the mirror and the turn.
	Steps []EditStep
	// Crop is the part kept, on even pixels of even sides; empty for all.
	Crop image.Rectangle
	// Flip mirrors it left to right, before Angle turns it clockwise.
	Flip  bool
	Angle int
	// From is where it starts, To where it ends, 0 for its end.
	From, To time.Duration
	// GIF makes it a GIF of Telegram's, encoded as ConvertGIF encodes one: see
	// GIFPlan.
	GIF bool
	// Formats are the demuxers the video may be read by, mediaFormats for "".
	Formats string
	// gifPass is that encoding, nil for the first.
	gifPass *GIFPlan
}

// outSize is the size of the picture the edit makes, before a GIF's box.
func (p EditPlan) outSize() image.Point {
	size := p.Media.EditSize()
	if !p.Crop.Empty() {
		size = p.Crop.Size()
	}
	if p.Angle%180 != 0 {
		return image.Pt(size.Y, size.X)
	}
	return size
}

// pass is the encoding of a GIF.
func (p EditPlan) pass() GIFPlan {
	if p.gifPass != nil {
		return *p.gifPass
	}
	size := p.outSize()
	return PlanGIF(size.X, size.Y, p.Length())
}

// EditSize is the size the pictures of the video show at, turned as they show
// and with square pixels, on even sides: what is drawn on them is of this size.
func (m Media) EditSize() image.Point {
	w, h := float64(m.Width), float64(m.Height)
	if m.PixelAspect > 0 {
		w *= m.PixelAspect
	}
	even := func(v float64) int { return max(2, int(math.Round(v/2))*2) }
	return image.Pt(even(w), even(h))
}

// matrix is the name of the colors' matrix for FFmpeg's scale filter: as the
// video states it, or else as players guess it by its size.
func (m Media) matrix() string {
	switch m.ColorSpace {
	case "bt709":
		return "bt709"
	case "bt2020nc", "bt2020c":
		return "bt2020"
	case "smpte170m", "bt470bg", "fcc":
		return "bt601"
	}
	if m.Width >= 1280 || m.Height >= 720 {
		return "bt709"
	}
	return "bt601"
}

// inputRange is the colors' range as scale takes it.
func (m Media) inputRange() string {
	if m.ColorRange == "pc" {
		return "pc"
	}
	return "tv"
}

// scaleIn is the filter that makes frames of the video RGB at its size to edit.
func (m Media) scaleIn() string {
	size := m.EditSize()
	return fmt.Sprintf("scale=%d:%d:in_color_matrix=%s:in_range=%s:flags=bicubic+accurate_rnd+full_chroma_int+full_chroma_inp,setsar=1,format=gbrp", size.X, size.Y, m.matrix(), m.inputRange())
}

// Filter is the graph that makes the frames of input 0 into [out], the inputs
// from 1 being the pictures of Steps.
func (p EditPlan) Filter() string {
	size := p.Media.EditSize()
	var g strings.Builder
	fmt.Fprintf(&g, "[0:v]%s[v0]", p.Media.scaleIn())
	for i, step := range p.Steps {
		cur, next, in := i, i+1, i+1
		if step.Radius > 0 {
			// boxblur takes no more than half the shorter side.
			radius := min(step.Radius, max(1, min(size.X, size.Y)/2-1))
			fmt.Fprintf(&g, ";[v%d]split[keep%d][blur%d];[blur%d]boxblur=%d:2[soft%d];[%d:v]format=gray[mask%d];[soft%d][mask%d]alphamerge[patch%d];[keep%d][patch%d]overlay=format=gbrp[v%d]",
				cur, i, i, i, radius, i, in, i, i, i, i, i, i, next)
			continue
		}
		fmt.Fprintf(&g, ";[v%d][%d:v]overlay=format=gbrp[v%d]", cur, in, next)
	}
	fmt.Fprintf(&g, ";[v%d]", len(p.Steps))
	filters := []string{}
	if !p.Crop.Empty() {
		filters = append(filters, fmt.Sprintf("crop=%d:%d:%d:%d", p.Crop.Dx(), p.Crop.Dy(), p.Crop.Min.X, p.Crop.Min.Y))
	}
	if p.Flip {
		filters = append(filters, "hflip")
	}
	switch p.Angle {
	case 90:
		filters = append(filters, "transpose=1")
	case 180:
		filters = append(filters, "transpose=1", "transpose=1")
	case 270:
		filters = append(filters, "transpose=2")
	}
	// A GIF fits its box, by the filter ConvertGIF scales one with.
	box, scaler := "", "bicubic"
	if p.GIF {
		pass := p.pass()
		box, scaler = fmt.Sprintf("w=%d:h=%d:", pass.Width, pass.Height), "lanczos"
	}
	filters = append(filters, fmt.Sprintf("scale=%sout_color_matrix=%s:out_range=tv:flags=%s+accurate_rnd+full_chroma_int+full_chroma_inp", box, p.Media.matrix(), scaler), "format=yuv420p")
	g.WriteString(strings.Join(filters, ","))
	g.WriteString("[out]")
	return g.String()
}

// Length is how long what comes out lasts; 0 when the video's is not known.
func (p EditPlan) Length() time.Duration {
	to := p.Media.Duration
	if p.To > 0 && (to == 0 || p.To < to) {
		to = p.To
	}
	if to <= p.From {
		return 0
	}
	return to - p.From
}

// Args are FFmpeg's arguments to encode the file at in, a path or a URL, at
// out, telling its progress on the standard output.
func (p EditPlan) Args(in, out string) []string {
	formats := p.Formats
	if formats == "" {
		formats = mediaFormats
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-format_whitelist", formats,
		"-threads", strconv.Itoa(gifDecodeThreads),
	}
	seconds := func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }
	if p.From > 0 {
		args = append(args, "-ss", seconds(p.From))
	}
	if p.To > p.From {
		args = append(args, "-t", seconds(p.To-p.From))
	}
	args = append(args, "-i", in)
	for _, step := range p.Steps {
		// The client's own pictures, whatever a path looks like.
		args = append(args, "-format_whitelist", "image2", "-f", "image2", "-pattern_type", "none", "-i", step.Path)
	}
	matrix := p.Media.matrix()
	tag := map[string]string{"bt709": "bt709", "bt601": "smpte170m", "bt2020": "bt2020nc"}[matrix]
	crf, preset, maxRate := editCRF, editPreset, 0
	if p.GIF {
		pass := p.pass()
		crf, preset, maxRate = pass.CRF, pass.Preset, pass.MaxRate
	}
	args = append(args, "-filter_complex", p.Filter(), "-map", "[out]", "-sn", "-dn", "-map_metadata", "-1",
		"-c:v", "libx264", "-preset", preset, "-crf", strconv.Itoa(crf),
		"-threads", strconv.Itoa(gifEncodeThreads), "-colorspace", tag, "-color_range", "tv")
	if maxRate > 0 {
		args = append(args, "-maxrate", strconv.Itoa(maxRate), "-bufsize", strconv.Itoa(2*maxRate))
	}
	switch {
	case !p.Media.Sound || p.GIF:
		args = append(args, "-an")
	case p.Media.AudioCodec == "aac":
		args = append(args, "-map", "0:a:0", "-c:a", "copy")
	default:
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", "160k")
	}
	return append(args, "-movflags", "+faststart", "-f", "mp4",
		"-progress", "pipe:1", "-nostats", out)
}

// EncodeEdit makes the video at in, a path or a URL, what plan says at out with
// ffmpeg (the one found for ""), and tells report how far it has come.
func EncodeEdit(ctx context.Context, ffmpeg, in, out string, plan EditPlan, report func(GIFProgress)) error {
	path := ResolveFFmpeg(ffmpeg)
	if path == "" {
		return errors.New("FFmpeg is not installed")
	}
	size := plan.Media.EditSize()
	memory := DecodeMemory(image.Pt(plan.Media.Width, plan.Media.Height)) + gifEncodeMemory +
		uint64(size.X)*uint64(size.Y)*editFilterMemory*uint64(len(plan.Steps)+1)
	if plan.GIF {
		return encodeEditGIF(ctx, path, in, out, plan, memory, GIFMaxBytes, report)
	}
	if err := encodeGIF(ctx, path, plan.Args(in, out), memory, GIFProgress{Pass: 1, Total: plan.Length()}, report); err != nil {
		os.Remove(out)
		return err
	}
	return nil
}

// encodeEditGIF encodes a GIF within limit bytes, as ConvertGIF does: as GIFPlan
// says, and again under a lower cap while it is too large. It is ErrGIFTooLarge
// when it stays so.
func encodeEditGIF(ctx context.Context, ffmpeg, in, out string, plan EditPlan, memory uint64, limit int64, report func(GIFProgress)) error {
	long := plan.Length()
	pass := plan.pass()
	if long > 0 {
		pass.MaxRate = int(float64(limit) * 8 * gifFill / long.Seconds())
	}
	for n := 1; n <= gifPasses; n++ {
		plan.gifPass = &pass
		if err := encodeGIF(ctx, ffmpeg, plan.Args(in, out), memory, GIFProgress{Pass: n, Total: long}, report); err != nil {
			os.Remove(out)
			return err
		}
		info, err := os.Stat(out)
		if err != nil {
			return err
		}
		if info.Size() <= limit {
			return nil
		}
		pass = pass.againWithin(info.Size(), long, limit)
	}
	os.Remove(out)
	return ErrGIFTooLarge
}

// FrameAt is the picture of the video at path, a path or a URL, at a time, as
// EncodeEdit takes it to draw on, scaled down to fit box unless it is empty.
func FrameAt(ctx context.Context, ffmpeg, path string, media Media, at time.Duration, box image.Point) (*image.RGBA, error) {
	bin := ResolveFFmpeg(ffmpeg)
	if bin == "" {
		return nil, errors.New("FFmpeg is not installed")
	}
	filter := media.scaleIn()
	if box.X > 0 && box.Y > 0 {
		size := media.EditSize()
		scale := min(1, float64(box.X)/float64(size.X), float64(box.Y)/float64(size.Y))
		if scale < 1 {
			filter += fmt.Sprintf(",scale=%d:%d", max(1, int(float64(size.X)*scale)), max(1, int(float64(size.Y)*scale)))
		}
	}
	memory := DecodeMemory(image.Pt(media.Width, media.Height)) + frameMemory
	args := append(LimitArgs(memory), "-hide_banner", "-loglevel", "error", "-nostdin",
		"-threads", "2", "-format_whitelist", "mov,matroska,webm,gif")
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", path, "-an", "-sn", "-dn", "-frames:v", "1", "-vf", filter+",format=rgba",
		"-threads", "1", "-f", "image2pipe", "-c:v", "png", "-")
	out, err := outputLimited(program.CommandContext(ctx, bin, args...), memory)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rectangle{Max: b.Size()})
	for y := range b.Dy() {
		for x := range b.Dx() {
			r, g, b2, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := rgba.PixOffset(x, y)
			rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3] = uint8(r>>8), uint8(g>>8), uint8(b2>>8), uint8(a>>8)
		}
	}
	return rgba, nil
}

// pixelAspect reads ffprobe's sample_aspect_ratio, "16:15"; 0 for a square
// pixel or none told.
func pixelAspect(s string) float64 {
	num, den, ok := strings.Cut(s, ":")
	if !ok {
		return 0
	}
	n, errN := strconv.ParseFloat(num, 64)
	d, errD := strconv.ParseFloat(den, 64)
	if errN != nil || errD != nil || n <= 0 || d <= 0 || n == d {
		return 0
	}
	// A file may state any; beyond this it is no video to show as it says.
	if a := n / d; a >= .1 && a <= 10 {
		return a
	}
	return 0
}
