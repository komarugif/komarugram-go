// SPDX-License-Identifier: Unlicense OR MIT

package proxy

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	const secret = "dddf83f4ddaee1f1171b2e7a6e7403c60d"
	for _, tc := range []struct {
		link string
		want Proxy
	}{
		{"tg://proxy?server=127.0.0.1&port=666&secret=" + secret,
			Proxy{Kind: MTProto, Host: "127.0.0.1", Port: 666, Secret: secret}},
		{"https://t.me/proxy?server=example.org&port=443&secret=" + secret,
			Proxy{Kind: MTProto, Host: "example.org", Port: 443, Secret: secret}},
		{" tg://proxy?server=127.0.0.1&port=666&secret=3d-DdruTaRaVsW6HzGY85g ",
			Proxy{Kind: MTProto, Host: "127.0.0.1", Port: 666, Secret: "3d-DdruTaRaVsW6HzGY85g"}},
		{"tg://socks?server=10.0.0.1&port=1080&user=bob&pass=hunter2",
			Proxy{Kind: SOCKS5, Host: "10.0.0.1", Port: 1080, User: "bob", Password: "hunter2"}},
		{"https://t.me/socks?server=10.0.0.1&port=1080",
			Proxy{Kind: SOCKS5, Host: "10.0.0.1", Port: 1080}},

		// What sing-box and the like are put to: no user, or either order.
		{"socks5://127.0.0.1:2080", Proxy{Kind: SOCKS5, Host: "127.0.0.1", Port: 2080}},
		{"socks5h://localhost:1080/", Proxy{Kind: SOCKS5, Host: "localhost", Port: 1080}},
		{"127.0.0.1:2080", Proxy{Kind: SOCKS5, Host: "127.0.0.1", Port: 2080}},
		{"socks5://bob:hunter2@192.168.1.5:1080",
			Proxy{Kind: SOCKS5, Host: "192.168.1.5", Port: 1080, User: "bob", Password: "hunter2"}},
		{"socks5://192.168.1.5:1080@bob:hunter2",
			Proxy{Kind: SOCKS5, Host: "192.168.1.5", Port: 1080, User: "bob", Password: "hunter2"}},
		{"socks5://proxy.example.org:1080@bob:hunter2",
			Proxy{Kind: SOCKS5, Host: "proxy.example.org", Port: 1080, User: "bob", Password: "hunter2"}},
		{"socks5://bob:hunter2@proxy.example.org:1080",
			Proxy{Kind: SOCKS5, Host: "proxy.example.org", Port: 1080, User: "bob", Password: "hunter2"}},
		// A password that is a number is a port too: the side with the IP address is the address.
		{"socks5://admin:1234@10.1.1.1:1080",
			Proxy{Kind: SOCKS5, Host: "10.1.1.1", Port: 1080, User: "admin", Password: "1234"}},
		{"socks5://10.1.1.1:1080@admin:1234",
			Proxy{Kind: SOCKS5, Host: "10.1.1.1", Port: 1080, User: "admin", Password: "1234"}},
		// An "@" in the password, and escapes.
		{"socks5://bob:p@ss@10.1.1.1:1080",
			Proxy{Kind: SOCKS5, Host: "10.1.1.1", Port: 1080, User: "bob", Password: "p@ss"}},
		{"socks5://bob:p%40ss%3A1@10.1.1.1:1080",
			Proxy{Kind: SOCKS5, Host: "10.1.1.1", Port: 1080, User: "bob", Password: "p@ss:1"}},
		{"socks5://bob:x@[::1]:1080", Proxy{Kind: SOCKS5, Host: "::1", Port: 1080, User: "bob", Password: "x"}},
		// Names that look alike: the URL's own order.
		{"socks5://user:1234@host:1080", Proxy{Kind: SOCKS5, Host: "host", Port: 1080, User: "user", Password: "1234"}},
	} {
		got, err := Parse(tc.link)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", tc.link, got, err, tc.want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for link, want := range map[string]error{
		"":                                     ErrEmpty,
		"   ":                                  ErrEmpty,
		"tg://resolve?domain=x":                ErrUnknown,
		"https://example.org/":                 ErrUnknown,
		"http://127.0.0.1:8080":                ErrUnknown,
		"tg://proxy?server=127.0.0.1&port=666": ErrNoSecret,
		"tg://proxy?server=127.0.0.1&port=666&secret=zz!!": ErrBadSecret,
		"tg://proxy?port=666&secret=dd00":                  ErrNoAddress,
		"tg://proxy?server=x.org&secret=dd00":              ErrBadPort,
		"socks5://127.0.0.1":                               ErrNoAddress,
		"socks5://127.0.0.1:0":                             ErrBadPort,
		"socks5://127.0.0.1:65536":                         ErrBadPort,
		"socks5://127.0.0.1:port":                          ErrBadPort,
		"socks5://bad host:1080":                           ErrBadHost,
		"socks5://bob@127.0.0.1:1080":                      ErrNoPassword,
		"socks5://bob:@127.0.0.1:1080":                     ErrNoPassword,
	} {
		if _, err := Parse(link); !errors.Is(err, want) {
			t.Errorf("Parse(%q): %v, want %v", link, err, want)
		}
	}
}

// TestParseLookupTellsTheAddress checks that two names that look alike are
// told apart by which of them exists, and only then.
func TestParseLookupTellsTheAddress(t *testing.T) {
	const link = "socks5://gateway:1080@alice:2000"
	exists := func(host string) bool { return host == "gateway" }

	got, err := ParseLookup(link, exists)
	want := Proxy{Kind: SOCKS5, Host: "gateway", Port: 1080, User: "alice", Password: "2000"}
	if err != nil || got != want {
		t.Errorf("ParseLookup: %+v, %v; want %+v", got, err, want)
	}
	// Without it, or where neither exists or both do, the URL's order.
	for name, lookup := range map[string]Lookup{
		"none": nil, "neither": func(string) bool { return false }, "both": func(string) bool { return true },
	} {
		got, err := ParseLookup(link, lookup)
		first := Proxy{Kind: SOCKS5, Host: "alice", Port: 2000, User: "gateway", Password: "1080"}
		if err != nil || got != first {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, first)
		}
	}
	// A side that looks like an address is not given up for a name that exists.
	for _, answer := range []bool{false, true} {
		got, _ := ParseLookup("socks5://bob:x@10.0.0.1:1080", func(string) bool { return answer })
		if got.Host != "10.0.0.1" {
			t.Errorf("an IP address was given up for a name (lookup says %v): %+v", answer, got)
		}
	}
}

func TestLinkReadsBack(t *testing.T) {
	for _, p := range []Proxy{
		{Kind: MTProto, Host: "example.org", Port: 443, Secret: "dddf83f4ddaee1f1171b2e7a6e7403c60d"},
		{Kind: SOCKS5, Host: "127.0.0.1", Port: 2080},
		{Kind: SOCKS5, Host: "10.0.0.1", Port: 1080, User: "bob", Password: "p@ss:w/rd#1"},
		{Kind: SOCKS5, Host: "::1", Port: 1080, User: "a", Password: "b"},
	} {
		got, err := Parse(p.Link())
		if err != nil || got != p {
			t.Errorf("%q: %+v, %v; want %+v", p.Link(), got, err, p)
		}
	}
}

func TestID(t *testing.T) {
	a := Proxy{Kind: SOCKS5, Host: "Example.org", Port: 1080, User: "bob", Password: "one"}
	b := Proxy{Kind: SOCKS5, Host: "example.org", Port: 1080, User: "bob", Password: "two"}
	c := Proxy{Kind: SOCKS5, Host: "example.org", Port: 1080, User: "alice", Password: "one"}
	if a.ID() != b.ID() {
		t.Error("the same server and user are two proxies for a password")
	}
	if a.ID() == c.ID() {
		t.Error("two users of a server are one proxy")
	}
}
