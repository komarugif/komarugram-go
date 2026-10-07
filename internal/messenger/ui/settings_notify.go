// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"

	"gio-mw/widget/toggle"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

// notifySwitch is what the switch named by key turns on and off; its text
// is "notify." and the key.
func notifySwitch(n *preferences.Notify, key string) *bool {
	switch key {
	case "desktop":
		return &n.Desktop
	case "sound":
		return &n.Sound
	case "name":
		return &n.Name
	case "text":
		return &n.Text
	case "private":
		return &n.Private
	case "groups":
		return &n.Groups
	case "channels":
		return &n.Channels
	case "all_accounts":
		return &n.AllAccounts
	}
	return new(bool)
}

// notifyGroup is a card of switches, as Telegram Desktop's section has
// them; title and hint are keys of texts.
type notifyGroup struct {
	title, hint string
	keys        []string
	// accounts shows the card only with more than one account.
	accounts bool
	toggle   *toggle.Toggle[string]
	height   heightTransition
}

type notifySettings struct {
	get    func() preferences.Notify
	set    func(preferences.Notify)
	groups []*notifyGroup
	// texts are the switches' texts in language.
	texts    map[string]string
	language localization.Language
}

func newNotifySettings() *notifySettings {
	s := &notifySettings{}
	for _, g := range []notifyGroup{
		{title: "notify.global", keys: []string{"desktop", "sound"}},
		{title: "notify.shown", keys: []string{"name", "text"}},
		{title: "notify.chats", hint: "notify.chats_hint", keys: []string{"private", "groups", "channels"}},
		{title: "notify.from", hint: "notify.all_accounts_hint", keys: []string{"all_accounts"}, accounts: true},
	} {
		g.toggle = toggle.NewToggle(g.keys, nil, func(values []string) {
			if s.get == nil || s.set == nil {
				return
			}
			n := s.get()
			for _, key := range g.keys {
				*notifySwitch(&n, key) = slices.Contains(values, key)
			}
			s.set(n)
		})
		s.groups = append(s.groups, &g)
	}
	return s
}

func (s *notifySettings) subtitle(l localization.Catalog) string {
	if s.get != nil && !s.get().Desktop {
		return l.T("notify.off")
	}
	return l.T("notify.on")
}

// Layout draws the section. The switches follow the settings, which
// another window may have changed since the last frame.
func (s *notifySettings) Layout(gtx layout.Context, l localization.Catalog, accounts int) layout.Dimensions {
	if s.get == nil {
		return layout.Dimensions{}
	}
	if s.texts == nil || s.language != l.Language() {
		s.texts, s.language = map[string]string{}, l.Language()
		for _, g := range s.groups {
			for _, key := range g.keys {
				s.texts[key] = l.T("notify." + key)
			}
		}
	}
	n := s.get()
	var children []layout.FlexChild
	for _, g := range s.groups {
		if g.accounts && accounts < 2 {
			continue
		}
		var want []string
		for _, key := range g.keys {
			if *notifySwitch(&n, key) {
				want = append(want, key)
			}
		}
		if !slices.Equal(want, g.toggle.GetValues()) {
			g.toggle.SetValues(want)
		}
		hint := ""
		if g.hint != "" {
			hint = l.T(g.hint)
		}
		if len(children) > 0 {
			children = append(children, vspace(12))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return settingsChoiceCard(gtx, &g.height, l.T(g.title), hint, func(gtx layout.Context) layout.Dimensions {
				return g.toggle.Layout(gtx, s.texts)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}
