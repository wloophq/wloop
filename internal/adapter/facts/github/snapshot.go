package factsgithub

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/workspace"
)

// view is one read of everything the facts derive from but the clock: the
// stored pull requests, the books, the round counts, the holds and the
// agents' activity. Facts, events and the plugin's reports load lazily, by
// unit.
type view struct {
	p       *Provider
	gen     port.Generation
	builtAt time.Time
	cfg     config.Config

	viewer    string
	rows      []dinv.PR
	byRef     map[pr.Ref]dinv.PR
	books     map[unit.Unit]turn.Book
	rounds    *round.Store
	overrides map[pr.Ref]dinv.Override
	registry  *dintent.Registry
	activity  map[string]wsdomain.Activity

	mu      sync.Mutex
	prFacts map[pr.Ref]dinv.PlanFacts
	events  map[pr.Ref][]dinv.Event
	// reports are the plugin's reports read, nil for a pull request it
	// has not reported.
	reports map[pr.Ref]*prrow.Report
}

func (p *Provider) build(ctx context.Context, now time.Time) (*view, error) {
	v := &view{p: p, builtAt: now, cfg: p.cfg(), prFacts: map[pr.Ref]dinv.PlanFacts{}, events: map[pr.Ref][]dinv.Event{},
		reports: map[pr.Ref]*prrow.Report{}}
	if p.d.Changes != nil {
		v.gen = p.d.Changes.Gen()
	}
	var err error
	if v.viewer, err = p.d.Inputs.Viewer(ctx); err != nil {
		return nil, err
	}
	if v.rows, err = p.d.Inputs.Rows(ctx); err != nil {
		return nil, err
	}
	v.byRef = make(map[pr.Ref]dinv.PR, len(v.rows))
	for _, r := range v.rows {
		v.byRef[r.Ref] = r
	}
	v.books = map[unit.Unit]turn.Book{}
	if p.d.Books != nil {
		if v.books, err = p.d.Books.Books(ctx); err != nil {
			return nil, err
		}
	}
	v.rounds = round.New()
	if p.d.Rounds != nil {
		if v.rounds, err = p.d.Rounds.Load(ctx); err != nil {
			return nil, err
		}
	}
	if v.overrides, err = p.d.Store.Overrides(ctx); err != nil {
		return nil, err
	}
	v.registry = &dintent.Registry{}
	if p.d.Intents != nil {
		if r := p.d.Intents(); r != nil {
			v.registry = r
		}
	}
	if p.d.Snapshot != nil {
		snap, err := p.d.Snapshot.Read(ctx, wsdomain.PartAll)
		if err != nil {
			return nil, err
		}
		v.activity = snap.Activity()
	}
	return v, nil
}

// listed are the open pull requests nothing lapsed, those a workspace
// targets, and those with an open turn, an answer waiting or a recent
// review turn.
func (v *view) listed(ctx context.Context, now time.Time) ([]unit.Unit, error) {
	seen := map[unit.Unit]bool{}
	var out []unit.Unit
	add := func(u unit.Unit) {
		if u.Provider == unit.GitHub && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	all := make([]unit.Unit, 0, len(v.rows))
	for _, r := range v.rows {
		all = append(all, r.Ref.Unit())
	}
	if err := v.loadReports(ctx, all); err != nil {
		return nil, err
	}
	for _, r := range v.rows {
		if r.State == "OPEN" && (!r.Lapsed() || v.overrides[r.Ref].Adopted) {
			add(r.Ref.Unit())
		}
		if rep := v.reportOf(r.Ref); rep != nil && rep.Plan.Board.Carded {
			add(r.Ref.Unit())
		}
	}
	for u, b := range v.books {
		if _, ok := openTurn(b); ok {
			add(u)
		}
		if _, ok := waitingAnswer(b); ok {
			add(u)
		}
		if at, ok := newestReview(b); ok && now.Sub(at) < dinv.ReviewTurnsFollowed {
			add(u)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	})
	return out, nil
}

// newestReview is when b's newest review turn was sent.
func newestReview(b turn.Book) (time.Time, bool) {
	var at time.Time
	found := false
	for _, t := range b.Turns {
		if t.Role == builtin.RoleReview && t.State != turn.Withdrawn {
			found = true
			if t.SentAt.After(at) {
				at = t.SentAt
			}
		}
	}
	return at, found
}

// loadFacts reads the facts of the stored units not read yet, in one query.
func (v *view) loadFacts(ctx context.Context, units []unit.Unit) error {
	v.mu.Lock()
	var missing []dinv.PR
	for _, u := range units {
		ref, err := refOf(u)
		if err != nil {
			continue
		}
		row, stored := v.byRef[ref]
		if _, ok := v.prFacts[ref]; stored && !ok {
			missing = append(missing, row)
		}
	}
	v.mu.Unlock()
	if len(missing) == 0 {
		return nil
	}
	got, err := v.p.d.Inputs.Facts(ctx, missing, v.viewer)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for u, f := range got {
		if ref, err := refOf(u); err == nil {
			v.prFacts[ref] = f
		}
	}
	return nil
}

// loadEvents reads the events of the stored units not read yet, in one query.
func (v *view) loadEvents(ctx context.Context, units []unit.Unit) error {
	v.mu.Lock()
	var missing []pr.Ref
	for _, u := range units {
		ref, err := refOf(u)
		if err != nil {
			continue
		}
		if _, stored := v.byRef[ref]; !stored {
			continue
		}
		if _, ok := v.events[ref]; !ok {
			missing = append(missing, ref)
		}
	}
	v.mu.Unlock()
	if len(missing) == 0 {
		return nil
	}
	got, err := v.p.d.Store.EventsByPR(ctx, missing)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, ref := range missing {
		v.events[ref] = got[ref]
	}
	return nil
}

// loadReports reads the plugin's reports of the units not read yet, in one
// query.
func (v *view) loadReports(ctx context.Context, units []unit.Unit) error {
	if v.p.d.Reports == nil {
		return nil
	}
	v.mu.Lock()
	var missing []unit.Unit
	for _, u := range units {
		if ref, err := refOf(u); err == nil {
			if _, ok := v.reports[ref]; !ok {
				missing = append(missing, ref.Unit())
			}
		}
	}
	v.mu.Unlock()
	if len(missing) == 0 {
		return nil
	}
	snaps, err := v.p.d.Reports.Reported(ctx, missing)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, u := range missing {
		ref, _ := refOf(u)
		v.reports[ref] = nil
		rep, ok, err := prrow.ReportOf(snaps[u])
		if err != nil {
			return err
		}
		if ok {
			v.reports[ref] = &rep
		}
	}
	return nil
}

// reportOf is the plugin's report of ref; nil for none.
func (v *view) reportOf(ref pr.Ref) *prrow.Report {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reports[ref]
}

func (v *view) factsOf(ref pr.Ref) (dinv.PlanFacts, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	f, ok := v.prFacts[ref]
	return f, ok
}

func (v *view) eventsOf(ref pr.Ref) []dinv.Event {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.events[ref]
}

// openTurn is the book's newest open turn.
func openTurn(b turn.Book) (turn.Turn, bool) {
	for _, t := range slices.Backward(b.Turns) {
		if t.State.Open() {
			return t, true
		}
	}
	return turn.Turn{}, false
}

// waitingAnswer is an answer still pending or queued for its session.
func waitingAnswer(b turn.Book) (turn.Answer, bool) {
	for _, a := range b.Answers {
		if a.Route == turn.Queued || a.Route == turn.Pending {
			return a, true
		}
	}
	return turn.Answer{}, false
}

// InventoryInputs are the planning inputs as the inventory stores them.
func InventoryInputs(st *inventory.Store) dinv.PlanInputs { return inventoryInputs{st} }

type inventoryInputs struct{ store *inventory.Store }

func (i inventoryInputs) Viewer(ctx context.Context) (string, error) {
	return i.store.Viewer(ctx, unit.GitHub)
}

func (i inventoryInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	return i.store.ListPRs(ctx, false)
}

// Facts reads the details of rows in one query; a detail of another version
// gives its facts with a zero ReadAt, as the collector has yet to re-read it.
func (i inventoryInputs) Facts(ctx context.Context, rows []dinv.PR, viewer string) (map[unit.Unit]dinv.PlanFacts, error) {
	refs := make([]pr.Ref, len(rows))
	for n, row := range rows {
		refs[n] = row.Ref
	}
	details, err := i.store.Details(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PlanFacts, len(rows))
	for _, row := range rows {
		d, ok := details[row.Ref]
		f := dinv.PlanFacts{Facts: inventory.FactsOf(row, d, viewer)}
		if ok && d.Version == inventory.DetailVersion {
			f.ReadAt = d.ReadAt
		}
		out[row.Ref.Unit()] = f
	}
	return out, nil
}

func (i inventoryInputs) Ended(ctx context.Context, kind string, units []unit.Unit) (map[unit.Unit]bool, error) {
	refs := make([]pr.Ref, 0, len(units))
	for _, u := range units {
		if ref, err := refOf(u); err == nil {
			refs = append(refs, ref)
		}
	}
	ended, err := i.store.Ended(ctx, kind, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]bool, len(ended))
	for ref, ok := range ended {
		out[ref.Unit()] = ok
	}
	return out, nil
}

// LocalGit reads the checkouts through git.
func LocalGit(g gitx.Git) sweep.LocalGit { return localGit{g} }

type localGit struct{ git gitx.Git }

func (g localGit) Status(ctx context.Context, dir string) (sweep.GitFacts, error) {
	dirty, err := g.git.DirtyTracked(ctx, dir)
	if err != nil {
		return sweep.GitFacts{}, err
	}
	head, err := g.git.Head(ctx, dir)
	if err != nil {
		return sweep.GitFacts{}, err
	}
	return sweep.GitFacts{Dirty: dirty, Head: head}, nil
}

func (g localGit) Head(ctx context.Context, dir string) (string, error) { return g.git.Head(ctx, dir) }

func (g localGit) Upstream(ctx context.Context, dir string) (string, error) {
	return g.git.UpstreamBranch(ctx, dir)
}

func (g localGit) IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error) {
	return g.git.IsAncestor(ctx, dir, ancestor, commit)
}

// SnapshotOf is the snapshot merged from every manager of set; nil when one keeps none.
func SnapshotOf(set *workspace.Set) sweep.Snapshot {
	if set.Snapshots() {
		return set
	}
	return nil
}
