// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bufio"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// instanceSocket is where the running instance listens; Unix sockets work on
// Windows 10 1803 and later as well.
func instanceSocket() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cache, "komarugram-go")
	}
	return filepath.Join(dir, "komarugram-go.sock"), nil
}

// maxSocketPath is the shortest limit on a socket path, Linux's 108 bytes
// including the terminating zero.
const maxSocketPath = 107

// errRunning reports that another instance is running; it has been asked to
// show its windows.
var errRunning = errors.New("the messenger is already running")

// claimInstance makes this process the running instance, whose activate is
// called with an XDG activation token, maybe empty, whenever the messenger is
// started again, and whose open is called with the tag of a notification
// when it is started to open one (-notified). If another instance is
// running, it asks that one to show its windows, with this process's
// activation token, or to open notice when it is not empty, and returns
// errRunning. Where the socket cannot be used, every process runs on its
// own, as before.
func claimInstance(notice string, activate func(token string), open func(tag string)) (release func(), err error) {
	release = func() {}
	path, err := instanceSocket()
	if err != nil || len(path) > maxSocketPath {
		return release, nil
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		line := "activate " + os.Getenv("XDG_ACTIVATION_TOKEN")
		if notice != "" {
			line = "open " + notice
		}
		if _, err := conn.Write([]byte(line + "\n")); err != nil {
			return release, err
		}
		return release, errRunning
	}
	// Nobody answers: the socket, if any, is left over from a crash.
	os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("single instance: %v", err)
		return release, nil
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("single instance: %v", err)
		return release, nil
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimSuffix(line, "\n")
				if token, ok := strings.CutPrefix(line, "activate"); ok {
					activate(strings.TrimSpace(token))
				} else if tag, ok := strings.CutPrefix(line, "open "); ok {
					open(tag)
				}
			}()
		}
	}()
	// Closing a listener made by Listen removes its socket.
	return func() { ln.Close() }, nil
}
