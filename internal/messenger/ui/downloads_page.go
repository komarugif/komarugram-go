// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"sort"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

// The Downloads section: the files the user downloaded, as Telegram
// Desktop's Downloads list them (info_downloads_widget.cpp), the ones on
// their way above. Each opens or goes to its message, and the header shows
// the folder they are all downloaded into;
// any can be picked and deleted from the disk, where they stay otherwise:
// they are the user's, and the cache's budget does not count them. Sorting
// by size shows what takes the most.

var (
	iconDownloads  = iconDownload
	iconShowFolder = wdk.RequireIconWidget(icons.FileFolderOpen)
	iconViewInChat = wdk.RequireIconWidget(icons.CommunicationChat)
)

// downloadsItemSize is the height of a download's row.
const downloadsItemSize = unit.Dp(64)

type downloadsPage struct {
	source     model.Downloader
	files      func() *attachmentFiles
	invalidate func()
	// openMessage goes to the message a file is of.
	openMessage func(model.DownloadedFile)
	// dir is the folder files are downloaded into.
	dir    func() string
	folder surface

	page scrollPage

	mu      sync.Mutex
	list    []model.DownloadedFile
	loaded  bool
	loading bool
	err     error
	// seen is the count of downloads ended when the list was read.
	seen int64

	bySize             bool
	byDate, bySizeChip folderChip
	rows               map[int64]*downloadRow
	active             map[model.MessageKey]*activeRow
	selected           map[int64]bool
	deleteSelected     surface
	clearSelection     surface
	loader             loadingIndicator

	confirm       modal
	confirmDelete surface
	confirmCancel surface
	pending       []int64
	told          string
}

type downloadRow struct {
	row, mark, chat surface
}

type activeRow struct {
	cancel surface
	ring   downloadRing
}

func newDownloadsPage(source model.Downloader, invalidate func()) *downloadsPage {
	return &downloadsPage{source: source, invalidate: invalidate, dir: appDownloadsDir, rows: map[int64]*downloadRow{},
		active: map[model.MessageKey]*activeRow{}, selected: map[int64]bool{}}
}

// open reads the list again, as the section opens.
func (p *downloadsPage) open() {
	p.selected = map[int64]bool{}
	p.reload()
}

// reload asks the store for the list, unless it is asking already.
func (p *downloadsPage) reload() {
	if p.source == nil {
		return
	}
	p.mu.Lock()
	if p.loading {
		p.mu.Unlock()
		return
	}
	p.loading = true
	if files := p.attachments(); files != nil {
		p.seen = files.finished.Load()
	}
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, err := p.source.Downloads(ctx)
		p.mu.Lock()
		p.loading, p.err = false, err
		if err == nil {
			p.list, p.loaded = list, true
		}
		p.mu.Unlock()
		if p.invalidate != nil {
			p.invalidate()
		}
	}()
}

func (p *downloadsPage) attachments() *attachmentFiles {
	if p.files == nil {
		return nil
	}
	return p.files()
}

// shown are the downloads in the order chosen: the newest first, or the
// largest.
func (p *downloadsPage) shown() ([]model.DownloadedFile, bool, error) {
	p.mu.Lock()
	list := append([]model.DownloadedFile(nil), p.list...)
	loaded, err := p.loaded, p.err
	p.mu.Unlock()
	if p.bySize {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Size > list[j].Size })
	}
	return list, loaded, err
}

func (p *downloadsPage) row(id int64) *downloadRow {
	r := p.rows[id]
	if r == nil {
		r = new(downloadRow)
		p.rows[id] = r
	}
	return r
}

func (p *downloadsPage) activeRow(key model.MessageKey) *activeRow {
	r := p.active[key]
	if r == nil {
		r = new(activeRow)
		p.active[key] = r
	}
	return r
}

// Update handles the rows, the sorting, the selection and the dialog, and
// reads the list again when a download has ended.
func (p *downloadsPage) Update(gtx layout.Context, l localization.Catalog) {
	if files := p.attachments(); files != nil {
		p.mu.Lock()
		changed := files.finished.Load() != p.seen
		p.mu.Unlock()
		if changed {
			p.reload()
		}
		for _, d := range files.active() {
			if p.activeRow(d.message.Key).cancel.Clicked(gtx) {
				files.cancelDownload(d.message.Key)
			}
		}
	}
	if p.byDate.Clicked(gtx) {
		p.bySize = false
	}
	if p.bySizeChip.Clicked(gtx) {
		p.bySize = true
	}
	list, _, _ := p.shown()
	for _, d := range list {
		r := p.row(d.ID)
		// A click on a mark or a button is the row's too: the button's
		// action is the one taken.
		mark, chat := r.mark.Clicked(gtx), r.chat.Clicked(gtx)
		row := r.row.Clicked(gtx)
		switch {
		case chat:
			if p.openMessage != nil {
				p.openMessage(d)
			}
		case mark || (row && len(p.selected) > 0):
			// While files are picked, a click on one picks it too.
			if p.selected[d.ID] {
				delete(p.selected, d.ID)
			} else {
				p.selected[d.ID] = true
			}
		case row && d.State == model.DownloadMissing:
			p.page.toast.Show(l.T("downloads.missing"))
		case row:
			path := d.Path
			go func() {
				if err := openBrowser(path); err != nil {
					p.page.toast.Show(mediaErrorText(err))
				}
			}()
		}
	}
	// What was picked and is gone is no longer picked.
	for id := range p.selected {
		if p.rows[id] == nil || !containsDownload(list, id) {
			delete(p.selected, id)
		}
	}
	if p.folder.Clicked(gtx) {
		dir := p.dir()
		go func() {
			// It is there before the first download, to be shown.
			err := os.MkdirAll(dir, 0o755)
			if err == nil {
				err = openBrowser(dir)
			}
			if err != nil {
				p.page.toast.Show(mediaErrorText(err))
			}
		}()
	}
	if p.clearSelection.Clicked(gtx) {
		p.selected = map[int64]bool{}
	}
	if p.deleteSelected.Clicked(gtx) && len(p.selected) > 0 {
		p.pending = p.pending[:0]
		for _, d := range list {
			if p.selected[d.ID] {
				p.pending = append(p.pending, d.ID)
			}
		}
		p.confirm.Open()
	}
	if p.confirmCancel.Clicked(gtx) {
		p.confirm.Close()
	}
	if p.confirmDelete.Clicked(gtx) {
		p.confirm.Close()
		ids := append([]int64(nil), p.pending...)
		p.selected = map[int64]bool{}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := p.source.DeleteDownloads(ctx, ids); err != nil {
				p.page.toast.Show(mediaErrorText(err))
			}
			p.reload()
		}()
	}
}

func containsDownload(list []model.DownloadedFile, id int64) bool {
	for _, d := range list {
		if d.ID == id {
			return true
		}
	}
	return false
}

// Layout draws the section, and its dialog over the window.
func (p *downloadsPage) Layout(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	list, loaded, err := p.shown()
	told := ""
	if err != nil {
		told = mediaErrorText(err)
	}
	if told != "" && told != p.told {
		p.page.toast.Show(told)
	}
	p.told = told
	dims := p.page.layout(gtx, func(gtx layout.Context) layout.Dimensions {
		var children []layout.FlexChild
		if active := p.attachments().active(); len(active) > 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.layoutActive(gtx, active, l, animate)
				})
			}))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.layoutList(gtx, list, loaded, l)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
	p.layoutConfirm(gtx, l)
	return dims
}

// layoutActive draws the files on their way, each with its progress, a
// click on it cancelling.
func (p *downloadsPage) layoutActive(gtx layout.Context, active []activeDownload, l localization.Catalog, animate bool) layout.Dimensions {
	sc := scheme(gtx)
	return card(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 14, Top: 10, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("downloads.active"), token.TypestyleTitleSmall, sc.Primary.Color, 1)
			})
		})}
		for _, d := range active {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				r := p.activeRow(d.message.Key)
				progress := float32(-1)
				details := sendfiles.Size(d.done)
				if d.total > 0 {
					progress = min(1, float32(d.done)/float32(d.total))
					details += " / " + sendfiles.Size(d.total)
				}
				size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(downloadsItemSize))
				style := surfaceStyle{radius: gtx.Dp(12), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: l.T("downloads.cancel")}
				return r.cancel.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
					ring := gtx.Dp(40)
					offset(gtx, image.Pt(gtx.Dp(14), (size.Y-ring)/2), func(gtx layout.Context) layout.Dimensions {
						return r.ring.layout(gtx, ring, true, progress, iconClear, sc.Primary.Color, animate)
					})
					p.layoutText(gtx, size, gtx.Dp(14)+ring+gtx.Dp(14), downloadTitle(d.message, l), details, sc.Surface.OnColor)
					return layout.Dimensions{Size: size}
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}, 6)
}

// downloadTitle is the name of the file of m on its way.
func downloadTitle(m model.Message, l localization.Catalog) string {
	if m.Media != nil && m.Media.FileName != "" {
		return m.Media.FileName
	}
	return l.T("history.file")
}

// layoutList draws the header, with the count and the size of the files
// there and the sorting or the actions on those picked, and the files.
func (p *downloadsPage) layoutList(gtx layout.Context, list []model.DownloadedFile, loaded bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	if !loaded && p.source != nil {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(24).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.loader.sized(gtx, l, 40)
			})
		})
	}
	var count int
	var total int64
	for _, d := range list {
		if d.State != model.DownloadMissing {
			count++
			total += d.Size
		}
	}
	now := time.Now()
	return card(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.layoutHeader(gtx, count, total, len(list) == 0, l)
		})}
		if len(list) == 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 14, Right: 14, Top: 6, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("downloads.empty"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				})
			}))
		}
		for _, d := range list {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.layoutRow(gtx, d, now, l)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}, 6)
}

func (p *downloadsPage) layoutHeader(gtx layout.Context, count int, total int64, empty bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	title := l.T("nav.downloads")
	if !empty {
		title = l.Count("downloads.summary", count, map[string]string{"size": sendfiles.Size(total)})
	}
	return layout.Inset{Left: 14, Right: 8, Top: 8, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleTitleSmall, sc.Primary.Color, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return navigationButtonColor(gtx, &p.folder, iconShowFolder, l.T("downloads.show_in_folder"), sc.SurfaceVariant.OnColor)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if empty {
					return layout.Dimensions{}
				}
				return layout.Spacer{Width: 8}.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if empty {
					return layout.Dimensions{}
				}
				if len(p.selected) > 0 {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &p.clearSelection, l.T("downloads.cancel"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return tonalButton(gtx, &p.deleteSelected, l.T("downloads.delete"), len(p.selected))
						}),
					)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.byDate.Layout(gtx, l.T("downloads.sort_date"), !p.bySize, 0)
					}),
					layout.Rigid(layout.Spacer{Width: 8}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.bySizeChip.Layout(gtx, l.T("downloads.sort_size"), p.bySize, 0)
					}),
				)
			}),
		)
	})
}

// layoutRow draws a download: the mark that picks it, its icon, its name,
// size, when it was downloaded and what became of it, and the buttons to its
// folder and its message.
func (p *downloadsPage) layoutRow(gtx layout.Context, d model.DownloadedFile, now time.Time, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	r := p.row(d.ID)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(downloadsItemSize))
	picked := p.selected[d.ID]
	background := sc.Surface.OnColor.SetOpacity(0)
	if picked {
		background = sc.SecondaryContainer.Color
	}
	style := surfaceStyle{radius: gtx.Dp(12), background: background, content: sc.Surface.OnColor, button: d.Name}
	markSize, pic, buttons := gtx.Dp(40), gtx.Dp(40), gtx.Dp(48)
	r.row.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		x := gtx.Dp(4) + markSize + gtx.Dp(6)
		fg, bg := sc.Primary.OnColor, sc.Primary.Color
		if d.State == model.DownloadMissing {
			fg, bg = sc.SurfaceVariant.OnColor, sc.SurfaceVariant.Color
		}
		offset(gtx, image.Pt(x, (size.Y-pic)/2), func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, bg.AsNRGBA(), clip.Ellipse{Max: image.Pt(pic, pic)}.Op(gtx.Ops))
			icon := gtx.Dp(22)
			return offset(gtx, image.Pt((pic-icon)/2, (pic-icon)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(icon, icon), func(gtx layout.Context) layout.Dimensions {
					return iconFileRow(gtx, fg)
				})
			})
		})
		x += pic + gtx.Dp(14)
		details := sendfiles.Size(d.Size) + " · " + chatTime(d.Added, now, l)
		switch d.State {
		case model.DownloadMissing:
			details += " · " + l.T("downloads.missing")
		case model.DownloadChanged:
			details += " · " + l.T("downloads.changed")
		}
		text := gtx
		text.Constraints.Max.X = max(0, size.X-buttons-gtx.Dp(4))
		p.layoutText(text, size, x, d.Name, details, sc.Surface.OnColor)
		return layout.Dimensions{Size: size}
	})
	// The mark and the buttons over the row, which takes no click of
	// theirs.
	offset(gtx, image.Pt(gtx.Dp(4), (size.Y-markSize)/2), func(gtx layout.Context) layout.Dimensions {
		markStyle := surfaceStyle{radius: markSize / 2, background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: l.T("downloads.select")}
		return r.mark.Layout(gtx, image.Pt(markSize, markSize), markStyle, func(gtx layout.Context) layout.Dimensions {
			mark := gtx.Dp(22)
			offset(gtx, image.Pt((markSize-mark)/2, (markSize-mark)/2), func(gtx layout.Context) layout.Dimensions {
				return selectionMark(gtx, mark, picked)
			})
			return layout.Dimensions{Size: image.Pt(markSize, markSize)}
		})
	})
	offset(gtx, image.Pt(size.X-buttons-gtx.Dp(4), (size.Y-gtx.Dp(48))/2), func(gtx layout.Context) layout.Dimensions {
		return navigationButtonColor(gtx, &r.chat, iconViewInChat, l.T("downloads.view_in_chat"), sc.SurfaceVariant.OnColor)
	})
	return layout.Dimensions{Size: size}
}

// layoutText draws a row's title and the line under it, from x.
func (p *downloadsPage) layoutText(gtx layout.Context, size image.Point, x int, title, details string, fg token.MatColor) {
	sc := scheme(gtx)
	tgtx := gtx
	tgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-x-gtx.Dp(8)), size.Y)}
	macro := op.Record(gtx.Ops)
	dims := layout.Flex{Axis: layout.Vertical}.Layout(tgtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title, token.TypestyleBodyLarge, fg, 1)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, details, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
		}),
	)
	call := macro.Stop()
	offset(gtx, image.Pt(x, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return dims
	})
}

// layoutConfirm asks, over the window, whether to delete the files picked
// from the disk, as Telegram Desktop does.
func (p *downloadsPage) layoutConfirm(gtx layout.Context, l localization.Catalog) {
	if !p.confirm.Shown() {
		return
	}
	sc := scheme(gtx)
	n := len(p.pending)
	title, about := l.T("downloads.delete_sure_one"), l.T("downloads.delete_in_cloud_one")
	if n > 1 {
		title, about = l.Count("downloads.delete_sure", n, nil), l.T("downloads.delete_in_cloud")
	}
	p.confirm.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return p.confirm.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleTitleLarge, sc.Surface.OnColor, 3)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, about, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &p.confirmCancel, l.T("downloads.cancel"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &p.confirmDelete, l.T("downloads.delete"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
}

// downloadMessage is where the message of a downloaded file is: its chat,
// the forum topic it is in, or why it was not found.
type downloadMessage struct {
	chat    model.Chat
	topic   int
	message model.MessageID
	err     error
}

// openDownloaded goes to the message of f. Its chat is found by its ID when
// the chat list has it not, as a channel read without joining it; in a
// forum, the message is in its topic, which a file downloaded before topics
// were kept finds from the message.
func (a *App) openDownloaded(f model.DownloadedFile) {
	results, invalidate, store := a.downloadMessages, a.window.Invalidate, a.store
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r := downloadMessage{topic: f.Topic, message: f.Key.MessageID}
		r.chat, r.err = findChat(ctx, store, f.Key.ChatID)
		if r.err == nil && r.chat.Forum && r.topic == 0 {
			r.topic = lookupTopic(ctx, store, f.Key)
		}
		results <- r
		invalidate()
	}()
}

// findChat is the chat of id: in the chat list, or asked for by its ID.
func findChat(ctx context.Context, store model.Store, id int64) (model.Chat, error) {
	for _, c := range store.Chats() {
		if c.ID == id {
			return c, nil
		}
	}
	if channels, ok := store.(interface {
		ChannelByID(context.Context, int64) (model.Chat, error)
	}); ok && model.IsChannelID(id) {
		return channels.ChannelByID(ctx, -id-1000000000000)
	}
	if users, ok := store.(interface {
		UserByID(context.Context, int64) (model.Chat, error)
	}); ok && id > 0 {
		return users.UserByID(ctx, id)
	}
	return model.Chat{}, model.ErrLinkNotFound
}

// lookupTopic is the forum topic of the message of key, 0 when it is not
// found.
func lookupTopic(ctx context.Context, store model.Store, key model.MessageKey) int {
	lookup, ok := store.(model.MessageLookup)
	if !ok {
		return 0
	}
	for {
		m, state := lookup.LookupMessage(key.ChatID, key.MessageID)
		switch state {
		case model.LookupFound:
			return m.TopicID()
		case model.LookupGone:
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// takeDownloadMessage opens the chat that came at the message, a forum's
// topic at it, or tells the Downloads section why it did not.
func (a *App) takeDownloadMessage(r downloadMessage) {
	l := a.catalog()
	switch {
	case errors.Is(r.err, model.ErrLinkNotFound):
		a.downloads.page.toast.Show(l.T("links.post_invalid"))
		return
	case r.err != nil:
		a.downloads.page.toast.Show(mediaErrorText(r.err))
		return
	}
	a.closeComments()
	a.section = section{kind: sectionAll}
	if !r.chat.Forum || a.forum == nil {
		a.open(chatPick{ID: r.chat.ID, Chat: &r.chat, Message: r.message})
		return
	}
	a.open(chatPick{ID: r.chat.ID, Chat: &r.chat})
	if r.topic == 0 {
		// The forum, where the topic was not found.
		return
	}
	topic := model.Topic{ID: r.topic, General: r.topic == model.GeneralTopic}
	if source, ok := a.store.(model.ForumSource); ok {
		for _, t := range source.Topics(r.chat.ID).Topics {
			if t.ID == r.topic {
				topic = t
			}
		}
	}
	a.openTopicAt(r.chat, topic, r.message)
}
