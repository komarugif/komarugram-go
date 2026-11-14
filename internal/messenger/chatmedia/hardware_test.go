// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/hwvideo"
	"komarugram/pkg/program"
)

// hardwareHelperEnv makes the test binary the helper that decodes on the
// GPU, as the client's binary is with its flag.
const hardwareHelperEnv = "KOMARUGRAM_CHATMEDIA_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(hardwareHelperEnv) != "" {
		_ = hwvideo.Serve(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func hardwareClient(t *testing.T) *hwvideo.Client {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := hwvideo.NewClient(func() *exec.Cmd {
		cmd := program.Command(self)
		cmd.Env = append(os.Environ(), hardwareHelperEnv+"=1")
		return cmd
	})
	t.Cleanup(c.Close)
	return c
}

func savedGIF(t *testing.T) (*fakeSource, model.Message) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", "video.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSource{data: data}, model.Message{Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif/saved", MIMEType: "video/mp4", Width: 720, Height: 1280}}
}

// playSaved draws the list of saved GIFs with msg in it for a while, and
// returns how many pictures of it the frames showed.
func playSaved(m *Manager, msg model.Message, long time.Duration) (pictures int, row bool) {
	seen := map[image.Image]bool{}
	for until := time.Now().Add(long); time.Now().Before(until); {
		m.BeginFrameAt(time.Now())
		if _, ok := m.GIFRow([]model.Message{msg}, []int{72}, 128); ok {
			row = true
		}
		if im := m.StatusFit(msg, true, image.Pt(72, 128), true).Frame; im != nil {
			seen[im] = true
		}
		m.EndFrame()
		time.Sleep(10 * time.Millisecond)
	}
	return len(seen), row
}

// A GIF of the list of saved ones is decoded on the GPU, in the helper,
// where the machine has a decoder. It needs one: Windows with a GPU that
// decodes H.264.
func TestSavedGIFPlaysOnGPU(t *testing.T) {
	source, msg := savedGIF(t)
	client := hardwareClient(t)
	m := NewShared(source, func() {})
	defer m.Close()
	m.SetHardware(client)
	pictures, row := playSaved(m, msg, 2*time.Second)
	if err := client.Failed(); err != nil {
		t.Skipf("no hardware decoder: %v", err)
	}
	if row {
		t.Error("the row was played as one, with FFmpeg")
	}
	if client.Opened() == 0 {
		t.Fatal("the helper opened no video")
	}
	if _, failed := m.hardwareFailed.Load(msg.Media.ID); failed {
		t.Fatal("the GIF was left to software")
	}
	if pictures < 20 {
		t.Fatalf("%d pictures in 2 s", pictures)
	}
}

// Where nothing decodes in hardware, a machine without a decoder or a
// helper that ended, the list of saved GIFs plays as it does without:
// nothing is asked of the helper again.
func TestSavedGIFsWithoutHardwarePlayAsBefore(t *testing.T) {
	source, msg := savedGIF(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// A helper that ends at once, as one whose driver falls.
	client := hwvideo.NewClient(func() *exec.Cmd { return program.Command(self, "-test.run=^$") })
	t.Cleanup(client.Close)
	m := NewShared(source, func() {})
	defer m.Close()
	m.SetHardware(client)
	if !m.hardwareRow([]model.Message{msg}) {
		t.Fatal("the row is not for the hardware before it failed")
	}
	deadline := time.Now().Add(10 * time.Second)
	for client.Failed() == nil && time.Now().Before(deadline) {
		playSaved(m, msg, 50*time.Millisecond)
	}
	if client.Failed() == nil {
		t.Fatal("the client did not fail")
	}
	if m.hardwareRow([]model.Message{msg}) {
		t.Fatal("the row is still for the hardware after it failed")
	}
	if _, failed := m.hardwareFailed.Load(msg.Media.ID); !failed {
		t.Fatal("the GIF was not left to software")
	}
}
