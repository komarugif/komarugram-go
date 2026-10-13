// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

func TestServiceTexts(t *testing.T) {
	peers := func(names ...string) []model.ServicePeer {
		var out []model.ServicePeer
		for i, n := range names {
			out = append(out, model.ServicePeer{ID: int64(i + 10), Name: n})
		}
		return out
	}
	msg := func(a model.ServiceAction) model.Message {
		return model.Message{Kind: model.MessageService, SenderID: 1, SenderName: "Anna", Service: &a}
	}
	out := func(m model.Message) model.Message { m.Outgoing, m.SenderName = true, ""; return m }
	post := func(m model.Message) model.Message { m.Post, m.SenderName, m.SenderID = true, "", 0; return m }
	pinned := model.Message{Text: "A rather long pinned message"}
	photo := model.Message{Kind: model.MessagePhoto, Text: "caption"}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		lang string
		m    model.Message
		ctx  ServiceContext
		want string
	}{
		{"en", msg(model.ServiceAction{Kind: model.ServiceAddUser, Peers: []model.ServicePeer{{ID: 1, Name: "Anna"}}}), ServiceContext{}, "Anna joined the group"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceAddUser, Peers: peers("Bob")}), ServiceContext{}, "Anna added Bob"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceAddUser, Peers: peers("Bob", "Carl", "Dan")}), ServiceContext{}, "Anna added Bob, Carl and Dan"},
		{"ru", msg(model.ServiceAction{Kind: model.ServiceAddUser, Peers: peers("Боб", "Карл")}), ServiceContext{}, "Anna добавил(а) Боб и Карл"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceDeleteUser, Peers: []model.ServicePeer{{ID: 1}}}), ServiceContext{}, "Anna left the group"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceDeleteUser, Peers: peers("Bob")}), ServiceContext{}, "Anna removed Bob"},
		{"en", post(msg(model.ServiceAction{Kind: model.ServiceCreateChannel, Title: "News"})), ServiceContext{}, "Channel created"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceCreateChat, Title: "Friends"}), ServiceContext{}, "Anna created the group «Friends»"},
		{"en", post(msg(model.ServiceAction{Kind: model.ServiceEditTitle, Title: "News"})), ServiceContext{}, "Channel name was changed to «News»"},
		{"en", msg(model.ServiceAction{Kind: model.ServicePin}), ServiceContext{Pinned: &pinned}, `Anna pinned "A rather long pi…"`},
		{"en", msg(model.ServiceAction{Kind: model.ServicePin}), ServiceContext{Pinned: &photo}, "Anna pinned a photo"},
		{"en", msg(model.ServiceAction{Kind: model.ServicePin}), ServiceContext{}, "Anna pinned Loading..."},
		{"en", msg(model.ServiceAction{Kind: model.ServicePin}), ServiceContext{PinnedGone: true}, "Anna pinned Deleted message"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceHidden}), ServiceContext{}, ""},
		{"en", msg(model.ServiceAction{}), ServiceContext{}, "Service message"},
		{"en", out(msg(model.ServiceAction{Kind: model.ServicePhoneCall, Count: 754})), ServiceContext{}, "Outgoing call (12 minutes)"},
		{"ru", msg(model.ServiceAction{Kind: model.ServicePhoneCall, Reason: "missed", Video: true}), ServiceContext{}, "Пропущенный видеозвонок"},
		{"ru", out(msg(model.ServiceAction{Kind: model.ServicePhoneCall, Reason: "missed"})), ServiceContext{}, "Отменённый звонок"},
		{"ru", msg(model.ServiceAction{Kind: model.ServiceGroupCall, Count: 3 * 3600}), ServiceContext{}, "Anna завершил(а) видеочат (3 часа)"},
		{"en", post(msg(model.ServiceAction{Kind: model.ServiceGroupCall})), ServiceContext{}, "Live stream started"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceGroupCallScheduled, Date: now.Add(3 * time.Hour)}), ServiceContext{Now: now}, "Anna scheduled a video chat for today at 15:00"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceTTL, Count: 86400}), ServiceContext{}, "Anna set messages to auto-delete in 1 day"},
		{"ru", out(msg(model.ServiceAction{Kind: model.ServiceTTL, Count: 10 * 86400})), ServiceContext{}, "Вы установили автоудаление сообщений через 1 неделю 3 дня"},
		{"ru", out(msg(model.ServiceAction{Kind: model.ServiceTTL})), ServiceContext{}, "Вы отключили автоудаление сообщений"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceScreenshot}), ServiceContext{}, "Anna took a screenshot!"},
		{"en", out(msg(model.ServiceAction{Kind: model.ServiceChatTheme, Title: "🌷"})), ServiceContext{}, "You changed the chat theme to 🌷"},
		{"ru", msg(model.ServiceAction{Kind: model.ServiceBoost, Count: 3}), ServiceContext{}, "Anna усилил(а) группу 3 раза"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceStarGift, Count: 50}), ServiceContext{}, "Anna sent you a gift for 50 Stars"},
		{"en", msg(model.ServiceAction{Kind: model.ServiceCustom, Title: "Hello"}), ServiceContext{}, "Hello"},
		// A private chat's other side is named by the chat.
		{"en", model.Message{Kind: model.MessageService, Service: &model.ServiceAction{Kind: model.ServiceScreenshot}}, ServiceContext{Chat: "Bob"}, "Bob took a screenshot!"},
	} {
		if got := For(c.lang).Service(c.m, c.ctx); got != c.want {
			t.Errorf("%s %+v: got %q, want %q", c.lang, *c.m.Service, got, c.want)
		}
	}
}

// Texts from Telegram's language pack take the place of the bundled ones,
// plural forms included.
func TestServiceServerTexts(t *testing.T) {
	l := For("en").WithPack(testPack("en", map[string]string{
		"lng_action_add_user": "{from} brought {user}",
		"lng_hours#one":       "{count} hr",
		"lng_hours#other":     "{count} hrs",
	}))
	m := model.Message{Kind: model.MessageService, SenderName: "Anna", Service: &model.ServiceAction{Kind: model.ServiceAddUser, Peers: []model.ServicePeer{{ID: 2, Name: "Bob"}}}}
	if got := l.Service(m, ServiceContext{}); got != "Anna brought Bob" {
		t.Errorf("got %q", got)
	}
	m.Service = &model.ServiceAction{Kind: model.ServiceTTL, Count: 2 * 3600}
	if got := l.Service(m, ServiceContext{}); got != "Anna set messages to auto-delete in 2 hrs" {
		t.Errorf("got %q", got)
	}
}
