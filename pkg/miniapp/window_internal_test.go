// SPDX-License-Identifier: Unlicense OR MIT

package miniapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Window geometry comes from the protocol, including the frame, and BiDi
// must choose this page's window rather than the first browser window.
func TestWindowSize(t *testing.T) {
	for _, test := range []struct {
		name, method, result string
		bidi                 bool
		wantError            bool
	}{
		{"CDP", "Browser.getWindowForTarget", `{"windowId":7,"bounds":{"width":450,"height":800}}`, false, false},
		{"BiDi", "browser.getClientWindows", `{"clientWindows":[{"clientWindow":"other","width":900,"height":600},{"clientWindow":"page","width":450,"height":800}]}`, true, false},
		{"BiDi missing window", "browser.getClientWindows", `{"clientWindows":[{"clientWindow":"other","width":900,"height":600}]}`, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				_, data, err := conn.Read(r.Context())
				if err != nil {
					t.Error(err)
					return
				}
				var request struct {
					ID     int
					Method string
				}
				if err := json.Unmarshal(data, &request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != test.method {
					t.Errorf("method %q, want %q", request.Method, test.method)
				}
				reply, _ := json.Marshal(map[string]any{"type": "success", "id": request.ID, "result": json.RawMessage(test.result)})
				if err := conn.Write(r.Context(), websocket.MessageText, reply); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			rpc, err := dialRPC(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), test.bidi, nil)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { rpc.read(ctx); close(done) }()
			defer func() { rpc.close(); <-done }()
			bridge := &Bridge{proto: &cdp{rpc: rpc}}
			if test.bidi {
				bridge.proto = &bidi{rpc: rpc, window: "page"}
			}
			width, height, err := bridge.WindowSize(ctx)
			if test.wantError {
				if err == nil {
					t.Fatal("accepted a window belonging to another page")
				}
			} else if err != nil || width != 450 || height != 800 {
				t.Fatalf("size %dx%d, %v; want 450x800", width, height, err)
			}
		})
	}
}
