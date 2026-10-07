// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"log"
	"slices"
	"sync"
	"time"

	"gioui.org/io/system"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/updates/hook"
	"golang.org/x/sync/errgroup"

	"komarugram/internal/appwindow"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"komarugram/internal/messenger/security"
	"komarugram/internal/messenger/tgstore"
	"komarugram/internal/messenger/ui"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/notify"
)

// logOutTimeout bounds how long leaving an account waits for Telegram
// before deleting the local data anyway.
const logOutTimeout = 30 * time.Second

// accountWindows is the process-wide account/window registry. UI windows only
// depend on model.Accounts, which keeps this lifecycle concern out of widgets.
//
// The process starts with one window that knows no account yet (see
// startSpec): it unlocks local data if it is protected, reads the accounts
// and adds those given with -tdata, and then becomes the window of one of
// them, or signs in to a new one.
type accountWindows struct {
	mu          sync.Mutex
	process     *appwindow.Host
	options     appwindow.Options
	manager     *account.Manager
	security    *security.Manager
	preferences *preferences.Store
	miniapps    *miniappprefs.Settings
	// imports are the archives given on the command line, added by start.
	imports []*account.TData
	// startMu serializes start, which runs once it has succeeded.
	startMu  sync.Mutex
	started  bool
	accounts map[string]*account.Account
	order    []string
	// cards are what is known about each account without connecting to it,
	// and photos their decoded avatars. Until cardsLoaded they have not been
	// read for every account.
	cards       map[string]account.Card
	photos      map[string]image.Image
	cardsLoaded bool
	windows     map[string]*appwindow.Window
	opening     map[string]bool
	// leaving are the accounts being logged out of, and closed the channels
	// their windows close when they are gone.
	leaving     map[string]bool
	closed      map[string]chan struct{}
	subscribers map[uint64]func()
	nextID      uint64
	// signIn is the window adding an account, nil if there is none.
	signIn        *appwindow.Window
	signInOpening bool
	// sessions are the running accounts, with or without a window.
	sessions map[string]*accountSession
	// tray, if set, lets a closed window leave its account running in the
	// background while the icon is shown. quitting stops every account.
	tray     interface{ Available() bool }
	notifier notify.Notifier
	quitting bool
}

func newAccountWindows(process *appwindow.Host, options appwindow.Options, manager *account.Manager, protection *security.Manager, preferences *preferences.Store, miniapps *miniappprefs.Settings, imports []*account.TData) *accountWindows {
	return &accountWindows{
		process: process, options: options, manager: manager, security: protection,
		preferences: preferences, miniapps: miniapps, imports: imports,
		accounts: make(map[string]*account.Account),
		cards:    make(map[string]account.Card), photos: make(map[string]image.Image),
		windows: make(map[string]*appwindow.Window), opening: make(map[string]bool),
		leaving: make(map[string]bool), closed: make(map[string]chan struct{}),
		subscribers: make(map[uint64]func()),
		sessions:    make(map[string]*accountSession),
	}
}

// start reads the accounts, which protected data allows only once unlocked,
// and adds those of the -tdata archives, asking with offer first whether to
// protect local data if any of them is new. It returns the accounts added.
func (h *accountWindows) start(ctx context.Context, offer func(context.Context) error) ([]*account.Account, error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	if h.started {
		return nil, nil
	}
	if err := h.manager.Load(); err != nil {
		return nil, err
	}
	h.addAccounts(h.manager.Accounts()...)
	pending := 0
	for _, t := range h.imports {
		n, err := h.manager.NewInTData(t)
		if err != nil {
			return nil, err
		}
		pending += n
	}
	var added []*account.Account
	if pending > 0 {
		if err := offer(ctx); err != nil {
			return nil, err
		}
		for _, t := range h.imports {
			accounts, err := h.manager.ImportTData(ctx, t)
			added = append(added, accounts...)
			if err != nil {
				h.addAccounts(added...)
				return nil, fmt.Errorf("import tdata: %w", err)
			}
		}
		h.addAccounts(added...)
	}
	h.imports, h.started = nil, true
	return added, nil
}

// addAccounts lists accounts the manager has, with their cards.
func (h *accountWindows) addAccounts(accounts ...*account.Account) {
	h.mu.Lock()
	for _, a := range accounts {
		if h.accounts[a.ID] == nil {
			h.accounts[a.ID] = a
			h.order = append(h.order, a.ID)
			h.cardsLoaded = false
		}
	}
	h.mu.Unlock()
	h.loadCards()
	h.notify()
}

// loadCards reads the cards of the listed accounts not read yet.
func (h *accountWindows) loadCards() {
	if h.manager == nil {
		return
	}
	h.mu.Lock()
	loaded := h.cardsLoaded
	h.mu.Unlock()
	if loaded {
		return
	}
	cards, err := h.manager.Cards()
	if err != nil {
		// Without cards the accounts are still usable, only unnamed.
		log.Printf("account list: %v", err)
		return
	}
	photos := make(map[string]image.Image, len(cards))
	for id, card := range cards {
		photos[id] = decodePhoto(id, card.Photo)
	}
	h.mu.Lock()
	for _, id := range h.order {
		if _, ok := h.cards[id]; !ok {
			h.cards[id], h.photos[id] = cards[id], photos[id]
		}
	}
	h.cardsLoaded = true
	h.mu.Unlock()
	h.notify()
}

func decodePhoto(id string, data []byte) image.Image {
	if len(data) == 0 {
		return nil
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		log.Printf("account %s: avatar: %v", id, err)
		return nil
	}
	return im
}

func (h *accountWindows) All() []model.AccountInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make([]model.AccountInfo, 0, len(h.order))
	for i, id := range h.order {
		if h.leaving[id] {
			continue
		}
		result = append(result, model.AccountInfo{
			ID: id, UserID: h.accounts[id].UserID,
			Name: h.nameLocked(id, i+1), Username: h.cards[id].Username, Phone: h.cards[id].Phone,
			Premium: h.cards[id].Premium,
			Avatar:  h.photos[id],
			Open:    h.windows[id] != nil || h.opening[id],
		})
	}
	return result
}

// nameLocked is what account id, the index-th, is called. h.mu must be held.
func (h *accountWindows) nameLocked(id string, index int) string {
	if name := h.cards[id].Name(); name != "" {
		return name
	}
	return fmt.Sprintf("%s %d", h.catalog().T("settings.account"), index)
}

// Add implements model.Accounts: open a sign-in window, or raise the one
// already open.
func (h *accountWindows) Add() {
	h.mu.Lock()
	if window := h.signIn; window != nil {
		h.mu.Unlock()
		// Asked from another window's goroutine: see PerformLater.
		window.PerformLater(system.ActionRaise)
		return
	}
	if h.signInOpening {
		h.mu.Unlock()
		return
	}
	h.signInOpening = true
	h.mu.Unlock()
	h.process.Open(h.windowSpec(nil, nil, false))
}

// Open implements model.Accounts: focus an existing account window or create
// a new one if it was closed.
func (h *accountWindows) Open(id string) {
	h.mu.Lock()
	if window := h.windows[id]; window != nil {
		h.mu.Unlock()
		h.remember(id)
		window.PerformLater(system.ActionRaise)
		return
	}
	a := h.accounts[id]
	if a == nil || h.opening[id] || h.leaving[id] || h.quitting {
		h.mu.Unlock()
		return
	}
	h.opening[id] = true
	// An account running in the background gets its window back.
	session := h.sessions[id]
	h.mu.Unlock()
	h.remember(id)
	h.notify()
	h.process.Open(h.windowSpec(a, session, false))
}

// ShowAll brings the application back from the tray: it raises the open
// windows, with the XDG activation token if there is one, and opens a window
// for every account running in the background.
func (h *accountWindows) ShowAll(token string) {
	h.mu.Lock()
	if h.quitting {
		h.mu.Unlock()
		return
	}
	type background struct {
		account *account.Account
		session *accountSession
	}
	var reopen []background
	for _, id := range h.order {
		s := h.sessions[id]
		if s != nil && h.windows[id] == nil && !h.opening[id] && !h.leaving[id] {
			h.opening[id] = true
			reopen = append(reopen, background{h.accounts[id], s})
		}
	}
	h.mu.Unlock()
	h.process.ActivateAll(token)
	for _, b := range reopen {
		h.process.Open(h.windowSpec(b.account, b.session, false))
	}
	if len(reopen) > 0 {
		h.notify()
	}
}

// Quit closes every window and stops every account, and with them the
// process.
func (h *accountWindows) Quit() {
	h.mu.Lock()
	if h.quitting {
		h.mu.Unlock()
		return
	}
	h.quitting = true
	background := map[string]*accountSession{}
	for id, s := range h.sessions {
		if s.window.Load() == nil {
			background[id] = s
		}
	}
	h.mu.Unlock()
	if h.process != nil {
		h.process.CloseAll()
	}
	for id, s := range background {
		go func() {
			s.stop()
			h.forgetSession(id, s)
		}()
	}
}

// keepInBackground reports whether account id keeps running when its window
// closes.
func (h *accountWindows) keepInBackground(id string) bool {
	if id == "" {
		return false
	}
	h.mu.Lock()
	keep := !h.quitting && !h.leaving[id] && h.tray != nil
	h.mu.Unlock()
	return keep && h.tray.Available()
}

func (h *accountWindows) forgetSession(id string, s *accountSession) {
	h.mu.Lock()
	if h.sessions[id] == s {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
}

// hold keeps the process running for background work.
func (h *accountWindows) hold() func() {
	if h.process == nil {
		return func() {}
	}
	return h.process.Hold()
}

func (h *accountWindows) newSession() *accountSession {
	s := newSession(h.hold(), func(s *accountSession) {
		if id := s.accountID(); id != "" {
			h.updateProfile(id, s.store.Me(), s.window.Load())
		}
		s.invalidate()
	})
	s.store.SetNotices(func(n model.MessageNotice) { h.notice(s, n) })
	return s
}

// LogOut implements model.Accounts. The account's window closes first, so
// that its connection is released; then the session is ended in Telegram,
// as far as Telegram can be reached, and the local data deleted.
func (h *accountWindows) LogOut(id string) {
	h.mu.Lock()
	a := h.accounts[id]
	if a == nil || h.leaving[id] {
		h.mu.Unlock()
		return
	}
	h.leaving[id] = true
	window := h.windows[id]
	session := h.sessions[id]
	var closed chan struct{}
	if window != nil {
		closed = make(chan struct{})
		h.closed[id] = closed
	}
	// The process ends with its last window, so another one has to open
	// first: the next account's, or a sign-in.
	othersOpen := h.signIn != nil || h.signInOpening
	for other, w := range h.windows {
		othersOpen = othersOpen || other != id && w != nil
	}
	next := ""
	for _, other := range h.order {
		if other != id && !h.leaving[other] {
			next = other
			break
		}
	}
	h.mu.Unlock()
	h.notify()
	// Telegram is told even if no window is left open meanwhile.
	release := h.hold()
	if window != nil && !othersOpen {
		if next != "" {
			h.Open(next)
		} else {
			h.Add()
		}
	}
	go func() {
		defer release()
		if window != nil {
			// The window stops the session as it closes.
			window.Perform(system.ActionClose)
			<-closed
		} else if session != nil {
			session.stop()
			h.forgetSession(id, session)
		}
		ctx, cancel := context.WithTimeout(context.Background(), logOutTimeout)
		err := h.manager.LogOut(ctx, a)
		cancel()
		if err != nil {
			log.Printf("account %s: log out in Telegram: %v", id, err)
		}
		if err := h.manager.Remove(a); err != nil {
			log.Printf("account %s: %v", id, err)
			h.mu.Lock()
			delete(h.leaving, id)
			h.mu.Unlock()
			h.notify()
			return
		}
		h.mu.Lock()
		delete(h.accounts, id)
		h.order = slices.DeleteFunc(h.order, func(other string) bool { return other == id })
		delete(h.cards, id)
		delete(h.photos, id)
		delete(h.leaving, id)
		h.mu.Unlock()
		h.notify()
	}()
}

// InitialAccountID chooses the single account restored at process startup.
// Unknown or removed IDs safely fall back to the first saved account.
func (h *accountWindows) InitialAccountID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	last := h.preferences.Global().LastAccountID
	if h.accounts[last] != nil {
		return last
	}
	if len(h.order) != 0 {
		return h.order[0]
	}
	return ""
}

func (h *accountWindows) remember(id string) {
	if id == "" {
		return
	}
	if err := h.preferences.SetLastAccount(id); err != nil {
		log.Printf("save last account: %v", err)
	}
}

func (h *accountWindows) catalog() localization.Catalog {
	return localization.For(h.preferences.Global().Language)
}

func (h *accountWindows) Subscribe(callback func()) func() {
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subscribers[id] = callback
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subscribers, id)
		h.mu.Unlock()
	}
}

func (h *accountWindows) notify() {
	h.mu.Lock()
	callbacks := make([]func(), 0, len(h.subscribers))
	for _, callback := range h.subscribers {
		callbacks = append(callbacks, callback)
	}
	h.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

// startSpec is the first window of the process.
func (h *accountWindows) startSpec() appwindow.Spec {
	return h.windowSpec(nil, nil, true)
}

// windowSpec is the window of account a, attached to its session if it runs
// in the background, or to a new one. Without an account it is a window that
// finds its account on its own: the one the process starts with when startup
// is set, or one signing in to a new account. Either first offers to protect
// local data if an account is about to be added and it is not protected.
func (h *accountWindows) windowSpec(a *account.Account, session *accountSession, startup bool) appwindow.Spec {
	options := h.options
	options.ProfileName = "account-window"
	if a != nil {
		if r := diagnostics.Current(); r != nil {
			options.ProfileName = r.Scope(a.ID)
		}
		h.mu.Lock()
		options.Title = h.catalog().T("app.title") + " — " + h.nameLocked(a.ID, slices.Index(h.order, a.ID)+1)
		h.mu.Unlock()
	}
	if session == nil {
		session = h.newSession()
	}
	var window *appwindow.Window
	var app *ui.App
	return appwindow.Spec{
		Options:   options,
		Activated: func() { h.remember(session.accountID()) },
		Closed: func() {
			session.window.CompareAndSwap(window, nil)
			session.app.CompareAndSwap(app, nil)
			id := session.accountID()
			if id != "" && h.security != nil && h.security.Enabled() && h.preferences.Global().LockOnClose {
				session.locked.Store(true)
			}
			if !h.keepInBackground(id) {
				session.stop()
				h.forgetSession(id, session)
			}
			h.windowClosed(id, window)
		},
		Build: func(w *appwindow.Window) appwindow.Content {
			window = w
			session.window.Store(w)
			content := ui.New(w, session.store, ui.Services{
				Preferences: h.preferences, MiniApps: h.miniapps, Accounts: h, Security: h.security,
				WindowLocked:   &session.locked,
				CurrentAccount: session.accountID,
				OpenWindow:     h.process.Open,
			})
			app = content
			session.app.Store(content)
			if chat := session.openChat.Swap(0); chat != 0 {
				content.OpenChat(chat)
			}
			if a != nil {
				session.id.Store(a.ID)
				h.bind(a, session, w)
				session.start(func(ctx context.Context) { h.runAccount(ctx, a, session.store) })
				return content
			}
			h.mu.Lock()
			if !startup {
				h.signIn = w
				h.signInOpening = false
			}
			h.mu.Unlock()
			signIn := login.New(w.Invalidate)
			content.RequireLogin(signIn)
			session.start(func(ctx context.Context) {
				if h.security != nil {
					// The window shows the unlock screen meanwhile.
					if err := h.security.WaitUnlocked(ctx); err != nil {
						return
					}
				}
				offered := false
				offer := func(ctx context.Context) error {
					if offered || h.security == nil || h.security.Enabled() {
						return nil
					}
					offered = true
					return signIn.OfferProtection(ctx)
				}
				err := signIn.Run(ctx, func(ctx context.Context, p account.Prompter) error {
					var chosen *account.Account
					if startup {
						added, err := h.start(ctx, offer)
						if err != nil {
							return err
						}
						if len(added) > 0 {
							chosen = added[0]
							for _, other := range added[1:] {
								h.Open(other.ID)
							}
						} else {
							id := h.InitialAccountID()
							h.mu.Lock()
							chosen = h.accounts[id]
							h.mu.Unlock()
						}
					}
					if chosen == nil {
						if err := offer(ctx); err != nil {
							return err
						}
						a, err := h.manager.Add(ctx, p)
						if err != nil {
							return err
						}
						h.addAccounts(a)
						chosen = a
					}
					session.id.Store(chosen.ID)
					h.bind(chosen, session, w)
					h.remember(chosen.ID)
					signIn.Finish()
					// The sign-in is over: what fails from here on is told
					// by the account's window, not by the sign-in.
					h.runAccount(ctx, chosen, session.store)
					return nil
				})
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Print(err)
				}
			})
			return content
		},
	}
}

// bind makes w the window of a, and session its session.
func (h *accountWindows) bind(a *account.Account, session *accountSession, w *appwindow.Window) {
	h.mu.Lock()
	h.sessions[a.ID] = session
	h.windows[a.ID] = w
	delete(h.opening, a.ID)
	// A sign-in window that has its account is that account's window now;
	// another Add opens a sign-in of its own.
	if h.signIn == w {
		h.signIn = nil
	}
	title := h.catalog().T("app.title") + " — " + h.nameLocked(a.ID, slices.Index(h.order, a.ID)+1)
	h.mu.Unlock()
	w.SetTitle(title)
	h.notify()
}

// windowClosed forgets window w, of account id if it had one.
func (h *accountWindows) windowClosed(id string, w *appwindow.Window) {
	h.mu.Lock()
	var closed chan struct{}
	if id != "" && h.windows[id] == w {
		delete(h.windows, id)
		delete(h.opening, id)
		closed = h.closed[id]
		delete(h.closed, id)
	}
	if h.signIn == w {
		h.signIn = nil
	}
	h.mu.Unlock()
	if closed != nil {
		close(closed)
	}
	h.notify()
}

// updateProfile records the profile a connected store has loaded, and saves
// it in the account's card if it changed. w is the account's window, nil
// while it runs in the background.
func (h *accountWindows) updateProfile(id string, profile model.Profile, w *appwindow.Window) {
	if profile.Name() == "" {
		return
	}
	if w != nil {
		h.mu.Lock()
		locked := h.sessions[id] != nil && h.sessions[id].locked.Load()
		h.mu.Unlock()
		if !locked {
			w.SetTitle(h.catalog().T("app.title") + " — " + profile.Name())
		}
	}
	old, ok := h.card(id)
	if !ok {
		return
	}
	card := old
	card.FirstName, card.LastName = profile.FirstName, profile.LastName
	card.Username, card.Phone, card.Premium = profile.Username, profile.Phone, profile.Premium
	if card.FirstName == old.FirstName && card.LastName == old.LastName &&
		card.Username == old.Username && card.Phone == old.Phone && card.Premium == old.Premium {
		return
	}
	h.saveCard(id, card, nil)
}

// card returns the card of account id. It reports false when the cards have
// not been read, so that a caller does not replace a saved card it has not
// seen.
func (h *accountWindows) card(id string) (account.Card, bool) {
	h.loadCards()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cards[id], h.cardsLoaded
}

// saveCard makes card the one of account id, with photo decoded from it or
// nil to keep the current one.
func (h *accountWindows) saveCard(id string, card account.Card, photo image.Image) {
	h.mu.Lock()
	a := h.accounts[id]
	h.cards[id] = card
	if photo != nil || card.PhotoID == 0 {
		h.photos[id] = photo
	}
	h.mu.Unlock()
	if a != nil && h.manager != nil {
		if err := h.manager.SaveCard(a, card); err != nil {
			log.Printf("account %s: save card: %v", id, err)
		}
	}
	h.notify()
}

// refreshPhoto downloads the account's small profile photo into its card
// when it differs from the one saved.
func (h *accountWindows) refreshPhoto(ctx context.Context, a *account.Account, store *tgstore.Store) {
	saved, ok := h.card(a.ID)
	if !ok || store.SelfPhotoID() == saved.PhotoID {
		return
	}
	photoID, data, err := store.SelfPhoto(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("account %s: profile photo: %v", a.ID, err)
		}
		return
	}
	if len(data) > account.MaxCardPhoto {
		log.Printf("account %s: profile photo of %d bytes is too large", a.ID, len(data))
		return
	}
	photo := decodePhoto(a.ID, data)
	if photo == nil {
		photoID, data = 0, nil
	}
	card, _ := h.card(a.ID)
	card.PhotoID, card.Photo = photoID, data
	h.saveCard(a.ID, card, photo)
}

var _ model.Accounts = (*accountWindows)(nil)

// runAccount keeps account a connected until ctx ends. A connection that
// stops is told by the account's window, which offers to make it again,
// unless Telegram ended the session: that has a dialog of its own.
func (h *accountWindows) runAccount(ctx context.Context, a *account.Account, store *tgstore.Store) {
	keepConnected(ctx, func(ctx context.Context) error {
		err := h.runStore(ctx, a, store)
		if err != nil && ctx.Err() == nil {
			log.Printf("account %s: %v", a.ID, err)
		}
		return err
	}, store)
}

// connection is where keepConnected tells a failure and waits to be asked
// for the connection again.
type connection interface {
	FailConnection(error)
	Reconnects() <-chan struct{}
}

// keepConnected calls connect until ctx ends or Telegram ends the session;
// after any other failure it waits to be asked for the connection again.
func keepConnected(ctx context.Context, connect func(context.Context) error, c connection) {
	for {
		err := connect(ctx)
		if err == nil || ctx.Err() != nil || tgstore.SessionEnd(err) != model.SessionAlive {
			return
		}
		c.FailConnection(err)
		select {
		case <-c.Reconnects():
		case <-ctx.Done():
			return
		}
	}
}

func (h *accountWindows) runStore(ctx context.Context, a *account.Account, store *tgstore.Store) error {
	if card, ok := h.card(a.ID); ok {
		store.Remember(model.Profile{ID: a.UserID, FirstName: card.FirstName, LastName: card.LastName,
			Username: card.Username, Phone: card.Phone, Badges: model.Badges{Premium: card.Premium}, DC: a.DC})
	}
	err := h.runClient(ctx, a, store)
	if why := tgstore.SessionEnd(err); why != model.SessionAlive {
		// The window asks whether to log out; the account stays until then.
		store.EndSession(why)
	}
	return err
}

func (h *accountWindows) runClient(ctx context.Context, a *account.Account, store *tgstore.Store) error {
	if err := store.Configure(ctx, a.ID, a.HistoryPath(), h.security); err != nil {
		return err
	}
	defer store.Disconnect()
	manager := store.Updates()
	return h.manager.RunClient(ctx, a, manager, func(ctx context.Context, client *telegram.Client) error {
		store.Attach(client)
		group, runCtx := errgroup.WithContext(ctx)
		group.Go(func() error {
			return manager.Run(runCtx, client.API(), a.UserID, updates.AuthOptions{OnStart: func(context.Context) {}})
		})
		group.Go(func() error {
			if err := store.Load(runCtx, client.API()); err != nil {
				return err
			}
			h.refreshPhoto(runCtx, a, store)
			<-runCtx.Done()
			return nil
		})
		return group.Wait()
	},
		// Updates that come as replies to requests, and the pts of their
		// messages.affected* replies, go to the manager too: without them
		// its pts falls behind after each read, deletion or message sent,
		// and the next update waits for a getDifference.
		store.Middleware(), hook.UpdateHook(manager.Handle), hook.AffectedHook(manager))
}
