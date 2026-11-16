// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/model"
)

var (
	iconReset         = wdk.RequireIconWidget(icons.ActionRestore)
	iconKeyboard      = wdk.RequireIconWidget(icons.HardwareKeyboard)
	iconKeyboardHide  = wdk.RequireIconWidget(icons.HardwareKeyboardHide)
	iconReorder       = wdk.RequireIconWidget(icons.ActionReorder)
	iconAttach        = wdk.RequireIconWidget(icons.EditorAttachFile)
	iconSkipPrevious  = wdk.RequireIconWidget(icons.AVSkipPrevious)
	iconSkipNext      = wdk.RequireIconWidget(icons.AVSkipNext)
	iconVolumeUp      = wdk.RequireIconWidget(icons.AVVolumeUp)
	iconVolumeDown    = wdk.RequireIconWidget(icons.AVVolumeDown)
	iconVolumeOff     = wdk.RequireIconWidget(icons.AVVolumeOff)
	iconEmoji         = wdk.RequireIconWidget(icons.EditorInsertEmoticon)
	iconSend          = wdk.RequireIconWidget(icons.ContentSend)
	iconAttachPhoto   = wdk.RequireIconWidget(icons.ImagePhoto)
	iconAttachFile    = wdk.RequireIconWidget(icons.EditorInsertDriveFile)
	iconAttachTasks   = wdk.RequireIconWidget(icons.ActionCheckCircle)
	iconAllChats      = wdk.RequireIconWidget(icons.CommunicationForum)
	iconFolder        = wdk.RequireIconWidget(icons.FileFolder)
	iconPersonal      = wdk.RequireIconWidget(icons.SocialPerson)
	iconGroups        = wdk.RequireIconWidget(icons.SocialGroup)
	iconChannels      = wdk.RequireIconWidget(icons.ActionAnnouncement)
	iconBots          = wdk.RequireIconWidget(icons.ActionExtension)
	iconSearch        = wdk.RequireIconWidget(icons.ActionSearch)
	iconProfile       = wdk.RequireIconWidget(icons.ActionAccountCircle)
	iconSaved         = wdk.RequireIconWidget(icons.ActionBookmark)
	iconSettings      = wdk.RequireIconWidget(icons.ActionSettings)
	iconDark          = wdk.RequireIconWidget(icons.ImageBrightness2)
	iconLight         = wdk.RequireIconWidget(icons.ImageWBSunny)
	iconBack          = wdk.RequireIconWidget(icons.NavigationArrowBack)
	iconAhead         = wdk.RequireIconWidget(icons.NavigationArrowForward)
	iconMenu          = wdk.RequireIconWidget(icons.NavigationMenu)
	iconClear         = wdk.RequireIconWidget(icons.ContentClear)
	iconBlurOn        = wdk.RequireIconWidget(icons.ImageBlurOn)
	iconBlurOff       = wdk.RequireIconWidget(icons.ImageBlurOff)
	iconChevron       = wdk.RequireIconWidget(icons.NavigationChevronRight)
	iconExpandMore    = wdk.RequireIconWidget(icons.NavigationExpandMore)
	iconPeek          = wdk.RequireIconWidget(icons.ImageRemoveRedEye)
	iconVisibility    = wdk.RequireIconWidget(icons.ImageRemoveRedEye)
	iconAutoTheme     = wdk.RequireIconWidget(icons.DeviceBrightnessAuto)
	iconProxy         = wdk.RequireIconWidget(icons.HardwareRouter)
	iconInvertColors  = wdk.RequireIconWidget(icons.ActionInvertColors)
	iconExpandLess    = wdk.RequireIconWidget(icons.NavigationExpandLess)
	iconToTop         = wdk.RequireIconWidget(icons.NavigationArrowUpward)
	iconToBottom      = wdk.RequireIconWidget(icons.NavigationArrowDownward)
	iconEmojiPeople   = wdk.RequireIconWidget(icons.SocialMood)
	iconEmojiNature   = wdk.RequireIconWidget(icons.ActionPets)
	iconEmojiFood     = wdk.RequireIconWidget(icons.MapsRestaurant)
	iconEmojiActivity = wdk.RequireIconWidget(icons.MapsDirectionsBike)
	iconEmojiTravel   = wdk.RequireIconWidget(icons.MapsDirectionsCar)
	iconEmojiObjects  = wdk.RequireIconWidget(icons.ActionLightbulbOutline)
	iconEmojiSymbols  = wdk.RequireIconWidget(icons.ContentFlag)
	iconRefresh       = wdk.RequireIconWidget(icons.NavigationRefresh)
	iconRead          = wdk.RequireIconWidget(icons.ActionDoneAll)
	iconHistory       = wdk.RequireIconWidget(icons.ActionHistory)
	iconFilter        = wdk.RequireIconWidget(icons.ContentFilterList)
	iconTranslate     = wdk.RequireIconWidget(icons.ActionTranslate)
	iconRepeat        = wdk.RequireIconWidget(icons.AVRepeat)
	iconMic           = wdk.RequireIconWidget(icons.AVMic)
	iconCalendar      = wdk.RequireIconWidget(icons.ActionEvent)
	iconDataCenter    = wdk.RequireIconWidget(icons.ActionDNS)
	iconUserID        = wdk.RequireIconWidget(icons.ActionPermIdentity)
	iconMessages      = wdk.RequireIconWidget(icons.CommunicationChatBubbleOutline)
	iconPhone         = wdk.RequireIconWidget(icons.CommunicationCall)
	iconDevices       = wdk.RequireIconWidget(icons.DeviceDevices)
	iconChevronLeft   = wdk.RequireIconWidget(icons.NavigationChevronLeft)
	iconOpenInNew     = wdk.RequireIconWidget(icons.ActionOpenInNew)
	iconOpenInBrowser = wdk.RequireIconWidget(icons.ActionOpenInBrowser)
	iconPalette       = wdk.RequireIconWidget(icons.ImagePalette)
	iconPrivacy       = wdk.RequireIconWidget(icons.ActionLock)
	iconPower         = wdk.RequireIconWidget(icons.DeviceBatteryStd)
	iconNotifications = wdk.RequireIconWidget(icons.SocialNotifications)
	iconIntegrations  = wdk.RequireIconWidget(icons.ActionSettingsInputComponent)
	iconAddAccount    = wdk.RequireIconWidget(icons.SocialPersonAdd)
	iconLogOut        = wdk.RequireIconWidget(icons.ActionExitToApp)
	iconUninstall     = wdk.RequireIconWidget(icons.ActionDeleteForever)
	iconEdit          = wdk.RequireIconWidget(icons.EditorModeEdit)
	iconReply         = wdk.RequireIconWidget(icons.ContentReply)
	iconCopy          = wdk.RequireIconWidget(icons.ContentContentCopy)
	iconLink          = wdk.RequireIconWidget(icons.ContentLink)
	iconMusic         = wdk.RequireIconWidget(icons.ImageMusicNote)
	iconPlace         = wdk.RequireIconWidget(icons.MapsPlace)
	iconForward       = wdk.RequireIconWidget(icons.ContentForward)
	iconDelete        = wdk.RequireIconWidget(icons.ActionDelete)
	iconCheck         = wdk.RequireIconWidget(icons.NavigationCheck)
	iconGIF           = wdk.RequireIconWidget(icons.ActionGIF)
	iconEditBrush     = wdk.RequireIconWidget(icons.ImageBrush)
	iconEditCrop      = wdk.RequireIconWidget(icons.ImageCrop)
	iconEditRotate    = wdk.RequireIconWidget(icons.ImageRotateLeft)
	iconEditFlip      = wdk.RequireIconWidget(icons.ImageFlip)
	iconEditUndo      = wdk.RequireIconWidget(icons.ContentUndo)
	iconEditRedo      = wdk.RequireIconWidget(icons.ContentRedo)
	iconEditSticker   = wdk.RequireIconWidget(icons.EditorInsertEmoticon)
	iconEditText      = wdk.RequireIconWidget(icons.EditorTextFields)
	iconEditShapes    = wdk.RequireIconWidget(icons.ImageCropSquare)
	iconEditPen       = wdk.RequireIconWidget(icons.EditorModeEdit)
	iconEditArrow     = wdk.RequireIconWidget(icons.NavigationArrowForward)
	iconEditMarker    = wdk.RequireIconWidget(icons.EditorHighlight)
	iconEditEraser    = wdk.RequireIconWidget(icons.ContentBackspace)
	iconEditBlur      = wdk.RequireIconWidget(icons.ImageBlurOn)
	iconEditDelete    = wdk.RequireIconWidget(icons.ActionDelete)
	iconEditCopy      = wdk.RequireIconWidget(icons.ContentContentCopy)
	iconEditCircle    = wdk.RequireIconWidget(icons.ImagePanoramaFishEye)
	iconEditStar      = wdk.RequireIconWidget(icons.ToggleStarBorder)
	iconEditBubble    = wdk.RequireIconWidget(icons.CommunicationChatBubbleOutline)
	iconEditFill      = wdk.RequireIconWidget(icons.EditorFormatColorFill)
	iconEditAlignL    = wdk.RequireIconWidget(icons.EditorFormatAlignLeft)
	iconEditAlignC    = wdk.RequireIconWidget(icons.EditorFormatAlignCenter)
	iconEditAlignR    = wdk.RequireIconWidget(icons.EditorFormatAlignRight)
	iconSelect        = wdk.RequireIconWidget(icons.ActionCheckCircle)
	iconReacted       = wdk.RequireIconWidget(icons.ActionFavoriteBorder)
	iconLiked         = wdk.RequireIconWidget(icons.ActionFavorite)
	iconMore          = wdk.RequireIconWidget(icons.NavigationMoreVert)
	iconDownload      = wdk.RequireIconWidget(icons.FileFileDownload)

	// The actions of a channel's bottom bar.
	iconDirectMessages   = wdk.RequireIconWidget(icons.ContentCreate)
	iconDiscussion       = wdk.RequireIconWidget(icons.CommunicationChatBubbleOutline)
	iconNotificationsOn  = wdk.RequireIconWidget(icons.SocialNotificationsNone)
	iconNotificationsOff = wdk.RequireIconWidget(icons.SocialNotificationsOff)
	iconGift             = wdk.RequireIconWidget(icons.ActionCardGiftcard)

	// Polls: a quiz's explanation, a wrong answer, and the menu's items
	// that take a vote back and close a poll.
	iconSolution  = wdk.RequireIconWidget(icons.ActionLightbulbOutline)
	iconPollWrong = wdk.RequireIconWidget(icons.NavigationClose)
	iconRetract   = wdk.RequireIconWidget(icons.ContentUndo)
	iconStopPoll  = wdk.RequireIconWidget(icons.AVStop)
	// The mini icons of a poll and a checklist in the chat list.
	iconPollMini = wdk.RequireIconWidget(icons.SocialPoll)
	iconTodoMini = wdk.RequireIconWidget(icons.ActionAssignmentTurnedIn)
)

// folderIcon picks an icon for a folder by the kinds of chats it holds.
func folderIcon(f model.Folder) wdk.IconWidget {
	if len(f.Kinds) == 0 {
		return iconFolder
	}
	switch f.Kinds[0] {
	case model.KindUser:
		return iconPersonal
	case model.KindGroup:
		return iconGroups
	case model.KindChannel:
		return iconChannels
	case model.KindBot:
		return iconBots
	default:
		return iconFolder
	}
}

// iconExternalPlayer opens a video in the external player.
var iconExternalPlayer = wdk.RequireIconWidget(icons.ActionLaunch)

// iconQRCode opens a profile's QR code: Material Symbols' qr_code, which
// the icons of golang.org/x/exp/shiny lack, drawn from its squares on the
// icon's grid of 24: three frames in the corners, and the modules of the
// fourth.
func iconQRCode(gtx layout.Context, c token.MatColor) layout.Dimensions {
	side := min(gtx.Constraints.Max.X, gtx.Constraints.Max.Y)
	unit := float32(side) / 24
	var path clip.Path
	path.Begin(gtx.Ops)
	rect := func(x, y, w, h float32) {
		path.MoveTo(f32.Pt(x*unit, y*unit))
		path.LineTo(f32.Pt((x+w)*unit, y*unit))
		path.LineTo(f32.Pt((x+w)*unit, (y+h)*unit))
		path.LineTo(f32.Pt(x*unit, (y+h)*unit))
		path.Close()
	}
	for _, at := range [][2]float32{{3, 3}, {13, 3}, {3, 13}} {
		x, y := at[0], at[1]
		rect(x, y, 8, 2)
		rect(x, y+6, 8, 2)
		rect(x, y+2, 2, 4)
		rect(x+6, y+2, 2, 4)
	}
	for _, at := range [][2]float32{{13, 13}, {17, 13}, {15, 15}, {19, 15}, {13, 17}, {17, 17}, {15, 19}, {19, 19}} {
		rect(at[0], at[1], 2, 2)
	}
	paint.FillShape(gtx.Ops, c.AsNRGBA(), clip.Outline{Path: path.End()}.Op())
	return layout.Dimensions{Size: gtx.Constraints.Max}
}
