// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"bufio"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A write reaches the player while a read waits for its next event, as
// mpv's driver reads all the time: on a synchronous handle the write would
// wait for the read.
func TestPipeWritesWhileReading(t *testing.T) {
	path := pipePath(MPV)
	name, _ := windows.UTF16PtrFromString(path)
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(server)
	// The player answers a line with a line, and says nothing first.
	go func() {
		if err := windows.ConnectNamedPipe(server, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
			return
		}
		buf := make([]byte, 64)
		var n uint32
		if windows.ReadFile(server, buf, &n, nil) == nil {
			windows.WriteFile(server, []byte("pong\n"), &n, nil)
		}
	}()
	conn, err := dialPipe(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(conn).ReadString('\n')
		lines <- line
	}()
	time.Sleep(200 * time.Millisecond) // the read is waiting
	wrote := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("ping\n")); wrote <- err }()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the write waited for the read")
	}
	select {
	case line := <-lines:
		if line != "pong\n" {
			t.Fatalf("read %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no answer")
	}
}
