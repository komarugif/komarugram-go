// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"reflect"
	"strings"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

type messageDraft struct {
	editor   widget.Editor
	entities []model.Entity
	text     string
	pending  *model.OutgoingMessage
	sending  bool
	// err is why the last message was not sent: Send then retries it. It
	// is told in the history's toast once, when it happens.
	err  error
	told error
	// reply is the message what is sent next replies to, nil for none.
	reply *model.Message
	// editing is the message the field edits, of a chat of editKind, nil
	// for none; before, what the field had when the edit began.
	editing  *model.Message
	editKind model.ChatKind
	before   *draftBefore
	// cloud is when the chat's draft the field took was saved.
	cloud time.Time
}
type composerResult struct {
	chat    int64
	request model.OutgoingMessage
	err     error
}
type pickerResult struct {
	generation uint64
	page       model.PickerPage
	err        error
	append     bool
	// refreshed is the page up to date after a Stale one of the request.
	refreshed bool
}
type featuredPackResult struct {
	generation uint64
	id         int64
	tab        model.PickerTab
	pack       model.StickerSet
	err        error
}
type messageComposer struct {
	shortcuts shortcutPreferences
	menus     menuPreferences
	join      surface
	// The actions of a channel's bottom bar: icons whose text shows on
	// hover.
	mute, discussion, directMessages, gift revealButton
	// muted is what Mute set where the store cannot mute a chat.
	muted map[int64]bool
	// mutes tells what came of a mute or an unmute; muting, that one runs.
	mutes  chan error
	muting bool
	// discussions bring the chats the discussion and direct messages
	// buttons open.
	discussions                      chan discussionResult
	discussionLoading, directLoading bool
	nextRights                       time.Time
	// confirmations tells whether a sticker and a GIF are sent only once
	// confirmed; sendConfirm is the dialog that asks.
	confirmations func() (sticker, gif bool)
	// input tells whether the field takes the keyboard only when clicked,
	// and the picker opens only by a click; nil is neither.
	input func() (manualFocus, pickerOnClick bool)
	// pickerHovered tells that the pointer over the emoji button opened the
	// picker, which closes pickerHideDelay after the pointer left both; a
	// click pins it. pickerInside tells whether the pointer is over it,
	// pickerLeft when it left both, and hoverSpent that the pointer has not
	// left the button since the picker closed, so it does not open again;
	// pickerWasOpen is whether the picker was open in the last frame.
	pickerHovered, pickerInside, hoverSpent, pickerWasOpen bool
	pickerLeft                                             time.Time
	pickerPointer                                          struct{}
	sendConfirm                                            sendConfirm
	editorHit                                              struct{}
	// editorChat is the chat whose field was laid out last, editorLaid
	// whether it was in this frame, with room to type, and editorWasLaid
	// whether in the frame before.
	editorChat                int64
	editorLaid, editorWasLaid bool
	// farSend tells whether the capsule's far slot shows Send rather than
	// the microphone in farChat, since farSwitched, zero for no change to
	// animate.
	farSend     bool
	farChat     int64
	farSwitched time.Time
	// top is where the composer began on the last frame, its reply strip
	// included: the history's toast shows above it.
	top                    int
	source                 model.ComposerStore
	invalidate             func()
	ctx                    context.Context
	cancel                 context.CancelFunc
	drafts                 map[int64]*messageDraft
	chat                   int64
	attach, smile          surface
	dismiss                widget.Clickable
	send                   surface
	pickerOpen, attachOpen bool
	tab                    model.PickerTab
	search                 widget.Editor
	query                  string
	due                    time.Time
	generation             uint64
	loading                bool
	loadCancel             context.CancelFunc
	page                   model.PickerPage
	pickerErr              error
	// pickerToast tells, at the picker's bottom, why it could not load;
	// pickerTold is the failure told last.
	pickerToast        toast
	pickerTold         error
	results            chan pickerResult
	featuredResults    chan featuredPackResult
	featuredGeneration uint64
	featuredCancel     context.CancelFunc
	featuredLoading    bool
	sends              chan composerResult
	list               scroll.List
	loader             loadingIndicator
	strip              layout.List
	hover              hoverPlay
	// preview shows an item of the picker held large.
	preview holdPreview
	// itemMenu is the menu of an item of the picker right-clicked;
	// pickerEdits brings what came of its actions.
	itemMenu    pickerItemMenu
	pickerEdits chan pickerEdit
	// pickerDrawn is whether the picker was drawn in the last frame.
	pickerDrawn bool
	stripDrag   stripDrag
	packClicks  map[int64]*surface
	// sectionClicks are the footer's buttons of the static emoji sections.
	sectionClicks [len(emojiSections) + 1]surface
	// pageLoaded is whether the tab has had its page: what follows changes
	// it, not replaces it. local is what the search of emoji found for
	// localQuery, in localLanguage.
	pageLoaded bool
	// sentNotes are the messages sent that the history has not shown yet.
	sentNotes []sentNote
	// sentJump is the chat a message was just sent to, whose history goes
	// to its end in the next frame; 0 for none.
	sentJump                  int64
	localQuery, localLanguage string
	local                     []model.PickerItem
	itemClicks                map[string]*surface
	selectedPack              int64
	recent                    [3][]model.PickerItem
	// drawn tells the emoji the fonts draw, which the picker offers.
	drawn             emojiDrawn
	more, retry       surface
	attachmentActions [3]surface
	// replies is the strip of the message a draft replies to.
	replies replyBar
	// The picker, the attachment menu and the attachment forms open as
	// context menus; formShown is the form shown, kept while it closes.
	pickerMenu, attachMenu, formMenu contextMenu
	formShown                        int
	// tabs are the picker's tabs.
	tabs                        tabRow
	form                        int
	path, title, tasks          widget.Editor
	browse, confirm, cancelForm surface
	fileResults                 chan fileChoice
	choosing                    bool
	// files is the box for sending the files chosen.
	files filesBox
	// chooser asks for files in the system's chooser; tests replace it.
	chooser func(ctx context.Context, filter *fileFilter, several bool) fileChoice
	// voice records voice messages; recording is the one being recorded,
	// voiceResults the recordings encoded for sending, voicePicks the audio
	// files chosen instead for want of an FFmpeg, and recorded the
	// temporary files of recordings, removed once sent.
	voice                 voiceTools
	recording             *voiceRecording
	voiceResults          chan voiceResult
	voicePicks            chan voicePick
	recorded              map[string]bool
	paste                 struct{}
	pasted                map[string]bool
	uploading             map[string]bool
	micClick, voiceCancel surface
	// stopDraft stops the draft a bot streams in the chat, in place of the
	// microphone or Send while it may.
	stopDraft surface
	// ffmpeg is the FFmpeg the user set, "" for the one on PATH.
	ffmpeg func() string
	// edits brings what came of saving edits; askDelete is a message
	// edited to nothing, which the page asks to delete instead.
	edits     chan editResult
	askDelete *model.Message
	// fieldMenu is the field's menu of formatting, and fieldOrigin where
	// the field was in the composer on the last frame; link, the dialog
	// that makes a link of what is selected.
	fieldMenu   fieldMenu
	fieldOrigin image.Point
	link        linkDialog
}

func newMessageComposer(source model.ConversationStore, invalidate func()) *messageComposer {
	ctx, cancel := context.WithCancel(context.Background())
	c := &messageComposer{invalidate: invalidate, ctx: ctx, cancel: cancel, drafts: map[int64]*messageDraft{}, results: make(chan pickerResult, 8), featuredResults: make(chan featuredPackResult, 8), sends: make(chan composerResult, 8), fileResults: make(chan fileChoice, 1), files: newFilesBox(), chooser: chooseFiles, voice: ffmpegVoice, voiceResults: make(chan voiceResult, 2), voicePicks: make(chan voicePick, 1), recorded: map[string]bool{}, pasted: map[string]bool{}, uploading: map[string]bool{}, packClicks: map[int64]*surface{}, itemClicks: map[string]*surface{}}
	c.source, _ = source.(model.ComposerStore)
	c.muted = map[int64]bool{}
	c.discussions = make(chan discussionResult, 2)
	c.edits = make(chan editResult, 2)
	c.mutes = make(chan error, 1)
	c.pickerEdits = make(chan pickerEdit, 4)
	c.search.SingleLine = true
	c.path.SingleLine = true
	c.title.SingleLine = true
	c.list.Axis = layout.Vertical
	c.strip.Axis = layout.Horizontal

	return c
}
func (c *messageComposer) draft(chat int64) *messageDraft {
	d := c.drafts[chat]
	if d == nil {
		d = &messageDraft{editor: widget.Editor{Submit: true}}
		c.drafts[chat] = d
	}
	return d
}
func (c *messageComposer) request(l localization.Catalog, appendPage bool) {
	if !appendPage {
		c.cancelFeatured()
	}
	if c.loadCancel != nil {
		c.loadCancel()
	}
	c.generation++
	generation := c.generation
	c.loading = true
	c.pickerErr = nil
	ctx, cancel := context.WithTimeout(c.ctx, 90*time.Second)
	c.loadCancel = cancel
	req := model.PickerRequest{Tab: c.tab, Query: c.query, Language: string(l.Language()), ChatID: c.chat}
	if appendPage {
		req.Offset = c.page.Next
	}
	go func() {
		defer cancel()
		defer crash.Recover("picker", func(e *crash.Panic) {
			select {
			case c.results <- pickerResult{generation: generation, err: e, append: appendPage}:
				c.invalidate()
			case <-c.ctx.Done():
			}
		})
		page, err := c.source.Picker(ctx, req)
		select {
		case c.results <- pickerResult{generation: generation, page: page, err: err, append: appendPage}:
			c.invalidate()
		case <-c.ctx.Done():
			return
		}
		// The page kept is shown, and the one up to date replaces it.
		refresher, ok := c.source.(model.PickerRefresher)
		if err != nil || !page.Stale || !ok {
			return
		}
		page, err = refresher.RefreshPicker(ctx, req)
		if err != nil {
			// The page shown stays; the next opening asks again.
			return
		}
		select {
		case c.results <- pickerResult{generation: generation, page: page, append: appendPage, refreshed: true}:
			c.invalidate()
		case <-c.ctx.Done():
		}
	}()
}
func (c *messageComposer) cancelFeatured() {
	c.featuredGeneration++
	if c.featuredCancel != nil {
		c.featuredCancel()
		c.featuredCancel = nil
	}
	c.featuredLoading = false
}
func (c *messageComposer) selectedFeatured() *model.PickerPack {
	for i := range c.page.Featured {
		if c.page.Featured[i].ID == c.selectedPack {
			return &c.page.Featured[i]
		}
	}
	return nil
}

// keepFeatured keeps in page the featured sets shown loads in full, which
// page has only the covers of.
func keepFeatured(page *model.PickerPage, shown model.PickerPage) {
	full := map[int64]model.PickerPack{}
	for _, pack := range shown.Featured {
		if pack.Complete {
			full[pack.ID] = pack
		}
	}
	for i, pack := range page.Featured {
		if kept, ok := full[pack.ID]; ok {
			page.Featured[i] = kept
		}
	}
}

func (c *messageComposer) loadFeatured(pack model.PickerPack) {
	c.cancelFeatured()
	source, ok := c.source.(model.StickerSetStore)
	if !ok {
		c.pickerErr = fmt.Errorf("sticker set unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	c.featuredCancel = cancel
	c.featuredLoading = true
	c.pickerErr = nil
	generation, tab := c.featuredGeneration, c.tab
	go func() {
		defer cancel()
		defer crash.Recover("featured sticker set", func(e *crash.Panic) {
			select {
			case c.featuredResults <- featuredPackResult{generation: generation, id: pack.ID, tab: tab, err: e}:
				c.invalidate()
			case <-c.ctx.Done():
			}
		})
		full, err := source.StickerSet(ctx, pack.Ref)
		select {
		case c.featuredResults <- featuredPackResult{generation: generation, id: pack.ID, tab: tab, pack: full, err: err}:
			c.invalidate()
		case <-c.ctx.Done():
		}
	}()
}
func randomMessageID() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UnixNano()
	}
	v := int64(binary.LittleEndian.Uint64(b[:]))
	if v == 0 {
		return 1
	}
	return v
}
func (c *messageComposer) submit(chat int64, msg model.OutgoingMessage) {
	d := c.draft(chat)
	if err := c.checkMessage(chat, msg); err != nil {
		d.err = err
		return
	}
	if d.sending || c.source == nil {
		return
	}
	if d.reply != nil && msg.ReplyTo == 0 {
		msg.ReplyTo = d.reply.Key.MessageID
	}
	if msg.Path != "" || msg.Files != nil {
		msg.FFmpeg = c.ffmpegPath()
	}
	if msg.RandomID == 0 {
		if d.pending != nil {
			prior := *d.pending
			id := prior.RandomID
			prior.RandomID = 0
			if reflect.DeepEqual(prior, msg) {
				msg.RandomID = id
			}
		}
		if msg.RandomID == 0 {
			msg.RandomID = randomMessageID()
		}
	}
	if err := msg.Validate(); err != nil {
		d.err = err
		return
	}
	d.text = d.editor.Text()
	d.pending = &msg
	d.sending = true
	d.err = nil
	c.noteSent(chat)
	go func() {
		defer crash.Recover("send", func(e *crash.Panic) {
			select {
			case c.sends <- composerResult{chat: chat, request: msg, err: e}:
				c.invalidate()
			case <-c.ctx.Done():
			}
		})
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Minute)
		defer cancel()
		err := c.source.Send(ctx, chat, msg)
		select {
		case c.sends <- composerResult{chat: chat, request: msg, err: err}:
			c.invalidate()
		case <-c.ctx.Done():
		}
	}()
}
func (c *messageComposer) submitText() {
	d := c.draft(c.chat)
	if d.editing != nil {
		c.saveEdit(c.chat)
		return
	}
	if d.pending != nil && d.err != nil {
		c.submit(c.chat, *d.pending)
		return
	}
	if strings.TrimSpace(d.editor.Text()) == "" {
		return
	}
	c.submit(c.chat, model.OutgoingMessage{Text: d.editor.Text(), Entities: append([]model.Entity(nil), d.entities...)})
}

func (c *messageComposer) textChanged(d *messageDraft) {
	next := d.editor.Text()
	if next == d.text {
		return
	}
	caret, _ := d.editor.Selection()
	d.entities = shiftEntities(d.text, next, d.entities, caret)
	d.text = next
	d.pending = nil
	d.err = nil
	if g, ok := c.source.(model.GhostStore); ok && strings.TrimSpace(next) != "" {
		g.SendAction(c.chat, model.ActionTyping)
	}
}

// tellAction tells chat what the account does in the composer, as Telegram
// Desktop does: records a voice message, or chooses a sticker in the
// picker; typing is told as the text changes. The store tells it again
// only every few seconds, and only as far as Ghost Mode allows.
func (c *messageComposer) tellAction(chat int64) {
	g, ok := c.source.(model.GhostStore)
	switch {
	case !ok:
	case c.recording != nil:
		g.SendAction(c.recording.chat, model.ActionRecordVoice)
	case c.pickerOpen && c.tab == model.PickerStickers:
		g.SendAction(chat, model.ActionChooseSticker)
	}
}

// takeKeyboard gives the message field the keyboard whenever nobody else
// wants it, as Telegram Desktop's field keeps it: a dialog, the chat's
// search, text selected in the history and a recording take it, and it comes
// back once they are done. While the picker is open, its search takes the
// field's place. free tells whether nobody holds the keyboard now. It runs
// before anything else of the frame, so that whatever asks for the keyboard
// in the frame gets it, and only for a field laid out in the frame before:
// Gio drops the focus of what is not laid out.
func (c *messageComposer) takeKeyboard(gtx layout.Context, chat int64, free bool) {
	c.editorWasLaid = c.editorLaid && c.editorChat == chat
	c.editorLaid = false
	// A picker or a form closing, by Escape or by a click beside it, is
	// still drawn while it fades: its field keeps a keyboard nobody wants.
	if !c.pickerOpen && gtx.Focused(&c.search) || c.form == 0 && (gtx.Focused(&c.title) || gtx.Focused(&c.path)) {
		free = true
	}
	if !free || c.form != 0 || c.recording != nil {
		return
	}
	manualFocus, _ := c.inputPrefs()
	if c.pickerOpen {
		// A picker the pointer opened leaves the keyboard where it is, to
		// go on typing; a click on it gives it to the search.
		if !c.pickerHovered && c.pickerDrawn && !gtx.Focused(&c.search) {
			gtx.Execute(key.FocusCmd{Tag: &c.search})
		}
		return
	}
	if !c.editorWasLaid || manualFocus {
		return
	}
	if d := c.drafts[chat]; d != nil && !gtx.Focused(&d.editor) {
		gtx.Execute(key.FocusCmd{Tag: &d.editor})
	}
}

func (c *messageComposer) update(gtx layout.Context, chat int64, l localization.Catalog, p *chatPage) {
	if c.chat != chat {
		c.keepDraft(c.chat)
		c.takeDraft(chat)
		c.cancelFeatured()
		c.chat = chat
		c.nextRights = time.Time{}
		c.pickerOpen = false
		c.attachOpen = false
		c.form = 0
		c.files.close()
	}
	if c.nextRights.IsZero() || !gtx.Now.Before(c.nextRights) {
		c.nextRights = gtx.Now.Add(time.Minute)
		if source, ok := c.source.(model.SendPermissionsRefresher); ok {
			go func() {
				defer crash.Recover("send permissions", nil)
				_ = source.RefreshSendPermissions(c.ctx, chat)
			}()
		}
	}
	if _, ok := c.source.(model.SendPermissionsRefresher); ok {
		gtx.Execute(op.InvalidateCmd{At: c.nextRights})
	}
	for {
		select {
		case r := <-c.sends:
			d := c.draft(r.chat)
			d.sending = false
			d.err = r.err
			if r.err != nil {
				c.forgetSent(r.chat)
			}
			if r.err == nil {
				if r.request.Item == nil && r.request.Path == "" && len(r.request.Tasks) == 0 && d.editor.Text() == r.request.Text {
					d.editor.SetText("")
					d.text = ""
					d.entities = nil
				}
				d.pending = nil
				if d.reply != nil && r.request.ReplyTo == d.reply.Key.MessageID {
					d.reply = nil
				}
				if r.request.Files != nil {
					c.forgetPasted(r.request.Files.Paths)
				}
				if c.recorded[r.request.Path] {
					delete(c.recorded, r.request.Path)
					os.Remove(r.request.Path)
				}
				if r.chat == c.chat {
					c.form = 0
				}
			}
		case r := <-c.edits:
			c.editDone(r)
		case e := <-c.pickerEdits:
			c.itemEdited(e, l)
		case r := <-c.results:
			if r.generation != c.generation {
				continue
			}
			c.loading = false
			c.pageLoaded = true
			c.pickerErr = r.err
			if r.err == nil {
				if r.append {
					c.page.Items = model.PreferSaved(c.page.Items, r.page.Items)
					c.page.Next = r.page.Next
				} else {
					if r.refreshed {
						// What was shown of a featured set stays shown.
						keepFeatured(&r.page, c.page)
					}
					c.page = r.page
					c.itemClicks = map[string]*surface{}
					if c.query == "" && !c.featuredLoading {
						if pack := c.selectedFeatured(); pack != nil && !pack.Complete {
							c.loadFeatured(*pack)
						}
					}
				}
			}
		case r := <-c.featuredResults:
			if r.generation != c.featuredGeneration || r.tab != c.tab || r.id != c.selectedPack {
				continue
			}
			c.featuredLoading = false
			c.featuredCancel = nil
			c.pickerErr = r.err
			if r.err == nil {
				if pack := c.selectedFeatured(); pack != nil {
					pack.Items = r.pack.Items
					pack.Title = r.pack.Title
					pack.Ref = r.pack.Ref
					pack.Complete = true
					c.list.Position = layout.Position{}
				}
			}
		case r := <-c.voiceResults:
			if r.err != nil {
				c.draft(r.chat).err = r.err
			} else if r.path != "" {
				note := r.note
				c.recorded[r.path] = true
				c.submit(r.chat, model.OutgoingMessage{Path: r.path, Voice: &note})
			}
		case p := <-c.voicePicks:
			c.choosing = false
			if p.err != nil {
				c.draft(p.chat).err = p.err
			} else if p.path != "" {
				note := p.note
				c.submit(p.chat, model.OutgoingMessage{Path: p.path, Voice: &note})
			}
		case f := <-c.fileResults:
			c.choosing = false
			if f.chat != c.chat {
				continue
			}
			switch {
			case f.menu && errors.Is(f.err, errNoChooser) && f.form != 0:
				// Without a chooser, the path is typed.
				c.form = f.form
				c.formShown = f.form
				gtx.Execute(key.FocusCmd{Tag: &c.path})
			case f.err != nil:
				c.draft(c.chat).err = f.err
			case f.menu:
				// Chosen from the attachment menu or the box itself.
				if len(f.paths) > 0 {
					c.files.addPaths(c, f.paths, f.form == 2)
				}
			case f.form == c.form && f.path != "":
				c.path.SetText(f.path)
			}
		default:
			goto drained
		}
	}
drained:
	c.enforcePermissions(gtx)
	permissions := c.permissions(chat)
	d := c.draft(chat)
	c.updateRecording(gtx, l)
	c.updateEdit(chat, p, l)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.editorHit, Kinds: pointer.Press})
		if !ok {
			break
		}
		if !d.sending && c.source != nil && permissions.Allows(model.SendText) {
			gtx.Execute(key.FocusCmd{Tag: &d.editor})
			if e, ok := ev.(pointer.Event); ok && e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary {
				c.openFieldMenu(gtx, d, e.Position.Round())
			}
		}
		c.attachOpen = false
	}
	for _, i := range c.attachmentOrder() {
		if c.attachmentActions[i].Clicked(gtx) && permissions.Any(model.SendAttachments) && (i != 2 || permissions.Allows(model.SendText)) {
			c.attachOpen = false
			c.pickerOpen = false
			if i < 2 {
				// A photo, a video or a file: the system's chooser takes
				// them, and the box for sending files shows what it took.
				c.chooseAttachments(i + 1)
				continue
			}
			c.form = i + 1
			gtx.Execute(key.FocusCmd{Tag: &c.title})
		}
	}

	if !d.sending && permissions.Allows(model.SendText) {
		c.editKeys(gtx, chat, d, p)
		c.updatePaste(gtx, d)
		for {
			ev, ok := d.editor.Update(gtx)
			if !ok {
				break
			}
			switch ev.(type) {
			case widget.SubmitEvent:
				c.submitText()
			case widget.ChangeEvent:
				c.textChanged(d)
			}
		}
	}
	for {
		_, ok := c.search.Update(gtx)
		if !ok {
			break
		}
	}
	q := strings.TrimSpace(c.search.Text())
	if q != c.query {
		c.cancelFeatured()
		c.query = q
		c.due = gtx.Now.Add(300 * time.Millisecond)
		c.generation++
		if c.loadCancel != nil {
			c.loadCancel()
		}
		c.loading = false
		// What the last search found stays until the next one has come,
		// so that the list does not empty and fill as a query is typed.
		c.page.Next = ""
		c.selectedPack = 0
		c.list.Position = layout.Position{}
	}
	pickerAllowed := permissions.Any(model.SendText | model.SendSticker | model.SendGIF)
	if c.smile.Clicked(gtx) && pickerAllowed {
		switch {
		case c.pickerOpen && c.pickerHovered:
			// A click on the button of a picker the pointer opened pins it.
			c.pickerHovered = false
		case c.pickerOpen:
			c.pickerOpen, c.hoverSpent = false, true
		default:
			c.pickerOpen = true
		}
		if c.pickerOpen {
			// The search takes the keyboard once the picker is drawn, which
			// it is not yet: takeKeyboard gives it.
			gtx.Execute(key.FocusCmd{})
		}
		c.attachOpen = false
		c.form = 0
		if c.pickerOpen && c.source != nil {
			c.request(l, false)
		}
	}
	c.hoverPicker(gtx, l, pickerAllowed)
	if c.attach.Clicked(gtx) && permissions.Any(model.SendAttachments) && len(c.attachmentOrder()) > 0 {
		c.attachOpen = !c.attachOpen
		if c.attachOpen {
			gtx.Execute(key.FocusCmd{Tag: &d.editor})
		}
		c.pickerOpen = false
		c.form = 0
	}
	if c.send.Clicked(gtx) {
		c.submitText()
	}
	if c.dismiss.Clicked(gtx) {
		c.pickerOpen = false
		c.attachOpen = false
		c.form = 0
	}
	if i, ok := c.tabs.Clicked(gtx, int(c.tab), 3); ok && c.pickerAllowed(model.PickerTab(i)) {
		c.cancelFeatured()
		c.tab = model.PickerTab(i)
		c.search.SetText("")
		c.query = ""
		c.due = time.Time{}
		c.page, c.pageLoaded = model.PickerPage{}, false
		c.selectedPack = 0
		c.list.Position = layout.Position{}
		c.strip.Position = layout.Position{}
		if c.source != nil {
			c.request(l, false)
		}
	}
	for c.pickerOpen || c.attachOpen || c.form != 0 {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			if c.itemMenu.open {
				// The menu of an item closes, the picker stays.
				c.itemMenu.open = false
				continue
			}
			c.pickerOpen = false
			c.attachOpen = false
			c.form = 0
			gtx.Execute(key.FocusCmd{Tag: &d.editor})
		}
	}
	if !c.pickerOpen {
		// A picker opened again shows no menu of before.
		c.itemMenu.open = false
	}
	if c.pickerOpen && !c.due.IsZero() {
		if !gtx.Now.Before(c.due) {
			c.due = time.Time{}
			if c.source != nil {
				c.request(l, false)
			}
		} else {
			gtx.Execute(op.InvalidateCmd{At: c.due})
		}
	}
	if !c.pickerOpen && c.featuredLoading {
		c.cancelFeatured()
	}
}

func flatEditor(gtx layout.Context, e *widget.Editor, hint string) layout.Dimensions {
	theme := wdk.GetMaterialTheme(gtx)
	sc := scheme(gtx)
	style := theme.Typescale[token.TypestyleBodyLarge]
	text := op.Record(gtx.Ops)
	paint.ColorOp{Color: sc.Surface.OnColor.AsNRGBA()}.Add(gtx.Ops)
	textOp := text.Stop()
	selected := op.Record(gtx.Ops)
	paint.ColorOp{Color: sc.Primary.Color.SetOpacity(token.OpacityLevel3).AsNRGBA()}.Add(gtx.Ops)
	selection := selected.Stop()
	if e.Text() == "" {
		label(gtx, hint, token.TypestyleBodyLarge, sc.SurfaceVariant.OnColor, 1)
	}
	e.LineHeight = style.LineHeight
	return e.Layout(gtx, theme.TextShaper, style.AsRegularFont(), style.Size, textOp, selection)
}

const (
	// composerBlurRadius is how much the content behind an overlay is
	// blurred.
	composerBlurRadius = unit.Dp(16)
	// composerBlurOpacity is the opacity of an overlay over the blur while
	// the preferences do not say.
	composerBlurOpacity = token.OpacityLevel(0.7)
)

// layoutBackdrop fills the current clip, of size, with backdrop blurred:
// the recording of what is behind it, whose origin is at -origin.
func layoutBackdrop(gtx layout.Context, size, origin image.Point, backdrop op.CallOp) {
	radius := gtx.Dp(composerBlurRadius)
	defer paint.PushBlur(gtx.Ops, float32(radius)).Pop()
	// The blur reaches past the clip by about radius: cover that much, the
	// window's edge included, so that no transparency is blurred in.
	margin := image.Pt(2*radius, 2*radius)
	defer clip.Rect{Min: margin.Mul(-1), Max: size.Add(margin)}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: scheme(gtx).SurfaceContainerLow.AsNRGBA()}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	defer op.Offset(origin.Mul(-1)).Push(gtx.Ops).Pop()
	backdrop.Add(gtx.Ops)
}

// composerBarHeight is the height of the composer's bar in a chat of size.
func composerBarHeight(gtx layout.Context, size image.Point) int {
	return min(gtx.Dp(56), size.Y)
}

// floatingComposerCover is how much of the bottom of a chat of size the
// floating composer covers, with the gap below it.
func floatingComposerCover(gtx layout.Context, size image.Point) int {
	height := composerBarHeight(gtx, size)
	return height + min(gtx.Dp(12), max(0, size.Y-height))
}

// pickerHideDelay is how long a picker the pointer opened stays after the
// pointer left it and its button, Telegram Desktop's kHideTimeoutMs: long
// enough to cross the gap between them.
const pickerHideDelay = 300 * time.Millisecond

// inputPrefs tells whether the field takes the keyboard only when clicked,
// and the picker opens only by a click.
func (c *messageComposer) inputPrefs() (manualFocus, pickerOnClick bool) {
	if c.input == nil {
		return false, false
	}
	return c.input()
}

// hoverPicker opens the picker while the pointer is over the emoji button,
// as Telegram Desktop's tabbed panel does, and closes it once the pointer
// has left both for pickerHideDelay, unless a click pinned it. allowed
// tells whether the chat lets anything of the picker be sent.
func (c *messageComposer) hoverPicker(gtx layout.Context, l localization.Catalog, allowed bool) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &c.pickerPointer, Kinds: pointer.Enter | pointer.Leave | pointer.Press | pointer.Cancel})
		if !ok {
			break
		}
		switch e, _ := ev.(pointer.Event); e.Kind {
		case pointer.Enter:
			c.pickerInside = true
		case pointer.Leave, pointer.Cancel:
			c.pickerInside = false
		case pointer.Press:
			// Working in the picker pins it, as a click on its button does.
			c.pickerHovered = false
		}
	}
	if !c.pickerOpen {
		c.pickerHovered, c.pickerInside = false, false
	}
	button := c.smile.click.Hovered()
	// However the picker closed, by Escape, a click beside it or a sticker
	// sent, it does not open again under the pointer still on the button.
	if c.pickerWasOpen && !c.pickerOpen {
		c.hoverSpent = true
	}
	if !button {
		c.hoverSpent = false
	}
	defer func() { c.pickerWasOpen = c.pickerOpen }()
	_, onClick := c.inputPrefs()
	switch {
	case !c.pickerOpen:
		if onClick || !allowed || !button || c.hoverSpent || c.attachOpen || c.form != 0 || c.recording != nil {
			return
		}
		c.pickerOpen, c.pickerHovered, c.pickerLeft = true, true, time.Time{}
		if c.source != nil {
			c.request(l, false)
		}
	case !c.pickerHovered:
	case button || c.pickerInside:
		c.pickerLeft = time.Time{}
	case c.pickerLeft.IsZero():
		c.pickerLeft = gtx.Now
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(pickerHideDelay)})
	case gtx.Now.Sub(c.pickerLeft) >= pickerHideDelay:
		c.pickerOpen, c.pickerHovered = false, false
	default:
		gtx.Execute(op.InvalidateCmd{At: c.pickerLeft.Add(pickerHideDelay)})
	}
}

// farSwitchDuration is how long the microphone and Send take to change
// into each other, Telegram Desktop's universalDuration.
const farSwitchDuration = 120 * time.Millisecond

// farIcon is the icon of the capsule's far slot, Send or the microphone. As
// in Telegram Desktop, the one shown shrinks and fades while the other grows
// in its place; not in a chat just opened, nor with animations off.
func (c *messageComposer) farIcon(gtx layout.Context, chat int64, send bool) wdk.IconWidget {
	if c.farChat != chat || !wdk.AnimationsEnabled(gtx) {
		c.farChat, c.farSend, c.farSwitched = chat, send, time.Time{}
	} else if send != c.farSend {
		c.farSend, c.farSwitched = send, gtx.Now
	}
	sc := scheme(gtx)
	to, from, fromColor := iconMic, iconSend, sc.Primary.Color
	if send {
		to, from, fromColor = iconSend, iconMic, sc.SurfaceVariant.OnColor
	}
	if c.farSwitched.IsZero() {
		return to
	}
	progress := float32(gtx.Now.Sub(c.farSwitched)) / float32(farSwitchDuration)
	if progress >= 1 {
		c.farSwitched = time.Time{}
		return to
	}
	gtx.Execute(op.InvalidateCmd{})
	return morphIcon(from, fromColor, to, max(0, progress))
}

// forgetFar makes the far slot's next icon appear without animation: the
// slot showed something else, Stop or nothing.
func (c *messageComposer) forgetFar() { c.farChat = 0 }

// morphIcon draws from, in fromColor, changing into to, in the color it is
// given, progress of the way: from shrinks to a fifth and fades out while to
// grows from a fifth and fades in, both about their centers.
func morphIcon(from wdk.IconWidget, fromColor token.MatColor, to wdk.IconWidget, progress float32) wdk.IconWidget {
	return func(gtx layout.Context, color token.MatColor) layout.Dimensions {
		var size image.Point
		draw := func(icon wdk.IconWidget, color token.MatColor, scale, opacity float32) {
			m := op.Record(gtx.Ops)
			dims := icon(gtx, color)
			call := m.Stop()
			size = image.Pt(max(size.X, dims.Size.X), max(size.Y, dims.Size.Y))
			center := layout.FPt(dims.Size).Mul(.5)
			defer op.Affine(f32.AffineId().Scale(center, f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
			defer paint.PushOpacity(gtx.Ops, opacity).Pop()
			call.Add(gtx.Ops)
		}
		draw(from, fromColor, 1-.8*progress, 1-progress)
		draw(to, color, .2+.8*progress, progress)
		return layout.Dimensions{Size: size}
	}
}

// composerShadow draws the shadow of a floating composer's plate, under the
// plate; the classic bar below the history casts none.
func composerShadow(gtx layout.Context, classic bool, plate image.Rectangle, radius int) {
	if !classic {
		drawShadow(gtx, plate, radius, composerShadowStyle)
	}
}

// composerFill fills a plate of the composer: a floating one over the
// blurred history, as an overlay; the classic bar as the window's other
// surfaces, as translucent as the window.
func composerFill(gtx layout.Context, classic bool, backdrop *blurBackdrop, size, origin image.Point, fill token.MatColor, radius int) {
	if classic {
		fillWindowSurface(gtx, fill, size)
		return
	}
	overlayFill(gtx, backdrop, size, origin, fill, radius)
}

// Layout draws the composer over the chat page p. A floating composer shows
// backdrop, the history behind it, blurred; nil draws it opaque.
func (c *messageComposer) Layout(gtx layout.Context, chat int64, l localization.Catalog, p *chatPage, animate bool, backdrop *blurBackdrop) layout.Dimensions {
	c.update(gtx, chat, l, p)
	c.tellAction(chat)
	size := gtx.Constraints.Max
	pad := min(gtx.Dp(16), size.X/8)
	classic := p.classicComposer()
	height := composerBarHeight(gtx, size)
	bottom := floatingComposerCover(gtx, size) - height
	rect := image.Rect(pad, max(0, size.Y-bottom-height), max(pad, size.X-pad), size.Y-bottom)
	if classic {
		rect = image.Rect(0, max(0, size.Y-height), size.X, size.Y)
	}
	c.top = rect.Min.Y
	if c.stopDraft.Clicked(gtx) {
		p.stopStreamedDraft()
	}
	d := c.draft(chat)
	if d.err != nil && d.err != d.told {
		p.toast.Show(composerErrorText(d.err, l))
	}
	d.told = d.err
	if m := c.askDelete; m != nil {
		// An edit to nothing deletes the message, once asked, as in
		// Telegram Desktop.
		c.askDelete = nil
		parts := []model.Message{*m}
		p.openDeleteParts(gtx, parts, p.rightsFor(parts))
	}
	if p.frozen.Frozen() {
		// A frozen account cannot send: the bar tells why, as Telegram
		// Desktop's does.
		inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			s := gtx.Constraints.Max
			radius := s.Y / 2
			if classic {
				radius = 0
			}
			composerShadow(gtx, classic, image.Rectangle{Max: s}, radius)
			defer clip.UniformRRect(image.Rectangle{Max: s}, radius).Push(gtx.Ops).Pop()
			composerFill(gtx, classic, backdrop, s, rect.Min, sc.SurfaceContainerHigh, radius)
			if classic {
				fillRect(gtx, sc.OutlineVariant, image.Pt(s.X, gtx.Dp(1)))
			}
			return p.frozen.layoutComposer(gtx, l, s, radius)
		})
		return layout.Dimensions{Size: size}
	}
	if !c.permissions(chat).Any(model.SendText | model.SendAttachments | model.SendVoice | model.SendSticker) {
		inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
			s := gtx.Constraints.Max
			radius := s.Y / 2
			if classic {
				radius = 0
			}
			composerShadow(gtx, classic, image.Rectangle{Max: s}, radius)
			defer clip.UniformRRect(image.Rectangle{Max: s}, radius).Push(gtx.Ops).Pop()
			composerFill(gtx, classic, backdrop, s, rect.Min, scheme(gtx).SurfaceContainerHigh, radius)
			return c.layoutReadOnly(gtx, p, l)
		})
		return layout.Dimensions{Size: size}
	}
	if p.bot.empty {
		// An empty chat with a bot starts it, as Telegram Desktop's does.
		p.layoutStart(gtx, chat, rect, classic, backdrop, l)
		return layout.Dimensions{Size: size}
	}
	if c.pickerOpen || c.attachOpen || c.form != 0 {
		c.dismiss.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: size} })
	}
	// above is the top of the composer, the reply strip included: what
	// opens from the composer opens over it.
	kb := p.keyboardHeight(gtx, classic, size)
	if !classic {
		p.layoutKeyboard(gtx, chat, image.Rect(rect.Min.X, rect.Min.Y-kb, rect.Max.X, rect.Min.Y-gtx.Dp(replyGap)), classic, backdrop, l)
	} else {
		p.layoutKeyboard(gtx, chat, image.Rect(rect.Min.X, rect.Min.Y-kb, rect.Max.X, rect.Min.Y), classic, backdrop, l)
	}
	// The reply strip is over the keyboard, which is over the bar.
	c.replyLayout(gtx, chat, rect.Sub(image.Pt(0, kb)), classic, backdrop, l, p)
	above := rect.Min.Y - kb - c.replyHeight(gtx, chat, classic)
	c.top = above

	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		s := gtx.Constraints.Max
		radius := s.Y / 2
		if classic {
			radius = 0
		}
		// Keep pointer input inside the composer from reaching the history.
		composerShadow(gtx, classic, image.Rectangle{Max: s}, radius)
		defer clip.UniformRRect(image.Rectangle{Max: s}, radius).Push(gtx.Ops).Pop()
		composerFill(gtx, classic, backdrop, s, rect.Min, sc.SurfaceContainerHigh, radius)
		if classic {
			fillRect(gtx, sc.OutlineVariant, image.Pt(s.X, gtx.Dp(1)))
		}
		event.Op(gtx.Ops, c)
		// Square slots put the icons' circles at the centers of the
		// capsule's rounded ends, as far from its edges across as along.
		iconWidth := min(s.Y, s.X/3)
		button := func(x int, click *surface, icon wdk.IconWidget, label string) {
			inRect(gtx, image.Rect(x, 0, x+iconWidth, s.Y), func(gtx layout.Context) layout.Dimensions {
				size := gtx.Constraints.Max
				rights := c.permissions(chat)
				disabled := click == &c.attach && (!rights.Any(model.SendAttachments) || d.editing != nil) || click == &c.smile && !rights.Any(model.SendText|model.SendSticker|model.SendGIF) || click == &c.micClick && !rights.Allows(model.SendVoice) ||
					click == &c.send && c.recording == nil && (d.sending || !rights.Allows(model.SendText))
				if disabled {
					gtx = gtx.Disabled()
				}
				// The whole slot takes clicks; the state layer and the ripple
				// are a circle around the icon, as on Material icon buttons.
				d := min(gtx.Dp(40), size.X, size.Y)
				circle := image.Rectangle{Max: image.Pt(d, d)}.Add(size.Sub(image.Pt(d, d)).Div(2))
				content := sc.SurfaceVariant.OnColor
				if click == &c.send {
					// Send stands out, as in Telegram Desktop.
					content = sc.Primary.Color
				}
				if disabled {
					content = content.SetOpacity(0.38)
				}
				style := surfaceStyle{area: circle, radius: d / 2, background: content.SetOpacity(0), content: content, button: label}
				return click.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icon(gtx, content) })
				})
			})
		}
		if c.recording != nil {
			// While it records, the cross drops the recording and Send
			// sends it.
			c.forgetFar()
			button(0, &c.voiceCancel, iconClear, l.T("record.cancel"))
			button(s.X-iconWidth, &c.send, iconSend, l.T("composer.send"))
			inRect(gtx, image.Rect(iconWidth, 0, max(iconWidth, s.X-iconWidth), s.Y), c.layoutRecording)
			return layout.Dimensions{Size: s}
		}
		button(0, &c.attach, iconAttach, l.T("composer.attach"))
		// A chat with a bot's keyboard has the button that hides it and
		// shows it again left of the emoji button, as in Telegram Desktop.
		keyboardWidth := 0
		if p.bot.offered {
			keyboardWidth = iconWidth
		}
		// A bot's menu button, which opens its Mini App, is beside the
		// clip.
		menuWidth := p.layoutBotMenu(gtx, chat, image.Rect(iconWidth, 0, s.X-2*iconWidth-keyboardWidth, s.Y), l)
		sendWidth := 0
		if d.editing != nil {
			// An edit is saved by the check in Send's place.
			sendWidth = iconWidth
			c.forgetFar()
			label := l.T("composer.save")
			if d.sending {
				label = l.T("composer.saving")
			}
			button(s.X-iconWidth, &c.send, iconDone, label)
			button(s.X-2*iconWidth, &c.smile, iconEmoji, l.T("composer.emoji"))
		} else if p.stoppableDraft() && !d.sending {
			// While a bot streams a draft the account may stop, Stop takes
			// the place of Send, as in Telegram Desktop.
			sendWidth = iconWidth
			c.forgetFar()
			button(s.X-iconWidth, &c.stopDraft, iconStop, l.T("rich.stop_draft"))
			button(s.X-2*iconWidth, &c.smile, iconEmoji, l.T("composer.emoji"))
		} else if c.canRecord(d) || !c.permissions(chat).Allows(model.SendVoice) && !d.sending && d.pending == nil && d.editor.Text() == "" {
			// With nothing written, the microphone takes the far right, as
			// in Telegram Desktop, and the emoji button moves left of it.
			sendWidth = iconWidth
			button(s.X-iconWidth, &c.micClick, c.farIcon(gtx, chat, false), l.T("record.voice"))
			button(s.X-2*iconWidth, &c.smile, iconEmoji, l.T("composer.emoji"))
		} else if strings.TrimSpace(d.editor.Text()) != "" || d.sending || d.pending != nil {
			// Send takes the microphone's place, the emoji button stays
			// left of it.
			sendWidth = iconWidth
			label := l.T("composer.send")
			if d.sending {
				label = l.T("composer.sending")
			}
			if d.err != nil && d.pending != nil {
				label = l.T("composer.retry")
			}
			button(s.X-iconWidth, &c.send, c.farIcon(gtx, chat, true), label)
			button(s.X-2*iconWidth, &c.smile, iconEmoji, l.T("composer.emoji"))
		} else {
			c.forgetFar()
			button(s.X-iconWidth, &c.smile, iconEmoji, l.T("composer.emoji"))
		}
		if keyboardWidth > 0 {
			icon, label := p.toggleKeyboard(gtx, chat, l)
			button(s.X-2*iconWidth-sendWidth, &p.bot.toggle, icon, label)
		}
		inRect(gtx, image.Rect(iconWidth+menuWidth, 0, max(iconWidth+menuWidth, s.X-iconWidth-sendWidth-keyboardWidth), s.Y), func(gtx layout.Context) layout.Dimensions {
			if !c.permissions(chat).Allows(model.SendText) {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, restrictionText(c.permissions(chat), model.SendText, l), token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 2)
				})
			}
			if d.sending || c.source == nil {
				gtx = gtx.Disabled()
			} else {
				if !c.editorWasLaid {
					// A field just shown takes the keyboard in the next
					// frame, which nothing else may ask for.
					gtx.Execute(op.InvalidateCmd{})
				}
				c.editorChat, c.editorLaid = chat, true
			}
			c.fieldOrigin = rect.Min.Add(image.Pt(iconWidth+menuWidth, 0))
			size := gtx.Constraints.Max
			inner := gtx
			inner.Constraints.Min.Y = 0
			inner.Constraints.Max.Y = max(0, size.Y-gtx.Dp(12))
			record := op.Record(gtx.Ops)
			dims := formattedEditor(inner, &d.editor, l.T("composer.message"), d.entities)
			call := record.Stop()
			transform := op.Offset(image.Pt(0, max(0, (size.Y-dims.Size.Y)/2))).Push(gtx.Ops)
			call.Add(gtx.Ops)
			transform.Pop()
			// Pass presses to the editor for caret placement, but focus it from the
			// capsule's full height, including the padding above and below the text.
			area := clip.Rect{Max: size}.Push(gtx.Ops)
			pass := pointer.PassOp{}.Push(gtx.Ops)
			event.Op(gtx.Ops, &c.editorHit)
			pass.Pop()
			area.Pop()
			return layout.Dimensions{Size: size}
		})
		return layout.Dimensions{Size: s}
	})
	{
		// The picker opens from the emoji button, at the composer's end.
		w := min(gtx.Dp(372), max(0, size.X-2*pad))
		h := min(gtx.Dp(488), max(0, above-gtx.Dp(12)))
		area := image.Rect(size.X-pad-w, above-gtx.Dp(8)-h, size.X-pad, above-gtx.Dp(8))
		drawn := false
		c.pickerMenu.Layout(gtx, c.pickerOpen, area, menuFromBottomRight, gtx.Dp(16), func(gtx layout.Context) layout.Dimensions {
			drawn = true
			return c.pickerLayout(gtx, l, p, animate, c.pickerMenu.bounds.Min)
		})
		// The picker closed: its stickers keep their first frames only.
		if c.pickerDrawn && !drawn {
			p.dropStickerLoops()
		}
		if drawn && !c.pickerDrawn {
			p.stickerViewShown()
			// Its search takes the keyboard in the next frame.
			gtx.Execute(op.InvalidateCmd{})
		}
		c.pickerDrawn = drawn
	}
	{
		// The attachment menu opens from the paperclip, at the start.
		w := min(gtx.Dp(264), size.X-2*pad)
		order := c.attachmentOrder()
		h := min(gtx.Dp(48)*len(order), max(0, above-gtx.Dp(8)))
		area := image.Rect(pad, above-h-gtx.Dp(8), pad+w, above-gtx.Dp(8))
		c.attachMenu.Layout(gtx, c.attachOpen, area, menuFromBottomLeft, gtx.Dp(12), func(gtx layout.Context) layout.Dimensions {
			menuSize := gtx.Constraints.Max
			sc := scheme(gtx)
			defer clip.UniformRRect(image.Rectangle{Max: menuSize}, gtx.Dp(12)).Push(gtx.Ops).Pop()
			overlayFill(gtx, p.menuBackdrop(), menuSize, c.attachMenu.bounds.Min, sc.SurfaceContainerHigh, gtx.Dp(12))
			// Without this clip the menu's input region covers the surrounding chat
			// and prevents the outside-click handler from closing the menu.
			event.Op(gtx.Ops, &c.attachmentActions)
			icons := []wdk.IconWidget{iconAttachPhoto, iconAttachFile, iconAttachTasks}
			for position, i := range order {
				k := []string{"photo", "file", "tasks"}[i]
				inRect(gtx, image.Rect(0, position*h/len(order), w, (position+1)*h/len(order)), func(gtx layout.Context) layout.Dimensions {
					row := gtx.Constraints.Max
					if i == 2 && !c.permissions(chat).Allows(model.SendText) {
						gtx = gtx.Disabled()
					}
					style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
					return c.attachmentActions[i].Layout(gtx, row, style, func(gtx layout.Context) layout.Dimensions {
						if position > 0 {
							fillRect(gtx, sc.OutlineVariant, image.Pt(row.X, gtx.Dp(1)))
						}
						return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icons[i](gtx, sc.SurfaceVariant.OnColor) }),
								layout.Rigid(layout.Spacer{Width: 12}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
										gtx.Constraints.Min = image.Point{}
										return label(gtx, l.T("composer."+k), token.TypestyleBodyMedium, sc.Surface.OnColor, 1)
									})
								}),
							)
						})
					})
				})
			}
			return layout.Dimensions{Size: menuSize}
		})
	}
	p.layoutCommands(gtx, chat, size, pad, above, l)
	bar := rect
	bar.Min.Y = above
	if classic {
		// Keep the form off the window's edge, where the capsule would start.
		bar.Min.X, bar.Max.X = pad, max(pad, size.X-pad)
	}
	c.formLayout(gtx, bar, l)
	c.layoutFieldMenu(gtx, d, p, l)
	return layout.Dimensions{Size: size}
}
func (c *messageComposer) formLayout(gtx layout.Context, bar image.Rectangle, l localization.Catalog) {
	if c.form != 0 {
		c.formShown = c.form
	}
	form := c.formShown
	w := min(gtx.Dp(420), bar.Dx())
	h := min(gtx.Dp(330), max(0, bar.Min.Y-gtx.Dp(8)))
	area := image.Rect(bar.Min.X, bar.Min.Y-gtx.Dp(8)-h, bar.Min.X+w, bar.Min.Y-gtx.Dp(8))
	// A form opens from the attachment menu, at the composer's start.
	c.formMenu.Layout(gtx, c.form != 0, area, menuFromBottomLeft, gtx.Dp(16), func(gtx layout.Context) layout.Dimensions {
		s := gtx.Constraints.Max
		fillRounded(gtx, scheme(gtx).SurfaceContainerHigh, s, gtx.Dp(16))
		defer clip.Rect{Max: s}.Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, c)
		if c.cancelForm.Clicked(gtx) {
			c.form = 0
		}
		if c.browse.Clicked(gtx) && !c.choosing {
			c.choosing = true
			ctx := c.ctx
			media := form == 1
			chat := c.chat
			go func() {
				defer crash.Recover("choose attachment", func(e *crash.Panic) {
					select {
					case c.fileResults <- fileChoice{chat: chat, form: form, err: e}:
						c.invalidate()
					case <-ctx.Done():
					}
				})
				f := chooseAttachment(ctx, media)
				f.chat, f.form = chat, form
				select {
				case c.fileResults <- f:
					c.invalidate()
				case <-ctx.Done():
				}
			}()
		}
		if c.confirm.Clicked(gtx) {
			msg := model.OutgoingMessage{}
			if form != 3 {
				// The path typed opens the box, as a file chosen does.
				if path := strings.TrimSpace(c.path.Text()); path != "" {
					c.form = 0
					c.files.addPaths(c, []string{path}, form == 2)
				}
			}
			if form == 3 {
				msg.Path = ""
				msg.Text = strings.TrimSpace(c.title.Text())
				for _, task := range strings.Split(c.tasks.Text(), "\n") {
					if task = strings.TrimSpace(task); task != "" {
						msg.Tasks = append(msg.Tasks, task)
					}
				}
				if len(msg.Tasks) == 0 {
					c.draft(c.chat).err = fmt.Errorf("%s", l.T("composer.items"))
				}
			}
			if form == 3 && len(msg.Tasks) > 0 {
				c.submit(c.chat, msg)
			}
		}
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if c.draft(c.chat).sending {
				gtx = gtx.Disabled()
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					k := "file"
					if form == 1 {
						k = "photo"
					}
					if form == 3 {
						k = "tasks"
					}
					return label(gtx, l.T("composer."+k), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 1)
				}), vspace(16),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if form != 3 {
						return flatEditor(gtx, &c.path, l.T("composer.path"))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return flatEditor(gtx, &c.title, l.T("composer.title")) }), vspace(16), layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return flatEditor(gtx, &c.tasks, l.T("composer.items")) }), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("composer.task_hint"), token.TypestyleBodySmall, scheme(gtx).SurfaceVariant.OnColor, 2)
					}))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if form == 3 {
							return layout.Dimensions{}
						}
						return textButton(gtx, &c.browse, l.T("composer.browse"))
					}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return textButton(gtx, &c.cancelForm, l.T("composer.cancel"))
					}), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &c.confirm, l.T("composer.send")) }))
				}),
			)
		})
	})
}

func (c *messageComposer) attachmentOrder() []int {
	return arrangeMenu(c.menus, menuAttachment, []int{0, 1, 2}, func(i int) string { return []string{"photo", "file", "tasks"}[i] })
}
