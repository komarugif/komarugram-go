// SPDX-License-Identifier: Unlicense OR MIT

//go:build !linux && !darwin && !windows && !haiku

package voice

import (
	"context"
	"errors"
)

func inputs(context.Context, string) ([][]string, error) {
	return nil, errors.New("recording is not supported on this system")
}
