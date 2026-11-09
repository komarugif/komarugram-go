package ui

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/layout"
	"gioui.org/op"
)

// TestRenderSkeletonBars saves the same rows of a history drawn as the
// skeletons of the reserve, with the bars their profiles tell, and laid
// out, for looking at how the messages fall on their skeletons:
// SKELETON_PNG_DIR=/tmp/skeletons.
func TestRenderSkeletonBars(t *testing.T) {
	dir := os.Getenv("SKELETON_PNG_DIR")
	if dir == "" {
		t.Skip("set SKELETON_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	messages := predictRows(7001)[:14]
	for i := range messages {
		messages[i].Key.ChatID = 1
	}
	size := image.Pt(520, 1400)
	chat := model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}
	l := localization.For("ru")
	// Laid out.
	var calls []string
	real := newChatPage(jumpSource{benchmarkHistory{h: model.History{Messages: messages, Revision: 1}}, &calls, nil}, func() {})
	defer real.Close()
	renderFrames(t, size, filepath.Join(dir, "messages.png"), func(gtx layout.Context) {
		real.Layout(gtx, chat, l, false)
		if real.restored {
			real.list.Position.First, real.list.Position.Offset, real.list.Position.BeforeEnd = 0, 0, true
		}
	})
	// The first two loaded, the rest skeletons of the reserve, from the
	// profiles their rows took laid out.
	page := newChatPage(jumpSource{benchmarkHistory{h: model.History{Messages: messages[:2], Revision: 1, HasNewer: true}}, &calls, nil}, func() {})
	defer page.Close()
	frames := 0
	renderFrames(t, size, filepath.Join(dir, "skeletons.png"), func(gtx layout.Context) {
		if frames == 1 {
			var rows []model.ReserveRow
			// Profiles taken anew: the frames took them already.
			real.profiled = nil
			for _, m := range messages[2:] {
				delete(real.rows, m.Key.MessageID)
				var r *messageRow
				height := 0
				for range 2 {
					row := real.measurer.gtx
					row.Ops = new(op.Ops)
					height = real.row(row, m, false, real.joins[indexOf(real.messages, m.Key.MessageID)], l, false).Size.Y
					r = real.rows[m.Key.MessageID]
				}
				data := real.takeProfile(r, m, real.env.WidthPx, height)
				if data == nil {
					t.Logf("message %d took no profile", m.Key.MessageID)
				}
				rows = append(rows, model.ReserveRow{ID: m.Key.MessageID, Revision: m.ContentRevision, Outgoing: m.Outgoing, Height: height, HeightRevision: m.ContentRevision, Profile: data, ProfileRevision: m.ContentRevision})
			}
			page.reserve = reserve{chat: 1, env: page.env, asked: true}
			page.reserveDone = make(chan reserveResult, 1)
			tellReserve(page.reserveDone, reserveResult{chat: 1, env: page.env, rows: reserveRows(rows, page.env)})
		}
		frames++
		page.Layout(gtx, chat, l, false)
		if page.restored {
			page.list.Position.First, page.list.Position.Offset, page.list.Position.BeforeEnd = 0, 0, true
		}
	})
}
