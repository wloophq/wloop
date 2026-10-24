package factsgithub

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/workspace"
)

// view is one read of everything the facts derive from but the clock: the
// stored pull requests, the books, the round state, the holds and blocks,
// and the workspace snapshot. Facts and events load lazily, by unit.
type view struct {
	p       *Provider
	gen     port.Generation
	builtAt time.Time
	cfg     config.Config

	viewer    string
	teams     []string
	rows      []dinv.PR
	byRef     map[pr.Ref]dinv.PR
	books     map[unit.Unit]turn.Book
	rounds    *round.Store
	pause     round.Pause
	overrides map[pr.Ref]dinv.Override
	blocks    map[pr.Ref]dinv.Event
	registry  *dintent.Registry

	repos      []wsdomain.Repo
	workspaces []wsdomain.Workspace
	activity   map[string]wsdomain.Activity
	// partial is a listing that may miss a workspace: no round is due on it.
	partial bool

	mu      sync.Mutex
	prFacts map[pr.Ref]sweep.Facts
	events  map[pr.Ref][]dinv.Event
	ended   map[unit.Unit]bool
	targets *targets
}

func (p *Provider) build(ctx context.Context, now time.Time) (*view, error) {
	v := &view{p: p, builtAt: now, cfg: p.cfg(), prFacts: map[pr.Ref]sweep.Facts{}, events: map[pr.Ref][]dinv.Event{}}
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
	if p.d.Teams != nil {
		if v.teams, err = p.d.Teams(ctx); err != nil {
			return nil, err
		}
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
		if v.pause, err = p.d.Rounds.Pause(ctx); err != nil {
			return nil, err
		}
	}
	if v.overrides, err = p.d.Store.Overrides(ctx); err != nil {
		return nil, err
	}
	if v.blocks, err = prBlocks(ctx, p.d.Store); err != nil {
		return nil, err
	}
	v.registry = &dintent.Registry{}
	if p.d.Intents != nil {
		if r := p.d.Intents(); r != nil {
			v.registry = r
		}
	}
	if err := v.readSnapshot(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// readSnapshot reads the workspace listing: a listing
// that may miss some is partial, and one without a snapshot lists none.
func (v *view) readSnapshot(ctx context.Context) error {
	if v.p.d.Snapshot == nil {
		v.partial = true
		return nil
	}
	snap, err := v.p.d.Snapshot.Read(ctx, wsdomain.PartAll)
	if err != nil {
		return err
	}
	v.partial = partialSnapshot(snap)
	v.repos = slices.Clone(snap.Repos)
	v.workspaces = slices.Clone(snap.Workspaces)
	v.activity = snap.Activity()
	return nil
}

// partialSnapshot says whether the listing may miss a workspace. In a snapshot
// merged from several managers, each manager answers for its own: one whose
// app is down while its worktrees still list, terminals unread, leaves the
// listing whole.
func partialSnapshot(snap *wsdomain.Snapshot) bool {
	split := func(ws, ts wsdomain.Scope) bool {
		return !ws.Complete || !ts.Complete || ws.RuntimeID == "" || ws.RuntimeID != ts.RuntimeID
	}
	if len(snap.Scopes) == 0 {
		return split(snap.WorkspaceScope, snap.TerminalScope)
	}
	for _, sc := range snap.Scopes {
		if !sc.Workspaces.Complete || sc.Terminals.Complete && split(sc.Workspaces, sc.Terminals) {
			return true
		}
	}
	return false
}

// listed are the open pull requests nothing lapsed, those the plan phases
// reach, and those with an open turn or an answer waiting.
func (v *view) listed(ctx context.Context, now time.Time) []unit.Unit {
	seen := map[unit.Unit]bool{}
	var out []unit.Unit
	add := func(u unit.Unit) {
		if u.Provider == unit.GitHub && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, r := range v.rows {
		if r.State == "OPEN" && (!r.Lapsed() || v.overrides[r.Ref].Adopted) {
			add(r.Ref.Unit())
		}
	}
	t := v.targetsOf(ctx)
	for _, ref := range t.order {
		add(ref.Unit())
	}
	for u, b := range v.books {
		if _, ok := openTurn(b); ok {
			add(u)
		}
		if _, ok := waitingAnswer(b); ok {
			add(u)
		}
		if at, ok := newestReview(b); ok && now.Sub(at) < sweep.ReviewTurnsFollowed {
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
	return out
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

func (v *view) factsOf(ref pr.Ref) (sweep.Facts, bool) {
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
func InventoryInputs(st *inventory.Store) sweep.Inputs { return inventoryInputs{st} }

type inventoryInputs struct{ store *inventory.Store }

func (i inventoryInputs) Viewer(ctx context.Context) (string, error) {
	return i.store.Viewer(ctx, unit.GitHub)
}

func (i inventoryInputs) Rows(ctx context.Context) ([]dinv.PR, error) {
	return i.store.ListPRs(ctx, false)
}

// Facts reads the details of rows in one query; a detail of another version
// gives its facts with a zero ReadAt, as the collector has yet to re-read it.
func (i inventoryInputs) Facts(ctx context.Context, rows []dinv.PR, viewer string) (map[unit.Unit]sweep.Facts, error) {
	refs := make([]pr.Ref, len(rows))
	for n, row := range rows {
		refs[n] = row.Ref
	}
	details, err := i.store.Details(ctx, refs)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]sweep.Facts, len(rows))
	for _, row := range rows {
		d, ok := details[row.Ref]
		f := sweep.Facts{Facts: inventory.FactsOf(row, d, viewer)}
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

// targets are the pull requests the workspaces stand for: review workspaces by link or by branch, and author
// workspaces by link or among my pull requests by branch.
type targets struct {
	review map[pr.Ref]wsdomain.Workspace
	author map[pr.Ref]wsdomain.Workspace
	// authorOrder are the author targets in the workspaces' order.
	authorOrder []pr.Ref
	// reviewLinked are the pull requests a review workspace links, lower-cased.
	reviewLinked map[string]bool
	order        []pr.Ref
}

func (v *view) targetsOf(ctx context.Context) *targets {
	v.mu.Lock()
	t := v.targets
	v.mu.Unlock()
	if t != nil {
		return t
	}
	t = v.buildTargets(ctx)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.targets == nil {
		v.targets = t
	}
	return v.targets
}

func (v *view) buildTargets(ctx context.Context) *targets {
	t := &targets{review: map[pr.Ref]wsdomain.Workspace{}, author: map[pr.Ref]wsdomain.Workspace{}, reviewLinked: map[string]bool{}}
	repoByID := map[string]wsdomain.Repo{}
	for _, r := range v.repos {
		repoByID[r.ID] = r
	}
	var reviewWS, authorWS []wsdomain.Workspace
	for _, w := range v.workspaces {
		if w.IsMain {
			w.LinkedPR = 0
		}
		switch {
		case wsdomain.MainOnDefault(w, repoByID[w.RepoID]):
		case w.HostID != wsdomain.LocalHost:
		case w.IsMain:
			authorWS = append(authorWS, w)
		case strings.HasPrefix(w.DisplayName, v.cfg.ReviewPrefix):
			reviewWS = append(reviewWS, w)
		default:
			authorWS = append(authorWS, w)
		}
	}
	add := func(m map[pr.Ref]wsdomain.Workspace, ref pr.Ref, w wsdomain.Workspace) {
		if _, ok := m[ref]; ok {
			return
		}
		m[ref] = w
		if !slices.Contains(t.order, ref) {
			t.order = append(t.order, ref)
		}
	}
	for _, w := range reviewWS {
		owner, name, ok := repoByID[w.RepoID].OwnerName()
		if !ok {
			continue
		}
		n := w.LinkedPR
		if n == 0 {
			n, _ = v.byBranch(owner, name, strings.TrimPrefix(w.DisplayName, v.cfg.ReviewPrefix))
		}
		if n == 0 {
			continue
		}
		ref := pr.Ref{Owner: owner, Repo: name, Number: n}
		t.reviewLinked[strings.ToLower(ref.String())] = true
		add(t.review, ref, w)
	}
	for _, w := range v.workspaces {
		if owner, name, ok := repoByID[w.RepoID].OwnerName(); ok && !w.IsMain && strings.HasPrefix(w.DisplayName, v.cfg.ReviewPrefix) && w.LinkedPR != 0 {
			t.reviewLinked[strings.ToLower(pr.Ref{Owner: owner, Repo: name, Number: w.LinkedPR}.String())] = true
		}
	}
	mine := v.authored(v.builtAt.Add(-authorLookback))
	for _, w := range authorWS {
		owner, name, ok := repoByID[w.RepoID].OwnerName()
		if !ok {
			continue
		}
		ref, why := matchAuthored(mine, w, owner, name)
		if ref.Number == 0 && why == "" && w.PushBranch == "" && !w.IsMain && w.HostID == wsdomain.LocalHost && v.p.d.Git != nil {
			upstream, err := v.p.d.Git.Upstream(ctx, w.Path)
			if err != nil {
				continue
			}
			if upstream != "" && upstream != w.Branch {
				w.PushBranch = upstream
				ref, _ = matchAuthored(mine, w, owner, name)
			}
		}
		if _, seen := t.author[ref]; ref.Number != 0 && !seen {
			t.authorOrder = append(t.authorOrder, ref)
			add(t.author, ref, w)
		}
	}
	return t
}

// byBranch picks, as a branch search would, the pull request of owner/name
// whose head is branch: a single one, or a single open one among several.
func (v *view) byBranch(owner, name, branch string) (int, string) {
	var matches []github.BranchMatch
	for _, p := range v.rows {
		if p.HeadBranch == branch && strings.EqualFold(p.Ref.Owner, owner) && strings.EqualFold(p.Ref.Repo, name) {
			matches = append(matches, github.BranchMatch{Number: p.Ref.Number, State: p.State})
		}
	}
	return github.PickByBranch(matches)
}

// authored are the viewer's open pull requests and those closed or merged
// since the day of since began, open first.
func (v *view) authored(since time.Time) []dinv.PR {
	y, m, d := since.UTC().Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	var open, recent []dinv.PR
	for _, p := range v.rows {
		switch {
		case !strings.EqualFold(p.Author, v.viewer):
		case p.State == "OPEN":
			open = append(open, p)
		case !p.UpdatedAt.Before(day):
			recent = append(recent, p)
		}
	}
	return append(open, recent...)
}

// matchAuthored takes the workspace's linked pull request, whoever opened
// it. By branch it needs a unique match among my pull requests, preferring
// the open one, from the same repository and not from a fork.
func matchAuthored(mine []dinv.PR, w wsdomain.Workspace, owner, name string) (pr.Ref, string) {
	if w.LinkedPR != 0 {
		return pr.Ref{Owner: owner, Repo: name, Number: w.LinkedPR}, ""
	}
	branch := w.PRBranch()
	var found []dinv.PR
	for _, p := range mine {
		if strings.EqualFold(p.Ref.Owner, owner) && strings.EqualFold(p.Ref.Repo, name) && p.HeadBranch == branch && !p.IsFork {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return pr.Ref{}, ""
	case 1:
		return found[0].Ref, ""
	}
	var open []dinv.PR
	for _, p := range found {
		if p.State == "OPEN" {
			open = append(open, p)
		}
	}
	if len(open) == 1 {
		return open[0].Ref, ""
	}
	return pr.Ref{}, fmt.Sprintf("%d of my pull requests match branch %s", len(found), branch)
}

// prBlocks are the stored start blocks of pull requests.
func prBlocks(ctx context.Context, st Store) (map[pr.Ref]dinv.Event, error) {
	all, err := st.FirstRoundBlocks(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[pr.Ref]dinv.Event, len(all))
	for _, ev := range all {
		if ref, ok := port.PROf(ev); ok {
			out[ref] = dinv.Event{PR: ref, Kind: ev.Kind, Key: ev.Key, At: ev.At, Data: ev.Data}
		}
	}
	return out, nil
}
