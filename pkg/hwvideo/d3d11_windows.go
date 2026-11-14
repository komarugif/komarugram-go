// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"encoding/binary"
	"fmt"
	"image"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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

	videoDeviceCreateVideoDecoder           = 3
	videoDeviceCreateVideoDecoderOutputView = 7
	videoDeviceGetVideoDecoderProfileCount  = 11
	videoDeviceGetVideoDecoderProfile       = 12
	videoDeviceCheckVideoDecoderFormat      = 13
	videoDeviceGetVideoDecoderConfigCount   = 14
	videoDeviceGetVideoDecoderConfig        = 15

	videoContextGetDecoderBuffer     = 7
	videoContextReleaseDecoderBuffer = 8
	videoContextDecoderBeginFrame    = 9
	videoContextDecoderEndFrame      = 10
	videoContextSubmitDecoderBuffers = 11
)

var (
	iidVideoDevice  = windows.GUID{Data1: 0x10EC4D5B, Data2: 0x975A, Data3: 0x4689, Data4: [8]byte{0xB9, 0xE4, 0xD0, 0xAA, 0xC3, 0x0F, 0xE3, 0x33}}
	iidVideoContext = windows.GUID{Data1: 0x61F21C45, Data2: 0x3C0E, Data3: 0x4A74, Data4: [8]byte{0x9C, 0xEA, 0x67, 0x10, 0x0D, 0x9A, 0xD5, 0xE4}}
	// D3D11_DECODER_PROFILE_H264_VLD_NOFGT: H.264 decoded from the slices on.
	profileH264 = windows.GUID{Data1: 0x1B81BE68, Data2: 0xA0C7, Data3: 0x11D3, Data4: [8]byte{0xB9, 0x84, 0x00, 0xC0, 0x4F, 0x2E, 0x73, 0xC5}}
)

const (
	formatNV12 = 103 // DXGI_FORMAT_NV12

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

type mappedSubresource struct {
	Data                 unsafe.Pointer
	RowPitch, DepthPitch uint32
}

// d3dDevice is a Direct3D 11 device with its video decoder. The calls of
// every session are made one at a time: mu.
type d3dDevice struct {
	mu                        sync.Mutex
	device, context           *comObject
	videoDevice, videoContext *comObject
}

var d3d11CreateDevice = windows.NewLazySystemDLL("d3d11.dll").NewProc("D3D11CreateDevice")

// newDevice makes a device on the GPU the system gives by default.
func newDevice() (device, error) {
	if err := d3d11CreateDevice.Find(); err != nil {
		return nil, unsupported("no Direct3D 11: %v", err)
	}
	d := new(d3dDevice)
	levels := [...]uint32{0xb000, 0xa100, 0xa000}
	r, _, _ := d3d11CreateDevice.Call(0, driverHardware, 0, createVideo,
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
		return nil, unsupported("the GPU decodes no H.264")
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
	return s, nil
}

func (s *d3dSession) release() {
	s.decoder.release()
	for _, v := range s.views {
		v.release()
	}
	s.staging.release()
	s.textures.release()
	s.decoder, s.views, s.staging, s.textures = nil, nil, nil, nil
}

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
		cb := dst.Cb[y*dst.CStride : y*dst.CStride+cw]
		cr := dst.Cr[y*dst.CStride : y*dst.CStride+cw]
		for x := range cw {
			cb[x], cr[x] = row[2*x], row[2*x+1]
		}
	}
	return nil
}

// Available reports whether this system has a hardware decoder to try:
// whether a machine has one is known once the helper is asked.
func Available() bool { return true }
