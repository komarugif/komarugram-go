// SPDX-License-Identifier: Unlicense OR MIT

package model

import "context"

// StorageChannelTitle is the name of the channel Local Premium keeps its lists
// in. A channel's description comes only with its full info, so that this name
// is what finds the channel on another device. It is not translated: devices
// in different languages look for the same channel.
const StorageChannelTitle = "KomaruGram Storage"

// StorageChannel is a channel the user could keep the lists in.
type StorageChannel struct {
	ID    int64
	Title string
}

// StorageFound tells where the account stands with the channel.
type StorageFound struct {
	// Bound: the lists are kept in a channel, named Title.
	Bound bool
	Title string
	// SendAll is how many saved GIFs could be sent to a channel that has none
	// of them; 0 when there is nothing to offer.
	SendAll int
	// Candidates are the user's own private channels, when none is bound.
	Candidates []StorageChannel
}

// StoragePass tells how far sending the whole list of GIFs has come.
type StoragePass struct {
	Active      bool
	Done, Total int
	// Failed is set once when the pass could not go on: the channel is gone.
	Failed bool
}

// StorageChannels is a store that keeps Local Premium's lists in a private
// channel, shared by the account's devices.
type StorageChannels interface {
	// FindStorageChannel binds the channel of the name StorageChannelTitle if
	// there is one, and tells what it found. It asks Telegram.
	FindStorageChannel(ctx context.Context) (StorageFound, error)
	// BindStorageChannel binds the channel id, or a new one with id 0.
	BindStorageChannel(ctx context.Context, id int64) (StorageFound, error)
	// SendAllGIFs starts sending the saved GIFs to the channel, last to first,
	// which leaves them in the order they had. It cannot be cancelled, and goes
	// on after a restart.
	SendAllGIFs(ctx context.Context) error
	// StoragePassState is asked in frames.
	StoragePassState() StoragePass
}
