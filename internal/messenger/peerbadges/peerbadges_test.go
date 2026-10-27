// SPDX-License-Identifier: Unlicense OR MIT

package peerbadges

import (
	"context"
	"errors"
	"image/color"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestParseKomaruLines(t *testing.T) {
	got := parseKomaru([]byte("# comment\n\n10,#ff0000,#00ff00,128\n11,#md3\n12\n13,#12,#zzzzzz\nx,#ffffff\n0\n"))
	if len(got) != 4 {
		t.Fatalf("got %d people, want 4: %v", len(got), got)
	}
	if f := got[10]; f.Theme || f.Color != (color.NRGBA{G: 0xff, A: 128}) {
		t.Errorf("10: %+v, want the face's own green at alpha 128", f)
	}
	if f := got[11]; !f.Theme || f.Color.A != 0xff {
		t.Errorf("11: %+v, want the color of the surrounding badges", f)
	}
	if f := got[12]; f.Color != komaruOrange {
		t.Errorf("12: %+v, want Komaru orange for a line with no colors", f)
	}
	if f := got[13]; f.Color != komaruOrange {
		t.Errorf("13: %+v, want the default for colors that are not colors", f)
	}
}

func TestFaceTakesTheParticlesColor(t *testing.T) {
	got := parseKomaru([]byte("5,#102030\n6,#md3,#ff0000,64\n"))
	if f := got[5]; f.Color != (color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xff}) || f.Particles != f.Color || f.ParticlesTheme {
		t.Fatalf("face %+v, want the particles' color when its own is left out", f)
	}
	if f := got[6]; f.Theme || !f.ParticlesTheme || f.Particles.A != 64 || f.Color != (color.NRGBA{R: 0xff, A: 64}) {
		t.Fatalf("face %+v, want red in the particles of the theme, both at alpha 64", f)
	}
}

func TestParseStatuses(t *testing.T) {
	got := parseStatuses([]byte("# id,status[,popup text]\n" +
		"1,5453871634215606874,**Big**, really\n" +
		"2,https://t.me/addemoji/EmojiKomaru/3\n" +
		"3,EmojiKomaru/1/\n" +
		"4,EmojiKomaru/0\n5,not a status\n6\n0,1\n7,-5\n"))
	want := map[int64]Status{
		1: {Emoji: 5453871634215606874, Text: "**Big**, really"},
		2: {Set: "EmojiKomaru", Index: 3},
		3: {Set: "EmojiKomaru", Index: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for id, s := range want {
		if got[id] != s {
			t.Errorf("%d: %+v, want %+v", id, got[id], s)
		}
	}
}

// KomaruGram keeps no list of partners any more: the one saved is removed.
func TestPartnersAreForgotten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "komaru_partner.txt")
	if err := os.WriteFile(path, []byte("7,#0000ff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !New(dir).Lookup(7).Empty() {
		t.Error("a partner has a mark")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the list of partners is still there: %v", err)
	}
}

const ayuList = `{"developers":[1,2],"officialChannels":[3],"supporters":[4],"supporterChannels":[5],
"customBadges":[{"id":4,"badge":{"documentId":777,"text":"hi"}},{"id":9,"badge":{"documentId":0}}],"donateUsername":"x"}`

func TestLookup(t *testing.T) {
	r := New("")
	if !r.Apply("ayu_rc.json", []byte(ayuList)) {
		t.Fatal("the list was not taken")
	}
	r.Apply("komaru_badges.txt", []byte("1,#ff0000\n6,#00ff00\n"))
	r.Apply("komaru_donates.txt", []byte("4,#0000ff\n"))
	r.Apply("komaru_statuses.txt", []byte("7,EmojiKomaru/2,**Hi**, there\n"))
	for _, c := range []struct {
		id    int64
		arrow Arrow
		emoji int64
		dev   bool
		sup   bool
	}{
		{1, Official, 0, true, false},
		{2, Official, 0, false, false},
		{3, Official, 0, false, false},
		{4, Supporter, 777, false, true},
		{5, Supporter, 0, false, false},
		{6, NoArrow, 0, true, false},
		{7, NoArrow, 0, false, false},
		{9, NoArrow, 0, false, false},
		{0, NoArrow, 0, false, false},
	} {
		m := r.Lookup(c.id)
		if m.Arrow != c.arrow || m.Emoji != c.emoji || (m.Developer != nil) != c.dev || (m.Supporter != nil) != c.sup {
			t.Errorf("%d: %+v, want arrow %d emoji %d developer %v supporter %v", c.id, m, c.arrow, c.emoji, c.dev, c.sup)
		}
	}
	if m := r.Lookup(4); m.Text != "hi" {
		t.Errorf("the custom badge's text is %q", m.Text)
	}
	if s := r.Lookup(7).Status; s == nil || *s != (Status{Set: "EmojiKomaru", Index: 2, Text: "**Hi**, there"}) {
		t.Errorf("7's status is %+v", s)
	}
	if r.Lookup(4).Status != nil {
		t.Error("4 has a status the list does not give")
	}
	if r.Lookup(7).Empty() {
		t.Error("a peer with a status alone has no mark")
	}
	var none *Registry
	if !none.Lookup(1).Empty() {
		t.Error("a nil registry has marks")
	}
}

// A list that is not one is not taken, so that a broken answer does not take
// every mark away.
func TestBadListKeepsTheOldOne(t *testing.T) {
	r := New("")
	r.Apply("ayu_rc.json", []byte(ayuList))
	for _, body := range []string{"", "<html>", `{}`, `{"developers":[]}`} {
		if r.Apply("ayu_rc.json", []byte(body)) {
			t.Errorf("%q was taken", body)
		}
	}
	if r.Lookup(1).Arrow != Official {
		t.Fatal("the marks were lost")
	}
}

func TestCachedListsShowAtOnce(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	r.save("ayu_rc.json", []byte(ayuList))
	r.save("komaru_badges.txt", []byte("1,#ff0000\n"))
	again := New(dir)
	if m := again.Lookup(1); m.Arrow != Official || m.Developer == nil {
		t.Fatalf("%+v: the saved lists were not read", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "ayu_rc.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribersAreTold(t *testing.T) {
	r := New("")
	n := 0
	cancel := r.Subscribe(func() { n++ })
	r.changed()
	cancel()
	r.changed()
	if n != 1 {
		t.Fatalf("called %d times, want once", n)
	}
}

// A round reads only the lists of the marks shown: none of the arrows'
// while they are hidden, none of the faces' while those are.
func TestRefreshReadsOnlyTheListsShown(t *testing.T) {
	var asked []string
	r := New("")
	r.client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		asked = append(asked, req.URL.Host)
		return nil, errors.New("offline")
	})}
	r.Refresh(context.Background(), false, true)
	for _, host := range asked {
		if host != "gist.githubusercontent.com" {
			t.Fatalf("the faces alone asked %s", host)
		}
	}
	if len(asked) == 0 {
		t.Fatal("the faces' lists were not asked for")
	}
	asked = nil
	r.Refresh(context.Background(), true, false)
	for _, host := range asked {
		if host == "gist.githubusercontent.com" {
			t.Fatal("the arrows alone asked for the faces' lists")
		}
	}
	if len(asked) == 0 {
		t.Fatal("the arrows' list was not asked for")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
