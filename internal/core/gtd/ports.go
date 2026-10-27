// Package gtd is the GTD service: entries derived from items, captured by
// the user or opened by other services, and the verbs that move them.
package gtd

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Reader is the read half of Store.
type Reader interface {
	Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error)
	// EntriesSince is the page of q after position since: Full when since is from another epoch or too old;
	// else the entries of q changed after Rev and the ids that left q. Counts in the same read transaction.
	EntriesSince(ctx context.Context, q gtd.Query, since Since) (Page, error)
	Entry(ctx context.Context, id gtd.ID) (gtd.Entry, error) // ErrNoEntry; ErrVerb is a verb the entry's state refuses, naming both
	EntriesOf(ctx context.Context, u unit.Unit) ([]gtd.Entry, error)
	Counts(ctx context.Context, area string) (gtd.Counters, error) // stored views only
}

type Store interface {
	Reader
	// Apply writes cs and evs in one transaction, bumps Rev and notes port.TopicEntries; with no effective
	// change it writes evs alone and bumps nothing.
	Apply(ctx context.Context, cs []gtd.Change, evs ...port.Event) error
}

type Since struct {
	Epoch string
	Rev   uint64
}

type Page struct {
	Epoch    string
	Rev      uint64
	Full     bool
	Entries  []gtd.Entry
	Removed  []gtd.ID
	Counters gtd.Counters
}

// Items is the items ledger's reader.
type Items interface {
	Items(ctx context.Context, u unit.Unit) ([]items.Item, error)
}

// Facts reads a unit's facts; ok false: plugin disabled.
type Facts interface {
	UnitFacts(ctx context.Context, u unit.Unit) (gtd.UnitFacts, bool, error)
}

// ViewSource serves a view whose entries live elsewhere (outbox with prefix "draft:", review).
// Its ids carry its prefix; Count is what the view's counter shows.
type ViewSource interface {
	Entries(ctx context.Context, q gtd.Query) ([]gtd.Entry, error)
	Entry(ctx context.Context, id gtd.ID) (gtd.Entry, bool, error)
	Count(ctx context.Context, area string) (int, error)
}

// VerbHandler runs a verb another service owns (answer/approve/dismiss on messages, approve/dismiss on drafts).
type VerbHandler func(ctx context.Context, e gtd.Entry, req Request) (Result, error)

type Request struct {
	Verb   gtd.Verb
	Args   map[string]string
	Source string
}

type Result struct {
	Entry gtd.Entry
	Lines []string
}

type OpenRequest struct {
	Kind     gtd.Kind
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
