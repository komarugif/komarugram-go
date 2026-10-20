// SPDX-License-Identifier: Unlicense OR MIT

// Command windows-build installs gogio when needed and builds the Windows
// messenger with its icon and manifest. See docs/BUILD_WINDOWS.md.
//
//	go run ./cmd/windows-build -low-memory -o /tmp/KomaruGram.exe
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"komarugram/pkg/program"
)

type options struct {
	out, arch, tool, version string
	lowMemory                bool
}

func main() {
	log.SetFlags(0)
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	o := options{}
	flag.StringVar(&o.out, "o", "", "output executable (default: KomaruGram.exe in the system temporary directory)")
	flag.StringVar(&o.arch, "arch", arch, "Windows architecture: amd64 or arm64")
	flag.StringVar(&o.tool, "gogio", os.Getenv("GOGIO"), "gogio executable (default: GOGIO, PATH, or automatic go install)")
	flag.StringVar(&o.version, "gogio-version", "latest", "gogio version to install if absent; cached after the first install")
	flag.BoolVar(&o.lowMemory, "low-memory", false, "reduce compiler concurrency and disable inlining only in gotd/td/tg")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected arguments; use -h for help")
	}
	if err := build(o); err != nil {
		log.Fatal(err)
	}
}

func build(o options) error {
	if o.arch != "amd64" && o.arch != "arm64" {
		return fmt.Errorf("unsupported Windows architecture %q; use amd64 or arm64", o.arch)
	}
	if o.out == "" {
		o.out = filepath.Join(os.TempDir(), "KomaruGram.exe")
	}
	if filepath.Ext(o.out) != ".exe" {
		return fmt.Errorf("output name must end with .exe")
	}
	var err error
	o.out, err = filepath.Abs(o.out)
	if err != nil {
		return err
	}
	env := buildEnv(os.Environ(), o.lowMemory)
	repo, err := goOutput("", env, "list", "-m", "-f", "{{.Dir}}", "komarugram")
	if err != nil {
		return fmt.Errorf("run from the repository: %w", err)
	}
	tool, err := resolveGogio(o, env)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "komarugram-windows-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := prepareWork(repo, work, env); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(o.out), ".komarugram-windows-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	built := filepath.Join(staging, filepath.Base(o.out))
	args := []string{"-target=windows", "-arch=" + o.arch, "-ldflags=-s -w" + commitFlags(repo), "-appid=komarugram-go", "-name=KomaruGram", "-icon=" + filepath.Join(repo, "assets", "logo_round.png"), "-o", built, "./messenger"}
	env = setEnv(env, "GOWORK", "off")
	env = setEnv(env, "GOOS", "windows")
	env = setEnv(env, "GOARCH", o.arch)
	env = setEnv(env, "CGO_ENABLED", "0")
	log.Printf("building Windows/%s with its icon: the first build needs about 4.5 GB; -low-memory reduces compilation memory", o.arch)
	if err := run(tool, args, work, env); err != nil {
		return err
	}
	if err := os.Rename(built, o.out); err != nil {
		return fmt.Errorf("replace %s (close the application if it is running): %w", o.out, err)
	}
	log.Printf("built %s", o.out)
	return nil
}

// commitFlags are the linker flags that tell the program the commit of
// repo it is built from: the work directory's main module is not the
// repository, and Go stamps no commit then. Without git it tells none.
func commitFlags(repo string) string {
	commit, err := program.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		log.Printf("the build will not know its commit: git rev-parse: %v", err)
		return ""
	}
	flags := " -X komarugram/internal/buildinfo.commit=" + strings.TrimSpace(string(commit))
	if status, err := program.Command("git", "-C", repo, "status", "--porcelain").Output(); err == nil && len(strings.TrimSpace(string(status))) > 0 {
		flags += " -X komarugram/internal/buildinfo.modified=true"
	}
	return flags
}

func resolveGogio(o options, env []string) (string, error) {
	if o.tool != "" {
		path, err := program.LookPath(o.tool)
		if err != nil {
			return "", fmt.Errorf("gogio %q: %w", o.tool, err)
		}
		return filepath.Abs(path)
	}
	if path, err := program.LookPath("gogio"); err == nil {
		return filepath.Abs(path)
	}
	if o.version == "" || strings.ContainsAny(o.version, `/\\@`) {
		return "", fmt.Errorf("invalid gogio version %q", o.version)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "komarugram-go", "windows-build", "gogio-"+o.version+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	name := "gogio"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if program.IsExecutable(path) {
		log.Printf("using cached gogio: %s", path)
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Install the tool for the host; the app's target is set separately.
	env = setEnv(env, "GOBIN", dir)
	env = setEnv(env, "GOWORK", "off")
	env = setEnv(env, "GOOS", runtime.GOOS)
	env = setEnv(env, "GOARCH", runtime.GOARCH)
	env = setEnv(env, "CGO_ENABLED", "0")
	log.Printf("installing gioui.org/cmd/gogio@%s into %s", o.version, dir)
	if err := run("go", []string{"install", "gioui.org/cmd/gogio@" + o.version}, "", env); err != nil {
		return "", err
	}
	if !program.IsExecutable(path) {
		return "", fmt.Errorf("go install did not create %s", path)
	}
	return path, nil
}

// prepareWork isolates gogio's generated .syso from the real sources and
// stale resources. Keep the komarugram/ import prefix so internal imports
// remain allowed, and resolve local module replacements against the repo.
func prepareWork(repo, work string, env []string) error {
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(work, name), data, 0o644); err != nil {
			return err
		}
	}
	text, err := goOutput(repo, env, "mod", "edit", "-json")
	if err != nil {
		return err
	}
	var mod struct {
		Replace []struct {
			Old, New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal([]byte(text), &mod); err != nil {
		return err
	}
	args := []string{"mod", "edit", "-module=komarugram/windowsbuild", "-require=komarugram@v0.0.0", "-replace=komarugram=" + repo}
	for _, r := range mod.Replace {
		if r.New.Version != "" {
			continue
		}
		path := r.New.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(repo, path)
		}
		old := r.Old.Path
		if r.Old.Version != "" {
			old += "@" + r.Old.Version
		}
		args = append(args, "-replace="+old+"="+path)
	}
	env = setEnv(env, "GOWORK", "off")
	if err := run("go", args, work, env); err != nil {
		return err
	}
	src := filepath.Join(repo, "cmd", "messenger")
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(work, "messenger", rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if strings.HasSuffix(entry.Name(), ".syso") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported source file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

func buildEnv(env []string, lowMemory bool) []string {
	env = defaultEnv(env, "GOGC", "50")
	flags := os.Getenv("GOFLAGS") + " -p=1"
	if lowMemory {
		env = defaultEnv(env, "GOMAXPROCS", "1")
		env = defaultEnv(env, "GOMEMLIMIT", "1792MiB")
		flags += " -gcflags=github.com/gotd/td/tg=-l"
	}
	return setEnv(env, "GOFLAGS", strings.TrimSpace(flags))
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
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}

func goOutput(dir string, env []string, args ...string) (string, error) {
	cmd := program.Command("go", args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func run(name string, args []string, dir string, env []string) error {
	cmd := program.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}
