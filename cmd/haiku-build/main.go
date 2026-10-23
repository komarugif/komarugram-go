// SPDX-License-Identifier: Unlicense OR MIT

// Command haiku-build builds the messenger on Haiku, from the repository,
// into a directory of its own: the program with its resources, and the four
// libraries of C++ it loads from beside itself (docs/BUILD_HAIKU.md).
//
// It needs the Go of komarugif/go-haiku: upstream Go knows no Haiku, and
// the toolchain's own copy of golang.org/x/sys is where the module's
// Haiku files come from. Five modules need patches for Haiku
// (docs/haiku/*.patch): they are downloaded, copied out of the module
// cache, patched, and named in a go.work beside them.
//
//	go run -ldflags=-linkmode=internal ./cmd/haiku-build -o /boot/home/apps/KomaruGram
//
// With -test it runs the tests instead, with the same modules and flags,
// and with the four libraries built into the work directory, where the
// tests are told to load them from. What follows the flags goes to go
// test as it is: packages and its flags, after a "--"; with nothing, the
// tests of ./internal/... ./cmd/... ./pkg/...
//
//	go run -ldflags=-linkmode=internal ./cmd/haiku-build -test
//	go run -ldflags=-linkmode=internal ./cmd/haiku-build -test -- -run TestPlayback ./pkg/audio
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"komarugram/pkg/program"
)

// patched is a module that needs a patch for Haiku, and the patch, named
// after the version it was made for.
type patched struct {
	path, dir, version string
}

var modules = []patched{
	{"golang.org/x/sys", "x-sys", "v0.48.0"},
	{"github.com/tetratelabs/wazero", "wazero", "v1.12.0"},
	{"gioui.org/shader", "gioui-shader", "v1.0.9"},
	{"github.com/go-text/typesetting", "go-text-typesetting", "v0.3.4"},
	{"github.com/ebitengine/oto/v3", "oto", "v3.5.1"},
}

func main() {
	log.SetFlags(0)
	out := flag.String("o", "", "the directory to build into (required)")
	work := flag.String("work", "", "where the patched modules are kept (default: a directory in the user's cache)")
	lowMemory := flag.Bool("low-memory", false, "reduce compiler concurrency and disable inlining only in gotd/td/tg")
	test := flag.Bool("test", false, "run the tests instead of building; what follows the flags goes to go test")
	flag.Parse()
	if *out == "" && !*test {
		flag.Usage()
		os.Exit(2)
	}
	if runtime.GOOS != "haiku" {
		log.Fatal("haiku-build runs on Haiku; to build from another system, see docs/PLATFORMS.md")
	}
	if *lowMemory {
		if err := enableLowMemory(); err != nil {
			log.Fatal(err)
		}
	}
	repo, err := goOutput("list", "-m", "-f", "{{.Dir}}")
	if err != nil {
		log.Fatalf("run it from the repository: %v", err)
	}
	if *work == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			log.Fatal(err)
		}
		*work = filepath.Join(cache, "komarugram-go-haiku-build")
	}
	goWork, dirs, err := prepareWork(repo, *work)
	if err != nil {
		log.Fatal(err)
	}
	if *test {
		err = runTests(repo, *work, goWork, dirs, flag.Args())
	} else {
		err = build(repo, *work, goWork, dirs, *out)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// prepareWork prepares the patched modules in work and writes the go.work
// that names them, returning its path and the modules' directories.
func prepareWork(repo, work string) (string, map[string]string, error) {
	goroot, err := goOutput("env", "GOROOT")
	if err != nil {
		return "", nil, err
	}
	haikuSys := filepath.Join(goroot, "src", "cmd", "vendor", "golang.org", "x", "sys", "unix")
	if names, _ := filepath.Glob(filepath.Join(haikuSys, "*_haiku*.go")); len(names) == 0 {
		return "", nil, fmt.Errorf("this Go (%s) has no Haiku files of golang.org/x/sys: build with the Go of github.com/komarugif/go-haiku", goroot)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", nil, err
	}
	goMod, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		return "", nil, err
	}
	goLine := modGoLine(string(goMod))
	if goLine == "" {
		return "", nil, errors.New("go.mod has no go line")
	}
	workFile := fmt.Sprintf("%s\n\nuse %s\n\n", goLine, repo)
	dirs := map[string]string{}
	for _, m := range modules {
		dir, err := prepare(repo, work, m, haikuSys)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", m.path, err)
		}
		dirs[m.path] = dir
		workFile += fmt.Sprintf("replace %s => %s\n", m.path, dir)
	}
	goWork := filepath.Join(work, "go.work")
	if err := os.WriteFile(goWork, []byte(workFile), 0o644); err != nil {
		return "", nil, err
	}
	return goWork, dirs, nil
}

// modGoLine is the go line of goMod, which go.work repeats: Go refuses a
// go.work that asks for an older Go than a module it uses.
func modGoLine(goMod string) string {
	for _, line := range strings.Split(goMod, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "go ") {
			return line
		}
	}
	return ""
}

// goEnv is the environment of the go commands that build the messenger
// and its tests.
func goEnv(goWork string) []string {
	env := append(os.Environ(), "GOWORK="+goWork, "CGO_ENABLED=1")
	if _, set := os.LookupEnv("GOGC"); !set {
		env = append(env, "GOGC=50")
	}
	return env
}

// Apply limits to this build process and all Go children, including the
// library builders. Explicit user limits take precedence over the defaults.
func enableLowMemory() error {
	for key, value := range map[string]string{
		"GOMAXPROCS": "1", "GOGC": "50", "GOMEMLIMIT": "1792MiB",
	} {
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	flags := strings.TrimSpace(os.Getenv("GOFLAGS") + " -p=1 -gcflags=github.com/gotd/td/tg=-l")
	return os.Setenv("GOFLAGS", flags)
}

func build(repo, work, goWork string, dirs map[string]string, out string) error {
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	binary := filepath.Join(out, "messenger")
	log.Printf("building the messenger: several minutes, and about 4.5 GB of memory")
	// gotd's tg package takes most of the memory: one package at a time,
	// and a collector that runs sooner, keep it within reach.
	cmd := program.Command("go", "build", "-p", "1", "-ldflags=-linkmode=internal", "-tags", "sqlite3_flock", "-o", binary, "./cmd/messenger")
	cmd.Dir = repo
	cmd.Env = goEnv(goWork)
	if err := run(cmd); err != nil {
		return err
	}
	if _, err := buildLibs(repo, dirs, out); err != nil {
		return err
	}
	// The signature, flags, version and icon, from the resource definition.
	rsrc := filepath.Join(work, "messenger.rsrc")
	if err := run(program.Command("rc", "-o", rsrc, filepath.Join(repo, "cmd", "messenger", "messenger.rdef"))); err != nil {
		return err
	}
	if err := run(program.Command("xres", "-o", binary, rsrc)); err != nil {
		return err
	}
	if err := run(program.Command("mimeset", "-f", binary)); err != nil {
		return err
	}
	log.Printf("done: %s", binary)
	return nil
}

// lib is one of the C++ libraries the messenger loads from beside itself,
// the program that builds it, and the variable that names it in its place.
type lib struct{ name, build, env string }

func libs(repo string, dirs map[string]string) []lib {
	return []lib{
		{"libgiohaiku.so", filepath.Join(repo, "third_party", "gio", "app", "internal", "haiku", "build.go"), "GIO_HAIKU_LIB"},
		{"libotohaiku.so", filepath.Join(dirs["github.com/ebitengine/oto/v3"], "internal", "haiku", "build.go"), "OTO_HAIKU_LIB"},
		{"libtrayhaiku.so", filepath.Join(repo, "internal", "tray", "haiku", "build.go"), "KOMARUGRAM_TRAY_LIB"},
		{"libvoicehaiku.so", filepath.Join(repo, "pkg", "voice", "haiku", "build.go"), "KOMARUGRAM_VOICE_LIB"},
	}
}

// buildLibs builds the libraries into out, and returns the variables that
// name them there.
func buildLibs(repo string, dirs map[string]string, out string) ([]string, error) {
	var env []string
	for _, l := range libs(repo, dirs) {
		log.Printf("building %s", l.name)
		path := filepath.Join(out, l.name)
		cmd := program.Command("go", "run", l.build, "-o", path)
		cmd.Dir = filepath.Dir(l.build)
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if err := run(cmd); err != nil {
			return nil, err
		}
		env = append(env, l.env+"="+path)
	}
	return env, nil
}

// runTests runs go test with args, or with the tests of the repository,
// as the messenger is built. A test binary runs from a temporary
// directory, so the libraries are named by their variables.
func runTests(repo, work, goWork string, dirs map[string]string, args []string) error {
	libDir := filepath.Join(work, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return err
	}
	libEnv, err := buildLibs(repo, dirs, libDir)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		args = []string{"./internal/...", "./cmd/...", "./pkg/..."}
	}
	log.Printf("running the tests: the first build takes several minutes, and about 4.5 GB of memory")
	cmd := program.Command("go", append([]string{"test", "-p", "1", "-ldflags=-linkmode=internal", "-tags", "sqlite3_flock"}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(goEnv(goWork), libEnv...)
	return run(cmd)
}

// prepare copies module m out of the module cache into work, adds what it
// needs for Haiku, and returns its directory. A copy made before is kept.
func prepare(repo, work string, m patched, haikuSys string) (string, error) {
	dir := filepath.Join(work, m.dir+"-"+m.version)
	if _, err := os.Stat(filepath.Join(dir, ".patched")); err == nil {
		return dir, nil
	}
	cmd := program.Command("go", "mod", "download", "-json", m.path+"@"+m.version)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOWORK=off")
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	var info struct{ Dir string }
	if err := json.Unmarshal(data, &info); err != nil || info.Dir == "" {
		return "", fmt.Errorf("download: no directory in %q", data)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := copyTree(info.Dir, dir); err != nil {
		return "", err
	}
	if m.path == "golang.org/x/sys" {
		// The Haiku files of the module are the toolchain's own copy's.
		names, _ := filepath.Glob(filepath.Join(haikuSys, "*_haiku*"))
		for _, name := range names {
			if err := copyFile(name, filepath.Join(dir, "unix", filepath.Base(name))); err != nil {
				return "", err
			}
		}
	}
	patch := filepath.Join(repo, "docs", "haiku", m.dir+"-"+m.version+".patch")
	apply := program.Command("patch", "-p1", "--forward", "--batch", "-i", patch)
	apply.Dir = dir
	if err := run(apply); err != nil {
		return "", fmt.Errorf("apply %s: %w", filepath.Base(patch), err)
	}
	return dir, os.WriteFile(filepath.Join(dir, ".patched"), nil, 0o644)
}

// copyTree copies the files of src into dst, writable: the module cache
// keeps them read-only.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func run(cmd *exec.Cmd) error {
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return nil
}

func goOutput(args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := program.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(strings.TrimSpace(err.Error() + ": " + stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
