package present

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/termfmt"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// unitRow is the row of a pull request the read model does not list: a
// lapsed one the inventory holds, else one read from GitHub; its Unit is
// the pull request as the inventory or GitHub names it.
func (p *Presenter) unitRow(ctx context.Context, q api.UnitRowQuery) (api.Row, error) {
	asked := catalog.Asked{Name: q.Action, Inputs: q.Inputs}
	if st, found, err := p.storedRead(ctx, q.Unit, asked, q.Unmuted); err != nil || found {
		return st.row, err
	}
	if q.Kept {
		return api.Row{}, api.Refuse(http.StatusNotFound, "%s is not in the inventory", q.Unit.Label())
	}
	base, at, err := p.h.Model.Base(ctx)
	if err != nil {
		return api.Row{}, err
	}
	base.Asked = asked
	d := p.deriverAt(base, at)
	c, err := p.readUntracked(ctx, q.Unit)
	if err != nil {
		return api.Row{}, err
	}
	o, err := p.d.Store.Override(ctx, c.Ref)
	if err != nil {
		return api.Row{}, err
	}
	return d.UntrackedRow(c, o), nil
}

// stored is what a pull request the inventory holds derives its row from.
type stored struct {
	row api.Row
	pr  dinv.PR
	evs []dinv.Event
}

// storedRead derives the row of a pull request the plugin reported from its
// report, the host's events and overrides, its turn book and its open
// intakes; unmuted drops the override and the pending round.
func (p *Presenter) storedRead(ctx context.Context, u unit.Unit, asked catalog.Asked, unmuted bool) (stored, bool, error) {
	rep, found, err := p.reportOf(ctx, u)
	if err != nil || !found {
		return stored{}, false, err
	}
	pull := rep.PR
	ref := pull.Ref
	evs, err := p.d.Store.Events(ctx, ref)
	if err != nil {
		return stored{}, false, err
	}
	o := dinv.Override{PR: ref}
	if !unmuted {
		if o, err = p.d.Store.Override(ctx, ref); err != nil {
			return stored{}, false, err
		}
		o.PR = ref
	}
	base, at, err := p.h.Model.Base(ctx)
	if err != nil {
		return stored{}, false, err
	}
	base.Asked = asked
	if unmuted {
		base.Pending = nil
	}
	d := p.deriverAt(base, at)
	if p.h.Books != nil {
		books, err := p.h.Books(ctx, ref.Unit())
		if err != nil {
			return stored{}, false, err
		}
		if set := p.h.Settings(); set.Loaded != nil {
			d.Turns = prrow.TurnFacts(books, set.Loaded.Registry)
		}
	}
	if p.h.Intakes != nil {
		intakes, err := p.h.Intakes(ctx)
		if err != nil {
			return stored{}, false, err
		}
		if open := intakes[ref.Unit()]; len(open) > 0 {
			d.Intakes = map[pr.Ref][]intake.Intake{ref: open}
		}
	}
	return stored{row: d.Row(rep, evs, o), pr: pull, evs: evs}, true, nil
}

// reportOf is the plugin's report of the pull request of u under any case.
func (p *Presenter) reportOf(ctx context.Context, u unit.Unit) (prrow.Report, bool, error) {
	if p.d.Reports == nil {
		return prrow.Report{}, false, nil
	}
	units, err := p.d.Reports.Units(ctx, unit.GitHub)
	if err != nil {
		return prrow.Report{}, false, err
	}
	i := slices.IndexFunc(units, func(x unit.Unit) bool { return x == u })
	if i < 0 {
		i = slices.IndexFunc(units, func(x unit.Unit) bool { return p.same(x, u) })
	}
	if i < 0 {
		return prrow.Report{}, false, nil
	}
	snaps, err := p.d.Reports.Reported(ctx, units[i:i+1])
	if err != nil {
		return prrow.Report{}, false, err
	}
	s, ok := snaps[units[i]]
	if !ok {
		return prrow.Report{}, false, nil
	}
	return prrow.ReportOf(s)
}

// deriverAt derives the rows of a request from base, with no turn and no
// intake of any pull request.
func (p *Presenter) deriverAt(base readmodel.Base, at time.Time) prrow.Deriver {
	return prrow.DeriverAt(base, p.h.Settings(), p.h.BriefOn, at)
}

// inventoryPR is the reported pull request of the unit under any case.
func (p *Presenter) inventoryPR(ctx context.Context, u unit.Unit) (dinv.PR, bool, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return dinv.PR{}, false, nil
	}
	reported := prrow.ReportedPRs{Reports: p.d.Reports}
	pull, ok, err := reported.PR(ctx, ref)
	if err != nil || ok {
		return pull, ok, err
	}
	all, err := reported.List(ctx, false)
	if err != nil {
		return dinv.PR{}, false, err
	}
	if i := slices.IndexFunc(all, func(x dinv.PR) bool { return p.same(x.Ref.Unit(), u) }); i >= 0 {
		return all[i], true, nil
	}
	return dinv.PR{}, false, nil
}

// readUntracked reads from GitHub a pull request the inventory does not hold.
func (p *Presenter) readUntracked(ctx context.Context, u unit.Unit) (dgh.Candidate, error) {
	ref, ok := pr.RefOf(u)
	if !ok {
		return dgh.Candidate{}, src.ErrNotFound
	}
	var page dgh.CandidatePage
	if err := p.pluginView(ctx, pluginViewCandidate, map[string]any{"owner": ref.Owner, "repo": ref.Repo, "number": ref.Number}, &page); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, src.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		return dgh.Candidate{}, api.Refuse(status, "cannot read %s from GitHub: %v", u.Label(), err)
	}
	if len(page.Candidates) == 0 {
		return dgh.Candidate{}, api.Refuse(http.StatusNotFound, "%s: GitHub knows no such pull request", u.Label())
	}
	return page.Candidates[0], nil
}

// detail is the timeline and the brief of a pull request the inventory
// holds; none of another.
func (p *Presenter) detail(ctx context.Context, u unit.Unit) (api.PluginDetail, error) {
	out := api.PluginDetail{Events: []api.Event{}}
	pull, evs, found, err := p.read(ctx, u)
	if err != nil || !found {
		return out, err
	}
	for _, e := range evs {
		if !dinv.Bookkeeping(e.Kind) {
			out.Events = append(out.Events, wireEvent(e))
		}
	}
	if p.d.Briefs != nil {
		views, err := p.d.Briefs.Views(ctx, pull)
		if err != nil {
			p.h.Logf("detail %s: brief: %v", u.Label(), err)
		} else if err = p.d.Briefs.RefreshIfStale(ctx, pull); err != nil {
			p.h.Logf("detail %s: brief refresh: %v", u.Label(), err)
		}
		out.Brief = WireBriefView(views[pull.Ref])
	}
	return out, nil
}

// read is the pull request of u and its events as the read model holds
// them, else as the inventory does under any case.
func (p *Presenter) read(ctx context.Context, u unit.Unit) (dinv.PR, []dinv.Event, bool, error) {
	if p.h.Model != nil {
		if rd, ok, err := p.h.Model.Read(ctx, u); err != nil {
			return dinv.PR{}, nil, false, err
		} else if pull, has := prrow.PROf(rd.In); ok && has {
			return pull, prrow.EventsOf(rd.In), true, nil
		}
	}
	pull, found, err := p.inventoryPR(ctx, u)
	if err != nil || !found {
		return pull, nil, found, err
	}
	evs, err := p.d.Store.Events(ctx, pull.Ref)
	return pull, evs, err == nil, err
}

func wireEvent(e dinv.Event) api.Event {
	return api.Event{Unit: e.PR.Unit(), Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: e.Data}
}

// WireBriefView is v as clients read it; nil for none.
func WireBriefView(v *brief.View) *api.BriefView {
	if v == nil {
		return nil
	}
	out := &api.BriefView{State: v.State, Model: v.Model, At: v.At, Error: v.Error, Stale: v.Stale}
	if b := v.Brief; b != nil {
		out.Brief = &api.Brief{Goal: b.Goal, Context: b.Context, Kind: b.Kind, Size: b.Size, Risk: b.Risk,
			RiskWhy: b.RiskWhy, OpenQuestions: b.OpenQuestions, Tags: b.Tags, Extra: b.Extra, ExtraVersions: b.ExtraVersions}
	}
	return out
}

// cleanCandidate cuts the free text of a candidate to what a row shows.
func cleanCandidate(c dgh.Candidate) dgh.Candidate {
	c.Title = termfmt.OneLine(c.Title, 300)
	c.HeadBranch = termfmt.OneLine(c.HeadBranch, 255)
	c.BaseBranch = termfmt.OneLine(c.BaseBranch, 255)
	return c
}
