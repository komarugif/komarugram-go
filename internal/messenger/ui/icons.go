// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/wdk"

	"golang.org/x/exp/shiny/materialdesign/icons"

	"komarugram/internal/messenger/model"
)

var (
	iconAttach        = wdk.RequireIconWidget(icons.EditorAttachFile)
	iconSkipPrevious  = wdk.RequireIconWidget(icons.AVSkipPrevious)
	iconSkipNext      = wdk.RequireIconWidget(icons.AVSkipNext)
	iconVolumeUp      = wdk.RequireIconWidget(icons.AVVolumeUp)
	iconVolumeDown    = wdk.RequireIconWidget(icons.AVVolumeDown)
	iconVolumeOff     = wdk.RequireIconWidget(icons.AVVolumeOff)
	iconEmoji         = wdk.RequireIconWidget(icons.EditorInsertEmoticon)
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
	iconChevron       = wdk.RequireIconWidget(icons.NavigationChevronRight)
	iconExpandMore    = wdk.RequireIconWidget(icons.NavigationExpandMore)
	iconExpandLess    = wdk.RequireIconWidget(icons.NavigationExpandLess)
	iconToTop         = wdk.RequireIconWidget(icons.EditorVerticalAlignTop)
	iconToBottom      = wdk.RequireIconWidget(icons.EditorVerticalAlignBottom)
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
	iconEdit          = wdk.RequireIconWidget(icons.EditorModeEdit)
	iconReply         = wdk.RequireIconWidget(icons.ContentReply)
	iconCopy          = wdk.RequireIconWidget(icons.ContentContentCopy)
	iconLink          = wdk.RequireIconWidget(icons.ContentLink)
	iconMusic         = wdk.RequireIconWidget(icons.ImageMusicNote)
	iconPlace         = wdk.RequireIconWidget(icons.MapsPlace)
	iconForward       = wdk.RequireIconWidget(icons.ContentForward)
	iconDelete        = wdk.RequireIconWidget(icons.ActionDelete)
	iconSelect        = wdk.RequireIconWidget(icons.ActionCheckCircle)
	iconReacted       = wdk.RequireIconWidget(icons.ActionFavoriteBorder)
	iconMore          = wdk.RequireIconWidget(icons.NavigationMoreVert)
	iconDownload      = wdk.RequireIconWidget(icons.FileFileDownload)
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
