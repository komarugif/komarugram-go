// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// instanceSocket is where the running instance listens; Unix sockets work on
// Windows 10 1803 and later as well. Where they do not, as on Windows 7, it
// listens on the loopback instead, and says where in the file of
// instancePort beside the socket.
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

// instancePort is the file that names the loopback address the running
// instance listens on where there are no Unix sockets, with the token a
// message to it starts with: any program on the machine can reach the
// loopback, only the user can read the file.
func instancePort(socket string) string {
	return filepath.Join(filepath.Dir(socket), "komarugram-go.port")
}

// noUnixSockets makes the instance use the loopback, as where Unix sockets
// cannot be made; for tests.
var noUnixSockets bool

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
	if conn, prefix, err := dialInstance(path); err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		line := "activate " + os.Getenv("XDG_ACTIVATION_TOKEN")
		if notice != "" {
			line = "open " + notice
		}
		if _, err := conn.Write([]byte(prefix + line + "\n")); err != nil {
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
	ln, prefix, err := listenInstance(path)
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
				line, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), prefix)
				if !ok {
					return
				}
				if token, ok := strings.CutPrefix(line, "activate"); ok {
					activate(strings.TrimSpace(token))
				} else if tag, ok := strings.CutPrefix(line, "open "); ok {
					open(tag)
				}
			}()
		}
	}()
	// Closing a listener made by Listen removes its socket.
	return func() {
		ln.Close()
		if prefix != "" {
			os.Remove(instancePort(path))
		}
	}, nil
}

// dialInstance reaches the running instance: on its socket, or on the
// loopback address its port file names, and then each message starts with
// prefix, the token of that file.
func dialInstance(path string) (conn net.Conn, prefix string, err error) {
	if !noUnixSockets {
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			return conn, "", nil
		}
	}
	data, err := os.ReadFile(instancePort(path))
	if err != nil {
		return nil, "", err
	}
	addr, token, ok := strings.Cut(strings.TrimSpace(string(data)), " ")
	if !ok || !strings.HasPrefix(addr, "127.0.0.1:") {
		return nil, "", errors.New("single instance: bad port file")
	}
	conn, err = net.DialTimeout("tcp", addr, time.Second)
	return conn, token + " ", err
}

// listenInstance listens on the socket, or where Unix sockets cannot be
// made, on the loopback, naming the address and a token in the port file.
func listenInstance(path string) (ln net.Listener, prefix string, err error) {
	if !noUnixSockets {
		ln, err := net.Listen("unix", path)
		if err == nil {
			return ln, "", nil
		}
		log.Printf("single instance: %v; using the loopback", err)
	}
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	token := make([]byte, 16)
	rand.Read(token)
	prefix = hex.EncodeToString(token) + " "
	if err := os.WriteFile(instancePort(path), []byte(ln.Addr().String()+" "+prefix+"\n"), 0o600); err != nil {
		ln.Close()
		return nil, "", err
	}
	return ln, prefix, nil
}
