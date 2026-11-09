// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"

	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/localization"
)

// tellHistoryError shows in the toast a failure of the history to load,
// once as it comes: each failure is an error of its own, which the
// history keeps until a page is asked for again. Without it a failure
// showed only as the retry button, and a FLOOD_WAIT did not say for how
// long Telegram would refuse.
func (p *chatPage) tellHistoryError(err error, l localization.Catalog) {
	// errors.Is compares only what can be compared: an error of a type
	// that cannot be would panic under ==.
	if err != nil && !errors.Is(err, p.toldErr) {
		p.toast.Show(historyErrorText(err, l))
	}
	p.toldErr = err
}

// historyErrorText is what the toast says of a failure to load the history.
func historyErrorText(err error, l localization.Catalog) string {
	if wait, ok := tgerr.AsFloodWait(err); ok {
		return l.Format("history.flood_wait", map[string]string{"time": waitText(wait, l)})
	}
	return l.Format("history.load_failed", map[string]string{"error": mediaErrorText(err)})
}
