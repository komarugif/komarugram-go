// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"log"
	"slices"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/checkbox"

	"gioui.org/layout"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

var animationChoices = []struct {
	kind wdk.AnimationKind
	key  string
}{
	{wdk.AnimationGIF, "gif"},
	{wdk.AnimationSticker, "sticker"},
	{wdk.AnimationAvatar, "avatar"},
	{wdk.AnimationEmoji, "emoji"},
	{wdk.AnimationBadge, "badge"},
	{wdk.AnimationGift, "gift"},
	{wdk.AnimationMessage, "message"},
	{wdk.AnimationChatAction, "chat_action"},
	{wdk.AnimationAttachmentLoading, "attachment_loading"},
	{wdk.AnimationLoading, "loading"},
	{wdk.AnimationHeight, "height"},
	{wdk.AnimationViewerAttachment, "viewer_attachment"},
	{wdk.AnimationViewer, "viewer"},
	{wdk.AnimationMenu, "menu"},
	{wdk.AnimationDialog, "dialog"},
	{wdk.AnimationRipple, "ripple"},
	{wdk.AnimationCheckboxSlider, "checkbox_slider"},
	{wdk.AnimationProgress, "progress"},
	{wdk.AnimationScrollbar, "scrollbar"},
	{wdk.AnimationListScroll, "list_scroll"},
}

type animationOptions struct {
	prefs    *preferences.Store
	boxes    *checkbox.Checkboxes[wdk.AnimationKind]
	disabled wdk.AnimationKind
}

func newAnimationOptions(prefs *preferences.Store) *animationOptions {
	v := &animationOptions{prefs: prefs}
	var all wdk.AnimationKind
	options := make([]wdk.AnimationKind, 0, len(animationChoices))
	for _, choice := range animationChoices {
		options = append(options, choice.kind)
		all |= choice.kind
	}
	v.boxes = checkbox.NewCheckboxes(options, slices.Clone(options), func(enabled []wdk.AnimationKind) {
		disabled := all
		for _, kind := range enabled {
			disabled &^= kind
		}
		if err := v.prefs.SetDisabledAnimations((v.disabled^disabled)&all, disabled); err != nil {
			log.Printf("save settings: %v", err)
			return
		}
		v.disabled = disabled
	})
	return v
}

func (v *animationOptions) Update(gtx layout.Context) {
	v.disabled = v.prefs.Global().DisabledAnimations
	enabled := make([]wdk.AnimationKind, 0, len(animationChoices))
	for _, choice := range animationChoices {
		if v.disabled&choice.kind == 0 {
			enabled = append(enabled, choice.kind)
		}
	}
	if !slices.Equal(v.boxes.GetValues(), enabled) {
		v.boxes.SetValues(enabled)
	}
	v.boxes.Update(gtx)
}

func (v *animationOptions) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	labels := make(map[wdk.AnimationKind]string, len(animationChoices))
	for _, choice := range animationChoices {
		labels[choice.kind] = l.T("animations.option." + choice.key)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("animations.options"), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 1)
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.boxes.Layout(gtx, labels) }),
	)
}

// watchAuxiliaryMotion applies the shared saving policy to windows built
// outside the main messenger view.
func watchAuxiliaryMotion(w *appwindow.Window, prefs *preferences.Store) func() {
	apply := func() {
		g := prefs.Global()
		w.Motion.ApplyPreferences(g.MotionMode, g.LowBattery, g.DisabledAnimations)
	}
	unsubscribe := prefs.Subscribe(apply)
	apply()
	return unsubscribe
}
