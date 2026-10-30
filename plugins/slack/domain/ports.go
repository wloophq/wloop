package slack

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

var ErrUnknownUnit = errors.New("slack: unknown unit")

type FactsReader interface {
	// Messages of u with ts > after, ascending, at most limit (0: all). A segment unit
	// is sliced by Segments.Range, replies in the DM included by their parent's ts.
	Messages(ctx context.Context, u unit.Unit, after string, limit int) ([]Message, error)
	Conversation(ctx context.Context, id string) (Conversation, error)
	Users(ctx context.Context, ids []string) (map[string]User, error) // cached only, never calls Slack
	UserByEmail(ctx context.Context, email string) (User, bool, error)
	Changes(ctx context.Context, afterSeq int64, limit int) ([]Change, error) // ascending Seq
	Viewer(ctx context.Context) (team, user string, err error)
}

// ObjectRef names a watched object; TS "" names a DM conversation.
type ObjectRef struct{ Conversation, TS string }

type WatchReason string // "admission", "extraction", "user-follow", "turn"

const (
	ReasonAdmission  WatchReason = "admission"
	ReasonExtraction WatchReason = "extraction"
	ReasonUserFollow WatchReason = "user-follow"
	ReasonTurn       WatchReason = "turn"
)

type Watcher interface {
	Watch(ctx context.Context, obj ObjectRef, reason WatchReason) error // idempotent; keeps the first reason, adds later ones
	Unwatch(ctx context.Context, obj ObjectRef) error                   // stops polling; stored messages stay
}

// Scheduler gives the next poll time of watched objects. The default is now+5 min;
// the module replaces it with a heat scheduler.
type Scheduler interface {
	Next(ctx context.Context, objects []string, now time.Time) (map[string]time.Time, error)
}
