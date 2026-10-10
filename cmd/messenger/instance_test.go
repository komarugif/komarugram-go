// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"errors"
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
