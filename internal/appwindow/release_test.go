package appwindow

import "testing"

func TestReleaseOnlyWhenEveryWindowIsHidden(t *testing.T) {
	a, b := new(Window), new(Window)
	h := &Host{windows: map[*Window]struct{}{a: {}, b: {}}}
	h.setHidden(a, true)
	if h.release != nil {
		t.Fatal("release scheduled while a window is shown")
	}
	h.setHidden(b, true)
	if h.release == nil {
		t.Fatal("release not scheduled with every window hidden")
	}
	h.setHidden(a, false)
	if h.release != nil {
		t.Fatal("showing a window did not cancel the release")
	}
	// The shown window closes; the one left is hidden.
	h.mu.Lock()
	delete(h.windows, a)
	h.scheduleReleaseLocked()
	h.mu.Unlock()
	if h.release == nil {
		t.Fatal("closing the shown window did not schedule the release")
	}
	h.release.Stop()
}

func TestReleaseInBackground(t *testing.T) {
	h := new(Host)
	release := h.Hold()
	if h.release == nil {
		t.Fatal("release not scheduled for a process with only background work")
	}
	h.mu.Lock()
	h.held-- // what release does, short of ending the process
	h.scheduleReleaseLocked()
	h.mu.Unlock()
	if h.release != nil {
		t.Fatal("release scheduled for a process with nothing left")
	}
	_ = release
}

func TestReleaseMemoryLaterPutsOffTheRelease(t *testing.T) {
	h := new(Host)
	w := &Window{host: h}
	w.ReleaseMemoryLater()
	first := h.freed
	w.ReleaseMemoryLater()
	if h.freed == nil || h.freed == first {
		t.Fatal("a second call did not put the release off")
	}
	if first.Stop() {
		t.Fatal("the release put off still runs")
	}
	w.KeepMemory()
	if h.freed != nil {
		t.Fatal("showing the view again kept the release")
	}
}

// What serves only drawing is told when every window is hidden, before the
// memory is given back.
func TestAllHiddenTellsTheHooks(t *testing.T) {
	hiddenHooks.mu.Lock()
	saved := hiddenHooks.fs
	hiddenHooks.fs = nil
	hiddenHooks.mu.Unlock()
	defer func() {
		hiddenHooks.mu.Lock()
		hiddenHooks.fs = saved
		hiddenHooks.mu.Unlock()
	}()
	var told []int
	OnAllHidden(func() { told = append(told, 1) })
	OnAllHidden(func() { told = append(told, 2) })
	allHidden()
	if len(told) != 2 || told[0] != 1 || told[1] != 2 {
		t.Fatalf("told %v", told)
	}
}
