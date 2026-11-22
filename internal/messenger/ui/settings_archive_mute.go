// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"time"

	"gio-mw/token"
	"gio-mw/widget/toggle"
	"gioui.org/layout"
	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

type archiveMuteSettings struct {
	source     model.ArchiveMuteStore
	toggle     *toggle.Toggle[string]
	retry      surface
	pending    chan error
	invalidate func()
	reload     bool
	failed     bool
	saving     bool
}

func newArchiveMuteSettings(source model.ArchiveMuteStore, invalidate, requirePremium func()) *archiveMuteSettings {
	v := &archiveMuteSettings{source: source, invalidate: invalidate}
	v.toggle = toggle.NewToggle([]string{"mute"}, nil, func(values []string) {
		source := v.source
		state := source.ArchiveMute()
		on := len(values) != 0
		v.showValue(state.Enabled)
		if v.pending != nil || v.failed || !state.Known {
			return
		}
		if on && !state.CanEnable {
			requirePremium()
			return
		}
		v.run(true, func(ctx context.Context) error { return source.SetArchiveMute(ctx, on) })
	})
	return v
}

func (v *archiveMuteSettings) run(saving bool, request func(context.Context) error) {
	result := make(chan error, 1)
	v.pending, v.saving = result, saving
	invalidate := v.invalidate
	go func() {
		if invalidate != nil {
			defer invalidate()
		}
		defer crash.Recover("archive and mute settings", func(p *crash.Panic) { result <- p })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result <- request(ctx)
	}()
}

func (v *archiveMuteSettings) update(gtx layout.Context, l localization.Catalog, t *toast) {
	select {
	case err := <-v.pending:
		v.pending = nil
		if err != nil {
			v.failed = !v.saving
			key := "privacy.archive_mute_load_failed"
			if errors.Is(err, model.ErrArchiveMuteRequiresPremium) {
				key = "privacy.archive_mute_premium_required"
			} else if v.saving {
				key = "privacy.archive_mute_save_failed"
			}
			t.Show(l.T(key))
		}
	default:
	}
	if v.pending == nil && (v.retry.Clicked(gtx) || v.reload) {
		v.reload, v.failed = false, false
		v.run(false, v.source.RefreshArchiveMute)
	}
}

func (v *archiveMuteSettings) showValue(on bool) {
	if on != (len(v.toggle.GetValues()) != 0) {
		if on {
			v.toggle.SetValues([]string{"mute"})
		} else {
			v.toggle.SetValues(nil)
		}
	}
}

func (v *archiveMuteSettings) layout(gtx layout.Context, l localization.Catalog, premiumMark layout.Widget) layout.Dimensions {
	state := v.source.ArchiveMute()
	v.showValue(state.Enabled)
	v.toggle.Enable()
	if !state.Known || v.pending != nil || v.failed {
		v.toggle.Disable()
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.toggle.LayoutWithLabel(gtx, func(gtx layout.Context, _ string) layout.Dimensions {
				return withBadges(gtx, nil, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("privacy.archive_mute"), token.TypestyleBodyLarge, scheme(gtx).Surface.OnColor, 0)
				}, premiumMark)
			})
		}),
		vspace(4),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("privacy.archive_mute_body"), token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 0)
		}),
	}
	if v.failed {
		children = append(children, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return textButton(gtx, &v.retry, l.T("connection.retry"))
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}
