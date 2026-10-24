package ghsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/scope"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type hostedPR struct {
	ref       github.Ref
	author    string
	state     string
	draft     bool
	updated   time.Time
	head      string
	checks    string
	failed    []string
	mergeable string
	requests  []string
	threads   int // review threads, every other one resolved
}

// hosted answers the GraphQL documents github.Client sends for searches,
// by-ref stubs and full reads from its pull requests, each request at the
// one point measured for a discovery alias set, 40 stubs or 20 full reads.
type hosted struct {
	mu          sync.Mutex
	prs         map[github.Ref]*hostedPR
	searches    int // requests holding a search
	stubs       []int
	reads       []github.Ref
	readReqs    int      // requests holding a full read
	threads     []int    // aliases per review-thread request
	failThreads bool     // answer no pull request to thread reads
	heads       []string // the head branches searched, in order
}

var (
	searchRE = regexp.MustCompile(`(?:(\w+): )?search\(query: ("(?:[^"\\]|\\.)*"), type: ISSUE, first: (\d+)(?:, after: ("(?:[^"\\]|\\.)*"|null))?\)`)
	byRefRE  = regexp.MustCompile(`(pr\d+): repository\(owner: ("[^"]*"), name: ("[^"]*")\) \{ pullRequest\(number: (\d+)\) \{ \.\.\.(\w+) \} \}`)
	threadRE = regexp.MustCompile(`(pr\d+): repository\(owner: ("[^"]*"), name: ("[^"]*")\) \{ pullRequest\(number: (\d+)\) \{ reviewThreads\(last: (\d+)\)`)
)

func (h *hosted) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload struct {
		Query string `json:"query"`
	}
	raw, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	data := map[string]any{"rateLimit": map[string]any{"cost": 1, "remaining": 4900, "limit": 5000, "resetAt": now.Add(time.Hour)}}
	if ms := searchRE.FindAllStringSubmatch(payload.Query, -1); len(ms) > 0 {
		h.searches++
		for _, m := range ms {
			alias := m[1]
			if alias == "" {
				alias = "search"
			}
			var q, after string
			json.Unmarshal([]byte(m[2]), &q)
			if m[4] != "" && m[4] != "null" {
				json.Unmarshal([]byte(m[4]), &after)
			}
			first, _ := strconv.Atoi(m[3])
			data[alias] = h.search(q, first, after)
		}
	}
	if ms := threadRE.FindAllStringSubmatch(payload.Query, -1); len(ms) > 0 {
		h.threads = append(h.threads, len(ms))
		for _, m := range ms {
			var ref github.Ref
			json.Unmarshal([]byte(m[2]), &ref.Owner)
			json.Unmarshal([]byte(m[3]), &ref.Repo)
			ref.Number, _ = strconv.Atoi(m[4])
			last, _ := strconv.Atoi(m[5])
			var node any
			if p, ok := h.prs[ref]; ok && !h.failThreads {
				node = map[string]any{"reviewThreads": p.threadNodes(last)}
			}
			data[m[1]] = map[string]any{"pullRequest": node}
		}
	}
	stubs, full := 0, 0
	for _, m := range byRefRE.FindAllStringSubmatch(payload.Query, -1) {
		var ref github.Ref
		json.Unmarshal([]byte(m[2]), &ref.Owner)
		json.Unmarshal([]byte(m[3]), &ref.Repo)
		ref.Number, _ = strconv.Atoi(m[4])
		if m[5] == "stub" {
			stubs++
		} else {
			full++
			h.reads = append(h.reads, ref)
		}
		var node any
		if p, ok := h.prs[ref]; ok {
			node = p.node()
		}
		data[m[1]] = map[string]any{"pullRequest": node}
	}
	if stubs > 0 {
		h.stubs = append(h.stubs, stubs)
	}
	if full > 0 {
		h.readReqs++
	}
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

// search matches the qualifiers the scope's queries use: is:open, is:closed,
// updated:>=, and author:, user-review-requested: and repo: terms, ORed per
// qualifier.
func (h *hosted) search(q string, first int, after string) map[string]any {
	var since time.Time
	want := map[string][]string{}
	var state string
	for _, term := range strings.Fields(q) {
		k, v, _ := strings.Cut(term, ":")
		switch {
		case term == "is:open":
			state = dgh.StateOpen
		case term == "is:closed":
			state = "closed"
		case k == "updated":
			since, _ = time.Parse(time.RFC3339, strings.TrimPrefix(v, ">="))
		case k == "author" || k == "user-review-requested" || k == "repo" || k == "head":
			want[k] = append(want[k], v)
			if k == "head" {
				h.heads = append(h.heads, v)
			}
		}
	}
	var hits []*hostedPR
	for _, p := range h.prs {
		switch {
		case state == dgh.StateOpen && p.state != dgh.StateOpen,
			state == "closed" && p.state == dgh.StateOpen,
			p.updated.Before(since),
			want["author"] != nil && !slices.Contains(want["author"], p.author),
			want["repo"] != nil && !slices.Contains(want["repo"], p.ref.Owner+"/"+p.ref.Repo),
			want["head"] != nil && !slices.Contains(want["head"], p.branch()),
			want["user-review-requested"] != nil && !slices.ContainsFunc(p.requests, func(r string) bool { return slices.Contains(want["user-review-requested"], r) }):
			continue
		}
		hits = append(hits, p)
	}
	slices.SortFunc(hits, func(a, b *hostedPR) int { return b.ref.Number - a.ref.Number })
	off, _ := strconv.Atoi(after)
	end := min(off+first, len(hits))
	nodes := []any{}
	for _, p := range hits[min(off, len(hits)):end] {
		nodes = append(nodes, p.node())
	}
	next := end < len(hits)
	cursor := any(nil)
	if next {
		cursor = strconv.Itoa(end)
	}
	return map[string]any{"pageInfo": map[string]any{"hasNextPage": next, "endCursor": cursor}, "nodes": nodes}
}

func (p *hostedPR) node() map[string]any {
	var requests []any
	for _, r := range p.requests {
		requests = append(requests, map[string]any{"requestedReviewer": map[string]any{"__typename": "User", "login": r}})
	}
	var rollup any
	if p.checks != "" {
		var contexts []any
		for _, f := range p.failed {
			contexts = append(contexts, map[string]any{"name": f, "status": "COMPLETED", "conclusion": "FAILURE"})
		}
		rollup = map[string]any{"state": p.checks, "contexts": map[string]any{"nodes": contexts}}
	}
	return map[string]any{
		"number": p.ref.Number, "state": p.state, "isDraft": p.draft, "updatedAt": p.updated, "createdAt": p.updated.Add(-time.Hour),
		"title": fmt.Sprintf("change %d", p.ref.Number), "url": p.ref.URL(), "mergeable": p.mergeable,
		"headRefOid": p.head, "headRefName": p.branch(), "baseRefName": "main",
		"repository":     map[string]any{"nameWithOwner": p.ref.Owner + "/" + p.ref.Repo},
		"author":         map[string]any{"__typename": "User", "login": p.author},
		"reviewRequests": map[string]any{"nodes": requests},
		"headCommit":     map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"oid": p.head, "statusCheckRollup": rollup}}}},
		"reviewThreads":  map[string]any{"totalCount": p.threads},
	}
}

func (p *hostedPR) branch() string { return fmt.Sprintf("b%d", p.ref.Number) }

func (p *hostedPR) threadNodes(last int) map[string]any {
	nodes := []any{}
	for i := max(0, p.threads-last); i < p.threads; i++ {
		comment := map[string]any{"createdAt": p.updated.Add(-time.Duration(i) * time.Minute), "author": map[string]any{"__typename": "User", "login": "ana"}}
		nodes = append(nodes, map[string]any{"isResolved": i%2 == 1, "comments": map[string]any{"nodes": []any{comment}}})
	}
	return map[string]any{"totalCount": p.threads, "nodes": nodes}
}

func (h *hosted) add(p hostedPR) *hostedPR {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prs[p.ref] = &p
	return &p
}

func (h *hosted) edit(ref github.Ref, f func(*hostedPR)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(h.prs[ref])
}

func (h *hosted) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.searches, h.stubs, h.reads, h.readReqs, h.threads, h.heads = 0, nil, nil, 0, nil, nil
}

// live is the store and GitHub at live sizes once a first fast run read
// them: 69 open followed pull requests, of which 23 outside the scope and
// linked to a workspace and 7 tracked, under a viewer-only scope.
type live struct {
	gh                               *hosted
	c                                *Collector
	log                              *change.Log
	mine, requested, linked, tracked []github.Ref
	at                               time.Time
}

func liveFixture(t *testing.T) *live {
	t.Helper()
	l := &live{gh: &hosted{prs: map[github.Ref]*hostedPR{}}, log: change.NewLog(change.DefaultWindow, nil), at: now}
	quiet := now.Add(-time.Hour)
	ref := func(n int) github.Ref { return github.Ref{Owner: "acme", Repo: "api", Number: n} }
	open := func(n int, author string, requests ...string) github.Ref {
		l.gh.add(hostedPR{ref: ref(n), author: author, state: dgh.StateOpen, updated: quiet, head: fmt.Sprintf("h%d", n), checks: "SUCCESS", mergeable: "MERGEABLE", requests: requests})
		return ref(n)
	}
	n := 100
	for range 30 {
		n++
		l.mine = append(l.mine, open(n, "me"))
	}
	for range 9 {
		n++
		l.requested = append(l.requested, open(n, "ana", "me"))
	}
	var wss []wsdomain.Workspace
	for range 23 {
		n++
		l.linked = append(l.linked, open(n, "zoe"))
		wss = append(wss, wsdomain.Workspace{ID: fmt.Sprintf("w%d", n), RepoID: "r1", DisplayName: fmt.Sprintf("w%d", n), HostID: wsdomain.LocalHost, LinkedPR: n})
	}
	for range 7 {
		n++
		l.tracked = append(l.tracked, open(n, "zoe"))
	}

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "inv.db"), l.log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Defaults().WithColumns(map[string]map[string]string{"board": {config.ColumnNeedsMe: "needs-me"}})
	l.c = &Collector{Store: inventory.New(db), GH: githubtest.Client(l.gh), Sink: "board", Cfg: cfg, Now: func() time.Time { return l.at }, Log: t.Logf,
		WS: workspace.NewSet(&fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", RemoteKey: "github.com/acme/api"}}, WorkspaceList: wss})}
	ctx := context.Background()
	b, _ := json.Marshal(scope.Resolved{Viewer: "me"})
	if err := l.c.Store.SetMeta(ctx, metaScope, string(b)); err != nil {
		t.Fatal(err)
	}
	for _, r := range l.tracked {
		if err := l.c.Store.SetOverride(ctx, dinv.Override{PR: r, Tracked: true}); err != nil {
			t.Fatal(err)
		}
	}
	if rep, err := l.c.Fast(ctx); err != nil || rep.Fetched != 69 || rep.Failed != 0 {
		t.Fatalf("first run: %+v %v", rep, err)
	}
	if prs, _ := l.c.Store.ListPRs(ctx, true); len(prs) != 69 || slices.ContainsFunc(prs, func(p dinv.PR) bool { return !p.Followed() }) {
		t.Fatalf("%d open pull requests stored", len(prs))
	}
	l.gh.reset()
	l.at = now.Add(2 * time.Minute)
	return l
}

func (l *live) fast(t *testing.T) (Report, *meter.Scope) {
	t.Helper()
	ctx, sc := metertest.Start(t, context.Background())
	rep, err := l.c.Fast(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return rep, sc
}

var (
	graphqlCalls  = metertest.Limit{Res: meter.GitHub, Op: "graphql"}
	graphqlPoints = metertest.Limit{Res: meter.GitHub, Op: "graphql", Field: metertest.Points}
	githubSpawns  = metertest.Limit{Res: meter.GitHub, Field: metertest.Spawns}
)

func TestIdleFastRunBudget(t *testing.T) {
	l := liveFixture(t)
	rep, sc := l.fast(t)
	if rep.Fetched != 0 || rep.Failed != 0 {
		t.Fatalf("report %+v", rep)
	}
	for lim, want := range map[metertest.Limit]int64{graphqlCalls: 3, graphqlPoints: 3, githubSpawns: 0} {
		if got := metertest.Count(sc, lim); got != want {
			t.Errorf("%s = %d, want %d\n%s", lim, got, want, metertest.Report(sc.Tally()))
		}
	}
	if l.gh.searches != 1 || !slices.Equal(l.gh.stubs, []int{40, 29}) || len(l.gh.reads) != 0 {
		t.Fatalf("searches %d, stub batches %v, reads %v", l.gh.searches, l.gh.stubs, l.gh.reads)
	}
}

func TestFastReadsOnlyWhatMoved(t *testing.T) {
	l := liveFixture(t)
	moved := now.Add(time.Minute)
	for _, r := range []github.Ref{l.mine[3], l.requested[2]} {
		l.gh.edit(r, func(p *hostedPR) { p.updated = moved })
	}
	l.gh.edit(l.linked[5], func(p *hostedPR) { p.checks, p.failed = "FAILURE", []string{"lint"} })
	rep, sc := l.fast(t)
	if got := metertest.Count(sc, graphqlCalls); got != 4 {
		t.Fatalf("%d GraphQL requests, want 4\n%s", got, metertest.Report(sc.Tally()))
	}
	if l.gh.searches != 1 || !slices.Equal(l.gh.stubs, []int{40, 27}) {
		t.Fatalf("searches %d, stub batches %v", l.gh.searches, l.gh.stubs)
	}
	want := []github.Ref{l.mine[3], l.requested[2], l.linked[5]}
	if got := slices.SortedFunc(slices.Values(l.gh.reads), cmpRef); !slices.Equal(got, slices.SortedFunc(slices.Values(want), cmpRef)) || rep.Fetched != 3 {
		t.Fatalf("read %v (fetched %d), want %v in one request", l.gh.reads, rep.Fetched, want)
	}
}

func TestMergeableOnlyMoveIsNotRead(t *testing.T) {
	l := liveFixture(t)
	ref := l.mine[0]
	l.gh.edit(ref, func(p *hostedPR) { p.mergeable = "CONFLICTING" })
	gen := l.log.Gen()
	rep, _ := l.fast(t)
	if rep.Fetched != 0 || len(l.gh.reads) != 0 {
		t.Fatalf("a mergeable move was read in full: %v", l.gh.reads)
	}
	pr, _, err := l.c.Store.PR(context.Background(), ref)
	if err != nil || pr.Mergeable != "CONFLICTING" || !pr.MergeableAt.Equal(l.at) {
		t.Fatalf("stored %q at %v: %v", pr.Mergeable, pr.MergeableAt, err)
	}
	if c := l.log.Since(gen, port.TopicPRs); !slices.Equal(c.Units, []unit.Unit{ref.Unit()}) {
		t.Fatalf("noted %v, want %s", c.Units, ref)
	}
}

func TestDiscoveryPagesPastTen(t *testing.T) {
	l := liveFixture(t)
	var fresh []github.Ref
	for n := 500; n < 513; n++ {
		p := l.gh.add(hostedPR{ref: github.Ref{Owner: "acme", Repo: "api", Number: n}, author: "bruno", state: dgh.StateOpen, updated: now.Add(time.Minute), head: "x", checks: "SUCCESS", requests: []string{"me"}})
		fresh = append(fresh, p.ref)
	}
	rep, _ := l.fast(t)
	if l.gh.searches != 2 {
		t.Fatalf("%d search requests, want 2", l.gh.searches)
	}
	if rep.Fetched != 13 || len(l.gh.reads) != 13 {
		t.Fatalf("fetched %d, read %v", rep.Fetched, l.gh.reads)
	}
	stored, err := l.c.Store.PRs(context.Background(), fresh)
	if err != nil || len(stored) != 13 {
		t.Fatalf("stored %d of 13: %v", len(stored), err)
	}
}

func TestOutOfScopeCheckChangeSeen(t *testing.T) {
	l := liveFixture(t)
	ref := l.linked[0]
	l.gh.edit(ref, func(p *hostedPR) { p.checks, p.failed = "FAILURE", []string{"e2e"} })
	l.fast(t)
	if !slices.Equal(l.gh.reads, []github.Ref{ref}) {
		t.Fatalf("read %v, want %s", l.gh.reads, ref)
	}
	pr, _, err := l.c.Store.PR(context.Background(), ref)
	if err != nil || pr.ChecksState != "FAILURE" || len(pr.FailingChecks) != 1 || len(pr.Reasons) != 0 {
		t.Fatalf("stored %+v: %v", pr, err)
	}
}

func TestCollectorIdleFastRun(t *testing.T) {
	l := liveFixture(t)
	gen := l.log.Gen()
	_, sc := l.fast(t)
	if c := l.log.Since(gen, port.TopicPRs|port.TopicSourceEvents|port.TopicOverrides|port.TopicFollow); c.Topics != 0 {
		t.Fatalf("an idle fast run noted %+v", c)
	}
	var sql []string
	traced := 0
	for _, x := range sc.Samples() {
		if x.Res != meter.SQL {
			continue
		}
		traced++
		if !strings.Contains(x.Detail, " meta ") && !strings.Contains(x.Detail, " meta(") {
			sql = append(sql, x.Op+": "+x.Detail)
		}
	}
	if traced == 0 || len(sql) == 0 {
		t.Fatalf("the scope traced %d store statements, %d outside meta: nothing to count", traced, len(sql))
	}
	if len(sql) > 10 {
		t.Fatalf("%d store statements excluding meta, want at most 10:\n%s", len(sql), strings.Join(sql, "\n"))
	}
	t.Logf("%d store statements excluding meta", len(sql))
}

func cmpRef(a, b github.Ref) int { return strings.Compare(a.String(), b.String()) }
