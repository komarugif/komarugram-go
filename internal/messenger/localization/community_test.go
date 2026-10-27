// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"testing"

	"komarugram/internal/messenger/model"
)

// The service messages of communities and paid messages are worded as
// Telegram Desktop's: by the chat itself or by one of its admins, the
// community named when known.
func TestCommunityServiceTexts(t *testing.T) {
	l := For("en")
	chat := model.MessageKey{ChatID: -100}
	for _, tc := range []struct {
		m       model.Message
		channel bool
		want    string
	}{
		{model.Message{Key: chat, SenderID: 5, SenderName: "Ann", Service: &model.ServiceAction{Kind: model.ServiceCommunity, Title: "Go"}}, false, "Ann added this group to the Go community."},
		{model.Message{Key: chat, Service: &model.ServiceAction{Kind: model.ServiceCommunity, Title: "Go"}}, true, "This channel was added to the Go community."},
		{model.Message{Key: chat, Service: &model.ServiceAction{Kind: model.ServiceCommunity}}, false, "This chat was added to a community."},
		{model.Message{Key: chat, Service: &model.ServiceAction{Kind: model.ServiceCommunity, Reason: "removed"}}, true, "This channel was removed from the community."},
		{model.Message{Key: chat, SenderID: 5, SenderName: "Ann", Service: &model.ServiceAction{Kind: model.ServiceJoinedViaCommunity, Title: "Go"}}, false, "Ann joined the group via the Go community"},
		{model.Message{Key: chat, Service: &model.ServiceAction{Kind: model.ServicePaidMessages, Reason: "allowed"}}, true, "Channel enabled Direct Messages."},
		{model.Message{Key: chat, Service: &model.ServiceAction{Kind: model.ServicePaidMessages, Count: 1}}, true, "Channel allows Direct Messages for 1 Star each."},
		{model.Message{Key: chat, Service: &model.ServiceAction{Kind: model.ServicePaidMessages, Count: 5}}, false, "Messages now cost 5 Stars each in this group."},
	} {
		if got := l.Service(tc.m, ServiceContext{Chat: "Chat", Channel: tc.channel}); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.m.Service, got, tc.want)
		}
	}
}
