// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"log"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"komarugram/pkg/resample"
)

// The decoder of Direct3D 11 (DXVA 2): the application hands the driver
// the parameters of each picture (DXVA_PicParams_H264), its scaling lists
// and its slices as they are in the stream, and the driver decodes them
// into a texture of NV12. COM is called through the tables of methods, as
// Gio's Direct3D backend does: the Windows client is built without cgo.

// comObject is a COM object: a pointer to its table of methods.
type comObject struct {
	vtbl *[80]uintptr
}

// call calls method index of o with args after the object itself.
func (o *comObject) call(index int, args ...uintptr) uintptr {
	all := make([]uintptr, 0, 12)
	all = append(all, uintptr(unsafe.Pointer(o)))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[index], all...)
	return r
}

func (o *comObject) release() {
	if o != nil {
		o.call(2)
	}
}

// hresult is the error of a call that failed.
type hresult struct {
	what string
	code uint32
}

func (e hresult) Error() string { return fmt.Sprintf("hwvideo: %s: 0x%08x", e.what, e.code) }

func check(what string, r uintptr) error {
	if int32(r) < 0 {
		return hresult{what, uint32(r)}
	}
	return nil
}

// Methods, by their place in the tables (d3d11.h).
const (
	deviceCreateTexture2D = 5

	contextMap                   = 14
	contextUnmap                 = 15
	contextCopySubresourceRegion = 46

	videoDeviceCreateVideoDecoder             = 3
	videoDeviceCreateVideoProcessor           = 4
	videoDeviceCreateVideoDecoderOutputView   = 7
	videoDeviceCreateVideoProcessorInputView  = 8
	videoDeviceCreateVideoProcessorOutputView = 9
	videoDeviceCreateVideoProcessorEnumerator = 10
	videoDeviceGetVideoDecoderProfileCount    = 11
	videoDeviceGetVideoDecoderProfile         = 12
	videoDeviceCheckVideoDecoderFormat        = 13
	videoDeviceGetVideoDecoderConfigCount     = 14
	videoDeviceGetVideoDecoderConfig          = 15

	videoContextGetDecoderBuffer     = 7
	videoContextReleaseDecoderBuffer = 8
	videoContextDecoderBeginFrame    = 9
	videoContextDecoderEndFrame      = 10
	videoContextSubmitDecoderBuffers = 11

	videoContextSetOutputTargetRect     = 13
	videoContextSetOutputColorSpace     = 15
	videoContextSetStreamFrameFormat    = 27
	videoContextSetStreamColorSpace     = 28
	videoContextSetStreamSourceRect     = 30
	videoContextSetStreamDestRect       = 31
	videoContextSetStreamAutoProcessing = 37
	videoContextVideoProcessorBlt       = 53
	processorEnumeratorCheckFormat      = 8
)

var (
	iidVideoDevice  = windows.GUID{Data1: 0x10EC4D5B, Data2: 0x975A, Data3: 0x4689, Data4: [8]byte{0xB9, 0xE4, 0xD0, 0xAA, 0xC3, 0x0F, 0xE3, 0x33}}
	iidVideoContext = windows.GUID{Data1: 0x61F21C45, Data2: 0x3C0E, Data3: 0x4A74, Data4: [8]byte{0x9C, 0xEA, 0x67, 0x10, 0x0D, 0x9A, 0xD5, 0xE4}}
	// D3D11_DECODER_PROFILE_H264_VLD_NOFGT: H.264 decoded from the slices on.
	profileH264 = windows.GUID{Data1: 0x1B81BE68, Data2: 0xA0C7, Data3: 0x11D3, Data4: [8]byte{0xB9, 0x84, 0x00, 0xC0, 0x4F, 0x2E, 0x73, 0xC5}}
)

const (
	formatNV12 = 103 // DXGI_FORMAT_NV12
	formatRGBA = 28  // DXGI_FORMAT_R8G8B8A8_UNORM
	formatBGRA = 87  // DXGI_FORMAT_B8G8R8A8_UNORM

	bindRenderTarget = 0x20
	// D3D11_VIDEO_PROCESSOR_FORMAT_SUPPORT
	processorInput  = 1
	processorOutput = 2

	bindDecoder    = 0x200
	usageStaging   = 3
	cpuAccessRead  = 0x20000
	mapRead        = 1
	createVideo    = 0x800 // D3D11_CREATE_DEVICE_VIDEO_SUPPORT
	driverHardware = 1
	sdkVersion     = 7

	// Kinds of the buffers of a picture (D3D11_VIDEO_DECODER_BUFFER_TYPE).
	bufferPicture   = 0
	bufferMatrix    = 4
	bufferSlices    = 5
	bufferBitstream = 6

	// ePending is what DecoderBeginFrame answers while the surface is
	// still in use.
	ePending = 0x8000000A
)

type texture2DDesc struct {
	Width, Height, MipLevels, ArraySize, Format uint32
	SampleCount, SampleQuality                  uint32
	Usage, BindFlags, CPUAccessFlags, MiscFlags uint32
}

type videoDecoderDesc struct {
	GUID                      windows.GUID
	SampleWidth, SampleHeight uint32
	OutputFormat              uint32
}

type videoDecoderConfig struct {
	BitstreamEncryption, MBcontrolEncryption, ResidDiffEncryption windows.GUID
	ConfigBitstreamRaw                                            uint32
	_                                                             [11]uint32
	ConfigMinRenderTargetBuffCount, ConfigDecoderSpecific         uint16
}

type outputViewDesc struct {
	DecodeProfile windows.GUID
	ViewDimension uint32 // 1: a texture
	ArraySlice    uint32
}

type decoderBufferDesc struct {
	BufferType, BufferIndex, DataOffset, DataSize uint32
	FirstMBaddress, NumMBsInBuffer                uint32
	Width, Height, Stride, ReservedBits           uint32
	IV                                            uintptr
	IVSize                                        uint32
	PartialEncryption                             int32
	EncryptedBlockInfo                            [3]uint32
}

// processorContentDesc is D3D11_VIDEO_PROCESSOR_CONTENT_DESC.
type processorContentDesc struct {
	InputFrameFormat                     uint32
	InputRateNumerator, InputRateDenom   uint32
	InputWidth, InputHeight              uint32
	OutputRateNumerator, OutputRateDenom uint32
	OutputWidth, OutputHeight            uint32
	Usage                                uint32
}

// processorInputViewDesc is D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC of a
// slice of a texture.
type processorInputViewDesc struct {
	FourCC, ViewDimension, MipSlice, ArraySlice uint32
}

// processorOutputViewDesc is D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC of a
// texture, its union as large as its largest member.
type processorOutputViewDesc struct {
	ViewDimension, MipSlice uint32
	_                       [2]uint32
}

// processorStream is D3D11_VIDEO_PROCESSOR_STREAM.
type processorStream struct {
	Enable, OutputIndex, InputFrameOrField, PastFrames, FutureFrames uint32
	PastSurfaces, InputSurface, FutureSurfaces                       uintptr
	PastSurfacesRight, InputSurfaceRight, FutureSurfacesRight        uintptr
}

// rect is a RECT.
type rect struct{ Left, Top, Right, Bottom int32 }

type mappedSubresource struct {
	Data                 unsafe.Pointer
	RowPitch, DepthPitch uint32
}

// d3dDevice is a Direct3D 11 device with its video decoder. The calls of
// every session are made one at a time: mu.
type d3dDevice struct {
	// told is set once the device was told in the log.
	told                      sync.Once
	mu                        sync.Mutex
	device, context           *comObject
	videoDevice, videoContext *comObject
}

var (
	d3d11CreateDevice  = windows.NewLazySystemDLL("d3d11.dll").NewProc("D3D11CreateDevice")
	createDXGIFactory1 = windows.NewLazySystemDLL("dxgi.dll").NewProc("CreateDXGIFactory1")
)

const (
	driverUnknown          = 0
	dxgiFactoryEnumAdapters1 = 12
	dxgiAdapterGetDesc1    = 10
	dxgiAdapterSoftware    = 2
	dxgiErrorNotFound      = 0x887A0002
)

var iidIDXGIFactory1 = windows.GUID{
	Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba,
	Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87},
}

type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuidLow        uint32
	AdapterLuidHigh       int32
	Flags                 uint32
}

// newDevice makes a Direct3D 11 H.264 decoder device, honoring
// KOMARUGRAM_HWVIDEO_DEVICE and falling back across hardware DXGI adapters
// (matching Linux's multi-GPU Vulkan Video selection).
func newDevice() (device, error) {
	if err := d3d11CreateDevice.Find(); err != nil {
		return nil, unsupported("no Direct3D 11: %v", err)
	}
	wantVendor, wantDevice, only, filtered := ParseDeviceEnv()

	if createDXGIFactory1.Find() == nil {
		var factory *comObject
		if r, _, _ := createDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidIDXGIFactory1)), uintptr(unsafe.Pointer(&factory))); int32(r) >= 0 && factory != nil {
			defer factory.release()
			var lastErr error
			matched := false
			for i := uint32(0); ; i++ {
				var adapter *comObject
				r := factory.call(dxgiFactoryEnumAdapters1, uintptr(i), uintptr(unsafe.Pointer(&adapter)))
				if uint32(r) == dxgiErrorNotFound || int32(r) < 0 || adapter == nil {
					break
				}
				var desc dxgiAdapterDesc1
				if int32(adapter.call(dxgiAdapterGetDesc1, uintptr(unsafe.Pointer(&desc)))) < 0 {
					adapter.release()
					continue
				}
				if desc.Flags&dxgiAdapterSoftware != 0 {
					adapter.release()
					continue
				}
				same := desc.VendorID == wantVendor && desc.DeviceID == wantDevice
				if filtered && ((only && !same) || (!only && same)) {
					adapter.release()
					continue
				}
				matched = true
				d, err := openD3DAdapter(adapter, windows.UTF16ToString(desc.Description[:]), desc.VendorID, desc.DeviceID)
				adapter.release()
				if err == nil {
					return d, nil
				}
				lastErr = err
			}
			if filtered {
				if !matched {
					return nil, unsupported("no matching Direct3D 11 GPU for %s", DeviceEnv)
				}
				return nil, lastErr
			}
			if lastErr != nil {
				return nil, lastErr
			}
		}
	}
	if filtered {
		return nil, unsupported("DXGI unavailable to filter Direct3D 11 GPU")
	}
	return openD3DAdapter(nil, "default", 0, 0)
}

func openD3DAdapter(adapter *comObject, name string, vendor, device uint32) (*d3dDevice, error) {
	d := new(d3dDevice)
	levels := [...]uint32{0xb000, 0xa100, 0xa000}
	driverType := uintptr(driverHardware)
	adapterPtr := uintptr(0)
	if adapter != nil {
		driverType = driverUnknown
		adapterPtr = uintptr(unsafe.Pointer(adapter))
	}
	r, _, _ := d3d11CreateDevice.Call(adapterPtr, driverType, 0, createVideo,
		uintptr(unsafe.Pointer(&levels[0])), uintptr(len(levels)), sdkVersion,
		uintptr(unsafe.Pointer(&d.device)), 0, uintptr(unsafe.Pointer(&d.context)))
	if err := check("D3D11CreateDevice", r); err != nil {
		return nil, unsupported("%v", err)
	}
	if err := check("ID3D11VideoDevice", d.device.call(0, uintptr(unsafe.Pointer(&iidVideoDevice)), uintptr(unsafe.Pointer(&d.videoDevice)))); err != nil {
		d.close()
		return nil, unsupported("%v", err)
	}
	if err := check("ID3D11VideoContext", d.context.call(0, uintptr(unsafe.Pointer(&iidVideoContext)), uintptr(unsafe.Pointer(&d.videoContext)))); err != nil {
		d.close()
		return nil, unsupported("%v", err)
	}
	// The profile, and that it decodes into NV12.
	found := false
	for i := range uint32(d.videoDevice.call(videoDeviceGetVideoDecoderProfileCount)) {
		var guid windows.GUID
		if int32(d.videoDevice.call(videoDeviceGetVideoDecoderProfile, uintptr(i), uintptr(unsafe.Pointer(&guid)))) >= 0 && guid == profileH264 {
			found = true
		}
	}
	var supported int32
	if found {
		r := d.videoDevice.call(videoDeviceCheckVideoDecoderFormat, uintptr(unsafe.Pointer(&profileH264)), formatNV12, uintptr(unsafe.Pointer(&supported)))
		found = int32(r) >= 0 && supported != 0
	}
	if !found {
		d.close()
		return nil, unsupported("the GPU %s (%04x:%04x) decodes no H.264", name, vendor, device)
	}
	if vendor != 0 || device != 0 {
		log.Printf("hwvideo: Direct3D 11 adapter %s (%04x:%04x)", name, vendor, device)
	}
	return d, nil
}

func (d *d3dDevice) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.videoContext.release()
	d.videoDevice.release()
	d.context.release()
	d.device.release()
	d.videoContext, d.videoDevice, d.context, d.device = nil, nil, nil, nil
}

// d3dSession is a decoder of one stream: its surfaces, the slices of one
// array of textures, and a texture pictures are read back through.
type d3dSession struct {
	d             *d3dDevice
	width, height int // of the surfaces
	decoder       *comObject
	textures      *comObject
	views         []*comObject
	staging       *comObject
	// report numbers the pictures for the driver's reports.
	report uint32
	// processor scales pictures to the size it was made for; noRender is
	// why it could not be made, for the CPU to scale them.
	processor *videoProcessor
	noRender  error
}

// videoProcessor scales the pictures of a session to one size, into an
// RGB texture read back through a staging one of that size.
type videoProcessor struct {
	width, height int
	enumerator    *comObject
	processor     *comObject
	inputs        []*comObject // a view of each surface, made when first scaled
	output, view  *comObject
	staging       *comObject
	// bgra is set when the processor puts out BGRA alone, the bytes
	// swapped to RGBA as they are read back.
	bgra bool
}

func (p *videoProcessor) release() {
	if p == nil {
		return
	}
	for _, v := range p.inputs {
		v.release()
	}
	p.view.release()
	p.output.release()
	p.staging.release()
	p.processor.release()
	p.enumerator.release()
	*p = videoProcessor{}
}

func (d *d3dDevice) open(sps *SPS, surfaces int) (session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if surfaces > 127 {
		return nil, unsupported("%d surfaces", surfaces)
	}
	s := &d3dSession{d: d, width: sps.WidthMbs * 16, height: sps.HeightMbs * 16}
	desc := videoDecoderDesc{GUID: profileH264, SampleWidth: uint32(s.width), SampleHeight: uint32(s.height), OutputFormat: formatNV12}
	var count uint32
	if err := check("GetVideoDecoderConfigCount", d.videoDevice.call(videoDeviceGetVideoDecoderConfigCount, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&count)))); err != nil {
		return nil, unsupported("%v", err)
	}
	// The slices are sent as they are in the stream, with the short
	// description of each (ConfigBitstreamRaw 2): the driver reads their
	// headers itself.
	var config videoDecoderConfig
	found := false
	for i := range count {
		var c videoDecoderConfig
		if int32(d.videoDevice.call(videoDeviceGetVideoDecoderConfig, uintptr(unsafe.Pointer(&desc)), uintptr(i), uintptr(unsafe.Pointer(&c)))) >= 0 && c.ConfigBitstreamRaw == 2 {
			config, found = c, true
			break
		}
	}
	if !found {
		return nil, unsupported("the GPU takes no slices as they are sent")
	}
	fail := func(err error) (session, error) {
		s.release()
		return nil, unsupported("%v", err)
	}
	texture := texture2DDesc{Width: uint32(s.width), Height: uint32(s.height), MipLevels: 1, ArraySize: uint32(surfaces), Format: formatNV12, SampleCount: 1, BindFlags: bindDecoder}
	if err := check("CreateTexture2D", d.device.call(deviceCreateTexture2D, uintptr(unsafe.Pointer(&texture)), 0, uintptr(unsafe.Pointer(&s.textures)))); err != nil {
		return fail(err)
	}
	for i := range surfaces {
		view := outputViewDesc{DecodeProfile: profileH264, ViewDimension: 1, ArraySlice: uint32(i)}
		var v *comObject
		if err := check("CreateVideoDecoderOutputView", d.videoDevice.call(videoDeviceCreateVideoDecoderOutputView, uintptr(unsafe.Pointer(s.textures)), uintptr(unsafe.Pointer(&view)), uintptr(unsafe.Pointer(&v)))); err != nil {
			return fail(err)
		}
		s.views = append(s.views, v)
	}
	staging := texture2DDesc{Width: uint32(s.width), Height: uint32(s.height), MipLevels: 1, ArraySize: 1, Format: formatNV12, SampleCount: 1, Usage: usageStaging, CPUAccessFlags: cpuAccessRead}
	if err := check("CreateTexture2D", d.device.call(deviceCreateTexture2D, uintptr(unsafe.Pointer(&staging)), 0, uintptr(unsafe.Pointer(&s.staging)))); err != nil {
		return fail(err)
	}
	if err := check("CreateVideoDecoder", d.videoDevice.call(videoDeviceCreateVideoDecoder, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&config)), uintptr(unsafe.Pointer(&s.decoder)))); err != nil {
		return fail(err)
	}
	d.told.Do(func() {
		log.Printf("hwvideo: decoding on Direct3D 11; pictures are scaled on the GPU by its video processor, only the tile's pixels read back (a stream it cannot scale is told when met, and scaled on the CPU)")
	})
	return s, nil
}

func (s *d3dSession) release() {
	s.processor.release()
	s.processor = nil
	s.decoder.release()
	for _, v := range s.views {
		v.release()
	}
	s.staging.release()
	s.textures.release()
	s.decoder, s.views, s.staging, s.textures = nil, nil, nil, nil
}

// reset needs nothing: Direct3D 11 keeps nothing of a picture but its
// surface, and the decoder tells the references of each.
func (s *d3dSession) reset() {}

func (s *d3dSession) close() {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.release()
}

// buffer fills the decoder's buffer of kind with fill, which returns how
// much of it it wrote.
func (s *d3dSession) buffer(kind uint32, fill func(buf []byte) (int, error)) (int, error) {
	var size uint32
	var data unsafe.Pointer
	vc := s.d.videoContext
	if err := check("GetDecoderBuffer", vc.call(videoContextGetDecoderBuffer, uintptr(unsafe.Pointer(s.decoder)), uintptr(kind), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)))); err != nil {
		return 0, err
	}
	var n int
	var err error
	if data == nil || size > 1<<30 {
		err = fmt.Errorf("hwvideo: decoder buffer %d of %d bytes", kind, size)
	} else {
		n, err = fill(unsafe.Slice((*byte)(data), size))
	}
	if e := check("ReleaseDecoderBuffer", vc.call(videoContextReleaseDecoderBuffer, uintptr(unsafe.Pointer(s.decoder)), uintptr(kind))); err == nil {
		err = e
	}
	return n, err
}

// picParamsSize is the size of DXVA_PicParams_H264, matrixSize of
// DXVA_Qmatrix_H264, shortSliceSize of DXVA_Slice_H264_Short.
const (
	picParamsSize  = 1040
	matrixSize     = 6*16 + 2*64
	shortSliceSize = 10
)

// picParams writes the DXVA_PicParams_H264 of p into buf, at the places
// the structure, packed, gives its fields.
func picParams(buf []byte, p *picture, report uint32) {
	clear(buf[:picParamsSize])
	le := binary.LittleEndian
	sps, pps := p.sps, p.pps
	le.PutUint16(buf[0:], uint16(sps.WidthMbs-1))
	le.PutUint16(buf[2:], uint16(sps.HeightMbs-1))
	buf[4] = uint8(p.surface) // CurrPic
	buf[5] = sps.MaxRefFrames
	bit := func(b bool, shift int) uint16 {
		if b {
			return 1 << shift
		}
		return 0
	}
	fields := uint16(sps.ChromaFormat)<<4 |
		bit(p.reference, 6) |
		bit(pps.ConstrainedIntraPred, 7) |
		bit(pps.WeightedPred, 8) |
		uint16(pps.WeightedBipred)<<9 |
		1<<11 | // MbsConsecutiveFlag
		bit(sps.FrameMbsOnly, 12) |
		bit(pps.Transform8x8, 13) |
		bit(sps.Level >= 31, 14) | // MinLumaBipredSize8x8Flag
		bit(p.intra, 15)
	le.PutUint16(buf[6:], fields)
	buf[8] = sps.BitDepthLuma
	buf[9] = sps.BitDepthChroma
	le.PutUint16(buf[10:], 3) // Reserved16Bits, as other decoders set it
	le.PutUint32(buf[12:], report)
	// RefFrameList at 16, CurrFieldOrderCnt at 32, FieldOrderCntList at
	// 40, FrameNumList at 176, UsedForReferenceFlags at 208.
	for i := range 16 {
		buf[16+i] = 0xff
	}
	var used uint32
	for i, r := range p.refs {
		entry := uint8(r.surface)
		if r.long {
			entry |= 0x80
		}
		buf[16+i] = entry
		le.PutUint32(buf[40+8*i:], uint32(r.top))
		le.PutUint32(buf[44+8*i:], uint32(r.bottom))
		le.PutUint16(buf[176+2*i:], uint16(r.frameNum))
		used |= 3 << (2 * i)
	}
	le.PutUint32(buf[32:], uint32(p.top))
	le.PutUint32(buf[36:], uint32(p.bottom))
	buf[168] = uint8(pps.InitQS)
	buf[169] = uint8(pps.ChromaQPOffset)
	buf[170] = uint8(pps.SecondChromaQPOffset)
	buf[171] = 1 // ContinuationFlag: the fields below are there
	buf[172] = uint8(pps.InitQP)
	buf[173] = pps.RefIdxL0
	buf[174] = pps.RefIdxL1
	le.PutUint32(buf[208:], used)
	le.PutUint16(buf[214:], uint16(p.frameNum))
	buf[216] = sps.Log2MaxFrameNum - 4
	buf[217] = sps.POCType
	if sps.POCType == 0 {
		buf[218] = sps.Log2MaxPOCLsb - 4
	}
	if sps.DeltaAlwaysZero {
		buf[219] = 1
	}
	flag := func(b bool) uint8 {
		if b {
			return 1
		}
		return 0
	}
	buf[220] = flag(sps.Direct8x8)
	buf[221] = flag(pps.CABAC)
	buf[222] = flag(pps.PicOrderPresent)
	buf[225] = flag(pps.DeblockingControl)
	buf[226] = flag(pps.RedundantPicCntPresent)
}

func (s *d3dSession) decode(p *picture) error {
	if len(p.refs) > 16 || p.surface >= len(s.views) {
		return malformed("picture of %d references in surface %d", len(p.refs), p.surface)
	}
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	vc := s.d.videoContext
	decoder := uintptr(unsafe.Pointer(s.decoder))
	for try := 0; ; try++ {
		r := vc.call(videoContextDecoderBeginFrame, decoder, uintptr(unsafe.Pointer(s.views[p.surface])), 0, 0)
		if uint32(r) == ePending && try < 50 {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		if err := check("DecoderBeginFrame", r); err != nil {
			return err
		}
		break
	}
	err := s.submit(p)
	if e := check("DecoderEndFrame", vc.call(videoContextDecoderEndFrame, decoder)); err == nil {
		err = e
	}
	return err
}

// submit hands the driver the buffers of p, between the beginning and the
// end of its frame.
func (s *d3dSession) submit(p *picture) error {
	s.report++
	if s.report == 0 {
		s.report = 1
	}
	var buffers [4]decoderBufferDesc
	n, err := s.buffer(bufferPicture, func(buf []byte) (int, error) {
		if len(buf) < picParamsSize {
			return 0, fmt.Errorf("hwvideo: picture parameters buffer of %d bytes", len(buf))
		}
		picParams(buf, p, s.report)
		return picParamsSize, nil
	})
	if err != nil {
		return err
	}
	buffers[0] = decoderBufferDesc{BufferType: bufferPicture, DataSize: uint32(n)}
	n, err = s.buffer(bufferMatrix, func(buf []byte) (int, error) {
		if len(buf) < matrixSize {
			return 0, fmt.Errorf("hwvideo: scaling lists buffer of %d bytes", len(buf))
		}
		at := 0
		for i := range p.pps.scaling.l4 {
			at += copy(buf[at:], p.pps.scaling.l4[i][:])
		}
		for i := range p.pps.scaling.l8 {
			at += copy(buf[at:], p.pps.scaling.l8[i][:])
		}
		return at, nil
	})
	if err != nil {
		return err
	}
	buffers[1] = decoderBufferDesc{BufferType: bufferMatrix, DataSize: uint32(n)}
	// The slices, each after a start code, and where each lies.
	type placed struct{ at, size int }
	places := make([]placed, len(p.slices))
	n, err = s.buffer(bufferBitstream, func(buf []byte) (int, error) {
		at := 0
		for i, nal := range p.slices {
			if len(buf)-at < 3+len(nal) {
				return 0, unsupported("a picture over the %d bytes the decoder takes", len(buf))
			}
			places[i] = placed{at, 3 + len(nal)}
			buf[at], buf[at+1], buf[at+2] = 0, 0, 1
			at += 3 + copy(buf[at+3:], nal)
		}
		// Padded to 128 bytes, as decoders expect, the padding counted to
		// the last slice.
		pad := min(128-at&127, len(buf)-at)
		clear(buf[at : at+pad])
		places[len(places)-1].size += pad
		return at + pad, nil
	})
	if err != nil {
		return err
	}
	mbs := uint32(p.sps.WidthMbs * p.sps.HeightMbs)
	buffers[2] = decoderBufferDesc{BufferType: bufferBitstream, DataSize: uint32(n), NumMBsInBuffer: mbs}
	n, err = s.buffer(bufferSlices, func(buf []byte) (int, error) {
		if len(buf) < shortSliceSize*len(places) {
			return 0, unsupported("over the %d slices the decoder takes", len(buf)/shortSliceSize)
		}
		for i, place := range places {
			entry := buf[shortSliceSize*i:]
			binary.LittleEndian.PutUint32(entry[0:], uint32(place.at))
			binary.LittleEndian.PutUint32(entry[4:], uint32(place.size))
			binary.LittleEndian.PutUint16(entry[8:], 0)
		}
		return shortSliceSize * len(places), nil
	})
	if err != nil {
		return err
	}
	buffers[3] = decoderBufferDesc{BufferType: bufferSlices, DataSize: uint32(n), NumMBsInBuffer: mbs}
	return check("SubmitDecoderBuffers", s.d.videoContext.call(videoContextSubmitDecoderBuffers, uintptr(unsafe.Pointer(s.decoder)), uintptr(len(buffers)), uintptr(unsafe.Pointer(&buffers[0]))))
}

func (s *d3dSession) read(surface int, sps *SPS, dst *image.YCbCr) error {
	if surface < 0 || surface >= len(s.views) {
		return malformed("surface %d", surface)
	}
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	ctx := s.d.context
	ctx.call(contextCopySubresourceRegion, uintptr(unsafe.Pointer(s.staging)), 0, 0, 0, 0, uintptr(unsafe.Pointer(s.textures)), uintptr(surface), 0)
	var mapped mappedSubresource
	if err := check("Map", ctx.call(contextMap, uintptr(unsafe.Pointer(s.staging)), 0, mapRead, 0, uintptr(unsafe.Pointer(&mapped)))); err != nil {
		return err
	}
	defer ctx.call(contextUnmap, uintptr(unsafe.Pointer(s.staging)), 0)
	pitch := int(mapped.RowPitch)
	if mapped.Data == nil || pitch < s.width {
		return fmt.Errorf("hwvideo: surface mapped with rows of %d bytes", pitch)
	}
	// The rows of luma, then those of chroma, Cb and Cr in turn.
	src := unsafe.Slice((*byte)(mapped.Data), pitch*s.height*3/2)
	w, h := sps.Width(), sps.Height()
	sizeYCbCr(dst, w, h)
	for y := range h {
		row := src[(y+sps.CropTop)*pitch+sps.CropLeft:]
		copy(dst.Y[y*dst.YStride:y*dst.YStride+w], row)
	}
	cw, ch := (w+1)/2, (h+1)/2
	chroma := src[pitch*s.height:]
	for y := range ch {
		row := chroma[(y+sps.CropTop/2)*pitch+sps.CropLeft/2*2:]
		splitUV(dst.Cb[y*dst.CStride:y*dst.CStride+cw], dst.Cr[y*dst.CStride:y*dst.CStride+cw], row)
	}
	return nil
}

// newProcessor makes the video processor of s that scales its pictures to
// w×h; an error of errNoRender is a GPU that cannot. s.d.mu is held.
func (s *d3dSession) newProcessor(w, h int) (*videoProcessor, error) {
	d := s.d
	p := &videoProcessor{width: w, height: h, inputs: make([]*comObject, len(s.views))}
	fail := func(err error) (*videoProcessor, error) {
		p.release()
		return nil, err
	}
	desc := processorContentDesc{
		InputRateNumerator: 30, InputRateDenom: 1, InputWidth: uint32(s.width), InputHeight: uint32(s.height),
		OutputRateNumerator: 30, OutputRateDenom: 1, OutputWidth: uint32(w), OutputHeight: uint32(h),
	}
	if err := check("CreateVideoProcessorEnumerator", d.videoDevice.call(videoDeviceCreateVideoProcessorEnumerator, uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&p.enumerator)))); err != nil {
		return fail(noRender("%v", err))
	}
	support := func(format uint32) uint32 {
		var flags uint32
		if int32(p.enumerator.call(processorEnumeratorCheckFormat, uintptr(format), uintptr(unsafe.Pointer(&flags)))) < 0 {
			return 0
		}
		return flags
	}
	if support(formatNV12)&processorInput == 0 {
		return fail(noRender("the video processor does not take NV12"))
	}
	format := uint32(formatRGBA)
	if support(formatRGBA)&processorOutput == 0 {
		if support(formatBGRA)&processorOutput == 0 {
			return fail(noRender("the video processor puts out neither RGBA nor BGRA"))
		}
		format, p.bgra = formatBGRA, true
	}
	if err := check("CreateVideoProcessor", d.videoDevice.call(videoDeviceCreateVideoProcessor, uintptr(unsafe.Pointer(p.enumerator)), 0, uintptr(unsafe.Pointer(&p.processor)))); err != nil {
		return fail(noRender("%v", err))
	}
	output := texture2DDesc{Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: format, SampleCount: 1, BindFlags: bindRenderTarget}
	if err := check("CreateTexture2D", d.device.call(deviceCreateTexture2D, uintptr(unsafe.Pointer(&output)), 0, uintptr(unsafe.Pointer(&p.output)))); err != nil {
		return fail(err)
	}
	staging := texture2DDesc{Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: format, SampleCount: 1, Usage: usageStaging, CPUAccessFlags: cpuAccessRead}
	if err := check("CreateTexture2D", d.device.call(deviceCreateTexture2D, uintptr(unsafe.Pointer(&staging)), 0, uintptr(unsafe.Pointer(&p.staging)))); err != nil {
		return fail(err)
	}
	view := processorOutputViewDesc{ViewDimension: 1}
	if err := check("CreateVideoProcessorOutputView", d.videoDevice.call(videoDeviceCreateVideoProcessorOutputView, uintptr(unsafe.Pointer(p.output)), uintptr(unsafe.Pointer(p.enumerator)), uintptr(unsafe.Pointer(&view)), uintptr(unsafe.Pointer(&p.view)))); err != nil {
		return fail(noRender("%v", err))
	}
	return p, nil
}

// colorSpace is a D3D11_VIDEO_PROCESSOR_COLOR_SPACE: YCbCr of colors, or
// RGB of the full range.
func colorSpace(colors resample.Colors, rgb bool) uint32 {
	if rgb {
		// Usage 0 (playback), RGB_Range 0 (0…255).
		return 0
	}
	var cs uint32
	if colors.Matrix == resample.BT709 {
		cs |= 1 << 2 // YCbCr_Matrix: BT.709
	}
	if colors.Limited {
		cs |= 1 << 4 // Nominal_Range: 16…235
	} else {
		cs |= 2 << 4 // Nominal_Range: 0…255
	}
	return cs
}

// render scales the picture with the video processor of the GPU, whose
// filter is the driver's own: not the CPU's, rough or not.
func (s *d3dSession) render(surface int, sps *SPS, plan resample.Plan, dst *image.RGBA) error {
	if surface < 0 || surface >= len(s.views) {
		return malformed("surface %d", surface)
	}
	colors := plan.Colors
	if colors.Matrix != resample.BT601 && colors.Matrix != resample.BT709 {
		// BT.2020 needs ID3D11VideoContext1's colour spaces.
		return noRender("colours of BT.2020")
	}
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.noRender != nil {
		return s.noRender
	}
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	if p := s.processor; p == nil || p.width != w || p.height != h {
		p.release()
		s.processor = nil
		p, err := s.newProcessor(w, h)
		if errors.Is(err, errNoRender) {
			s.noRender = err
		}
		if err != nil {
			return err
		}
		s.processor = p
	}
	p := s.processor
	if p.inputs[surface] == nil {
		desc := processorInputViewDesc{ViewDimension: 1, ArraySlice: uint32(surface)}
		if err := check("CreateVideoProcessorInputView", s.d.videoDevice.call(videoDeviceCreateVideoProcessorInputView, uintptr(unsafe.Pointer(s.textures)), uintptr(unsafe.Pointer(p.enumerator)), uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&p.inputs[surface])))); err != nil {
			s.noRender = noRender("%v", err)
			return s.noRender
		}
	}
	vc, vp := s.d.videoContext, uintptr(unsafe.Pointer(p.processor))
	source := rect{int32(sps.CropLeft), int32(sps.CropTop), int32(sps.CropLeft + sps.Width()), int32(sps.CropTop + sps.Height())}
	target := rect{0, 0, int32(w), int32(h)}
	vc.call(videoContextSetStreamFrameFormat, vp, 0, 0)
	in, out := colorSpace(colors, false), colorSpace(colors, true)
	vc.call(videoContextSetStreamColorSpace, vp, 0, uintptr(unsafe.Pointer(&in)))
	vc.call(videoContextSetOutputColorSpace, vp, uintptr(unsafe.Pointer(&out)))
	vc.call(videoContextSetStreamAutoProcessing, vp, 0, 0)
	vc.call(videoContextSetStreamSourceRect, vp, 0, 1, uintptr(unsafe.Pointer(&source)))
	vc.call(videoContextSetStreamDestRect, vp, 0, 1, uintptr(unsafe.Pointer(&target)))
	vc.call(videoContextSetOutputTargetRect, vp, 1, uintptr(unsafe.Pointer(&target)))
	stream := processorStream{Enable: 1, InputSurface: uintptr(unsafe.Pointer(p.inputs[surface]))}
	if err := check("VideoProcessorBlt", vc.call(videoContextVideoProcessorBlt, vp, uintptr(unsafe.Pointer(p.view)), 0, 1, uintptr(unsafe.Pointer(&stream)))); err != nil {
		return err
	}
	ctx := s.d.context
	ctx.call(contextCopySubresourceRegion, uintptr(unsafe.Pointer(p.staging)), 0, 0, 0, 0, uintptr(unsafe.Pointer(p.output)), 0, 0)
	var mapped mappedSubresource
	if err := check("Map", ctx.call(contextMap, uintptr(unsafe.Pointer(p.staging)), 0, mapRead, 0, uintptr(unsafe.Pointer(&mapped)))); err != nil {
		return err
	}
	defer ctx.call(contextUnmap, uintptr(unsafe.Pointer(p.staging)), 0)
	pitch := int(mapped.RowPitch)
	if mapped.Data == nil || pitch < w*4 {
		return fmt.Errorf("hwvideo: picture mapped with rows of %d bytes", pitch)
	}
	src := unsafe.Slice((*byte)(mapped.Data), pitch*(h-1)+w*4)
	for y := range h {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+w*4]
		copy(row, src[y*pitch:])
		for x := 0; x < len(row); x += 4 {
			if p.bgra {
				row[x], row[x+2] = row[x+2], row[x]
			}
			// The alpha the processor fills is not to be trusted: the
			// picture is opaque.
			row[x+3] = 0xff
		}
	}
	return nil
}

// Available reports whether this system has a hardware decoder to try:
// whether a machine has one is known once the helper is asked.
func Available() bool { return true }
