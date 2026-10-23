// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
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

// fakePoints is a source of registration points that records what the
// offer did with them.
type fakePoints struct {
	mu        sync.Mutex
	points    []model.RegistrationPoint
	unasked   int
	never     bool
	sent      [][]model.RegistrationPoint
	postponed int
}

func (f *fakePoints) RegistrationPoints(context.Context) ([]model.RegistrationPoint, int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.points, f.unasked, f.never, nil
}

func (f *fakePoints) SendRegistrationPoints(_ context.Context, points []model.RegistrationPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, points)
	return nil
}

func (f *fakePoints) PostponeRegistrationPoints(_ context.Context, _ []model.RegistrationPoint, never bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postponed++
	f.unasked = 0
	f.never = f.never || never
	return nil
}

func (f *fakePoints) sends() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func registrationSource(n, unasked int) *fakePoints {
	f := &fakePoints{unasked: unasked}
	for i := range n {
		f.points = append(f.points, model.RegistrationPoint{User: 7000000000 + int64(i), Date: time.Date(2024, 4, 1+i, 0, 0, 0, 0, time.UTC)})
	}
	return f
}

func registrationFrame(o *registrationOffer, toasts *[]string) {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 900)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	l := localization.For("ru")
	o.Update(gtx, func(s string) { *toasts = append(*toasts, s) }, l, func() {})
	o.Layout(gtx, l)
}

// registrationLooked runs frames until the offer has looked for the points.
func registrationLooked(t *testing.T, o *registrationOffer, toasts *[]string) {
	t.Helper()
	o.next = time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for {
		registrationFrame(o, toasts)
		if !o.next.IsZero() && o.next.After(time.Now()) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the points were not looked for")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRegistrationOfferSendsOnlyAgreed opens the dialog once there are
// enough points not asked about: nothing is sent before the warning is
// ticked, and the points are once it is.
func TestRegistrationOfferSendsOnlyAgreed(t *testing.T) {
	source := registrationSource(7, 7)
	o := newRegistrationOffer(source)
	var toasts []string
	registrationLooked(t, o, &toasts)
	if !o.modal.Shown() {
		t.Fatal("the dialog is not shown")
	}
	o.sendPoints(func() {})
	// Sending runs on its own goroutine: it is given the time to.
	time.Sleep(100 * time.Millisecond)
	if o.points == nil || source.sends() != 0 {
		t.Fatal("points sent without the warning ticked")
	}
	o.agree.SetValues([]string{"agree"})
	o.sendPoints(func() {})
	deadline := time.Now().Add(5 * time.Second)
	for len(toasts) == 0 && time.Now().Before(deadline) {
		registrationFrame(o, &toasts)
		time.Sleep(5 * time.Millisecond)
	}
	if source.sends() != 1 || len(source.sent[0]) != 7 || len(toasts) != 1 {
		t.Fatalf("sent %d times, toasts %q", source.sends(), toasts)
	}
	if source.postponed != 0 {
		t.Error("the closing dialog postponed what was sent")
	}
}

// TestRegistrationOfferAsksNotTooSoon asks about nothing with fewer points
// not asked about than RegistrationPointsMin, nor after "don't ask again".
func TestRegistrationOfferAsksNotTooSoon(t *testing.T) {
	for _, source := range []*fakePoints{
		registrationSource(20, model.RegistrationPointsMin-1),
		func() *fakePoints { f := registrationSource(20, 20); f.never = true; return f }(),
	} {
		o := newRegistrationOffer(source)
		var toasts []string
		registrationLooked(t, o, &toasts)
		if o.modal.Shown() {
			t.Errorf("asked with %d new points, never %v", source.unasked, source.never)
		}
	}
}

// TestRegistrationOfferNotNow postpones the points with "not now": the next
// look, with no new points, asks nothing.
func TestRegistrationOfferNotNow(t *testing.T) {
	source := registrationSource(7, 7)
	o := newRegistrationOffer(source)
	var toasts []string
	registrationLooked(t, o, &toasts)
	o.answer(false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		source.mu.Lock()
		done := source.postponed == 1
		source.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not postponed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for range 10 {
		registrationFrame(o, &toasts)
	}
	registrationLooked(t, o, &toasts)
	if o.modal.Shown() || source.sends() != 0 {
		t.Fatal("asked again with no new points")
	}
}

// TestRenderRegistrationOffer saves the dialog, with the warning ticked or
// not, for looking at.
func TestRenderRegistrationOffer(t *testing.T) {
	prefix := os.Getenv("REGISTRATION_OFFER_PNG")
	if prefix == "" {
		t.Skip("set REGISTRATION_OFFER_PNG to a path prefix")
	}
	size := image.Pt(800, 640)
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	o := newRegistrationOffer(registrationSource(12, 12))
	var toasts []string
	registrationLooked(t, o, &toasts)
	for _, agreed := range []bool{false, true} {
		if agreed {
			o.agree.SetValues([]string{"agree"})
		}
		var ops *op.Ops
		// The dialog animates in: the last of the frames is drawn.
		for range 40 {
			ops = new(op.Ops)
			gtx := layout.Context{Ops: ops, Now: time.Now().Add(time.Second), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
			wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
			fillRect(gtx, scheme(gtx).Surface.Color, size)
			l := localization.For("ru")
			o.Update(gtx, func(string) {}, l, func() {})
			o.Layout(gtx, l)
			time.Sleep(10 * time.Millisecond)
		}
		if err := win.Frame(ops); err != nil {
			t.Fatal(err)
		}
		im := image.NewRGBA(image.Rectangle{Max: size})
		if err := win.Screenshot(im); err != nil {
			t.Fatal(err)
		}
		name := prefix + "-unticked.png"
		if agreed {
			name = prefix + "-ticked.png"
		}
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, im)
		f.Close()
	}
}
