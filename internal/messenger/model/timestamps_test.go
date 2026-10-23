// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"testing"
	"time"
)

// stamps are the texts of the timestamps TimestampEntities finds in text.
func stamps(text string, entities []Entity, limit time.Duration) []string {
	var out []string
	for _, r := range TextRuns(text, TimestampEntities(text, entities, limit)) {
		if r.Action == "timestamp" {
			out = append(out, r.Value)
		}
	}
	return out
}

func TestTimestampEntities(t *testing.T) {
	hour := time.Hour
	for _, c := range []struct {
		text  string
		limit time.Duration
		want  []string
	}{
		{"at 1:23 and 12:34", hour, []string{"1:23", "12:34"}},
		{"1:02:03", 2 * hour, []string{"1:02:03"}},
		{"12:34:56", 13 * hour, []string{"12:34:56"}},
		{"(0:15), [0:16] \"0:17\" 0:18, 0:19. -0:20- 0:21+", hour, []string{"0:15", "0:16", "0:17", "0:18", "0:19", "0:20", "0:21"}},
		// Glued to other characters it is no timestamp.
		{"a1:23 1:23b 123:45 1:234 1:2 x:1:23 1:23:4", hour, nil},
		// Seconds and the minutes of an hour stop at 59.
		{"1:60 1:60:00 1:59:59", 2 * hour, []string{"1:59:59"}},
		// Past the end of the media, it names nothing.
		{"0:59 1:00 1:01", time.Minute, []string{"0:59", "1:00"}},
		// After a character of two UTF-16 units, its offset counts both.
		{"🎬 2:05", hour, []string{"2:05"}},
		{"no media 1:23", 0, nil},
	} {
		got := stamps(c.text, nil, c.limit)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %q, want %q", c.text, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %q, want %q", c.text, got, c.want)
			}
		}
	}
}

func TestTimestampEntitiesOtherEntities(t *testing.T) {
	text := "1:00 1:01 1:02 1:03"
	got := stamps(text, []Entity{
		{Kind: "spoiler", Offset: 0, Length: 4},
		{Kind: "bold", Offset: 5, Length: 4},
		{Kind: "quote", Offset: 10, Length: 9},
		{Kind: "code", Offset: 15, Length: 2},
	}, time.Hour)
	// Bold and code keep theirs as text; a spoiler and a quote do not.
	want := []string{"1:00", "1:02"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseTimestamp(t *testing.T) {
	for s, want := range map[string]time.Duration{
		"1:23":     83 * time.Second,
		"12:34":    754 * time.Second,
		"1:02:03":  3723 * time.Second,
		"10:00:00": 10 * time.Hour,
	} {
		if got, ok := ParseTimestamp(s); !ok || got != want {
			t.Errorf("%q: %v %v, want %v", s, got, ok, want)
		}
	}
	for _, s := range []string{"1:2", "123:45", "1:60", "1:60:00", "1:2:03", ":12", "1:23:", "a:bc"} {
		if _, ok := ParseTimestamp(s); ok {
			t.Errorf("%q parsed", s)
		}
	}
}

func TestTimestampURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://youtu.be/abc":                        "https://youtu.be/abc?t=83",
		"https://www.youtube.com/watch?v=abc":         "https://www.youtube.com/watch?v=abc&t=83",
		"https://www.youtube.com/watch?v=abc&t=10#x":  "https://www.youtube.com/watch?v=abc&t=83#x",
		"https://www.youtube.com/watch?t=1s&v=abc&x=": "https://www.youtube.com/watch?v=abc&x=&t=83",
	} {
		if got := TimestampURL(url, 83*time.Second); got != want {
			t.Errorf("%s: %s, want %s", url, got, want)
		}
	}
}

func TestTimestampLimit(t *testing.T) {
	video := &MessageMedia{Duration: 90*time.Second + 500*time.Millisecond}
	for _, c := range []struct {
		name string
		m    Message
		want time.Duration
	}{
		{"video", Message{Kind: MessageVideo, Media: video}, 90 * time.Second},
		{"voice", Message{Kind: MessageVoice, Media: video}, 90 * time.Second},
		{"music", Message{Kind: MessageMusic, Media: video}, 90 * time.Second},
		{"round", Message{Kind: MessageRound, Media: video}, 90 * time.Second},
		// Unlike Telegram Desktop's, a GIF has timestamps too.
		{"gif", Message{Kind: MessageGIF, Media: video}, 90 * time.Second},
		{"gif of no length", Message{Kind: MessageGIF, Media: &MessageMedia{MIMEType: "image/gif"}}, 0},
		{"photo", Message{Kind: MessagePhoto, Media: &MessageMedia{}}, 0},
		{"youtube", Message{WebPage: &WebPreview{Site: "YouTube", Type: "video", Duration: 600}}, 10 * time.Minute},
		{"youtube of no length", Message{WebPage: &WebPreview{Site: "YouTube", Type: "video"}}, maxYouTubeTimestamp},
		{"youtube cached before its type", Message{WebPage: &WebPreview{Site: "YouTube", URL: "https://youtu.be/abc"}}, maxYouTubeTimestamp},
		{"youtube channel", Message{WebPage: &WebPreview{Site: "YouTube", URL: "https://www.youtube.com/@channel"}}, 0},
		{"article", Message{WebPage: &WebPreview{Site: "Example", Type: "article"}}, 0},
		{"page's video", Message{WebPage: &WebPreview{Site: "YouTube", Video: video, VideoKind: MessageVideo}}, 90 * time.Second},
	} {
		if got := TimestampLimit(c.m); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
