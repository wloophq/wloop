// Package ghsource keeps the inventory in step with GitHub: a cheap fast sweep
// that reads only what changed and a slow sweep that rebuilds the scope and
// catches what the searches no longer return.
package ghsource

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/sweep"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/scope"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace"
)

const (
	metaBudget = "github_budget"
	metaFastAt = "fast_sweep_at"
	metaSlowAt = "slow_sweep_at"
	metaScope  = "scope_resolved"

	// sinceMargin covers the lag between a pull request changing and the
	// search index returning it.
	sinceMargin     = 5 * time.Minute
	closedWindow    = 14 * 24 * time.Hour
	discoveryWindow = 90 * 24 * time.Hour
	defaultMaxPages = 10
)

type GitHub interface {
	SearchStubs(ctx context.Context, q, after string) (github.StubPage, error)
	SearchStubsFirst(ctx context.Context, qs []string, first int) ([]github.StubPage, []error, error)
	StubsByRef(ctx context.Context, refs []github.Ref) (map[github.Ref]github.Stub, map[github.Ref]error, error)
	InventoryByRef(ctx context.Context, refs []github.Ref) (map[github.Ref]github.Snapshot, map[github.Ref]error, dgh.RateLimit, error)
	Threads(ctx context.Context, refs map[github.Ref]int) (map[github.Ref][]pr.Thread, map[github.Ref]error, error)
	TeamMembers(ctx context.Context, org, slug string) ([]string, error)
	Viewer(ctx context.Context) (string, error)
	Allow(p dgh.Priority) bool
	Budget() map[dgh.Resource]dgh.Limit
	FindByBranch(ctx context.Context, queries []github.BranchQuery) (map[github.BranchQuery][]github.BranchMatch, error)
}

type Collector struct {
	Store *inventory.Store
	GH    GitHub
	WS    *workspace.Set // nil when no workspace manager is configured
	// Sink is the view sink whose columns WS reports as workspace statuses.
	Sink     string
	Cfg      config.Config
	Now      func() time.Time
	MaxPages int // per query, default 10
	Log      func(format string, args ...any)
	// Units is the daemon's unit registry and Links its link store; the fast pass's registration carries them.
	Units func() *unit.Registry
	Links port.LinkStore

	mu      sync.Mutex
	missing map[github.Ref]bool // linked pull requests GitHub did not return, until the next slow sweep
	// searched are the workspace branches the fast sweeps searched.
	searched map[branchAsk]bool
}

type Report struct {
	Queries   int
	Stubs     int
	Fetched   int // full snapshots read
	Events    int
	Failed    int // searches and reads that errored
	Truncated []string
	RateLimit dgh.Limit // the GraphQL budget once the sweep ended
	Skipped   string    // non-empty when the sweep did not run (budget)
	Warnings  []string
}

func (c *Collector) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

func (c *Collector) warn(rep *Report, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	rep.Warnings = append(rep.Warnings, msg)
	c.logf("warning: %s", msg)
}

func (c *Collector) maxPages() int {
	if c.MaxPages > 0 {
		return c.MaxPages
	}
	return defaultMaxPages
}

// Fast is one fast pass, run as the ingestion job runs it.
func (c *Collector) Fast(ctx context.Context) (Report, error) {
	var rep Report
	_, _, err := ingest.RunJob(ctx, ingest.Job{Prepare: func(ctx context.Context, now time.Time) (ingest.Plan, string, error) {
		return c.fastPlan(ctx, now, func(r Report) { rep = r })
	}}, c.Now())
	return rep, err
}

// fastRun is one fast pass between its preparation and its end.
type fastRun struct {
	rep      Report
	now      time.Time
	resolved scope.Resolved
	reg      ingest.Registration
	src      *source
	st       *sourceStore
}

// fastPlan prepares one fast pass: the two-phase pass of the GitHub source,
// then, as its After, the reads no stub can stand for: tracked and
// workspace-linked pull requests the inventory lacks. done receives the
// pass's report once it ends.
func (c *Collector) fastPlan(ctx context.Context, now time.Time, done func(Report)) (ingest.Plan, string, error) {
	r := &fastRun{now: now}
	end := func(ctx context.Context, err error) (string, string, error) {
		rep, err := c.cutShort(ctx, "fast", r.rep, err)
		done(rep)
		return Outcome(rep, err)
	}
	if err := c.prepareFast(ctx, r); err != nil || r.rep.Skipped != "" {
		_, skipped, err := end(ctx, err)
		return ingest.Plan{}, skipped, err
	}
	return ingest.Plan{Registration: r.reg, After: func(ctx context.Context, _ ingest.Report, err error) (string, string, error) {
		if err == nil {
			err = c.afterFast(ctx, r)
		}
		return end(ctx, err)
	}}, "", nil
}

func (c *Collector) prepareFast(ctx context.Context, r *fastRun) error {
	if skip := c.budgetLow(); skip != "" {
		r.rep.Skipped = skip
		c.logf("fast sweep skipped: %s", skip)
		return nil
	}
	var err error
	if r.resolved, err = c.resolved(ctx, &r.rep); err != nil {
		return err
	}
	r.reg, r.src, r.st = c.registration(r.resolved, r.now, &r.rep)
	return nil
}

func (c *Collector) afterFast(ctx context.Context, r *fastRun) error {
	rep, now, resolved := &r.rep, r.now, r.resolved
	reads, err := c.trackedReads(ctx)
	if err != nil {
		return err
	}
	list, linking := c.locals(ctx, rep)
	var index *links
	if linking {
		if index, err = c.buildLinks(ctx, list); err != nil {
			return err
		}
		for _, ref := range c.workspaceReads(list, index) {
			if !slices.Contains(reads, ref) {
				reads = append(reads, ref)
			}
		}
	}
	if len(reads) > 0 {
		r.src.unread = nil
		if _, err := ingest.Touch(ctx, r.reg, objectsOf(reads), now); err != nil {
			return err
		}
		c.noteUnreadable(r.src.unread)
		if linking {
			if index, err = c.buildLinks(ctx, list); err != nil {
				return err
			}
		}
	}
	if linking {
		read, err := c.findBranches(ctx, list, index, true, resolved, now, rep)
		if err != nil {
			return err
		}
		if read {
			if index, err = c.buildLinks(ctx, list); err != nil {
				return err
			}
		}
		if err := c.linkWorkspaces(ctx, list, index, rep); err != nil {
			return err
		}
		if err := c.readDevelopedThreads(ctx, list, index, resolved, now, rep); err != nil {
			return err
		}
	}
	return c.endFast(ctx, rep, r.st.budget)
}

// trackedReads lists the tracked pull requests the inventory lacks; the
// stored ones are watched. One GitHub did not return waits for the next
// slow sweep.
func (c *Collector) trackedReads(ctx context.Context) ([]github.Ref, error) {
	refs, err := c.Store.Tracked(ctx)
	if err != nil {
		return nil, err
	}
	stored, err := c.Store.PRs(ctx, refs)
	if err != nil {
		return nil, err
	}
	var out []github.Ref
	for _, ref := range refs {
		if _, ok := stored[ref]; !ok && !c.unreadable(ref) {
			out = append(out, ref)
		}
	}
	return out, nil
}

// Track reads a pull request the user is about to track.
func (c *Collector) Track(ctx context.Context, ref github.Ref) (port.TrackRead, error) {
	var rep Report
	resolved, err := c.resolved(ctx, &rep)
	if err != nil {
		return port.TrackRead{}, err
	}
	snaps, errs, _, err := c.GH.InventoryByRef(ctx, []github.Ref{ref})
	if err != nil {
		return port.TrackRead{}, fmt.Errorf("%w: read %s: %w", port.ErrGitHub, ref, err)
	}
	if err := errs[ref]; err != nil {
		if notFound(err) {
			return port.TrackRead{}, fmt.Errorf("%w: %v", port.ErrNotFound, err)
		}
		return port.TrackRead{}, fmt.Errorf("%w: %v", port.ErrGitHub, err)
	}
	s, ok := snaps[ref]
	if !ok {
		return port.TrackRead{}, fmt.Errorf("%w: GitHub returned no pull request %s", port.ErrNotFound, ref)
	}
	if s.Ref.Owner == "" || s.Ref.Number != ref.Number {
		s.Ref = ref
	}
	read := []github.Snapshot{s}
	if err := c.readThreads(ctx, read, resolved.Viewer, &rep); err != nil {
		return port.TrackRead{}, fmt.Errorf("%w: read %s: %w", port.ErrGitHub, ref, err)
	}
	s = read[0]
	save := func(ctx context.Context) error {
		if err := c.save(ctx, []github.Snapshot{s}, resolved, c.Now(), &rep); err != nil {
			return fmt.Errorf("store %s: %w", s.Ref, err)
		}
		return nil
	}
	return port.TrackRead{Unit: pr.Ref(s.Ref).Unit(), State: s.State, Save: save}, nil
}

// Refresh reads ref in full at the priority ctx carries, stores the read and
// returns the stored row and detail.
func (c *Collector) Refresh(ctx context.Context, ref github.Ref) (dinv.PR, inventory.Detail, error) {
	var rep Report
	resolved, err := c.resolved(ctx, &rep)
	if err != nil {
		return dinv.PR{}, inventory.Detail{}, err
	}
	snaps, errs, _, err := c.GH.InventoryByRef(ctx, []github.Ref{ref})
	if err == nil {
		err = errs[ref]
	}
	s, ok := snaps[ref]
	if err == nil && !ok {
		err = errors.New("GitHub did not return the pull request")
	}
	if err != nil {
		return dinv.PR{}, inventory.Detail{}, fmt.Errorf("read %s: %w", ref, err)
	}
	s.Ref = ref
	read := []github.Snapshot{s}
	if err := c.readThreads(ctx, read, resolved.Viewer, &rep); err != nil {
		return dinv.PR{}, inventory.Detail{}, fmt.Errorf("read %s: %w", ref, err)
	}
	if err := c.save(ctx, read, resolved, c.Now(), &rep); err != nil {
		return dinv.PR{}, inventory.Detail{}, fmt.Errorf("store %s: %w", ref, err)
	}
	rows, err := c.Store.PRs(ctx, []github.Ref{ref})
	if err != nil {
		return dinv.PR{}, inventory.Detail{}, err
	}
	details, err := c.Store.Details(ctx, []github.Ref{ref})
	if err != nil {
		return dinv.PR{}, inventory.Detail{}, err
	}
	return rows[ref], details[ref], nil
}

// notFound matches what GitHub answers for a repository or a number it has
// not, or will not show: GraphQL's NOT_FOUND message, or no pull request.
func notFound(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Could not resolve") || strings.HasSuffix(msg, ": not found")
}

func (c *Collector) Slow(ctx context.Context) (Report, error) {
	rep, err := c.slow(ctx)
	return c.cutShort(ctx, "slow", rep, err)
}

func (c *Collector) slow(ctx context.Context) (Report, error) {
	var rep Report
	if skip := c.budgetLow(); skip != "" {
		rep.Skipped = skip
		c.logf("slow sweep skipped: %s", skip)
		return rep, nil
	}
	c.forgetUnreadable()
	now := c.Now()
	resolved, errs := scope.Resolve(ctx, c.Cfg.Scope, c.refreshViewer(ctx, &rep), c.GH)
	for _, err := range errs {
		c.warn(&rep, "scope: %v", err)
	}
	discovered, failed, err := c.search(ctx, resolved.DiscoveryQueries(now.Add(-discoveryWindow)), github.SearchFirst, &rep)
	if err != nil {
		return rep, err
	}
	for _, s := range discovered {
		if s.NameWithOwner != "" {
			resolved.Repos = append(resolved.Repos, strings.ToLower(s.NameWithOwner))
		}
	}
	if len(errs) > 0 || failed > 0 {
		prev, _, err := c.storedScope(ctx, &rep)
		if err != nil {
			return rep, err
		}
		resolved = keepPrevious(resolved, prev, c.Cfg.Scope.Teams, failed > 0)
	}
	resolved.Normalize()
	b, err := json.Marshal(resolved)
	if err != nil {
		return rep, err
	}
	var noted port.Topic
	if prevTeams, err := MyTeams(ctx, c.Store); err == nil && !slices.Equal(prevTeams, resolved.MyTeams) {
		noted = port.TopicViewer
	}
	if err := c.Store.SetMetaNoting(ctx, metaScope, string(b), noted); err != nil {
		return rep, err
	}

	if err := c.refreshOpen(ctx, resolved, now, &rep); err != nil {
		return rep, err
	}
	open, err := c.Store.ListPRs(ctx, true)
	if err != nil {
		return rep, err
	}
	for _, pr := range open {
		reasons := resolved.Reasons(pr.Author, pr.Ref.Owner+"/"+pr.Ref.Repo, pr.ReviewRequests)
		if slices.Equal(reasons, slices.DeleteFunc(slices.Clone(pr.Reasons), func(r string) bool { return r == dinv.ReasonTracked })) {
			continue
		}
		if err := c.Store.SetReasons(ctx, pr.Ref, reasons); err != nil {
			return rep, err
		}
	}

	if err := c.discoverWorkspaceBranches(ctx, resolved, now, &rep); err != nil {
		return rep, err
	}

	closed, _, err := c.search(ctx, resolved.ClosedQueries(now.Add(-closedWindow)), github.SearchFirst, &rep)
	if err != nil {
		return rep, err
	}
	changed, err := c.triage(ctx, closed, now)
	if err != nil {
		return rep, err
	}
	if err := c.fetch(ctx, changed, resolved, now, &rep); err != nil {
		return rep, err
	}
	return rep, c.endRun(ctx, &rep, map[string]string{metaSlowAt: now.UTC().Format(time.RFC3339)})
}

// refreshOpen lists every open pull request of the scope as stubs, without
// the updated filter, so windows the fast sweeps lost are recovered while
// only what moved is read in full. Stored open pull requests that neither
// this listing nor the last fast sweep returned are read by reference.
func (c *Collector) refreshOpen(ctx context.Context, resolved scope.Resolved, now time.Time, rep *Report) error {
	qs := resolved.Queries(time.Time{})
	stubs, failed, err := c.search(ctx, qs, github.SearchFirst, rep)
	if err != nil {
		return err
	}
	changed, err := c.triage(ctx, stubs, now)
	if err != nil {
		return err
	}
	if err := c.fetch(ctx, changed, resolved, now, rep); err != nil {
		return err
	}
	if failed > 0 && failed == len(qs) {
		c.warn(rep, "every open search failed: stored open pull requests not refreshed")
		return nil
	}
	fastAt, err := c.metaTime(ctx, metaFastAt)
	if err != nil {
		return err
	}
	// A fast sweep running beside this one may have started after now.
	seenBy := fastAt
	if now.Before(seenBy) {
		seenBy = now
	}
	open, err := c.followedOpen(ctx, now)
	if err != nil {
		return err
	}
	seen, err := c.Store.Seen(ctx)
	if err != nil {
		return err
	}
	var unseen []github.Ref
	for _, pr := range open {
		if seen[pr.Ref].Before(seenBy) {
			unseen = append(unseen, pr.Ref)
		}
	}
	return c.fetch(ctx, unseen, resolved, now, rep)
}

// followedOpen are the stored open pull requests something follows: a scope
// reason, a track, a workspace, or a review turn sent within
// sweep.ReviewTurnsFollowed of now, as long as the sweep plans a follow-up on
// it.
func (c *Collector) followedOpen(ctx context.Context, now time.Time) ([]dinv.PR, error) {
	open, err := c.Store.ListPRs(ctx, true)
	if err != nil {
		return nil, err
	}
	reviewed, err := c.Store.Reviewed(ctx, now.Add(-sweep.ReviewTurnsFollowed))
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(open, func(p dinv.PR) bool { return !p.Followed() && !slices.Contains(reviewed, p.Ref) }), nil
}

// MyTeams are the scope teams the viewer belongs to, as the last slow sweep
// resolved them; nil before the first one or when the stored scope is
// unreadable.
func MyTeams(ctx context.Context, st *inventory.Store) ([]string, error) {
	raw, err := st.Meta(ctx, metaScope)
	if err != nil || raw == "" {
		return nil, err
	}
	var r scope.Resolved
	if json.Unmarshal([]byte(raw), &r) != nil {
		return nil, nil
	}
	return r.MyTeams, nil
}

func (c *Collector) storedScope(ctx context.Context, rep *Report) (scope.Resolved, bool, error) {
	raw, err := c.Store.Meta(ctx, metaScope)
	if err != nil || raw == "" {
		return scope.Resolved{}, false, err
	}
	var r scope.Resolved
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		c.warn(rep, "stored scope unreadable: %v", err)
		return scope.Resolved{}, false, nil
	}
	return r, true, nil
}

// keepPrevious restores from the previous scope what a failed source could
// not confirm, so a transient error does not hide pull requests.
func keepPrevious(cur, prev scope.Resolved, configured []string, discoveryFailed bool) scope.Resolved {
	if cur.Viewer == "" {
		cur.Viewer = prev.Viewer
		for _, t := range prev.MyTeams {
			if slices.Contains(cur.Teams, t) {
				cur.MyTeams = append(cur.MyTeams, t)
			}
		}
	}
	for _, t := range configured {
		t = strings.ToLower(t)
		if slices.Contains(prev.Teams, t) && !slices.Contains(cur.Teams, t) {
			cur.Teams = append(cur.Teams, t)
			cur.People = append(cur.People, prev.People...)
			if slices.Contains(prev.MyTeams, t) {
				cur.MyTeams = append(cur.MyTeams, t)
			}
		}
	}
	if discoveryFailed {
		cur.Repos = append(cur.Repos, prev.Repos...)
	}
	return cur
}

// budgetLow says why a sweep waits for the GraphQL bucket to reset; "" when
// the client admits background reads.
func (c *Collector) budgetLow() string {
	if c.GH.Allow(dgh.PriorityBackground) {
		return ""
	}
	rl := c.GH.Budget()[dgh.ResourceGraphQL]
	return fmt.Sprintf("rate limit at %d of %d, below %.0f%%, until %s", rl.Remaining, rl.Limit, c.Cfg.Sweep.RateLimitFraction*100, rl.ResetAt.UTC().Format(time.RFC3339))
}

// cutShort turns a sweep the client refused mid-run into a skip, keeping
// the budget it saw and moving no watermark.
func (c *Collector) cutShort(ctx context.Context, sweep string, rep Report, err error) (Report, error) {
	if !errors.Is(err, dgh.ErrBudget) {
		return rep, err
	}
	rep.Skipped = cmp.Or(c.budgetLow(), err.Error())
	c.logf("%s sweep cut short: %s", sweep, rep.Skipped)
	return rep, c.endRun(ctx, &rep, map[string]string{})
}

// endRun writes the run's watermarks and the client's budget in one
// transaction.
func (c *Collector) endRun(ctx context.Context, rep *Report, kv map[string]string) error {
	rep.RateLimit = c.GH.Budget()[dgh.ResourceGraphQL]
	budget, _, err := c.budgetMeta()
	if err != nil {
		return err
	}
	maps.Copy(kv, budget)
	return c.Store.SetMetas(ctx, kv)
}

// endFast stores the client's budget unless the pass's save already stored
// the same one.
func (c *Collector) endFast(ctx context.Context, rep *Report, saved string) error {
	rep.RateLimit = c.GH.Budget()[dgh.ResourceGraphQL]
	kv, budget, err := c.budgetMeta()
	if err != nil || budget == saved {
		return err
	}
	return c.Store.SetMetas(ctx, kv)
}

// StoredBudget is the GitHub budget the last sweep kept, for the client's
// Restore; nil when none was kept or it is unreadable.
func StoredBudget(ctx context.Context, st *inventory.Store) ([]dgh.Limit, error) {
	raw, err := st.Meta(ctx, metaBudget)
	if err != nil || raw == "" {
		return nil, err
	}
	var limits []dgh.Limit
	if json.Unmarshal([]byte(raw), &limits) != nil {
		return nil, nil
	}
	return limits, nil
}

func (c *Collector) metaTime(ctx context.Context, key string) (time.Time, error) {
	return metaTime(ctx, c.Store, key)
}

func metaTime(ctx context.Context, st *inventory.Store, key string) (time.Time, error) {
	v, err := st.Meta(ctx, key)
	if err != nil || v == "" {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("meta %s: %w", key, err)
	}
	return t, nil
}

func (c *Collector) viewer(ctx context.Context, rep *Report) string {
	if v, err := c.Store.Viewer(ctx, unit.GitHub); err == nil && v != "" {
		return v
	}
	return c.refreshViewer(ctx, rep)
}

// refreshViewer asks GitHub for the login and stores it; on an error the
// stored login stays.
func (c *Collector) refreshViewer(ctx context.Context, rep *Report) string {
	v, err := c.GH.Viewer(ctx)
	if err != nil {
		c.warn(rep, "viewer: %v", err)
		v, _ = c.Store.Viewer(ctx, unit.GitHub)
		return v
	}
	if err := c.Store.SetViewer(ctx, unit.GitHub, v); err != nil {
		c.warn(rep, "viewer: %v", err)
	}
	return v
}

func (c *Collector) resolved(ctx context.Context, rep *Report) (scope.Resolved, error) {
	if r, ok, err := c.storedScope(ctx, rep); err != nil || ok {
		return r, err
	}
	r, errs := scope.Resolve(ctx, c.Cfg.Scope, c.viewer(ctx, rep), c.GH)
	for _, err := range errs {
		c.warn(rep, "scope: %v", err)
	}
	return r, nil
}

// searchBatch caps the searches sent in one request.
const searchBatch = 10

// search reads the first page, of first stubs, of every query in batched
// requests and the next pages one by one, up to MaxPages. A failing query is
// a warning; failed counts them so the caller can tell when nothing answered.
func (c *Collector) search(ctx context.Context, qs []scope.Query, first int, rep *Report) (stubs []github.Stub, failed int, err error) {
	for batch := range slices.Chunk(qs, searchBatch) {
		texts := make([]string, len(batch))
		for i, q := range batch {
			texts[i] = q.Q
		}
		firsts, errs, err := c.GH.SearchStubsFirst(ctx, texts, first)
		if err != nil {
			if ctx.Err() != nil {
				return nil, failed, ctx.Err()
			}
			if errors.Is(err, dgh.ErrBudget) {
				return nil, failed, err
			}
			firsts, errs = make([]github.StubPage, len(batch)), make([]error, len(batch))
			for i := range errs {
				errs[i] = err
			}
		}
		for i, q := range batch {
			rep.Queries++
			if errors.Is(errs[i], dgh.ErrBudget) {
				return nil, failed, errs[i]
			}
			if errs[i] != nil {
				failed++
				rep.Failed++
				c.warn(rep, "search %q: %v", q.Q, errs[i])
				continue
			}
			found, hasNext, err := c.searchPages(ctx, q, firsts[i], rep)
			if err != nil {
				return nil, failed, err
			}
			if hasNext {
				rep.Truncated = append(rep.Truncated, q.Q)
				c.logf("search truncated at %d pages: %s", c.maxPages(), q.Q)
			}
			rep.Stubs += len(found)
			stubs = append(stubs, found...)
		}
	}
	return stubs, failed, nil
}

// searchPages follows a query past its first page; a failed next page keeps
// what the earlier pages returned.
func (c *Collector) searchPages(ctx context.Context, q scope.Query, p github.StubPage, rep *Report) ([]github.Stub, bool, error) {
	stubs := p.Stubs
	hasNext, after := p.HasNext, p.EndCursor
	for page := 1; hasNext && page < c.maxPages(); page++ {
		next, err := c.GH.SearchStubs(ctx, q.Q, after)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			if errors.Is(err, dgh.ErrBudget) {
				return nil, false, err
			}
			rep.Failed++
			c.warn(rep, "search %q: %v", q.Q, err)
			return stubs, false, nil
		}
		stubs = append(stubs, next.Stubs...)
		hasNext, after = next.HasNext, next.EndCursor
	}
	return stubs, hasNext, nil
}

// triage returns the refs whose stored snapshot is absent, older than the
// stub, on another checks state or with other failed checks, and touches
// the others, which also stores a mergeable state the stub knows.
func (c *Collector) triage(ctx context.Context, stubs []github.Stub, now time.Time) ([]github.Ref, error) {
	stored, err := c.Store.PRs(ctx, stubRefs(stubs))
	if err != nil {
		return nil, err
	}
	var changed []github.Ref
	var unchanged []github.Stub
	seen := map[github.Ref]bool{}
	for _, s := range stubs {
		if seen[s.Ref] {
			continue
		}
		seen[s.Ref] = true
		pr, ok := stored[s.Ref]
		if !ok || pr.UpdatedAt.Before(s.UpdatedAt) || pr.ChecksState != s.ChecksState || !sameFailed(pr.FailingChecks, s.FailedChecks) {
			changed = append(changed, s.Ref)
			continue
		}
		unchanged = append(unchanged, s)
	}
	return changed, c.Store.TouchStubs(ctx, unchanged, now)
}

func stubRefs(stubs []github.Stub) []github.Ref {
	refs := make([]github.Ref, len(stubs))
	for i, s := range stubs {
		refs[i] = s.Ref
	}
	return refs
}

func sameFailed(stored []dinv.FailingCheck, names []string) bool {
	if len(stored) != len(names) {
		return false
	}
	have := make([]string, len(stored))
	for i, c := range stored {
		have[i] = c.Name
	}
	want := make([]string, len(names))
	for i, n := range names {
		want[i] = failedName(n)
	}
	slices.Sort(have)
	slices.Sort(want)
	return slices.Equal(have, want)
}

func failedName(name string) string { return termfmt.OneLine(name, 100) }

func (c *Collector) fetch(ctx context.Context, refs []github.Ref, resolved scope.Resolved, now time.Time, rep *Report) error {
	_, err := c.read(ctx, refs, resolved, now, rep)
	return err
}

// read stores what GitHub returned of refs and lists those it did not.
func (c *Collector) read(ctx context.Context, refs []github.Ref, resolved scope.Resolved, now time.Time, rep *Report) (unread []github.Ref, err error) {
	if len(refs) == 0 {
		return nil, nil
	}
	snaps, errs, _, err := c.GH.InventoryByRef(ctx, refs)
	if err != nil {
		return nil, fmt.Errorf("read pull requests: %w", err)
	}
	var got []github.Snapshot
	taken := map[github.Ref]bool{}
	for _, ref := range refs {
		if err := errs[ref]; err != nil {
			rep.Failed++
			c.warn(rep, "read %s: %v", ref, err)
			unread = append(unread, ref)
			continue
		}
		s, ok := snaps[ref]
		if !ok {
			rep.Failed++
			c.warn(rep, "read %s: not returned", ref)
			unread = append(unread, ref)
			continue
		}
		if taken[ref] {
			continue
		}
		taken[ref] = true
		s.Ref = ref
		got = append(got, s)
	}
	if err := c.readThreads(ctx, got, resolved.Viewer, rep); err != nil {
		return unread, err
	}
	if err := c.save(ctx, got, resolved, now, rep); err != nil {
		return unread, fmt.Errorf("store pull requests: %w", err)
	}
	return unread, nil
}

// save stores snaps in one write transaction, after reading the stored facts
// they need.
func (c *Collector) save(ctx context.Context, snaps []github.Snapshot, resolved scope.Resolved, now time.Time, rep *Report) error {
	if len(snaps) == 0 {
		return nil
	}
	refs := make([]github.Ref, len(snaps))
	for i, s := range snaps {
		refs[i] = s.Ref
	}
	facts, err := c.Store.StoredFacts(ctx, refs)
	if err != nil {
		return err
	}
	batch := make([]inventory.Snapshot, len(snaps))
	for i, s := range snaps {
		f := facts[s.Ref]
		evs := EventsFrom(s, f.PR.ChecksState, now)
		clampPushes(evs, f, now)
		batch[i] = inventory.Snapshot{PR: snapshotPR(s, resolved), Events: evs, Merges: merges(evs, f), Detail: detailOf(s, now)}
		if s.TimelineMissed > 0 {
			c.logf("%s: the timeline read left out its %d earliest events", s.Ref, s.TimelineMissed)
		}
	}
	if err := c.keepThreads(ctx, snaps, batch, resolved.Viewer, now); err != nil {
		return err
	}
	added, err := c.Store.SaveSnapshots(ctx, batch, now)
	if err != nil {
		return err
	}
	rep.Fetched += len(snaps)
	for _, n := range added {
		rep.Events += n
	}
	return nil
}

// threadsWanted are the snapshots with threads whose open threads a full
// read follows with a thread read: those the viewer authored and those a
// workspace that is not a review workspace links, which the viewer develops.
func (c *Collector) threadsWanted(ctx context.Context, snaps []github.Snapshot, viewer string) (map[github.Ref]bool, error) {
	want := map[github.Ref]bool{}
	var others []github.Ref
	for _, s := range snaps {
		switch {
		case s.ThreadsTotal == 0:
		case viewer != "" && strings.EqualFold(s.Author, viewer):
			want[s.Ref] = true
		default:
			others = append(others, s.Ref)
		}
	}
	if len(others) == 0 {
		return want, nil
	}
	stored, err := c.Store.PRs(ctx, others)
	if err != nil {
		return nil, err
	}
	for ref, p := range stored {
		if p.Workspace.ID != "" && !p.Workspace.IsReview {
			want[ref] = true
		}
	}
	return want, nil
}

// readThreads reads the open threads of the snapshots threadsWanted names,
// in one request per 20; a thread read that fails is a warning and leaves
// that snapshot's threads unread.
func (c *Collector) readThreads(ctx context.Context, snaps []github.Snapshot, viewer string, rep *Report) error {
	wanted, err := c.threadsWanted(ctx, snaps, viewer)
	if err != nil {
		return err
	}
	want := map[github.Ref]int{}
	for i := range snaps {
		s := &snaps[i]
		s.ThreadsRead = s.ThreadsTotal == 0
		if wanted[s.Ref] {
			want[s.Ref] = min(s.ThreadsTotal, github.MaxThreads)
		}
	}
	if len(want) == 0 {
		return nil
	}
	threads, errs, err := c.GH.Threads(ctx, want)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, dgh.ErrBudget) {
			return err
		}
		rep.Failed++
		c.warn(rep, "review threads: %v", err)
	}
	for i := range snaps {
		s := &snaps[i]
		if _, asked := want[s.Ref]; !asked {
			continue
		}
		if err := errs[s.Ref]; err != nil {
			rep.Failed++
			c.warn(rep, "review threads %s: %v", s.Ref, err)
			continue
		}
		if open, ok := threads[s.Ref]; ok {
			s.OpenThreads, s.ThreadsRead = open, true
		}
	}
	return nil
}

// keepThreads gives each detail whose thread read failed the stored threads
// and read time, so the next fast run reads it again; one never stored is
// dated stale.
func (c *Collector) keepThreads(ctx context.Context, snaps []github.Snapshot, batch []inventory.Snapshot, viewer string, now time.Time) error {
	unread := slices.DeleteFunc(slices.Clone(snaps), func(s github.Snapshot) bool { return s.ThreadsRead })
	if len(unread) == 0 {
		return nil
	}
	wanted, err := c.threadsWanted(ctx, unread, viewer)
	if err != nil {
		return err
	}
	var failed []github.Ref
	for _, s := range unread {
		if wanted[s.Ref] {
			failed = append(failed, s.Ref)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	stored, err := c.Store.Details(ctx, failed)
	if err != nil {
		return err
	}
	for i := range batch {
		d := batch[i].Detail
		if !slices.Contains(failed, batch[i].PR.Ref) {
			continue
		}
		was, ok := stored[batch[i].PR.Ref]
		if !ok {
			d.ReadAt = now.Add(-c.DetailMaxAge())
			continue
		}
		d.OpenThreads, d.ThreadsRead, d.ReadAt = was.OpenThreads, was.ThreadsRead, was.ReadAt
	}
	return nil
}

func detailOf(s github.Snapshot, at time.Time) *inventory.Detail {
	return &inventory.Detail{
		Version: inventory.DetailVersion, ReadAt: at,
		AuthorAssociation: s.AuthorAssociation, Visibility: s.Visibility, Labels: s.Labels, Commits: s.Commits,
		Reviews: s.Reviews, LatestReviews: s.LatestReviews, NewestChangesRequestOid: s.NewestChangesRequestOid,
		Comments: s.Comments, Checks: s.Checks,
		ThreadsTotal: s.ThreadsTotal, OpenThreads: s.OpenThreads, ThreadsRead: s.ThreadsRead,
	}
}

func snapshotPR(s github.Snapshot, resolved scope.Resolved) dinv.PR {
	url := s.URL
	if url == "" {
		url = s.Ref.URL()
	}
	requests := s.PullRequest.ReviewRequests
	pr := dinv.PR{
		Ref: s.Ref, Title: s.Title, Author: s.Author, State: s.State, IsDraft: s.IsDraft,
		HeadOid: s.HeadRefOid, HeadBranch: s.HeadRefName, BaseBranch: s.BaseRefName, IsFork: s.IsCrossRepository,
		ChecksState: s.ChecksState, ReviewDecision: s.ReviewDecision, ReviewRequests: requests, URL: url, Mergeable: s.Mergeable,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, MergedAt: s.MergedAt, ClosedAt: s.ClosedAt,
		BodyEditedAt: s.LastEditedAt, ChangedFiles: s.ChangedFiles,
		Reasons: resolved.Reasons(s.Author, s.Ref.Owner+"/"+s.Ref.Repo, requests),
	}
	for _, ch := range s.Checks {
		if !ch.Failed {
			continue
		}
		// 200 is situation.ReasonMax, which collect does not import.
		fc := dinv.FailingCheck{Name: failedName(ch.Name), Summary: termfmt.OneLine(ch.Summary, 200)}
		// Some apps (Cloud Build) title every run "Summary".
		if strings.EqualFold(fc.Summary, "summary") {
			fc.Summary = ""
		}
		if strings.HasPrefix(ch.URL, "https://") {
			fc.URL = ch.URL
		}
		pr.FailingChecks = append(pr.FailingChecks, fc)
	}
	return pr
}

// merges are the merged events of evs that fill a stored one lacking the
// merger or the commit.
func merges(evs []dinv.Event, f inventory.StoredFacts) []dinv.Event {
	var out []dinv.Event
	for _, at := range f.UnfilledMerges {
		for _, e := range evs {
			if e.Kind == dinv.KindMerged && e.At.Equal(at) {
				out = append(out, e)
			}
		}
	}
	return out
}
