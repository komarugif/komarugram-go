// SPDX-License-Identifier: Unlicense OR MIT

package wdk

import "gioui.org/layout"

const AnimationsNamespace = "gio-mw.wdk.Animations"

// AnimationKind identifies an independently configurable animation.
// A mask of disabled kinds leaves animations enabled by default.
type AnimationKind uint32

// These bit positions are persisted by the messenger. Append new kinds.
const (
	AnimationGIF AnimationKind = 1 << iota
	AnimationSticker
	AnimationAvatar
	AnimationEmoji
	AnimationBadge
	AnimationGift
	AnimationMessage
	AnimationChatAction
	AnimationAttachmentLoading
	AnimationLoading
	AnimationHeight
	AnimationViewerAttachment
	AnimationViewer
	AnimationMenu
	AnimationDialog
	AnimationRipple
	AnimationProgress
	AnimationScrollbar
	AnimationListScroll
	AnimationCheckboxSlider
)

const disabledAnimationsNamespace = "gio-mw.wdk.DisabledAnimations"

func SetDisabledAnimations(gtx layout.Context, disabled AnimationKind) {
	gtx.Values[disabledAnimationsNamespace] = disabled
}

// AnimationAllowed reports whether the kind is allowed independently of
// the global setting, for interactions that can play with autoplay off.
func AnimationAllowed(gtx layout.Context, kind AnimationKind) bool {
	disabled, _ := gtx.Values[disabledAnimationsNamespace].(AnimationKind)
	return disabled&kind == 0
}

func AnimationEnabled(gtx layout.Context, kind AnimationKind) bool {
	return AnimationsEnabled(gtx) && AnimationAllowed(gtx, kind)
}

// SetAnimationsEnabled turns widget animations on or off for the frame.
// When they are off, animated values jump straight to their targets.
// Animations are enabled unless this is called with false.
func SetAnimationsEnabled(gtx layout.Context, enabled bool) {
	gtx.Values[AnimationsNamespace] = enabled
}

// AnimationsEnabled reports whether widgets should animate in this frame.
func AnimationsEnabled(gtx layout.Context) bool {
	if enabled, ok := gtx.Values[AnimationsNamespace].(bool); ok {
		return enabled
	}
	return true
}
