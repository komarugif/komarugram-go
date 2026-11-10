// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"

	"komarugram/internal/messenger/historylog"
	"komarugram/internal/messenger/model"
)

// logPaging tells the journal (historylog), each time it changes, whether
// the history at one of its ends asks for a page there, and what keeps it
// from asking: the flags of the history it was given, which the store's
// own may have left behind.
func (p *chatPage) logPaging(history model.History) {
	if !historylog.On() {
		return
	}
	n := len(p.messages)
	first := p.list.Position.First - len(p.above)
	top := first < 3
	bottom := first+p.list.Position.Count >= n-2
	why := ""
	switch {
	case !p.restored:
		why = "not restored"
	case top && !bottom:
		why = "top: " + pagingReason(history.HasOlder, history.LoadingOlder, history.Err, "no older")
	case bottom && !top:
		why = "bottom: " + pagingReason(history.HasNewer, history.LoadingNewer, history.Err, "no newer")
	case top && bottom:
		why = "top: " + pagingReason(history.HasOlder, history.LoadingOlder, history.Err, "no older") +
			", bottom: " + pagingReason(history.HasNewer, history.LoadingNewer, history.Err, "no newer")
	default:
		why = "between the ends"
	}
	why = fmt.Sprintf("chat=%d %s", p.chat, why)
	if why == p.pagingWhy {
		return
	}
	p.pagingWhy = why
	span := "-"
	if n > 0 {
		span = fmt.Sprintf("%d..%d", p.messages[0].Key.MessageID, p.messages[n-1].Key.MessageID)
	}
	historylog.Printf("view %s; shown %d rows %s, first %d of %d visible, history: older=%t newer=%t loadingOlder=%t loadingNewer=%t offline=%t err=%v rev=%d",
		why, n, span, p.list.Position.First, p.list.Position.Count, history.HasOlder, history.HasNewer, history.LoadingOlder, history.LoadingNewer, history.Offline, history.Err, history.Revision)
}

// pagingReason is what the view does at an end: asks for a page, or why not.
func pagingReason(has, loading bool, err error, none string) string {
	switch {
	case err != nil:
		return "STOPPED by error " + err.Error()
	case loading:
		return "loading"
	case !has:
		return none
	}
	return "asks"
}
