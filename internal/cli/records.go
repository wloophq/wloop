package cli

import (
	"context"
	"fmt"
	"slices"

	intentload "github.com/wloophq/wloop/internal/intent"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// briefRecords projects the brief records onto their tasks' properties, as the loaded records.toml and
// tasks.toml declare them, through the tasks service.
func (e *Env) briefRecords(db *sqlite.DB) *brief.Records {
	return &brief.Records{Holders: db, Props: serviceProps{e}, Projection: e.briefProjection}
}

func (e *Env) briefProjection() brief.Projection {
	l := e.loadedIntents()
	kind, ok := l.Records.Kind(builtin.BuildsBrief)
	if !ok {
		return brief.Projection{}
	}
	tasks := l.Tasks
	return brief.Projection{Projects: kind.Projects, Parse: func(prop, text string, w task.Writer) (task.Value, bool) {
		p, ok := tasks.Prop(prop)
		if !ok || !kind.Writes(w) || !slices.Contains(p.Writers, w) {
			return task.Value{}, false
		}
		v, err := tasks.Parse(prop, text, e.Now().Location())
		return v, err == nil
	}}
}

// namedGates opens a gate of the loaded gates.toml by its name, of the kind and with the intents it
// declares.
type namedGates struct {
	e     *Env
	store gates.Store
}

func (n namedGates) OpenNamed(ctx context.Context, name string, g gate.Gate) (gate.Gate, error) {
	def, ok := n.e.loadedIntents().Gates.Gate(name)
	if !ok {
		return gate.Gate{}, fmt.Errorf("%s declares no gate %q", intentload.GatesFile, name)
	}
	g.Name, g.Kind, g.Intents = name, def.Kind, nil
	for _, in := range def.Intents {
		g.Intents = append(g.Intents, in.Name)
	}
	return n.store.Open(ctx, g)
}
