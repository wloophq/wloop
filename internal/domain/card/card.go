// Package card holds a unit's card: what a plugin shows of one unit.
package card

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workday"
)

type Tone string // "neutral", "good", "warn", "bad", "info"

type Signal struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Tone  Tone   `json:"tone"`
	// At is the instant Value shows, for a client to show in the viewer's zone.
	At time.Time `json:"at,omitzero"`
	// Key and Value are codes the plugin's catalog words (api.SignalSpec), Params the values its words fill.
	Params map[string]string `json:"params,omitempty"`
	Notes  []Note            `json:"notes,omitempty"`
}

// Note is what a signal's line adds after its value, in its own tone: a code
// its signal's notes word, filled with Params.
type Note struct {
	Code   string            `json:"code"`
	Tone   Tone              `json:"tone"`
	Params map[string]string `json:"params,omitempty"`
	// Since is the instant the note counts from; a read of the card fills
	// Params["days"] with the working days since it (CountedAt).
	Since time.Time `json:"since,omitzero"`
}

// DaysParam is the param a note that counts from an instant reads its working days in.
const DaysParam = "days"

// CountedAt is c with each note that counts from an instant reading the whole
// working days from it to now, at least one, as the read sees them; c's own
// signals are left as they are.
func (c Card) CountedAt(now time.Time) Card {
	counted := false
	for _, s := range c.Signals {
		counted = counted || slices.ContainsFunc(s.Notes, func(n Note) bool { return !n.Since.IsZero() })
	}
	if !counted {
		return c
	}
	signals := slices.Clone(c.Signals)
	for i, s := range signals {
		notes := slices.Clone(s.Notes)
		for j, n := range notes {
			if n.Since.IsZero() {
				continue
			}
			n.Params = maps.Clone(n.Params)
			if n.Params == nil {
				n.Params = map[string]string{}
			}
			n.Params[DaysParam] = strconv.Itoa(max(1, int(workday.Between(n.Since, now)/(24*time.Hour))))
			notes[j] = n
		}
		signals[i].Notes = notes
	}
	c.Signals = signals
	return c
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
