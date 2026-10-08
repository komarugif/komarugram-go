// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"context"
	"log"
	"sync"
	"sync/atomic"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/tgstore"
	"komarugram/internal/messenger/ui"
)

// accountSession is the store and connection of one account, or of a window
// signing in to one. It outlives its window while the application waits in
// the tray, so that updates keep arriving; the window attaches to it and
// detaches again.
type accountSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	store  *tgstore.Store
	// id is the account, "" until a sign-in window has one.
	id atomic.Value
	// window is the attached window, nil while the session runs in the
	// background.
	window atomic.Pointer[appwindow.Window]
	// locked survives closing and recreating the window in the tray.
	locked atomic.Bool
	// app is the window's content while there is a window; openChat is a
	// chat for the next window to open.
	app      atomic.Pointer[ui.App]
	openChat atomic.Int64
	// hold keeps the process running while the session does.
	hold    func()
	running atomic.Bool
	workers sync.WaitGroup
	stop    func()
}

// newSession returns a session of no account yet. changed is called with
// the session whenever its store has read more.
func newSession(hold func(), changed func(*accountSession)) *accountSession {
	s := &accountSession{hold: hold}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.id.Store("")
	s.store = tgstore.New(func() { changed(s) })
	s.stop = sync.OnceFunc(func() {
		s.cancel()
		s.workers.Wait()
		if err := s.store.Close(); err != nil {
			log.Printf("close history cache: %v", err)
		}
		s.hold()
	})
	return s
}

func (s *accountSession) accountID() string { return s.id.Load().(string) }

// start runs the session's worker, once; Stop waits for it.
func (s *accountSession) start(run func(ctx context.Context)) {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		run(s.ctx)
	}()
}

// handOff runs the session's work on from here with run, on a goroutine of
// its own, so that the caller's worker can end: a sign-in's worker holds
// its window, which would stay in memory, closed or not, for as long as
// the account ran on that worker. Stop waits for run too.
func (s *accountSession) handOff(run func(ctx context.Context)) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		run(s.ctx)
	}()
}

// invalidate redraws the attached window, if any.
func (s *accountSession) invalidate() {
	if w := s.window.Load(); w != nil {
		w.Invalidate()
	}
}
