// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"

	"gio-mw/token"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
)

// hasVisibility reports whether the Visibility section has anything to
// show.
func (p *settingsPage) hasVisibility() bool {
	return p.calm != nil || p.hidePeerBadges != nil || p.ghost != nil || p.filtersView.filters != nil || p.keep != nil
}

// layoutCalm draws the switch that hides what distracts, and the boxes of
// what it hides. The setting may have changed in another window since the
// last frame; the switches follow.
func (p *settingsPage) layoutCalm(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	c := p.calm()
	if c.On != (len(p.calmToggle.GetValues()) == 1) {
		if c.On {
			p.calmToggle.SetValues([]string{"calm"})
		} else {
			p.calmToggle.SetValues(nil)
		}
	}
	var hidden []string
	if !c.KeepOnline {
		hidden = append(hidden, "online")
	}
	if !c.KeepRead {
		hidden = append(hidden, "read")
	}
	if !slices.Equal(hidden, p.calmBoxes.GetValues()) {
		p.calmBoxes.SetValues(hidden)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.calmToggle.Layout(gtx, map[string]string{"calm": l.T("visibility.calm")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("visibility.calm_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.calmBoxes.Layout(gtx, map[string]string{"online": l.T("visibility.calm_online"), "read": l.T("visibility.calm_read")})
		}),
	)
}

// layoutPeerBadges draws the switch that hides the marks of exteraGram,
// AyuGram and KomaruGram, and the boxes of which of them it hides. The
// settings may have changed in another window; the switches follow.
func (p *settingsPage) layoutPeerBadges(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if want := p.hidePeerBadges(); want != (len(p.hidePeerBadgesToggle.GetValues()) == 1) {
		if want {
			p.hidePeerBadgesToggle.SetValues([]string{"hide"})
		} else {
			p.hidePeerBadgesToggle.SetValues(nil)
		}
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.hidePeerBadgesToggle.Layout(gtx, map[string]string{"hide": l.T("badges.hide")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("badges.hide_body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
	}
	if p.keepPeerBadges != nil {
		var hidden []string
		arrows, faces := p.keepPeerBadges()
		if !arrows {
			hidden = append(hidden, "arrows")
		}
		if !faces {
			hidden = append(hidden, "faces")
		}
		if !slices.Equal(hidden, p.peerBadgeBoxes.GetValues()) {
			p.peerBadgeBoxes.SetValues(hidden)
		}
		children = append(children, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.peerBadgeBoxes.Layout(gtx, map[string]string{"arrows": l.T("badges.hide_arrows"), "faces": l.T("badges.hide_faces")})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}
