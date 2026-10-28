package cli

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/task"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/source"
)

// unitsEnded says whether a unit has ended by its plugin's ended fact
// (builtin.EndedFact); a unit whose plugin has no such fact, or whose facts
// are gone, has not.
func unitsEnded(mods []source.Module) func(ctx context.Context, u unit.Unit) (bool, error) {
	return func(ctx context.Context, u unit.Unit) (bool, error) {
		name, ok := builtin.EndedFact(u.Provider)
		if !ok {
			return false, nil
		}
		for _, m := range mods {
			if m.ID != u.Provider || m.Facts == nil {
				continue
			}
			f, err := m.Facts.Facts(ctx, u)
			if errors.Is(err, automation.ErrUnitGone) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			ended, _ := f[name].(bool)
			return ended, nil
		}
		return false, nil
	}
}

// taskResults attaches to an entry what the turns on it produced: a done
// record's url (turns.Results) and their posts (outbox.Results).
type taskResults struct {
	tasks   *coretasks.Service
	units   *unit.Registry
	intents func() *dintent.Registry
}

var (
	_ turns.Results  = taskResults{}
	_ outbox.Results = taskResults{}
)

func (r taskResults) UnitOf(url string) (unit.Unit, bool) { return r.units.ParseURL(url) }

func (r taskResults) Attach(ctx context.Context, tk task.ID, u unit.Unit) error {
	return r.tasks.Attach(ctx, tk, u)
}

func (r taskResults) AttachesPosts(intent string) bool {
	reg := r.intents()
	if reg == nil {
		return false
	}
	d, ok := reg.Get(dintent.Name(intent))
	return ok && d.AttachPosts
}

// newTaskResults attaches through g, resolving addresses with the codecs of
// mods.
func (e *Env) newTaskResults(g *coretasks.Service, mods []source.Module) taskResults {
	codecs := make([]unit.Codec, 0, len(mods))
	for _, m := range mods {
		if m.Codec != nil {
			codecs = append(codecs, m.Codec)
		}
	}
	return taskResults{tasks: g, units: unit.NewRegistry(codecs...), intents: func() *dintent.Registry { return e.loadedIntents().Registry }}
}

// unitsRepo is the repository of a unit: the one the placement rules give it, else the repo fact of its
// plugin's facts; "" when neither names one.
func unitsRepo(mods []source.Module, cfg func() config.Config) func(ctx context.Context, u unit.Unit) (string, error) {
	return func(ctx context.Context, u unit.Unit) (string, error) {
		if p, ok := cfg().PlacementMap().Place(u); ok && p.Repo != "" {
			return p.Repo, nil
		}
		slot, ok := builtin.Slots[u.Provider]
		if !ok {
			return "", nil
		}
		for _, m := range mods {
			if m.ID != u.Provider || m.Facts == nil {
				continue
			}
			f, err := m.Facts.Facts(ctx, u)
			if errors.Is(err, automation.ErrUnitGone) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			repo, _ := f[slot+".repo"].(string)
			return repo, nil
		}
		return "", nil
	}
}
