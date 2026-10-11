// SPDX-License-Identifier: Unlicense OR MIT

// Command appimage-build builds a Linux AppImage with the messenger,
// its desktop entry and icon. It downloads appimagetool when needed; see
// docs/BUILD_APPIMAGE.md.
//
//	go run ./cmd/appimage-build -o /tmp/KomaruGram.AppImage
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"komarugram/internal/appicon"
	"komarugram/pkg/program"
)

//go:embed APPIMAGE_RUNTIME_LICENSE
var runtimeLicense []byte

const desktopID = "komarugram-go"

const desktopEntry = `[Desktop Entry]
Type=Application
Name=KomaruGram
Comment=Telegram desktop client
Exec=komarugram
Icon=komarugram-go
Terminal=false
Categories=Network;InstantMessaging;
`

// Resolve the launcher directory in a subshell: the application keeps the
// caller's working directory, and quoted "$@" preserves every argument.
const appRun = `#!/bin/sh
appdir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
exec "$appdir/usr/bin/komarugram" "$@"
`

type options struct {
	out, tool, runtime, tags string
	lowMemory, keepWork      bool
}

func main() {
	log.SetFlags(0)
	o := options{}
	flag.StringVar(&o.out, "o", "", "output AppImage (default: KomaruGram-<architecture>.AppImage in the system temporary directory)")
	flag.StringVar(&o.tool, "appimagetool", os.Getenv("APPIMAGETOOL"), "appimagetool executable (default: APPIMAGETOOL, PATH, or automatic download)")
	flag.StringVar(&o.runtime, "runtime", "", "local AppImage runtime; otherwise appimagetool downloads its runtime")
	flag.StringVar(&o.tags, "tags", "", "Go build tags, for example nowayland or novulkan")
	flag.BoolVar(&o.lowMemory, "low-memory", false, "reduce compiler concurrency and disable inlining only in gotd/td/tg")
	flag.BoolVar(&o.keepWork, "keep-work", false, "keep the temporary AppDir for inspection")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected arguments; use -h for help")
	}
	if err := build(o); err != nil {
		log.Fatal(err)
	}
}

func build(o options) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("appimage-build runs on Linux; see docs/BUILD_APPIMAGE.md")
	}
	tool, err := resolveTool(o.tool)
	if err != nil {
		return err
	}
	if o.runtime != "" {
		o.runtime, err = filepath.Abs(o.runtime)
		if err != nil {
			return err
		}
		if _, err := os.Stat(o.runtime); err != nil {
			return fmt.Errorf("AppImage runtime: %w", err)
		}
	}
	repo, err := goOutput("list", "-m", "-f", "{{.Dir}}", "komarugram")
	if err != nil {
		return fmt.Errorf("run from the repository: %w", err)
	}
	config, err := goOutput("env", "GOOS", "GOARCH", "CGO_ENABLED")
	if err != nil {
		return err
	}
	values := strings.Fields(config)
	if len(values) != 3 || values[0] != "linux" || values[2] != "1" {
		return fmt.Errorf("the messenger requires GOOS=linux and CGO_ENABLED=1 (go env reports %q)", config)
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[values[1]]
	if arch == "" {
		return fmt.Errorf("unsupported AppImage architecture %q; supported: amd64, arm64", values[1])
	}
	if o.out == "" {
		o.out = filepath.Join(os.TempDir(), "KomaruGram-"+arch+".AppImage")
	}
	o.out, err = filepath.Abs(o.out)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "komarugram-appimage-")
	if err != nil {
		return err
	}
	if o.keepWork {
		log.Printf("work directory: %s", work)
	} else {
		defer os.RemoveAll(work)
	}
	appDir := filepath.Join(work, "KomaruGram.AppDir")
	if err := prepareAppDir(appDir, repo); err != nil {
		return err
	}
	env := os.Environ()
	env = defaultEnv(env, "GOGC", "50")
	args := []string{"build", "-p", "1", "-ldflags=-s -w -X gioui.org/app.ID=" + desktopID}
	if o.lowMemory {
		env = defaultEnv(env, "GOMAXPROCS", "1")
		env = defaultEnv(env, "GOMEMLIMIT", "1792MiB")
		args = append(args, "-gcflags=github.com/gotd/td/tg=-l")
	}
	if o.tags != "" {
		args = append(args, "-tags", o.tags)
	}
	args = append(args, "-o", filepath.Join(appDir, "usr", "bin", "komarugram"), "./cmd/messenger")
	log.Print("building the messenger: the first build needs about 4.5 GB; -low-memory reduces compilation memory")
	if err := run("go", args, repo, env); err != nil {
		return err
	}
	o.tool = tool
	return packageAppImage(o, appDir, work, arch, env)
}

func packageAppImage(o options, appDir, work, arch string, env []string) error {
	// Package beside the destination, then rename: a failed packager never
	// replaces a previous working AppImage, even across filesystems.
	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(o.out), ".komarugram-appimage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	packed := filepath.Join(staging, filepath.Base(o.out))
	packArgs := []string{"--no-appstream", "--mksquashfs-opt", "-processors", "--mksquashfs-opt", "1"}
	if o.runtime != "" {
		packArgs = append(packArgs, "--runtime-file", o.runtime)
	}
	packArgs = append(packArgs, appDir, packed)
	// An appimagetool distributed as an AppImage works without FUSE too.
	env = setEnv(env, "APPIMAGE_EXTRACT_AND_RUN", "1")
	env = setEnv(env, "ARCH", arch)
	log.Print("packaging the AppImage")
	if err := runPackager(o.tool, packArgs, work, env); err != nil {
		return err
	}
	if err := os.Chmod(packed, 0o755); err != nil {
		return err
	}
	if err := os.Rename(packed, o.out); err != nil {
		return err
	}
	info, err := os.Stat(o.out)
	if err != nil {
		return err
	}
	log.Printf("built %s (%.1f MiB)", o.out, float64(info.Size())/(1<<20))
	return nil
}

func prepareAppDir(dir, repo string) error {
	if err := os.MkdirAll(filepath.Join(dir, "usr", "bin"), 0o755); err != nil {
		return err
	}
	var icon bytes.Buffer
	if err := png.Encode(&icon, appicon.Image(256)); err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		"AppRun": []byte(appRun), desktopID + ".desktop": []byte(desktopEntry),
		desktopID + ".png": icon.Bytes(), "APPIMAGE_RUNTIME_LICENSE": runtimeLicense,
	} {
		mode := os.FileMode(0o644)
		if name == "AppRun" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, mode); err != nil {
			return err
		}
	}
	if err := os.Symlink(desktopID+".png", filepath.Join(dir, ".DirIcon")); err != nil {
		return err
	}
	for _, name := range []string{"LICENSE", "docs/THIRD_PARTY_LICENSES.md"} {
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(name)), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func defaultEnv(env []string, key, value string) []string {
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			return env
		}
	}
	return setEnv(env, key, value)
}

func setEnv(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func goOutput(args ...string) (string, error) {
	out, err := program.Command("go", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func run(name string, args []string, dir string, env []string) error {
	return runWithStderr(name, args, dir, env, os.Stderr)
}

func runWithStderr(name string, args []string, dir string, env []string, stderr io.Writer) error {
	cmd := program.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdout, cmd.Stderr = os.Stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}
