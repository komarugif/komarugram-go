// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/widget/indicator"

	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// chatMessages is the last row of the info's card of details: how many
// messages the chat has, as Telegram counts them, and how many the local
// cache holds. A click saves those the cache lacks, in a dialog that tells
// how far it has come (model.ChatArchiver).
type chatMessages struct {
	item settingsItem
	// known is set once the cache's count has come; total is below 0 until
	// Telegram has told its own.
	known         bool
	cached, total int
	result        chan messageCountsResult
	cancel        context.CancelFunc
	// dialog is the one that shows the saving, while it goes on.
	dialog archiveDialog
}

// messageCountsResult is the counts of a chat as they came.
type messageCountsResult struct {
	chat          int64
	cached, total int
	// hasCached and hasTotal tell which of them were read.
	hasCached, hasTotal bool
}

// archiveDialog is the dialog of a saving under way, as the one that saves
// a video as a GIF is, and its button stops it.
type archiveDialog struct {
	modal  modal
	bar    *indicator.Indicator
	cancel surface
	// job is the saving under way, nil while there is none.
	job *archiveJob
}

// archiveJob is a saving of a chat's messages under way, which tells how far
// it has come from its goroutine.
type archiveJob struct {
	stop     context.CancelFunc
	done     chan error
	mu       sync.Mutex
	progress model.ArchiveProgress
}

func (j *archiveJob) report(p model.ArchiveProgress) {
	j.mu.Lock()
	j.progress = p
	j.mu.Unlock()
}

func (j *archiveJob) state() model.ArchiveProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progress
}

// messagesShown tells whether the info has the row of the chat's messages.
func (p *chatInfo) messagesShown() bool {
	return p.archiver != nil && p.chat.ID != 0 && !p.closed && p.chat.Kind != model.KindCommunity
}

// askMessageCounts has the counts of the chat's messages read: the cache's at
// once, and Telegram's, which a search for "-" tells, after them.
func (p *chatInfo) askMessageCounts(withTotal bool) {
	m := &p.msgs
	if !p.messagesShown() {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	m.cancel = cancel
	if m.result == nil {
		m.result = make(chan messageCountsResult, 4)
	}
	chat, archiver, results, invalidate := p.chat.ID, p.archiver, m.result, p.invalidate
	go func() {
		defer crash.Recover("message counts", nil)
		defer cancel()
		send := func(r messageCountsResult) {
			r.chat = chat
			select {
			case results <- r:
			default:
			}
			invalidate()
		}
		if n, err := archiver.CachedMessages(ctx, chat); err == nil {
			send(messageCountsResult{cached: n, hasCached: true})
		}
		if withTotal {
			if n, err := archiver.MessageTotal(ctx, chat); err == nil {
				send(messageCountsResult{total: n, hasTotal: true})
			}
		}
	}()
}

// drainMessageCounts takes the counts that came.
func (p *chatInfo) drainMessageCounts() {
	m := &p.msgs
	for {
		select {
		case r := <-m.result:
			if r.chat != p.chat.ID {
				continue
			}
			if r.hasCached {
				m.cached, m.known = r.cached, true
			}
			if r.hasTotal {
				m.total = r.total
			}
		default:
			return
		}
	}
}

// resetMessages forgets the counts of the chat that was open, and stops a
// saving of its messages.
func (p *chatInfo) resetMessages() {
	m := &p.msgs
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.known, m.cached, m.total = false, 0, -1
	m.result = nil
	p.stopArchive()
}

// messagesText is the row's title: "n messages (n in the local cache)", or,
// until Telegram has told its count, the cache's alone.
func (p *chatInfo) messagesText(l localization.Catalog) string {
	m := &p.msgs
	if !p.messagesShown() || !m.known {
		return ""
	}
	cached, total := m.cached, m.total
	if job := m.dialog.job; job != nil {
		// The cache's count follows the saving.
		cached = max(cached, job.state().Cached)
	}
	if total < 0 {
		return l.Count("info.messages_cached", cached, map[string]string{"count": groupDigits(cached)})
	}
	return l.Count("info.messages", total, map[string]string{"count": groupDigits(total), "cached": groupDigits(cached)})
}

// startArchive begins saving the chat's messages, and opens the dialog.
func (p *chatInfo) startArchive() {
	a := &p.msgs.dialog
	if p.archiver == nil || a.job != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &archiveJob{stop: cancel, done: make(chan error, 1), progress: model.ArchiveProgress{Cached: p.msgs.cached, Total: p.msgs.total}}
	a.job, a.bar = job, indicator.Linear()
	a.modal.Open()
	chat, archiver, invalidate := p.chat.ID, p.archiver, p.invalidate
	go func() {
		defer crash.Recover("archive chat", func(e *crash.Panic) { job.done <- e; invalidate() })
		job.done <- archiver.ArchiveChat(ctx, chat, func(pr model.ArchiveProgress) {
			job.report(pr)
			invalidate()
		})
		invalidate()
	}()
}

// stopArchive stops the saving under way, as the info closes.
func (p *chatInfo) stopArchive() {
	a := &p.msgs.dialog
	if a.job != nil {
		a.job.stop()
		a.job = nil
	}
	a.modal.Hide()
}

// updateArchive takes the click on the row and on the dialog's button, and
// the end of the saving, before anything is drawn.
func (p *chatInfo) updateArchive(gtx layout.Context, l localization.Catalog) {
	p.drainMessageCounts()
	a := &p.msgs.dialog
	if p.messagesShown() && p.msgs.item.click.Clicked(gtx) && a.job == nil && p.messagesText(l) != "" {
		p.startArchive()
	}
	if a.job == nil {
		return
	}
	if a.cancel.Clicked(gtx) {
		a.job.stop()
	}
	select {
	case err := <-a.job.done:
		a.job.stop()
		a.job = nil
		a.modal.Close()
		// What it saved stays, whether it ended or was stopped.
		p.askMessageCounts(false)
		switch {
		case err == nil:
			p.modal.Toast(l.T("archive.done"))
		case errors.Is(err, context.Canceled):
		default:
			p.modal.Toast(l.T("archive.failed") + ": " + mediaErrorText(err))
		}
	default:
	}
}

// archiveStatus says how far the saving has come.
func archiveStatus(p model.ArchiveProgress, l localization.Catalog) string {
	switch {
	case p.Wait > 0:
		return l.Format("archive.waiting", map[string]string{"time": waitText(p.Wait, l)})
	case p.Total > 0:
		return l.Format("archive.status", map[string]string{"cached": groupDigits(p.Cached), "total": groupDigits(p.Total)})
	}
	return l.Format("archive.status_unknown", map[string]string{"cached": groupDigits(p.Cached)})
}

// layoutArchive draws the dialog of the saving over the info while it is
// shown.
func (p *chatInfo) layoutArchive(gtx layout.Context, l localization.Catalog) {
	a := &p.msgs.dialog
	if !a.modal.Shown() {
		return
	}
	progress := model.ArchiveProgress{Cached: p.msgs.cached, Total: p.msgs.total}
	if a.job != nil {
		progress = a.job.state()
	}
	shown := a.modal.Layout(gtx, a.job != nil, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return a.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("archive.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("archive.about"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(12),
			}
			children = append(children, progressRows(a.bar, archiveStatus(progress, l), progress.Share())...)
			children = append(children, vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &a.cancel, l.T("archive.cancel"))
						}),
					)
				}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}, defaultCardPadding)
	})
	if !shown {
		a.modal = modal{}
	}
}
