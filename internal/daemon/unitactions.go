package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/port"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// unitAction refuses an action on u that only a pull request takes. Any
// other unit takes a turn action, track and untrack when its source follows
// it, and an intent unitIntent admits. The row of such a unit offers the
// intents serving its provider, but this route does not check their
// visible and enabled predicates; the entries' bridge does.
func unitAction(l *intentload.Loaded, name string, u unit.Unit, follows func(unit.ProviderID) bool) error {
	if isPullRequest(u) {
		return nil
	}
	if _, ok := turnActions[name]; ok {
		return nil
	}
	if sourceFollowed(name, u, follows) {
		return nil
	}
	if isBuiltin(name) {
		return notFound{fmt.Sprintf("no pull request %s: %s acts on pull requests only", u.Label(), name)}
	}
	d, err := intentDef(l, name)
	if err != nil {
		return err
	}
	return unitIntent(d, u)
}

// taskIntentActions are the actions of the intents that name the entry provider
// among theirs, over e's facts.
func taskIntentActions(l *intentload.Loaded, e task.Task) []api.ActionState {
	if l == nil {
		return nil
	}
	u := task.UnitOf(e.ID)
	f := catalog.Facts{Row: api.Row{Unit: u}, Intents: l, Open: e.Live(), CapturedByUser: e.CapturedByUser()}
	var out []api.ActionState
	for _, st := range catalog.UnitStates(f) {
		d, ok := l.Registry.Get(dintent.Name(st.ID))
		if ok && (d.ProviderOrDefault() == u.Provider || slices.Contains(d.Providers, u.Provider)) {
			st.Label = d.Label
			out = append(out, st)
		}
	}
	return out
}

// taskRefusal is why the entry u names refuses the intent name, "" when
// it takes it or names no entry.
func (s *Server) taskRefusal(ctx context.Context, l *intentload.Loaded, name string, u unit.Unit) (string, error) {
	id, ok := task.IDOf(u)
	if !ok || isBuiltin(name) {
		return "", nil
	}
	if s.deps.Tasks == nil {
		return "", errTasksUnavailable
	}
	e, err := s.deps.Tasks.Task(ctx, id)
	if err != nil {
		return "", notFound{err.Error()}
	}
	d, ok := l.Registry.Get(dintent.Name(name))
	if !ok || !(d.ProviderOrDefault() == u.Provider || slices.Contains(d.Providers, u.Provider)) {
		return "", nil
	}
	for _, st := range taskIntentActions(l, e) {
		if st.ID == name {
			return st.Reason, nil
		}
	}
	return NotOffered(name, u), nil
}

// unitIntent admits d on u, a unit that is no pull request: d serves u's
// provider, is offered on some surface, and declares none of the builtins
// that act on pull requests only.
func unitIntent(d dintent.Def, u unit.Unit) error {
	if !d.Serves(u.Provider) {
		return badRequest{fmt.Sprintf("the intent %s serves %s units, not %s", d.Name, d.ProviderOrDefault(), u.Label())}
	}
	if d.Action == nil {
		return conflict{NotOffered(string(d.Name), u), api.RefusalNotOffered}
	}
	var prOnly []string
	if d.Prepare != "" {
		prOnly = append(prOnly, "prepare "+d.Prepare)
	}
	if len(d.Effects) > 0 {
		prOnly = append(prOnly, "effects "+strings.Join(d.Effects, ", "))
	}
	if d.Intake {
		prOnly = append(prOnly, "intake")
	}
	if d.Launch != "" {
		prOnly = append(prOnly, "launch "+d.Launch)
	}
	if len(prOnly) > 0 {
		return badRequest{fmt.Sprintf("the intent %s declares %s, which act on pull requests only; %s cannot run it", d.Name, strings.Join(prOnly, "; "), u.Label())}
	}
	return nil
}

// parseUnitArg reads a unit as the agent's command line names it: its URL,
// or "<provider>:<key>" of a provider the daemon knows whose units have no
// URL.
func (s *Server) parseUnitArg(raw string) (unit.Unit, error) {
	if u, ok := s.deps.Units.ParseURL(raw); ok {
		return u, nil
	}
	provider, key, ok := strings.Cut(raw, ":")
	if !ok || s.deps.Units == nil || s.deps.Units.Linked(unit.ProviderID(provider)) || strings.Contains(provider, "/") {
		return unit.Unit{}, fmt.Errorf("not a unit: %s; name it by its URL, or as <provider>:<key>", raw)
	}
	return s.deps.Units.Parse(provider, key)
}

// unitArg names u as parseUnitArg reads it.
func (s *Server) unitArg(u unit.Unit) string {
	if u.IsZero() {
		return ""
	}
	if url, ok := s.deps.Units.URL(u); ok {
		return url
	}
	return u.String()
}

// sourceFollowed says whether the track or untrack of u goes to its source,
// which keeps its own follows, instead of the pull request overrides.
func sourceFollowed(name string, u unit.Unit, follows func(unit.ProviderID) bool) bool {
	return (name == "track" || name == "untrack") && !isPullRequest(u) && follows != nil && follows(u.Provider)
}

// sourceFollow runs track or untrack on a unit its source follows.
func (s *Server) sourceFollow(ctx context.Context, name string, u unit.Unit) error {
	if name == "untrack" {
		if s.deps.Untrack == nil {
			return notFound{"untrack " + u.Label() + ": no source untracks it"}
		}
		return s.deps.Untrack(ctx, u)
	}
	if s.track == nil {
		return notFound{"track " + u.Label() + ": no source tracks it"}
	}
	read, err := s.track(ctx, u)
	if errors.Is(err, port.ErrNotFound) {
		return notFound{"track " + u.Label() + ": " + err.Error()}
	}
	if err != nil || read.Save == nil {
		return err
	}
	return read.Save(ctx)
}
