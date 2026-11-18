// SPDX-License-Identifier: Unlicense OR MIT

package indicator

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"time"

	"gioui.org/layout"
)

type kind int

const (
	linearKind kind = iota
	circularKind
)

type Indicator struct {
	started       time.Time
	Progress      float32
	indeterminate bool
	kind          kind
	progress      wdk.FloatTween
}

func (i *Indicator) Layout(gtx layout.Context) layout.Dimensions {
	if i.indeterminate && i.kind == circularKind {
		return i.circularIndeterminate(gtx)
	}
	if i.Progress < 0 {
		i.Progress = 0
	}
	if i.Progress > 1 {
		i.Progress = 1
	}
	if i.progress.Duration == 0 {
		i.progress.Duration = token.DurationMedium1
	}
	progress := i.progress.AnimateFor(gtx, wdk.AnimationProgress, i.Progress)
	if progress < 0.001 {
		// Avoid bug: gioui.org/issue/655
		progress = 0
	}

	style := widgetStyle{
		iIndeterminate: i.indeterminate,
		iProgress:      progress,
		iType:          i.kind,
		iTheme:         BuildTheme(gtx),
	}
	return style.layout(gtx)
}
