package slack

import (
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type ConversationKind string // "channel", "private", "im", "mpim"

const (
	KindChannel ConversationKind = "channel"
	KindPrivate ConversationKind = "private"
	KindIM      ConversationKind = "im"
	KindMPIM    ConversationKind = "mpim"
)

// DM says whether the conversation is an im or an mpim.
func (k ConversationKind) DM() bool { return k == KindIM || k == KindMPIM }

type Conversation struct {
	ID, Name           string // Name "" for an im
	Kind               ConversationKind
	IsMember, Archived bool
	DMUser             string // the other party of an im
}

type User struct {
	ID, Name, DisplayName, Email string
	IsBot, Deleted               bool // USLACKBOT is always IsBot
	Fetched                      time.Time
}

type Facts struct {
	Team          string
	Conversations []Conversation
	Threads       []Thread
	DMs           []DMWindow
	Users         []User
}

type Thread struct {
	Conversation, TS, LatestReply string
	ReplyCount                    int
	Messages                      []Message // ascending ts; Messages[0] is the parent when it was read
}

type DMWindow struct {
	Conversation, Latest string
	Messages             []Message
	// Units: Segments.UnitsFor(conv, ThreadTS or TS) per message, so a reply follows its
	// parent's segment; empty until segmented.
	Units []unit.Unit
}

// Units are the thread units, then DM segment units; distinct, sorted by Unit.Less.
func (f Facts) Units() []unit.Unit {
	var out []unit.Unit
	for _, t := range f.Threads {
		out = append(out, ThreadUnit(f.Team, t.Conversation, t.TS))
	}
	for _, w := range f.DMs {
		out = append(out, w.Units...)
	}
	slices.SortFunc(out, compareUnits)
	return slices.Compact(out)
}

func compareUnits(a, b unit.Unit) int {
	switch {
	case a.Less(b):
		return -1
	case b.Less(a):
		return 1
	}
	return 0
}

type ChangeKind string // "new", "edited", "deleted", "reaction"

const (
	ChangeNew      ChangeKind = "new"
	ChangeEdited   ChangeKind = "edited"
	ChangeDeleted  ChangeKind = "deleted"
	ChangeReaction ChangeKind = "reaction"
)

type Change struct {
	Seq                        int64
	Conversation, TS, ThreadTS string
	Kind                       ChangeKind
	At                         time.Time
}
