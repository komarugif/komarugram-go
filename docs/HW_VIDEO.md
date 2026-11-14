# Hardware-decoded video for the saved GIFs (design, 2026-11-13)

The requirements were set by the maintainer on 2026-11-13. Windows is
built and checked (2026-11-14, see "What is built"); Linux, on Vulkan
Video, is written and not yet built nor run. The sections after "What
is built" are the proposal it started from.

## What is built (2026-11-14)

`pkg/hwvideo`, off by default: the settings' section of hardware
acceleration has the choice, "Saved GIFs" (`preferences.Graphics.
HardwareGIFs`), shown where the system has a decoder to try.

- **The part every system shares**, in Go: the parser of the H.264
  headers (`headers.go`: parameter sets, scaling lists, slice headers to
  the end of the marking of references) and what a hardware decoder leaves
  to the application (`decoder.go`: picture order counts, references by
  the sliding window and the marking operations, the order pictures show
  in, a surface for each). Only progressive 4:2:0 of 8 bits, Baseline,
  Main and High, frames only, at most 2048 a side, is taken; fields, slice
  groups, switching slices, data partitioning, gaps in the frame numbers
  and a size that changes are refused with `ErrUnsupported`, and that GIF
  plays in software. Fuzzed (`FuzzSend`, `FuzzParameterSets`: 8 and 3
  million runs, no finding).
- **The helper**: the client's own binary with `-video-decoder`
  (`Serve`), started by `Client` when the first GIF of the list is opened,
  stopped 10 s after the last is closed, and at once when every window
  has been hidden or closed for 5 s (`appwindow.OnAllHidden`, as the
  memory is given back): a hidden window draws nothing. A helper stopped so
  fails nothing (`ErrStopped`): the next GIF a window shown plays starts a
  new one. Streams are counted by helper, so that those of one stopped,
  ending late, do not count against the next. The window demuxes the MP4 and
  sends samples over the helper's standard input; the helper reads no
  file. A helper that ends on its own fails the client: every GIF goes
  back to software for the rest of the session, and the settings tell so;
  choosing again tries again.
- **Windows**: Direct3D 11 (`d3d11_windows.go`), COM called without cgo
  as Gio's backend does: `ID3D11VideoDecoder` of
  `D3D11_DECODER_PROFILE_H264_VLD_NOFGT`, the slices sent as they are
  (`ConfigBitstreamRaw` 2), NV12 read back through a staging texture.
- **In the list**: `chatmedia.Manager.SetHardware`; `GIFRow` leaves the
  row to the GPU, each tile a stream of the helper (`playGIFHardware`),
  played by the loop that plays the sandbox's (`playVideoFrames`). A GIF
  the hardware refuses, or a helper that ended, gives the row back to
  FFmpeg, or the tile to the sandbox.

What differs from the proposal below, for now:

- Frames come back over the helper's standard output, not shared memory:
  a tile's frame is tens of kilobytes.
- The helper scales on the GPU where it can, with the CPU's filters on
  Linux (see "Scaling on the GPU" below), and on the CPU
  (`resample.VideoScaler`, the same as the sandbox's pictures) where it
  cannot, after reading the whole NV12 picture back.
- Nothing is kept across starts after the helper's deaths, the helper's
  memory is not bounded (`program.StartLimited`), and it is not confined
  (a restricted token, a job).

Checked on Windows 11, AMD Radeon RX 580 (driver 26.5.2):

- `TestDecodesAsFFmpeg`: every picture of `assets/video.mp4` (720x1280,
  1081 pictures, High, B-frames) and of 13 clips made with x264 (CAVLC
  and CABAC, 16 references, B-pyramid with an open GOP, 5 slices, JVT
  scaling lists, weighted prediction, cropped 636x358, 1080p, 60 a
  second, intra only) is the same bytes as FFmpeg's.
- The built client as the helper, 12 streams of 720x1280 at once, each
  picture scaled to a tile of 72x128: 533 pictures a second, 1.9 ms a
  picture, against 360 a second that 12 tiles show. The sandbox takes
  10.5 ms a picture of this size (`docs/PLAN.md`).

Not checked: Intel and NVIDIA, Windows 7 and 8, a laptop with two GPUs
(the device is the system's default one), a GPU reset, the list itself in
a window with an account (the tests play it through `chatmedia`,
`TestSavedGIFPlaysOnGPU`), and the processor time of the whole against
FFmpeg's row.

**Linux, Vulkan Video** (`vulkan_linux.go`, `hwvk_linux.c`, written
2026-11-14 on Windows, where it could not be compiled):

- `VK_KHR_video_decode_h264`, the session's profile High (it decodes
  Main and Constrained Baseline too), progressive, 4:2:0 of 8 bits. The
  pictures are the layers of one NV12 image that are both the output and
  the references (`DPB_AND_OUTPUT_COINCIDE`; a device that decodes only
  into pictures apart from its references is refused). Each picture is
  read back with a copy into a host buffer, on the decode queue if it
  copies, else on a graphics or compute one, the image shared by both.
- The StdVideo structures are filled in C from plain ones: cgo does not
  reach bit fields. Every parameter set of the stream is handed to the
  session again whenever one changes.
- `libvulkan.so.1` is loaded with `dlopen`, as Gio does: a machine
  without it runs, and decodes in software. Headers older than 1.3.238
  build a decoder that refuses everything.
- The pictures' layers are not the DPB slots: a slot is held only by a
  reference and by the picture being decoded, so a session asks for
  `max_num_ref_frames + 1` slots however many pictures wait to be shown
  or read (NVIDIA gives 17; a slot for every picture refused streams
  that reorder much).
- A session of a stream that ended is kept a while (`pool.go`: six at
  most, 5 s) and given to the next stream of the same size that needs no
  more surfaces, references or a higher level: the list opens and ends
  streams all the while it scrolls, and making and letting go of a
  session took 7% of the helper's CPU. It is reset for the new stream
  (`hwvk_session_reset`: the DPB at the next picture, no slot held, the
  parameter sets handed again).
- Pictures are read back from memory that is cached and not the GPU's
  own across the bus where the driver has such: reading the other kinds
  is many times slower. The helper tells in the log, once, the device and
  which memory it got.
- A picture rendered is scaled into a buffer of its stream, reused, and
  read by the window's process straight into the image it goes to
  (`readAnswer`, `callInto`): no copy, nothing allocated for it.
- Pictures are scaled on the GPU (2026-11-14): see "Scaling on the GPU".
- The GPUs are tried in order, a helper each (`NewClientFor`, tiers of
  `Client`): first the one the windows draw with, its driver alone
  loaded (`VK_DRIVER_FILES`) and the device named by its ids
  (`KOMARUGRAM_HWVIDEO_DEVICE`), awake already; then, started only when
  that one has no decoder or refuses a stream (a session it cannot make:
  too many references, a level, a size), one with every driver that
  leaves that GPU out, an integrated GPU first. A GPU without a decoder,
  or whose helper ended, is tried no more; a stream refused by one goes
  to the next; a stream the parser refuses wakes no other. Where the
  windows' device is not known, one helper tries every GPU, an
  integrated one first. The windows keep their device: only the GIFs
  move. On Windows the tiers are Direct3D 11 adapters, as DXGI lists them
  without loading a driver: first the one the windows draw with
  (`Graphics.D3D11`, the system's default where none is chosen or the one
  chosen could not draw, named `default`), then every other but WARP, the
  default first. The client follows the windows' GPU: when they move to
  another, a new client is made for it, and the old one's helpers stop
  once its streams have ended.
- A device lost, or a submission not done in 5 s, ends the helper
  (exit 4): its client gives every GIF back to software.
- Every call waits for the GPU (one fence); a session needs Vulkan 1.3
  (synchronization2).

To check on Linux: `go vet ./pkg/hwvideo`, `go test ./pkg/hwvideo` (the
comparison with FFmpeg, `TestDecodesAsFFmpeg`, and the GPU's scaling
against the CPU's, `TestRendersAsCPU`, run where a GPU decodes H.264, with
`KOMARUGRAM_HW_VIDEOS` for more clips) and
`go test ./internal/messenger/chatmedia -run SavedGIF`. lavapipe has no
video decoding: in a container without a GPU those tests skip, and only
the build, the refusal and the scaler on pictures from the host
(`TestScalerAsCPU`) are checked.

### Scaling on the GPU (2026-11-14)

A profile of the helper on Linux (RTX 3050 Laptop, Vulkan Video, the
list of saved GIFs scrolled for 15 s: 1.56 cores) put 61% of its CPU in
`resample.VideoScaler` and 28% in reading the whole NV12 picture back
(the copy of the luma out of the mapped buffer, `splitUV` of the
chroma); decoding itself was 2.6%. So the render request (`opRender`:
the tile's size and the rough flag) is now served on the GPU, and only
the tile's RGBA comes back: `session.render`, `Decoder.Render`. Where a
session cannot (`errNoRender`), the stream goes back to the CPU's path
for good, `Decoder.Read` and `VideoScaler.ResizeInto`, and the helper
tells the first such stream in its log, with the reason.

- **What is done is the CPU's**: `resample.VideoScaler.Plan` gives what
  `ResizeInto` decides from the sizes (the blocks averaged first, whether
  the filter after is Catmull-Rom or linear, the conversion's factors),
  so the GPU does not decide it again. A change to `ResizeInto`'s path
  for opaque 4:2:0 is a change to the shader.
- **Vulkan** (`hwvk_linux.c`, `shaders/scale.comp`): a compute shader of
  three steps in one submission on a queue that computes (the one that
  copies if it does, else any): the decoded picture's layer is copied
  into a buffer in the GPU's memory as the readback copied it (so the
  DPB image needs no sampled or storage usage, which drivers may not
  give a DPB), its planes are averaged in blocks into the same buffer,
  then filtered and converted to RGBA straight into a buffer of cached
  host memory, `w×h×4` bytes, copied into the answer. The buffers and
  the descriptor set are the device's, grown as needed, shared by its
  sessions: every scaling is waited for. The image and buffers are shared
  by the decode, copy and compute queues (`VK_SHARING_MODE_CONCURRENT`
  where they are not one). On lavapipe, from pictures filled on the host,
  every case of `TestScalerAsCPU` (shrinking with and without the rough
  filter, cropped, odd sizes, 1080p to 20×12, enlarged, the same size,
  BT.601, BT.709, BT.2020, both ranges) is within 1 of the CPU's in each
  channel, the mean under 0.0001 (0.07 for JPEG's colours at the same
  size, which the CPU converts with `image/draw`).
- **The SPIR-V is in the repository**, `scale_spv.h`, made from
  `shaders/scale.comp` by `go generate ./pkg/hwvideo` (`shaders/gen.go`),
  which needs `glslc` (shaderc) on the PATH: the build compiles no
  shader. It was made with glslc of shaderc 2026.4 (SPIRV-Tools and
  glslang 1.4.363), `--target-env=vulkan1.0 -O`, and checked with
  `spirv-val`.
- **Direct3D 11** (`d3d11_windows.go`): the decoder's slice goes through
  an `ID3D11VideoProcessor` (`VideoProcessorBlt`), cropped as the stream
  says, into an RGBA texture of the tile's size (BGRA where the processor
  puts out only that, swapped as it is read), then a staging copy of it.
  The filter is the driver's, not the CPU's: `TestRendersAsCPU` holds it
  to a mean difference only. BT.2020 needs `ID3D11VideoContext1` and is
  scaled on the CPU. Written on Linux, built and vetted for Windows, not
  run.
- **The log**: the helper's line telling the device tells now whether its
  pictures are scaled on the GPU, or on the CPU and why (Vulkan: no queue
  that computes, a pipeline not made); a stream refused later is told once.
- **Measured** on the CPU's side only (`BenchmarkTileCPU`, a picture of
  720×1280 to a tile of 160×284, on this VM): reading back the whole
  picture and scaling it, 1.6 ms (1.2 ms rough), against 4 µs to copy the
  tile's pixels; the GPU's work and the wait for it are not in it.

To profile it: run the client with `-profile -profile-dir <dir>`, open
the picker's GIF tab (the helpers start with the first tile), press the
profiler's CPU button and scroll the list slowly and evenly for its
15 s, then press Allocations. The helper's files are beside the
window's, `<time>-cpu-video-decoder-window-gpu-<pid>.pprof` and
`<time>-allocs-video-decoder-…`; its log line, on the client's standard
error, tells whether the pictures were scaled on the GPU. Read them with
`go tool pprof -top <file>`: `resample.(*VideoScaler).ResizeInto` and
`vkSession.read` should be gone, and `vkSession.render` in their place,
its time in C (`runtime.cgocall`): the submission and the wait for the
GPU (`vkWaitForFences`, which may spin in NVIDIA's driver).

## Why

Saved GIFs are MP4 files (H.264, up to HD and 10 MB, often longer than a
few seconds), and the list of them in the picker plays many at once. Each
visible tile runs a decoder of its own, libavcodec built to WASM in
`pkg/sandbox` (`chatmedia.playGIFSandbox`) or an FFmpeg process
(`playGIF`), and each frame is converted from YCbCr and scaled to the tile
on the CPU (`pkg/h264` `Picture`, `pkg/video`), then uploaded as RGBA. A
machine with a weak CPU keeps up with neither decoder, and the list
lags on a 12-core one too, without being bound by its CPU: something on the
way of the frames is wrong before decoding is.

**So the first step is not this document**: a profile of the list
scrolled for 15 s, with the WASM decoder and with FFmpeg
(`docs/PROFILING.md`), and the fixes it points to. Hardware decoding is
for what remains after them, if the decoding itself is what remains.

Looping clips decoded once into memory (`video.Clip`) were rejected: HD
GIFs of many seconds take hundreds of megabytes.

## Requirements

1. **The window must not fall, nor be reached by hostile input, at any
   cost.** Telegram Desktop's windows fall on bad media; ours do not,
   because the decoders are WASM in a sandbox or another process. Hardware
   decoding must keep that, so it starts from what browsers do: a process
   of its own.
2. **The saved GIFs only.** The list is on screen less than any other
   place with video, so the process need not run in the background all
   the time; and a GIF gets into it only when the user saves it, so
   hostile input is less likely there (not impossible: a saved GIF was
   still made by someone else).
3. **Vulkan on Linux first.** Direct3D 11 on Windows only if Linux shows
   it is worth it.

## Why not in the window's process

- **Vulkan does not check anything.** A wrong parameter is undefined
  behavior, not an error. With Vulkan Video the application parses the
  stream's headers itself (SPS, PPS, slice headers, picture order, the
  reference pictures) and hands them to the driver. A parser in Go is
  memory-safe, but whatever it lets through (a reference out of the
  picture buffer, a size the profile does not allow) reaches the driver,
  C code in the process. A SIGSEGV there is not a Go panic: `crash.Guard`
  never sees it, and the process ends with every window.
- **The slice data are parsed by the GPU's hardware and firmware.** Bad
  data may hang the GPU; the kernel resets it, and every Vulkan device on
  it is lost (`VK_ERROR_DEVICE_LOST`). The windows survive that (they make
  their GPU anew, as on a device lost or `app.ResetGPUs`), but a flaw in a
  decoder's firmware is below any sandbox of a process. On NVIDIA a reset
  has been seen to reach other programs too.
- Today an error of the WASM decoder stays in its sandbox as a Go error,
  and FFmpeg falling ends only FFmpeg.

## Proposed design (Linux, Vulkan)

### A decoding process

The client's own binary started again with a flag (`program.Command`, as
every program is started), one for the account's window, only while the
GIF tab of the picker is shown, and stopped once it has decoded nothing for
a while (as `gifIdle` stops FFmpeg). It holds one Vulkan device and a
decode session per tile.

- It makes its instance with the windows' driver alone
  (`vk.CreateInstanceFor`, the device of `app.CurrentGraphics` or of the
  compositor), so that it wakes no other GPU (see the Vulkan steps in
  `docs/PLAN.md`).
- The window's process demuxes the MP4 in Go (`pkg/mp4`) and sends the
  helper the samples of the tiles over a Unix socket; the helper reads no
  file itself.
- The helper parses the H.264 headers in Go and checks them against the
  device's capabilities (`vkGetPhysicalDeviceVideoCapabilitiesKHR`:
  profile, level, size, number of references) before anything reaches the
  driver. What it cannot or must not decode is refused, and that tile goes
  back to the software decoder.
- Hardening that needs no dependency, for the maintainer to decide:
  Landlock and seccomp through `golang.org/x/sys/unix`, so that the helper
  opens no file and no socket but its own.

### Frames back to the window, two stages

1. **Small frames through shared memory.** The helper converts each
   decoded picture from NV12 and scales it to the tile on its GPU (a blit,
   or a compute shader), copies it into a host-visible buffer, and the
   frame, a few hundred kilobytes at a tile's size, goes to the window
   through a `memfd` mapped by both. The window uploads it as any image
   (`paint.NewImageOp`). Nothing of the GPU's objects the helper touched
   reaches the window's device; no change to Gio; it works with windows on
   OpenGL too. This is the stage to build and measure first.
2. **Zero-copy, only if stage 1 costs too much.** The helper exports the
   decoded image as a dma-buf (`VK_EXT_external_memory_dma_buf`,
   `VK_EXT_image_drm_format_modifier`) with a sync file
   (`VK_KHR_external_semaphore_fd`), passed with `SCM_RIGHTS`; the window
   imports it into each window's device and samples it with a
   `VkSamplerYcbcrConversion`. This needs a new kind of image in the Gio
   fork (an external texture rather than `*image.RGBA`), and works only
   with windows on Vulkan (or EGL's `EGL_EXT_image_dma_buf_import`).

### When it fails

- **The helper dies** (a driver fault, a signal): its tiles go back to the
  software decoder for the rest of the session, as `VulkanFailed` does for
  windows; after a few such deaths it is kept off across starts, and a
  choice in the settings tries it again.
- **The GPU is reset**: the helper's device is lost and it ends; the
  windows on the same GPU lose theirs and make it anew. Worth testing on
  Intel, AMD and NVIDIA before it is enabled by default.
- **The device cannot decode a stream** (H.264 4:4:4 or 10-bit, a size
  beyond its limits, too many sessions): that tile is decoded in software,
  which stays in any case.

### Support to check

Vulkan Video decoding of H.264 (`VK_KHR_video_queue`,
`VK_KHR_video_decode_queue`, `VK_KHR_video_decode_h264`) is in NVIDIA's
driver and in recent Mesa (anv for Intel since Gen9, radv for AMD); hasvk
(older Intel) has none. Which Mesa versions enable it by default, and what
a session costs in memory on each, is to be checked on the machines it
will run on, the weak one first: if its GPU has no Vulkan Video, this
design does nothing for it.

## Windows, later

The same process with a D3D11 video decoder (`ID3D11VideoDecoder`, or
Media Foundation with a DXGI device manager), frames back through shared
memory as in stage 1, or as shared textures (`IDXGIResource1::
CreateSharedHandle`) for stage 2. Only once Linux has shown the gain.

## To decide

- The profile of the list, and what it fixes without hardware decoding.
- Whether the gain left is worth a process of its own, after stage 1 is
  measured against the software decoders on the weak machine.
- The H.264 header parser is new code reading hostile data: fuzzed
  (`go test -fuzz`) before it is used.
- Hardening of the helper (Landlock, seccomp) and whether it is on by
  default.
