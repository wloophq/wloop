// Package port holds what every core package's ports share: the event a
// save writes, the clock and the publisher.
package port

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Event is a fact a repository writes in the transaction of the change it
// tells. A keyed event is stored once per unit, kind and key; an
// unkeyed one moves forward a nanosecond until its time is free.
type Event struct {
	Unit      unit.Unit
	Kind, Key string
	Actor     string
	Data      string
	At        time.Time
}

// PROf is the pull request of an event on a github unit.
func PROf(ev Event) (pr.Ref, bool) { return pr.RefOf(ev.Unit) }

type Clock interface{ Now() time.Time }

type Publisher interface{ Publish(evs ...Event) }
