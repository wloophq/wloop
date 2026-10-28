// Package port holds what every core package's ports share: the event a
// save writes, the clock and the publisher.
package port

import (
	"time"

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

// Activity is an event as a feed shows it, with its unit's title, address,
// author and state; Mine says the viewer started the unit.
type Activity struct {
	Event
	Title, URL, Author, State string
	Mine                      bool
}

// SentText is the text a turn was sent with, kept under its id.
type SentText struct {
	ID, Kind, Repo, Text string
	Sources              []string
	BundledSHA           string
}

type Clock interface{ Now() time.Time }

type Publisher interface{ Publish(evs ...Event) }
