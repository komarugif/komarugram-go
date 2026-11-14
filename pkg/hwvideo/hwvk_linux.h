// SPDX-License-Identifier: Unlicense OR MIT

// Vulkan's H.264 decoder for vulkan_linux.go, in plain structures: cgo cannot set Vulkan's bit fields.

#include <stddef.h>
#include <stdint.h>

typedef struct hwvk_device hwvk_device;
typedef struct hwvk_session hwvk_session;

enum {
	HWVK_OK = 0,
	// Not decoded here: software decodes it.
	HWVK_UNSUPPORTED = 1,
	HWVK_ERROR = 2,
	// The device is lost or hangs: nothing decodes on it any more.
	HWVK_LOST = 3,
};

typedef struct {
	uint8_t id, profile, level, constraints;
	uint8_t log2_max_frame_num_minus4, poc_type, log2_max_poc_lsb_minus4;
	uint8_t max_num_ref_frames, delta_pic_order_always_zero, gaps_allowed;
	uint8_t direct_8x8, scaling_present;
	int32_t offset_for_non_ref_pic, offset_for_top_to_bottom_field;
	uint32_t num_ref_frames_in_poc_cycle;
	int32_t offset_for_ref_frame[255];
	uint32_t width_mbs, height_mbs;
	// In units of two luma samples, as the stream sends them.
	uint32_t crop_left, crop_right, crop_top, crop_bottom;
	// In the order they are sent in.
	uint8_t scaling4[6][16];
	uint8_t scaling8[2][64];
} hwvk_sps;

typedef struct {
	uint8_t id, sps_id, num_ref_idx_l0_minus1, num_ref_idx_l1_minus1;
	uint8_t weighted_bipred_idc, weighted_pred, transform_8x8;
	uint8_t redundant_pic_cnt_present, constrained_intra_pred;
	uint8_t deblocking_filter_control_present, bottom_field_pic_order_present;
	uint8_t entropy_coding_mode;
	int8_t init_qp_minus26, init_qs_minus26, chroma_qp_index_offset, second_chroma_qp_index_offset;
	uint8_t scaling4[6][16];
	uint8_t scaling8[2][64];
} hwvk_pps;

typedef struct {
	int32_t surface;
	// frame_num, or LongTermFrameIdx of a long-term reference.
	uint32_t frame_num;
	uint8_t long_term;
	int32_t poc[2];
} hwvk_ref;

typedef struct {
	uint8_t sps_id, pps_id, intra, idr, reference;
	uint32_t frame_num, idr_pic_id;
	int32_t poc[2];
	int32_t surface;
	uint32_t nrefs;
	hwvk_ref refs[16];
} hwvk_pic;

int hwvk_device_open(hwvk_device **out, char *err, size_t errlen);
void hwvk_device_close(hwvk_device *d);

int hwvk_session_open(hwvk_device *d, uint32_t width_mbs, uint32_t height_mbs, uint32_t level, uint32_t refs, uint32_t surfaces, hwvk_session **out, char *err, size_t errlen);
void hwvk_session_close(hwvk_session *s);

// Replaces the session's parameter sets with every one the stream sent.
int hwvk_session_parameters(hwvk_session *s, const hwvk_sps *sps, uint32_t nsps, const hwvk_pps *pps, uint32_t npps, char *err, size_t errlen);

// Decodes the picture whose slices, each after a start code, are data at offsets.
int hwvk_decode(hwvk_session *s, const hwvk_pic *p, const uint8_t *data, size_t size, const uint32_t *offsets, uint32_t nslices, char *err, size_t errlen);

// Copies surface to host memory as NV12 rows of pitch bytes, valid until the session's next read.
int hwvk_read(hwvk_session *s, uint32_t surface, const uint8_t **nv12, uint32_t *pitch, uint32_t *rows, char *err, size_t errlen);
