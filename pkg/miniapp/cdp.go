// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A Chromium-based browser is driven over the Chrome DevTools Protocol: a
// binding the shim calls for the page's half of the transport, an
// evaluation for the client's, and the page opened with --app, in a window
// without the browser's own controls.

// chromiumArgs are the switches that open url in a window of its own on
// profile dir.
func chromiumArgs(url, dir string, page Page) []string {
	return append([]string{
		"--app=" + url,
		"--user-data-dir=" + dir,
		"--remote-debugging-port=0",
		"--no-first-run", "--no-default-browser-check",
		fmt.Sprintf("--window-size=%d,%d", page.Width, page.Height),
	}, page.Args...)
}

// seedProfile writes the preferences the browser is to start with. A Mini App
// is a client surface rather than a page the user browsed to, so Chromium's
// offer to translate it — a bar across the top of someone else's app — has no
// place here. There is no command-line switch for it: --disable-features has
// no Translate feature to switch off, and on distributions whose launcher is a
// wrapper script it would also override the flags that script passes. The
// preference is read out of a fresh profile on every launch, which makes it
// permanent for as long as this is the only way a Mini App opens.
func seedProfile(profile string) error {
	dir := filepath.Join(profile, "Default")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Two files, because the browsers keep these two settings at different
	// levels: the offer to translate belongs to the profile, and Brave's notice
	// about its analytics belongs to the browser. A browser that has never
	// heard of the other one's setting ignores it.
	for path, value := range map[string]string{
		filepath.Join(dir, "Preferences"): `{"translate":{"enabled":false}}`,
		filepath.Join(profile, "Local State"): `{"brave":{"p3a":` +
			`{"enabled":false,"notice_acknowledged":true}}}`,
	} {
		// Only where there is none: the browser keeps its whole state in these
		// files, and a kept profile is handed back the ones it wrote last time.
		// The settings survive, because the browser rewrites them with it.
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			return fmt.Errorf("seed browser profile: %w", err)
		}
	}
	return nil
}

// checkNotRunning reports whether a browser already holds this profile.
// Chromium allows one process per profile: a second launch hands its window to
// the first and exits, leaving two pages on one debugging endpoint and this
// bridge attached to whichever it found first. A throwaway profile cannot
// collide, a persistent one can, so it is caught here while it can still be
// explained.
func checkNotRunning(profile string) error {
	path := filepath.Join(profile, "DevToolsActivePort")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	port, _, _ := strings.Cut(string(data), "\n")
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/json/version")
	if err == nil {
		resp.Body.Close()
		return fmt.Errorf("this Mini App is already open in another window")
	}
	// Nobody answered, so the file is what an earlier run left behind. It has
	// to go, or the launch below would take it for its own port.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cdp is a page of a Chromium-based browser, driven over the DevTools
// protocol.
type cdp struct {
	rpc *rpc
	// browserWS is the endpoint of the browser itself, the only one that
	// will shut the browser down cleanly on request.
	browserWS string
}

// connectCDP waits for the debugging endpoint and attaches to the page, and
// installs both halves of the bridge when telegram is set.
func connectCDP(ctx context.Context, b *Bridge, telegram bool) (protocol, error) {
	port, err := waitForPort(ctx, b, filepath.Join(b.profile, "DevToolsActivePort"), func(data []byte) (string, bool) {
		port, _, ok := strings.Cut(string(data), "\n")
		return port, ok
	})
	if err != nil {
		return nil, err
	}
	target, err := pageTarget(ctx, port)
	if err != nil {
		return nil, err
	}
	p := &cdp{}
	p.browserWS, _ = browserTarget(ctx, port)
	p.rpc, err = dialRPC(ctx, target, false, func(method string, params json.RawMessage) {
		if method == "Runtime.bindingCalled" {
			var call struct {
				Name    string `json:"name"`
				Payload string `json:"payload"`
			}
			if json.Unmarshal(params, &call) == nil && call.Name == bindingName {
				b.message(call.Payload)
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("attach to page: %w", err)
	}
	b.watch(ctx, p.rpc)

	type step struct {
		method string
		params map[string]any
	}
	steps := []step{{"Runtime.enable", nil}, {"Page.enable", nil}}
	if telegram {
		steps = append(steps,
			step{"Runtime.addBinding", map[string]any{"name": bindingName}},
			step{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": shim}},
			// The page loaded before the shim existed, so it is loaded again
			// with the bridge in place.
			step{"Page.reload", map[string]any{"ignoreCache": true}},
		)
	}
	for _, step := range steps {
		if _, err := p.rpc.call(ctx, step.method, step.params); err != nil {
			p.close()
			return nil, err
		}
	}
	return p, nil
}

func (p *cdp) eval(ctx context.Context, expression string, value bool) (string, error) {
	params := map[string]any{"expression": expression}
	if value {
		params["returnByValue"] = true
		params["awaitPromise"] = true
	}
	result, err := p.rpc.call(ctx, "Runtime.evaluate", params)
	if err != nil || !value {
		return "", err
	}
	var wrapper struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &wrapper); err != nil {
		return "", err
	}
	text, _ := wrapper.Result.Value.(string)
	return text, nil
}

func (p *cdp) setWindowBounds(ctx context.Context, left, top, width, height int) error {
	result, err := p.rpc.call(ctx, "Browser.getWindowForTarget", nil)
	if err != nil {
		return err
	}
	var window struct {
		WindowID int `json:"windowId"`
	}
	if err := json.Unmarshal(result, &window); err != nil {
		return err
	}
	// The size and the place are asked for apart, the size first: a window
	// that may not place itself should still be given its size.
	set := func(bounds map[string]any) error {
		_, err := p.rpc.call(ctx, "Browser.setWindowBounds", map[string]any{
			"windowId": window.WindowID,
			"bounds":   bounds,
		})
		return err
	}
	if err := set(map[string]any{"width": width, "height": height, "windowState": "normal"}); err != nil {
		return err
	}
	return set(map[string]any{"left": left, "top": top})
}

// shutdown asks the browser to close, on its own endpoint: Browser.close is
// the browser's own shutdown path, and is answered as soon as the endpoint
// is up.
func (p *cdp) shutdown(ctx context.Context) error {
	if p.browserWS == "" {
		return fmt.Errorf("no browser endpoint")
	}
	conn, err := dialRPC(ctx, p.browserWS, false, nil)
	if err != nil {
		return err
	}
	defer conn.close()
	return conn.send(ctx, "Browser.close", nil)
}

func (p *cdp) close() { p.rpc.close() }

// browserTarget returns the WebSocket of the browser itself, as opposed to the
// page in it.
func browserTarget(ctx context.Context, port string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://127.0.0.1:"+port+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var version struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(bufio.NewReader(resp.Body)).Decode(&version); err != nil {
		return "", err
	}
	return version.WS, nil
}

func pageTarget(ctx context.Context, port string) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:" + port + "/json/list")
		if err == nil {
			var targets []struct {
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			err = json.NewDecoder(bufio.NewReader(resp.Body)).Decode(&targets)
			resp.Body.Close()
			if err == nil {
				for _, target := range targets {
					if target.Type == "page" && target.WS != "" {
						return target.WS, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("the browser never opened a page")
}
