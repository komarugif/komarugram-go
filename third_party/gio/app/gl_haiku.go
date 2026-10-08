// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package app

import (
	"errors"
	"image"
	"runtime"

	"gioui.org/gpu"
	"gioui.org/internal/gl"
)

// haikuGLContext is a window's OSMesa context: Mesa's desktop OpenGL,
// llvmpipe, drawing into memory. BGLView is not used: its renderer locks
// the window from the drawing thread and makes the window's thread wait
// for the drawing, so a window that animates took no input. Haiku's EGL
// does not initialize.
//
// OSMesa's framebuffer has no sRGB encoding, which Gio's desktop OpenGL
// path takes for granted (GL_FRAMEBUFFER_SRGB): drawn into it, colors came
// out in linear values, too dark. Gio draws into a texture of SRGB8_ALPHA8
// instead, and Present copies its encoded values to the framebuffer as
// they are, with a blit that does not decode them.
type haikuGLContext struct {
	w *haikuWindow
	f *gl.Functions

	fbo  gl.Framebuffer
	tex  gl.Texture
	size image.Point
	// vao is bound whenever the context is: the core profile takes no
	// vertex attributes without one, and Gio, reading the state anew each
	// frame (Shared), restores what it found bound.
	vao gl.VertexArray
}

func newHaikuGLContext(w *haikuWindow) (*haikuGLContext, error) {
	if w.win == nil {
		return nil, errors.New("haiku: the window is gone")
	}
	f, err := gl.NewFunctions(nil, false)
	if err != nil {
		return nil, err
	}
	return &haikuGLContext{w: w, f: f}, nil
}

func (c *haikuGLContext) API() gpu.API {
	// Shared: Present changes state behind Gio's back (the blit), so Gio
	// reads the state anew each frame. Without it, Gio took
	// GL_FRAMEBUFFER_SRGB for still enabled after Present disabled it, and
	// every frame after the first came out too dark.
	return gpu.OpenGL{Shared: true}
}

// RenderTarget is the sRGB texture's framebuffer, made anew for a new
// size of the window. The context is current.
func (c *haikuGLContext) RenderTarget() (gpu.RenderTarget, error) {
	size := c.w.config.Size
	if c.fbo.Valid() && size == c.size {
		return gpu.OpenGLRenderTarget(c.fbo), nil
	}
	c.release()
	f := c.f
	c.tex = f.CreateTexture()
	f.BindTexture(gl.TEXTURE_2D, c.tex)
	f.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	f.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	f.TexStorage2D(gl.TEXTURE_2D, 1, gl.SRGB8_ALPHA8, size.X, size.Y)
	c.fbo = f.CreateFramebuffer()
	f.BindFramebuffer(gl.FRAMEBUFFER, c.fbo)
	f.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, c.tex, 0)
	st := f.CheckFramebufferStatus(gl.FRAMEBUFFER)
	f.BindFramebuffer(gl.FRAMEBUFFER, gl.Framebuffer{})
	f.BindTexture(gl.TEXTURE_2D, gl.Texture{})
	if st != gl.FRAMEBUFFER_COMPLETE {
		c.release()
		return nil, errors.New("haiku: the sRGB framebuffer is incomplete")
	}
	c.size = size
	return gpu.OpenGLRenderTarget(c.fbo), nil
}

func (c *haikuGLContext) release() {
	if c.fbo.Valid() {
		c.f.DeleteFramebuffer(c.fbo)
		c.fbo = gl.Framebuffer{}
	}
	if c.tex.V != 0 {
		c.f.DeleteTexture(c.tex)
		c.tex = gl.Texture{}
	}
}

func (c *haikuGLContext) Present() error {
	f := c.f
	if c.fbo.Valid() {
		f.Disable(gl.FRAMEBUFFER_SRGB)
		f.BindFramebuffer(gl.READ_FRAMEBUFFER, c.fbo)
		f.BindFramebuffer(gl.DRAW_FRAMEBUFFER, gl.Framebuffer{})
		f.BlitFramebuffer(0, 0, c.size.X, c.size.Y, 0, 0, c.size.X, c.size.Y, gl.COLOR_BUFFER_BIT, gl.NEAREST)
		f.BindFramebuffer(gl.FRAMEBUFFER, gl.Framebuffer{})
	}
	haikuGLSwap(c.w.win)
	return nil
}

func (c *haikuGLContext) Refresh() error {
	return nil
}

// Release gives back the context with its buffers: Gio calls it when the
// window is hidden (minimized) too, and Lock makes them anew when it is
// shown. OSMesa's context, llvmpipe's, and the window-sized buffers stayed
// otherwise.
func (c *haikuGLContext) Release() {
	if c.w.win == nil {
		return
	}
	if c.Lock() == nil {
		c.release()
		c.Unlock()
	}
	// The objects were the destroyed context's.
	c.vao = gl.VertexArray{}
	c.size = image.Point{}
	haikuGLRelease(c.w.win)
}

func (c *haikuGLContext) Lock() error {
	if c.w.win == nil {
		return errors.New("haiku: the window is gone")
	}
	// OpenGL contexts are current on a thread.
	runtime.LockOSThread()
	if err := haikuGLLock(c.w.win, c.w.config.Size); err != nil {
		runtime.UnlockOSThread()
		return err
	}
	if !c.vao.Valid() {
		c.vao = c.f.CreateVertexArray()
	}
	c.f.BindVertexArray(c.vao)
	return nil
}

func (c *haikuGLContext) Unlock() {
	if c.w.win != nil {
		haikuGLUnlock(c.w.win)
	}
	runtime.UnlockOSThread()
}
