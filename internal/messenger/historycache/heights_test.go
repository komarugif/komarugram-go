package historycache

import (
	"bytes"
	"context"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"komarugram/internal/messenger/model"
)

func openHeights(t testing.TB, limits heightLimits) (*Cache, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history")
	c, err := Open(path, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.heightLimits = limits
	return c, path
}

// saveHeights keeps rows first..last of chat, each id px high, in env, the
// history viewed at the last of them.
func saveHeights(t *testing.T, c *Cache, chat int64, env model.RenderEnvironment, first, last int) {
	t.Helper()
	var ls []model.MessageLayout
	for id := first; id <= last; id++ {
		ls = append(ls, model.MessageLayout{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}, Environment: env, ContentRevision: 1, HeightPx: id})
	}
	if err := c.SaveView(context.Background(), model.Viewport{AccountID: "a", ChatID: chat, AnchorMessageID: model.MessageID(last)}, ls); err != nil {
		t.Fatal(err)
	}
}

func keptHeights(t *testing.T, c *Cache, chat int64, env model.RenderEnvironment) []int {
	t.Helper()
	ls, err := c.Layouts(context.Background(), chat, env)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, l := range ls {
		if l.HeightPx != int(l.Key.MessageID) && l.HeightPx != 50 {
			t.Fatalf("message %d kept %d px high", l.Key.MessageID, l.HeightPx)
		}
		ids = append(ids, int(l.Key.MessageID))
	}
	slices.Sort(ids)
	return ids
}

// A chat keeps at most its share of heights, those nearest to where its
// history was viewed; the heights of the others stay, however many the
// chat lays out, until all of them are past their bound: then the chat
// viewed longest ago goes.
func TestHeightsKeptByChat(t *testing.T) {
	c, _ := openHeights(t, heightLimits{perChat: 5, all: 10, profileBytesPerChat: 1 << 20, profileBytes: 1 << 20, envs: 8})
	env := model.RenderEnvironment{WidthPx: 696, ScaleMilli: 1000, TextScaleMilli: 1000}
	saveHeights(t, c, 2, env, 1, 2)
	for first := 1; first <= 100; first += 10 {
		saveHeights(t, c, 1, env, first, first+9)
	}
	if got := keptHeights(t, c, 1, env); !slices.Equal(got, []int{96, 97, 98, 99, 100}) {
		t.Fatalf("the large chat kept %v", got)
	}
	if got := keptHeights(t, c, 2, env); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("the small chat kept %v", got)
	}
	// All chats together keep their bound, losing the chat viewed first.
	saveHeights(t, c, 3, env, 1, 5)
	saveHeights(t, c, 4, env, 1, 5)
	if got := keptHeights(t, c, 2, env); len(got) != 0 {
		t.Fatalf("past the bound of all, the chat viewed first kept %v", got)
	}
	if got := keptHeights(t, c, 4, env); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("the chat viewed last kept %v", got)
	}
}

// The heights of the environments written to longest ago go, and with
// them their rows; the environment ids outlast the cache's closing.
func TestHeightsKeepTheLastEnvironments(t *testing.T) {
	c, path := openHeights(t, heightLimits{perChat: 100, all: 100, profileBytesPerChat: 1 << 20, profileBytes: 1 << 20, envs: 2})
	envs := []model.RenderEnvironment{{WidthPx: 300}, {WidthPx: 400}, {WidthPx: 500}}
	for _, env := range envs {
		saveHeights(t, c, 1, env, 1, 3)
	}
	if got := keptHeights(t, c, 1, envs[0]); len(got) != 0 {
		t.Fatalf("the environment written first kept %v", got)
	}
	var orphans int
	if err := c.db.QueryRow(`SELECT count(*) FROM row_heights WHERE env NOT IN (SELECT id FROM row_envs)`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("%d heights of environments gone (%v)", orphans, err)
	}
	c.Close()
	c, err := Open(path, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, env := range envs[1:] {
		if got := keptHeights(t, c, 1, env); !slices.Equal(got, []int{1, 2, 3}) {
			t.Fatalf("reopened, %+v kept %v", env, got)
		}
	}
	// Written again after the reopening, a height goes under the same id.
	saveHeights(t, c, 1, envs[2], 4, 4)
	if got := keptHeights(t, c, 1, envs[2]); !slices.Equal(got, []int{1, 2, 3, 4}) {
		t.Fatalf("written again, kept %v", got)
	}
}

// A row's profile is kept under its environment without the width: rows
// laid out at any width find it. A chat keeps its share of them.
func TestProfilesKeptWithoutTheWidth(t *testing.T) {
	// Two bytes a profile: three of them for the chat.
	c, _ := openHeights(t, heightLimits{perChat: 100, all: 100, profileBytesPerChat: 6, profileBytes: 100, envs: 8})
	ctx := context.Background()
	at := func(width int) model.RenderEnvironment {
		return model.RenderEnvironment{WidthPx: width, ScaleMilli: 1000, TextScaleMilli: 1000, Locale: "en"}
	}
	var ls []model.MessageLayout
	for id := 1; id <= 5; id++ {
		ls = append(ls, model.MessageLayout{Key: model.MessageKey{AccountID: "a", ChatID: 1, MessageID: model.MessageID(id)}, Environment: at(400), ContentRevision: uint64(10 + id), HeightPx: 50, Profile: []byte{1, byte(id)}})
	}
	// One with no profile: its height is kept, no profile is.
	ls = append(ls, model.MessageLayout{Key: model.MessageKey{AccountID: "a", ChatID: 1, MessageID: 6}, Environment: at(400), ContentRevision: 16, HeightPx: 50})
	if err := c.SaveView(ctx, model.Viewport{AccountID: "a", ChatID: 1, AnchorMessageID: 5}, ls); err != nil {
		t.Fatal(err)
	}
	profiled, err := c.Profiled(ctx, 1, at(650), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := map[model.MessageID]uint64{3: 13, 4: 14, 5: 15}
	if !maps.Equal(profiled, want) {
		t.Fatalf("profiled at another width: %v, want %v (the three nearest the view)", profiled, want)
	}
	data, err := c.Profiles(ctx, 1, at(0))
	if err != nil || !bytes.Equal(data[4], []byte{1, 4}) || len(data) != 3 {
		t.Fatalf("profiles %v, %v", data, err)
	}
	other := at(400)
	other.Locale = "ru"
	if profiled, _ := c.Profiled(ctx, 1, other, 1); len(profiled) != 0 {
		t.Fatalf("another environment found %v", profiled)
	}
	// Profiles of another version of the encoding are as none.
	if profiled, _ := c.Profiled(ctx, 1, at(400), 2); len(profiled) != 0 {
		t.Fatalf("another environment found %v", profiled)
	}
	if got := keptHeights(t, c, 1, at(400)); len(got) != 6 {
		t.Fatalf("heights kept %v", got)
	}
}

// profileRows saves a two-byte profile of each of rows first..last of chat,
// viewed at anchor.
func profileRows(t *testing.T, c *Cache, chat int64, first, last int, anchor model.MessageID) {
	t.Helper()
	var ls []model.MessageLayout
	for id := first; id <= last; id++ {
		ls = append(ls, model.MessageLayout{Key: model.MessageKey{AccountID: "a", ChatID: chat, MessageID: model.MessageID(id)}, Environment: model.RenderEnvironment{WidthPx: 400}, ContentRevision: 1, HeightPx: 50, Profile: []byte{1, 2}})
	}
	if err := c.SaveView(context.Background(), model.Viewport{AccountID: "a", ChatID: chat, AnchorMessageID: anchor}, ls); err != nil {
		t.Fatal(err)
	}
}

func profiledRows(t *testing.T, c *Cache, chat int64) []int {
	t.Helper()
	profiled, err := c.Profiled(context.Background(), chat, model.RenderEnvironment{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for id := range profiled {
		ids = append(ids, int(id))
	}
	slices.Sort(ids)
	return ids
}

// A chat past its bytes loses the profiles farthest from where it was
// viewed, older and newer alike.
func TestProfilesKeptAroundTheView(t *testing.T) {
	c, _ := openHeights(t, heightLimits{perChat: 100, all: 100, profileBytesPerChat: 8, profileBytes: 100, envs: 8})
	profileRows(t, c, 1, 1, 10, 5)
	if got := profiledRows(t, c, 1); !slices.Equal(got, []int{4, 5, 6, 7}) {
		t.Fatalf("viewed at 5, the chat kept %v", got)
	}
}

// All the chats past their bytes lose the chat viewed longest ago, whole;
// the one viewed now stays, however much of it there is.
func TestProfilesLoseTheChatViewedFirst(t *testing.T) {
	c, _ := openHeights(t, heightLimits{perChat: 100, all: 100, profileBytesPerChat: 100, profileBytes: 12, envs: 8})
	profileRows(t, c, 1, 1, 3, 3)
	profileRows(t, c, 2, 1, 3, 3)
	if got := profiledRows(t, c, 1); len(got) != 3 {
		t.Fatalf("within the bound, chat 1 kept %v", got)
	}
	profileRows(t, c, 3, 1, 3, 3)
	if got := profiledRows(t, c, 1); len(got) != 0 {
		t.Fatalf("past the bound, the chat viewed first kept %v", got)
	}
	if got := profiledRows(t, c, 2); len(got) != 3 {
		t.Fatalf("chat 2 kept %v", got)
	}
	// Viewed again, chat 2 is the last viewed: chat 3 goes next.
	profileRows(t, c, 2, 4, 4, 4)
	profileRows(t, c, 1, 1, 1, 1)
	if got := profiledRows(t, c, 3); len(got) != 0 {
		t.Fatalf("chat 3, viewed before chat 2 was again, kept %v", got)
	}
	if got := profiledRows(t, c, 2); len(got) != 4 {
		t.Fatalf("chat 2 kept %v", got)
	}
	// Alone over the bound, the chat viewed now keeps all of its rows.
	profileRows(t, c, 4, 1, 20, 1)
	if got := profiledRows(t, c, 4); len(got) != 20 {
		t.Fatalf("the chat viewed now kept %v", got)
	}
}
