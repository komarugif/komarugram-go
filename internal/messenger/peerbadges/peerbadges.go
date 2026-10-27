// SPDX-License-Identifier: Unlicense OR MIT

// Package peerbadges knows who wears the marks the forks of Telegram clients
// put beside a name: the arrow of exteraGram and AyuGram's developers,
// supporters and official channels, a custom emoji exteraGram gives to some
// people, and the Komaru faces of KomaruGram's developers and supporters,
// with the emoji statuses it gives to some. The lists are not Telegram's:
// they are kept by those projects and read from their servers, as their
// clients do, and the last ones received are saved, so that the marks show
// from the first frame.
package peerbadges

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"komarugram/internal/crash"
)

// Where the lists are. AyuGram's is asked first, and exteraGram's when it
// does not answer; KomaruGram keeps three, a person a line.
const (
	ayuURL     = "https://update.ayugram.one/rc/current/desktop2"
	exteraURL  = "https://api.exteragram.app/api/v1/profiles/compact"
	komaruBase = "https://gist.githubusercontent.com/svatoshgpt/300f51e5b67854aa23ab5b592bca0aa8/raw/"
)

const (
	refreshEvery = time.Hour
	fetchTimeout = 15 * time.Second
	// maxList is how much of a list is read; they are some tens of KiB.
	maxList = 4 << 20
)

// Arrow is the arrow of exteraGram and AyuGram beside a name.
type Arrow uint8

const (
	NoArrow Arrow = iota
	// Official is the arrow of the developers and the official channels,
	// in a rosette.
	Official
	// Supporter is the arrow of those who support the project.
	Supporter
)

// Face is how a Komaru face is painted: in Color, and, with Theme, in the
// color of the badges around it instead; the sparkles that circle it are
// in Particles, or with ParticlesTheme in that color too.
type Face struct {
	Color          color.NRGBA
	Theme          bool
	Particles      color.NRGBA
	ParticlesTheme bool
}

// Status is the emoji status KomaruGram gives a person: the custom emoji
// Emoji, or, when it is 0, the Index-th emoji, from 1, of the emoji set
// called Set. Text is what its popup says, empty for the supporter's text.
type Status struct {
	Emoji int64
	Set   string
	Index int
	Text  string
}

// Mark is the marks one peer has.
type Mark struct {
	Arrow Arrow
	// Emoji is the custom emoji exteraGram gives in place of the arrow, 0
	// for none, and Text what its popup says.
	Emoji int64
	Text  string
	// Developer and Supporter are the Komaru faces, and Status KomaruGram's
	// emoji status, drawn after them.
	Developer, Supporter *Face
	Status               *Status
}

// Empty reports whether the peer has no mark.
func (m Mark) Empty() bool {
	return m.Arrow == NoArrow && m.Emoji == 0 && m.Developer == nil && m.Supporter == nil && m.Status == nil
}

type custom struct {
	emoji int64
	text  string
}

// lists are what the sources say, by the bare id of a user or channel.
type lists struct {
	developers, channels, supporters, supporterChannels map[int64]bool
	custom                                              map[int64]custom
	komaruDevelopers, komaruSupporters                  map[int64]Face
	komaruStatuses                                      map[int64]Status
}

// Registry holds the lists. It is safe for concurrent use: every window
// reads it, from its own goroutine.
type Registry struct {
	dir    string
	client *http.Client

	mu   sync.RWMutex
	data lists
	subs map[int]func()
	next int
	wake chan struct{}
}

// New returns a registry that saves the lists in dir; an empty dir keeps them
// in memory only.
func New(dir string) *Registry {
	r := &Registry{dir: dir, client: &http.Client{Timeout: fetchTimeout}, subs: map[int]func(){}, wake: make(chan struct{}, 1)}
	r.loadCached()
	return r
}

// Subscribe has f called, from any goroutine, when a list changes; cancel
// stops it.
func (r *Registry) Subscribe(f func()) (cancel func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.next
	r.next++
	r.subs[id] = f
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.subs, id)
	}
}

func (r *Registry) changed() {
	r.mu.RLock()
	subs := make([]func(), 0, len(r.subs))
	for _, f := range r.subs {
		subs = append(subs, f)
	}
	r.mu.RUnlock()
	for _, f := range subs {
		f()
	}
}

// Lookup returns the marks of the user or channel with the bare id.
func (r *Registry) Lookup(id int64) Mark {
	if r == nil || id == 0 {
		return Mark{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	d := &r.data
	var m Mark
	switch c, ok := d.custom[id]; {
	case ok:
		m.Emoji, m.Text = c.emoji, c.text
		if d.developers[id] || d.channels[id] {
			m.Arrow = Official
		} else {
			m.Arrow = Supporter
		}
	case d.developers[id] || d.channels[id]:
		m.Arrow = Official
	case d.supporters[id] || d.supporterChannels[id]:
		m.Arrow = Supporter
	}
	if f, ok := d.komaruDevelopers[id]; ok {
		m.Developer = &f
	}
	if f, ok := d.komaruSupporters[id]; ok {
		m.Supporter = &f
	}
	if s, ok := d.komaruStatuses[id]; ok {
		m.Status = &s
	}
	return m
}

// Start reads the lists now and again every hour, until ctx is done: those
// of the arrows and of the faces while enabled says so of them; Wake asks
// for a round at once, such as when it turns true.
func (r *Registry) Start(ctx context.Context, enabled func() (arrows, faces bool)) {
	go func() {
		for {
			if arrows, faces := enabled(); arrows || faces {
				func() {
					// A round that panics is tried again at the next.
					defer crash.Recover("peer badges", nil)
					r.Refresh(ctx, arrows, faces)
				}()
			}
			select {
			case <-ctx.Done():
				return
			case <-r.wake:
			case <-time.After(refreshEvery):
			}
		}
	}()
}

// Wake has Start read the lists now.
func (r *Registry) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Refresh reads the lists once: that of the arrows, and those of the
// faces. A list that cannot be read stays as it was, rather than dropping
// every mark of it.
func (r *Registry) Refresh(ctx context.Context, arrows, faces bool) {
	changed := false
	if arrows {
		body, err := r.get(ctx, ayuURL)
		if err != nil {
			body, err = r.get(ctx, exteraURL)
		}
		if err == nil && r.applyAyu(body) {
			r.save("ayu_rc.json", body)
			changed = true
		}
	}
	for _, name := range komaruNames {
		if !faces {
			break
		}
		body, err := r.get(ctx, komaruBase+name)
		if err != nil {
			continue
		}
		if r.applyKomaru(name, body) {
			r.save("komaru_"+name, body)
			changed = true
		}
	}
	if changed {
		r.changed()
	}
}

func (r *Registry) get(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", address, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxList))
}

func (r *Registry) save(name string, body []byte) {
	if r.dir == "" {
		return
	}
	if os.MkdirAll(r.dir, 0o700) != nil {
		return
	}
	path := filepath.Join(r.dir, name)
	if os.WriteFile(path+".tmp", body, 0o600) == nil {
		os.Rename(path+".tmp", path)
	}
}

func (r *Registry) loadCached() {
	if r.dir == "" {
		return
	}
	if body, err := os.ReadFile(filepath.Join(r.dir, "ayu_rc.json")); err == nil {
		r.applyAyu(body)
	}
	for _, name := range komaruNames {
		if body, err := os.ReadFile(filepath.Join(r.dir, "komaru_"+name)); err == nil {
			r.applyKomaru(name, body)
		}
	}
	// KomaruGram no longer keeps a list of partners.
	os.Remove(filepath.Join(r.dir, "komaru_partner.txt"))
}

// The three lists of KomaruGram, a file each.
var komaruNames = []string{"badges.txt", "donates.txt", "statuses.txt"}

// Apply takes body as the list called name, one of the files the lists are
// saved in: "ayu_rc.json" for the arrow and the custom badges, and
// "komaru_badges.txt", "komaru_donates.txt" and "komaru_statuses.txt" for
// the Komaru faces of developers and supporters and the emoji statuses
// KomaruGram gives. It reports whether
// the list was taken. Subscribers are not told: Refresh does that.
func (r *Registry) Apply(name string, body []byte) bool {
	if name == "ayu_rc.json" {
		return r.applyAyu(body)
	}
	if list, ok := strings.CutPrefix(name, "komaru_"); ok && slices.Contains(komaruNames, list) {
		return r.applyKomaru(list, body)
	}
	return false
}

func (r *Registry) applyAyu(body []byte) bool {
	parsed, err := parseAyu(body)
	if err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := &r.data
	d.developers, d.channels, d.supporters, d.supporterChannels, d.custom = parsed.developers, parsed.channels, parsed.supporters, parsed.supporterChannels, parsed.custom
	return true
}

func (r *Registry) applyKomaru(name string, body []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case "badges.txt":
		r.data.komaruDevelopers = parseKomaru(body)
	case "donates.txt":
		r.data.komaruSupporters = parseKomaru(body)
	case "statuses.txt":
		r.data.komaruStatuses = parseStatuses(body)
	}
	return true
}

// parseAyu reads what AyuGram's server and exteraGram's both answer: the
// developers and official channels, the supporters and their channels, and
// the custom badges, by the ids of Telegram.
func parseAyu(body []byte) (lists, error) {
	var raw struct {
		Developers        []int64 `json:"developers"`
		OfficialChannels  []int64 `json:"officialChannels"`
		Supporters        []int64 `json:"supporters"`
		SupporterChannels []int64 `json:"supporterChannels"`
		CustomBadges      []struct {
			ID    int64 `json:"id"`
			Badge struct {
				DocumentID int64  `json:"documentId"`
				Text       string `json:"text"`
			} `json:"badge"`
		} `json:"customBadges"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return lists{}, err
	}
	if len(raw.Developers)+len(raw.OfficialChannels)+len(raw.Supporters)+len(raw.SupporterChannels) == 0 {
		// An answer of another kind, or an empty one: not a list to replace
		// the marks with.
		return lists{}, errors.New("peerbadges: no one in the list")
	}
	set := func(ids []int64) map[int64]bool {
		out := make(map[int64]bool, len(ids))
		for _, id := range ids {
			if id != 0 {
				out[id] = true
			}
		}
		return out
	}
	out := lists{
		developers: set(raw.Developers), channels: set(raw.OfficialChannels),
		supporters: set(raw.Supporters), supporterChannels: set(raw.SupporterChannels),
		custom: map[int64]custom{},
	}
	for _, c := range raw.CustomBadges {
		if c.ID != 0 && c.Badge.DocumentID != 0 {
			out.custom[c.ID] = custom{emoji: c.Badge.DocumentID, text: c.Badge.Text}
		}
	}
	return out, nil
}

// komaruOrange is the color of a face whose line leaves its colors out.
var komaruOrange = color.NRGBA{R: 0xff, G: 0xad, B: 0x32, A: 0xff}

// parseKomaru reads a list of KomaruGram: a person a line, "id,particles,
// face,alpha", the colors "#rrggbb" or "#md3" for the color of the
// surrounding badges, the alpha 0 to 255; all but the id may be left out.
// Lines starting with '#' are comments. The face takes the particles' color
// when its own is left out, as KomaruGram's; the alpha is both's.
func parseKomaru(body []byte) map[int64]Face {
	out := map[int64]Face{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ",")
		id, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil || id == 0 {
			continue
		}
		col := func(i int, fallback Face) Face {
			if i >= len(parts) {
				return fallback
			}
			text := strings.TrimSpace(parts[i])
			if strings.EqualFold(text, "#md3") {
				return Face{Color: fallback.Color, Theme: true}
			}
			if len(text) == 7 && text[0] == '#' {
				if rgb, err := strconv.ParseUint(text[1:], 16, 32); err == nil {
					return Face{Color: color.NRGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xff}}
				}
			}
			return fallback
		}
		particles := col(1, Face{Color: komaruOrange})
		face := col(2, particles)
		alpha := 255
		if len(parts) > 3 {
			if a, err := strconv.Atoi(strings.TrimSpace(parts[3])); err == nil {
				alpha = min(max(a, 0), 255)
			}
		}
		face.Color.A = uint8(alpha)
		face.Particles, face.ParticlesTheme = particles.Color, particles.Theme
		face.Particles.A = uint8(alpha)
		out[id] = face
	}
	return out
}

// statusPack is an emoji set and a position in it, as statuses.txt names a
// status: "https://t.me/addemoji/EmojiKomaru/1", or "EmojiKomaru/1".
var statusPack = regexp.MustCompile(`^(?:(?:https?://)?(?:t|telegram)\.me/addemoji/)?([A-Za-z0-9_]+)/(\d+)/?$`)

// parseStatuses reads KomaruGram's list of emoji statuses: a person a line,
// "id,status,text", the status a custom emoji's id or an emoji set and a
// position in it, the text what the popup says, which may have commas or be
// left out. Lines starting with '#' are comments.
func parseStatuses(body []byte) map[int64]Status {
	out := map[int64]Status{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 3)
		id, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil || id == 0 || len(parts) < 2 {
			continue
		}
		var s Status
		ref := strings.TrimSpace(parts[1])
		if emoji, err := strconv.ParseInt(ref, 10, 64); err == nil {
			s.Emoji = emoji
		} else if m := statusPack.FindStringSubmatch(ref); m != nil {
			if i, err := strconv.Atoi(m[2]); err == nil && i > 0 {
				s.Set, s.Index = m[1], i
			}
		}
		if s.Emoji <= 0 && s.Index == 0 {
			continue
		}
		if len(parts) > 2 {
			s.Text = strings.TrimSpace(parts[2])
		}
		out[id] = s
	}
	return out
}
