// SPDX-License-Identifier: Unlicense OR MIT

// Command render-all renders every screen the render tests of
// internal/messenger/ui draw, every variant, into one directory, to look
// them over after a change that touches much of the UI:
//
//	go run ./cmd/render-all [-only regexp] [directory]
//
// The directory defaults to komarugram-renders in the system's temporary
// directory, outside the tree, and its PNGs are removed first. The tests
// compile once; a test that fails, or that is skipped or not found, is
// reported and the others still run. Nothing is compared: the PNGs are for
// looking at.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// render is one run of a render test: its name in the report, the test,
// and the variables that make it draw; {out} in a value is the directory.
type render struct {
	name, test string
	env        []string
}

func renders() []render {
	var all []render
	for _, view := range []string{"channel-readonly", "restricted-text", "restricted-media", "emoji", "stickers", "gif", "featured-stickers", "featured-emoji", "emoji-search", "floating", "classic", "blur", "avatars", "voice", "files", "files-one", "files-documents", "files-many", "files-caption", "files-music", "drop-photos", "drop-media", "drop-files", "tasks", "toast", "toast-classic"} {
		all = append(all, render{"composer-" + view, "TestRenderComposer", []string{"COMPOSER_VIEW=" + view, "COMPOSER_PNG={out}/composer-" + view + ".png"}})
	}
	all = append(all, render{"composer-motion", "TestRenderComposerMotion", []string{"COMPOSER_MOTION_PNG={out}/composer-motion"}})
	for _, section := range []string{"main", "appearance", "chats", "wallpapers", "notify", "privacy", "premium", "integrations"} {
		all = append(all, render{"settings-" + section, "TestRenderSettingsAccounts", []string{"SETTINGS_SECTION=" + section, "SETTINGS_PNG={out}/settings-" + section + ".png"}})
	}
	return append(all,
		render{"text-blocks", "TestRenderTextBlocks", []string{"TEXT_BLOCKS_PNG_DIR={out}"}},
		render{"code-colors", "TestRenderCodeColors", []string{"CODE_COLORS_PNG_DIR={out}"}},
		render{"article", "TestRenderArticle", []string{"ARTICLE_PNG_DIR={out}"}},
		render{"window-surfaces", "TestWindowSurfacePixels", []string{"WINDOW_SURFACES_PNG_DIR={out}"}},
		render{"accounts", "TestRenderAccountScreens", []string{"ACCOUNTS_PNG_DIR={out}"}},
		render{"session-ended", "TestRenderSessionEnded", []string{"SESSION_PNG_DIR={out}"}},
		render{"sessions", "TestRenderSessions", []string{"SESSIONS_PNG={out}/sessions.png"}},
		render{"sticker-set", "TestRenderStickerSet", []string{"STICKER_SET_PNG_DIR={out}"}},
		render{"message-menu", "TestRenderMessageMenu", []string{"MENU_PNG={out}/menu.png"}},
		render{"reaction-menu", "TestRenderReactionMenu", []string{"MENU_PNG={out}/menu.png"}},
		render{"reacted", "TestRenderReacted", []string{"REACTED_PNG={out}/reacted.png"}},
		render{"viewer", "TestRenderPhotoViewer", []string{"VIEWER_PNG={out}/viewer.png"}},
		render{"viewer-zoom", "TestRenderPhotoViewer", []string{"VIEWER_ZOOM=2", "VIEWER_PNG={out}/viewer-zoom.png"}},
		render{"player", "TestRenderPlayerChoice", []string{"PLAYER_PNG={out}/player.png"}},
		render{"comments", "TestRenderCommentsHead", []string{"COMMENTS_PNG={out}/comments.png"}},
		render{"forum", "TestRenderForum", []string{"FORUM_PNG={out}/forum.png"}},
		render{"unwrapped", "TestRenderUnwrapped", []string{"UNWRAPPED_PNG={out}/unwrapped.png"}},
		render{"service", "TestRenderService", []string{"SERVICE_PNG={out}/service.png"}},
		render{"jump-buttons", "TestRenderJumpButtons", []string{"JUMP_PNG_DIR={out}"}},
		render{"pinned", "TestRenderPinned", []string{"PINNED_PNG={out}/pinned.png"}},
		render{"chat-search", "TestRenderChatSearch", []string{"CHAT_SEARCH_PNG={out}/chat-search.png"}},
		render{"snapshot", "TestRenderSnapshotDialog", []string{"SHOT_PNG={out}/snapshot.png"}},
		render{"saved-empty", "TestRenderEmptySavedMessages", []string{"SAVED_EMPTY_PNG={out}/saved-empty.png"}},
		render{"shared", "TestRenderSharedMediaAndThemes", []string{"SHARED_PNG={out}/shared"}},
		render{"toasts", "TestRenderToasts", []string{"TOAST_PNG_DIR={out}"}},
		render{"audio", "TestRenderAudio", []string{"AUDIO_PNG_DIR={out}"}},
	)
}

func main() {
	only := flag.String("only", "", "render only the runs whose name matches this regular expression, as composer or settings-")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: go run ./cmd/render-all [-only regexp] [directory]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if err := run(*only, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(only, out string) error {
	match, err := regexp.Compile(only)
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(os.TempDir(), "komarugram-renders")
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(out, "*.png"))
	for _, f := range old {
		os.Remove(f)
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "render-all")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "ui.test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	fmt.Println("compiling the tests of internal/messenger/ui")
	build := exec.Command("go", "test", "-c", "-o", bin, "./internal/messenger/ui")
	build.Dir, build.Stdout, build.Stderr = root, os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return err
	}

	failed := 0
	for _, r := range renders() {
		if !match.MatchString(r.name) {
			continue
		}
		fmt.Printf("%-28s", r.name)
		// From the package's directory, as go test runs it.
		cmd := exec.Command(bin, "-test.run", "^"+r.test+"$", "-test.count=1", "-test.v")
		cmd.Dir = filepath.Join(root, "internal", "messenger", "ui")
		cmd.Env = os.Environ()
		for _, v := range r.env {
			cmd.Env = append(cmd.Env, strings.ReplaceAll(v, "{out}", out))
		}
		log, err := cmd.CombinedOutput()
		// A test that was skipped, or that is not there any more, passes
		// without drawing anything.
		if err == nil && bytes.Contains(log, []byte("--- PASS: "+r.test+" ")) {
			fmt.Println("ok")
			continue
		}
		failed++
		fmt.Println("FAILED")
		lines := strings.Split(strings.TrimSpace(string(log)), "\n")
		for _, line := range lines[max(0, len(lines)-20):] {
			fmt.Println("    " + line)
		}
	}
	pngs, _ := filepath.Glob(filepath.Join(out, "*.png"))
	fmt.Printf("%d PNGs in %s\n", len(pngs), out)
	if failed > 0 {
		return fmt.Errorf("%d failed", failed)
	}
	return nil
}

// moduleRoot is the directory of the module's go.mod.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == os.DevNull {
		return "", fmt.Errorf("run it inside the komarugram module")
	}
	return filepath.Dir(mod), nil
}
