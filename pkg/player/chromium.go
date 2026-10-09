// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/miniapp"
)

// ErrCannotPlay means the browser would not play the file: most often a
// Chromium built without the codec, such as H.264 or HEVC, it needs.
var ErrCannotPlay = errors.New("the browser cannot play this file")

// chromiumPage is the player: a video that fills the window, with the
// browser's own controls. Its URL and the window's title come in the
// fragment, which the page reads and never sends anywhere. Space and k
// pause, the arrows seek by five seconds, f toggles full screen and m the
// sound, as in the players people know.
const chromiumPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
html, body { margin: 0; height: 100%; background: #000; overflow: hidden }
video { display: block; width: 100%; height: 100%; object-fit: contain; outline: none }
</style>
</head>
<body>
<video id="player" controls autoplay></video>
<script>
var params = new URLSearchParams(location.hash.slice(1));
var v = document.getElementById('player');
document.title = params.get('title') || '';
v.src = params.get('src');
v.focus();
addEventListener('keydown', function (e) {
  if (e.ctrlKey || e.altKey || e.metaKey) return;
  switch (e.key) {
  case ' ': case 'k': if (v.paused) v.play(); else v.pause(); break;
  case 'ArrowLeft': v.currentTime = Math.max(0, v.currentTime - 5); break;
  case 'ArrowRight': v.currentTime += 5; break;
  case 'f': if (document.fullscreenElement) document.exitFullscreen(); else v.requestFullscreen(); break;
  case 'm': v.muted = !v.muted; break;
  default: return;
  }
  e.preventDefault();
}, true);
</script>
</body>
</html>
`

// servePage serves chromiumPage on 127.0.0.1. The page must come from
// loopback, as the video does: Chromium lets no page from elsewhere, not
// even about:blank, load media from a loopback address.
func servePage() (*http.Server, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, chromiumPage)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener) //nolint:errcheck // Serve always ends with an error on Close
	return server, "http://" + listener.Addr().String() + "/", nil
}

// chromiumStatus reads the video's state as JSON; "" before the page is
// built.
const chromiumStatus = `(function () {
  var v = document.getElementById('player');
  if (!v) return '';
  return JSON.stringify({t: v.currentTime, d: isFinite(v.duration) ? v.duration : 0, p: v.paused,
    e: v.error ? v.error.code : 0, m: v.error ? v.error.message : ''});
})()`

// chromiumFit waits for the video to tell its size, for five seconds at
// most, and returns as JSON the bounds of a window that shows it whole: at
// its own size where the screen has room, smaller to fit in nine tenths of
// the screen, and at least 400 pixels wide, so that the controls fit, all
// centered on the screen. A file of sound alone gets a strip for the
// controls. It returns "" when the video never told, and "wait" while the
// page is not built yet.
const chromiumFit = `new Promise(function (resolve) {
  var v = document.getElementById('player');
  if (!v) return resolve('wait');
  if (v.error) return resolve('');
  function fit() {
    var w = v.videoWidth, h = v.videoHeight;
    if (!w || !h) { w = 480; h = 120; }
    var frameW = %d - innerWidth, frameH = %d - innerHeight;
    var maxW = screen.availWidth * 0.9 - frameW, maxH = screen.availHeight * 0.9 - frameH;
    var scale = Math.min(1, maxW / w, maxH / h);
    if (w * scale < 400) scale = Math.min(400 / w, maxW / w, maxH / h);
    var width = Math.round(w * scale) + frameW, height = Math.round(h * scale) + frameH;
    resolve(JSON.stringify({
      left: Math.round((screen.availLeft || 0) + (screen.availWidth - width) / 2),
      top: Math.round((screen.availTop || 0) + (screen.availHeight - height) / 2),
      width: width, height: height}));
  }
  if (v.readyState >= 1) return fit();
  v.addEventListener('loadedmetadata', fit);
  v.addEventListener('error', function () { resolve(''); });
  setTimeout(function () { resolve(''); }, 5000);
})`

// chromium is a video played in a window of the browser Mini Apps run in,
// for a system with neither mpv nor VLC. It is driven over the DevTools
// protocol, as a Mini App is, and polled for its state: a page pushes
// nothing out on its own.
type chromium struct {
	page   *miniapp.Bridge
	server *http.Server
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	status Status
	closed bool
}

func openChromium(ctx context.Context, source string, extra []string) (*chromium, error) {
	server, address, err := servePage()
	if err != nil {
		return nil, err
	}
	fragment := url.Values{"src": {source}, "title": {path.Base(source)}}.Encode()
	page := miniapp.Page{
		Width: 960, Height: 600,
		// The window opens because the user asked for the video: it plays
		// without waiting for a click in it.
		Args: []string{"--autoplay-policy=no-user-gesture-required"},
	}
	// The size SizeArgs gave is the window's first one, which OpenPage
	// passes on itself.
	for _, arg := range extra {
		var width, height int
		if n, _ := fmt.Sscanf(arg, "--window-size=%d,%d", &width, &height); n == 2 {
			page.Width, page.Height = width, height
			continue
		}
		page.Args = append(page.Args, arg)
	}
	bridge, err := miniapp.OpenPage(ctx, address+"#"+fragment, page)
	if err != nil {
		_ = server.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &chromium{
		page:   bridge,
		server: server,
		cancel: cancel,
		done:   make(chan struct{}),
		status: Status{Running: true, File: source},
	}
	go p.poll(ctx)
	return p, nil
}

// poll reads the video's state until the window is gone.
func (p *chromium) poll(ctx context.Context) {
	defer close(p.done)
	p.fit(ctx)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		// The page's connection breaks when its window closes, even while
		// the browser itself stays behind, as Edge may.
		if !p.page.Running() || p.page.Err() != nil {
			p.mu.Lock()
			p.status.Running = false
			p.mu.Unlock()
			return
		}
		read, cancel := context.WithTimeout(ctx, 2*time.Second)
		answer, err := p.page.Eval(read, chromiumStatus)
		cancel()
		if err != nil || answer == "" {
			continue
		}
		var state struct {
			T, D float64
			P    bool
			E    int
			M    string
		}
		if json.Unmarshal([]byte(answer), &state) != nil {
			continue
		}
		p.mu.Lock()
		p.status.Position = time.Duration(state.T * float64(time.Second))
		p.status.Duration = time.Duration(state.D * float64(time.Second))
		p.status.Paused = state.P
		if state.E != 0 && p.status.Err == nil {
			p.status.Err = mediaError(state.E, state.M)
		}
		p.mu.Unlock()
	}
}

// fit gives the window the size of the video once the video tells it. The
// window opens before the file is read, at a size that suits none in
// particular; a window that cannot be fitted keeps that size.
func (p *chromium) fit(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The launch returns while the page may still be loading: the first
	// questions can land in the blank page before it, or in none.
	var answer string
	for {
		// Read the outer size through the protocol: Brave's privacy
		// protection randomizes the page's outerWidth and outerHeight.
		width, height, err := p.page.WindowSize(ctx)
		if err != nil {
			return
		}
		answer, err = p.page.Eval(ctx, fmt.Sprintf(chromiumFit, width, height))
		if err == nil && answer != "wait" {
			break
		}
		if !p.page.Running() || p.page.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	if answer == "" {
		return
	}
	var bounds struct{ Left, Top, Width, Height int }
	if json.Unmarshal([]byte(answer), &bounds) != nil || bounds.Width <= 0 || bounds.Height <= 0 {
		return
	}
	if err := p.page.SetWindowBounds(ctx, bounds.Left, bounds.Top, bounds.Width, bounds.Height); err != nil {
		// The window keeps the size it opened at, which SizeArgs chose.
		log.Printf("chromium player: fit the window to the video: %v", err)
	}
}

// mediaError is the error of a video element: its MediaError code and the
// browser's message.
func mediaError(code int, message string) error {
	names := map[int]string{1: "aborted", 2: "network error", 3: "decode error", 4: "format not supported"}
	name := names[code]
	if name == "" {
		name = fmt.Sprintf("error %d", code)
	}
	if message = strings.TrimSpace(message); message != "" {
		name += ": " + message
	}
	// A network error is the stream failing, which is reported where the
	// bytes are read; the rest is the browser giving up on the file.
	if code == 2 {
		return errors.New(name)
	}
	return fmt.Errorf("%w (%s)", ErrCannotPlay, name)
}

func (p *chromium) eval(expression string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := p.page.Eval(ctx, expression)
	return err
}

// TogglePause flips playback.
func (p *chromium) TogglePause() error {
	return p.eval(`(function () { var v = document.getElementById('player'); if (v.paused) v.play(); else v.pause(); })()`)
}

// Seek moves by delta, forward or backward.
func (p *chromium) Seek(delta time.Duration) error {
	return p.eval(fmt.Sprintf(`(function () { var v = document.getElementById('player'); v.currentTime = Math.max(0, v.currentTime + %f); })()`, delta.Seconds()))
}

// Status reports the state read last.
func (p *chromium) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Close closes the window and removes its profile.
func (p *chromium) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	p.cancel()
	<-p.done
	p.page.Close()
	_ = p.server.Close()
	p.mu.Lock()
	p.status.Running = false
	p.mu.Unlock()
	return nil
}
