// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package program

import "komarugram/pkg/program/haikumem"

// The tests of the bound need what tells the memory of a team.
func init() { SetTeamMemory(haikumem.TeamMemory) }
