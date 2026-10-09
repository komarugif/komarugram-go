// SPDX-License-Identifier: Unlicense OR MIT

package player

import (
	"net"
	"os"

	"golang.org/x/sys/windows"
)

// dialPipe opens the client end of a named pipe, as mpv's IPC on Windows
// is. The handle is asynchronous: with a synchronous one a read waiting for
// the player's next event would hold every write back until it came.
func dialPipe(path string) (net.Conn, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return pipeConn{os.NewFile(uintptr(h), path)}, nil
}

// pipeConn is a named pipe as a net.Conn.
type pipeConn struct{ *os.File }

func (pipeConn) LocalAddr() net.Addr  { return pipeAddr{} }
func (pipeConn) RemoteAddr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
