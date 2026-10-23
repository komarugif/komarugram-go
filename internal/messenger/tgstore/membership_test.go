// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"komarugram/internal/messenger/model"
)

func membershipStore(t *testing.T, basic, creator, left bool) (*Store, int64, *tg.Channel, *tg.Chat) {
	t.Helper()
	s := testStore(t)
	ch := &tg.Channel{ID: 8, Title: "News", Creator: creator, Left: left, Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}
	group := &tg.Chat{ID: 8, Title: "Group", Creator: creator, Left: left, Photo: &tg.ChatPhotoEmpty{}}
	id := peerID(&tg.PeerChannel{ChannelID: 8})
	if basic {
		id = peerID(&tg.PeerChat{ChatID: 8})
		s.rememberPeers(nil, []tg.ChatClass{group})
	} else {
		s.rememberPeers(nil, []tg.ChatClass{ch})
	}
	s.chats = []model.Chat{{ID: id, Title: "Test"}}
	return s, id, ch, group
}
func TestJoinMembership(t *testing.T) {
	for _, request := range []bool{false, true} {
		t.Run(map[bool]string{false: "joined", true: "requested"}[request], func(t *testing.T) {
			s, id, ch, _ := membershipStore(t, false, false, true)
			calls := 0
			s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
				r, ok := in.(*tg.ChannelsJoinChannelRequest)
				if !ok {
					t.Fatalf("unexpected RPC %T", in)
				}
				if r.Channel.(*tg.InputChannel).ChannelID != 8 {
					t.Fatal("wrong channel")
				}
				calls++
				if request {
					return nil, &tgerr.Error{Code: 400, Type: "INVITE_REQUEST_SENT"}
				}
				ch.Left = false
				return &tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{Chats: []tg.ChatClass{ch}}}, nil
			})
			err := s.JoinChat(context.Background(), id)
			if request {
				if !errors.Is(err, model.ErrJoinRequested) || !s.Membership(id).Left {
					t.Fatalf("request treated as join: %v", err)
				}
				return
			}
			if err != nil || s.Membership(id).Left {
				t.Fatalf("join: %v %+v", err, s.Membership(id))
			}
			if err = s.JoinChat(context.Background(), id); err != nil || calls != 1 {
				t.Fatal("duplicate join")
			}
		})
	}
}
func TestLeaveMembershipAndOwnerWarning(t *testing.T) {
	for _, basic := range []bool{false, true} {
		for _, owner := range []bool{false, true} {
			name := "channel"
			if basic {
				name = "basic"
			}
			if owner {
				name += "-owner"
			}
			t.Run(name, func(t *testing.T) {
				s, id, ch, group := membershipStore(t, basic, owner, false)
				future, leaves := 0, 0
				s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
					switch in.(type) {
					case *tg.ChannelsGetFullChannelRequest:
						return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ID: 8, ChatPhoto: &tg.PhotoEmpty{}}, Chats: []tg.ChatClass{ch}}, nil
					case *tg.MessagesGetFullChatRequest:
						return &tg.MessagesChatFull{FullChat: &tg.ChatFull{ID: 8, Participants: &tg.ChatParticipants{ChatID: 8}}, Chats: []tg.ChatClass{group}}, nil
					case *tg.MessagesGetFutureChatCreatorAfterLeaveRequest:
						future++
						if !owner {
							t.Fatal("non-owner preflight")
						}
						return &tg.User{ID: 55, FirstName: "Alice"}, nil
					case *tg.ChannelsLeaveChannelRequest:
						if basic {
							t.Fatal("basic group used channel RPC")
						}
						leaves++
						ch.Left = true
						return &tg.Updates{Chats: []tg.ChatClass{ch}}, nil
					case *tg.MessagesDeleteChatUserRequest:
						r := in.(*tg.MessagesDeleteChatUserRequest)
						if !basic || r.RevokeHistory || r.ChatID != 8 {
							t.Fatal("destructive or wrong group request")
						}
						if _, ok := r.UserID.(*tg.InputUserSelf); !ok {
							t.Fatal("removed someone else")
						}
						leaves++
						group.Left = true
						return &tg.Updates{Chats: []tg.ChatClass{group}}, nil
					default:
						t.Fatalf("unexpected RPC %T", in)
						return nil, nil
					}
				})
				plan, err := s.PrepareLeave(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				if plan.Creator != owner || plan.BasicGroup != basic || owner && (plan.SuccessorID != 55 || plan.SuccessorName != "Alice") {
					t.Fatalf("plan: %+v", plan)
				}
				if leaves != 0 {
					t.Fatal("left without confirmation")
				}
				if err = s.LeaveChat(context.Background(), id, plan); err != nil {
					t.Fatal(err)
				}
				if leaves != 1 || !s.Membership(id).Left || len(s.Chats()) != 0 {
					t.Fatalf("leave state: %d %+v %+v", leaves, s.Membership(id), s.Chats())
				}
				if owner && future != 2 {
					t.Fatal("successor not rechecked")
				}
			})
		}
	}
}
func TestLeaveOwnershipChanged(t *testing.T) {
	s, id, ch, _ := membershipStore(t, false, true, false)
	successor := int64(55)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.ChannelsGetFullChannelRequest:
			return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ID: 8, ChatPhoto: &tg.PhotoEmpty{}}, Chats: []tg.ChatClass{ch}}, nil
		case *tg.MessagesGetFutureChatCreatorAfterLeaveRequest:
			return &tg.User{ID: successor, FirstName: "Owner"}, nil
		default:
			t.Fatalf("left after successor changed: %T", in)
			return nil, nil
		}
	})
	plan, err := s.PrepareLeave(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	successor++
	if err = s.LeaveChat(context.Background(), id, plan); !errors.Is(err, model.ErrLeavePlanChanged) {
		t.Fatalf("changed owner: %v", err)
	}
	if s.Membership(id).Left {
		t.Fatal("changed membership before successful RPC")
	}
}
func TestLeavePreflightFailure(t *testing.T) {
	for _, rpc := range []bool{false, true} {
		t.Run(map[bool]string{false: "transport", true: "rpc"}[rpc], func(t *testing.T) {
			s, id, ch, _ := membershipStore(t, false, true, false)
			s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
				switch in.(type) {
				case *tg.ChannelsGetFullChannelRequest:
					return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ID: 8, ChatPhoto: &tg.PhotoEmpty{}}, Chats: []tg.ChatClass{ch}}, nil
				case *tg.MessagesGetFutureChatCreatorAfterLeaveRequest:
					if rpc {
						return nil, &tgerr.Error{Code: 400, Type: "CHANNEL_ID_INVALID"}
					}
					return nil, errors.New("connection lost")
				default:
					t.Fatalf("unexpected RPC %T", in)
					return nil, nil
				}
			})
			plan, err := s.PrepareLeave(context.Background(), id)
			if rpc {
				if err != nil || plan.SuccessorID != 0 || !plan.Creator {
					t.Fatalf("RPC fallback: %+v %v", plan, err)
				}
			} else if err == nil {
				t.Fatal("transport failure hid ownership warning")
			}
		})
	}
}

func TestLeaveFailureKeepsMembership(t *testing.T) {
	s, id, ch, _ := membershipStore(t, false, true, false)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		switch in.(type) {
		case *tg.ChannelsGetFullChannelRequest:
			return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ID: 8, ChatPhoto: &tg.PhotoEmpty{}}, Chats: []tg.ChatClass{ch}}, nil
		case *tg.MessagesGetFutureChatCreatorAfterLeaveRequest:
			return nil, &tgerr.Error{Code: 400, Type: "CHANNEL_ID_INVALID"}
		case *tg.ChannelsLeaveChannelRequest:
			return nil, &tgerr.Error{Code: 400, Type: "USER_CREATOR"}
		default:
			t.Fatalf("unexpected RPC %T", in)
			return nil, nil
		}
	})
	plan, err := s.PrepareLeave(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.LeaveChat(context.Background(), id, plan); !errors.Is(err, model.ErrOwnerCannotLeave) {
		t.Fatalf("owner rejection: %v", err)
	}
	if s.Membership(id).Left || len(s.Chats()) != 1 {
		t.Fatal("failed leave removed membership")
	}
}

func TestJoinVerificationDoesNotGrantMembership(t *testing.T) {
	s, id, _, _ := membershipStore(t, false, false, true)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if _, ok := in.(*tg.ChannelsJoinChannelRequest); !ok {
			t.Fatalf("unexpected RPC %T", in)
		}
		return &tg.MessagesChatInviteJoinResultWebView{BotID: 55, QueryID: 1}, nil
	})
	err := s.JoinChat(context.Background(), id)
	if !errors.Is(err, model.ErrJoinVerification) || !s.Membership(id).Left {
		t.Fatalf("verification treated as membership: %v", err)
	}
}

func TestJoinPreservesPeerMetadata(t *testing.T) {
	for _, scenario := range []string{"full", "min", "message", "empty", "count", "forum"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			channel := &tg.Channel{ID: 88, Title: "Verified channel", Left: true, Broadcast: true, Verified: true, Photo: &tg.ChatPhotoEmpty{}}
			if scenario == "forum" {
				channel.Broadcast = false
				channel.Megagroup = true
				channel.Forum = true
			}
			channel.SetParticipantsCount(1234)
			id := peerID(&tg.PeerChannel{ChannelID: channel.ID})
			// The channel was opened from search, not from an existing dialog.
			before := s.chatFor(id, s.peerList(nil, []tg.ChatClass{channel}))
			if len(s.Chats()) != 0 {
				t.Fatal("fixture already has a dialog")
			}
			next := *channel
			next.Left = false
			next.ParticipantsCount = 0
			next.Flags.Unset(17)
			if scenario == "min" {
				next.Min = true
				next.Verified = false
			}
			wantCount := 1234
			if scenario == "count" {
				next.SetParticipantsCount(1235)
				wantCount = 1235
			}
			s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
				if _, ok := in.(*tg.ChannelsJoinChannelRequest); !ok {
					t.Fatalf("unexpected RPC %T", in)
				}
				updates := &tg.Updates{Chats: []tg.ChatClass{&next}}
				if scenario == "empty" {
					updates.Chats = nil
				}
				if scenario == "message" {
					updates.Updates = []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 88}, Date: 100, Message: "Latest post"}}}
				}
				return &tg.MessagesChatInviteJoinResultOk{Updates: updates}, nil
			})
			if err := s.JoinChat(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			after := s.chatFor(id, nil)
			if after.Members != wantCount || after.Badges != before.Badges || after.Kind != before.Kind || after.Forum != before.Forum || after.Title != before.Title {
				t.Fatalf("metadata lost after join:\nbefore %+v\nafter  %+v", before, after)
			}
			if scenario == "message" && after.Last.Text != "Latest post" {
				t.Fatal("join dropped received message preview")
			}
			var dialogs []model.Chat
			if _, err := s.Cache().Get(context.Background(), "dialogs", &dialogs); err != nil {
				t.Fatal(err)
			}
			if len(dialogs) != 1 || dialogs[0].Members != wantCount || dialogs[0].Badges != before.Badges {
				t.Fatalf("incomplete dialog persisted: %+v", dialogs)
			}
			var peers map[int64]peerRecord
			if _, err := s.Cache().Get(context.Background(), "peers", &peers); err != nil {
				t.Fatal(err)
			}
			restored := New(nil)
			restored.history.peers = peers
			cached := restored.chatFor(id, nil)
			if cached.Members != wantCount || cached.Badges != before.Badges || cached.Kind != before.Kind || cached.Forum != before.Forum {
				t.Fatalf("peer cache lost metadata: %+v", cached)
			}

			s.setMembership(context.Background(), id, true)
			if len(s.Chats()) != 0 {
				t.Fatal("left dialog remains in list")
			}
			if err := s.JoinChat(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			again := s.chatFor(id, nil)
			if again.Members != wantCount || again.Badges != before.Badges || again.Kind != before.Kind || again.Forum != before.Forum {
				t.Fatalf("metadata lost after rejoin: %+v", again)
			}
		})
	}
}

func TestMembershipRefreshPreservesDialogState(t *testing.T) {
	s, id, ch, _ := membershipStore(t, false, false, true)
	ch.SetParticipantsCount(15)
	ch.Verified = true
	s.rememberPeers(nil, []tg.ChatClass{ch})
	original := model.Chat{ID: id, Kind: model.KindChannel, Title: ch.Title, Members: 14, Unread: 3, Muted: true, Pinned: true, PinRank: 2, Last: model.Preview{Text: "Keep preview"}}
	s.chats = []model.Chat{original}
	s.setMembership(context.Background(), id, false)
	updated := s.Chats()[0]
	original.Members = 15
	original.Verified = true
	original.Channel = ch.ID
	if updated != original {
		t.Fatalf("dialog state or metadata lost:\nwant %+v\ngot  %+v", original, updated)
	}
}

func TestJoinKeepsLoadedPreview(t *testing.T) {
	s, id, _, _ := membershipStore(t, false, false, true)
	s.chats = nil
	last := model.Message{Key: model.MessageKey{ChatID: id, MessageID: 9}, Text: "Already loaded post", Date: time.Unix(1000, 0)}
	s.history.histories[id] = &model.History{Messages: []model.Message{last}}
	s.setMembership(context.Background(), id, false)
	if c := s.Chats()[0]; c.Last.Text != last.Text || !c.LastTime.Equal(last.Date) {
		t.Fatalf("loaded preview discarded: %+v", c)
	}
}

func TestFullPeerRefreshUpdatesMembershipMetadata(t *testing.T) {
	s, id, ch, _ := membershipStore(t, false, false, false)
	ch.Verified = true
	ch.SetParticipantsCount(100)
	s.rememberPeers(nil, []tg.ChatClass{ch})
	s.setMembership(context.Background(), id, false)
	next := *ch
	next.Verified = false
	next.ParticipantsCount = 0
	next.Flags.Unset(17)
	full := &tg.ChannelFull{ID: 8, ChatPhoto: &tg.PhotoEmpty{}}
	full.SetParticipantsCount(101)
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		if _, ok := in.(*tg.ChannelsGetFullChannelRequest); !ok {
			t.Fatalf("unexpected RPC %T", in)
		}
		return &tg.MessagesChatFull{FullChat: full, Chats: []tg.ChatClass{&next}}, nil
	})
	if err := s.RefreshSendPermissions(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := s.Chats()[0]; got.Members != 101 || got.Verified {
		t.Fatalf("full peer changes not reflected: %+v", got)
	}
	full.SetParticipantsCount(0)
	if err := s.RefreshSendPermissions(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if s.Chats()[0].Members != 0 {
		t.Fatal("explicit zero treated as unknown")
	}
}

func TestJoinRequestSupergroup(t *testing.T) {
	for _, creator := range []bool{false, true} {
		s, id, ch, _ := membershipStore(t, false, creator, true)
		ch.Broadcast, ch.JoinRequest = false, true
		s.rememberPeers(nil, []tg.ChatClass{ch})
		m := s.Membership(id)
		if !m.Channel || m.Broadcast || m.Request == creator {
			t.Fatalf("creator %v: %+v", creator, m)
		}
	}
}
func TestJoinErrors(t *testing.T) {
	for typ, want := range map[string]error{"USERS_TOO_MUCH": model.ErrJoinFull, "USER_BANNED_IN_CHANNEL": model.ErrChatInaccessible, "INVITE_REQUEST_SENT": model.ErrJoinRequested} {
		s, id, _, _ := membershipStore(t, false, false, true)
		s.history.api = composerAPI(func(bin.Encoder) (bin.Encoder, error) { return nil, &tgerr.Error{Code: 400, Type: typ} })
		if err := s.JoinChat(context.Background(), id); !errors.Is(err, want) {
			t.Fatalf("%s: %v", typ, err)
		}
	}
}
