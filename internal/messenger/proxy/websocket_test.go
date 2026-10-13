// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/gotd/td/telegram/dcs"
)

func TestParseWebSocket(t *testing.T) {
	for link, want := range map[string]Proxy{
		"wss://web.telegram.org":         Telegram(),
		"wss://web.telegram.org/":        Telegram(),
		"wss://example.org:8443":         {Kind: WebSocket, Host: "example.org", Port: 8443},
		"wss://tg.example.org/apiws?x=1": {Kind: WebSocket, Host: "tg.example.org", Port: 443},
	} {
		got, err := Parse(link)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", link, got, err, want)
		}
	}
	for _, p := range []Proxy{Telegram(), {Kind: WebSocket, Host: "example.org", Port: 8443}} {
		if got, err := Parse(p.Link()); err != nil || got != p {
			t.Errorf("%q: %+v, %v", p.Link(), got, err)
		}
	}
	for _, link := range []string{"wss://", "wss://user@example.org", "wss://example.org:0", "wss://bad host"} {
		if _, err := Parse(link); err == nil {
			t.Errorf("Parse(%q) succeeded", link)
		}
	}
}

// TestWebSocketAddresses checks which addresses are tried for which server:
// the media servers of Telegram's own domain have names of their own, which
// are asked for first, and a domain of a user's has only the plain ones.
func TestWebSocketAddresses(t *testing.T) {
	tg := newWebSocketResolver(Telegram())
	own := newWebSocketResolver(Proxy{Kind: WebSocket, Host: "example.org", Port: 443})
	odd := newWebSocketResolver(Proxy{Kind: WebSocket, Host: "example.org", Port: 8443})
	for _, tc := range []struct {
		r     websocketResolver
		dc    int
		media bool
		want  []string
	}{
		{tg, 2, false, []string{"wss://kws2.web.telegram.org/apiws"}},
		{tg, 4, true, []string{"wss://kws4-1.web.telegram.org/apiws", "wss://kws4.web.telegram.org/apiws"}},
		{tg, 203, false, []string{"wss://kws2.web.telegram.org/apiws"}},
		{own, 1, true, []string{"wss://kws1.example.org/apiws"}},
		{odd, 5, false, []string{"wss://kws5.example.org:8443/apiws"}},
	} {
		if got := tc.r.urls(tc.dc, tc.media); !slices.Equal(got, tc.want) {
			t.Errorf("dc %d media %v: %v, want %v", tc.dc, tc.media, got, tc.want)
		}
	}
}

// TestWebSocketTriesTheNextAddress checks that a server that does not answer
// at the first address is looked for at the next, over the real dial.
func TestWebSocketTriesTheNextAddress(t *testing.T) {
	var mu sync.Mutex
	var hosts []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host)
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer server.Close()
	// Every name is the test server.
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server.Listener.Addr().String())
		},
	}}

	r := newWebSocketResolver(Telegram())
	r.client = client
	if _, err := r.MediaOnly(context.Background(), 3, dcsList()); err == nil {
		t.Fatal("a server that has no WebSocket was connected to")
	}
	if want := []string{"kws3-1.web.telegram.org", "kws3.web.telegram.org"}; !slices.Equal(hosts, want) {
		t.Errorf("asked for %v, want %v", hosts, want)
	}
	hosts = nil
	if _, err := r.Primary(context.Background(), 3, dcsList()); err == nil {
		t.Fatal("a server that has no WebSocket was connected to")
	}
	if want := []string{"kws3.web.telegram.org"}; !slices.Equal(hosts, want) {
		t.Errorf("asked for %v, want %v", hosts, want)
	}
}

func dcsList() dcs.List { return dcs.List{} }
