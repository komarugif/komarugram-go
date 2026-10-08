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
//	go run ./cmd/haiku-build -o /boot/home/apps/KomaruGram
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
	flag.Parse()
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if runtime.GOOS != "haiku" {
		log.Fatal("haiku-build runs on Haiku; to build from another system, see docs/PLATFORMS.md")
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
	if err := build(repo, *work, *out); err != nil {
		log.Fatal(err)
	}
}

func build(repo, work, out string) error {
	goroot, err := goOutput("env", "GOROOT")
	if err != nil {
		return err
	}
	haikuSys := filepath.Join(goroot, "src", "cmd", "vendor", "golang.org", "x", "sys", "unix")
	if names, _ := filepath.Glob(filepath.Join(haikuSys, "*_haiku*.go")); len(names) == 0 {
		return fmt.Errorf("this Go (%s) has no Haiku files of golang.org/x/sys: build with the Go of github.com/komarugif/go-haiku", goroot)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	workFile := fmt.Sprintf("go 1.27.1\n\nuse %s\n\n", repo)
	dirs := map[string]string{}
	for _, m := range modules {
		dir, err := prepare(repo, work, m, haikuSys)
		if err != nil {
			return fmt.Errorf("%s: %w", m.path, err)
		}
		dirs[m.path] = dir
		workFile += fmt.Sprintf("replace %s => %s\n", m.path, dir)
	}
	goWork := filepath.Join(work, "go.work")
	if err := os.WriteFile(goWork, []byte(workFile), 0o644); err != nil {
		return err
	}

	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	program := filepath.Join(out, "messenger")
	log.Printf("building the messenger: several minutes, and up to 5 GB of memory")
	// gotd's tg package takes most of the memory: one package at a time,
	// and a collector that runs sooner, keep it within reach.
	cmd := exec.Command("go", "build", "-p", "1", "-ldflags=-linkmode=internal", "-tags", "sqlite3_flock", "-o", program, "./cmd/messenger")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOWORK="+goWork, "CGO_ENABLED=1", "GOGC=50")
	if err := run(cmd); err != nil {
		return err
	}
	for _, lib := range []struct{ name, build string }{
		{"libgiohaiku.so", filepath.Join(repo, "third_party", "gio", "app", "internal", "haiku", "build.go")},
		{"libotohaiku.so", filepath.Join(dirs["github.com/ebitengine/oto/v3"], "internal", "haiku", "build.go")},
		{"libtrayhaiku.so", filepath.Join(repo, "internal", "tray", "haiku", "build.go")},
		{"libvoicehaiku.so", filepath.Join(repo, "pkg", "voice", "haiku", "build.go")},
	} {
		log.Printf("building %s", lib.name)
		cmd := exec.Command("go", "run", lib.build, "-o", filepath.Join(out, lib.name))
		cmd.Dir = filepath.Dir(lib.build)
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if err := run(cmd); err != nil {
			return err
		}
	}
	// The signature, flags, version and icon, from the resource definition.
	rsrc := filepath.Join(work, "messenger.rsrc")
	if err := run(exec.Command("rc", "-o", rsrc, filepath.Join(repo, "cmd", "messenger", "messenger.rdef"))); err != nil {
		return err
	}
	if err := run(exec.Command("xres", "-o", program, rsrc)); err != nil {
		return err
	}
	if err := run(exec.Command("mimeset", "-f", program)); err != nil {
		return err
	}
	log.Printf("done: %s", program)
	return nil
}

// prepare copies module m out of the module cache into work, adds what it
// needs for Haiku, and returns its directory. A copy made before is kept.
func prepare(repo, work string, m patched, haikuSys string) (string, error) {
	dir := filepath.Join(work, m.dir+"-"+m.version)
	if _, err := os.Stat(filepath.Join(dir, ".patched")); err == nil {
		return dir, nil
	}
	cmd := exec.Command("go", "mod", "download", "-json", m.path+"@"+m.version)
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
	apply := exec.Command("patch", "-p1", "--forward", "--batch", "-i", patch)
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
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(strings.TrimSpace(err.Error() + ": " + stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
