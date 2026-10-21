// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxYouTubeTimestamp is the latest time a timestamp of a YouTube video of
// no known length may name, as in Telegram Desktop: 100 hours.
const maxYouTubeTimestamp = 100 * time.Hour

// TimestampLimit is the latest time a timestamp in a text may name in the
// media of m, the message the text is the caption of or replies to: the
// length of its video, video message, voice message or music, or of the
// YouTube video it links to. It is 0 when m has no such media, and then no
// timestamp links to it, as Telegram Desktop's DurationForTimestampLinks.
func TimestampLimit(m Message) time.Duration {
	if w := m.WebPage; w != nil {
		if w.Video != nil {
			if w.VideoKind != MessageVideo {
				return 0
			}
			return w.Video.Duration.Truncate(time.Second)
		}
		if !w.YouTubeVideo() {
			return 0
		}
		if w.Duration > 0 {
			return time.Duration(w.Duration) * time.Second
		}
		return maxYouTubeTimestamp
	}
	switch m.Kind {
	case MessageVideo, MessageRound, MessageVoice, MessageMusic:
		if m.Media != nil {
			return m.Media.Duration.Truncate(time.Second)
		}
	}
	return 0
}

// YouTubeVideo reports whether the page is a video of YouTube, which a
// timestamp opens at its time. Pages cached before their type was kept are
// told by their URL.
func (w *WebPreview) YouTubeVideo() bool {
	if w.Site != "YouTube" {
		return false
	}
	if w.Type != "" {
		return w.Type == "video"
	}
	u := strings.ToLower(w.URL)
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	u = strings.TrimPrefix(strings.TrimPrefix(u, "www."), "m.")
	return strings.HasPrefix(u, "youtu.be/") || strings.HasPrefix(u, "youtube.com/watch") ||
		strings.HasPrefix(u, "youtube.com/shorts/") || strings.HasPrefix(u, "youtube.com/live/")
}

// TimestampURL is url, a page of a video, opened at second at: with its
// own t= parameters replaced by t=at, its fragment kept, as Telegram
// Desktop's TimestampLinkBase writes it.
func TimestampURL(url string, at time.Duration) string {
	base, fragment, hasFragment := strings.Cut(url, "#")
	path, query, _ := strings.Cut(base, "?")
	var params []string
	if query != "" {
		for p := range strings.SplitSeq(query, "&") {
			if !strings.HasPrefix(p, "t=") {
				params = append(params, p)
			}
		}
	}
	params = append(params, "t="+strconv.Itoa(int(at/time.Second)))
	out := path + "?" + strings.Join(params, "&")
	if hasFragment {
		out += "#" + fragment
	}
	return out
}

// MayHaveTimestamps reports whether text has a digit, a colon and two
// digits, as every timestamp does: the text of most messages has none, and
// is never looked at again.
func MayHaveTimestamps(text string) bool {
	for i := 0; ; {
		j := strings.IndexByte(text[i:], ':')
		if j < 0 {
			return false
		}
		i += j
		if i > 0 && isDigit(text[i-1]) && i+2 < len(text) && isDigit(text[i+1]) && isDigit(text[i+2]) {
			return true
		}
		i++
	}
}

// ParseTimestamp is the time a timestamp names: "m:ss", "mm:ss", "h:mm:ss"
// or "hh:mm:ss".
func ParseTimestamp(s string) (time.Duration, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var values []int
	for i, p := range parts {
		// The first part has one digit or two, the others two.
		if p == "" || len(p) > 2 || i > 0 && len(p) != 2 || !allDigits(p) {
			return 0, false
		}
		v, _ := strconv.Atoi(p)
		values = append(values, v)
	}
	n := len(values)
	if values[n-1] >= 60 || n == 3 && values[1] >= 60 {
		return 0, false
	}
	seconds := values[n-1] + values[n-2]*60
	if n == 3 {
		seconds += values[0] * 3600
	}
	return time.Duration(seconds) * time.Second, true
}

// TimestampEntities are entities with those of the timestamps in text
// that name a time up to limit added, of kind "timestamp", as Telegram
// Desktop's AddTimestampLinks finds them: "1:23", "12:34" or "1:23:45"
// standing alone or between spaces, brackets, quotes, commas, dots or
// dashes, and not in another entity but a spoiler or a quote. A click on
// one seeks the media it names a time of.
func TimestampEntities(text string, entities []Entity, limit time.Duration) []Entity {
	if limit <= 0 || !MayHaveTimestamps(text) {
		return entities
	}
	var found []Entity
	units, prev := 0, rune(-1)
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if isDigit(text[i]) && timestampBefore(prev) {
			if n := timestampAt(text[i:]); n > 0 {
				at, ok := ParseTimestamp(text[i : i+n])
				// Its characters are all ASCII: as many UTF-16 units as bytes.
				if ok && at <= limit && !timestampCovered(entities, units, units+n) {
					found = append(found, Entity{Kind: "timestamp", Offset: units, Length: n})
				}
				units += n
				prev = rune(text[i+n-1])
				i += n
				continue
			}
		}
		units += utf16Len(r)
		prev = r
		i += size
	}
	if len(found) == 0 {
		return entities
	}
	return append(append(make([]Entity, 0, len(entities)+len(found)), entities...), found...)
}

// timestampAt is the length of the timestamp s starts with, followed by
// what may follow one, or 0. Of the forms it takes, the longest is tried
// first, as the regular expression of Telegram Desktop does.
func timestampAt(s string) int {
	digits := func(at int) int {
		n := 0
		for at+n < len(s) && isDigit(s[at+n]) {
			n++
		}
		return n
	}
	// h:mm:ss and hh:mm:ss.
	for _, h := range []int{2, 1} {
		if digits(0) >= h && len(s) > h && s[h] == ':' && digits(h+1) >= 2 {
			if n := h + 3; len(s) > n+2 && s[n] == ':' && digits(n+1) >= 2 && timestampAfter(s, n+3) {
				return n + 3
			}
		}
	}
	// mm:ss and m:ss.
	for _, m := range []int{2, 1} {
		if digits(0) >= m && len(s) > m && s[m] == ':' && digits(m+1) >= 2 && timestampAfter(s, m+3) {
			return m + 3
		}
	}
	return 0
}

// timestampBefore reports whether a timestamp may come after r; -1 is the
// start of the text.
func timestampBefore(r rune) bool {
	return r < 0 || unicode.IsSpace(r) || strings.ContainsRune("()[]\",.-", r)
}

// timestampAfter reports whether a timestamp may end at s[end].
func timestampAfter(s string, end int) bool {
	if end > len(s) {
		return false
	}
	if end == len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[end:])
	return unicode.IsSpace(r) || strings.ContainsRune("()[]\",.-+", r)
}

// timestampCovered reports whether an entity a timestamp is not linked in
// overlaps from..till: any but a spoiler or a quote.
func timestampCovered(entities []Entity, from, till int) bool {
	for _, e := range entities {
		if e.Kind == "spoiler" || e.Kind == "quote" {
			continue
		}
		if e.Offset < till && e.Offset+e.Length > from {
			return true
		}
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func utf16Len(r rune) int {
	if r > 0xffff {
		return 2
	}
	return 1
}
