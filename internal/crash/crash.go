// Package crash turns panics in isolated units of work — one UI frame, one
// media load — into logged reports instead of a process exit.
//
// Only code whose state is discarded or rebuilt after the failure should be
// guarded: a recovered panic leaves whatever it was mutating half done.
package crash

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Panic is a recovered panic with the stack of the goroutine that raised it.
type Panic struct {
	Where string
	Value any
	Stack []byte
	At    time.Time
	Path  string
}

func (p *Panic) Error() string {
	text := fmt.Sprintf("panic in %s: %v", p.Where, p.Value)
	if at := p.First(); at != "" {
		text += " (first at " + at + ")"
	}
	return text
}

// First is where the first of the panics in the stack was raised, when
// there are several, and "" otherwise. A deferred call that panics while
// the stack unwinds, as Gio's Pop finding its stack unbalanced, replaces the
// value recovered: Value is then that panic's, and only the stack tells
// where the one that started it was.
func (p *Panic) First() string {
	lines := strings.Split(string(p.Stack), "\n")
	last, panics := -1, 0
	for i, l := range lines {
		if strings.HasPrefix(l, "panic(") {
			last, panics = i, panics+1
		}
	}
	if panics < 2 {
		return ""
	}
	// Below the outermost panic( and its file line: the frame that
	// panicked, past the runtime's own, as runtime.sigpanic.
	for i := last + 2; i+1 < len(lines); i += 2 {
		name := frameName(lines[i])
		if name == "" || strings.HasPrefix(name, "runtime.") {
			continue
		}
		file := strings.TrimSpace(lines[i+1])
		if cut := strings.LastIndex(file, " +0x"); cut >= 0 {
			file = file[:cut]
		}
		if slash := strings.LastIndexAny(file, `/\`); slash >= 0 {
			file = file[slash+1:]
		}
		return name + " " + file
	}
	return ""
}

// frameName is the function of a frame's line of a Go stack, without its
// package path and its arguments.
func frameName(line string) string {
	if line == "" || strings.HasPrefix(line, "\t") {
		return ""
	}
	// The arguments open with the first parenthesis not of a method's
	// receiver, as (*photoViewer).
	for i := 0; i < len(line); i++ {
		if line[i] == '(' && (i+1 == len(line) || line[i+1] != '*') {
			line = line[:i]
			break
		}
	}
	if slash := strings.LastIndex(line, "/"); slash >= 0 {
		line = line[slash+1:]
	}
	return line
}

// Text is the complete report offered by the copy button, even if writing a
// file failed.
func (p *Panic) Text() string {
	at := p.At
	if at.IsZero() {
		at = time.Now()
	}
	first := ""
	if place := p.First(); place != "" {
		first = "\nfirst panic: " + place
	}
	return fmt.Sprintf("time: %s\nwhere: %s\npanic: %v%s\n\n%s", at.Format(time.RFC3339Nano), p.Where, p.Value, first, p.Stack)
}

// Guard runs fn and returns the recovered panic, if any. The panic is
// reported before Guard returns.
func Guard(where string, fn func()) (p *Panic) {
	defer func() {
		if v := recover(); v != nil {
			p = &Panic{Where: where, Value: v, Stack: debug.Stack()}
			Report(p)
		}
	}()
	fn()
	return nil
}

// Recover is deferred at the top of a goroutine. It reports a panic and,
// when handle is not nil, passes it on so the owner can mark its work failed.
func Recover(where string, handle func(*Panic)) {
	v := recover()
	if v == nil {
		return
	}
	p := &Panic{Where: where, Value: v, Stack: debug.Stack()}
	Report(p)
	if handle != nil {
		handle(p)
	}
}

const (
	maxReports = 32 // files kept in the report directory
	reportGap  = 10 * time.Second
)

var (
	mu        sync.Mutex
	lastWrite = map[string]time.Time{}
	lastPath  = map[string]string{}
	subs      = map[uint64]func(*Panic){}
	nextID    uint64
)

// Subscribe receives reported panics after the report path has been set.
// A notifier must return promptly; it may open UI asynchronously.
func Subscribe(notify func(*Panic)) func() {
	mu.Lock()
	id := nextID
	nextID++
	subs[id] = notify
	mu.Unlock()
	return func() { mu.Lock(); delete(subs, id); mu.Unlock() }
}

func notify(p *Panic) {
	mu.Lock()
	callbacks := make([]func(*Panic), 0, len(subs))
	for _, callback := range subs {
		callbacks = append(callbacks, callback)
	}
	mu.Unlock()
	for _, callback := range callbacks {
		func() {
			defer func() {
				if v := recover(); v != nil {
					log.Printf("crash notifier failed: %v", v)
				}
			}()
			callback(p)
		}()
	}
}

// Report logs a panic and writes it to <user cache>/komarugram-go/crashes. A panic
// that repeats every frame is written at most once per reportGap per site so
// it cannot fill the disk; the log still gets a one-line note each time.
func Report(p *Panic) {
	p.At = time.Now()
	defer notify(p)
	mu.Lock()
	due := time.Since(lastWrite[p.Where]) >= reportGap
	p.Path = lastPath[p.Where]
	if due {
		lastWrite[p.Where] = time.Now()
	}
	mu.Unlock()
	if !due {
		log.Printf("recovered %v (repeat, report suppressed)", p)
		return
	}
	path, err := write(p)
	if err != nil {
		log.Printf("recovered %v\n%s(crash report not saved: %v)", p, p.Stack, err)
		return
	}
	p.Path = path
	mu.Lock()
	lastPath[p.Where] = path
	mu.Unlock()
	log.Printf("recovered %v\n%sreport: %s", p, p.Stack, path)
}

func Dir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "komarugram-go", "crashes"), nil
}

// fatalName is the file the runtime writes to when the process dies.
const fatalName = "fatal.txt"

// CaptureFatal makes the Go runtime write what ends the process, a panic on
// a goroutine nothing guards or a fatal error, to a file among the reports,
// besides stderr: a program without a console has none. It returns the path
// of the report the previous run left, empty if it left none.
func CaptureFatal() (previous string, err error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	previous = takeFatal(dir)
	// Appending keeps what another running process may have written.
	f, err := os.OpenFile(filepath.Join(dir, fatalName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return previous, err
	}
	defer f.Close()
	return previous, debug.SetCrashOutput(f, debug.CrashOptions{})
}

// takeFatal moves the report a run that died left in dir among the panic
// reports, and returns its path, empty if there is none.
func takeFatal(dir string) string {
	path := filepath.Join(dir, fatalName)
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return ""
	}
	kept := filepath.Join(dir, fmt.Sprintf("panic-%s-fatal.txt", info.ModTime().Format("20060102-150405")))
	// The file of a process still running cannot be moved on Windows, and
	// is its own to report.
	if err := os.Rename(path, kept); err != nil {
		return ""
	}
	prune(dir)
	return kept
}

func write(p *Panic) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	now := p.At
	name := fmt.Sprintf("panic-%s-%09d.txt", now.Format("20060102-150405"), now.Nanosecond())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(p.Text()), 0o600); err != nil {
		return "", err
	}
	prune(dir)
	return path, nil
}

// prune keeps the newest maxReports files; names sort by time.
func prune(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "panic-*.txt"))
	for len(matches) > maxReports {
		os.Remove(matches[0])
		matches = matches[1:]
	}
}
