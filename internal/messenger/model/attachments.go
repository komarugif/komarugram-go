package model

import "context"

// WebPreview is supplied by Telegram. Clients never fetch its URL to build it.
type WebPreview struct {
	URL, DisplayURL, Site, Title, Description string
	Photo                                     *MessageMedia
	// InstantView is set for a page Telegram has an Instant View of
	// (webPage.cached_page), which InstantViewStore loads.
	InstantView bool `json:",omitempty"`
	// Video is the page's video, as Telegram keeps it (webPage.document),
	// as of a page of YouTube: a document of the message, which plays as a
	// video message's does; VideoKind is MessageVideo or MessageGIF.
	Video     *MessageMedia `json:",omitempty"`
	VideoKind MessageKind   `json:",omitempty"`
}

// InstantViewStore loads the Instant View of the page at a URL, as an
// article (webPage.cached_page, through messages.getWebPage), and keeps it
// for when it is offline.
type InstantViewStore interface {
	InstantView(ctx context.Context, url string) (RichPage, error)
}

// Gift is presentation metadata for a saved gift, not a chat-history message.
type Gift struct {
	ID, Title, Slug                                 string
	Number                                          int
	Unique, SenderHidden                            bool
	SenderID                                        int64
	SenderName                                      string
	Stars                                           int64
	Issued, Total                                   int
	Model, Symbol, Backdrop                         string
	ModelRarity, SymbolRarity, BackdropRarity       int
	CenterColor, EdgeColor, PatternColor, TextColor uint32
	HasBackdrop                                     bool
	Pattern                                         *MessageMedia
}
