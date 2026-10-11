// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package player

import (
	"errors"
	"net"
)

// dialPipe opens a named pipe, which only Windows has.
func dialPipe(path string) (net.Conn, error) {
	return nil, errors.New("named pipes are Windows'")
}
