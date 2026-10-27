package mockstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"komarugram/internal/messenger/model"
)

var _ model.GIFMaker = (*Store)(nil)

// SaveGIFFile implements model.GIFMaker: the GIF converted is saved first,
// drawn as the demo's GIF; nothing is uploaded.
func (s *Store) SaveGIFFile(ctx context.Context, source model.Message, path string, width, height int, duration time.Duration) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	gif := s.madeGIF(width, height, duration, info.Size())
	s.rememberGIF(source, gif)
	return s.SaveGIF(ctx, gif, false)
}

// MadeGIF implements model.GIFMaker in memory.
func (s *Store) MadeGIF(_ context.Context, source model.Message) (model.Message, bool) {
	if source.Media == nil {
		return model.Message{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	gif, ok := s.gifsOf[source.Media.ID]
	return gif, ok
}

// rememberGIF keeps gif as the GIF made of the video of source.
func (s *Store) rememberGIF(source, gif model.Message) {
	if source.Media == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gifsOf == nil {
		s.gifsOf = map[string]model.Message{}
	}
	s.gifsOf[source.Media.ID] = gif
}

// madeGIF is the message of a GIF the demo made, of its own media ID: the
// demo's GIF, which is a GIF file, at the size of the video.
func (s *Store) madeGIF(width, height int, duration time.Duration, size int64) model.Message {
	s.mu.Lock()
	s.gifsMade++
	id := fmt.Sprintf("%smade/%d", demoSavedGIF, s.gifsMade)
	s.mu.Unlock()
	return model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: id, MIMEType: "image/gif", Width: width, Height: height, Duration: duration, Size: size}}
}

// demoGIFBot is what the demo's bot says as it converts a video, as
// model.GIFBot words it, each after the pause before it.
var demoGIFBot = func() []string {
	texts := []string{"You are in the queue. Your position: 1", "Downloading file...", "Extracting data from video..."}
	const frames = 1080
	for done := 0; done <= frames; done += frames / 12 {
		texts = append(texts, fmt.Sprintf("Processing...\nResolution: 720x1280 -> 720x1280\nProgress: %.2f%%\nFrames %d/%d\nFile size: %dkB\nSpeed: 2.4x",
			float64(done)/frames*100, done, frames, done*3))
	}
	return append(texts, "Sending a gif...")
}()

// demoGIFBotPause is how long the demo's bot takes over each step.
var demoGIFBotPause = 400 * time.Millisecond

// ConvertGIFByBot implements model.GIFMaker with a bot of the demo's, which
// says what the real one says, reads it as the real one's is read, and
// sends back a GIF of the demo's.
func (s *Store) ConvertGIFByBot(ctx context.Context, m model.Message, progress func(model.GIFProgress)) (model.Message, error) {
	if m.Media == nil {
		return model.Message{}, errors.New("no video")
	}
	if m.Media.Size > model.GIFBotMaxBytes {
		return model.Message{}, model.ErrGIFTooLarge
	}
	progress(model.GIFProgress{Share: -1})
	for _, text := range demoGIFBot {
		select {
		case <-ctx.Done():
			return model.Message{}, ctx.Err()
		case <-time.After(demoGIFBotPause):
		}
		p, ok, err := model.ParseGIFBot(text)
		if err != nil {
			return model.Message{}, err
		}
		if ok {
			progress(p)
		}
	}
	gif := s.madeGIF(m.Media.Width, m.Media.Height, m.Media.Duration, 3<<20)
	s.rememberGIF(m, gif)
	return gif, nil
}
