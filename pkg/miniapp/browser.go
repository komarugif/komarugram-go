// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/program"
)

// BrowserEnv names the browser to drive, overriding the one that would
// otherwise be chosen: a program on PATH, an absolute path to one, or the
// application id of an installed flatpak.
const BrowserEnv = "KITCHEN_MINIAPP_BROWSER"

// nativeBrowsers are the programs looked for on PATH. Among browsers of the
// same engine and version, the one listed first wins.
var nativeBrowsers = []string{
	"chromium", "chromium-browser",
	"google-chrome", "google-chrome-stable",
	"brave-browser", "brave",
	"firefox", "firefox-esr", "librewolf", "waterfox",
}

// flatpakBrowsers are the Chromium-based flatpaks looked for. They rank after
// the native browsers when versions tie.
var flatpakBrowsers = []string{
	"io.github.ungoogled_software.ungoogled_chromium",
	"com.github.Eloston.UngoogledChromium",
	"org.chromium.Chromium",
	"com.google.Chrome",
	"com.brave.Browser",
	"org.mozilla.firefox",
	"io.gitlab.librewolf-community",
	"net.waterfox.waterfox",
}

// engine is what a browser is built on, which decides how it is driven.
type engine int

const (
	// engineChromium is driven over the Chrome DevTools Protocol (cdp.go).
	engineChromium engine = iota
	// engineFirefox is driven over WebDriver BiDi (bidi.go): Firefox and the
	// browsers built from it, LibreWolf and Waterfox.
	engineFirefox
)

// minFirefox is the oldest Firefox driven: the extended support release
// of 2025, whose BiDi has every command the bridge needs but
// browser.setClientWindowState (Firefox 151), which moves and resizes a
// window once it is open.
const minFirefox = 140

// browser is the browser this client drives: either a program on this machine
// or a flatpak to run one from.
type browser struct {
	ref     string // a path, or an application id
	flatpak bool
	found   bool
	engine  engine
	// version is the Chromium the browser is built on, or the Firefox, as far
	// as it tells; nil when it could not be read.
	version version
	// banner is what the browser printed for --version.
	banner string
}

// command returns what to run to open a browser on profile.
func (b browser) command(profile string) (string, []string) {
	if !b.flatpak {
		return b.ref, nil
	}
	// A flatpak sees only what it is given: /tmp it already has, but a profile
	// anywhere else — a cache directory, say — is invisible to it, and the
	// browser would quietly keep its own copy inside the sandbox, where nothing
	// here could read it. The directory is granted by name, for this run only.
	pre := []string{"run"}
	if profile != "" {
		pre = append(pre, "--filesystem="+profile)
	}
	return "flatpak", append(pre, b.ref)
}

// Available reports whether a browser to drive was found.
func Available() bool { return findBrowser().found }

// Browser names what a launch will run: the path of a program, or the
// application id of a flatpak. It is empty when nothing was found.
func Browser() string {
	found := findBrowser()
	if !found.found {
		return ""
	}
	return found.ref
}

// BrowserVersion is what the browser a launch will run printed for
// --version, such as "Chromium 152.0.7977.82". It is empty when nothing was
// found or the browser would not say.
func BrowserVersion() string { return findBrowser().banner }

var (
	browserOnce  sync.Once
	browserFound browser

	// custom is the browser the user picked, and customFound what it said
	// for --version, asked once per path.
	customMu     sync.Mutex
	custom       string
	customProbed string
	customFound  browser
)

// SetBrowser makes launches run the browser at path, which CheckBrowser
// accepted, instead of the one found; "" goes back to finding one.
// BrowserEnv still wins over it. A path that no longer answers as a
// browser that can be driven is passed over for the one found.
func SetBrowser(path string) {
	customMu.Lock()
	custom = path
	customMu.Unlock()
}

// customBrowser returns the browser the user picked, found only while it
// still answers as a browser that can be driven.
func customBrowser() browser {
	customMu.Lock()
	defer customMu.Unlock()
	if custom == "" {
		return browser{}
	}
	if customProbed != custom {
		customProbed = custom
		customFound = browser{}
		if banner, err := CheckBrowser(context.Background(), custom); err == nil {
			customFound = browserAt(custom)
			customFound.identify(banner)
		}
	}
	return customFound
}

// browserAt is the browser at path; a flatpak's launcher becomes the
// flatpak, so that the profile directory is granted to it.
func browserAt(path string) browser {
	if id, ok := program.FlatpakApp(path); ok {
		return browser{ref: id, flatpak: true, found: true}
	}
	return browser{ref: path, found: true}
}

// snapLauncher is a snap's command in a launcher script, as Ubuntu's
// /usr/bin/firefox runs /snap/bin/firefox.
var snapLauncher = regexp.MustCompile(`/snap/bin/([A-Za-z0-9-]+)`)

// snapName returns the name of the snap the program at path runs, or "":
// the commands in /snap/bin are links to the snap launcher, and a script
// may run one of them.
func snapName(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil && filepath.Base(real) == "snap" {
		name, _, _ := strings.Cut(filepath.Base(path), ".")
		return name
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	if !bytes.HasPrefix(head[:n], []byte("#!")) {
		return ""
	}
	if m := snapLauncher.FindSubmatch(head[:n]); m != nil {
		return string(m[1])
	}
	return ""
}

var (
	// ErrNotExecutable means the path is not a program that can be run.
	ErrNotExecutable = errors.New("not an executable file")
	// ErrNotBrowser means the program is neither a Chromium-based browser
	// nor Firefox.
	ErrNotBrowser = errors.New("not a Chromium-based browser or Firefox")
	// ErrOldFirefox means the program is a Firefox older than minFirefox.
	ErrOldFirefox = fmt.Errorf("Firefox %d or later is needed", minFirefox)
)

// chromiumVersion is the four-part version every Chromium-based browser
// prints: Chromium, Chrome, Brave, Edge. Firefox prints two or three parts.
var chromiumVersion = regexp.MustCompile(`(^|\s)\d+\.\d+\.\d+\.\d+(\s|$)`)

// firefoxBanner is what Firefox and the browsers built from it print for
// --version: "Mozilla Firefox 157.0.1", "LibreWolf 157.0-1", "BrowserWorks
// Waterfox 6.7.5", or, on Windows, what their version resource reads,
// "Firefox 157.0.1".
var firefoxBanner = regexp.MustCompile(`^(?:Mozilla |BrowserWorks )?(Firefox|LibreWolf|Waterfox) \d+\.\d+`)

// identify sets what b is from what it printed for --version: its engine,
// and the version of the engine. Firefox's own version is its engine's, and
// LibreWolf follows it; Waterfox numbers its versions on its own, 6.7.5 on
// Firefox 153, so a browser built from Firefox is asked the version of its
// engine in platform.ini, beside the program, where it can be found. Beside
// a launcher script it cannot, and the version stays unknown until the
// browser tells it itself (checkGecko).
func (b *browser) identify(banner string) {
	b.banner = banner
	m := firefoxBanner.FindStringSubmatch(banner)
	if m == nil {
		b.engine = engineChromium
		b.version = parseVersion(banner)
		return
	}
	b.engine = engineFirefox
	b.version = geckoVersion(*b)
	if b.version == nil && m[1] != "Waterfox" {
		b.version = parseVersion(banner)
	}
}

// usable says whether a browser identified as Firefox can be driven, as far
// as its version is known: one whose version is not is asked once it runs.
func (b browser) usable() error {
	if b.engine == engineFirefox && len(b.version) > 0 && b.version[0] < minFirefox {
		return ErrOldFirefox
	}
	return nil
}

// geckoVersion reads the version of the engine a browser built from Firefox
// runs on out of its platform.ini (Milestone=153.4.0): beside the program,
// or, in a flatpak, in the application's files. It is nil where there is
// none, as beside a launcher script.
func geckoVersion(b browser) version {
	var paths []string
	if b.flatpak {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := program.CommandContext(ctx, "flatpak", "info", "--show-location", b.ref).Output()
		cancel()
		if err != nil {
			return nil
		}
		files := filepath.Join(strings.TrimSpace(string(out)), "files")
		for _, pattern := range []string{"*/platform.ini", "*/*/platform.ini"} {
			found, _ := filepath.Glob(filepath.Join(files, pattern))
			paths = append(paths, found...)
		}
	} else if real, err := filepath.EvalSymlinks(b.ref); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(real), "platform.ini"))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if milestone, ok := strings.CutPrefix(strings.TrimSpace(line), "Milestone="); ok {
				if v := parseVersion(milestone); v != nil {
					return v
				}
			}
		}
	}
	return nil
}

// CheckBrowser makes sure the program at path is a browser Mini Apps can be
// driven in — a Chromium-based one, over the DevTools protocol, or Firefox,
// over WebDriver BiDi — and returns what it is, such as "Chromium
// 152.0.7977.82" or "Mozilla Firefox 157.0.1".
//
// It runs the program with --version, so it is for a path the user picked
// and trusts to be a program, not for any file. On Windows, where a browser
// opens a window for --version, it reads the version resource instead.
//
// The answer is kept for each state of the file: the settings ask about the
// file the user picked off the frame, and a launch, or the choice of the
// player, asks again about the same file on it.
func CheckBrowser(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || !program.IsExecutable(path) {
		return "", ErrNotExecutable
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", ErrNotExecutable
	}
	key := checkKey{path, info.Size(), info.ModTime()}
	checksMu.Lock()
	result, ok := checks[key]
	checksMu.Unlock()
	if ok {
		return result.banner, result.err
	}
	banner, err := checkBrowser(ctx, path)
	// A timeout or a cancelled context says nothing about the file.
	if ctx.Err() == nil {
		checksMu.Lock()
		checks[key] = checkResult{banner, err}
		checksMu.Unlock()
	}
	return banner, err
}

// checkKey identifies a program file as it was when it was checked.
type checkKey struct {
	path    string
	size    int64
	modTime time.Time
}

type checkResult struct {
	banner string
	err    error
}

var (
	checksMu sync.Mutex
	checks   = map[checkKey]checkResult{}
)

// checkBrowser is CheckBrowser, asked every time.
func checkBrowser(ctx context.Context, path string) (string, error) {
	var banner string
	if runtime.GOOS == "windows" {
		product, version, err := program.FileVersion(ctx, path)
		if err != nil {
			return "", err
		}
		banner = product + " " + version
	} else {
		b := browserAt(path)
		name, args := b.command("")
		var err error
		banner, err = program.Banner(ctx, name, append(args, "--version")...)
		if err != nil {
			return "", err
		}
	}
	b := browserAt(path)
	b.identify(banner)
	if b.engine == engineChromium && !chromiumVersion.MatchString(banner) {
		return "", ErrNotBrowser
	}
	return banner, b.usable()
}

// findBrowser picks the browser to launch, once: the answer is asked for on
// every frame that draws the page, and finding one means running every
// candidate.
//
// A Mini App is a web page from a stranger, so the browser's engine is the
// sandbox it runs in, and the newest engine has the fewest known holes. Which
// install is newest cannot be guessed from how it was installed — a flatpak
// may be ahead of the distribution's package or behind it — so every browser
// found is asked for its version and the newest one wins. A Chromium-based
// browser is taken before Firefox: it opens a window without the browser's
// controls of its own, and Firefox, whose versions cannot be compared with
// Chromium's, is driven only where no Chromium is, as on Haiku.
func findBrowser() browser {
	if os.Getenv(BrowserEnv) == "" {
		if b := customBrowser(); b.found {
			return b
		}
	}
	return autoBrowser()
}

// FoundBrowser is the browser found on this system, or named by BrowserEnv,
// passing over the one SetBrowser picked: the path of a program or the id of
// a flatpak, and what it printed for --version. ref is "" when there is none.
func FoundBrowser() (ref, banner string) {
	found := autoBrowser()
	if !found.found {
		return "", ""
	}
	return found.ref, found.banner
}

// autoBrowser is findBrowser without the browser the user picked.
func autoBrowser() browser {
	browserOnce.Do(func() {
		if choice := os.Getenv(BrowserEnv); choice != "" {
			if strings.Contains(choice, ".") && flatpakInstalled(choice) {
				browserFound = probe(browser{ref: choice, flatpak: true, found: true})
			} else if path, err := exec.LookPath(choice); err == nil {
				browserFound = probe(browser{ref: path, found: true})
			}
			// A Firefox too old to drive is no browser at all.
			if browserFound.usable() != nil {
				browserFound = browser{}
			}
			return
		}
		if program.Searching() {
			browserFound = newest(candidates())
		}
	})
	return browserFound
}

// candidates lists every browser installed, in order of preference, each
// asked for its version. The questions run at once: a flatpak takes about a
// second to answer.
func candidates() []browser {
	var found []browser
	seen := map[string]bool{}
	for _, name := range nativeBrowsers {
		path, err := program.LookPath(name)
		if err != nil {
			continue
		}
		// chromium and chromium-browser are often one program under two names.
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		if !seen[real] {
			seen[real] = true
			found = append(found, browser{ref: path, found: true})
		}
	}
	for _, path := range registeredBrowsers() {
		if !seen[path] {
			seen[path] = true
			found = append(found, browser{ref: path, found: true})
		}
	}
	flatpakAt := len(found)
	found = append(found, make([]browser, len(flatpakBrowsers))...)

	var wg sync.WaitGroup
	for i := range found[:flatpakAt] {
		wg.Go(func() { found[i] = probe(found[i]) })
	}
	for i, id := range flatpakBrowsers {
		wg.Go(func() {
			if flatpakInstalled(id) {
				found[flatpakAt+i] = probe(browser{ref: id, flatpak: true, found: true})
			}
		})
	}
	wg.Wait()
	return found
}

// newest returns the browser built on the latest Chromium, the earliest in
// the list among equals, and the latest Firefox when there is no
// Chromium-based browser. A browser that would not tell its version is chosen
// only when no other of its engine is found; a Firefox too old to drive,
// never.
func newest(found []browser) browser {
	var best browser
	for _, b := range found {
		if !b.found || b.usable() != nil {
			continue
		}
		switch {
		case !best.found, b.engine < best.engine:
			best = b
		case b.engine == best.engine && b.version.newer(best.version):
			best = b
		}
	}
	return best
}

// probe asks b for its version. On Windows, where a browser prints nothing
// for --version and opens a window instead, it reads the version resource.
func probe(b browser) browser {
	var banner string
	if runtime.GOOS == "windows" && !b.flatpak {
		product, version, err := program.FileVersion(context.Background(), b.ref)
		if err != nil {
			return b
		}
		banner = product + " " + version
	} else {
		prog, args := b.command("")
		var err error
		banner, err = program.Banner(context.Background(), prog, append(args, "--version")...)
		if err != nil {
			return b
		}
	}
	b.identify(banner)
	return b
}

func flatpakInstalled(id string) bool {
	if _, err := exec.LookPath("flatpak"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return program.CommandContext(ctx, "flatpak", "info", id).Run() == nil
}

// version is a Chromium or Firefox version, as many of its numbers as are
// known.
type version []int

// parseVersion reads the Chromium version from what a browser printed for
// --version: "Chromium 152.0.7977.82 for Linux Mint", "Google Chrome
// 140.0.7339.80". Brave prints its own version after the Chromium major —
// "Brave Browser 148.1.90.124" — so only its first number is Chromium's.
func parseVersion(banner string) version {
	for _, field := range strings.Fields(banner) {
		parts := strings.Split(field, ".")
		if len(parts) < 2 {
			continue
		}
		v := make(version, 0, len(parts))
		for i, part := range parts {
			// A part may end in letters or more: 140.3.0esr, 157.0-1.
			digits := part
			if end := strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
				digits = part[:end]
			}
			n, err := strconv.Atoi(digits)
			if err != nil {
				if i == 0 {
					v = nil
				}
				break
			}
			v = append(v, n)
		}
		if v == nil {
			continue
		}
		if strings.HasPrefix(banner, "Brave") {
			v = v[:1]
		}
		return v
	}
	return nil
}

// newer reports whether v is a later version than other, both of one engine. Only the numbers
// both know are compared, so a Brave build, which tells only its Chromium
// major, ties with every Chromium of that major. Any version is newer than an
// unknown one.
func (v version) newer(other version) bool {
	if other == nil {
		return v != nil
	}
	for i := 0; i < len(v) && i < len(other); i++ {
		if v[i] != other[i] {
			return v[i] > other[i]
		}
	}
	return false
}
