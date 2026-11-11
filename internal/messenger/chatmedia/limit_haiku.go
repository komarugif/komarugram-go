// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package chatmedia

import (
	"komarugram/pkg/program"
	"komarugram/pkg/program/haikumem"
)

// The programs started with a bound are watched for the RAM they keep, which
// only haikumem, with its cgo, tells.
func init() { program.SetTeamMemory(haikumem.TeamMemory) }
