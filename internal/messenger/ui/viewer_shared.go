// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"time"

	"komarugram/internal/messenger/model"
)

// OpenShared shows m, a photo or a video of section kind of chat's info,
// among the others of the section alone, paged as the section is: a
// section of photos goes through photos, one of videos through videos,
// where the chat goes through both. known are the section's messages. A
// video plays from at, below 0 from the start, as OpenVideo plays it; a
// photo is shown.
func (v *mediaViewer) OpenShared(chat int64, m model.Message, known []model.Message, kind model.SharedKind, at time.Duration) {
	if v.shared == nil || kind != model.SharedPhotos && kind != model.SharedVideos {
		if m.Kind == model.MessageVideo {
			v.OpenVideo(chat, m, known, at)
		} else {
			v.Open(chat, m, known)
		}
		return
	}
	v.openList(chat, m, mediaList{items: known, gallery: galleryShared, shared: kind})
	if m.Kind == model.MessageVideo {
		v.autoplay, v.autoplayAt = m.Key.MessageID, at
	}
}

// sharedOnly is items without those of another kind than the section's
// the viewer goes through, all of them for no section.
func (v *mediaViewer) sharedOnly(items []model.Message) []model.Message {
	if v.sharedKind == "" {
		return items
	}
	want := model.MessagePhoto
	if v.sharedKind == model.SharedVideos {
		want = model.MessageVideo
	}
	out := items[:0:0]
	for _, m := range items {
		if m.Kind == want {
			out = append(out, m)
		}
	}
	return out
}
