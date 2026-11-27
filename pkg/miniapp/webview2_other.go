// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package miniapp

import "context"

func webView2DLLPath(exePath string) string { return "" }

func launchWebView2(ctx context.Context, chosen browser, url, dir string, ephemeral bool, page Page, telegram bool) (*Bridge, error) {
	return nil, ErrNotBrowser
}
