// Package card holds a unit's card: what a plugin shows of one unit.
package card

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Tone string // "neutral", "good", "warn", "bad", "info"

type Signal struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Tone  Tone   `json:"tone"`
	// At is the instant Value shows, for a client to show in the viewer's zone.
	At time.Time `json:"at,omitzero"`
}

type Human struct {
	Who  items.Person `json:"who"`
	What string       `json:"what"`
	At   time.Time    `json:"at"`
	Link string       `json:"link,omitempty"`
}

type Card struct {
	Unit          unit.Unit        `json:"unit"`
	Label         string           `json:"label,omitempty"` // the unit as a person reads it, such as "owner/repo#12" or "#channel"
	Title         string           `json:"title"`
	State         string           `json:"state"`             // the plugin's word: "open", "merged", "closed", "draft", "thread"
	Outcome       items.Resolution `json:"outcome,omitempty"` // "" while open; "done" or "moot" once the unit is finished (merged, closed unmerged)
	Signals       []Signal         `json:"signals"`
	LastHuman     *Human           `json:"last_human,omitempty"`
	Why           string           `json:"why"` // why it is here, one line
	WaitsOn       []items.Person   `json:"waits_on,omitempty"`
	Since         time.Time        `json:"since,omitzero"`
	Link          string           `json:"link"`
	TurnsLink     string           `json:"turns_link,omitempty"`
	StartedByMe   bool             `json:"started_by_me"`  // github: the viewer authored the PR; slack: the viewer started the thread
	ClosurePosted bool             `json:"closure_posted"` // github: merged or closed; slack: a closing reply was posted
	Cares         bool             `json:"cares"`          // the user follows it (github: in scope, adopted or tracked)
}

// Snapshot is a stored card; Stale: its plugin is disabled.
type Snapshot struct {
	Card  Card      `json:"card"`
	At    time.Time `json:"at"`
	Stale bool      `json:"stale"`
}

type Store interface {
	Save(ctx context.Context, c Card, at time.Time) (changed bool, err error) // notes TopicCards when changed
	Get(ctx context.Context, units []unit.Unit) (map[unit.Unit]Snapshot, error)
}
