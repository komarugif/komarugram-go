// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/go-text/typesetting/segmenter"
)

// Folder is an immutable snapshot. Peer IDs use the same representation as Chat.ID.
// ID zero represents All chats. A nil Color and a pointer to zero are distinct.
type Folder struct {
	ID                                   int64
	Title, Emoticon                      string
	TitleEntities                        []Entity
	TitleNoAnimate, Shared, HasMyInvites bool
	Color                                *int
	FolderTypes
	ExcludeMuted, ExcludeRead, ExcludeArchived bool
	Pinned, Included, Excluded                 []int64
	dialogFlags                                func(int64) (contact, marked bool)
	exceptions                                 map[int64]bool
}

// WithDialogFlags prepares fast membership checks for an immutable account snapshot.
func (f Folder) WithDialogFlags(flags func(int64) (contact, marked bool)) Folder {
	f.dialogFlags = flags
	f.exceptions = nil
	if n := len(f.Pinned) + len(f.Included) + len(f.Excluded); n > 0 {
		f.exceptions = make(map[int64]bool, n)
		for _, id := range f.Included {
			f.exceptions[id] = true
		}
		for _, id := range f.Pinned {
			f.exceptions[id] = true
		}
		for _, id := range f.Excluded {
			f.exceptions[id] = false
		}
	}
	return f
}

func (f Folder) matchesRules(c Chat) bool {
	if f.Shared || f.ExcludeArchived && c.Archived {
		return false
	}
	if f.ExcludeMuted && c.Muted && (c.UnreadMentions == 0 || c.Archived) {
		return false
	}
	contact, marked := c.Kind == KindSaved, false
	if f.dialogFlags != nil {
		contact, marked = f.dialogFlags(c.ID)
	}
	if f.ExcludeRead && c.Unread == 0 && c.UnreadMentions == 0 && !marked {
		return false
	}
	return f.FolderTypes.Includes(c.Kind, contact)
}

func (f Folder) Contains(c Chat) bool {
	if f.ID == 0 {
		return !c.Archived
	}
	if f.exceptions != nil {
		if in, ok := f.exceptions[c.ID]; ok {
			return in
		}
	} else {
		if slices.Contains(f.Excluded, c.ID) {
			return false
		}
		if slices.Contains(f.Pinned, c.ID) || slices.Contains(f.Included, c.ID) {
			return true
		}
	}
	return f.matchesRules(c)
}

type FolderChatAction uint8

const (
	FolderAddChat FolderChatAction = iota + 1
	FolderRemoveChat
	FolderPinChat
	FolderUnpinChat
)

// CanRemoveChat follows the editor's requirement that a folder include a type
// or at least one explicit chat. Pins count as explicit members.
func (f Folder) CanRemoveChat(c Chat) bool {
	if f.ID == 0 || !f.Contains(c) {
		return false
	}
	if f.FolderTypes != (FolderTypes{}) {
		return true
	}
	for _, ids := range [][]int64{f.Pinned, f.Included} {
		for _, id := range ids {
			if id != c.ID {
				return true
			}
		}
	}
	return false
}

// WithChat returns a draft for the normal filter save. Pinning moves the peer
// between Telegram's two explicit membership lists, which are disjoint.
func (f Folder) WithChat(c Chat, action FolderChatAction) (Folder, error) {
	if f.ID == 0 || c.ID == 0 || c.Kind == KindCommunity {
		return f, ErrFolderConflict
	}
	remove := func(ids []int64) []int64 {
		if !slices.Contains(ids, c.ID) {
			return ids
		}
		return slices.DeleteFunc(slices.Clone(ids), func(id int64) bool { return id == c.ID })
	}
	switch action {
	case FolderAddChat:
		if f.Contains(c) {
			return f, nil
		}
		f.Excluded = remove(f.Excluded)
		if !f.matchesRules(c) && !slices.Contains(f.Included, c.ID) && !slices.Contains(f.Pinned, c.ID) {
			f.Included = append(slices.Clone(f.Included), c.ID)
		}
	case FolderRemoveChat:
		if !f.Contains(c) {
			return f, nil
		}
		if !f.CanRemoveChat(c) {
			return f, ErrFolderTypes
		}
		f.Included = remove(f.Included)
		f.Pinned = remove(f.Pinned)
		if f.matchesRules(c) {
			f.Excluded = append(slices.Clone(f.Excluded), c.ID)
		}
	case FolderPinChat:
		if !f.Contains(c) {
			return f, ErrFolderConflict
		}
		if slices.Contains(f.Pinned, c.ID) {
			return f, nil
		}
		f.Included = remove(f.Included)
		f.Pinned = append([]int64{c.ID}, f.Pinned...)
	case FolderUnpinChat:
		if !slices.Contains(f.Pinned, c.ID) {
			return f, nil
		}
		f.Pinned = remove(f.Pinned)
		if !slices.Contains(f.Included, c.ID) {
			f.Included = append(slices.Clone(f.Included), c.ID)
		}
	default:
		return f, ErrFolderConflict
	}
	return f.WithDialogFlags(f.dialogFlags), nil
}

// MainDialogs keeps the main list's order, including local pins.
func MainDialogs(chats []Chat) []Chat {
	// Dialog pages commonly keep the two lists in contiguous runs.
	for len(chats) > 0 && chats[0].Archived {
		chats = chats[1:]
	}
	for len(chats) > 0 && chats[len(chats)-1].Archived {
		chats = chats[:len(chats)-1]
	}
	first := slices.IndexFunc(chats, func(c Chat) bool { return c.Archived })
	if first < 0 {
		return chats
	}
	out := make([]Chat, first, len(chats))
	copy(out, chats[:first])
	for _, c := range chats[first+1:] {
		if !c.Archived {
			out = append(out, c)
		}
	}
	return out
}

// Chats selects members and applies this folder's own pins to a fresh view.
func (f Folder) Chats(chats []Chat) []Chat {
	if f.ID == 0 {
		return MainDialogs(chats)
	}
	var members []Chat
	for _, c := range chats {
		if f.Contains(c) {
			members = append(members, c)
		}
	}
	return setPinOrder(members, f.Pinned)
}

// FolderTypes are the five independent inclusion rules of an ordinary filter.
type FolderTypes struct {
	Contacts, NonContacts, Groups, Channels, Bots bool
}

func (t FolderTypes) Includes(kind ChatKind, contact bool) bool {
	switch kind {
	case KindSaved, KindUser:
		return (contact && t.Contacts) || (!contact && t.NonContacts)
	case KindGroup:
		return t.Groups
	case KindChannel:
		return t.Channels
	case KindBot:
		return t.Bots
	default:
		return false
	}
}

type FolderManager interface {
	Folders() []Folder
	SaveFolder(ctx context.Context, previous *Folder, draft Folder) error
	SuggestedFolders(ctx context.Context) ([]Folder, error)
	ReorderFolders(ctx context.Context, previous, next []int64) error
}

// FolderReadChat freezes the unread state of a member before a bulk read
// changes membership (notably in folders which exclude read chats).
type FolderReadChat struct {
	ID               int64
	Top              MessageID
	Unread, Mentions int
	Marked, Forum    bool
}
type FolderActions interface {
	UnreadInFolder(id int64) []FolderReadChat
	ReadFolderChats(ctx context.Context, chats []FolderReadChat) error
	DeleteFolder(ctx context.Context, folder Folder) error
}

var (
	ErrFolderTypes     = errors.New("select at least one chat type")
	ErrFolderConflict  = errors.New("folder ID already in use")
	ErrFolderCache     = errors.New("folder saved on Telegram but not cached")
	ErrFolderTitle     = errors.New("folder name must be 1 to 12 characters")
	ErrFolderLimit     = errors.New("folder limit reached")
	ErrFolderDefault   = errors.New("folder duplicates All chats")
	ErrFolderPeerLimit = errors.New("too many chats in this folder")
	ErrFolderOrder     = errors.New("folder order has changed")
)

func ValidFolderTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title != "" && FitFolderTitle(title) == title
}

// FitFolderTitle limits a translated suggestion to twelve whole graphemes.
func FitFolderTitle(title string) string {
	runes := []rune(title)
	var seg segmenter.Segmenter
	seg.Init(runes)
	it := seg.GraphemeIterator()
	for n := 0; it.Next(); n++ {
		if n == 12 {
			return string(runes[:it.Grapheme().Offset])
		}
	}
	return title
}

func FolderIDs(folders []Folder) []int64 {
	ids := make([]int64, len(folders))
	for i, f := range folders {
		ids[i] = f.ID
	}
	return ids
}

// NewFolderID chooses an ID for a creation form. Keeping it across retries
// lets the store recognize a creation whose acknowledgement was lost.
func NewFolderID(folders []Folder) int64 {
	ids := FolderIDs(folders)
	id := int64(2)
	for slices.Contains(ids, id) {
		id++
	}
	return id
}

// SameRules compares fields owned by the editor. Telegram controls the other metadata.
func (f Folder) SameRules(g Folder) bool {
	return f.ID == g.ID && f.Title == g.Title && f.Emoticon == g.Emoticon && f.Shared == g.Shared &&
		f.FolderTypes == g.FolderTypes && f.ExcludeRead == g.ExcludeRead && f.ExcludeMuted == g.ExcludeMuted && f.ExcludeArchived == g.ExcludeArchived &&
		slices.Equal(f.Included, g.Included) && slices.Equal(f.Excluded, g.Excluded) && slices.Equal(f.Pinned, g.Pinned)
}

// CheckFolderSave recognizes an acknowledged retry and checks the draft against the
// current account snapshot. A nil previous folder denotes a creation.
func CheckFolderSave(folders []Folder, previous *Folder, f Folder, premium Premium) (done bool, err error) {
	if f.ID <= 0 || previous == nil && (f.ID < 2 || f.Shared) || previous != nil && (previous.ID != f.ID || previous.Shared != f.Shared) {
		return false, ErrFolderConflict
	}
	if !ValidFolderTitle(f.Title) {
		return false, ErrFolderTitle
	}
	if f.FolderTypes == (FolderTypes{}) && len(f.Included)+len(f.Pinned) == 0 {
		return false, ErrFolderTypes
	}
	if f.Shared && (f.FolderTypes != (FolderTypes{}) || len(f.Excluded) > 0 || f.ExcludeMuted || f.ExcludeRead || f.ExcludeArchived) {
		return false, ErrFolderTypes
	}
	if f.Contacts && f.NonContacts && f.Groups && f.Channels && f.Bots && f.ExcludeArchived && !f.ExcludeRead && !f.ExcludeMuted && len(f.Included)+len(f.Pinned)+len(f.Excluded) == 0 {
		return false, ErrFolderDefault
	}
	index := slices.IndexFunc(folders, func(cur Folder) bool { return cur.ID == f.ID })
	if index >= 0 {
		cur := folders[index]
		if cur.SameRules(f) {
			return true, nil
		}
		if previous == nil || !cur.SameRules(*previous) {
			return false, ErrFolderConflict
		}
	} else if previous != nil {
		return false, ErrFolderConflict
	}
	if limit := premium.Limit("dialog_filters_limit"); previous == nil && limit > 0 && len(folders)-1 >= limit {
		return false, ErrFolderLimit
	}
	included := map[int64]bool{}
	for _, id := range f.Pinned {
		included[id] = true
	}
	for _, id := range f.Included {
		included[id] = true
	}
	for _, id := range f.Excluded {
		if included[id] {
			return false, ErrFolderTypes
		}
	}
	if limit := premium.Limit("dialog_filters_chats_limit"); limit > 0 && (len(included) > limit || len(f.Excluded) > limit) {
		return false, ErrFolderPeerLimit
	}
	return false, nil
}

// ReorderedFolders validates the entire order against the one the window displayed.
func ReorderedFolders(folders []Folder, previous, next []int64, premium bool) ([]Folder, error) {
	if len(next) == 0 || len(next) != len(folders) || !slices.Equal(previous, FolderIDs(folders)) || (!premium && next[0] != 0) {
		return nil, ErrFolderOrder
	}
	byID := make(map[int64]Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	out := make([]Folder, 0, len(next))
	for _, id := range next {
		f, ok := byID[id]
		if !ok {
			return nil, ErrFolderOrder
		}
		out = append(out, f)
		delete(byID, id)
	}
	return out, nil
}
