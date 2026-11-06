// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
)

// uploadRedraw is how often a file on its way draws how far it went: the
// store tells nobody of a chunk, too many of them come.
const uploadRedraw = 100 * time.Millisecond

// uploading tells how far m, a file on its way to Telegram, went. A message
// that failed shows that instead: it waits for the user, moving nowhere.
func (p *chatPage) uploading(gtx layout.Context, m model.Message) (model.UploadStatus, bool) {
	if !m.Sending || m.SendError != "" {
		return model.UploadStatus{}, false
	}
	source, ok := p.source.(model.UploadSource)
	if !ok {
		return model.UploadStatus{}, false
	}
	status, ok := source.UploadStatus(m.Key)
	if ok {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(uploadRedraw)})
	}
	return status, ok
}

// uploadProgress is the share of status a ring draws, below 0 before its
// file's size is known.
func uploadProgress(status model.UploadStatus) float32 {
	if status.Total <= 0 {
		return -1
	}
	return min(1, float32(status.Done)/float32(status.Total))
}

// uploadPlateText is what the plate under a ring tells of status, as a file's
// row tells of a download; nothing before its size is known.
func uploadPlateText(status model.UploadStatus) string {
	if status.Total <= 0 {
		return ""
	}
	return sendfiles.Size(status.Done) + " / " + sendfiles.Size(status.Total)
}

// uploadPlateAt is where a plate lies under a ring of diameter in the middle
// of a tile: gap below it, in the middle, and inside the tile, over the ring's
// edge if it must be.
func uploadPlateAt(tile, plate image.Point, diameter, gap int) image.Point {
	y := min((tile.Y+diameter)/2+gap, tile.Y-plate.Y-gap)
	return image.Pt(max(0, (tile.X-plate.X)/2), max(0, y))
}

// cancelUpload takes m out of its message, or ends the message with the last
// of its files; the database is no business of a frame.
func (p *chatPage) cancelUpload(m model.Message) {
	source, ok := p.source.(model.UploadSource)
	if !ok {
		return
	}
	go func() {
		defer crash.Recover("cancel upload", nil)
		source.CancelUpload(m.Key)
	}()
}
