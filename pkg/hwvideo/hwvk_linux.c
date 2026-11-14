// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux && !novulkan

#define VK_NO_PROTOTYPES 1
#include <vulkan/vulkan.h>
#include <dlfcn.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "hwvk_linux.h"

// The video structures left Vulkan's beta headers in 1.3.238.
#if defined(VK_KHR_video_decode_h264) && VK_HEADER_VERSION >= 238
#define HWVK_VIDEO 1
#endif

#ifndef HWVK_VIDEO

int hwvk_device_open(hwvk_device **out, uint32_t vendor, uint32_t device, int pick, char *err, size_t errlen) {
	*out = NULL;
	snprintf(err, errlen, "built with Vulkan headers older than 1.3.238, without video decoding");
	return HWVK_UNSUPPORTED;
}
void hwvk_device_close(hwvk_device *d) {}
int hwvk_session_open(hwvk_device *d, uint32_t width_mbs, uint32_t height_mbs, uint32_t level, uint32_t refs, uint32_t surfaces, hwvk_session **out, char *err, size_t errlen) {
	*out = NULL;
	return HWVK_UNSUPPORTED;
}
void hwvk_session_close(hwvk_session *s) {}
void hwvk_session_reset(hwvk_session *s) {}
void hwvk_describe(hwvk_device *d, char *name, size_t namelen, int *cached, int *device_local, int *scales, char *why, size_t whylen) {}
int hwvk_session_parameters(hwvk_session *s, const hwvk_sps *sps, uint32_t nsps, const hwvk_pps *pps, uint32_t npps, char *err, size_t errlen) { return HWVK_ERROR; }
int hwvk_decode(hwvk_session *s, const hwvk_pic *p, const uint8_t *data, size_t size, const uint32_t *offsets, uint32_t nslices, char *err, size_t errlen) { return HWVK_ERROR; }
int hwvk_read(hwvk_session *s, uint32_t surface, const uint8_t **nv12, uint32_t *pitch, uint32_t *rows, char *err, size_t errlen) { return HWVK_ERROR; }
int hwvk_render(hwvk_session *s, uint32_t surface, const hwvk_scale *p, uint8_t *dst, uint32_t stride, char *err, size_t errlen) {
	snprintf(err, errlen, "built without video decoding");
	return HWVK_UNSUPPORTED;
}
int hwvk_compute_open(hwvk_device **out, char *err, size_t errlen) {
	*out = NULL;
	snprintf(err, errlen, "built with Vulkan headers older than 1.3.238");
	return HWVK_UNSUPPORTED;
}
int hwvk_scale_nv12(hwvk_device *d, const uint8_t *nv12, uint32_t pitch, uint32_t rows, const hwvk_scale *p, uint8_t *dst, uint32_t stride, char *err, size_t errlen) { return HWVK_ERROR; }

#else

#include "scale_spv.h"

#define INSTANCE_FUNCS(X) \
	X(vkDestroyInstance) \
	X(vkEnumeratePhysicalDevices) \
	X(vkGetPhysicalDeviceProperties) \
	X(vkGetPhysicalDeviceQueueFamilyProperties2) \
	X(vkEnumerateDeviceExtensionProperties) \
	X(vkGetPhysicalDeviceMemoryProperties) \
	X(vkCreateDevice) \
	X(vkGetDeviceProcAddr)

#define VIDEO_INSTANCE_FUNCS(X) \
	X(vkGetPhysicalDeviceVideoCapabilitiesKHR) \
	X(vkGetPhysicalDeviceVideoFormatPropertiesKHR)

#define DEVICE_FUNCS(X) \
	X(vkDestroyDevice) \
	X(vkGetDeviceQueue) \
	X(vkCreateCommandPool) \
	X(vkDestroyCommandPool) \
	X(vkAllocateCommandBuffers) \
	X(vkBeginCommandBuffer) \
	X(vkEndCommandBuffer) \
	X(vkQueueSubmit) \
	X(vkCreateFence) \
	X(vkDestroyFence) \
	X(vkWaitForFences) \
	X(vkResetFences) \
	X(vkCreateImage) \
	X(vkDestroyImage) \
	X(vkGetImageMemoryRequirements) \
	X(vkAllocateMemory) \
	X(vkFreeMemory) \
	X(vkBindImageMemory) \
	X(vkCreateImageView) \
	X(vkDestroyImageView) \
	X(vkCreateBuffer) \
	X(vkDestroyBuffer) \
	X(vkGetBufferMemoryRequirements) \
	X(vkBindBufferMemory) \
	X(vkMapMemory) \
	X(vkUnmapMemory) \
	X(vkCmdPipelineBarrier2) \
	X(vkCmdCopyImageToBuffer) \
	X(vkDeviceWaitIdle) \
	X(vkCreateShaderModule) \
	X(vkDestroyShaderModule) \
	X(vkCreateDescriptorSetLayout) \
	X(vkDestroyDescriptorSetLayout) \
	X(vkCreatePipelineLayout) \
	X(vkDestroyPipelineLayout) \
	X(vkCreateComputePipelines) \
	X(vkDestroyPipeline) \
	X(vkCreateDescriptorPool) \
	X(vkDestroyDescriptorPool) \
	X(vkAllocateDescriptorSets) \
	X(vkUpdateDescriptorSets) \
	X(vkCmdBindPipeline) \
	X(vkCmdBindDescriptorSets) \
	X(vkCmdPushConstants) \
	X(vkCmdDispatch)

#define VIDEO_DEVICE_FUNCS(X) \
	X(vkCreateVideoSessionKHR) \
	X(vkDestroyVideoSessionKHR) \
	X(vkGetVideoSessionMemoryRequirementsKHR) \
	X(vkBindVideoSessionMemoryKHR) \
	X(vkCreateVideoSessionParametersKHR) \
	X(vkDestroyVideoSessionParametersKHR) \
	X(vkCmdBeginVideoCodingKHR) \
	X(vkCmdEndVideoCodingKHR) \
	X(vkCmdControlVideoCodingKHR) \
	X(vkCmdDecodeVideoKHR)

// How long a submission may take before the device is taken to hang.
#define WAIT_NS 5000000000ull

#define MAX_SPS 32
#define MAX_PPS 256

// The buffers of a scaling: the picture copied and its blocks (work, on
// the GPU), the pixels (out, mapped), and the descriptor set naming them.
// One serves every session of a device: each scaling is waited for.
typedef struct {
	VkBuffer work;
	VkDeviceMemory work_memory;
	VkDeviceSize work_size;
	VkBuffer out;
	VkDeviceMemory out_memory;
	uint8_t *out_map;
	VkDeviceSize out_size;
	VkDescriptorPool pool;
	VkDescriptorSet set;
} scale_target;

// The push constants of shaders/scale.comp, in its order.
typedef struct {
	uint32_t mode, w, h, fx, fy, linear;
	uint32_t sw, sh, src_y, src_uv, pitch, blocks;
	float gain, black, rv, gu, gv, bu;
} scale_push;

struct hwvk_device {
	void *lib;
	PFN_vkGetInstanceProcAddr gipa;
	VkInstance instance;
	VkPhysicalDevice phys;
	VkDevice device;
	uint32_t decode_family, transfer_family;
	VkQueue decode_queue, transfer_queue;
	VkCommandPool decode_pool, transfer_pool;
	VkCommandBuffer decode_cmd, transfer_cmd;
	// The queue pictures are scaled on, -1 for none, and the families
	// the pictures and buffers are shared by, each once.
	int64_t compute_family;
	VkQueue compute_queue;
	VkCommandPool compute_pool;
	VkCommandBuffer compute_cmd;
	uint32_t families[3], nfamilies;
	// The scaler (shaders/scale.comp): its pipeline, or why there is none.
	VkShaderModule shader;
	VkDescriptorSetLayout set_layout;
	VkPipelineLayout pipeline_layout;
	VkPipeline pipeline;
	char scale_why[256];
	scale_target target;
	VkFence fence;
	VkPhysicalDeviceMemoryProperties memory;
	VkVideoDecodeH264ProfileInfoKHR h264_profile;
	VkVideoProfileInfoKHR profile;
	VkVideoProfileListInfoKHR profile_list;
	VkVideoDecodeH264CapabilitiesKHR h264_caps;
	VkVideoDecodeCapabilitiesKHR decode_caps;
	VkVideoCapabilitiesKHR caps;
	VkVideoFormatPropertiesKHR format;
	pthread_mutex_t mu;
	int lost;
	char name[VK_MAX_PHYSICAL_DEVICE_NAME_SIZE];
	// The memory pictures were last read back from.
	VkMemoryPropertyFlags readback_flags;
#define X(f) PFN_##f f;
	INSTANCE_FUNCS(X)
	VIDEO_INSTANCE_FUNCS(X)
	DEVICE_FUNCS(X)
	VIDEO_DEVICE_FUNCS(X)
#undef X
};

struct hwvk_session {
	hwvk_device *d;
	uint32_t coded_w, coded_h, surfaces, max_refs;
	// The DPB slots: the references and the picture being decoded need
	// one each, the pictures waiting to be put out or read none. slot_of
	// is the slot of each surface, -1 for none.
	uint32_t slots;
	int32_t *slot_of;
	VkVideoSessionKHR session;
	VkDeviceMemory *session_memory;
	uint32_t session_memory_count;
	VkVideoSessionParametersKHR params;
	VkImage image;
	VkDeviceMemory image_memory;
	VkImageView *views;
	VkImageLayout *layouts;
	VkBuffer bitstream;
	VkDeviceMemory bitstream_memory;
	uint8_t *bitstream_map;
	VkDeviceSize bitstream_size;
	VkBuffer readback;
	VkDeviceMemory readback_memory;
	uint8_t *readback_map;
	int needs_reset;
};

static VkDeviceSize align_up(VkDeviceSize v, VkDeviceSize a) {
	return a > 1 ? (v + a - 1) / a * a : v;
}

static int vk_fail(char *err, size_t errlen, const char *what, VkResult r) {
	snprintf(err, errlen, "%s: VkResult %d", what, (int)r);
	if (r == VK_ERROR_DEVICE_LOST) {
		return HWVK_LOST;
	}
	return HWVK_ERROR;
}

static void setup_profile(hwvk_device *d) {
	memset(&d->h264_profile, 0, sizeof d->h264_profile);
	d->h264_profile.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_H264_PROFILE_INFO_KHR;
	// High decodes Main and Constrained Baseline too; the parser refuses the rest of Baseline.
	d->h264_profile.stdProfileIdc = STD_VIDEO_H264_PROFILE_IDC_HIGH;
	d->h264_profile.pictureLayout = VK_VIDEO_DECODE_H264_PICTURE_LAYOUT_PROGRESSIVE_KHR;
	memset(&d->profile, 0, sizeof d->profile);
	d->profile.sType = VK_STRUCTURE_TYPE_VIDEO_PROFILE_INFO_KHR;
	d->profile.pNext = &d->h264_profile;
	d->profile.videoCodecOperation = VK_VIDEO_CODEC_OPERATION_DECODE_H264_BIT_KHR;
	d->profile.chromaSubsampling = VK_VIDEO_CHROMA_SUBSAMPLING_420_BIT_KHR;
	d->profile.lumaBitDepth = VK_VIDEO_COMPONENT_BIT_DEPTH_8_BIT_KHR;
	d->profile.chromaBitDepth = VK_VIDEO_COMPONENT_BIT_DEPTH_8_BIT_KHR;
	memset(&d->profile_list, 0, sizeof d->profile_list);
	d->profile_list.sType = VK_STRUCTURE_TYPE_VIDEO_PROFILE_LIST_INFO_KHR;
	d->profile_list.profileCount = 1;
	d->profile_list.pProfiles = &d->profile;
}

static int has_extension(VkExtensionProperties *ext, uint32_t n, const char *name) {
	for (uint32_t i = 0; i < n; i++) {
		if (strcmp(ext[i].extensionName, name) == 0) {
			return 1;
		}
	}
	return 0;
}

static const char *device_extensions[] = {
	VK_KHR_VIDEO_QUEUE_EXTENSION_NAME,
	VK_KHR_VIDEO_DECODE_QUEUE_EXTENSION_NAME,
	VK_KHR_VIDEO_DECODE_H264_EXTENSION_NAME,
};

// Fills d's families, capabilities and format for phys, or tells in why it does not decode.
static int suitable(hwvk_device *d, VkPhysicalDevice phys, char *why, size_t whylen) {
	VkPhysicalDeviceProperties props;
	d->vkGetPhysicalDeviceProperties(phys, &props);
	if (props.apiVersion < VK_API_VERSION_1_3) {
		snprintf(why, whylen, "%s: Vulkan %u.%u", props.deviceName, VK_API_VERSION_MAJOR(props.apiVersion), VK_API_VERSION_MINOR(props.apiVersion));
		return 0;
	}
	uint32_t n = 0;
	if (d->vkEnumerateDeviceExtensionProperties(phys, NULL, &n, NULL) != VK_SUCCESS) {
		return 0;
	}
	VkExtensionProperties *ext = calloc(n ? n : 1, sizeof *ext);
	if (!ext || d->vkEnumerateDeviceExtensionProperties(phys, NULL, &n, ext) != VK_SUCCESS) {
		free(ext);
		return 0;
	}
	int ok = 1;
	for (size_t i = 0; i < sizeof device_extensions / sizeof *device_extensions; i++) {
		ok = ok && has_extension(ext, n, device_extensions[i]);
	}
	free(ext);
	if (!ok) {
		snprintf(why, whylen, "%s: no H.264 decoding (VK_KHR_video_decode_h264)", props.deviceName);
		return 0;
	}
	uint32_t families = 0;
	d->vkGetPhysicalDeviceQueueFamilyProperties2(phys, &families, NULL);
	VkQueueFamilyProperties2 *qf = calloc(families ? families : 1, sizeof *qf);
	VkQueueFamilyVideoPropertiesKHR *qv = calloc(families ? families : 1, sizeof *qv);
	if (!qf || !qv) {
		free(qf);
		free(qv);
		return 0;
	}
	for (uint32_t i = 0; i < families; i++) {
		qv[i].sType = VK_STRUCTURE_TYPE_QUEUE_FAMILY_VIDEO_PROPERTIES_KHR;
		qf[i].sType = VK_STRUCTURE_TYPE_QUEUE_FAMILY_PROPERTIES_2;
		qf[i].pNext = &qv[i];
	}
	d->vkGetPhysicalDeviceQueueFamilyProperties2(phys, &families, qf);
	int64_t decode = -1, transfer = -1;
	for (uint32_t i = 0; i < families; i++) {
		VkQueueFlags flags = qf[i].queueFamilyProperties.queueFlags;
		if (decode < 0 && (flags & VK_QUEUE_VIDEO_DECODE_BIT_KHR) && (qv[i].videoCodecOperations & VK_VIDEO_CODEC_OPERATION_DECODE_H264_BIT_KHR)) {
			decode = i;
		}
	}
	if (decode >= 0 && (qf[decode].queueFamilyProperties.queueFlags & VK_QUEUE_TRANSFER_BIT)) {
		transfer = decode;
	}
	for (uint32_t i = 0; i < families && transfer < 0; i++) {
		// Graphics and compute queues copy too, whether they tell it or not.
		if (qf[i].queueFamilyProperties.queueFlags & (VK_QUEUE_TRANSFER_BIT | VK_QUEUE_GRAPHICS_BIT | VK_QUEUE_COMPUTE_BIT)) {
			transfer = i;
		}
	}
	// Pictures are scaled where they are copied if that queue computes,
	// else on any queue that does.
	int64_t compute = -1;
	if (transfer >= 0 && (qf[transfer].queueFamilyProperties.queueFlags & VK_QUEUE_COMPUTE_BIT)) {
		compute = transfer;
	}
	for (uint32_t i = 0; i < families && compute < 0; i++) {
		if (qf[i].queueFamilyProperties.queueFlags & VK_QUEUE_COMPUTE_BIT) {
			compute = i;
		}
	}
	free(qf);
	free(qv);
	if (decode < 0 || transfer < 0) {
		snprintf(why, whylen, "%s: no queue decodes H.264", props.deviceName);
		return 0;
	}
	memset(&d->h264_caps, 0, sizeof d->h264_caps);
	d->h264_caps.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_H264_CAPABILITIES_KHR;
	memset(&d->decode_caps, 0, sizeof d->decode_caps);
	d->decode_caps.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_CAPABILITIES_KHR;
	d->decode_caps.pNext = &d->h264_caps;
	memset(&d->caps, 0, sizeof d->caps);
	d->caps.sType = VK_STRUCTURE_TYPE_VIDEO_CAPABILITIES_KHR;
	d->caps.pNext = &d->decode_caps;
	VkResult r = d->vkGetPhysicalDeviceVideoCapabilitiesKHR(phys, &d->profile, &d->caps);
	if (r != VK_SUCCESS) {
		snprintf(why, whylen, "%s: H.264 High of 8 bits not decoded (VkResult %d)", props.deviceName, (int)r);
		return 0;
	}
	if (!(d->decode_caps.flags & VK_VIDEO_DECODE_CAPABILITY_DPB_AND_OUTPUT_COINCIDE_BIT_KHR)) {
		snprintf(why, whylen, "%s: decodes only into pictures apart from its references", props.deviceName);
		return 0;
	}
	VkPhysicalDeviceVideoFormatInfoKHR fi = {
		.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VIDEO_FORMAT_INFO_KHR,
		.pNext = &d->profile_list,
		.imageUsage = VK_IMAGE_USAGE_VIDEO_DECODE_DPB_BIT_KHR | VK_IMAGE_USAGE_VIDEO_DECODE_DST_BIT_KHR | VK_IMAGE_USAGE_TRANSFER_SRC_BIT,
	};
	uint32_t formats = 0;
	r = d->vkGetPhysicalDeviceVideoFormatPropertiesKHR(phys, &fi, &formats, NULL);
	if (r != VK_SUCCESS || formats == 0) {
		snprintf(why, whylen, "%s: no picture format to decode into and copy from (VkResult %d)", props.deviceName, (int)r);
		return 0;
	}
	VkVideoFormatPropertiesKHR *fp = calloc(formats, sizeof *fp);
	if (!fp) {
		return 0;
	}
	for (uint32_t i = 0; i < formats; i++) {
		fp[i].sType = VK_STRUCTURE_TYPE_VIDEO_FORMAT_PROPERTIES_KHR;
	}
	r = d->vkGetPhysicalDeviceVideoFormatPropertiesKHR(phys, &fi, &formats, fp);
	ok = 0;
	for (uint32_t i = 0; r == VK_SUCCESS && i < formats; i++) {
		if (fp[i].format == VK_FORMAT_G8_B8R8_2PLANE_420_UNORM) {
			d->format = fp[i];
			d->format.pNext = NULL;
			ok = 1;
			break;
		}
	}
	free(fp);
	if (!ok) {
		snprintf(why, whylen, "%s: does not decode into NV12", props.deviceName);
		return 0;
	}
	d->decode_family = (uint32_t)decode;
	d->transfer_family = (uint32_t)transfer;
	d->compute_family = compute;
	return 1;
}

// compute_suitable fills d's families for phys, which is only to scale
// pictures (the scaler's tests): its first queue that computes.
static int compute_suitable(hwvk_device *d, VkPhysicalDevice phys, char *why, size_t whylen) {
	VkPhysicalDeviceProperties props;
	d->vkGetPhysicalDeviceProperties(phys, &props);
	if (props.apiVersion < VK_API_VERSION_1_3) {
		snprintf(why, whylen, "%s: Vulkan %u.%u", props.deviceName, VK_API_VERSION_MAJOR(props.apiVersion), VK_API_VERSION_MINOR(props.apiVersion));
		return 0;
	}
	uint32_t families = 0;
	d->vkGetPhysicalDeviceQueueFamilyProperties2(phys, &families, NULL);
	VkQueueFamilyProperties2 *qf = calloc(families ? families : 1, sizeof *qf);
	if (!qf) {
		return 0;
	}
	for (uint32_t i = 0; i < families; i++) {
		qf[i].sType = VK_STRUCTURE_TYPE_QUEUE_FAMILY_PROPERTIES_2;
	}
	d->vkGetPhysicalDeviceQueueFamilyProperties2(phys, &families, qf);
	int64_t compute = -1;
	for (uint32_t i = 0; i < families && compute < 0; i++) {
		if (qf[i].queueFamilyProperties.queueFlags & VK_QUEUE_COMPUTE_BIT) {
			compute = i;
		}
	}
	free(qf);
	if (compute < 0) {
		snprintf(why, whylen, "%s: no queue computes", props.deviceName);
		return 0;
	}
	d->decode_family = d->transfer_family = (uint32_t)compute;
	d->compute_family = compute;
	return 1;
}

static void target_free(hwvk_device *d, scale_target *t);

void hwvk_device_close(hwvk_device *d) {
	if (!d) {
		return;
	}
	if (d->device) {
		d->vkDeviceWaitIdle(d->device);
		target_free(d, &d->target);
		if (d->pipeline) {
			d->vkDestroyPipeline(d->device, d->pipeline, NULL);
		}
		if (d->pipeline_layout) {
			d->vkDestroyPipelineLayout(d->device, d->pipeline_layout, NULL);
		}
		if (d->set_layout) {
			d->vkDestroyDescriptorSetLayout(d->device, d->set_layout, NULL);
		}
		if (d->shader) {
			d->vkDestroyShaderModule(d->device, d->shader, NULL);
		}
		if (d->fence) {
			d->vkDestroyFence(d->device, d->fence, NULL);
		}
		if (d->compute_pool && d->compute_pool != d->transfer_pool && d->compute_pool != d->decode_pool) {
			d->vkDestroyCommandPool(d->device, d->compute_pool, NULL);
		}
		if (d->transfer_pool && d->transfer_pool != d->decode_pool) {
			d->vkDestroyCommandPool(d->device, d->transfer_pool, NULL);
		}
		if (d->decode_pool) {
			d->vkDestroyCommandPool(d->device, d->decode_pool, NULL);
		}
		d->vkDestroyDevice(d->device, NULL);
	}
	if (d->instance) {
		d->vkDestroyInstance(d->instance, NULL);
	}
	if (d->lib) {
		dlclose(d->lib);
	}
	pthread_mutex_destroy(&d->mu);
	free(d);
}

static int command_pool(hwvk_device *d, uint32_t family, VkCommandPool *pool, VkCommandBuffer *cmd, char *err, size_t errlen) {
	VkCommandPoolCreateInfo pi = {
		.sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO,
		.flags = VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT,
		.queueFamilyIndex = family,
	};
	VkResult r = d->vkCreateCommandPool(d->device, &pi, NULL, pool);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkCreateCommandPool", r);
	}
	VkCommandBufferAllocateInfo ai = {
		.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO,
		.commandPool = *pool,
		.level = VK_COMMAND_BUFFER_LEVEL_PRIMARY,
		.commandBufferCount = 1,
	};
	r = d->vkAllocateCommandBuffers(d->device, &ai, cmd);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkAllocateCommandBuffers", r);
	}
	return HWVK_OK;
}

// scaler_create makes the pipeline pictures are scaled with, or tells in
// d->scale_why why there is none: the pictures are then scaled on the CPU.
static void scaler_create(hwvk_device *d) {
	if (d->compute_family < 0) {
		snprintf(d->scale_why, sizeof d->scale_why, "no queue of the device computes");
		return;
	}
	VkShaderModuleCreateInfo si = {
		.sType = VK_STRUCTURE_TYPE_SHADER_MODULE_CREATE_INFO,
		.codeSize = sizeof hwvk_scale_spv,
		.pCode = hwvk_scale_spv,
	};
	VkResult r = d->vkCreateShaderModule(d->device, &si, NULL, &d->shader);
	if (r != VK_SUCCESS) {
		d->shader = VK_NULL_HANDLE;
		snprintf(d->scale_why, sizeof d->scale_why, "vkCreateShaderModule: VkResult %d", (int)r);
		return;
	}
	VkDescriptorSetLayoutBinding bindings[2] = {
		{.binding = 0, .descriptorType = VK_DESCRIPTOR_TYPE_STORAGE_BUFFER, .descriptorCount = 1, .stageFlags = VK_SHADER_STAGE_COMPUTE_BIT},
		{.binding = 1, .descriptorType = VK_DESCRIPTOR_TYPE_STORAGE_BUFFER, .descriptorCount = 1, .stageFlags = VK_SHADER_STAGE_COMPUTE_BIT},
	};
	VkDescriptorSetLayoutCreateInfo li = {
		.sType = VK_STRUCTURE_TYPE_DESCRIPTOR_SET_LAYOUT_CREATE_INFO,
		.bindingCount = 2,
		.pBindings = bindings,
	};
	r = d->vkCreateDescriptorSetLayout(d->device, &li, NULL, &d->set_layout);
	if (r != VK_SUCCESS) {
		d->set_layout = VK_NULL_HANDLE;
		snprintf(d->scale_why, sizeof d->scale_why, "vkCreateDescriptorSetLayout: VkResult %d", (int)r);
		return;
	}
	VkPushConstantRange range = {VK_SHADER_STAGE_COMPUTE_BIT, 0, sizeof(scale_push)};
	VkPipelineLayoutCreateInfo pli = {
		.sType = VK_STRUCTURE_TYPE_PIPELINE_LAYOUT_CREATE_INFO,
		.setLayoutCount = 1,
		.pSetLayouts = &d->set_layout,
		.pushConstantRangeCount = 1,
		.pPushConstantRanges = &range,
	};
	r = d->vkCreatePipelineLayout(d->device, &pli, NULL, &d->pipeline_layout);
	if (r != VK_SUCCESS) {
		d->pipeline_layout = VK_NULL_HANDLE;
		snprintf(d->scale_why, sizeof d->scale_why, "vkCreatePipelineLayout: VkResult %d", (int)r);
		return;
	}
	VkComputePipelineCreateInfo ci = {
		.sType = VK_STRUCTURE_TYPE_COMPUTE_PIPELINE_CREATE_INFO,
		.stage = {
			.sType = VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO,
			.stage = VK_SHADER_STAGE_COMPUTE_BIT,
			.module = d->shader,
			.pName = "main",
		},
		.layout = d->pipeline_layout,
	};
	r = d->vkCreateComputePipelines(d->device, VK_NULL_HANDLE, 1, &ci, NULL, &d->pipeline);
	if (r != VK_SUCCESS) {
		d->pipeline = VK_NULL_HANDLE;
		snprintf(d->scale_why, sizeof d->scale_why, "vkCreateComputePipelines: VkResult %d", (int)r);
	}
}

// device_open opens the device of a decoder, or with video unset one that
// only scales pictures, for the scaler's tests: any device that computes.
static int device_open(hwvk_device **out, uint32_t vendor, uint32_t device, int pick, int video, char *err, size_t errlen) {
	*out = NULL;
	hwvk_device *d = calloc(1, sizeof *d);
	if (!d) {
		snprintf(err, errlen, "out of memory");
		return HWVK_ERROR;
	}
	pthread_mutex_init(&d->mu, NULL);
	d->compute_family = -1;
	setup_profile(d);
	d->lib = dlopen("libvulkan.so.1", RTLD_NOW | RTLD_LOCAL);
	if (!d->lib) {
		snprintf(err, errlen, "no Vulkan loader (libvulkan.so.1)");
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
	d->gipa = (PFN_vkGetInstanceProcAddr)dlsym(d->lib, "vkGetInstanceProcAddr");
	PFN_vkCreateInstance create_instance = d->gipa ? (PFN_vkCreateInstance)d->gipa(NULL, "vkCreateInstance") : NULL;
	PFN_vkEnumerateInstanceVersion instance_version = d->gipa ? (PFN_vkEnumerateInstanceVersion)d->gipa(NULL, "vkEnumerateInstanceVersion") : NULL;
	uint32_t version = VK_API_VERSION_1_0;
	if (instance_version) {
		instance_version(&version);
	}
	if (!create_instance || version < VK_API_VERSION_1_3) {
		snprintf(err, errlen, "the Vulkan loader is older than 1.3");
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
	VkApplicationInfo app = {
		.sType = VK_STRUCTURE_TYPE_APPLICATION_INFO,
		.pApplicationName = "KomaruGram video decoder",
		.apiVersion = VK_API_VERSION_1_3,
	};
	VkInstanceCreateInfo ci = {
		.sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO,
		.pApplicationInfo = &app,
	};
	VkResult r = create_instance(&ci, NULL, &d->instance);
	if (r != VK_SUCCESS) {
		vk_fail(err, errlen, "vkCreateInstance", r);
		d->instance = NULL;
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
#define X(f) \
	d->f = (PFN_##f)d->gipa(d->instance, #f); \
	if (!d->f) { \
		snprintf(err, errlen, "no %s", #f); \
		hwvk_device_close(d); \
		return HWVK_UNSUPPORTED; \
	}
	INSTANCE_FUNCS(X)
	if (video) {
		VIDEO_INSTANCE_FUNCS(X)
	}
#undef X
	uint32_t count = 0;
	d->vkEnumeratePhysicalDevices(d->instance, &count, NULL);
	VkPhysicalDevice *phys = calloc(count ? count : 1, sizeof *phys);
	if (!phys || d->vkEnumeratePhysicalDevices(d->instance, &count, phys) < 0) {
		free(phys);
		snprintf(err, errlen, "no Vulkan device");
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
	// An integrated GPU first: it is awake already, a discrete one may sleep.
	if (pick == HWVK_PICK_ONLY) {
		snprintf(err, errlen, "no Vulkan device %04x:%04x", vendor, device);
	} else if (pick == HWVK_PICK_OTHERS) {
		snprintf(err, errlen, "no Vulkan device but %04x:%04x", vendor, device);
	} else {
		snprintf(err, errlen, "no Vulkan device");
	}
	static const VkPhysicalDeviceType order[] = {
		VK_PHYSICAL_DEVICE_TYPE_INTEGRATED_GPU,
		VK_PHYSICAL_DEVICE_TYPE_DISCRETE_GPU,
		VK_PHYSICAL_DEVICE_TYPE_VIRTUAL_GPU,
		VK_PHYSICAL_DEVICE_TYPE_OTHER,
		VK_PHYSICAL_DEVICE_TYPE_CPU,
	};
	for (size_t o = 0; o < sizeof order / sizeof *order && !d->phys; o++) {
		for (uint32_t i = 0; i < count && !d->phys; i++) {
			VkPhysicalDeviceProperties props;
			d->vkGetPhysicalDeviceProperties(phys[i], &props);
			int same = props.vendorID == vendor && props.deviceID == device;
			if ((pick == HWVK_PICK_ONLY && !same) || (pick == HWVK_PICK_OTHERS && same)) {
				continue;
			}
			if (props.deviceType != order[o]) {
				continue;
			}
			if (video ? suitable(d, phys[i], err, errlen) : compute_suitable(d, phys[i], err, errlen)) {
				d->phys = phys[i];
				snprintf(d->name, sizeof d->name, "%s", props.deviceName);
			}
		}
	}
	free(phys);
	if (!d->phys) {
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
	d->vkGetPhysicalDeviceMemoryProperties(d->phys, &d->memory);
	// Each family once: the decoder's, the copies', the scaler's.
	int64_t wanted[3] = {d->decode_family, d->transfer_family, d->compute_family};
	for (int i = 0; i < 3; i++) {
		int seen = wanted[i] < 0;
		for (uint32_t j = 0; j < d->nfamilies && !seen; j++) {
			seen = d->families[j] == (uint32_t)wanted[i];
		}
		if (!seen) {
			d->families[d->nfamilies++] = (uint32_t)wanted[i];
		}
	}
	float priority = 1.0f;
	VkDeviceQueueCreateInfo queues[3];
	for (uint32_t i = 0; i < d->nfamilies; i++) {
		queues[i] = (VkDeviceQueueCreateInfo){.sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO, .queueFamilyIndex = d->families[i], .queueCount = 1, .pQueuePriorities = &priority};
	}
	VkPhysicalDeviceVulkan13Features features13 = {
		.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VULKAN_1_3_FEATURES,
		.synchronization2 = VK_TRUE,
	};
	VkDeviceCreateInfo di = {
		.sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO,
		.pNext = &features13,
		.queueCreateInfoCount = d->nfamilies,
		.pQueueCreateInfos = queues,
		.enabledExtensionCount = video ? sizeof device_extensions / sizeof *device_extensions : 0,
		.ppEnabledExtensionNames = video ? device_extensions : NULL,
	};
	r = d->vkCreateDevice(d->phys, &di, NULL, &d->device);
	if (r != VK_SUCCESS) {
		d->device = NULL;
		vk_fail(err, errlen, "vkCreateDevice", r);
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
#define X(f) \
	d->f = (PFN_##f)d->vkGetDeviceProcAddr(d->device, #f); \
	if (!d->f) { \
		snprintf(err, errlen, "no %s", #f); \
		hwvk_device_close(d); \
		return HWVK_UNSUPPORTED; \
	}
	DEVICE_FUNCS(X)
	if (video) {
		VIDEO_DEVICE_FUNCS(X)
	}
#undef X
	d->vkGetDeviceQueue(d->device, d->decode_family, 0, &d->decode_queue);
	d->vkGetDeviceQueue(d->device, d->transfer_family, 0, &d->transfer_queue);
	int status = command_pool(d, d->decode_family, &d->decode_pool, &d->decode_cmd, err, errlen);
	if (status == HWVK_OK && d->transfer_family != d->decode_family) {
		status = command_pool(d, d->transfer_family, &d->transfer_pool, &d->transfer_cmd, err, errlen);
	} else if (status == HWVK_OK) {
		d->transfer_pool = d->decode_pool;
		d->transfer_cmd = d->decode_cmd;
	}
	if (status == HWVK_OK && d->compute_family >= 0) {
		d->vkGetDeviceQueue(d->device, (uint32_t)d->compute_family, 0, &d->compute_queue);
		if ((uint32_t)d->compute_family == d->transfer_family) {
			d->compute_pool = d->transfer_pool;
			d->compute_cmd = d->transfer_cmd;
		} else if ((uint32_t)d->compute_family == d->decode_family) {
			d->compute_pool = d->decode_pool;
			d->compute_cmd = d->decode_cmd;
		} else {
			status = command_pool(d, (uint32_t)d->compute_family, &d->compute_pool, &d->compute_cmd, err, errlen);
		}
	}
	if (status == HWVK_OK) {
		VkFenceCreateInfo fi = {.sType = VK_STRUCTURE_TYPE_FENCE_CREATE_INFO};
		r = d->vkCreateFence(d->device, &fi, NULL, &d->fence);
		if (r != VK_SUCCESS) {
			status = vk_fail(err, errlen, "vkCreateFence", r);
		}
	}
	if (status != HWVK_OK) {
		hwvk_device_close(d);
		return status;
	}
	scaler_create(d);
	if (!video && !d->pipeline) {
		snprintf(err, errlen, "%s", d->scale_why);
		hwvk_device_close(d);
		return HWVK_ERROR;
	}
	*out = d;
	return HWVK_OK;
}

int hwvk_device_open(hwvk_device **out, uint32_t vendor, uint32_t device, int pick, char *err, size_t errlen) {
	return device_open(out, vendor, device, pick, 1, err, errlen);
}

int hwvk_compute_open(hwvk_device **out, char *err, size_t errlen) {
	return device_open(out, 0, 0, HWVK_PICK_ANY, 0, err, errlen);
}

// The memory type of bits that has want, best with prefer and without avoid, or -1.
static int memory_type(hwvk_device *d, uint32_t bits, VkMemoryPropertyFlags want, VkMemoryPropertyFlags prefer, VkMemoryPropertyFlags avoid) {
	int found = -1, best = -1;
	for (uint32_t i = 0; i < d->memory.memoryTypeCount; i++) {
		VkMemoryPropertyFlags flags = d->memory.memoryTypes[i].propertyFlags;
		if (!(bits & (1u << i)) || (flags & want) != want) {
			continue;
		}
		int score = ((flags & prefer) == prefer ? 2 : 0) + (avoid && !(flags & avoid) ? 1 : 0);
		if (score > best) {
			found = (int)i;
			best = score;
		}
	}
	return found;
}

static VkResult allocate(hwvk_device *d, VkMemoryRequirements req, VkMemoryPropertyFlags want, VkMemoryPropertyFlags prefer, VkMemoryPropertyFlags avoid, VkDeviceMemory *mem) {
	int t = memory_type(d, req.memoryTypeBits, want, prefer, avoid);
	if (t < 0) {
		return VK_ERROR_OUT_OF_DEVICE_MEMORY;
	}
	VkMemoryAllocateInfo ai = {
		.sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
		.allocationSize = req.size,
		.memoryTypeIndex = (uint32_t)t,
	};
	return d->vkAllocateMemory(d->device, &ai, NULL, mem);
}

// Makes a host-visible buffer of size bytes, mapped.
// A buffer mapped for the host; host_reads for one the CPU reads, which
// must be cached and not across the bus in the GPU's memory to be read fast.
static VkResult host_buffer(hwvk_device *d, VkDeviceSize size, VkBufferUsageFlags usage, const void *next, int host_reads, VkBuffer *buf, VkDeviceMemory *mem, uint8_t **map) {
	VkBufferCreateInfo bi = {
		.sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
		.pNext = next,
		.size = size,
		.usage = usage,
		.sharingMode = d->nfamilies == 1 ? VK_SHARING_MODE_EXCLUSIVE : VK_SHARING_MODE_CONCURRENT,
		.queueFamilyIndexCount = d->nfamilies,
		.pQueueFamilyIndices = d->families,
	};
	VkResult r = d->vkCreateBuffer(d->device, &bi, NULL, buf);
	if (r != VK_SUCCESS) {
		*buf = VK_NULL_HANDLE;
		return r;
	}
	VkMemoryRequirements req;
	d->vkGetBufferMemoryRequirements(d->device, *buf, &req);
	VkMemoryPropertyFlags want = VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT | VK_MEMORY_PROPERTY_HOST_COHERENT_BIT;
	VkMemoryPropertyFlags avoid = host_reads ? VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT : 0;
	if (host_reads) {
		int t = memory_type(d, req.memoryTypeBits, want, VK_MEMORY_PROPERTY_HOST_CACHED_BIT, avoid);
		d->readback_flags = t < 0 ? 0 : d->memory.memoryTypes[t].propertyFlags;
	}
	r = allocate(d, req, want, VK_MEMORY_PROPERTY_HOST_CACHED_BIT, avoid, mem);
	if (r != VK_SUCCESS) {
		*mem = VK_NULL_HANDLE;
		return r;
	}
	r = d->vkBindBufferMemory(d->device, *buf, *mem, 0);
	if (r != VK_SUCCESS) {
		return r;
	}
	return d->vkMapMemory(d->device, *mem, 0, VK_WHOLE_SIZE, 0, (void **)map);
}

static void free_buffer(hwvk_device *d, VkBuffer *buf, VkDeviceMemory *mem, uint8_t **map) {
	if (map && *map) {
		d->vkUnmapMemory(d->device, *mem);
		*map = NULL;
	}
	if (*buf) {
		d->vkDestroyBuffer(d->device, *buf, NULL);
		*buf = VK_NULL_HANDLE;
	}
	if (*mem) {
		d->vkFreeMemory(d->device, *mem, NULL);
		*mem = VK_NULL_HANDLE;
	}
}

static void target_free(hwvk_device *d, scale_target *t) {
	free_buffer(d, &t->work, &t->work_memory, NULL);
	free_buffer(d, &t->out, &t->out_memory, &t->out_map);
	if (t->pool) {
		d->vkDestroyDescriptorPool(d->device, t->pool, NULL);
	}
	memset(t, 0, sizeof *t);
}

static void session_free(hwvk_session *s) {
	hwvk_device *d = s->d;
	free_buffer(d, &s->bitstream, &s->bitstream_memory, &s->bitstream_map);
	free_buffer(d, &s->readback, &s->readback_memory, &s->readback_map);
	if (s->params) {
		d->vkDestroyVideoSessionParametersKHR(d->device, s->params, NULL);
	}
	if (s->session) {
		d->vkDestroyVideoSessionKHR(d->device, s->session, NULL);
	}
	for (uint32_t i = 0; s->session_memory && i < s->session_memory_count; i++) {
		if (s->session_memory[i]) {
			d->vkFreeMemory(d->device, s->session_memory[i], NULL);
		}
	}
	for (uint32_t i = 0; s->views && i < s->surfaces; i++) {
		if (s->views[i]) {
			d->vkDestroyImageView(d->device, s->views[i], NULL);
		}
	}
	if (s->image) {
		d->vkDestroyImage(d->device, s->image, NULL);
	}
	if (s->image_memory) {
		d->vkFreeMemory(d->device, s->image_memory, NULL);
	}
	free(s->session_memory);
	free(s->views);
	free(s->layouts);
	free(s->slot_of);
	free(s);
}

void hwvk_session_close(hwvk_session *s) {
	if (!s) {
		return;
	}
	hwvk_device *d = s->d;
	pthread_mutex_lock(&d->mu);
	session_free(s);
	pthread_mutex_unlock(&d->mu);
}

void hwvk_describe(hwvk_device *d, char *name, size_t namelen, int *cached, int *device_local, int *scales, char *why, size_t whylen) {
	pthread_mutex_lock(&d->mu);
	snprintf(name, namelen, "%s", d->name);
	*scales = d->pipeline != VK_NULL_HANDLE;
	snprintf(why, whylen, "%s", d->scale_why);
	*cached = (d->readback_flags & VK_MEMORY_PROPERTY_HOST_CACHED_BIT) != 0;
	*device_local = (d->readback_flags & VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT) != 0;
	pthread_mutex_unlock(&d->mu);
}

void hwvk_session_reset(hwvk_session *s) {
	if (!s) {
		return;
	}
	pthread_mutex_lock(&s->d->mu);
	s->needs_reset = 1;
	for (uint32_t i = 0; i < s->surfaces; i++) {
		s->slot_of[i] = -1;
	}
	pthread_mutex_unlock(&s->d->mu);
}

// Maps a level_idc of the stream to Vulkan's StdVideoH264LevelIdc.
static int level_idc(uint32_t level, StdVideoH264LevelIdc *out) {
	static const uint32_t levels[] = {10, 11, 12, 13, 20, 21, 22, 30, 31, 32, 40, 41, 42, 50, 51, 52, 60, 61, 62};
	if (level == 9) {
		level = 11;
	}
	for (size_t i = 0; i < sizeof levels / sizeof *levels; i++) {
		if (levels[i] == level) {
			*out = (StdVideoH264LevelIdc)i;
			return 1;
		}
	}
	return 0;
}

static int session_create(hwvk_session *s, uint32_t level, char *err, size_t errlen) {
	hwvk_device *d = s->d;
	VkVideoCapabilitiesKHR *caps = &d->caps;
	StdVideoH264LevelIdc idc;
	if (!level_idc(level, &idc) || idc > d->h264_caps.maxLevelIdc) {
		snprintf(err, errlen, "level %u", level);
		return HWVK_UNSUPPORTED;
	}
	if (s->coded_w < caps->minCodedExtent.width || s->coded_h < caps->minCodedExtent.height || s->coded_w > caps->maxCodedExtent.width || s->coded_h > caps->maxCodedExtent.height) {
		snprintf(err, errlen, "%ux%u, out of %ux%u to %ux%u", s->coded_w, s->coded_h, caps->minCodedExtent.width, caps->minCodedExtent.height, caps->maxCodedExtent.width, caps->maxCodedExtent.height);
		return HWVK_UNSUPPORTED;
	}
	if (s->slots > caps->maxDpbSlots || s->max_refs > caps->maxActiveReferencePictures) {
		snprintf(err, errlen, "%u references, of %u slots and %u references", s->max_refs, caps->maxDpbSlots, caps->maxActiveReferencePictures);
		return HWVK_UNSUPPORTED;
	}
	VkExtent2D extent = {
		(uint32_t)align_up(s->coded_w, caps->pictureAccessGranularity.width),
		(uint32_t)align_up(s->coded_h, caps->pictureAccessGranularity.height),
	};
	if (extent.width > caps->maxCodedExtent.width || extent.height > caps->maxCodedExtent.height) {
		extent.width = s->coded_w;
		extent.height = s->coded_h;
	}
	VkImageCreateInfo ii = {
		.sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO,
		.pNext = &d->profile_list,
		.flags = d->format.imageCreateFlags,
		.imageType = d->format.imageType,
		.format = d->format.format,
		.extent = {extent.width, extent.height, 1},
		.mipLevels = 1,
		.arrayLayers = s->surfaces,
		.samples = VK_SAMPLE_COUNT_1_BIT,
		.tiling = d->format.imageTiling,
		.usage = VK_IMAGE_USAGE_VIDEO_DECODE_DPB_BIT_KHR | VK_IMAGE_USAGE_VIDEO_DECODE_DST_BIT_KHR | VK_IMAGE_USAGE_TRANSFER_SRC_BIT,
		.sharingMode = d->nfamilies == 1 ? VK_SHARING_MODE_EXCLUSIVE : VK_SHARING_MODE_CONCURRENT,
		.queueFamilyIndexCount = d->nfamilies,
		.pQueueFamilyIndices = d->families,
		.initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
	};
	VkResult r = d->vkCreateImage(d->device, &ii, NULL, &s->image);
	if (r != VK_SUCCESS) {
		s->image = VK_NULL_HANDLE;
		return vk_fail(err, errlen, "vkCreateImage", r);
	}
	VkMemoryRequirements req;
	d->vkGetImageMemoryRequirements(d->device, s->image, &req);
	r = allocate(d, req, VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT, 0, 0, &s->image_memory);
	if (r == VK_SUCCESS) {
		r = d->vkBindImageMemory(d->device, s->image, s->image_memory, 0);
	} else {
		s->image_memory = VK_NULL_HANDLE;
	}
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "image memory", r);
	}
	for (uint32_t i = 0; i < s->surfaces; i++) {
		VkImageViewUsageCreateInfo usage = {
			.sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_USAGE_CREATE_INFO,
			.usage = VK_IMAGE_USAGE_VIDEO_DECODE_DPB_BIT_KHR | VK_IMAGE_USAGE_VIDEO_DECODE_DST_BIT_KHR,
		};
		VkImageViewCreateInfo vi = {
			.sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO,
			.pNext = &usage,
			.image = s->image,
			.viewType = VK_IMAGE_VIEW_TYPE_2D,
			.format = d->format.format,
			.components = d->format.componentMapping,
			.subresourceRange = {VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, i, 1},
		};
		r = d->vkCreateImageView(d->device, &vi, NULL, &s->views[i]);
		if (r != VK_SUCCESS) {
			s->views[i] = VK_NULL_HANDLE;
			return vk_fail(err, errlen, "vkCreateImageView", r);
		}
		s->layouts[i] = VK_IMAGE_LAYOUT_UNDEFINED;
	}
	VkVideoSessionCreateInfoKHR si = {
		.sType = VK_STRUCTURE_TYPE_VIDEO_SESSION_CREATE_INFO_KHR,
		.queueFamilyIndex = d->decode_family,
		.pVideoProfile = &d->profile,
		.pictureFormat = d->format.format,
		.maxCodedExtent = extent,
		.referencePictureFormat = d->format.format,
		.maxDpbSlots = s->slots,
		.maxActiveReferencePictures = caps->maxActiveReferencePictures < 16 ? caps->maxActiveReferencePictures : 16,
		.pStdHeaderVersion = &caps->stdHeaderVersion,
	};
	r = d->vkCreateVideoSessionKHR(d->device, &si, NULL, &s->session);
	if (r != VK_SUCCESS) {
		s->session = VK_NULL_HANDLE;
		snprintf(err, errlen, "vkCreateVideoSessionKHR: VkResult %d", (int)r);
		return r == VK_ERROR_DEVICE_LOST ? HWVK_LOST : HWVK_UNSUPPORTED;
	}
	uint32_t n = 0;
	d->vkGetVideoSessionMemoryRequirementsKHR(d->device, s->session, &n, NULL);
	VkVideoSessionMemoryRequirementsKHR *mr = calloc(n ? n : 1, sizeof *mr);
	VkBindVideoSessionMemoryInfoKHR *bind = calloc(n ? n : 1, sizeof *bind);
	s->session_memory = calloc(n ? n : 1, sizeof *s->session_memory);
	s->session_memory_count = n;
	if (!mr || !bind || !s->session_memory) {
		free(mr);
		free(bind);
		snprintf(err, errlen, "out of memory");
		return HWVK_ERROR;
	}
	for (uint32_t i = 0; i < n; i++) {
		mr[i].sType = VK_STRUCTURE_TYPE_VIDEO_SESSION_MEMORY_REQUIREMENTS_KHR;
	}
	d->vkGetVideoSessionMemoryRequirementsKHR(d->device, s->session, &n, mr);
	r = VK_SUCCESS;
	for (uint32_t i = 0; i < n && r == VK_SUCCESS; i++) {
		r = allocate(d, mr[i].memoryRequirements, 0, VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT, 0, &s->session_memory[i]);
		bind[i] = (VkBindVideoSessionMemoryInfoKHR){
			.sType = VK_STRUCTURE_TYPE_BIND_VIDEO_SESSION_MEMORY_INFO_KHR,
			.memoryBindIndex = mr[i].memoryBindIndex,
			.memory = s->session_memory[i],
			.memoryOffset = 0,
			.memorySize = mr[i].memoryRequirements.size,
		};
	}
	if (r == VK_SUCCESS && n > 0) {
		r = d->vkBindVideoSessionMemoryKHR(d->device, s->session, n, bind);
	}
	free(mr);
	free(bind);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "video session memory", r);
	}
	r = host_buffer(d, (VkDeviceSize)s->coded_w * s->coded_h * 3 / 2, VK_BUFFER_USAGE_TRANSFER_DST_BIT, NULL, 1, &s->readback, &s->readback_memory, &s->readback_map);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "readback buffer", r);
	}
	return HWVK_OK;
}

int hwvk_session_open(hwvk_device *d, uint32_t width_mbs, uint32_t height_mbs, uint32_t level, uint32_t refs, uint32_t surfaces, hwvk_session **out, char *err, size_t errlen) {
	*out = NULL;
	if (surfaces == 0 || surfaces > 64 || refs == 0 || refs > 16) {
		snprintf(err, errlen, "%u surfaces and %u references", surfaces, refs);
		return HWVK_UNSUPPORTED;
	}
	hwvk_session *s = calloc(1, sizeof *s);
	if (!s) {
		snprintf(err, errlen, "out of memory");
		return HWVK_ERROR;
	}
	s->d = d;
	s->coded_w = width_mbs * 16;
	s->coded_h = height_mbs * 16;
	s->surfaces = surfaces;
	s->max_refs = refs;
	s->slots = refs + 1;
	s->needs_reset = 1;
	s->views = calloc(surfaces, sizeof *s->views);
	s->layouts = calloc(surfaces, sizeof *s->layouts);
	s->slot_of = malloc(surfaces * sizeof *s->slot_of);
	for (uint32_t i = 0; s->slot_of && i < surfaces; i++) {
		s->slot_of[i] = -1;
	}
	pthread_mutex_lock(&d->mu);
	int status;
	if (d->lost) {
		snprintf(err, errlen, "the device is lost");
		status = HWVK_LOST;
	} else if (!s->views || !s->layouts || !s->slot_of) {
		snprintf(err, errlen, "out of memory");
		status = HWVK_ERROR;
	} else {
		status = session_create(s, level, err, errlen);
	}
	if (status != HWVK_OK) {
		session_free(s);
	} else {
		*out = s;
	}
	pthread_mutex_unlock(&d->mu);
	return status;
}

static void scaling_lists(StdVideoH264ScalingLists *l, const uint8_t s4[6][16], const uint8_t s8[2][64]) {
	memset(l, 0, sizeof *l);
	// Every list is given as it applies, after the stream's fall-backs: none falls back again.
	l->scaling_list_present_mask = 0xff;
	l->use_default_scaling_matrix_mask = 0;
	for (int i = 0; i < 6; i++) {
		memcpy(l->ScalingList4x4[i], s4[i], 16);
	}
	for (int i = 0; i < STD_VIDEO_H264_SCALING_LIST_8X8_NUM_LISTS; i++) {
		memcpy(l->ScalingList8x8[i], s8[i < 2 ? i : i % 2], 64);
	}
}

int hwvk_session_parameters(hwvk_session *s, const hwvk_sps *sps, uint32_t nsps, const hwvk_pps *pps, uint32_t npps, char *err, size_t errlen) {
	hwvk_device *d = s->d;
	if (nsps == 0 || nsps > MAX_SPS || npps == 0 || npps > MAX_PPS) {
		snprintf(err, errlen, "%u and %u parameter sets", nsps, npps);
		return HWVK_ERROR;
	}
	StdVideoH264SequenceParameterSet *vs = calloc(nsps, sizeof *vs);
	StdVideoH264ScalingLists *vsl = calloc(nsps, sizeof *vsl);
	StdVideoH264PictureParameterSet *vp = calloc(npps, sizeof *vp);
	StdVideoH264ScalingLists *vpl = calloc(npps, sizeof *vpl);
	int status = HWVK_OK;
	if (!vs || !vsl || !vp || !vpl) {
		snprintf(err, errlen, "out of memory");
		status = HWVK_ERROR;
	}
	for (uint32_t i = 0; status == HWVK_OK && i < nsps; i++) {
		const hwvk_sps *in = &sps[i];
		StdVideoH264SequenceParameterSet *o = &vs[i];
		StdVideoH264LevelIdc idc;
		if (!level_idc(in->level, &idc)) {
			snprintf(err, errlen, "level %u", in->level);
			status = HWVK_UNSUPPORTED;
			break;
		}
		o->flags.constraint_set0_flag = in->constraints >> 7 & 1;
		o->flags.constraint_set1_flag = in->constraints >> 6 & 1;
		o->flags.constraint_set2_flag = in->constraints >> 5 & 1;
		o->flags.constraint_set3_flag = in->constraints >> 4 & 1;
		o->flags.constraint_set4_flag = in->constraints >> 3 & 1;
		o->flags.constraint_set5_flag = in->constraints >> 2 & 1;
		o->flags.direct_8x8_inference_flag = in->direct_8x8;
		o->flags.frame_mbs_only_flag = 1;
		o->flags.delta_pic_order_always_zero_flag = in->delta_pic_order_always_zero;
		o->flags.gaps_in_frame_num_value_allowed_flag = in->gaps_allowed;
		o->flags.frame_cropping_flag = (in->crop_left | in->crop_right | in->crop_top | in->crop_bottom) != 0;
		o->flags.seq_scaling_matrix_present_flag = 1;
		o->profile_idc = (StdVideoH264ProfileIdc)in->profile;
		o->level_idc = idc;
		o->chroma_format_idc = STD_VIDEO_H264_CHROMA_FORMAT_IDC_420;
		o->seq_parameter_set_id = in->id;
		o->log2_max_frame_num_minus4 = in->log2_max_frame_num_minus4;
		o->pic_order_cnt_type = (StdVideoH264PocType)in->poc_type;
		o->offset_for_non_ref_pic = in->offset_for_non_ref_pic;
		o->offset_for_top_to_bottom_field = in->offset_for_top_to_bottom_field;
		o->log2_max_pic_order_cnt_lsb_minus4 = in->log2_max_poc_lsb_minus4;
		o->num_ref_frames_in_pic_order_cnt_cycle = (uint8_t)in->num_ref_frames_in_poc_cycle;
		o->max_num_ref_frames = in->max_num_ref_frames;
		o->pic_width_in_mbs_minus1 = in->width_mbs - 1;
		o->pic_height_in_map_units_minus1 = in->height_mbs - 1;
		o->frame_crop_left_offset = in->crop_left;
		o->frame_crop_right_offset = in->crop_right;
		o->frame_crop_top_offset = in->crop_top;
		o->frame_crop_bottom_offset = in->crop_bottom;
		o->pOffsetForRefFrame = in->offset_for_ref_frame;
		scaling_lists(&vsl[i], in->scaling4, in->scaling8);
		o->pScalingLists = &vsl[i];
	}
	for (uint32_t i = 0; status == HWVK_OK && i < npps; i++) {
		const hwvk_pps *in = &pps[i];
		StdVideoH264PictureParameterSet *o = &vp[i];
		o->flags.transform_8x8_mode_flag = in->transform_8x8;
		o->flags.redundant_pic_cnt_present_flag = in->redundant_pic_cnt_present;
		o->flags.constrained_intra_pred_flag = in->constrained_intra_pred;
		o->flags.deblocking_filter_control_present_flag = in->deblocking_filter_control_present;
		o->flags.weighted_pred_flag = in->weighted_pred;
		o->flags.bottom_field_pic_order_in_frame_present_flag = in->bottom_field_pic_order_present;
		o->flags.entropy_coding_mode_flag = in->entropy_coding_mode;
		o->flags.pic_scaling_matrix_present_flag = 1;
		o->seq_parameter_set_id = in->sps_id;
		o->pic_parameter_set_id = in->id;
		o->num_ref_idx_l0_default_active_minus1 = in->num_ref_idx_l0_minus1;
		o->num_ref_idx_l1_default_active_minus1 = in->num_ref_idx_l1_minus1;
		o->weighted_bipred_idc = (StdVideoH264WeightedBipredIdc)in->weighted_bipred_idc;
		o->pic_init_qp_minus26 = in->init_qp_minus26;
		o->pic_init_qs_minus26 = in->init_qs_minus26;
		o->chroma_qp_index_offset = in->chroma_qp_index_offset;
		o->second_chroma_qp_index_offset = in->second_chroma_qp_index_offset;
		scaling_lists(&vpl[i], in->scaling4, in->scaling8);
		o->pScalingLists = &vpl[i];
	}
	if (status == HWVK_OK) {
		VkVideoDecodeH264SessionParametersAddInfoKHR add = {
			.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_H264_SESSION_PARAMETERS_ADD_INFO_KHR,
			.stdSPSCount = nsps,
			.pStdSPSs = vs,
			.stdPPSCount = npps,
			.pStdPPSs = vp,
		};
		VkVideoDecodeH264SessionParametersCreateInfoKHR h264 = {
			.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_H264_SESSION_PARAMETERS_CREATE_INFO_KHR,
			.maxStdSPSCount = MAX_SPS,
			.maxStdPPSCount = MAX_PPS,
			.pParametersAddInfo = &add,
		};
		VkVideoSessionParametersCreateInfoKHR pi = {
			.sType = VK_STRUCTURE_TYPE_VIDEO_SESSION_PARAMETERS_CREATE_INFO_KHR,
			.pNext = &h264,
			.videoSession = s->session,
		};
		pthread_mutex_lock(&d->mu);
		VkVideoSessionParametersKHR params;
		VkResult r = d->lost ? VK_ERROR_DEVICE_LOST : d->vkCreateVideoSessionParametersKHR(d->device, &pi, NULL, &params);
		if (r == VK_SUCCESS) {
			if (s->params) {
				d->vkDestroyVideoSessionParametersKHR(d->device, s->params, NULL);
			}
			s->params = params;
		} else {
			status = vk_fail(err, errlen, "vkCreateVideoSessionParametersKHR", r);
		}
		pthread_mutex_unlock(&d->mu);
	}
	free(vs);
	free(vsl);
	free(vp);
	free(vpl);
	return status;
}

// Submits cmd on queue and waits for it; d->mu is held.
static int submit(hwvk_device *d, VkQueue queue, VkCommandBuffer cmd, char *err, size_t errlen) {
	VkSubmitInfo si = {
		.sType = VK_STRUCTURE_TYPE_SUBMIT_INFO,
		.commandBufferCount = 1,
		.pCommandBuffers = &cmd,
	};
	VkResult r = d->vkQueueSubmit(queue, 1, &si, d->fence);
	if (r == VK_SUCCESS) {
		r = d->vkWaitForFences(d->device, 1, &d->fence, VK_TRUE, WAIT_NS);
		if (r == VK_TIMEOUT) {
			// A device that hangs is as good as lost, and its fence may never come.
			d->lost = 1;
			snprintf(err, errlen, "the device did not finish in %llu s", WAIT_NS / 1000000000ull);
			return HWVK_LOST;
		}
		d->vkResetFences(d->device, 1, &d->fence);
	}
	if (r != VK_SUCCESS) {
		int status = vk_fail(err, errlen, "submit", r);
		if (status == HWVK_LOST) {
			d->lost = 1;
		}
		return status;
	}
	return HWVK_OK;
}

static void image_barrier(hwvk_device *d, VkCommandBuffer cmd, VkImage image, uint32_t layer, VkImageLayout from, VkImageLayout to, VkPipelineStageFlags2 src_stage, VkAccessFlags2 src_access, VkPipelineStageFlags2 dst_stage, VkAccessFlags2 dst_access) {
	VkImageMemoryBarrier2 b = {
		.sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER_2,
		.srcStageMask = src_stage,
		.srcAccessMask = src_access,
		.dstStageMask = dst_stage,
		.dstAccessMask = dst_access,
		.oldLayout = from,
		.newLayout = to,
		.srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
		.dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
		.image = image,
		.subresourceRange = {VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, layer, 1},
	};
	VkDependencyInfo dep = {
		.sType = VK_STRUCTURE_TYPE_DEPENDENCY_INFO,
		.imageMemoryBarrierCount = 1,
		.pImageMemoryBarriers = &b,
	};
	d->vkCmdPipelineBarrier2(cmd, &dep);
}

static int bitstream_room(hwvk_session *s, VkDeviceSize size, char *err, size_t errlen) {
	if (size <= s->bitstream_size) {
		return HWVK_OK;
	}
	hwvk_device *d = s->d;
	free_buffer(d, &s->bitstream, &s->bitstream_memory, &s->bitstream_map);
	VkDeviceSize grown = align_up(size < (1 << 20) ? (1 << 20) : size * 2, d->caps.minBitstreamBufferSizeAlignment);
	VkResult r = host_buffer(d, grown, VK_BUFFER_USAGE_VIDEO_DECODE_SRC_BIT_KHR, &d->profile_list, 0, &s->bitstream, &s->bitstream_memory, &s->bitstream_map);
	if (r != VK_SUCCESS) {
		free_buffer(d, &s->bitstream, &s->bitstream_memory, &s->bitstream_map);
		s->bitstream_size = 0;
		return vk_fail(err, errlen, "bitstream buffer", r);
	}
	s->bitstream_size = grown;
	return HWVK_OK;
}

static int decode_locked(hwvk_session *s, const hwvk_pic *p, const uint8_t *data, size_t size, const uint32_t *offsets, uint32_t nslices, char *err, size_t errlen) {
	hwvk_device *d = s->d;
	if (d->lost) {
		snprintf(err, errlen, "the device is lost");
		return HWVK_LOST;
	}
	if (!s->params) {
		snprintf(err, errlen, "no parameter sets");
		return HWVK_ERROR;
	}
	if (p->surface < 0 || (uint32_t)p->surface >= s->surfaces || p->nrefs > 16 || p->nrefs > s->max_refs || nslices == 0) {
		snprintf(err, errlen, "picture in surface %d with %u references and %u slices", p->surface, p->nrefs, nslices);
		return HWVK_ERROR;
	}
	for (uint32_t i = 0; i < p->nrefs; i++) {
		int32_t ref = p->refs[i].surface;
		if (ref < 0 || (uint32_t)ref >= s->surfaces || ref == p->surface || s->layouts[ref] != VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR || s->slot_of[ref] < 0) {
			snprintf(err, errlen, "reference in surface %d", ref);
			return HWVK_ERROR;
		}
	}
	// Every reference of the stream is among the picture's: the slots of
	// the other surfaces are free, and the picture takes one of them.
	uint8_t taken[17] = {0};
	for (uint32_t i = 0; i < p->nrefs; i++) {
		taken[s->slot_of[p->refs[i].surface]] = 1;
	}
	int32_t setup = -1;
	for (uint32_t i = 0; i < s->slots && setup < 0; i++) {
		if (!taken[i]) {
			setup = (int32_t)i;
		}
	}
	if (setup < 0) {
		snprintf(err, errlen, "no DPB slot left of %u", s->slots);
		return HWVK_ERROR;
	}
	VkDeviceSize range = align_up(size, d->caps.minBitstreamBufferSizeAlignment);
	int status = bitstream_room(s, range, err, errlen);
	if (status != HWVK_OK) {
		return status;
	}
	memcpy(s->bitstream_map, data, size);
	memset(s->bitstream_map + size, 0, range - size);

	StdVideoDecodeH264PictureInfo std = {0};
	std.flags.is_intra = p->intra;
	std.flags.IdrPicFlag = p->idr;
	std.flags.is_reference = p->reference;
	std.seq_parameter_set_id = p->sps_id;
	std.pic_parameter_set_id = p->pps_id;
	std.frame_num = (uint16_t)p->frame_num;
	std.idr_pic_id = (uint16_t)p->idr_pic_id;
	std.PicOrderCnt[0] = p->poc[0];
	std.PicOrderCnt[1] = p->poc[1];
	VkVideoDecodeH264PictureInfoKHR h264 = {
		.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_H264_PICTURE_INFO_KHR,
		.pStdPictureInfo = &std,
		.sliceCount = nslices,
		.pSliceOffsets = offsets,
	};

	// The references, then the picture itself as the slot it sets up.
	StdVideoDecodeH264ReferenceInfo ref_std[17];
	VkVideoDecodeH264DpbSlotInfoKHR ref_slot[17];
	VkVideoPictureResourceInfoKHR ref_res[17];
	VkVideoReferenceSlotInfoKHR refs[17];
	VkVideoReferenceSlotInfoKHR bound[17];
	uint32_t n = p->nrefs;
	for (uint32_t i = 0; i <= n; i++) {
		int32_t surface = i < n ? p->refs[i].surface : p->surface;
		memset(&ref_std[i], 0, sizeof ref_std[i]);
		if (i < n) {
			ref_std[i].flags.used_for_long_term_reference = p->refs[i].long_term;
			ref_std[i].FrameNum = (uint16_t)p->refs[i].frame_num;
			ref_std[i].PicOrderCnt[0] = p->refs[i].poc[0];
			ref_std[i].PicOrderCnt[1] = p->refs[i].poc[1];
		} else {
			ref_std[i].FrameNum = (uint16_t)p->frame_num;
			ref_std[i].PicOrderCnt[0] = p->poc[0];
			ref_std[i].PicOrderCnt[1] = p->poc[1];
		}
		ref_slot[i] = (VkVideoDecodeH264DpbSlotInfoKHR){
			.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_H264_DPB_SLOT_INFO_KHR,
			.pStdReferenceInfo = &ref_std[i],
		};
		ref_res[i] = (VkVideoPictureResourceInfoKHR){
			.sType = VK_STRUCTURE_TYPE_VIDEO_PICTURE_RESOURCE_INFO_KHR,
			.codedOffset = {0, 0},
			.codedExtent = {s->coded_w, s->coded_h},
			.baseArrayLayer = 0,
			.imageViewBinding = s->views[surface],
		};
		refs[i] = (VkVideoReferenceSlotInfoKHR){
			.sType = VK_STRUCTURE_TYPE_VIDEO_REFERENCE_SLOT_INFO_KHR,
			.pNext = &ref_slot[i],
			.slotIndex = i < n ? s->slot_of[surface] : setup,
			.pPictureResource = &ref_res[i],
		};
		bound[i] = refs[i];
	}
	// The slot the picture sets up is bound without one: it is not active yet.
	bound[n].slotIndex = -1;

	VkCommandBuffer cmd = d->decode_cmd;
	VkCommandBufferBeginInfo bi = {
		.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
		.flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
	};
	VkResult r = d->vkBeginCommandBuffer(cmd, &bi);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkBeginCommandBuffer", r);
	}
	if (s->needs_reset) {
		VkVideoBeginCodingInfoKHR begin = {
			.sType = VK_STRUCTURE_TYPE_VIDEO_BEGIN_CODING_INFO_KHR,
			.videoSession = s->session,
			.videoSessionParameters = s->params,
		};
		d->vkCmdBeginVideoCodingKHR(cmd, &begin);
		VkVideoCodingControlInfoKHR control = {
			.sType = VK_STRUCTURE_TYPE_VIDEO_CODING_CONTROL_INFO_KHR,
			.flags = VK_VIDEO_CODING_CONTROL_RESET_BIT_KHR,
		};
		d->vkCmdControlVideoCodingKHR(cmd, &control);
		VkVideoEndCodingInfoKHR end = {.sType = VK_STRUCTURE_TYPE_VIDEO_END_CODING_INFO_KHR};
		d->vkCmdEndVideoCodingKHR(cmd, &end);
	}
	image_barrier(d, cmd, s->image, (uint32_t)p->surface, s->layouts[p->surface], VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR,
		VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT, VK_ACCESS_2_MEMORY_WRITE_BIT | VK_ACCESS_2_MEMORY_READ_BIT,
		VK_PIPELINE_STAGE_2_VIDEO_DECODE_BIT_KHR, VK_ACCESS_2_VIDEO_DECODE_WRITE_BIT_KHR | VK_ACCESS_2_VIDEO_DECODE_READ_BIT_KHR);
	VkVideoBeginCodingInfoKHR begin = {
		.sType = VK_STRUCTURE_TYPE_VIDEO_BEGIN_CODING_INFO_KHR,
		.videoSession = s->session,
		.videoSessionParameters = s->params,
		.referenceSlotCount = n + 1,
		.pReferenceSlots = bound,
	};
	d->vkCmdBeginVideoCodingKHR(cmd, &begin);
	VkVideoDecodeInfoKHR info = {
		.sType = VK_STRUCTURE_TYPE_VIDEO_DECODE_INFO_KHR,
		.pNext = &h264,
		.srcBuffer = s->bitstream,
		.srcBufferOffset = 0,
		.srcBufferRange = range,
		.dstPictureResource = ref_res[n],
		.pSetupReferenceSlot = &refs[n],
		.referenceSlotCount = n,
		.pReferenceSlots = n ? refs : NULL,
	};
	d->vkCmdDecodeVideoKHR(cmd, &info);
	VkVideoEndCodingInfoKHR end = {.sType = VK_STRUCTURE_TYPE_VIDEO_END_CODING_INFO_KHR};
	d->vkCmdEndVideoCodingKHR(cmd, &end);
	r = d->vkEndCommandBuffer(cmd);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkEndCommandBuffer", r);
	}
	status = submit(d, d->decode_queue, cmd, err, errlen);
	if (status == HWVK_OK) {
		s->needs_reset = 0;
		s->layouts[p->surface] = VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR;
		for (uint32_t i = 0; i < s->surfaces; i++) {
			int keep = 0;
			for (uint32_t j = 0; j < p->nrefs && !keep; j++) {
				keep = p->refs[j].surface == (int32_t)i;
			}
			if (!keep) {
				s->slot_of[i] = -1;
			}
		}
		s->slot_of[p->surface] = setup;
	}
	return status;
}

int hwvk_decode(hwvk_session *s, const hwvk_pic *p, const uint8_t *data, size_t size, const uint32_t *offsets, uint32_t nslices, char *err, size_t errlen) {
	pthread_mutex_lock(&s->d->mu);
	int status = decode_locked(s, p, data, size, offsets, nslices, err, errlen);
	pthread_mutex_unlock(&s->d->mu);
	return status;
}

static int read_locked(hwvk_session *s, uint32_t surface, char *err, size_t errlen) {
	hwvk_device *d = s->d;
	if (d->lost) {
		snprintf(err, errlen, "the device is lost");
		return HWVK_LOST;
	}
	if (surface >= s->surfaces || s->layouts[surface] != VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR) {
		snprintf(err, errlen, "surface %u holds no picture", surface);
		return HWVK_ERROR;
	}
	VkCommandBuffer cmd = d->transfer_cmd;
	VkCommandBufferBeginInfo bi = {
		.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
		.flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
	};
	VkResult r = d->vkBeginCommandBuffer(cmd, &bi);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkBeginCommandBuffer", r);
	}
	image_barrier(d, cmd, s->image, surface, VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
		VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT, VK_ACCESS_2_MEMORY_WRITE_BIT,
		VK_PIPELINE_STAGE_2_COPY_BIT, VK_ACCESS_2_TRANSFER_READ_BIT);
	VkBufferImageCopy regions[2] = {
		{
			.bufferOffset = 0,
			.bufferRowLength = s->coded_w,
			.bufferImageHeight = s->coded_h,
			.imageSubresource = {VK_IMAGE_ASPECT_PLANE_0_BIT, 0, surface, 1},
			.imageExtent = {s->coded_w, s->coded_h, 1},
		},
		{
			.bufferOffset = (VkDeviceSize)s->coded_w * s->coded_h,
			.bufferRowLength = s->coded_w / 2,
			.bufferImageHeight = s->coded_h / 2,
			.imageSubresource = {VK_IMAGE_ASPECT_PLANE_1_BIT, 0, surface, 1},
			.imageExtent = {s->coded_w / 2, s->coded_h / 2, 1},
		},
	};
	d->vkCmdCopyImageToBuffer(cmd, s->image, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, s->readback, 2, regions);
	image_barrier(d, cmd, s->image, surface, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR,
		VK_PIPELINE_STAGE_2_COPY_BIT, VK_ACCESS_2_TRANSFER_READ_BIT,
		VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT, 0);
	VkBufferMemoryBarrier2 host = {
		.sType = VK_STRUCTURE_TYPE_BUFFER_MEMORY_BARRIER_2,
		.srcStageMask = VK_PIPELINE_STAGE_2_COPY_BIT,
		.srcAccessMask = VK_ACCESS_2_TRANSFER_WRITE_BIT,
		.dstStageMask = VK_PIPELINE_STAGE_2_HOST_BIT,
		.dstAccessMask = VK_ACCESS_2_HOST_READ_BIT,
		.srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
		.dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
		.buffer = s->readback,
		.offset = 0,
		.size = VK_WHOLE_SIZE,
	};
	VkDependencyInfo dep = {
		.sType = VK_STRUCTURE_TYPE_DEPENDENCY_INFO,
		.bufferMemoryBarrierCount = 1,
		.pBufferMemoryBarriers = &host,
	};
	d->vkCmdPipelineBarrier2(cmd, &dep);
	r = d->vkEndCommandBuffer(cmd);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkEndCommandBuffer", r);
	}
	return submit(d, d->transfer_queue, cmd, err, errlen);
}

int hwvk_read(hwvk_session *s, uint32_t surface, const uint8_t **nv12, uint32_t *pitch, uint32_t *rows, char *err, size_t errlen) {
	pthread_mutex_lock(&s->d->mu);
	int status = read_locked(s, surface, err, errlen);
	pthread_mutex_unlock(&s->d->mu);
	*nv12 = s->readback_map;
	*pitch = s->coded_w;
	*rows = s->coded_h;
	return status;
}

// A buffer on the GPU, not mapped: where pictures are copied and averaged.
static VkResult device_buffer(hwvk_device *d, VkDeviceSize size, VkBufferUsageFlags usage, VkBuffer *buf, VkDeviceMemory *mem) {
	VkBufferCreateInfo bi = {
		.sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
		.size = size,
		.usage = usage,
		.sharingMode = d->nfamilies == 1 ? VK_SHARING_MODE_EXCLUSIVE : VK_SHARING_MODE_CONCURRENT,
		.queueFamilyIndexCount = d->nfamilies,
		.pQueueFamilyIndices = d->families,
	};
	VkResult r = d->vkCreateBuffer(d->device, &bi, NULL, buf);
	if (r != VK_SUCCESS) {
		*buf = VK_NULL_HANDLE;
		return r;
	}
	VkMemoryRequirements req;
	d->vkGetBufferMemoryRequirements(d->device, *buf, &req);
	r = allocate(d, req, 0, VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT, 0, mem);
	if (r != VK_SUCCESS) {
		*mem = VK_NULL_HANDLE;
		return r;
	}
	return d->vkBindBufferMemory(d->device, *buf, *mem, 0);
}

static uint32_t align4(uint32_t v) {
	return (v + 3) & ~3u;
}

// blocks_size is how many bytes the blocks of a picture take.
static VkDeviceSize blocks_size(const hwvk_scale *p) {
	if (p->same || (p->block_x == 1 && p->block_y == 1)) {
		return 0;
	}
	uint32_t cw = (p->width + 1) / 2, ch = (p->height + 1) / 2;
	uint32_t bw = (p->width + p->block_x - 1) / p->block_x, bh = (p->height + p->block_y - 1) / p->block_y;
	uint32_t cbw = (cw + p->block_x - 1) / p->block_x, cbh = (ch + p->block_y - 1) / p->block_y;
	return (VkDeviceSize)align4(bw) * bh + 2 * (VkDeviceSize)align4(cbw) * cbh;
}

// check_scale checks a scaling of a picture of rows of pitch bytes, rows
// of luma, as hwvk_scale tells it, and fills the push constants for it.
static int check_scale(const hwvk_scale *p, uint32_t pitch, uint32_t rows, uint32_t blocks, scale_push *pc, char *err, size_t errlen) {
	int pow2 = p->block_x >= 1 && p->block_x <= 16 && !(p->block_x & (p->block_x - 1)) && p->block_y >= 1 && p->block_y <= 16 && !(p->block_y & (p->block_y - 1));
	if (p->width == 0 || p->height == 0 || p->crop_left + p->width > pitch || p->crop_top + p->height > rows || p->dst_width == 0 || p->dst_height == 0 || p->dst_width > 4096 || p->dst_height > 4096 || !pow2 || (p->crop_left | p->crop_top) & 1) {
		snprintf(err, errlen, "scaling of %ux%u at %u,%u of %ux%u to %ux%u in blocks of %ux%u", p->width, p->height, p->crop_left, p->crop_top, pitch, rows, p->dst_width, p->dst_height, p->block_x, p->block_y);
		return HWVK_ERROR;
	}
	*pc = (scale_push){
		.w = p->dst_width,
		.h = p->dst_height,
		.fx = p->block_x,
		.fy = p->block_y,
		.linear = p->linear,
		.sw = p->width,
		.sh = p->height,
		.src_y = p->crop_top * pitch + p->crop_left,
		.src_uv = pitch * rows + p->crop_top / 2 * pitch + p->crop_left / 2 * 2,
		.pitch = pitch,
		.blocks = blocks,
		.gain = p->gain,
		.black = p->black,
		.rv = p->rv,
		.gu = p->gu,
		.gv = p->gv,
		.bu = p->bu,
	};
	return HWVK_OK;
}

// target_ready makes t hold a picture of nv12 bytes with its blocks, and
// pixels of out bytes, the work buffer mapped for the host when host is
// set (the tests fill it); d->mu is held.
static int target_ready(hwvk_device *d, scale_target *t, VkDeviceSize work, VkDeviceSize out, int host, uint8_t **work_map, char *err, size_t errlen) {
	VkResult r;
	if (work > t->work_size || host) {
		free_buffer(d, &t->work, &t->work_memory, NULL);
		t->work_size = 0;
		VkBufferUsageFlags usage = VK_BUFFER_USAGE_STORAGE_BUFFER_BIT | VK_BUFFER_USAGE_TRANSFER_DST_BIT;
		r = host ? host_buffer(d, work, usage, NULL, 0, &t->work, &t->work_memory, work_map) : device_buffer(d, work, usage, &t->work, &t->work_memory);
		if (r != VK_SUCCESS) {
			free_buffer(d, &t->work, &t->work_memory, NULL);
			return vk_fail(err, errlen, "scaler's work buffer", r);
		}
		t->work_size = work;
	}
	if (out > t->out_size) {
		free_buffer(d, &t->out, &t->out_memory, &t->out_map);
		t->out_size = 0;
		// What the CPU reads, as the pictures read back: cached memory.
		r = host_buffer(d, out, VK_BUFFER_USAGE_STORAGE_BUFFER_BIT, NULL, 1, &t->out, &t->out_memory, &t->out_map);
		if (r != VK_SUCCESS) {
			free_buffer(d, &t->out, &t->out_memory, &t->out_map);
			return vk_fail(err, errlen, "scaler's pixel buffer", r);
		}
		t->out_size = out;
	}
	if (!t->pool) {
		VkDescriptorPoolSize size = {VK_DESCRIPTOR_TYPE_STORAGE_BUFFER, 2};
		VkDescriptorPoolCreateInfo pi = {
			.sType = VK_STRUCTURE_TYPE_DESCRIPTOR_POOL_CREATE_INFO,
			.maxSets = 1,
			.poolSizeCount = 1,
			.pPoolSizes = &size,
		};
		r = d->vkCreateDescriptorPool(d->device, &pi, NULL, &t->pool);
		if (r != VK_SUCCESS) {
			t->pool = VK_NULL_HANDLE;
			return vk_fail(err, errlen, "vkCreateDescriptorPool", r);
		}
		VkDescriptorSetAllocateInfo ai = {
			.sType = VK_STRUCTURE_TYPE_DESCRIPTOR_SET_ALLOCATE_INFO,
			.descriptorPool = t->pool,
			.descriptorSetCount = 1,
			.pSetLayouts = &d->set_layout,
		};
		r = d->vkAllocateDescriptorSets(d->device, &ai, &t->set);
		if (r != VK_SUCCESS) {
			d->vkDestroyDescriptorPool(d->device, t->pool, NULL);
			t->pool = VK_NULL_HANDLE;
			return vk_fail(err, errlen, "vkAllocateDescriptorSets", r);
		}
	}
	// The set is not in use: every submission is waited for.
	VkDescriptorBufferInfo buffers[2] = {
		{t->work, 0, VK_WHOLE_SIZE},
		{t->out, 0, VK_WHOLE_SIZE},
	};
	VkWriteDescriptorSet writes[2];
	for (int i = 0; i < 2; i++) {
		writes[i] = (VkWriteDescriptorSet){
			.sType = VK_STRUCTURE_TYPE_WRITE_DESCRIPTOR_SET,
			.dstSet = t->set,
			.dstBinding = (uint32_t)i,
			.descriptorCount = 1,
			.descriptorType = VK_DESCRIPTOR_TYPE_STORAGE_BUFFER,
			.pBufferInfo = &buffers[i],
		};
	}
	d->vkUpdateDescriptorSets(d->device, 2, writes, 0, NULL);
	return HWVK_OK;
}

static void memory_barrier(hwvk_device *d, VkCommandBuffer cmd, VkPipelineStageFlags2 src_stage, VkAccessFlags2 src_access, VkPipelineStageFlags2 dst_stage, VkAccessFlags2 dst_access) {
	VkMemoryBarrier2 b = {
		.sType = VK_STRUCTURE_TYPE_MEMORY_BARRIER_2,
		.srcStageMask = src_stage,
		.srcAccessMask = src_access,
		.dstStageMask = dst_stage,
		.dstAccessMask = dst_access,
	};
	VkDependencyInfo dep = {
		.sType = VK_STRUCTURE_TYPE_DEPENDENCY_INFO,
		.memoryBarrierCount = 1,
		.pMemoryBarriers = &b,
	};
	d->vkCmdPipelineBarrier2(cmd, &dep);
}

// record_scale records the passes of the scaler over the picture in t's
// work buffer, the pixels left in t's out for the host.
static void record_scale(hwvk_device *d, VkCommandBuffer cmd, scale_target *t, const hwvk_scale *p, scale_push *pc) {
	d->vkCmdBindPipeline(cmd, VK_PIPELINE_BIND_POINT_COMPUTE, d->pipeline);
	d->vkCmdBindDescriptorSets(cmd, VK_PIPELINE_BIND_POINT_COMPUTE, d->pipeline_layout, 0, 1, &t->set, 0, NULL);
	if (!p->same && (p->block_x > 1 || p->block_y > 1)) {
		// The blocks: an invocation for four of a row, the planes along z.
		uint32_t bw = (p->width + p->block_x - 1) / p->block_x, bh = (p->height + p->block_y - 1) / p->block_y;
		pc->mode = 0;
		d->vkCmdPushConstants(cmd, d->pipeline_layout, VK_SHADER_STAGE_COMPUTE_BIT, 0, sizeof *pc, pc);
		d->vkCmdDispatch(cmd, ((bw + 3) / 4 + 7) / 8, (bh + 7) / 8, 3);
		memory_barrier(d, cmd, VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT, VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT,
			VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT, VK_ACCESS_2_SHADER_STORAGE_READ_BIT);
	}
	pc->mode = p->same ? 2 : 1;
	d->vkCmdPushConstants(cmd, d->pipeline_layout, VK_SHADER_STAGE_COMPUTE_BIT, 0, sizeof *pc, pc);
	d->vkCmdDispatch(cmd, (p->dst_width + 7) / 8, (p->dst_height + 7) / 8, 1);
	memory_barrier(d, cmd, VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT, VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT,
		VK_PIPELINE_STAGE_2_HOST_BIT, VK_ACCESS_2_HOST_READ_BIT);
}

// copy_pixels copies the pixels t's out holds into dst, rows of stride bytes.
static void copy_pixels(scale_target *t, const hwvk_scale *p, uint8_t *dst, uint32_t stride) {
	size_t row = (size_t)p->dst_width * 4;
	if (stride == row) {
		memcpy(dst, t->out_map, row * p->dst_height);
		return;
	}
	for (uint32_t y = 0; y < p->dst_height; y++) {
		memcpy(dst + (size_t)y * stride, t->out_map + y * row, row);
	}
}

static int render_locked(hwvk_session *s, uint32_t surface, const hwvk_scale *p, uint8_t *dst, uint32_t stride, char *err, size_t errlen) {
	hwvk_device *d = s->d;
	if (d->lost) {
		snprintf(err, errlen, "the device is lost");
		return HWVK_LOST;
	}
	if (!d->pipeline) {
		snprintf(err, errlen, "%s", d->scale_why);
		return HWVK_UNSUPPORTED;
	}
	if (surface >= s->surfaces || s->layouts[surface] != VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR) {
		snprintf(err, errlen, "surface %u holds no picture", surface);
		return HWVK_ERROR;
	}
	VkDeviceSize picture = (VkDeviceSize)s->coded_w * s->coded_h * 3 / 2;
	scale_push pc;
	int status = check_scale(p, s->coded_w, s->coded_h, (uint32_t)picture, &pc, err, errlen);
	if (status != HWVK_OK) {
		return status;
	}
	if (stride < p->dst_width * 4) {
		snprintf(err, errlen, "rows of %u bytes for %u pixels", stride, p->dst_width);
		return HWVK_ERROR;
	}
	status = target_ready(d, &d->target, picture + blocks_size(p), (VkDeviceSize)p->dst_width * p->dst_height * 4, 0, NULL, err, errlen);
	if (status != HWVK_OK) {
		return status;
	}
	VkCommandBuffer cmd = d->compute_cmd;
	VkCommandBufferBeginInfo bi = {
		.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
		.flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
	};
	VkResult r = d->vkBeginCommandBuffer(cmd, &bi);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkBeginCommandBuffer", r);
	}
	// The picture is copied as the readback copies it, but into the GPU's
	// own memory: nothing of it crosses the bus.
	image_barrier(d, cmd, s->image, surface, VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
		VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT, VK_ACCESS_2_MEMORY_WRITE_BIT,
		VK_PIPELINE_STAGE_2_COPY_BIT, VK_ACCESS_2_TRANSFER_READ_BIT);
	VkBufferImageCopy regions[2] = {
		{
			.bufferOffset = 0,
			.bufferRowLength = s->coded_w,
			.bufferImageHeight = s->coded_h,
			.imageSubresource = {VK_IMAGE_ASPECT_PLANE_0_BIT, 0, surface, 1},
			.imageExtent = {s->coded_w, s->coded_h, 1},
		},
		{
			.bufferOffset = (VkDeviceSize)s->coded_w * s->coded_h,
			.bufferRowLength = s->coded_w / 2,
			.bufferImageHeight = s->coded_h / 2,
			.imageSubresource = {VK_IMAGE_ASPECT_PLANE_1_BIT, 0, surface, 1},
			.imageExtent = {s->coded_w / 2, s->coded_h / 2, 1},
		},
	};
	d->vkCmdCopyImageToBuffer(cmd, s->image, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, d->target.work, 2, regions);
	image_barrier(d, cmd, s->image, surface, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR,
		VK_PIPELINE_STAGE_2_COPY_BIT, VK_ACCESS_2_TRANSFER_READ_BIT,
		VK_PIPELINE_STAGE_2_ALL_COMMANDS_BIT, 0);
	memory_barrier(d, cmd, VK_PIPELINE_STAGE_2_COPY_BIT, VK_ACCESS_2_TRANSFER_WRITE_BIT,
		VK_PIPELINE_STAGE_2_COMPUTE_SHADER_BIT, VK_ACCESS_2_SHADER_STORAGE_READ_BIT | VK_ACCESS_2_SHADER_STORAGE_WRITE_BIT);
	record_scale(d, cmd, &d->target, p, &pc);
	r = d->vkEndCommandBuffer(cmd);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "vkEndCommandBuffer", r);
	}
	status = submit(d, d->compute_queue, cmd, err, errlen);
	if (status == HWVK_OK) {
		copy_pixels(&d->target, p, dst, stride);
	}
	return status;
}

int hwvk_render(hwvk_session *s, uint32_t surface, const hwvk_scale *p, uint8_t *dst, uint32_t stride, char *err, size_t errlen) {
	pthread_mutex_lock(&s->d->mu);
	int status = render_locked(s, surface, p, dst, stride, err, errlen);
	pthread_mutex_unlock(&s->d->mu);
	return status;
}

int hwvk_scale_nv12(hwvk_device *d, const uint8_t *nv12, uint32_t pitch, uint32_t rows, const hwvk_scale *p, uint8_t *dst, uint32_t stride, char *err, size_t errlen) {
	if (pitch % 4 || rows % 2 || stride < p->dst_width * 4) {
		snprintf(err, errlen, "a picture of %ux%u, rows of %u bytes", pitch, rows, stride);
		return HWVK_ERROR;
	}
	pthread_mutex_lock(&d->mu);
	VkDeviceSize picture = (VkDeviceSize)pitch * rows * 3 / 2;
	scale_target t = {0};
	uint8_t *work = NULL;
	scale_push pc;
	int status = check_scale(p, pitch, rows, (uint32_t)picture, &pc, err, errlen);
	if (status == HWVK_OK) {
		status = target_ready(d, &t, picture + blocks_size(p), (VkDeviceSize)p->dst_width * p->dst_height * 4, 1, &work, err, errlen);
	}
	if (status == HWVK_OK) {
		memcpy(work, nv12, picture);
		VkCommandBuffer cmd = d->compute_cmd;
		VkCommandBufferBeginInfo bi = {
			.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
			.flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
		};
		VkResult r = d->vkBeginCommandBuffer(cmd, &bi);
		if (r == VK_SUCCESS) {
			record_scale(d, cmd, &t, p, &pc);
			r = d->vkEndCommandBuffer(cmd);
		}
		status = r == VK_SUCCESS ? submit(d, d->compute_queue, cmd, err, errlen) : vk_fail(err, errlen, "command buffer", r);
	}
	if (status == HWVK_OK) {
		copy_pixels(&t, p, dst, stride);
	}
	if (work) {
		d->vkUnmapMemory(d->device, t.work_memory);
	}
	target_free(d, &t);
	pthread_mutex_unlock(&d->mu);
	return status;
}

#endif
