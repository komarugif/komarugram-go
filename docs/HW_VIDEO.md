# Hardware-decoded video for the saved GIFs (design, 2026-11-13)

Not started. The requirements were set by the maintainer on 2026-11-13;
the rest is a proposal to be measured and agreed before it is built.

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
