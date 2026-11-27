// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// registeredBrowsers lists the browsers installed on Windows, which are
// rarely on PATH: each registers under Clients\StartMenuInternet, for the
// user or for the machine, with the command that opens it, plus standard
// install paths for Edge and Zen if absent from StartMenuInternet.
func registeredBrowsers() []string {
	var found []string
	seen := map[string]bool{}
	add := func(path string) {
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		found = append(found, path)
	}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{0, registry.WOW64_32KEY} {
			k, err := registry.OpenKey(root, `SOFTWARE\Clients\StartMenuInternet`, registry.ENUMERATE_SUB_KEYS|view)
			if err != nil {
				continue
			}
			names, _ := k.ReadSubKeyNames(-1)
			k.Close()
			for _, name := range names {
				c, err := registry.OpenKey(root, `SOFTWARE\Clients\StartMenuInternet\`+name+`\shell\open\command`, registry.QUERY_VALUE|view)
				if err != nil {
					continue
				}
				command, _, err := c.GetStringValue("")
				c.Close()
				if err != nil {
					continue
				}
				if path, ok := commandProgram(command); ok {
					add(path)
				}
			}
		}
	}
	for _, path := range webView2Browsers() {
		add(path)
	}
	for _, fallback := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Zen Browser", "zen.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Zen Browser", "zen.exe"),
	} {
		if filepath.IsAbs(fallback) {
			if st, err := os.Stat(fallback); err == nil && !st.IsDir() {
				add(fallback)
			}
		}
	}
	return found
}

const webView2ClientGUID = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// webView2Browsers locates installed Microsoft Edge WebView2 Runtime binaries
// that ship EmbeddedBrowserWebView.dll for the current architecture.
func webView2Browsers() []string {
	var found []string
	seen := map[string]bool{}
	add := func(exe string) {
		if !filepath.IsAbs(exe) {
			return
		}
		key := strings.ToLower(exe)
		if seen[key] {
			return
		}
		if st, err := os.Stat(exe); err != nil || st.IsDir() {
			return
		}
		if webView2DLLPath(exe) == "" {
			return
		}
		seen[key] = true
		found = append(found, exe)
	}
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, subkey := range []string{
			`SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webView2ClientGUID,
			`SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webView2ClientGUID,
		} {
			k, err := registry.OpenKey(root, subkey, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			pv, _, errPV := k.GetStringValue("pv")
			loc, _, errLoc := k.GetStringValue("location")
			k.Close()
			if errPV == nil && pv != "" && pv != "0.0.0.0" {
				if errLoc == nil && loc != "" {
					add(filepath.Join(loc, pv, "msedgewebview2.exe"))
				}
				for _, base := range []string{
					filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "EdgeWebView", "Application"),
					filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "EdgeWebView", "Application"),
					filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "EdgeWebView", "Application"),
				} {
					add(filepath.Join(base, pv, "msedgewebview2.exe"))
				}
			}
		}
	}
	for _, base := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "EdgeWebView", "Application"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "EdgeWebView", "Application"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "EdgeWebView", "Application"),
	} {
		if !filepath.IsAbs(base) {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(base, "*", "msedgewebview2.exe"))
		for _, m := range matches {
			add(m)
		}
	}
	return found
}
