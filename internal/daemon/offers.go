package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// NotOffered is the conflict of an action the row does not offer.
func NotOffered(action string, u unit.Unit) string {
	return fmt.Sprintf("no %s on %s", action, u.Label())
}

// offerOf is the state of id on the row, false when the row does not offer it.
func offerOf(r api.Row, id string) (api.ActionState, bool) {
	i := slices.IndexFunc(r.Actions, func(st api.ActionState) bool { return st.ID == id })
	if i < 0 {
		return api.ActionState{}, false
	}
	return r.Actions[i], true
}

type unavailable struct{ msg string }

func (e unavailable) Error() string { return e.msg }

// currentRow is the row of the unit as served now, or, for a pull request
// the inventory lacks, the row a GitHub read derives.
func (s *Server) currentRow(ctx context.Context, u unit.Unit) (api.Row, error) {
	return s.rowAsked(ctx, u, catalog.Asked{})
}

// rowAsked is currentRow for a request that asks for an action with its
// inputs.
func (s *Server) rowAsked(ctx context.Context, u unit.Unit, asked catalog.Asked) (api.Row, error) {
	rd, ok, err := s.readRow(ctx, u)
	if err != nil {
		return api.Row{}, err
	}
	if ok {
		if asked.Name == "" && asked.Inputs == nil {
			return rd.Row, nil
		}
		b := rd.Base
		b.Asked = asked
		return rd.Derive(b, rd.In)
	}
	if st, found, err := s.storedRead(ctx, u, asked, false); err != nil {
		return api.Row{}, err
	} else if found {
		return st.row, nil
	}
	base, at, err := s.readModel().Base(ctx)
	if err != nil {
		return api.Row{}, err
	}
	base.Asked = asked
	d := s.deriverAt(base, at)
	c, err := s.readUntracked(ctx, u, d.Viewer)
	if err != nil {
		return api.Row{}, err
	}
	o, err := s.deps.Overrides.Override(ctx, c.Ref.Unit())
	if err != nil {
		return api.Row{}, err
	}
	return d.UntrackedRow(c, o), nil
}

// stored is what a pull request the inventory holds and the read model does
// not list, a lapsed one nobody adopted, derives from.
type stored struct {
	row  api.Row
	pr   dinv.PR
	evs  []dinv.Event
	book *turn.Book
	d    prrow.Deriver
}

// storedRead derives the row of such a pull request from the inventory, its
// own turn book and its open intakes; unmuted drops the override and the
// pending round.
func (s *Server) storedRead(ctx context.Context, u unit.Unit, asked catalog.Asked, unmuted bool) (stored, bool, error) {
	p, found, err := s.inventoryPR(ctx, u)
	if err != nil || !found {
		return stored{}, false, err
	}
	evs, err := s.deps.Events.Events(ctx, p.Ref.Unit())
	if err != nil {
		return stored{}, false, err
	}
	o := dinv.Override{PR: p.Ref}
	if !unmuted {
		if o, err = s.deps.Overrides.Override(ctx, p.Ref.Unit()); err != nil {
			return stored{}, false, err
		}
		o.PR = p.Ref
	}
	base, at, err := s.readModel().Base(ctx)
	if err != nil {
		return stored{}, false, err
	}
	base.Asked = asked
	if unmuted {
		base.Pending = nil
	}
	d := s.deriverAt(base, at)
	st := stored{pr: p, evs: evs, d: d}
	if s.deps.Turns != nil {
		books, err := s.deps.Turns.Store.Books(ctx, p.Ref.Unit())
		if err != nil {
			return stored{}, false, err
		}
		if b, ok := books[p.Ref.Unit()]; ok {
			st.book = &b
		}
		st.d.Turns = prrow.TurnFacts(books, s.loaded().Registry)
	}
	if intakes, err := s.waitingIntakes(ctx); err != nil {
		return stored{}, false, err
	} else if len(intakes[p.Ref.Unit()]) > 0 {
		st.d.Intakes = refIntakes(p.Ref, intakes[p.Ref.Unit()])
	}
	st.row = st.d.Row(p, evs, o)
	return st, true, nil
}

// readRow is the read model's entry of the unit under any case, which costs the
// inventory's lookup only when the exact key is unknown.
func (s *Server) readRow(ctx context.Context, u unit.Unit) (readmodel.Read, bool, error) {
	m := s.readModel()
	rd, ok, err := m.Read(ctx, u)
	if ok || err != nil {
		return rd, ok, err
	}
	pr, found, err := s.inventoryPR(ctx, u)
	if err != nil || !found || pr.Ref.Unit() == u {
		return readmodel.Read{}, false, err
	}
	return m.Read(ctx, pr.Ref.Unit())
}

// deriverAt derives the rows of a request from base, with no turn and no
// intake of any pull request.
func (s *Server) deriverAt(base readmodel.Base, at time.Time) prrow.Deriver {
	return prrow.DeriverAt(base, s.rowSettings(), s.deps.Brief != nil, at)
}

// inventoryPR is the inventory's pull request of the unit under any case.
func (s *Server) inventoryPR(ctx context.Context, u unit.Unit) (dinv.PR, bool, error) {
	pr, ok, err := s.deps.PRs.PR(ctx, u)
	if err != nil || ok {
		return pr, ok, err
	}
	all, err := s.deps.PRs.ListPRs(ctx, false)
	if err != nil {
		return dinv.PR{}, false, err
	}
	if i := slices.IndexFunc(all, func(p dinv.PR) bool { return s.sameUnit(p.Ref.Unit(), u) }); i >= 0 {
		return all[i], true, nil
	}
	return dinv.PR{}, false, nil
}

func (s *Server) readUntracked(ctx context.Context, u unit.Unit, viewer string) (dgh.Candidate, error) {
	if s.deps.Explorer == nil {
		return dgh.Candidate{}, unavailable{"cannot read " + u.Label() + " from GitHub"}
	}
	page, err := s.deps.Explorer.CandidateByRef(dgh.WithPriority(ctx, dgh.PriorityBackground), refOf(u), viewer)
	if err != nil {
		return dgh.Candidate{}, badGateway{"cannot read " + u.Label() + " from GitHub: " + err.Error()}
	}
	if len(page.Candidates) == 0 {
		return dgh.Candidate{}, notFound{u.Label() + ": GitHub knows no such pull request"}
	}
	return page.Candidates[0], nil
}

func forceableKey(u unit.Unit) string { return strings.ToLower(u.Label()) }

// forceableOf is the unit's last remove refusal that force gets past, ""
// for none.
func (s *Server) forceableOf(u unit.Unit) string {
	v, _ := s.forceable.Load(forceableKey(u))
	reason, _ := v.(string)
	return reason
}

// waitingIntakes are the open intakes by pull request, the oldest first,
// after the stale session ones expire; none when the daemon keeps none.
func (s *Server) waitingIntakes(ctx context.Context) (map[unit.Unit][]intake.Intake, error) {
	if s.intakes == nil {
		return nil, nil
	}
	open, err := s.openIntakes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[unit.Unit][]intake.Intake{}
	for _, i := range open {
		out[i.Unit] = append(out[i.Unit], i)
	}
	return out, nil
}

func (s *Server) handleActions(w http.ResponseWriter, _ *http.Request) {
	l := s.loaded()
	off := s.disabledPlugins(l)
	writeJSON(w, api.ActionsResponse{Version: catalog.VersionFor(l, off), Specs: catalog.SpecsFor(l, off), Plan: catalog.PlanSpecs(l)})
}

// disabledPlugins are the disabled plugins some action of l belongs to.
func (s *Server) disabledPlugins(l *intentload.Loaded) []unit.ProviderID {
	if s.deps.PluginEnabled == nil {
		return nil
	}
	var off []unit.ProviderID
	for _, sp := range catalog.Specs(l) {
		if p := unit.ProviderID(sp.Plugin); p != "" && !slices.Contains(off, p) && !s.pluginEnabled(p) {
			off = append(off, p)
		}
	}
	return off
}
