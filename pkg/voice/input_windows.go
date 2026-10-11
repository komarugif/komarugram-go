// SPDX-License-Identifier: Unlicense OR MIT

package voice

import (
	"bytes"
	"context"
	"errors"
	"time"

	"komarugram/pkg/program"
)

// inputs are DirectShow's audio devices, which ffmpeg lists by name.
func inputs(ctx context.Context, ffmpeg string) ([][]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := program.CommandContext(ctx, ffmpeg, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	program.Group(cmd)
	var out bytes.Buffer
	cmd.Stderr = &out
	// ffmpeg fails on the dummy input after listing the devices.
	_ = cmd.Run()
	var list [][]string
	for _, name := range dshowAudio(out.String()) {
		list = append(list, []string{"-f", "dshow", "-i", "audio=" + name})
	}
	if len(list) == 0 {
		return nil, errors.New("no microphone found")
	}
	return list, nil
}
