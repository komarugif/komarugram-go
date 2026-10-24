// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"

	"gio-mw/token"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

// ghostHidden are the boxes of what Ghost Mode hides, as AyuGram's Ghost
// Mode has them, an action of the account's to a box.
var ghostHidden = []string{"read", "online", "typing", "uploads", "recording", "stickers"}

// ghostKeeps are the Keep switches of g, in the order of ghostHidden.
func ghostKeeps(g *preferences.Ghost) []*bool {
	return []*bool{&g.KeepRead, &g.KeepOnline, &g.KeepTyping, &g.KeepUploads, &g.KeepRecording, &g.KeepStickers}
}

// ghostWithHidden is g hiding what values name.
func ghostWithHidden(g preferences.Ghost, values []string) preferences.Ghost {
	for i, keep := range ghostKeeps(&g) {
		*keep = !slices.Contains(values, ghostHidden[i])
	}
	return g
}

// ghostHiddenOf are the boxes of what g hides when on.
func ghostHiddenOf(g preferences.Ghost) []string {
	var out []string
	for i, keep := range ghostKeeps(&g) {
		if !*keep {
			out = append(out, ghostHidden[i])
		}
	}
	return out
}

// layoutGhost draws the switch of Ghost Mode, the boxes of what it hides,
// and the box that still reads a chat on an action in it. The settings may
// have changed in another window since the last frame; the switches
// follow.
func (p *settingsPage) layoutGhost(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	g := p.ghost()
	if g.On != (len(p.ghostToggle.GetValues()) == 1) {
		if g.On {
			p.ghostToggle.SetValues([]string{"ghost"})
		} else {
			p.ghostToggle.SetValues(nil)
		}
	}
	if want := ghostHiddenOf(g); !slices.Equal(want, p.ghostBoxes.GetValues()) {
		p.ghostBoxes.SetValues(want)
	}
	if g.ReadOnInteract != (len(p.ghostInteract.GetValues()) == 1) {
		if g.ReadOnInteract {
			p.ghostInteract.SetValues([]string{"interact"})
		} else {
			p.ghostInteract.SetValues(nil)
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.ghostToggle.Layout(gtx, map[string]string{"ghost": l.T("ghost.title")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("ghost.body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.ghostBoxes.Layout(gtx, map[string]string{
				"read":      l.T("ghost.read"),
				"online":    l.T("ghost.online"),
				"typing":    l.T("ghost.typing"),
				"uploads":   l.T("ghost.uploads"),
				"recording": l.T("ghost.recording"),
				"stickers":  l.T("ghost.stickers"),
			})
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.ghostInteract.Layout(gtx, map[string]string{"interact": l.T("ghost.interact")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("ghost.interact_body"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		}),
	)
}

// layoutKeep draws the switches of what the cache keeps that Telegram
// takes back.
func (p *settingsPage) layoutKeep(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	k := p.keep()
	var want []string
	if k.Deleted {
		want = append(want, "deleted")
	}
	if k.Edits {
		want = append(want, "edits")
	}
	if !slices.Equal(want, p.keepOpts.GetValues()) {
		p.keepOpts.SetValues(want)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("keep.title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("keep.body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.keepOpts.Layout(gtx, map[string]string{"deleted": l.T("keep.deleted"), "edits": l.T("keep.edits")})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("keep.deleted_body"), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 0)
		}),
	)
}
