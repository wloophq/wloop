// Package slack is the Slack plugin's domain: messages, deltas and the
// facts the source stores. The core never imports it.
package slack

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

const Provider unit.ProviderID = "slack"

type Kind string

const (
	KindMessage         Kind = "message"
	KindEdit            Kind = "edit"
	KindDelete          Kind = "delete"
	KindReactionAdded   Kind = "reaction_added"
	KindReactionRemoved Kind = "reaction_removed"
)

// Via is how a message arrived. A message search stored with no thread is
// not trusted to place a post: search may not say the thread.
type Via string

const (
	ViaSocket  Via = "socket"
	ViaCatchUp Via = "catchup"
	ViaProbe   Via = "probe"
	ViaSearch  Via = "search"
	ViaFollow  Via = "follow"
)

type File struct{ ID, Title, Mimetype string }

type Reaction struct {
	Name  string
	Users []string
}

type Message struct {
	TS, ThreadTS, ParentUser     string // ThreadTS "" or == TS for a parent or a lone message
	User, BotID, AppID, Username string // User is the human when set, whatever BotID holds
	Subtype, Text, EditedTS      string
	Deleted                      bool
	Files                        []File
	Links                        []string // URLs in text and attachments
	Mentions                     []string // user ids mentioned
	Reactions                    []Reaction
	ReplyCount                   int
	LatestReply                  string
	Permalink                    string
}

type Delta struct {
	Conversation, ChannelType, TS, ThreadTS string
	EventID                                 string
	Kind                                    Kind
	Message                                 *Message // nil for reactions
	Reaction                                *struct{ Name, User string }
	Via                                     Via
	ReceivedAt                              time.Time
}

// Codec keys "<team>/<conv>/<ts>" (thread) and "<team>/<dm>/seg/<id>" (DM segment).
func Codec() unit.Codec { return codec{} }

var HiddenSubtypes = []string{"message_replied", "channel_join", "channel_leave", "group_join",
	"group_leave", "channel_topic", "channel_purpose", "channel_name", "pinned_item", "unpinned_item"}
