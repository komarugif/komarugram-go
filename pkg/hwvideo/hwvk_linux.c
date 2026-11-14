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

int hwvk_device_open(hwvk_device **out, char *err, size_t errlen) {
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
int hwvk_session_parameters(hwvk_session *s, const hwvk_sps *sps, uint32_t nsps, const hwvk_pps *pps, uint32_t npps, char *err, size_t errlen) { return HWVK_ERROR; }
int hwvk_decode(hwvk_session *s, const hwvk_pic *p, const uint8_t *data, size_t size, const uint32_t *offsets, uint32_t nslices, char *err, size_t errlen) { return HWVK_ERROR; }
int hwvk_read(hwvk_session *s, uint32_t surface, const uint8_t **nv12, uint32_t *pitch, uint32_t *rows, char *err, size_t errlen) { return HWVK_ERROR; }

#else

#define INSTANCE_FUNCS(X) \
	X(vkDestroyInstance) \
	X(vkEnumeratePhysicalDevices) \
	X(vkGetPhysicalDeviceProperties) \
	X(vkGetPhysicalDeviceQueueFamilyProperties2) \
	X(vkEnumerateDeviceExtensionProperties) \
	X(vkGetPhysicalDeviceVideoCapabilitiesKHR) \
	X(vkGetPhysicalDeviceVideoFormatPropertiesKHR) \
	X(vkGetPhysicalDeviceMemoryProperties) \
	X(vkCreateDevice) \
	X(vkGetDeviceProcAddr)

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
#define X(f) PFN_##f f;
	INSTANCE_FUNCS(X)
	DEVICE_FUNCS(X)
#undef X
};

struct hwvk_session {
	hwvk_device *d;
	uint32_t coded_w, coded_h, surfaces, max_refs;
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
	return 1;
}

void hwvk_device_close(hwvk_device *d) {
	if (!d) {
		return;
	}
	if (d->device) {
		d->vkDeviceWaitIdle(d->device);
		if (d->fence) {
			d->vkDestroyFence(d->device, d->fence, NULL);
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

int hwvk_device_open(hwvk_device **out, char *err, size_t errlen) {
	*out = NULL;
	hwvk_device *d = calloc(1, sizeof *d);
	if (!d) {
		snprintf(err, errlen, "out of memory");
		return HWVK_ERROR;
	}
	pthread_mutex_init(&d->mu, NULL);
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
	snprintf(err, errlen, "no Vulkan device");
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
			if (props.deviceType == order[o] && suitable(d, phys[i], err, errlen)) {
				d->phys = phys[i];
			}
		}
	}
	free(phys);
	if (!d->phys) {
		hwvk_device_close(d);
		return HWVK_UNSUPPORTED;
	}
	d->vkGetPhysicalDeviceMemoryProperties(d->phys, &d->memory);
	float priority = 1.0f;
	VkDeviceQueueCreateInfo queues[2] = {
		{.sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO, .queueFamilyIndex = d->decode_family, .queueCount = 1, .pQueuePriorities = &priority},
		{.sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO, .queueFamilyIndex = d->transfer_family, .queueCount = 1, .pQueuePriorities = &priority},
	};
	VkPhysicalDeviceVulkan13Features features13 = {
		.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_VULKAN_1_3_FEATURES,
		.synchronization2 = VK_TRUE,
	};
	VkDeviceCreateInfo di = {
		.sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO,
		.pNext = &features13,
		.queueCreateInfoCount = d->decode_family == d->transfer_family ? 1 : 2,
		.pQueueCreateInfos = queues,
		.enabledExtensionCount = sizeof device_extensions / sizeof *device_extensions,
		.ppEnabledExtensionNames = device_extensions,
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
	*out = d;
	return HWVK_OK;
}

static int memory_type(hwvk_device *d, uint32_t bits, VkMemoryPropertyFlags want, VkMemoryPropertyFlags prefer) {
	int found = -1;
	for (uint32_t i = 0; i < d->memory.memoryTypeCount; i++) {
		VkMemoryPropertyFlags flags = d->memory.memoryTypes[i].propertyFlags;
		if (!(bits & (1u << i)) || (flags & want) != want) {
			continue;
		}
		if ((flags & prefer) == prefer) {
			return (int)i;
		}
		if (found < 0) {
			found = (int)i;
		}
	}
	return found;
}

static VkResult allocate(hwvk_device *d, VkMemoryRequirements req, VkMemoryPropertyFlags want, VkMemoryPropertyFlags prefer, VkDeviceMemory *mem) {
	int t = memory_type(d, req.memoryTypeBits, want, prefer);
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
static VkResult host_buffer(hwvk_device *d, VkDeviceSize size, VkBufferUsageFlags usage, const void *next, VkBuffer *buf, VkDeviceMemory *mem, uint8_t **map) {
	uint32_t families[2] = {d->decode_family, d->transfer_family};
	VkBufferCreateInfo bi = {
		.sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
		.pNext = next,
		.size = size,
		.usage = usage,
		.sharingMode = families[0] == families[1] ? VK_SHARING_MODE_EXCLUSIVE : VK_SHARING_MODE_CONCURRENT,
		.queueFamilyIndexCount = families[0] == families[1] ? 1 : 2,
		.pQueueFamilyIndices = families,
	};
	VkResult r = d->vkCreateBuffer(d->device, &bi, NULL, buf);
	if (r != VK_SUCCESS) {
		*buf = VK_NULL_HANDLE;
		return r;
	}
	VkMemoryRequirements req;
	d->vkGetBufferMemoryRequirements(d->device, *buf, &req);
	r = allocate(d, req, VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT | VK_MEMORY_PROPERTY_HOST_COHERENT_BIT, VK_MEMORY_PROPERTY_HOST_CACHED_BIT, mem);
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
	if (*map) {
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
	if (s->surfaces > caps->maxDpbSlots || s->max_refs > caps->maxActiveReferencePictures) {
		snprintf(err, errlen, "%u pictures and %u references, of %u and %u", s->surfaces, s->max_refs, caps->maxDpbSlots, caps->maxActiveReferencePictures);
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
	uint32_t families[2] = {d->decode_family, d->transfer_family};
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
		.sharingMode = families[0] == families[1] ? VK_SHARING_MODE_EXCLUSIVE : VK_SHARING_MODE_CONCURRENT,
		.queueFamilyIndexCount = families[0] == families[1] ? 1 : 2,
		.pQueueFamilyIndices = families,
		.initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
	};
	VkResult r = d->vkCreateImage(d->device, &ii, NULL, &s->image);
	if (r != VK_SUCCESS) {
		s->image = VK_NULL_HANDLE;
		return vk_fail(err, errlen, "vkCreateImage", r);
	}
	VkMemoryRequirements req;
	d->vkGetImageMemoryRequirements(d->device, s->image, &req);
	r = allocate(d, req, VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT, 0, &s->image_memory);
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
		.maxDpbSlots = s->surfaces,
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
		r = allocate(d, mr[i].memoryRequirements, 0, VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT, &s->session_memory[i]);
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
	r = host_buffer(d, (VkDeviceSize)s->coded_w * s->coded_h * 3 / 2, VK_BUFFER_USAGE_TRANSFER_DST_BIT, NULL, &s->readback, &s->readback_memory, &s->readback_map);
	if (r != VK_SUCCESS) {
		return vk_fail(err, errlen, "readback buffer", r);
	}
	return HWVK_OK;
}

int hwvk_session_open(hwvk_device *d, uint32_t width_mbs, uint32_t height_mbs, uint32_t level, uint32_t refs, uint32_t surfaces, hwvk_session **out, char *err, size_t errlen) {
	*out = NULL;
	if (surfaces == 0 || surfaces > 64) {
		snprintf(err, errlen, "%u surfaces", surfaces);
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
	s->needs_reset = 1;
	s->views = calloc(surfaces, sizeof *s->views);
	s->layouts = calloc(surfaces, sizeof *s->layouts);
	pthread_mutex_lock(&d->mu);
	int status;
	if (d->lost) {
		snprintf(err, errlen, "the device is lost");
		status = HWVK_LOST;
	} else if (!s->views || !s->layouts) {
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
	VkResult r = host_buffer(d, grown, VK_BUFFER_USAGE_VIDEO_DECODE_SRC_BIT_KHR, &d->profile_list, &s->bitstream, &s->bitstream_memory, &s->bitstream_map);
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
		if (ref < 0 || (uint32_t)ref >= s->surfaces || ref == p->surface || s->layouts[ref] != VK_IMAGE_LAYOUT_VIDEO_DECODE_DPB_KHR) {
			snprintf(err, errlen, "reference in surface %d", ref);
			return HWVK_ERROR;
		}
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
			.slotIndex = surface,
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

#endif
