// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"komarugram/pkg/program"
)

// mpv is one running mpv process, driven over its JSON IPC socket, which
// pushes property changes instead of being polled.
//
// The IPC channel is a Unix domain socket on Linux, macOS and Haiku, and a
// named pipe on Windows (--input-ipc-server=\\.\pipe\name), with the same
// JSON protocol over it.
type mpv struct {
	cmd    *exec.Cmd
	socket string
	conn   net.Conn

	mu       sync.Mutex
	status   Status
	nextID   int
	pending  map[int]chan json.RawMessage
	closed   bool
	closeErr error
}

func openMPV(ctx context.Context, path, source string, extra []string) (*mpv, error) {
	network, socket := "unix", socketPath(MPV, path)
	if runtime.GOOS == "windows" {
		network, socket = "pipe", pipePath(MPV)
	}
	if socket == "" {
		return nil, fmt.Errorf("no directory for the socket of %s", path)
	}

	args := append([]string{
		"--input-ipc-server=" + socket,
		// The window belongs to mpv, so the application does not have to
		// reimplement a seek bar, subtitles or keyboard shortcuts.
		"--force-window=yes",
		"--keep-open=yes",
		"--no-terminal",
	}, extra...)
	args = append(args, source)

	cmd := exec.CommandContext(ctx, path, args...)
	program.Group(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mpv: %w", err)
	}

	player := &mpv{
		cmd:     cmd,
		socket:  socket,
		pending: map[int]chan json.RawMessage{},
		status:  Status{Running: true, File: source},
	}

	conn, err := dial(ctx, network, socket)
	if err != nil {
		_ = program.KillGroup(cmd)
		_ = cmd.Wait()
		return nil, err
	}
	player.conn = conn

	go player.readLoop()
	go player.wait()

	// Ask mpv to push these properties whenever they change, instead of
	// polling it from the layout loop.
	for id, property := range map[int]string{1: "time-pos", 2: "duration", 3: "pause", 4: "eof-reached"} {
		if err := player.send(map[string]any{"command": []any{"observe_property", id, property}}); err != nil {
			_ = player.Close()
			return nil, err
		}
	}
	return player, nil
}

// readLoop consumes property updates and command replies.
func (p *mpv) readLoop() {
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 0, 8192), 1<<20)
	for scanner.Scan() {
		var message struct {
			Event     string          `json:"event"`
			Name      string          `json:"name"`
			Data      json.RawMessage `json:"data"`
			RequestID int             `json:"request_id"`
			Error     string          `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}

		if message.Event == "property-change" {
			p.applyProperty(message.Name, message.Data)
			continue
		}
		if message.Event == "" && message.RequestID != 0 {
			p.mu.Lock()
			waiter := p.pending[message.RequestID]
			delete(p.pending, message.RequestID)
			p.mu.Unlock()
			if waiter != nil {
				waiter <- message.Data
			}
		}
	}
}

func (p *mpv) applyProperty(name string, data json.RawMessage) {
	var seconds float64
	var flag bool
	p.mu.Lock()
	defer p.mu.Unlock()
	switch name {
	case "time-pos":
		if json.Unmarshal(data, &seconds) == nil {
			p.status.Position = time.Duration(seconds * float64(time.Second))
		}
	case "duration":
		if json.Unmarshal(data, &seconds) == nil {
			p.status.Duration = time.Duration(seconds * float64(time.Second))
		}
	case "pause":
		if json.Unmarshal(data, &flag) == nil {
			p.status.Paused = flag
		}
	}
}

// wait notices when the window is closed by the user.
func (p *mpv) wait() {
	err := p.cmd.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.Running = false
	if err != nil && !p.closed {
		p.status.Err = err
	}
}

func (p *mpv) send(command map[string]any) error {
	p.mu.Lock()
	if p.closed || p.conn == nil {
		p.mu.Unlock()
		return fmt.Errorf("player is closed")
	}
	p.nextID++
	id := p.nextID
	command["request_id"] = id
	conn := p.conn
	p.mu.Unlock()

	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(payload, '\n'))
	return err
}

// Command sends a raw mpv command, e.g. Command("seek", 10, "relative").
func (p *mpv) Command(args ...any) error {
	return p.send(map[string]any{"command": args})
}

// TogglePause flips playback without restarting mpv.
func (p *mpv) TogglePause() error {
	return p.Command("cycle", "pause")
}

// Seek moves by delta, forward or backward.
func (p *mpv) Seek(delta time.Duration) error {
	return p.Command("seek", strconv.FormatFloat(delta.Seconds(), 'f', 3, 64), "relative")
}

// Status reports the latest state pushed by mpv.
func (p *mpv) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Close stops mpv and removes its socket.
func (p *mpv) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return p.closeErr
	}
	p.closed = true
	conn := p.conn
	p.mu.Unlock()

	if conn != nil {
		_ = p.send(map[string]any{"command": []any{"quit"}})
		_ = conn.Close()
	}
	_ = program.KillGroup(p.cmd)
	if runtime.GOOS != "windows" {
		_ = os.Remove(p.socket)
	}
	return nil
}
