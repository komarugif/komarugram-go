// SPDX-License-Identifier: Unlicense OR MIT

package localization

import (
	"time"

	"komarugram/internal/messenger/model"
)

// Activity words what others do in a chat now, as Telegram Desktop's list
// and header say it (SendActionPainter): who types, by how many, before
// anything else; then the first other action; then a game. In a private
// chat, private, the one who does it goes unnamed. Only the activities of
// thread top count, or of any for -1, and those not over at now. It
// returns "" when nobody does anything, and the action it words.
func (c Catalog) Activity(acts []model.Activity, top int, private bool, now time.Time) (string, model.ChatAction) {
	live := func(a model.Activity) bool { return a.Until.After(now) && (top < 0 || a.Top == top) }
	typing, games := 0, 0
	var first, last, gamer, lastGamer string
	other := -1
	for i, a := range acts {
		if !live(a) {
			continue
		}
		switch a.Action {
		case model.ActionTyping:
			if typing == 0 {
				first = a.Name
			}
			last = a.Name
			typing++
		case model.ActionPlayGame:
			if games == 0 {
				gamer = a.Name
			}
			lastGamer = a.Name
			games++
		default:
			if other < 0 {
				other = i
			}
		}
	}
	switch {
	case typing > 2:
		return c.Count("action.many_typing", typing, nil), model.ActionTyping
	case typing == 2:
		return c.Format("action.users_typing", map[string]string{"user": first, "second_user": last}), model.ActionTyping
	case typing == 1 && private:
		return c.T("action.typing"), model.ActionTyping
	case typing == 1:
		return c.Format("action.user_typing", map[string]string{"user": first}), model.ActionTyping
	case other >= 0:
		a := acts[other]
		key := actionKeys[a.Action]
		if a.Action == model.ActionChooseLocation || a.Action == model.ActionChooseContact {
			// Telegram Desktop says so of them too.
			key = "typing"
		}
		if private {
			return c.T("action." + key), a.Action
		}
		return c.Format("action.user_"+key, map[string]string{"user": a.Name}), a.Action
	case games > 2:
		return c.Count("action.many_game", games, nil), model.ActionPlayGame
	case games == 2:
		return c.Format("action.users_game", map[string]string{"user": gamer, "second_user": lastGamer}), model.ActionPlayGame
	case games == 1 && private:
		return c.T("action.game"), model.ActionPlayGame
	case games == 1:
		return c.Format("action.user_game", map[string]string{"user": gamer}), model.ActionPlayGame
	}
	return "", model.ActionNone
}

// actionKeys name the texts of actions, after "action." or "action.user_".
var actionKeys = map[model.ChatAction]string{
	model.ActionRecordVoice:   "record_voice",
	model.ActionUploadVoice:   "upload_voice",
	model.ActionRecordVideo:   "record_video",
	model.ActionUploadVideo:   "upload_video",
	model.ActionRecordRound:   "record_round",
	model.ActionUploadRound:   "upload_round",
	model.ActionUploadPhoto:   "upload_photo",
	model.ActionUploadFile:    "upload_file",
	model.ActionChooseSticker: "choose_sticker",
}
