package dcpool

import (
	"context"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"sync"
	"testing"
	"time"
)

func TestTakeoutDoesNotRelockPool(t *testing.T) {
	p := &pool{mu: &sync.Mutex{}, takeout: 42, invokers: map[int]tg.Invoker{2: telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return nil })}}
	done := make(chan struct{})
	go func() { p.Takeout(context.Background(), 2); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Takeout deadlocked")
	}
}
func TestCloseIdempotentAndRejectsReopen(t *testing.T) {
	calls := 0
	p := &pool{mu: &sync.Mutex{}, closes: map[int]func() error{2: func() error { calls++; return nil }}}
	if e := p.Close(); e != nil {
		t.Fatal(e)
	}
	if e := p.Close(); e != nil || calls != 1 {
		t.Fatal("close not idempotent")
	}
	inv := p.invoker(context.Background(), 2)
	if e := inv.Invoke(context.Background(), nil, nil); e == nil {
		t.Fatal("closed pool reopened")
	}
}

func TestOnTransferSkipsOnlyOwnDC(t *testing.T) {
	ran := false
	transfer := func(context.Context) error { ran = true; return nil }
	own := context.WithValue(context.Background(), ownAuthorization{}, true)
	if err := OnTransfer(own, nil, transfer); err != nil || ran {
		t.Fatalf("own DC transferred: %v, %v", err, ran)
	}
	if err := OnTransfer(context.Background(), nil, transfer); err != nil || !ran {
		t.Fatalf("other DC not transferred: %v, %v", err, ran)
	}
}

func TestHasMediaOnly(t *testing.T) {
	options := []tg.DCOption{
		{ID: 2, IPAddress: "149.154.167.41"},
		{ID: 2, IPAddress: "149.154.167.151", MediaOnly: true},
		{ID: 203, IPAddress: "91.105.192.100", MediaOnly: true, CDN: true},
	}
	if !hasMediaOnly(options, 2) {
		t.Error("DC 2 has a media-only address")
	}
	if hasMediaOnly(options, 203) || hasMediaOnly(options, 4) {
		t.Error("a CDN or missing DC is not a media-only address")
	}
}

// cdnInvoker is a CDN connection that counts its closes.
type cdnInvoker struct{ closes *int }

func (cdnInvoker) Invoke(context.Context, bin.Encoder, bin.Decoder) error { return nil }
func (c cdnInvoker) Close() error {
	*c.closes++
	return nil
}

// A CDN DC's connection is opened once and kept for every file it serves:
// a new one for each file waited for a new auth key. The pool closes it.
func TestCDNConnectionIsKept(t *testing.T) {
	p := NewPool(nil, 1).(*pool)
	opened, closes := 0, 0
	p.openCDN = func(context.Context, int, int64) (telegram.CloseInvoker, error) {
		opened++
		return cdnInvoker{&closes}, nil
	}
	for range 3 {
		_, closer, err := p.CDN(context.Background(), 201, 4)
		if err != nil {
			t.Fatal(err)
		}
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if opened != 1 || closes != 0 {
		t.Fatalf("3 files: %d connections opened, %d closed", opened, closes)
	}
	if _, _, err := p.CDN(context.Background(), 203, 4); err != nil || opened != 2 {
		t.Fatalf("another CDN DC: %d connections opened, err %v", opened, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if closes != 2 {
		t.Fatalf("the pool closed, %d of 2 connections closed", closes)
	}
	if _, _, err := p.CDN(context.Background(), 201, 4); err == nil {
		t.Fatal("a closed pool gave a CDN connection")
	}
}
