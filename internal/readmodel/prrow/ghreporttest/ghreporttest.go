// Package ghreporttest derives the GitHub plugin's snapshot of a pull request
// the host's tests hold as inventory rows, with the plugin's own code, so a
// host test renders the rows the running plugin's reports render. Only
// tests import it.
package ghreporttest

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/sweep"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/readmodel"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghplugin "github.com/wloophq/wloop/plugins/github"
	"github.com/wloophq/wloop/plugins/github/plan"
	ghpr "github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/row"
	"github.com/wloophq/wloop/plugins/github/situation"
	ghstore "github.com/wloophq/wloop/plugins/github/store"
)

// Env is what every snapshot of a test reads besides its pull request.
type Env struct {
	Viewer string
	Teams  []string
	Ignore []string
	// Stalled zero is the configuration's default.
	Stalled *config.Stalled
	Now     time.Time
	Loop    string
	// MaxDetailAge is the oldest detail the plugin calls fresh.
	MaxDetailAge time.Duration
}

// Input is one pull request as a host test holds it.
type Input struct {
	PR     dinv.PR
	Events []dinv.Event
	// Adopted and Tracked are the plugin's overrides of the pull request;
	// Held is the user's hold of its rounds.
	Adopted, Tracked, Held bool
	// AddressedAt is when its driving address turn reached its state.
	AddressedAt time.Time
	// Facts are its detail as the host's planner reads it; nil before a
	// full read.
	Facts *dinv.PlanFacts
	// PlanFacts are its detail as the plugin's planner alone reads it; nil
	// is Facts.
	PlanFacts *dinv.PlanFacts
}

// Snapshot is the snapshot the plugin reports of in under env.
func Snapshot(env Env, in Input) src.Snapshot {
	r := Row(env, in)
	raw, err := json.Marshal(ghplugin.Data{PR: r.PR, Tracked: in.Tracked, Row: &r.Payload})
	if err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	s := r.Report()
	s.Unit, s.Rev, s.At, s.Data = r.PR.Ref.Unit(), 1, env.Now, raw
	return s
}

// Row is the source row the plugin derives of in under env.
func Row(env Env, in Input) row.Row {
	p := PluginPR(in.PR)
	if env.MaxDetailAge == 0 {
		sw := config.Defaults().Sweep
		env.MaxDetailAge = time.Duration(sw.Slow) + 2*time.Duration(sw.Fast)
	}
	th := config.Defaults().GitHub.Stalled
	if env.Stalled != nil {
		th = *env.Stalled
	}
	evs := make([]ghpr.Event, len(in.Events))
	for i, e := range in.Events {
		evs[i] = ghpr.Event{PR: p.Ref, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: e.Data}
	}
	var detail *ghstore.Detail
	if f := in.Facts; f != nil {
		detail = &ghstore.Detail{ReadAt: f.ReadAt, AuthorAssociation: f.AuthorAssociation, Visibility: f.Visibility, Labels: f.Labels}
		for _, r := range f.Reviews {
			detail.Reviews = append(detail.Reviews, ghpr.Review{ID: r.ID, State: r.State, Author: r.Author, SubmittedAt: r.SubmittedAt, CommitOid: r.CommitOid})
		}
	}
	return row.Derive(row.Input{PR: p, Events: evs, Override: ghpr.Override{Ref: p.Ref, Adopted: in.Adopted, Tracked: in.Tracked}, Detail: detail,
		MaxDetailAge: env.MaxDetailAge,
		Viewer:       env.Viewer, Teams: env.Teams, Ignore: env.Ignore,
		Stalled:     situation.Thresholds{Review: time.Duration(th.Review), Reply: time.Duration(th.Reply), Merge: time.Duration(th.Merge)},
		AddressedAt: in.AddressedAt, Loop: env.Loop, Now: env.Now})
}

// PluginPR is p as the plugin keeps it.
func PluginPR(p dinv.PR) ghpr.PR {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	var out ghpr.PR
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	return out
}

// Inventory are the reports of the pull requests a host test's inventory
// holds, each derived when read, as the plugin would report them now: the
// viewer and teams the inventory keeps, unless Env names them.
type Inventory struct {
	Store *inventory.Store
	Env   Env
	// Now nil is Env.Now.
	Now func() time.Time
	// Config is the configuration whose [plugins.github] the plugin reads:
	// who a row never waits on and when a situation stalls; nil is Env's.
	Config func() config.Config
	// Identity is the viewer as the plugin names them; nil reads the viewer
	// and teams the inventory keeps.
	Identity func(ctx context.Context) (src.Identity, error)
	// Plan is what the plugin's planner reads of the host; nil reports no
	// planner facts.
	Plan *InventoryPlan
}

// InventoryPlan are the host's state the plugin's planner reads, as a host
// test holds it.
type InventoryPlan struct {
	Facts    dinv.PlanInputs
	Rounds   sweep.Rounds
	Books    sweep.Books
	Snapshot sweep.Snapshot
	Git      plan.Git
}

var _ readmodel.Reports = Inventory{}

// metaTeams is the meta key the host keeps the viewer's GitHub teams under.
const metaTeams = "teams:github"

func (i Inventory) Units(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	if p != unit.GitHub {
		return nil, nil
	}
	prs, err := inventorytest.Of(i.Store).ListPRs(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]unit.Unit, len(prs))
	for n, x := range prs {
		out[n] = x.Ref.Unit()
	}
	return out, nil
}

func (i Inventory) Reported(ctx context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	if i.Plan != nil {
		return i.planned(ctx, units)
	}
	env, ins, err := i.inputs(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]src.Snapshot, len(ins))
	for _, in := range ins {
		out[in.PR.Ref.Unit()] = Snapshot(env, in)
	}
	return out, nil
}

// planned are the reports of units, the planner's facts included, which
// reads every pull request i holds.
func (i Inventory) planned(ctx context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	all, err := i.Units(ctx, unit.GitHub)
	if err != nil {
		return nil, err
	}
	env, ins, err := i.inputs(ctx, all)
	if err != nil {
		return nil, err
	}
	overrides, err := i.Store.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	prs := make([]dinv.PR, len(ins))
	for n, x := range ins {
		prs[n] = x.PR
	}
	facts, err := i.Plan.Facts.Facts(ctx, prs, env.Viewer)
	if err != nil {
		return nil, err
	}
	for n, x := range ins {
		ins[n].Held = overrides[x.PR.Ref].Hold
		if f, ok := facts[x.PR.Ref.Unit()]; ok {
			ins[n].PlanFacts = &f
		}
	}
	pe := PlanEnv{Git: i.Plan.Git, Config: config.Defaults()}
	if i.Config != nil {
		pe.Config = i.Config()
	}
	if i.Plan.Rounds != nil {
		if pe.Rounds, err = i.Plan.Rounds.Load(ctx); err != nil {
			return nil, err
		}
		if pe.Pause, err = i.Plan.Rounds.Pause(ctx); err != nil {
			return nil, err
		}
	}
	if i.Plan.Books != nil {
		if pe.Books, err = i.Plan.Books.Books(ctx); err != nil {
			return nil, err
		}
	}
	if i.Plan.Snapshot != nil {
		if pe.Snapshot, err = i.Plan.Snapshot.Read(ctx, wsdomain.PartAll); err != nil {
			return nil, err
		}
	}
	reported := Planned(env, pe, ins)
	out := make(map[unit.Unit]src.Snapshot, len(units))
	for _, u := range units {
		if s, ok := reported[u]; ok {
			out[u] = s
		}
	}
	return out, nil
}

// Rows are the source rows the plugin derives now of every pull request i
// holds.
func (i Inventory) Rows(ctx context.Context) ([]row.Row, error) {
	units, err := i.Units(ctx, unit.GitHub)
	if err != nil {
		return nil, err
	}
	env, ins, err := i.inputs(ctx, units)
	if err != nil {
		return nil, err
	}
	out := make([]row.Row, len(ins))
	for n, in := range ins {
		out[n] = Row(env, in)
	}
	return out, nil
}

// inputs are the env and the inputs of the pull requests of units i holds.
func (i Inventory) inputs(ctx context.Context, units []unit.Unit) (Env, []Input, error) {
	prs, err := inventorytest.Of(i.Store).ListPRs(ctx, false)
	if err != nil {
		return Env{}, nil, err
	}
	want := map[unit.Unit]bool{}
	for _, u := range units {
		want[u] = true
	}
	var refs []pr.Ref
	byRef := map[pr.Ref]dinv.PR{}
	for _, x := range prs {
		if want[x.Ref.Unit()] {
			refs = append(refs, x.Ref)
			byRef[x.Ref] = x
		}
	}
	if len(refs) == 0 {
		return Env{}, nil, nil
	}
	evs, err := i.Store.EventsByPR(ctx, refs)
	if err != nil {
		return Env{}, nil, err
	}
	overrides, err := i.Store.Overrides(ctx)
	if err != nil {
		return Env{}, nil, err
	}
	env := i.Env
	if env.Loop == "" {
		env.Loop = brand.Name
	}
	if i.Config != nil {
		cfg := i.Config()
		env.Ignore = slices.Concat(cfg.GitHub.AutomatedReviewers, cfg.GitHub.AddressSkipReviewers)
		env.Stalled = &cfg.GitHub.Stalled
		env.MaxDetailAge = time.Duration(cfg.Sweep.Slow) + 2*time.Duration(cfg.Sweep.Fast)
	}
	if i.Now != nil {
		env.Now = i.Now()
	}
	if i.Identity != nil && env.Viewer == "" {
		id, err := i.Identity(ctx)
		if err != nil {
			return Env{}, nil, err
		}
		env.Viewer, env.Teams = strings.TrimPrefix(id.Viewer.Key, "github:"), id.Teams
		if env.Teams == nil {
			env.Teams = []string{}
		}
	}
	if env.Viewer == "" {
		if env.Viewer, err = i.Store.Viewer(ctx, unit.GitHub); err != nil {
			return Env{}, nil, err
		}
	}
	if env.Teams == nil {
		raw, err := i.Store.Meta(ctx, metaTeams)
		if err != nil {
			return Env{}, nil, err
		}
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &env.Teams)
		}
	}
	out := make([]Input, 0, len(refs))
	for _, ref := range refs {
		o := overrides[ref]
		out = append(out, Input{PR: byRef[ref], Events: evs[ref], Adopted: o.Adopted, Tracked: o.Tracked})
	}
	return env, out, nil
}

// Keeper keeps reports, as the host's snapshot store does.
type Keeper interface {
	Put(ctx context.Context, snapshots map[unit.Unit]src.Snapshot) error
}

// Keep puts the reports of every pull request i holds now into k.
func (i Inventory) Keep(ctx context.Context, k Keeper) error {
	units, err := i.Units(ctx, unit.GitHub)
	if err != nil {
		return err
	}
	snaps, err := i.Reported(ctx, units)
	if err != nil {
		return err
	}
	return k.Put(ctx, snaps)
}
