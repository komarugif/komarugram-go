# Prompt for a Linux container: build and test the Vulkan decoder

Paste the text below into a Claude Code session on Linux (a cloud
container will do for the build; the comparison with FFmpeg needs a GPU
with Vulkan Video).

---

Build and test, change nothing. Repository
https://github.com/komarugif/komarugram-go, branch `claude/vibecode`.
Do not edit files, commit, or push; only run commands and report.

1. Setup (Debian/Ubuntu; use the distribution's equivalents elsewhere):

   ```sh
   sudo apt-get update
   sudo apt-get install -y git gcc pkg-config ffmpeg vulkan-tools \
     libvulkan-dev mesa-vulkan-drivers libwayland-dev libx11-dev \
     libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev \
     libegl1-mesa-dev libffi-dev libxcursor-dev
   git clone --branch claude/vibecode https://github.com/komarugif/komarugram-go
   cd komarugram-go
   ```

   Go 1.27.3 or newer is needed (`go.mod`); install it from go.dev if
   `go version` is older.

2. Report the Vulkan headers and devices:

   ```sh
   grep -m1 'define VK_HEADER_VERSION ' /usr/include/vulkan/vulkan_core.h
   vulkaninfo --summary 2>&1 | head -40
   vulkaninfo 2>/dev/null | grep -i -E 'video_decode|video_queue' | sort -u
   ```

3. Build and vet:

   ```sh
   go vet ./pkg/hwvideo ./internal/messenger/chatmedia ./internal/messenger/ui ./cmd/messenger
   go build -o /tmp/messenger ./cmd/messenger
   go build -tags novulkan -o /dev/null ./cmd/messenger
   ```

4. Tests:

   ```sh
   go test -count=1 -v ./pkg/hwvideo 2>&1 | tail -60
   go test -count=1 -v -run 'SavedGIF' ./internal/messenger/chatmedia 2>&1 | tail -30
   go test -count=1 -run '^$' -fuzz FuzzSend -fuzztime 30s ./pkg/hwvideo 2>&1 | tail -5
   ```

5. Why the GPU is refused, if it is (the helper answers on stdout; its
   message is in the test's skip line of `TestDecodesAsFFmpeg` too):

   ```sh
   go test -count=1 -v -run TestDecodesAsFFmpeg ./pkg/hwvideo 2>&1 | grep -E 'SKIP|skip|PASS|FAIL|differs|hwvideo'
   ```

Report: the output of every step, in full where it failed, and for each
compiler error the file, line and message exactly. Say which steps were
skipped and why.
