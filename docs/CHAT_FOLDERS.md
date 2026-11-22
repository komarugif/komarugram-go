# Chat folders and `t.me/addlist`

Implementation notes for KomaruGram at `2f1ee18`. The references are the
Telegram Desktop 7.3.1 and Telegram for Android 13.1.1 (7237) source trees
and the Telegram API. The flows below were traced in source on Windows.

## Which client to follow

| Surface | Upstream behavior | Recommendation for KomaruGram |
| --- | --- | --- |
| Settings | Desktop places Chat Folders immediately below Chat Settings. Both clients have a folder list, creation, editing, ordering and server-suggested folders. | Add **Settings → Chat Folders** immediately below **Chat Settings**, including for an account with zero folders. Make **Create folder** available in the empty state. Show suggestions when available. |
| Folder context menu | Desktop offers **Edit**, **Mark as read** when there is something to read, and **Remove**. On **All chats** it offers the applicable read action and folder settings. Android's long press also offers actions such as reorder, mute and share. | Use Desktop's menu on a secondary click in the sidebar, horizontal tabs and drawer. Target the clicked folder even if another one is selected. Open the same editor from Settings and the menu. Keep reorder in Settings and invitation management in the editor. |
| Folder editor | Desktop edits the name, icon, included and excluded chats, color and links in one place. Android uses searchable selections and explains why a folder or chat cannot be shared. | Use Desktop's editor structure with Android's selection and eligibility feedback. Keep a draft while editing. On Back or a sidebar destination, offer to save the changes, leave without saving or keep editing. |
| Opening a folder link | Desktop has separate headings for adding a folder, adding chats and everything already added. Android's `FolderBottomSheet` reuses a selectable list for import, updates and leaving. | Use one Gio selection dialog with distinct headings, counts and action verbs for each operation. The button should say what will happen, e.g. **Add folder**, **Add 3 chats** or **Remove folder and leave 2 chats**. |

Desktop entry points are [Settings](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/settings/sections/settings_main.cpp#L396-L433), [folder settings](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/settings/sections/settings_folders.cpp), and [FiltersMenu::showMenu](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/window/window_filters_menu.cpp#L746-L798).
Android counterparts are [FiltersSetupActivity](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/FiltersSetupActivity.java), the [tab menu](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/DialogsActivity.java#L3798-L3831), and [FolderBottomSheet](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/Components/FolderBottomSheet.java).

## What the Go client already knows

| Existing path | Consequence for implementation |
| --- | --- |
| [tgstore/dialog_filters.go](../internal/messenger/tgstore/dialog_filters.go), [model/folders.go](../internal/messenger/model/folders.go) | The account keeps the full Telegram filter response. The UI sees ordered ordinary, shared and default folders, including explicit peers, pins, rule flags, title entities and optional color. The raw peer access hashes remain available for subsequent edits. |
| [tgstore/store.go](../internal/messenger/tgstore/store.go), [dialogs_kept.go](../internal/messenger/tgstore/dialogs_kept.go), [history.go](../internal/messenger/tgstore/history.go) | Main and archived dialogs are fetched separately. The offline snapshot restores their membership inputs, folder definitions, order and tag switch. Filter updates refresh the snapshot independently of dialog pagination. |
| [folderbar.go](../internal/messenger/ui/folderbar.go), [sidebar.go](../internal/messenger/ui/sidebar.go), [drawer.go](../internal/messenger/ui/drawer.go), [chatlist.go](../internal/messenger/ui/chatlist.go) | Navigation uses the ordered folder list, including All chats. A custom folder has its own pins and can show archived chats. If the selected folder disappears the window returns to All chats. The sidebar and drawer each have one Chat Folders entry in Menu item order. It moves or hides the entire group. Folder settings determine the order within it. |
| [settings_folders.go](../internal/messenger/ui/settings_folders.go), [mockstore.go](../internal/messenger/mockstore/mockstore.go) | Settings → Chat Folders is available below Chat Settings, including on accounts with no custom folders. Select any combination of the five chat types. A single selection suggests a localized name until the user edits it. Drag a folder by its handle to change its position. Telegram confirms each change before the client publishes it. Click a custom folder to edit it. A draft holds its name, icon, chat types, read/mute/archive exclusions and explicit peers. Both peer selectors reuse the forwarding list with multiple selection and local username search. Recommended filters open as editable creation templates. Shared folders expose only explicit membership. Secondary-click a sidebar folder or horizontal tab to open this editor, mark the folder read or remove it. Deletion with an empty leave set preserves all chats. |
| [model/tglink.go](../internal/messenger/model/tglink.go), [ui/history.go](../internal/messenger/ui/history.go), [ui/tglinks.go](../internal/messenger/ui/tglinks.go) | `addlist` is a reserved web path with no parser case. The internal link dispatcher already has asynchronous preview and confirmation for single-chat invites. Give folder invites a distinct link kind, result and selected peer list. On success navigate to the folder. |

## Editing, ordering and reading folders

**Store the definition alongside its display projection.** Add a typed,
copyable definition at the model/store boundary. Distinguish ordinary,
shared (`dialogFilterChatlist`) and built-in All chats. Keep the ID, title
with entities, `title_noanimate`, optional emoticon and color, rule flags,
ordered pins, explicit includes and excludes. Shared folders carry explicit
pins/includes and the server's `has_my_invites` flag. That flag says the
account has its own links for this folder. The constructor determines which
editing controls apply. Preserve peer type and access hash in the store,
including peers outside the local dialog list. Renaming a folder must round
trip all its other fields. [TL constructors and methods](https://core.telegram.org/api/folders)

**Membership.** Offer contacts, non-contacts, groups, channels and bots as
include rules, plus individual chats. The exclude controls are muted, read,
archived and individual chats. Shared folders only offer explicit membership.
Desktop checks an explicit exclusion first, then honors explicit inclusions
and pins independently of category rules. Its automatic rules account for
manual unread marks, unread mentions and archive state. In particular, a
muted main-list chat with an unread mention can still match. The Go client applies these rules to cached dialogs from both lists.
The editor should make selections mutually exclusive. Preserve unresolved peers through
edits. The chat-row **Add to / Remove from folder** actions remove the opposing
exception, reevaluate membership, and add an explicit exception only if the
rules still give the wrong result. Removing an explicitly pinned chat also
removes its folder pin. Pinning puts the chat first in the selected folder. Unpinning keeps it explicitly included. These actions use the same checked save and cache update as the editor. [Desktop membership](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/data/data_chat_filters.cpp#L339-L386) · [Chat-row actions](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/boxes/choose_filter_box.cpp#L115-L157)

**Archive and pins.** Full membership needs archived dialogs as candidates
when an explicit inclusion or a rule admits them. Track archive membership
and keep the main view scoped to the main list when loading both. The
existing storage-channel lookup already pages archive ID 1. Moving a chat
between the archive and main list uses `folders.editPeerFolders`. Editing
a custom folder uses `messages.updateDialogFilter`. A chat can belong to
several custom folders. Order folder pins by that filter's `pinned_peers`
and keep them independent of main-list pins and local pin preferences.
[Archive API](https://core.telegram.org/api/folders#peer-folders) · [Existing archive lookup](../internal/messenger/tgstore/storage_channel.go)

**Create, save, delete and reorder.** Both editors use a 12-character
title limit. Count visible title characters with emoji in mind. Require a
name and at least one included type or explicit chat. Desktop also rejects
a duplicate of All chats (all types, exclude archived, no exceptions).
Allocate an unused ID starting at 2 against the current account snapshot.
Save the whole definition with `messages.updateDialogFilter(id, filter)`.
For ordinary deletion, confirm the name and omit the optional filter field.
Send the ordered IDs with `messages.updateDialogFiltersOrder` after required
creates/deletes succeed. Represent All chats as ID 0 in the order, fixed
first for non-Premium users and movable for Premium users. Keep it out of
the custom-folder count. Offer `messages.getSuggestedDialogFilters` results
as templates that the user can add. [Desktop validation](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/boxes/filters/edit_filter_box.cpp#L981-L1010) · [Android editor and save path](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/FilterCreateActivity.java)

**Implemented read and removal.** The menu reads from an account-wide snapshot
of matching dialogs, including manual unread marks and eligible archived chats.
An older cache without a completion marker needs one full pass over the main
and archive lists before bulk read is available. Retries after an interrupted
first load continue beyond an unchanged first page. The store processes reads
sequentially, pages forum topics and saves the acknowledged results. Removal
uses Desktop's empty-leave-set path and never leaves a chat.

**Mark as read.** Desktop snapshots the folder's histories before iterating.
This matters in an Unread folder, where reading one chat can remove its row
immediately. It hides the action for an empty unread state and asks for
confirmation above 1,000 unread messages. Use one store operation capturing
the target folder and its unread chats at activation. Reuse the explicit
user-requested read path (`MarkChatRead` → `MarkRead(..., true)`) and its
Ghost semantics. Cover manually marked chats even when their unread count
is zero, forum topics, and missing top IDs. Bound concurrent requests and
reconcile partial success with the server. Simply looping over the visible
rows would miss collapsed or off-screen chats. [Desktop read action](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/menu/menu_mark_as_read.cpp) · [Go read entry point](../internal/messenger/tgstore/pin.go)

**Synchronization and limits.** Apply `updateDialogFilter` as upsert or
delete and `updateDialogFilterOrder` as reorder. On `updateDialogFilters`,
unknown IDs or a stale order, refetch `messages.getDialogFilters`. Store
definitions, order and the account-wide `tags_enabled` together in a
versioned per-account cache entry. Older caches can populate it on the
next successful fetch. Publish one coherent snapshot to all windows.
Serialize local edits per account, check a draft against its starting
version before saving, and prevent an older paginated dialog reload from
publishing stale filters. A failed save should leave the draft available.
Offline, display the last confirmed snapshot and keep remote actions explicit.

Use the existing `Premium.Limit` for `dialog_filters_limit`,
`dialog_filters_chats_limit`, `dialogs_folder_pinned_limit` and
`chatlists_joined_limit`. Add the per-folder `chatlist_invites_limit` and
read `chatlist_update_period` from app config. For Premium folder tags,
keep the account switch (`messages.toggleDialogFilterTags`) independent
of each folder's optional color. [Go limits](../internal/messenger/model/premium.go) · [Telegram client configuration](https://core.telegram.org/api/config) · [Folder tags](https://core.telegram.org/api/folders#folder-tags)

### Proposed Local Premium folder tags

A folder tag is its name and color shown below a chat's message preview.
One chat can show several tags in folder order. Compute them from current
membership so renames and automatic rules work offline. Omit All chats,
folders without a tag color and, as in Desktop, the currently open folder's
own tag. Preserve the distinction between red (color 0) and no tag.

Offer a per-account **Folder tags on this device** switch alongside the
other Local Premium options. Keep its color choices keyed by folder ID in
a separate local collection. On an account without a subscription, render
from those choices and the cached definitions. Keep local colors out of
filter edits and exports. Disabling this option restores the server's
presentation. With Telegram Premium use the account's server setting and
the ordinary folder color editor by default. Clear overrides for confirmed
deletions so a new folder reusing an ID cannot inherit the old color.
This proposal needs no per-chat tag table or storage-channel synchronization.
[Local Premium](LOCAL_PREMIUM.md) · [Telegram folder tags](https://core.telegram.org/api/folders#folder-tags)

## Archived Chats in the official clients

Archive navigation was traced in the Telegram Desktop 7.4.4 stable release,
the 7.4.5 prerelease and the Telegram for Android 13.2.2 (7303) source.

- **Desktop.** The archive row belongs to **All chats** and can be collapsed
  or moved to the main menu. The menu opens the same account-wide archive
  from any selected folder and can open it in a separate window. A custom
  folder lists matching chats, including eligible archived chats. The archive
  itself opens as a single list with no custom-folder filter.
  [Opening the archive](https://github.com/telegramdesktop/tdesktop/blob/v7.4.4/Telegram/SourceFiles/window/window_session_controller.cpp#L2263-L2276) · [Archive entry](https://github.com/telegramdesktop/tdesktop/blob/v7.4.4/Telegram/SourceFiles/window/window_main_menu.cpp#L491-L538) · [Filter population](https://github.com/telegramdesktop/tdesktop/blob/v7.4.4/Telegram/SourceFiles/data/data_chat_filters.cpp#L737-L771)
- **Android.** The archive row is in **All chats**. The user can pin it in
  the list or hide it above the list and reveal it by pulling down. Opening
  the row starts a separate chat-list screen with `folderId = 1`. Folder tabs
  are created on the main screen with `folderId = 0`.
  [Archive row](https://github.com/TelegramMessenger/Telegram-Android/blob/21f7d109da6ab3506c9e9577f98c6a38771ab335/TMessagesProj/src/main/java/org/telegram/messenger/MessagesController.java#L22679-L22702) · [Opening it](https://github.com/TelegramMessenger/Telegram-Android/blob/21f7d109da6ab3506c9e9577f98c6a38771ab335/TMessagesProj/src/main/java/org/telegram/ui/DialogsActivity.java#L7819-L7830) · [Tabs](https://github.com/TelegramMessenger/Telegram-Android/blob/21f7d109da6ab3506c9e9577f98c6a38771ab335/TMessagesProj/src/main/java/org/telegram/ui/DialogsActivity.java#L3469-L3473) · [Hide and reveal](https://telegram.org/blog/folders#archived-chats)
- **Membership.** In both clients an archived chat can still appear in a
  custom folder. Explicit exclusion wins, while explicit inclusion or a
  folder pin overrides the automatic **Exclude archived** rule. The API
  supports that exclusion and provides no automatic **Only archived** rule.
  [Desktop matching](https://github.com/telegramdesktop/tdesktop/blob/v7.4.4/Telegram/SourceFiles/data/data_chat_filters.cpp#L362-L387) · [Android matching](https://github.com/TelegramMessenger/Telegram-Android/blob/21f7d109da6ab3506c9e9577f98c6a38771ab335/TMessagesProj/src/main/java/org/telegram/messenger/MessagesController.java#L1309-L1325) · [Telegram folder API](https://core.telegram.org/api/folders)
- **Archive Settings.** Both clients expose `keep_archived_unmuted` and
  `keep_archived_folders`. The second setting can keep an unmuted chat in
  the archive when it receives a new message, if the chat was explicitly
  included or pinned in a custom folder. This controls automatic unarchiving.
  [Telegram privacy settings](https://core.telegram.org/constructor/globalPrivacySettings) · [Desktop controls](https://github.com/telegramdesktop/tdesktop/blob/v7.4.4/Telegram/SourceFiles/settings/sections/settings_advanced.cpp#L1908-L1989) · [Android controls](https://github.com/TelegramMessenger/Telegram-Android/blob/21f7d109da6ab3506c9e9577f98c6a38771ab335/TMessagesProj/src/main/java/org/telegram/ui/ArchiveSettingsActivity.java#L92-L122)

In KomaruGram, **Visibility → Archived chats** is on by default.
The switch follows the shared Visibility settings until the account pins
its own settings for the section.
It shows the archive card in **All chats** when the archive has chats.
The card opens the archive, which has a
matching **All chats** card for returning. Each card previews the destination's
most recent chat titles. Both lists share the same stories row and folder
navigation. Inside the archive a folder matches archived chats and ignores
its **Exclude archived** rule. The remaining rules and explicit exceptions
apply as usual. Both lists work from the cached dialog snapshot.
Chat-row actions move dialogs with `folders.editPeerFolders` using IDs 1 and 0.
The store applies `updateFolderPeers` from the response and from other sessions.
The same settings card exposes **Archive and Mute** for new chats from
non-contacts. Enabling it requires Telegram Premium. Its value belongs to
the Telegram account. The client refreshes
it when the Visibility page opens or regains focus and writes only the
`archive_and_mute_new_noncontact_peers` flag of the latest global privacy object.
[Cached dialog updates](../internal/messenger/tgstore/dialogs_kept.go) · [Folder matching](../internal/messenger/model/folders.go) · [Archive API](https://core.telegram.org/api/folders#peer-folders)

## Shared folders and their links

**Receiving a link**

1. Recognize `t.me/addlist/<slug>` and `tg://addlist?slug=<slug>` in the
   existing parser. Retain the existing supported web hosts. Android also
   accepts `tg:addlist?slug=…`. Desktop accepts letters, digits, dot,
   underscore and hyphen in the slug. Preserve case. Reject an empty slug,
   extra path segments and lookalike hosts. Route message clicks and any
   supported application URL entry point to the same account-bound handler.
2. Call `chatlists.checkChatlistInvite(slug)`. Ingest its accompanying
   chats and users before building rows or constructing input peers. For
   `chatlistInvite`, show the supplied folder title, icon and peers. For
   `chatlistInviteAlready`, resolve the existing folder by `filter_id`.
   Desktop reloads filters if this ID is missing locally. Show
   `missing_peers` as candidates and `already_peers` as checked,
   noninteractive rows. If there is nothing to add, offer to open the folder.
3. Preselect eligible candidates and allow a subset. Show inaccessible
   peers disabled with a reason. A fresh import needs at least one eligible
   selected peer. Confirming a link with a selection calls
   `chatlists.joinChatlistInvite(slug, peers)` even on the Already path.
   Apply the returned `Updates` through the account's update path and
   select the resulting filter. Cancel, empty selection on Already, and
   merely opening a preview make no membership change. Guard double clicks
   and discard late results after an account switch or a closed preview.

[Desktop URL handlers](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/core/local_url_handlers.cpp) · [Desktop check and import](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/api/api_chat_filters.cpp) · [Android link dispatch](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/LaunchActivity.java#L4162-L4195) · [Chat folder deep links](https://core.telegram.org/api/links#chat-folder-links)

**Creating and managing links.** Save the folder first. Both clients require
explicit chats for export and reject category rules or exclusions. Explain
which setting prevents sharing and keep the user's filter intact. Let the
user choose a nonempty subset of eligible groups and channels. Private
conversations and bots are ineligible. Desktop and Android allow a public
channel/supergroup without join requests, or one where the account has
invite rights. Basic groups require invite rights and Telegram can migrate
them to supergroups during export. Give unavailable rows a specific reason.
[Desktop sharing rules](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/boxes/filters/edit_filter_links.cpp) · [Sharing API](https://core.telegram.org/api/folders#shared-folders)

| Action in the folder editor | Request and result |
| --- | --- |
| Create link | `chatlists.exportChatlistInvite` takes the filter ID, link label and selected peers. Apply **both** the returned filter and invitation. The first export can turn an ordinary filter into a shared one. Display the returned URL. |
| Manage links | `chatlists.getExportedInvites` lists this account's links for the filter. Each has its own URL, optional administrative label and peer subset. Offer copy and share. Desktop also offers a QR code. |
| Rename or change included chats | `chatlists.editExportedInvite` addresses the link by filter ID and slug. Set only the fields the user changed. Updating one invitation must preserve its siblings. |
| Revoke | Confirm the specific link, call `chatlists.deleteExportedInvite` and refresh its state. Revocation prevents future imports through that URL. Existing recipients keep their folders and memberships. |

[Desktop link controls](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/boxes/filters/edit_filter_links.cpp) · [Android link editor](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/FilterChatlistActivity.java)

**New chats after import.** Desktop polls a watched shared folder, with
one request in flight and a delay from `chatlist_update_period`. Use
`chatlists.getChatlistUpdates` while the folder is visible. When
`missing_peers` is nonempty, show a banner opening the same peer selector.
Accepting uses `chatlists.joinChatlistUpdates(filter, selected)`. Explicit
dismissal of the offer, including confirming an empty selection, uses
`chatlists.hideChatlistUpdates`. Closing a preview can leave the offer
available. Reconcile successful joins before clearing the banner.
[Desktop polling](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/data/data_chat_filters.cpp#L1102-L1152) · [Android selection and requests](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/Components/FolderBottomSheet.java#L374-L420)

**Removing a shared folder.** Both clients distinguish removing the
folder from leaving its chats. Show the folder name and, when the account
has exported links, explain the effect on its invites. Fetch
`chatlists.getLeaveChatlistSuggestions` for an imported folder. Premark
the suggested chats and let the user clear every selection. Desktop
uses plain filter deletion for an empty leave set and
`chatlists.leaveChatlist` otherwise. Android's shared-folder sheet sends
`leaveChatlist` with the selected vector, including an empty one. Follow
Android/API for imported folders so **Remove folder and keep all chats**
is an explicit operation. Apply returned updates. Ordinary deletion never
selects chats to leave. [Desktop removal](https://github.com/telegramdesktop/tdesktop/blob/v7.3.1/Telegram/SourceFiles/api/api_chat_filters_remove_manager.cpp) · [Android removal sheet](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/Components/FolderBottomSheet.java#L110-L228)

**Errors.** Distinguish an expired/revoked invite (`INVITE_SLUG_EXPIRED`),
an empty selection (`PEERS_LIST_EMPTY`), too many selected or included
chats (`INVITE_PEERS_TOO_MUCH`, `FILTER_INCLUDE_TOO_MUCH`), too many
folders, shared folders or exported links (`DIALOG_FILTERS_TOO_MUCH`,
`CHATLISTS_TOO_MUCH`, `INVITES_TOO_MUCH`) and channel membership limits
(`CHANNELS_TOO_MUCH`). Surface network errors with retry and respect
flood waits. For a timeout after a mutation, refetch before retrying,
especially before creating another link. Keep selections on recoverable
errors. Use local catalog keys mapped to Desktop's `lng_…` strings, e.g.
`lng_settings_section_filters`, `lng_filters_context_edit` and
`lng_filters_context_remove`. [Android error handling](https://github.com/DrKLO/Telegram/blob/3de37c8cfb039452be7398909461da6288babf15/TMessagesProj/src/main/java/org/telegram/ui/FilterCreateActivity.java#L2770-L2800) · [Project localization](LOCALIZATION.md)

## Delivery order and acceptance checks

| Step | Observable result |
| --- | --- |
| 1. Definitions, matching and account sync | Load ordinary, shared and default filters. Rename a folder containing a custom-emoji title, an archived chat, an unloaded peer and ordered pins. Verify the intended edit is the only TL change. Restore the order on an offline restart. Apply an edit, reorder and deletion from another session while a window is open. |
| 2. Settings, editor and navigation menus | From zero folders create one under Chat Settings. Change its rules from the sidebar and horizontal tab, then reorder it. Verify the clicked folder is edited while a different folder is active. Mark an Unread folder read with several off-screen chats and one manually marked chat. Delete the selected folder and land in All chats. |
| 3. Incoming links | Exercise web and tg forms, first import with a subset, Already with some/all peers present, inaccessible peers, expired link, cancel, and two clicks while a request is pending. Inspect the RPC arguments and navigate only after success. |
| 4. Export, updates and removal | Give two links different labels and peer subsets. Revoke one and reopen the other. Accept a subset of new chats, dismiss another offer, then remove the imported folder once keeping all chats and once leaving a selection. Verify both outgoing vectors and resulting membership. |

Exercise writes through the demo and a recording Telegram client in the
store harness. Render the settings list, editor, context menu, invite preview
and leave confirmation in wide and narrow layouts, and inspect the PNGs. The
existing gotd v0.162.0 bindings include the required RPCs. No dependency
change is needed. [UI components and render conventions](UI_COMPONENTS.md)
