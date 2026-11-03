// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"sync"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// fakeStorage is a store whose answers the test sets.
type fakeStorage struct {
	mu      sync.Mutex
	found   model.StorageFound
	bound   model.StorageFound
	pass    model.StoragePass
	sent    int
	missing bool
}

func (f *fakeStorage) FindStorageChannel(context.Context) (model.StorageFound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.found, nil
}

func (f *fakeStorage) BindStorageChannel(context.Context, int64) (model.StorageFound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bound, nil
}

func (f *fakeStorage) SendAllGIFs(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	f.pass = model.StoragePass{Active: true, Done: 40, Total: 200}
	return nil
}

func (f *fakeStorage) RememberStorage(model.StorageBinding, func(model.StorageBinding)) {}

func (f *fakeStorage) StorageMissing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.missing
	f.missing = false
	return m
}

func (f *fakeStorage) StoragePassState() model.StoragePass {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pass
}

func (f *fakeStorage) setPass(p model.StoragePass) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pass = p
}

func storageFrame(o *storageOffer, toasts *[]string, size image.Point) *op.Ops {
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Now: time.Now().Add(time.Second), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	fillRect(gtx, scheme(gtx).Surface.Color, size)
	l := localization.For("ru")
	o.Update(gtx, func(s string) { *toasts = append(*toasts, s) }, l, func() {})
	o.Layout(gtx, l)
	return ops
}

// storageSettled draws frames until the store has answered.
func storageSettled(t *testing.T, o *storageOffer, toasts *[]string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		storageFrame(o, toasts, image.Pt(600, 800))
		if !o.busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the store did not answer")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// With no channel of the name, the user is asked. With one, nothing is asked
// but sending the GIFs to it, when it has none.
func TestStorageOfferAsksOnlyWithoutChannel(t *testing.T) {
	var toasts []string
	source := &fakeStorage{found: model.StorageFound{Bound: true, Title: model.StorageChannelTitle}}
	o := newStorageOffer(source)
	o.Activated(func() {})
	storageSettled(t, o, &toasts)
	if o.modal.Shown() || len(toasts) != 1 {
		t.Fatalf("a channel found: shown %v, toasts %q", o.modal.Shown(), toasts)
	}
	source.found = model.StorageFound{Bound: true, Title: model.StorageChannelTitle, SendAll: 3}
	o = newStorageOffer(source)
	o.Activated(func() {})
	storageSettled(t, o, &toasts)
	if !o.modal.Shown() || o.page != storageSend {
		t.Fatalf("an empty channel: shown %v, page %d", o.modal.Shown(), o.page)
	}
	source.found = model.StorageFound{Candidates: []model.StorageChannel{{ID: 1, Title: "a"}}}
	o = newStorageOffer(source)
	o.Activated(func() {})
	storageSettled(t, o, &toasts)
	if !o.modal.Shown() || o.page != storageAsk || len(o.rows) != 1 {
		t.Fatalf("no channel: shown %v, page %d", o.modal.Shown(), o.page)
	}
}

// While the GIFs go, neither Escape nor a click beside the dialog closes it, and
// it closes itself when they have gone. It opens by itself too: a pass goes on
// after a restart.
func TestStorageOfferProgressCannotBeClosed(t *testing.T) {
	var toasts []string
	source := &fakeStorage{pass: model.StoragePass{Active: true, Done: 1, Total: 2}}
	o := newStorageOffer(source)
	storageFrame(o, &toasts, image.Pt(600, 800))
	if !o.modal.Shown() || o.page != storageProgress {
		t.Fatal("the pass under way shows no dialog")
	}
	o.modal.scrim.Click()
	for range 3 {
		storageFrame(o, &toasts, image.Pt(600, 800))
	}
	if !o.modal.Shown() || o.modal.closing {
		t.Fatal("a click beside the dialog closed it")
	}
	source.setPass(model.StoragePass{})
	for range 3 {
		storageFrame(o, &toasts, image.Pt(600, 800))
	}
	if !o.modal.closing || len(toasts) != 1 {
		t.Fatalf("the pass ended: closing %v, toasts %q", o.modal.closing, toasts)
	}
}

// TestRenderStorageOffer saves each page of the dialog, for looking at.
func TestRenderStorageOffer(t *testing.T) {
	dir := os.Getenv("STORAGE_PNG_DIR")
	if dir == "" {
		t.Skip("set STORAGE_PNG_DIR to a directory")
	}
	size := image.Pt(800, 640)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	candidates := []model.StorageChannel{{ID: 1, Title: "Мой архив"}, {ID: 2, Title: "Заметки"}}
	for name, page := range map[string]storagePage{"ask": storageAsk, "choose": storageChoose, "send": storageSend, "progress": storageProgress, "missing": storageMissing} {
		source := &fakeStorage{}
		if page == storageProgress {
			source.pass = model.StoragePass{Active: true, Done: 57, Total: 200}
		}
		o := newStorageOffer(source)
		o.candidates, o.rows = candidates, make([]surface, len(candidates))
		o.page, o.sendAll = page, 200
		o.modal.Open()
		var toasts []string
		var ops *op.Ops
		for range 40 {
			ops = storageFrame(o, &toasts, size)
			time.Sleep(10 * time.Millisecond)
		}
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rectangle{Max: size})
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(fmt.Sprintf("%s/storage-%s.png", dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, im); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// Closing the dialog with no channel bound takes the tick back, so that ticking
// asks again; with a channel it stays.
func TestStorageOfferDeclineUnticks(t *testing.T) {
	var toasts []string
	source := &fakeStorage{found: model.StorageFound{Candidates: []model.StorageChannel{{ID: 1, Title: "a"}}}}
	o := newStorageOffer(source)
	declined := 0
	o.declined = func() { declined++ }
	closed := func() {
		o.modal.Close()
		for range 200 {
			storageFrame(o, &toasts, image.Pt(600, 800))
			if !o.modal.Shown() {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	o.Activated(func() {})
	storageSettled(t, o, &toasts)
	if declined != 0 {
		t.Fatal("declined before answering")
	}
	closed()
	if o.modal.Shown() || declined != 1 {
		t.Fatalf("shown %v, declined %d", o.modal.Shown(), declined)
	}
	source.found = model.StorageFound{Bound: true, Title: "x", SendAll: 2}
	o.Activated(func() {})
	storageSettled(t, o, &toasts)
	closed()
	if declined != 1 {
		t.Fatal("declined with a channel bound")
	}
}

// No channel with the sync on: the tick goes, and the user is told, for what is
// pushed out would otherwise be thought safe.
func TestStorageOfferWarnsWithoutChannel(t *testing.T) {
	var toasts []string
	source := &fakeStorage{missing: true}
	o := newStorageOffer(source)
	declined := 0
	o.declined = func() { declined++ }
	storageFrame(o, &toasts, image.Pt(600, 800))
	if declined != 1 || !o.modal.Shown() || o.page != storageMissing {
		t.Fatalf("declined %d, shown %v, page %d", declined, o.modal.Shown(), o.page)
	}
	storageFrame(o, &toasts, image.Pt(600, 800))
	if declined != 1 {
		t.Fatal("told twice")
	}
}

// Going on from the warning asks for a channel, straight to choosing it.
func TestStorageOfferChoosesFromTheWarning(t *testing.T) {
	var toasts []string
	source := &fakeStorage{missing: true, found: model.StorageFound{Candidates: []model.StorageChannel{{ID: 1, Title: "a"}}}}
	o := newStorageOffer(source)
	storageFrame(o, &toasts, image.Pt(600, 800))
	o.chooseAgain(func() {})
	storageSettled(t, o, &toasts)
	if !o.modal.Shown() || o.page != storageChoose || !o.unbound {
		t.Fatalf("shown %v, page %d, unbound %v", o.modal.Shown(), o.page, o.unbound)
	}
}

// While the user is being asked, what comes of that says it: no warning over it.
func TestStorageOfferNoWarningOverAsking(t *testing.T) {
	var toasts []string
	source := &fakeStorage{found: model.StorageFound{Candidates: []model.StorageChannel{{ID: 1, Title: "a"}}}}
	o := newStorageOffer(source)
	declined := 0
	o.declined = func() { declined++ }
	o.Activated(func() {})
	storageSettled(t, o, &toasts)
	source.mu.Lock()
	source.missing = true
	source.mu.Unlock()
	storageFrame(o, &toasts, image.Pt(600, 800))
	if o.page != storageAsk || declined != 0 {
		t.Fatalf("page %d, declined %d", o.page, declined)
	}
}
