// SPDX-License-Identifier: Unlicense OR MIT

package gpu

import (
	"errors"
	"fmt"
	"testing"
)

// removed is an error of a driver's API, as d3d11.ErrorCode is.
type removed struct{ lost bool }

func (e removed) Error() string    { return "DeviceCreateBuffer: 0x887a0005" }
func (e removed) DeviceLost() bool { return e.lost }

func frameThatPanics(v any) (err error) {
	defer recoverDeviceLost(&err)
	panic(v)
}

func TestDeviceLostInAFrameIsReturned(t *testing.T) {
	for _, v := range []error{removed{lost: true}, fmt.Errorf("buffer: %w", removed{lost: true}), ErrDeviceLost} {
		if err := frameThatPanics(v); !errors.Is(err, ErrDeviceLost) {
			t.Errorf("panic(%v) returned %v, want ErrDeviceLost", v, err)
		}
	}
}

func TestOtherPanicsInAFrameGoOn(t *testing.T) {
	for _, v := range []any{removed{lost: false}, "index out of range"} {
		func() {
			defer func() {
				if r := recover(); r != v {
					t.Errorf("panic(%v) became %v", v, r)
				}
			}()
			frameThatPanics(v)
			t.Errorf("panic(%v) was swallowed", v)
		}()
	}
}
