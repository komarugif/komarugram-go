// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"komarugram/internal/buildinfo"
	"komarugram/internal/crash"
	"komarugram/pkg/program"
)

const (
	// issueTemplate is the issue form of a panic, in .github/ISSUE_TEMPLATE;
	// GitHub reads it from the default branch.
	issueTemplate = "panic.yml"
	// loginLimit is the longest link the form may be sent through. GitHub
	// sends someone who is not signed in to its login page, with the form's
	// link escaped once more in return_to: measured in 2026-10, that link
	// past about 6900 bytes fails with 500, and the form's own past 8192
	// with 414. The margin is for what GitHub may change.
	loginLimit = 6000
	// issueTitleRunes is how much of the panic the issue's title takes.
	issueTitleRunes = 100
)

// panicIssue is the link to a new issue of p, and whether the report is in
// it. A report too long for a link is left out: the form is opened empty
// of it, for the report the dialog copies to be pasted there.
func panicIssue(p *crash.Panic, build buildinfo.Build) (link string, filled bool) {
	query := url.Values{
		"template": {issueTemplate},
		"title":    {issueTitle(p)},
		"system":   {runtime.GOOS + "/" + runtime.GOARCH + ", " + runtime.Version()},
	}
	if commit := build.Short(); commit != "" {
		query.Set("commit", commit)
	}
	empty := issueLink(query)
	// The form renders the field as code itself.
	query.Set("report", privateTrimmed(p.Text()))
	full := issueLink(query)
	if len("https://github.com/login?return_to=")+len(url.QueryEscape(full)) > loginLimit {
		return empty, false
	}
	return full, true
}

func issueLink(query url.Values) string {
	return "https://" + buildinfo.Repository + "/issues/new?" + query.Encode()
}

func issueTitle(p *crash.Panic) string {
	title, _, _ := strings.Cut(fmt.Sprint(p.Value), "\n")
	title = "Panic: " + strings.TrimSpace(title)
	if r := []rune(title); len(r) > issueTitleRunes {
		title = string(r[:issueTitleRunes-1]) + "…"
	}
	return title
}

// privateTrimmed is report with the home directory, which names the user
// who built the program, written as ~: the stack has the paths of the
// sources it was built from.
func privateTrimmed(report string) string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return report
	}
	for _, h := range []string{filepath.ToSlash(home), home} {
		report = strings.ReplaceAll(report, h, "~")
	}
	return report
}

// openBrowser opens link in the system's browser.
func openBrowser(link string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin", "haiku":
		command = program.Command("open", link)
	case "windows":
		command = program.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		command = program.Command("xdg-open", link)
	}
	return command.Run()
}
