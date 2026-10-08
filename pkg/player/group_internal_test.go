// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package player

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"komarugram/pkg/program"
)

// TestCloseLeavesNothing checks that closing VLC ends every process it
// started. A flatpak VLC runs under bwrap, and killing bwrap alone leaves
// VLC playing with no parent.
func TestCloseLeavesNothing(t *testing.T) {
	var paths []string
	if path, err := exec.LookPath("vlc"); err == nil {
		paths = append(paths, path)
	}
	if path := program.FindFlatpak(VLC.FlatpakID()); path != "" {
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		t.Skip("vlc is not installed")
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			// vlc://pause keeps VLC waiting with nothing to play. With no
			// item ("") VLC for Haiku 3.0.23 crashes within seconds, and
			// the system shows its crash dialog for each run.
			p, err := openVLC(context.Background(), path, "vlc://pause:60", []string{"--intf=dummy", "--vout=dummy", "--aout=dummy", "--ignore-config"})
			if err != nil {
				t.Fatal(err)
			}
			group := p.cmd.Process.Pid
			// Close before VLC could quit on its own, as when a window closes.
			p.conn.Close()
			p.Close()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(-group, 0); errors.Is(err, syscall.ESRCH) {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Errorf("processes of group %d still run after Close", group)
			syscall.Kill(-group, syscall.SIGKILL)
		})
	}
}
