// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package main

import "os"

func init() {
	for k, v := range haikuEnv(os.Getenv) {
		os.Setenv(k, v)
	}
}
