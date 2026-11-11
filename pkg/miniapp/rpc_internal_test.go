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

// A command waits for the answer as long as it is told to: session.new is
// answered once Firefox has started, which takes longer than the others in a
// virtual machine on Haiku.
func TestCallWithin(t *testing.T) {
	const delay = 300 * time.Millisecond
	for _, test := range []struct {
		name      string
		limit     time.Duration
		wantError bool
	}{
		{"too short", delay / 6, true},
		{"long enough", 10 * delay, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				_, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				var request struct{ ID int }
				if json.Unmarshal(data, &request) != nil {
					return
				}
				time.Sleep(delay)
				reply, _ := json.Marshal(map[string]any{"type": "success", "id": request.ID, "result": map[string]any{}})
				_ = conn.Write(r.Context(), websocket.MessageText, reply)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rpc, err := dialRPC(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), true, nil)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { rpc.read(ctx); close(done) }()
			defer func() { rpc.close(); <-done }()
			_, err = rpc.callWithin(ctx, test.limit, "session.new", nil)
			if (err != nil) != test.wantError {
				t.Fatalf("waiting %v for an answer after %v: %v", test.limit, delay, err)
			}
		})
	}
	if sessionTimeout <= commandTimeout {
		t.Errorf("session.new waits %v, no longer than the %v of the others", sessionTimeout, commandTimeout)
	}
}
