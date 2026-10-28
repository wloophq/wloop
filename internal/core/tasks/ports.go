// Package tasks is the GTD service: entries derived from items, captured by
// the user or opened by other services, and the verbs that move them.
package tasks

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Reader is the read half of Store.
type Reader interface {
	Entries(ctx context.Context, q task.Query) ([]task.Entry, error)
	// EntriesSince is the page of q after position since: Full when since is from another epoch or too old;
	// else the entries of q changed after Rev and the ids that left q. Counts in the same read transaction.
	EntriesSince(ctx context.Context, q task.Query, since Since) (Page, error)
	Entry(ctx context.Context, id task.ID) (task.Entry, error) // ErrNoEntry; ErrVerb is a verb the entry's state refuses, naming both
	EntriesOf(ctx context.Context, u unit.Unit) ([]task.Entry, error)
	Counts(ctx context.Context, area string) (task.Counters, error) // stored views only
}

type Store interface {
	Reader
	// Apply writes cs and evs in one transaction, bumps Rev and notes port.TopicEntries; when cs change
	// nothing it writes and notes nothing, and with no cs it writes evs alone.
	Apply(ctx context.Context, cs []task.Change, evs ...port.Event) error
}

type Since struct {
	Epoch string
	Rev   uint64
}

type Page struct {
	Epoch    string
	Rev      uint64
	Full     bool
	Entries  []task.Entry
	Removed  []task.ID
	Counters task.Counters
}

// Items is the items ledger's reader.
type Items interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// Facts reads a unit's facts; ok false: plugin disabled.
type Facts interface {
	UnitFacts(ctx context.Context, u unit.Unit) (task.UnitFacts, bool, error)
}

// ViewSource serves a view whose entries live elsewhere (outbox with prefix "draft:", review).
// Its ids carry its prefix; Count is what the view's counter shows.
type ViewSource interface {
	Entries(ctx context.Context, q task.Query) ([]task.Entry, error)
	Entry(ctx context.Context, id task.ID) (task.Entry, bool, error)
	Count(ctx context.Context, area string) (int, error)
}

// VerbHandler runs a verb another service owns (answer/approve/dismiss on messages, approve/dismiss on drafts);
// one on a stored entry records the verb with Service.RecordVerb once it succeeded.
type VerbHandler func(ctx context.Context, e task.Entry, req Request) (Result, error)

type Request struct {
	Verb   task.Verb
	Args   map[string]string
	Source string
	// Automated is a verb a workflow ran: it records no verb event, which
	// says a person ran it and could start another workflow in turn.
	Automated bool
}

type Result struct {
	Entry task.Entry
	Lines []string
}

type OpenRequest struct {
	Kind     task.Kind
	Title    string
	Unit     *unit.Unit
	Origin   string
	Contexts []string
}

var (
	ErrNoEntry = errors.New("no such entry")
	ErrVerb    = errors.New("the entry's state refuses the verb")
	// ErrArgs is a request the service cannot read: an unknown verb or view, or a missing or bad argument.
	ErrArgs = errors.New("bad entry request")
	// ErrPluginDisabled refuses a change to an entry whose unit's plugin is disabled.
	ErrPluginDisabled = errors.New("the unit's plugin is disabled")
)
