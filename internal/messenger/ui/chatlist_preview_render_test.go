// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	xdraw "golang.org/x/image/draw"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// previewSource is the demo's media, with custom emoji of its own: faces,
// the second of the text's color, as Telegram marks some.
type previewSource struct{ demo *mockstore.Store }

func (s previewSource) Media(ctx context.Context, m model.Message) ([]byte, error) {
	var n int
	if _, err := fmt.Sscanf(m.Media.ID, "test/emoji/%d", &n); err == nil {
		return previewEmojiPNG(n)
	}
	return s.demo.Media(ctx, m)
}

func (s previewSource) CustomEmoji(_ context.Context, id int64) (model.Message, error) {
	return model.Message{Kind: model.MessageSticker, Media: &model.MessageMedia{ID: fmt.Sprintf("test/emoji/%d", id), MIMEType: "image/png", TextColor: id == 2}}, nil
}

// previewEmojiPNG is a face: a disc with eyes, in a color of its own.
func previewEmojiPNG(n int) ([]byte, error) {
	const side = 64
	fills := []color.NRGBA{{0xff, 0xc8, 0x3d, 0xff}, {0xff, 0xc8, 0x3d, 0xff}, {0, 0, 0, 0xff}}
	fill := fills[n%len(fills)]
	im := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			dx, dy := x-side/2, y-side/2
			if dx*dx+dy*dy > (side/2-2)*(side/2-2) {
				continue
			}
			c := fill
			eye := func(ex int) bool { return (x-ex)*(x-ex)+(y-24)*(y-24) < 30 }
			mouth := dy > 6 && dy < 14 && dx*dx < 150 && dx*dx+(dy-2)*(dy-2) > 90
			if (eye(22) || eye(42) || mouth) && n != 2 {
				c = color.NRGBA{0x40, 0x30, 0x20, 0xff}
			}
			if (eye(22) || eye(42) || mouth) && n == 2 {
				continue
			}
			im.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, im)
	return b.Bytes(), err
}

// previewChats are a chat of each kind of last message the list draws, the
// photos with the inline pictures of inline, when not nil, as Telegram
// sends them; without, they are downloaded.
func previewChats(now time.Time, inline func(n int) []byte) []model.Chat {
	photo := func(n int) model.PreviewThumb {
		t := model.PreviewThumb{ID: mockstoreThumb(n), Message: model.MessageID(n)}
		if inline != nil {
			t.Blurred = inline(n)
		}
		return t
	}
	video := model.PreviewThumb{ID: "demo/portrait", Message: 50, Video: true}
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	text := func(s string) model.Preview { return model.Preview{Text: s} }
	formatted := text("курсив, подчёркнутый, зачёркнутый и код: go test ./...")
	formatted.Sender, formatted.Entities = "Анна", []model.Entity{{Kind: "italic", Offset: 0, Length: 6}, {Kind: "underline", Offset: 8, Length: 13}, {Kind: "strike", Offset: 23, Length: 11}, {Kind: "code", Offset: 42, Length: 13}}
	spoiler := text("Ответ на загадку: это был дворецкий")
	spoiler.Outgoing, spoiler.Entities = true, []model.Entity{{Kind: "spoiler", Offset: 18, Length: 17}}
	emoji := text("Идея 🔥 и цвет текста 💙")
	emoji.Entities = []model.Entity{{Kind: "emoji", Offset: 5, Length: 2, DocumentID: 1}, {Kind: "emoji", Offset: 22, Length: 2, DocumentID: 2}}
	login := text("Код для входа в Telegram: 52107. Никому не давайте код")
	login.Entities = []model.Entity{{Kind: "spoiler", Offset: 26, Length: 5}}
	caption := model.Preview{Text: "Закат над заливом", Media: "preview.photo", Thumbs: []model.PreviewThumb{photo(1)}}
	album := model.Preview{Media: "preview.photo", Thumbs: []model.PreviewThumb{photo(2), photo(3), photo(4)}, Album: &model.PreviewAlbum{ID: 1, Photos: 5, Last: 5}}
	mixed := model.Preview{Text: "Поездка в горы, лучшие кадры", Media: "preview.photo", Thumbs: []model.PreviewThumb{photo(5), video, photo(6)},
		Album: &model.PreviewAlbum{ID: 2, Photos: 2, Videos: 1, Captions: 1, Caption: 1, Last: 3}, Sender: "Игорь"}
	files := model.Preview{Media: "preview.file", Album: &model.PreviewAlbum{ID: 3, Files: 4, Last: 4}}
	hidden := model.Preview{Text: "Не открывай до праздника", Media: "preview.photo", Thumbs: []model.PreviewThumb{{ID: mockstoreThumb(7), Spoiler: true, Blurred: mockstoreBlurred()}}}
	forward := text("Пересланное сообщение со стрелкой перед текстом")
	forward.Forwarded = true
	forwardPhoto := model.Preview{Media: "preview.photo", Forwarded: true, Thumbs: []model.PreviewThumb{photo(8)}}
	link := text("https://telegram.org — страница со своей картинкой")
	link.Thumbs = []model.PreviewThumb{photo(9)}
	long := text("Очень длинное сообщение, которое не помещается в строку списка и обрезается многоточием в самом конце, где бы ни кончилось")
	long.Entities = []model.Entity{{Kind: "italic", Offset: 6, Length: 7}}
	service := model.Message{Kind: model.MessageService, SenderName: "Игорь", Service: &model.ServiceAction{Kind: model.ServiceAddUser, Peers: []model.ServicePeer{{ID: 5, Name: "Анна"}}}, Key: model.MessageKey{ChatID: 18}}
	return []model.Chat{
		{ID: 1, Kind: model.KindUser, Title: "Обычный текст", Last: text("До встречи в субботу!"), LastTime: ago(2 * time.Minute), Unread: 2},
		{ID: 2, Kind: model.KindGroup, Title: "Форматирование", Last: formatted, LastTime: ago(5 * time.Minute), Unread: 14},
		{ID: 3, Kind: model.KindGroup, Title: "Спойлер, свой", Last: spoiler, LastTime: ago(9 * time.Minute)},
		{ID: 4, Kind: model.KindUser, Title: "Custom emoji", Last: emoji, LastTime: ago(12 * time.Minute)},
		{ID: 777000, Kind: model.KindUser, Title: "Telegram", Last: login, LastTime: ago(15 * time.Minute), Unread: 1, Badges: model.Badges{Verified: true}},
		{ID: 30, Kind: model.KindUser, Title: "Черновик", Last: text("Последнее сообщение"), LastTime: ago(16 * time.Minute),
			Draft: &model.Draft{Text: "Не забыть купить\nмолоко и хлеб", Entities: []model.Entity{{Kind: "italic", Offset: 17, Length: 6}}}},
		{ID: 31, Kind: model.KindUser, Title: "Печатает", Last: text("Не видно, пока печатает"), LastTime: ago(17 * time.Minute)},
		{ID: 32, Kind: model.KindGroup, Title: "Двое печатают", Last: text("Не видно"), LastTime: ago(17 * time.Minute)},
		{ID: 33, Kind: model.KindGroup, Title: "Запись голосового", Last: text("Не видно"), LastTime: ago(17 * time.Minute)},
		{ID: 34, Kind: model.KindUser, Title: "Отправка фото", Last: text("Не видно"), LastTime: ago(17 * time.Minute)},
		{ID: 35, Kind: model.KindGroup, Title: "Выбор стикера", Last: text("Не видно"), LastTime: ago(17 * time.Minute), Unread: 2},
		{ID: 5, Kind: model.KindUser, Title: "Фото с подписью", Last: caption, LastTime: ago(20 * time.Minute)},
		{ID: 6, Kind: model.KindUser, Title: "Фото без подписи", Last: model.Preview{Media: "preview.photo", Thumbs: []model.PreviewThumb{photo(10)}}, LastTime: ago(25 * time.Minute)},
		{ID: 7, Kind: model.KindUser, Title: "Видео", Last: model.Preview{Text: "Смотри, что снял", Media: "preview.video", Thumbs: []model.PreviewThumb{video}}, LastTime: ago(30 * time.Minute)},
		{ID: 8, Kind: model.KindUser, Title: "Видеосообщение", Last: model.Preview{Media: "preview.round", Thumbs: []model.PreviewThumb{{ID: "demo/portrait", Message: 51, Video: true, Round: true}}}, LastTime: ago(35 * time.Minute)},
		{ID: 9, Kind: model.KindUser, Title: "GIF", Last: model.Preview{Media: "preview.gif", Thumbs: []model.PreviewThumb{{ID: "demo/portrait", Message: 52}}}, LastTime: ago(40 * time.Minute)},
		{ID: 10, Kind: model.KindGroup, Title: "Альбом без подписи", Last: album, LastTime: ago(45 * time.Minute), Unread: 5, Muted: true},
		{ID: 11, Kind: model.KindGroup, Title: "Альбом с подписью", Last: mixed, LastTime: ago(50 * time.Minute)},
		{ID: 12, Kind: model.KindUser, Title: "Альбом файлов", Last: files, LastTime: ago(55 * time.Minute)},
		{ID: 13, Kind: model.KindUser, Title: "Фото под спойлером", Last: hidden, LastTime: ago(time.Hour)},
		{ID: 14, Kind: model.KindUser, Title: "Пересланное", Last: forward, LastTime: ago(2 * time.Hour)},
		{ID: 15, Kind: model.KindChannel, Title: "Пересланное фото", Last: forwardPhoto, LastTime: ago(3 * time.Hour), Unread: 3, Muted: true},
		{ID: 16, Kind: model.KindUser, Title: "Ссылка с картинкой", Last: link, LastTime: ago(4 * time.Hour)},
		{ID: 17, Kind: model.KindUser, Title: "Длинный текст", Last: long, LastTime: ago(5 * time.Hour), Pinned: true, PinRank: 1},
		{ID: 18, Kind: model.KindGroup, Title: "Служебное", Last: model.Preview{Service: &service}, LastTime: ago(6 * time.Hour)},
		{ID: 19, Kind: model.KindUser, Title: "Стикер", Last: model.Preview{Media: "preview.sticker", Emoji: "😀"}, LastTime: ago(7 * time.Hour)},
		{ID: 20, Kind: model.KindUser, Title: "Голосовое", Last: model.Preview{Media: "preview.voice", Sent: true, ID: 3}, ReadOutbox: 3, LastTime: ago(26 * time.Hour)},
		{ID: 21, Kind: model.KindUser, Title: "Выбранный чат", Last: model.Preview{Text: "Выделен: курсив и фото", Entities: []model.Entity{{Kind: "italic", Offset: 9, Length: 6}}, Thumbs: []model.PreviewThumb{photo(11)}, Media: "preview.photo"}, LastTime: ago(30 * time.Hour)},
	}
}

// previewActivities are what others do in the chats of previewChats.
type previewActivities map[int64][]model.Activity

func (a previewActivities) Activities(chat int64) []model.Activity { return a[chat] }

func newPreviewActivities(now time.Time) previewActivities {
	until := now.Add(time.Hour)
	act := func(name string, action model.ChatAction) model.Activity {
		return model.Activity{Name: name, Action: action, Until: until}
	}
	return previewActivities{
		31: {act("Анна", model.ActionTyping)},
		32: {act("Игорь", model.ActionTyping), act("Ольга", model.ActionTyping)},
		33: {act("Павел", model.ActionRecordVoice)},
		34: {act("Мария", model.ActionUploadPhoto)},
		35: {act("Сергей", model.ActionChooseSticker)},
	}
}

// mockstoreThumb is the smallest size of the demo's photo n, as a store
// picks for a thumbnail.
func mockstoreThumb(n int) string {
	return fmt.Sprintf("demo/photo/%d/m", n)
}

// mockstoreBlurred is a blurred picture, as Telegram's inline one: a
// small JPEG of soft colors.
func mockstoreBlurred() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			im.Set(x, y, color.RGBA{uint8(120 + 3*x), uint8(90 + 2*y), 170, 0xff})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, im, nil)
	return b.Bytes()
}

// mockstoreInline is the demo's photo n as Telegram's inline picture of a
// photo is: 40 pixels on its longer side, a JPEG of the quality of the
// tables its stripped thumbnails share.
func mockstoreInline(t *testing.T, demo *mockstore.Store, n int) []byte {
	data, err := demo.Media(context.Background(), model.Message{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: mockstoreThumb(n), MIMEType: "image/jpeg"}})
	if err != nil {
		t.Fatal(err)
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := im.Bounds()
	side := max(b.Dx(), b.Dy())
	small := image.NewRGBA(image.Rect(0, 0, max(1, b.Dx()*40/side), max(1, b.Dy()*40/side)))
	xdraw.ApproxBiLinear.Scale(small, small.Bounds(), im, b, xdraw.Src, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, small, &jpeg.Options{Quality: 20}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestRenderChatPreviews draws the chat list with a chat of each kind of
// last message, for looking at it: CHAT_PREVIEW_PNG=/tmp/previews.png,
// with -dark and -narrow beside it, -2x at twice the pixels, and -files
// and -2x-files with the photos' thumbnails downloaded, not Telegram's
// inline pictures, to compare.
func TestRenderChatPreviews(t *testing.T) {
	path := os.Getenv("CHAT_PREVIEW_PNG")
	if path == "" {
		t.Skip("set CHAT_PREVIEW_PNG to a file")
	}
	base := strings.TrimSuffix(path, ".png")
	now := time.Now()
	demo := mockstore.New(now, 0)
	inline := previewChats(now, func(n int) []byte { return mockstoreInline(t, demo, n) })
	files := previewChats(now, nil)
	for _, v := range []struct {
		suffix string
		width  int
		scale  float32
		dark   bool
		chats  []model.Chat
	}{
		{"", 600, 1.5, false, inline}, {"-dark", 600, 1.5, true, inline}, {"-narrow", 400, 1.5, false, inline},
		{"-files", 600, 1.5, false, files}, {"-2x", 800, 2, false, inline}, {"-2x-files", 800, 2, false, files},
	} {
		size := image.Pt(v.width, int(float32(len(v.chats)*64+11)*v.scale))
		renderChatPreviews(t, base+v.suffix+".png", size, v.scale, v.dark, v.chats)
	}
}

func renderChatPreviews(t *testing.T, path string, size image.Point, scale float32, dark bool, chats []model.Chat) {
	t.Helper()
	l := newChatList()
	source := previewSource{mockstore.New(time.Now(), 0)}
	images := new(imageOps)
	l.previews = &previewMedia{media: chatmedia.NewPreviews(source, func() {}), images: images}
	l.activity = newPreviewActivities(time.Now())
	// The demo's stories, over the chats.
	l.stories = source.demo
	defer l.previews.media.Close()
	catalog := localization.For("ru")
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	sc := schemes.SchemeBaselineLight()
	if dark {
		sc = schemes.SchemeBaselineDark()
	}
	ops := new(op.Ops)
	// The pictures load meanwhile.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: scale, PxPerSp: scale}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, sc))
		images.BeginFrame()
		l.previews.media.BeginFrame()
		l.Layout(gtx, section{kind: sectionAll}, nil, chats, 21, false, catalog)
		l.previews.media.EndFrame()
		images.EndFrame()
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
