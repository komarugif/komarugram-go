package mockstore

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"strings"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/resample"
)

// demoPhotoSizes are the originals of the demo photos: the shapes a phone
// camera, a screenshot and a panorama produce, up to Telegram's 2560 px.
var demoPhotoSizes = []image.Point{
	{2560, 1707}, {1707, 2560}, {2560, 1440}, {1920, 1920}, {2560, 1080},
	{1280, 960}, {2560, 1920}, {1600, 2400}, {2048, 1365}, {2400, 1600},
	{1440, 2560}, {2560, 1280},
}

// demoVariants mirror Telegram's photo sizes: a picture is kept at each
// size that is smaller than the original.
var demoVariants = []struct {
	kind string
	side int
}{{"m", 320}, {"x", 800}, {"y", 1280}}

// demoPhoto describes photo n (from 1) with its variants and a small blurred
// preview, like a converted Telegram photo.
func demoPhoto(n int) *model.MessageMedia {
	orig := demoPhotoSizes[(n-1)%len(demoPhotoSizes)]
	meta := &model.MessageMedia{ID: fmt.Sprintf("demo/photo/%d/w", n), MIMEType: "image/jpeg", Width: orig.X, Height: orig.Y}
	for _, v := range demoVariants {
		size := resample.Fit(orig.X, orig.Y, image.Pt(v.side, v.side), false)
		if size.X < orig.X {
			meta.Variants = append(meta.Variants, model.MessageMedia{ID: fmt.Sprintf("demo/photo/%d/%s", n, v.kind), MIMEType: "image/jpeg", Width: size.X, Height: size.Y})
		}
	}
	small := resample.Fit(orig.X, orig.Y, image.Pt(40, 40), false)
	meta.Preview, _ = demoPhotoJPEG(n, small.X, small.Y)
	return meta
}

func parseDemoPhoto(id string) (n int, size image.Point, ok bool) {
	var kind string
	if _, e := fmt.Sscanf(strings.ReplaceAll(id, "/", " "), "demo photo %d %s", &n, &kind); e != nil || n < 1 {
		return 0, image.Point{}, false
	}
	meta := demoPhoto(n)
	for _, v := range append([]model.MessageMedia{*meta}, meta.Variants...) {
		if v.ID == id {
			return n, image.Pt(v.Width, v.Height), true
		}
	}
	return 0, image.Point{}, false
}

// demoPhotoJPEG paints photo n at w×h: a sky, a sun and ridges of hills,
// with fine stripes on the water that show how well scaling keeps detail.
// Every size paints the same scene, so the variants match each other.
func demoPhotoJPEG(n, w, h int) ([]byte, error) {
	im := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	hue := float64(n) * 0.61
	sunX, sunY := 0.25+0.5*math.Mod(float64(n)*0.37, 1), 0.22+0.1*math.Sin(float64(n))
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(w)
			r, g, b := sky(fy, hue)
			aspect := float64(w) / float64(h)
			if d := math.Hypot((fx-sunX)*aspect, fy-sunY); d < 0.07 {
				r, g, b = 255, 236, 180
			} else if d < 0.16 {
				k := (0.16 - d) / 0.09 * 0.6
				r, g, b = mix(r, 255, k), mix(g, 220, k), mix(b, 160, k)
			}
			for ridge := 0; ridge < 3; ridge++ {
				top := 0.52 + 0.1*float64(ridge) + 0.06*math.Sin(fx*(5+float64(ridge)*3)+float64(n+ridge))
				if fy > top {
					shade := 0.25 + 0.2*float64(ridge)
					r, g, b = mix(40, 20, shade)*1.1, mix(90+30*math.Sin(hue), 40, shade), mix(70, 50, shade)
				}
			}
			if fy > 0.86 {
				// Water: stripes one source pixel apart at the original size.
				stripe := 0.0
				if (x/2+y)%4 < 2 {
					stripe = 18
				}
				r, g, b = 30+stripe, 70+stripe, 120+stripe
			}
			yy, cb, cr := color.RGBToYCbCr(clampByte(r), clampByte(g), clampByte(b))
			im.Y[im.YOffset(x, y)] = yy
			if x%2 == 0 && y%2 == 0 {
				ci := im.COffset(x, y)
				im.Cb[ci], im.Cr[ci] = cb, cr
			}
		}
	}
	var out bytes.Buffer
	err := jpeg.Encode(&out, im, &jpeg.Options{Quality: 88})
	return out.Bytes(), err
}

func sky(fy, hue float64) (r, g, b float64) {
	top := [3]float64{40 + 40*math.Sin(hue), 80 + 30*math.Sin(hue+2), 160 + 40*math.Sin(hue+4)}
	return mix(top[0], 250, fy), mix(top[1], 190, fy), mix(top[2], 150, fy)
}

func mix(a, b, k float64) float64 { return a + (b-a)*k }

func clampByte(v float64) uint8 { return uint8(max(0, min(255, v))) }

var (
	_ model.PhotoGallery       = (*Store)(nil)
	_ model.GIFGallery         = (*Store)(nil)
	_ model.ProfilePhotoSource = (*Store)(nil)
)

// ChatPhotos implements model.PhotoGallery over the demo history.
func (s *Store) ChatPhotos(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	return s.gallery(ctx, chat, anchor, dir, limit, func(k model.MessageKind) bool { return k == model.MessagePhoto || k == model.MessageVideo })
}

// ChatGIFs implements model.GIFGallery over the demo history.
func (s *Store) ChatGIFs(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	return s.gallery(ctx, chat, anchor, dir, limit, func(k model.MessageKind) bool { return k == model.MessageGIF })
}

// gallery pages the media of the demo history of the kinds in tells.
func (s *Store) gallery(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int, in func(model.MessageKind) bool) (model.GalleryPage, error) {
	var photos []model.Message
	total := 0
	for _, m := range s.History(chat).Messages {
		if !in(m.Kind) || m.Media == nil || m.StoryOf != nil {
			continue
		}
		total++
		if dir < 0 && m.Key.MessageID < anchor || dir > 0 && m.Key.MessageID > anchor {
			photos = append(photos, m)
		}
	}
	page := model.GalleryPage{Total: total, More: len(photos) > limit}
	if dir < 0 {
		page.Messages = photos[max(0, len(photos)-limit):]
	} else {
		page.Messages = photos[:min(len(photos), limit)]
	}
	return page, ctx.Err()
}

// demoProfilePhotos is how many photos the profile of a chat of the demo
// has: Анна Смирнова's more than Telegram gives at once, so that they are
// paged, three each of the others.
func demoProfilePhotos(chat int64) int {
	if chat == 2 {
		return 150
	}
	return 3
}

// ProfilePhoto implements model.ProfilePhotoSource: the photos of a chat of
// the demo are drawn as the photos of its history are.
func (s *Store) ProfilePhoto(chat int64) (model.Message, bool) {
	return s.profilePhoto(chat, 0), true
}

// ProfilePhotos implements model.ProfilePhotoSource; offset is the place of
// the page's first photo.
func (s *Store) ProfilePhotos(ctx context.Context, chat int64, offset string, limit int) (model.GalleryPage, error) {
	from := 0
	if offset != "" {
		if _, err := fmt.Sscan(offset, &from); err != nil {
			return model.GalleryPage{}, err
		}
	}
	total := demoProfilePhotos(chat)
	page := model.GalleryPage{Total: total}
	for i := from; i < min(total, from+limit); i++ {
		page.Messages = append(page.Messages, s.profilePhoto(chat, i))
	}
	if end := from + len(page.Messages); end < total {
		page.More, page.Next = true, fmt.Sprint(end)
	}
	return page, ctx.Err()
}

func (s *Store) profilePhoto(chat int64, i int) model.Message {
	n := int(uint64(chat)%uint64(len(demoPhotoSizes))) + i + 1
	return model.Message{
		Kind:       model.MessagePhoto,
		Key:        model.MessageKey{ChatID: chat, MessageID: model.ProfilePhotoID(i)},
		Media:      demoPhoto(n),
		SenderName: s.chatTitle(chat),
		Date:       time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -i),
	}
}

// chatTitle is the title of a chat of the demo, empty when it has none.
func (s *Store) chatTitle(chat int64) string {
	for _, c := range s.Chats() {
		if c.ID == chat {
			return c.Title
		}
	}
	return ""
}
