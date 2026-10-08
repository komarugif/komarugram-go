// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package notify

import (
	"bytes"
	"context"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"komarugram/internal/appicon"
)

// New returns the notifier of Haiku: its notification_server, through the
// notify command of the system. A notification with a tag takes the place
// of the last one with it, as its message ID. notify can only start a
// program on a click, by its signature: it starts the client with
// -notified and the tag, and that start hands the tag over to the running
// client, which opens the chat (cmd/messenger).
func New(app string, tray Balloon) Notifier {
	h := &haikuNotifier{app: app, queue: make(chan Notification, 64)}
	go h.run()
	return h
}

// signature is the client's, from its resources (cmd/messenger/messenger.rdef).
const signature = "application/x-vnd.komarugram-go"

type haikuNotifier struct {
	app   string
	icon  string
	queue chan Notification
}

func (h *haikuNotifier) Show(n Notification) {
	select {
	case h.queue <- n:
	default:
		// notify is not keeping up; what it has not shown is old.
	}
}

func (h *haikuNotifier) run() {
	h.icon = writeIcon()
	for n := range h.queue {
		h.show(n)
	}
}

func (h *haikuNotifier) show(n Notification) {
	args := []string{"--type", "information", "--group", h.app, "--title", clip(n.Title, 200), "--onClickApp", signature}
	if n.Tag != "" {
		args = append(args, "--messageID", n.Tag, "--onClickArgv", "-notified="+n.Tag)
	}
	if h.icon != "" {
		args = append(args, "--icon", h.icon)
	}
	args = append(args, clip(n.Body, 1000))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "notify", args...).CombinedOutput(); err != nil {
		log.Printf("notify: %v: %s", err, out)
	}
}

// writeIcon saves the client's icon where notify can read it, a picture
// file, and returns its path; "" when it could not.
func writeIcon() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(dir, "komarugram-go", "notify-icon.png")
	var b bytes.Buffer
	if err := png.Encode(&b, appicon.Image(32)); err != nil {
		return ""
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b.Bytes()) {
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		return ""
	}
	return path
}

func (h *haikuNotifier) Clicked(string) {}

func (h *haikuNotifier) Close() {}
