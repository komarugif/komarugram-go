// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package main

import (
	"log"
	"strings"

	"gioui.org/app"
)

// launchNotice is the tag of the notification whose click started the
// messenger. Haiku's notification server starts it with -notified in a
// message to its BApplication, not on its command line.
func launchNotice() string {
	args, err := app.HaikuLaunchArgs()
	if err != nil {
		log.Print(err)
		return ""
	}
	for _, arg := range args {
		if tag, ok := strings.CutPrefix(arg, "-notified="); ok {
			return tag
		}
	}
	return ""
}
