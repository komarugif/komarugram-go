// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/gpu"
	"gioui.org/op"
)

// lostContext loses its device on the first Present.
type lostContext struct {
	presents, releases int
}

func (c *lostContext) API() gpu.API                            { return nil }
func (c *lostContext) RenderTarget() (gpu.RenderTarget, error) { return nil, nil }
func (c *lostContext) Refresh() error                          { return nil }
func (c *lostContext) Release()                                { c.releases++ }
func (c *lostContext) Lock() error                             { return nil }
func (c *lostContext) Unlock()                                 {}
func (c *lostContext) Present() error {
	c.presents++
	return gpu.ErrDeviceLost
}

type nopGPU struct{}

func (nopGPU) Release()                                           {}
func (nopGPU) Clear(color.NRGBA)                                  {}
func (nopGPU) Frame(*op.Ops, gpu.RenderTarget, image.Point) error { return nil }

// invalidated counts the frames asked of it.
type invalidated struct {
	driver
	n int
}

func (d *invalidated) Invalidate() { d.n++ }

func TestDeviceLostOnPresentDrawsAgain(t *testing.T) {
	ctx, drv := new(lostContext), new(invalidated)
	w := &Window{driver: drv, ctx: ctx, gpu: nopGPU{}}
	if err := w.validateAndProcess(image.Pt(100, 100), false, new(op.Ops), nil); err != nil {
		t.Fatalf("a device lost on Present closed the window: %v", err)
	}
	if ctx.presents != 1 || ctx.releases != 1 || w.ctx != nil || w.gpu != nil {
		t.Errorf("presents %d, releases %d, context %v, GPU %v: the lost device is to be let go", ctx.presents, ctx.releases, w.ctx, w.gpu)
	}
	if drv.n != 1 {
		t.Errorf("%d frames asked for, want one with a new device", drv.n)
	}
}
