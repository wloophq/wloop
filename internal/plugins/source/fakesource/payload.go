package fakesource

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

// Payload is a fake thread as a turn reads it.
type Payload struct{ Key string }

func (p Payload) Unit() unit.Unit { return unit.Unit{Provider: ID, Key: p.Key} }

func (p Payload) Arg() string { return string(ID) + ":" + p.Key }

func (p Payload) Wording() turn.Wording {
	return turn.Wording{Noun: "thread", This: "this thread", Ref: "thread " + p.Key, Short: p.Key}
}

func (p Payload) Fact(name string) (any, bool) {
	if name == "thread.key" {
		return p.Key, true
	}
	return nil, false
}

// Payloads reads a fake thread's turn payload from its unit alone.
type Payloads struct{}

func (Payloads) Payload(_ context.Context, r turn.Request) (turn.Payload, error) {
	return Payload{Key: r.Unit.Key}, nil
}
