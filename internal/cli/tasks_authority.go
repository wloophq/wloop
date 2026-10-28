package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

func (p profileStates) Handler(verb string) string { return p.e.loadedIntents().Tasks.Handler(verb) }

// Gate is the gate of gates.toml verb's gate names, with its kind and intents.
func (p profileStates) Gate(verb string) (gate.Gate, bool) {
	l := p.e.loadedIntents()
	name := l.Tasks.Verbs[verb].Gate
	d, ok := l.Gates.Gate(name)
	if name == "" || !ok {
		return gate.Gate{}, false
	}
	g := gate.Gate{Name: d.Name, Kind: d.Kind}
	for _, in := range d.Intents {
		g.Intents = append(g.Intents, in.Name)
	}
	return g, true
}

// Authority is the profile's by over the verbs, as the catalog evaluates it.
func (p profileStates) Authority(ctx context.Context, verb string, a task.Actor, t task.Task) (bool, bool, error) {
	return catalog.Authority(p.e.loadedIntents)(ctx, verb, a, t)
}
