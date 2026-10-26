package crash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reports of a test binary are in a directory of its own, not among
// the user's: go test makes panics on purpose, and in fakes, which would
// push the user's own reports out.
func TestTestReportsStayOutOfTheUsers(t *testing.T) {
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	users := filepath.Join(cache, "komarugram-go")
	if rel, err := filepath.Rel(users, dir); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("a test reports into %s, in the user's %s", dir, users)
	}
}

func TestGuardReportsOncePerSite(t *testing.T) {
	var nilMap map[string]int
	for range 3 {
		p := Guard("test site", func() { nilMap["x"] = 1 })
		if p == nil || !strings.Contains(p.Error(), "test site") || len(p.Stack) == 0 {
			t.Fatalf("panic not recovered: %v", p)
		}
	}
	if Guard("test site", func() {}) != nil {
		t.Fatal("reported a panic that did not happen")
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "panic-*.txt"))
	if len(files) != 1 {
		t.Fatalf("want one report for a repeating panic, got %d", len(files))
	}
	body, _ := os.ReadFile(files[0])
	if !strings.Contains(string(body), "assignment to entry in nil map") {
		t.Fatalf("report without the panic value:\n%s", body)
	}
}

func TestTakeFatalKeepsReportOfDeadRun(t *testing.T) {
	dir := t.TempDir()
	if got := takeFatal(dir); got != "" {
		t.Fatalf("report %q of a run that left none", got)
	}
	path := filepath.Join(dir, fatalName)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := takeFatal(dir); got != "" {
		t.Fatalf("report %q of a run that ended well", got)
	}
	if err := os.WriteFile(path, []byte("fatal error: demonstration"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := takeFatal(dir)
	body, err := os.ReadFile(kept)
	if err != nil || string(body) != "fatal error: demonstration" {
		t.Fatalf("report not kept: %q, %v", body, err)
	}
	if matched, _ := filepath.Match("panic-*.txt", filepath.Base(kept)); !matched {
		t.Fatalf("report %q is not among those pruned", kept)
	}
	if got := takeFatal(dir); got != "" {
		t.Fatalf("report %q told twice", got)
	}
}

func TestReporterNotifiesWithSavedReport(t *testing.T) {
	// os.UserCacheDir reads LOCALAPPDATA on Windows, XDG_CACHE_HOME elsewhere.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
	var got *Panic
	stop := Subscribe(func(p *Panic) { got = p })
	defer stop()
	p := Guard("notification test", func() { panic("demonstration") })
	if got != p || p.Path == "" {
		t.Fatalf("notifier did not receive the saved panic: %+v", got)
	}
	data, err := os.ReadFile(p.Path)
	if err != nil || string(data) != p.Text() {
		t.Fatalf("saved report differs from copy text: %v", err)
	}
}

// A deferred call that panics as the stack unwinds replaces the value
// recovered; the report still names where the first panic was.
func TestFirstNamesThePanicUnderACascade(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
	p := Guard("cascade", func() {
		defer func() { panic("stack push and pop must not cross macro boundary") }()
		var m map[string]int
		m["first"] = 1
	})
	if p == nil || p.Value != "stack push and pop must not cross macro boundary" {
		t.Fatalf("recovered %v", p)
	}
	if got := p.First(); !strings.Contains(got, "TestFirstNamesThePanicUnderACascade.func1 crash_test.go:") {
		t.Fatalf("first panic at %q", got)
	}
	if !strings.Contains(p.Error(), "(first at ") || !strings.Contains(p.Text(), "\nfirst panic: ") {
		t.Fatalf("the report does not name the first panic:\n%s", p.Text())
	}
	// From the report of the photo viewer's strip, as Go printed it.
	p.Stack = []byte(`goroutine 53 [running, locked to thread]:
runtime/debug.Stack()
	/usr/local/go/src/runtime/debug/stack.go:26 +0x5e
panic({0x2e11868?, 0x1b9f0d0?})
	/usr/local/go/src/runtime/panic.go:859 +0x125
gioui.org/op.TransformStack.Pop(...)
	/home/kitty/projects/komarugram-go/third_party/gio/op/op.go:225
panic({0x2e3a010?, 0x304d720?})
	/usr/local/go/src/runtime/panic.go:859 +0x125
komarugram/internal/messenger/ui.(*photoViewer).layoutStrip.func1.1.1({{{0x0, 0x38}, {0xf423a, 0x38}}, ...)
	/home/kitty/projects/komarugram-go/internal/messenger/ui/photoviewer.go:1016 +0x35
gioui.org/layout.Inset.Layout({0x0?, 0x0?, 0x0?, 0x0?}, ...)
	/home/kitty/projects/komarugram-go/third_party/gio/layout/layout.go:169 +0x382
`)
	if got, want := p.First(), "ui.(*photoViewer).layoutStrip.func1.1.1 photoviewer.go:1016"; got != want {
		t.Fatalf("first panic at %q, want %q", got, want)
	}
	// One panic needs no word of a first.
	if p := Guard("single", func() { panic("alone") }); p.First() != "" || strings.Contains(p.Error(), "first at") {
		t.Fatalf("a single panic names a first: %s", p.Error())
	}
}
