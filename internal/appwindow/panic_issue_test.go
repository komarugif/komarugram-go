package appwindow

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"komarugram/internal/buildinfo"
	"komarugram/internal/crash"
)

// checkboxPanic is a panic the client had: a checkbox made without a
// change function, in the offer to send registrations (2026-10-20).
func checkboxPanic(t *testing.T) *crash.Panic {
	stack, err := os.ReadFile(filepath.Join("testdata", "checkbox-panic-stack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return &crash.Panic{Where: "window KomaruGram Go", Value: errors.New("runtime error: invalid memory address or nil pointer dereference"), Stack: stack}
}

func issueQuery(t *testing.T, link string) url.Values {
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "github.com" || u.Path != "/komarugif/komarugram-go/issues/new" {
		t.Errorf("the link is to %s%s", u.Host, u.Path)
	}
	return u.Query()
}

func loginLength(link string) int {
	return len("https://github.com/login?return_to=") + len(url.QueryEscape(link))
}

func TestPanicIssueHasATypicalReport(t *testing.T) {
	p := checkboxPanic(t)
	link, filled := panicIssue(p, buildinfo.Build{Commit: "9bff187bac4f9f4ac595ac16784b221381ff1b9c"})
	if !filled {
		t.Fatalf("a report of %d bytes did not fit: %d bytes through the login", len(p.Text()), loginLength(link))
	}
	t.Logf("report %d bytes, link %d, through the login %d of %d", len(p.Text()), len(link), loginLength(link), loginLimit)
	q := issueQuery(t, link)
	if q.Get("template") != issueTemplate || q.Get("commit") != "9bff187bac4f" {
		t.Errorf("template %q, commit %q", q.Get("template"), q.Get("commit"))
	}
	if !strings.Contains(q.Get("report"), "checkbox.(*Checkboxes[...]).Update") {
		t.Errorf("the report is not in the link:\n%s", q.Get("report"))
	}
	if want := "Panic: runtime error: invalid memory address or nil pointer dereference"; q.Get("title") != want {
		t.Errorf("title %q", q.Get("title"))
	}
	if !strings.HasPrefix(q.Get("system"), runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("system %q", q.Get("system"))
	}
}

func TestPanicIssueLeavesOutAReportTooLong(t *testing.T) {
	p := checkboxPanic(t)
	p.Stack = []byte(strings.Repeat(string(p.Stack), 4))
	link, filled := panicIssue(p, buildinfo.Build{})
	if filled {
		t.Fatalf("a report of %d bytes is in the link", len(p.Text()))
	}
	q := issueQuery(t, link)
	if q.Has("report") || q.Has("commit") || q.Get("template") != issueTemplate || q.Get("title") == "" {
		t.Errorf("the form without the report: %v", q)
	}
	if n := loginLength(link); n > loginLimit {
		t.Errorf("the link is %d bytes through the login", n)
	}
}

func TestPanicIssueTitleIsShort(t *testing.T) {
	title := issueTitle(&crash.Panic{Value: strings.Repeat("ж", 300) + "\nsecond line"})
	if r := []rune(title); len(r) != issueTitleRunes || r[len(r)-1] != '…' {
		t.Errorf("title of %d runes: %q", len(r), title)
	}
}

func TestPanicIssueHidesTheHomeDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "ada")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	report := privateTrimmed("at " + filepath.ToSlash(home) + "/src/a.go and " + home + string(filepath.Separator) + "b.go")
	if strings.Contains(report, "ada") || strings.Count(report, "~") != 2 {
		t.Errorf("report %q", report)
	}
}
