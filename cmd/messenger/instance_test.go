// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSecondInstanceActivatesFirst(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	tokens := make(chan string, 1)
	release, err := claimInstance("", func(token string) { tokens <- token }, func(string) { t.Error("notice opened") })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	t.Setenv("XDG_ACTIVATION_TOKEN", "token-1")
	if _, err := claimInstance("", func(string) { t.Error("second instance activated") }, nil); !errors.Is(err, errRunning) {
		t.Fatalf("second claim: %v, want errRunning", err)
	}
	select {
	case token := <-tokens:
		if token != "token-1" {
			t.Fatalf("activated with %q", token)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first instance not activated")
	}
	// A socket left by an instance that is gone is taken over.
	release()
	again, err := claimInstance("", func(string) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

// TestSecondInstanceOpensNotice checks that a start for a clicked
// notification, as Haiku's notify makes, hands its tag to the running
// instance rather than only showing its windows.
func TestSecondInstanceOpensNotice(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	tags := make(chan string, 1)
	release, err := claimInstance("", func(string) { t.Error("activated instead") }, func(tag string) { tags <- tag })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tag := noticeTag("1234", -1009876543210)
	if _, err := claimInstance(tag, nil, nil); !errors.Is(err, errRunning) {
		t.Fatalf("second claim: %v, want errRunning", err)
	}
	select {
	case got := <-tags:
		if got != tag {
			t.Fatalf("opened %q, want %q", got, tag)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notice not handed over")
	}
}

// TestInstanceOverLoopback checks the instance where Unix sockets cannot be
// made, as on Windows 7: a second start reaches the first on the loopback,
// and a message without the token of the port file is not taken.
func TestInstanceOverLoopback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	noUnixSockets = true
	defer func() { noUnixSockets = false }()
	tags := make(chan string, 2)
	release, err := claimInstance("", func(string) { tags <- "activated" }, func(tag string) { tags <- tag })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := claimInstance("chat", nil, nil); !errors.Is(err, errRunning) {
		t.Fatalf("second claim: %v, want errRunning", err)
	}
	select {
	case got := <-tags:
		if got != "chat" {
			t.Fatalf("opened %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notice not handed over")
	}
	path, _ := instanceSocket()
	data, err := os.ReadFile(instancePort(path))
	if err != nil {
		t.Fatal(err)
	}
	addr, _, _ := strings.Cut(string(data), " ")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("activate\n"))
	conn.Close()
	select {
	case got := <-tags:
		t.Fatalf("a message without the token was taken: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}
