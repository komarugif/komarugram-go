/* Minimal VP9 encoding surface for the host: write a picture's planes, encode
 * it, read back the compressed frame.
 *
 * Everything the host is allowed to do with this module goes through these
 * exports. The module has no main and no reason to touch the outside world.
 * The encoder keeps no frames back (no lag, no alt-ref) and makes a key
 * frame only when the host asks: two of them, the picture and its alpha,
 * give one frame each for the same pictures, key frames in step. */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "vpx/vpx_encoder.h"
#include "vpx/vp8cx.h"

typedef struct {
  vpx_codec_ctx_t codec;
  vpx_image_t image;
  vpx_codec_iter_t iter;
  const vpx_codec_cx_pkt_t *packet;
  int ready, has_image;
  unsigned long deadline;
} vpxe_encoder;

__attribute__((export_name("vpxe_alloc"))) uint8_t *vpxe_alloc(size_t size) {
  return (uint8_t *)malloc(size);
}

__attribute__((export_name("vpxe_dealloc"))) void vpxe_dealloc(uint8_t *ptr) {
  free(ptr);
}

/* Starts an encoder of width x height pictures, fps a second, at quality
 * cq (0 best, 63 worst) and speed (0 slowest, 9 fastest): up to 5 libvpx's
 * good quality, which takes speeds up to 5 only, and above it its real time
 * mode, faster for a lower quality. */
__attribute__((export_name("vpxe_new"))) vpxe_encoder *vpxe_new(int width, int height, int fps,
                                                                int cq, int speed) {
  if (width <= 0 || height <= 0 || fps <= 0 || cq < 0 || cq > 63) return NULL;
  vpxe_encoder *e = (vpxe_encoder *)calloc(1, sizeof(vpxe_encoder));
  if (e == NULL) return NULL;
  vpx_codec_enc_cfg_t cfg;
  if (vpx_codec_enc_config_default(vpx_codec_vp9_cx(), &cfg, 0)) {
    free(e);
    return NULL;
  }
  cfg.g_w = (unsigned int)width;
  cfg.g_h = (unsigned int)height;
  cfg.g_timebase.num = 1;
  cfg.g_timebase.den = fps;
  cfg.g_threads = 1;
  cfg.g_lag_in_frames = 0;
  cfg.g_pass = VPX_RC_ONE_PASS;
  cfg.g_error_resilient = 0;
  cfg.rc_end_usage = VPX_Q;
  cfg.rc_min_quantizer = 0;
  cfg.rc_max_quantizer = 63;
  cfg.kf_mode = VPX_KF_DISABLED;
  if (vpx_codec_enc_init(&e->codec, vpx_codec_vp9_cx(), &cfg, 0)) {
    free(e);
    return NULL;
  }
  e->ready = 1;
  e->deadline = speed > 5 ? VPX_DL_REALTIME : VPX_DL_GOOD_QUALITY;
  /* Full-range BT.601, as JPEG and Go's image.YCbCr have it, said so in the
   * bitstream for the decoders that read it. */
  if (vpx_codec_control(&e->codec, VP8E_SET_CQ_LEVEL, cq) ||
      vpx_codec_control(&e->codec, VP8E_SET_CPUUSED, speed) ||
      vpx_codec_control(&e->codec, VP8E_SET_ENABLEAUTOALTREF, 0) ||
      vpx_codec_control(&e->codec, VP9E_SET_COLOR_SPACE, VPX_CS_BT_601) ||
      vpx_codec_control(&e->codec, VP9E_SET_COLOR_RANGE, VPX_CR_FULL_RANGE)) {
    vpx_codec_destroy(&e->codec);
    free(e);
    return NULL;
  }
  if (vpx_img_alloc(&e->image, VPX_IMG_FMT_I420, (unsigned int)width, (unsigned int)height, 16) == NULL) {
    vpx_codec_destroy(&e->codec);
    free(e);
    return NULL;
  }
  e->image.cs = VPX_CS_BT_601;
  e->image.range = VPX_CR_FULL_RANGE;
  e->has_image = 1;
  return e;
}

__attribute__((export_name("vpxe_drop"))) void vpxe_drop(vpxe_encoder *e) {
  if (e == NULL) return;
  if (e->has_image) vpx_img_free(&e->image);
  if (e->ready) vpx_codec_destroy(&e->codec);
  free(e);
}

/* Plane 0 is luma, 1 and 2 are the chroma planes, at half the size. */
__attribute__((export_name("vpxe_plane"))) uint8_t *vpxe_plane(vpxe_encoder *e, int plane) {
  if (e == NULL || !e->has_image || plane < 0 || plane > 2) return NULL;
  return e->image.planes[plane];
}

__attribute__((export_name("vpxe_stride"))) int vpxe_stride(vpxe_encoder *e, int plane) {
  if (e == NULL || !e->has_image || plane < 0 || plane > 2) return 0;
  return e->image.stride[plane];
}

/* Encodes the picture in the planes as frame pts, a key frame when key is
 * set; flush instead encodes nothing and ends the stream. Returns 0 on
 * success, or the vpx error code. */
__attribute__((export_name("vpxe_encode"))) int vpxe_encode(vpxe_encoder *e, int64_t pts, int key,
                                                            int flush) {
  if (e == NULL || !e->ready) return -1;
  vpx_enc_frame_flags_t flags = key ? VPX_EFLAG_FORCE_KF : 0;
  vpx_codec_err_t err = vpx_codec_encode(&e->codec, flush ? NULL : &e->image, pts, 1, flags,
                                         e->deadline);
  e->iter = NULL;
  e->packet = NULL;
  return (int)err;
}

/* Moves to the next compressed frame of the last encode; returns its size,
 * or 0 when there is none. */
__attribute__((export_name("vpxe_next"))) int vpxe_next(vpxe_encoder *e) {
  if (e == NULL || !e->ready) return 0;
  const vpx_codec_cx_pkt_t *packet;
  while ((packet = vpx_codec_get_cx_data(&e->codec, &e->iter)) != NULL) {
    if (packet->kind == VPX_CODEC_CX_FRAME_PKT) {
      e->packet = packet;
      return (int)packet->data.frame.sz;
    }
  }
  e->packet = NULL;
  return 0;
}

__attribute__((export_name("vpxe_data"))) const uint8_t *vpxe_data(vpxe_encoder *e) {
  return e && e->packet ? (const uint8_t *)e->packet->data.frame.buf : NULL;
}

__attribute__((export_name("vpxe_key"))) int vpxe_key(vpxe_encoder *e) {
  return e && e->packet && (e->packet->data.frame.flags & VPX_FRAME_IS_KEY) ? 1 : 0;
}

__attribute__((export_name("vpxe_pts"))) int64_t vpxe_pts(vpxe_encoder *e) {
  return e && e->packet ? (int64_t)e->packet->data.frame.pts : -1;
}
