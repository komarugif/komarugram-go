// SPDX-License-Identifier: Unlicense OR MIT

//go:build !haiku

package main

// launchNotice is the tag of the notification whose click started the
// messenger, where it does not come with -notified: nowhere but on Haiku.
func launchNotice() string { return "" }
