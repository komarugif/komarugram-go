// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/program"
)

// vlcPoll is how often VLC is asked for the position and duration: its
// remote control interface reports them only when asked.
const vlcPoll = 250 * time.Millisecond

// vlc is one running VLC process, driven over its remote control interface
// (the "oldrc" module): a line of text per command, and a line per answer.
//
// Answers carry no request id, so the queries in flight are kept in the
// order they were sent: every bare number VLC writes answers the oldest.
// Everything else it writes is either an event ("status change: …") or the
// result of a command ("seek: returned 0 (no error)"). Neither is
// translated, unlike some of its help texts, so VLC keeps the user's
// language in its window.
type vlc struct {
	cmd    *exec.Cmd
	socket string
	conn   net.Conn

	mu      sync.Mutex
	status  Status
	queries []string
	closed  bool
	done    chan struct{}
}

func openVLC(ctx context.Context, path, source string, extra []string) (*vlc, error) {
	network, address, err := vlcEndpoint(path)
	if err != nil {
		return nil, err
	}
	control := "--rc-unix=" + address
	if network == "tcp" {
		control = "--rc-host=" + address
	}
	args := []string{
		"--extraintf=oldrc", control,
		// Without a terminal the interface does not start on a socket.
		"--rc-fake-tty",
		// Stay on the last frame, as mpv's --keep-open does.
		"--play-and-pause",
		// The source is named after the kind of media, not the file:
		// showing it over the picture says nothing.
		"--no-video-title-show",
	}
	if runtime.GOOS == "windows" {
		// Otherwise the interface opens a console window of its own.
		args = append(args, "--rc-quiet")
	}
	if hasOneInstance() {
		// A second VLC would otherwise hand the file to the first one and
		// exit, which reads as the window being closed.
		args = append(args, "--no-one-instance", "--no-playlist-enqueue")
	}
	args = append(args, extra...)
	args = append(args, source)

	cmd := exec.CommandContext(ctx, path, args...)
	program.Group(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start vlc: %w", err)
	}
	player := &vlc{
		cmd:    cmd,
		status: Status{Running: true, File: source},
		done:   make(chan struct{}),
	}
	if network == "unix" {
		player.socket = address
	}
	conn, err := dial(ctx, network, address)
	if err != nil {
		_ = program.KillGroup(cmd)
		_ = cmd.Wait()
		player.removeSocket()
		return nil, err
	}
	player.conn = conn

	go player.readLoop()
	go player.wait()
	go player.poll()
	return player, nil
}

// vlcEndpoint picks where VLC listens for commands: a Unix socket where it
// can, and loopback TCP on Windows and for a flatpak when there is no runtime
// directory to share a socket through.
func vlcEndpoint(path string) (network, address string, err error) {
	if runtime.GOOS != "windows" {
		if socket := socketPath(VLC, path); socket != "" {
			return "unix", socket, nil
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("find a port for vlc: %w", err)
	}
	address = listener.Addr().String()
	_ = listener.Close()
	return "tcp", address, nil
}

// poll asks for the position and duration until VLC exits.
func (p *vlc) poll() {
	ticker := time.NewTicker(vlcPoll)
	defer ticker.Stop()
	for {
		if p.query("get_time", "get_length") != nil {
			return
		}
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
	}
}

// query sends commands that VLC answers with a number.
func (p *vlc) query(commands ...string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("player is closed")
	}
	// Queue before writing, so that the answer never comes first; the lock
	// keeps the queue in the order of the writes.
	p.queries = append(p.queries, commands...)
	_, err := p.conn.Write([]byte(strings.Join(commands, "\n") + "\n"))
	return err
}

// command sends a command whose answer is not needed.
func (p *vlc) command(line string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return fmt.Errorf("player is closed")
	}
	_, err := p.conn.Write([]byte(line + "\n"))
	return err
}

func (p *vlc) readLoop() {
	scanner := bufio.NewScanner(p.conn)
	for scanner.Scan() {
		p.apply(strings.TrimSpace(scanner.Text()))
	}
}

// apply takes a line VLC wrote.
func (p *vlc) apply(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if event, ok := strings.CutPrefix(line, "status change:"); ok {
		// VLC 3 numbers its states: 3 is playing, 4 paused.
		switch {
		case strings.Contains(event, "pause state:"):
			p.status.Paused = true
		case strings.Contains(event, "play state: 3"):
			p.status.Paused = false
		}
		return
	}
	seconds, err := strconv.Atoi(line)
	if err != nil || len(p.queries) == 0 {
		return
	}
	query := p.queries[0]
	p.queries = p.queries[1:]
	value := time.Duration(seconds) * time.Second
	switch query {
	case "get_time":
		p.status.Position = value
	case "get_length":
		p.status.Duration = value
	}
}

// wait notices when the window is closed by the user.
func (p *vlc) wait() {
	err := p.cmd.Wait()
	close(p.done)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.Running = false
	if err != nil && !p.closed {
		p.status.Err = err
	}
}

// TogglePause flips playback without restarting VLC.
func (p *vlc) TogglePause() error {
	return p.command("pause")
}

// Seek moves by delta. VLC's remote control seeks to a position, so the
// target is counted from the last position it reported.
func (p *vlc) Seek(delta time.Duration) error {
	target := max(0, p.Status().Position+delta)
	return p.command("seek " + strconv.Itoa(int(target/time.Second)))
}

// Status reports the latest state VLC sent.
func (p *vlc) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Close stops VLC and removes its socket.
func (p *vlc) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	conn := p.conn
	if conn != nil {
		_, _ = conn.Write([]byte("quit\n"))
	}
	p.closed = true
	p.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	// A flatpak VLC is bwrap's child: the whole group has to go.
	_ = program.KillGroup(p.cmd)
	p.removeSocket()
	return nil
}

func (p *vlc) removeSocket() {
	if p.socket != "" {
		_ = os.Remove(p.socket)
	}
}

// hasOneInstance reports whether VLC knows --one-instance here: it has it
// only on Windows and where it is built with D-Bus (src/libvlc-module.c),
// and it refuses to start on an option it does not know. VLC for Haiku
// and for macOS has no D-Bus.
func hasOneInstance() bool {
	switch runtime.GOOS {
	case "darwin", "haiku":
		return false
	}
	return true
}
