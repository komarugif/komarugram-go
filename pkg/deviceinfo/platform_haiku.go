// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package deviceinfo

import (
	"context"
	"strings"
	"time"

	"komarugram/pkg/program"
)

// platform is Haiku with its revision, which uname -v starts with
// ("hrev57937 Sep 18 2024 …").
func platform() (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := program.CommandContext(ctx, "uname", "-v").Output()
	if err != nil {
		return "Haiku", ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "Haiku", ""
	}
	return "Haiku", fields[0]
}
