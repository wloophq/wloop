package collect

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/scope"
	"github.com/wloophq/wloop/plugins/github/store"
)

var now = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

// memHost is the host's event log in memory: a keyed event is kept once per
// unit, kind and key, an unkeyed one once per unit, kind and time, where the
// kept one gains the actor and the data it lacks.
type memHost struct {
	mu      sync.Mutex
	events  []src.Event
	records int // RecordEvents calls
}

func (h *memHost) RecordEvents(_ context.Context, evs []src.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records++
	for _, e := range evs {
		i := slices.IndexFunc(h.events, func(x src.Event) bool {
			return x.Unit == e.Unit && x.Kind == e.Kind && x.Key == e.Key && (e.Key != "" || x.At.Equal(e.At))
		})
		switch {
		case i < 0:
			h.events = append(h.events, e)
		case e.Key == "":
			kept := &h.events[i]
			if kept.Actor == "" {
				kept.Actor = e.Actor
			}
			if len(kept.Data) == 0 {
				kept.Data = e.Data
			}
		}
	}
	return nil
}

func (h *memHost) Events(_ context.Context, q src.EventQuery) ([]src.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []src.Event
	for _, e := range h.events {
		if slices.Contains(q.Units, e.Unit) && (len(q.Kinds) == 0 || slices.Contains(q.Kinds, e.Kind)) && !e.At.Before(q.Since) {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b src.Event) int { return a.At.Compare(b.At) })
	return out, nil
}

func (h *memHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

func (h *memHost) recorded() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

// openStore is a fresh plugin store over an in-memory host, dating the
// scope flips no write dates with clock.
func openStore(t *testing.T, clock func() time.Time) (*store.DB, *memHost) {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &memHost{}
	db.UseHost(h)
	db.UseClock(clock)
	hosts.Store(db, h)
	t.Cleanup(func() { hosts.Delete(db) })
	return db, h
}

// changes records the pull requests the store's commits changed.
type changes struct {
	mu   sync.Mutex
	refs []pr.Ref
}

func listen(db *store.DB) *changes {
	c := &changes{}
	db.OnChange(func(ch store.Change) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.refs = append(c.refs, ch.Refs...)
	})
	return c
}

// since are the refs changed after mark, sorted and unique.
func (c *changes) since(mark int) []pr.Ref {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := slices.Clone(c.refs[mark:])
	slices.SortFunc(out, pr.Compare)
	return slices.Compact(out)
}

func (c *changes) mark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.refs)
}

// testConfig is the configuration the tests run under: the default sweep
// and review prefix, a needs-me column and a scope of ana and acme/search.
func testConfig() Config {
	return Config{
		Scope:             scope.Config{People: []string{"ana"}, Teams: []string{"acme/search"}},
		ReviewPrefix:      "REVIEW ",
		RateLimitFraction: 0.5,
		Slow:              time.Hour,
		NeedsMe:           []string{"needs-me"},
	}
}

func configured(cfg Config) func() Config { return func() Config { return cfg } }

// listings are the host's workspaces as a test sets them.
type listings struct {
	mu      sync.Mutex
	listing src.Listing
	err     error
}

func (l *listings) Workspaces(context.Context, string) (src.Listing, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.listing
	out.Workspaces = slices.Clone(out.Workspaces)
	return out, l.err
}

func (l *listings) edit(f func(*src.Listing)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f(&l.listing)
}

// acmeAPI lists wss as local workspaces of acme/api, repository r1.
func acmeAPI(wss ...src.Workspace) *listings {
	return &listings{listing: src.Listing{Repos: []src.Repo{{ID: "r1", Remote: "github.com/acme/api"}}, Workspaces: localWorkspaces(wss)}}
}

func localWorkspaces(wss []src.Workspace) []src.Workspace {
	out := make([]src.Workspace, len(wss))
	for i, w := range wss {
		out[i] = onAcmeAPI(w)
	}
	return out
}

func onAcmeAPI(w src.Workspace) src.Workspace {
	w.Repo, w.RepoID, w.Local = cmp.Or(w.Repo, "acme/api"), cmp.Or(w.RepoID, "r1"), true
	return w
}

// linkedTo is the manager's link of a workspace to acme/api#n.
func linkedTo(n int) []sdk.Unit {
	return []sdk.Unit{api.Ref{Owner: "acme", Repo: "api", Number: n}.Unit()}
}

type fakeGH struct {
	mu          sync.Mutex
	pages       map[string][]api.StubPage // by query substring, each query served them in order
	served      map[string]int
	failing     string               // query substring that errors
	batches     int                  // SearchStubsFirst calls
	firsts      []int                // the first page size of each SearchStubsFirst call
	stubs       map[api.Ref]api.Stub // StubsByRef answers, over stubOf(byRef)
	stubRefs    [][]api.Ref          // each StubsByRef call's refs
	byRef       map[api.Ref]api.Snapshot
	remaining   int
	resetAt     time.Time
	now         func() time.Time
	calls       []string
	byRefCalls  int
	viewer      string
	viewerCalls int
	members     []string
	noLimit     bool
	honorSince  bool             // drop stubs older than the query's updated:>= qualifier
	closed      map[api.Ref]bool // stubs an is:open query does not return
	teamErr     error
	byBranch    map[api.BranchQuery][]api.BranchMatch
	branchQs    []api.BranchQuery
	read        []api.Ref
	refErrs     map[api.Ref]error
	byRefErr    error
	searchErr   error // returned by every search
	threadReads []map[api.Ref]int
}

func (f *fakeGH) FindByBranch(_ context.Context, qs []api.BranchQuery) (map[api.BranchQuery][]api.BranchMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchQs = append(f.branchQs, qs...)
	out := map[api.BranchQuery][]api.BranchMatch{}
	for _, q := range qs {
		out[q] = f.byBranch[q]
	}
	return out, nil
}

func (f *fakeGH) rl() api.RateLimit {
	if f.noLimit {
		return api.RateLimit{Remaining: f.remaining, ResetAt: f.resetAt}
	}
	return api.RateLimit{Remaining: f.remaining, Limit: 5000, ResetAt: f.resetAt}
}

func (f *fakeGH) StubsByRef(_ context.Context, refs []api.Ref) (map[api.Ref]api.Stub, map[api.Ref]error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stubRefs = append(f.stubRefs, refs)
	if f.byRefErr != nil {
		return nil, nil, f.byRefErr
	}
	out, errs := map[api.Ref]api.Stub{}, map[api.Ref]error{}
	for _, r := range refs {
		if err := f.refErrs[r]; err != nil {
			errs[r] = err
			continue
		}
		if st, ok := f.stubs[r]; ok {
			out[r] = st
		} else if s, ok := f.byRef[r]; ok {
			out[r] = stubOf(s)
		}
	}
	return out, errs, nil
}

func (f *fakeGH) SearchStubsFirst(_ context.Context, qs []string, first int) ([]api.StubPage, []error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches++
	f.firsts = append(f.firsts, first)
	pages, errs := make([]api.StubPage, len(qs)), make([]error, len(qs))
	for i, q := range qs {
		pages[i], errs[i] = f.search(q, "")
	}
	return pages, errs, nil
}

func (f *fakeGH) SearchStubs(_ context.Context, q, after string) (api.StubPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.search(q, after)
}

func (f *fakeGH) search(q, after string) (api.StubPage, error) {
	f.calls = append(f.calls, q+"|"+after)
	if f.searchErr != nil {
		return api.StubPage{}, f.searchErr
	}
	if f.failing != "" && strings.Contains(q, f.failing) {
		return api.StubPage{}, errors.New("validation failed")
	}
	if f.served == nil {
		f.served = map[string]int{}
	}
	for sub, pages := range f.pages {
		if !strings.Contains(q, sub) {
			continue
		}
		i := f.served[q]
		if i >= len(pages) {
			return api.StubPage{RateLimit: f.rl()}, nil
		}
		f.served[q]++
		p := pages[i]
		p.RateLimit = f.rl()
		if strings.Contains(q, "is:open") {
			p.Stubs = slices.DeleteFunc(slices.Clone(p.Stubs), func(s api.Stub) bool { return f.closed[s.Ref] })
		}
		if _, v, ok := strings.Cut(q, "updated:>="); ok && f.honorSince {
			since, _ := time.Parse(time.RFC3339, strings.Fields(v)[0])
			p.Stubs = slices.DeleteFunc(slices.Clone(p.Stubs), func(s api.Stub) bool { return s.UpdatedAt.Before(since) })
		}
		return p, nil
	}
	return api.StubPage{RateLimit: f.rl()}, nil
}

func (f *fakeGH) InventoryByRef(_ context.Context, refs []api.Ref) (map[api.Ref]api.Snapshot, map[api.Ref]error, api.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byRefCalls++
	f.read = append(f.read, refs...)
	if f.byRefErr != nil {
		return nil, nil, api.RateLimit{}, f.byRefErr
	}
	out := map[api.Ref]api.Snapshot{}
	for _, r := range refs {
		if s, ok := f.byRef[r]; ok {
			out[r] = s
		}
	}
	return out, f.refErrs, f.rl(), nil
}

func (f *fakeGH) Threads(_ context.Context, refs map[api.Ref]int) (map[api.Ref][]pr.Thread, map[api.Ref]error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadReads = append(f.threadReads, refs)
	return map[api.Ref][]pr.Thread{}, map[api.Ref]error{}, nil
}

func (f *fakeGH) TeamMembers(context.Context, string, string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.teamErr != nil {
		return nil, f.teamErr
	}
	if f.members != nil {
		return f.members, nil
	}
	return []string{"dylan"}, nil
}

func (f *fakeGH) Viewer(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewerCalls++
	return cmp.Or(f.viewer, "me"), nil
}

// Budget is the GraphQL bucket the fake reports, live until resetAt.
func (f *fakeGH) Budget() map[api.Resource]api.Limit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.budget()
}

func (f *fakeGH) budget() map[api.Resource]api.Limit {
	at := now
	if f.now != nil {
		at = f.now()
	}
	if f.noLimit || !at.Before(f.resetAt) {
		return map[api.Resource]api.Limit{}
	}
	return map[api.Resource]api.Limit{api.ResourceGraphQL: {Resource: api.ResourceGraphQL, Limit: 5000, Remaining: f.remaining, ResetAt: f.resetAt}}
}

func (f *fakeGH) Allow(p api.Priority) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.budget()[api.ResourceGraphQL]
	floor := api.DefaultFloors(0.5).Background
	if p == api.PriorityUser {
		floor = api.DefaultFloors(0.5).User
	}
	return !ok || float64(l.Remaining) >= floor*float64(l.Limit)
}

func stubOf(s api.Snapshot) api.Stub {
	st := api.Stub{Ref: s.Ref, Author: s.Author, UpdatedAt: s.UpdatedAt, IsDraft: s.IsDraft, HeadOid: s.HeadRefOid, ReviewDecision: s.ReviewDecision,
		ReviewRequests: s.PullRequest.ReviewRequests, NameWithOwner: s.Owner + "/" + s.Repo, ChecksState: s.ChecksState, State: s.State, Mergeable: s.Mergeable}
	for _, c := range s.Checks {
		if c.Failed {
			st.FailedChecks = append(st.FailedChecks, c.Name)
		}
	}
	slices.Sort(st.FailedChecks)
	return st
}

// newCollector is a collector over gh and a fresh store, under testConfig,
// at now, reading at most two pages per query.
func newCollector(t *testing.T, gh *fakeGH) *Collector {
	t.Helper()
	c := &Collector{GH: gh, Cfg: configured(testConfig()), Now: func() time.Time { return now }, MaxPages: 2, Log: t.Logf}
	c.Store, _ = openStore(t, func() time.Time { return c.Now() })
	return c
}

// hosts are the memory hosts of the stores openStore opened.
var hosts sync.Map

// eventsOf are the host's events of ref, oldest first.
func eventsOf(ctx context.Context, c *Collector, ref api.Ref) ([]pr.Event, error) {
	return c.Store.HostEvents(ctx, []pr.Ref{ref}, time.Time{})
}

func setTracked(ctx context.Context, db *store.DB, ref api.Ref) error {
	_, err := db.SetTracked(ctx, ref, true)
	return err
}

// heard are the topics c tells the host changed.
func heard(c *Collector) *[]sdk.Topic {
	var topics []sdk.Topic
	c.Changed = func(_ context.Context, ts ...sdk.Topic) { topics = append(topics, ts...) }
	return &topics
}

// fixture is a store seeded with open and closed pull requests of a few
// repositories, as a live store holds them.
type fixture struct {
	Store        *store.DB
	Host         *memHost
	Changes      *changes
	Open, Closed []api.Ref
	Now          time.Time
}

func seeded(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{Now: now}
	f.Store, f.Host = openStore(t, func() time.Time { return f.Now })
	ctx := context.Background()
	for i := range 160 {
		ref := api.Ref{Owner: "acme", Repo: []string{"api", "web", "search"}[i%3], Number: 1000 + i}
		p := pr.PR{Ref: ref, Title: fmt.Sprintf("Change %d", i), Author: []string{"ana", "bruno", "me"}[i%3],
			HeadOid: fmt.Sprintf("h%d", i), HeadBranch: fmt.Sprintf("feat/%d", i), BaseBranch: "main", ChecksState: "SUCCESS",
			CreatedAt: now.Add(-time.Duration(30+i%60) * 24 * time.Hour), UpdatedAt: now.Add(-time.Duration(i) * time.Hour),
			Reasons: []string{"author"}, LastSeenAt: now.Add(-time.Hour)}
		if i%7 == 0 {
			p.ChecksState, p.FailingChecks = "FAILURE", []pr.FailingCheck{{Name: "lint"}}
		}
		if i < 100 {
			p.State = api.StateOpen
			f.Open = append(f.Open, ref)
		} else {
			p.State, p.MergedAt = "MERGED", p.UpdatedAt
			f.Closed = append(f.Closed, ref)
		}
		if err := f.Store.UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	f.Changes = listen(f.Store)
	return f
}
