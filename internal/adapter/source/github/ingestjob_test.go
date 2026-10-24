package ghsource

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/port"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fastPassRecord runs pass four times on one recorded store (a first pass,
// an unchanged one, one under a low budget, one GitHub refuses mid-run) and
// renders what each did:
// its outcome, the GitHub reads, the store's statements, the notes and the
// store's rows after it.
func fastPassRecord(t *testing.T, pass func(ctx context.Context, c *Collector) (string, string, error)) string {
	t.Helper()
	ctx := context.Background()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, b, tracked, linked := snapshot(), snapshot(), snapshot(), snapshot()
	b.Number, b.HeadRefName, b.Title = 8, "feat/export", "Export"
	tracked.Number, tracked.Title = 9, "Tracked"
	linked.Number, linked.Title = 11, "Linked"
	for _, s := range []*github.Snapshot{&b, &tracked, &linked} {
		s.URL = fmt.Sprintf("https://github.com/acme/api/pull/%d", s.Number)
	}
	gh := &fakeGH{remaining: 4000, pages: map[string][]github.StubPage{"author:ana": {{Stubs: []github.Stub{stubOf(a), stubOf(b)}}}},
		byRef: map[github.Ref]github.Snapshot{a.Ref: a, b.Ref: b, tracked.Ref: tracked, linked.Ref: linked}}
	c := newCollector(t, gh)
	c.Store = inventory.New(db)
	c.WS = workspace.NewSet(&fake.Manager{
		RepoList: []wsdomain.Repo{{ID: "r1", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{
			{ID: "w0", RepoID: "r1", IsMain: true, HostID: wsdomain.LocalHost, Branch: "main"},
			{ID: "w1", RepoID: "r1", DisplayName: "linked", HostID: wsdomain.LocalHost, LinkedPR: 11},
			{ID: "w2", RepoID: "r1", DisplayName: "export", HostID: wsdomain.LocalHost, Branch: "feat/export"},
		},
	})
	if err := c.Store.UpsertPR(ctx, dinv.PR{Ref: a.Ref, State: "OPEN", Title: "stale", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SetOverride(ctx, dinv.Override{PR: tracked.Ref, Tracked: true}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notes []string
	stop := log.Subscribe(func(ch port.Change) {
		mu.Lock()
		defer mu.Unlock()
		notes = append(notes, fmt.Sprintf("topics %d all %v units %v", ch.Topics, ch.All, ch.Units))
	})
	defer stop()

	var out strings.Builder
	for i, name := range []string{"first", "unchanged", "low budget", "refused mid-run"} {
		switch name {
		case "low budget":
			gh.remaining, gh.resetAt = 10, now.Add(time.Hour)
		case "refused mid-run":
			moved := a
			moved.HeadRefOid = "h3"
			gh.remaining, gh.resetAt = 4000, time.Time{}
			gh.pages["author:ana"] = []github.StubPage{{Stubs: []github.Stub{stubOf(moved)}}}
			gh.byRefErr = fmt.Errorf("read: %w", dgh.ErrBudget)
		}
		gh.calls, gh.read, gh.stubRefs, gh.branchQs, gh.threadReads, gh.served = nil, nil, nil, nil, nil, nil
		mu.Lock()
		notes = nil
		mu.Unlock()
		mctx, scope := meter.Start(ctx, "pass")
		summary, skipped, err := pass(mctx, c)
		tally := scope.End()
		fmt.Fprintf(&out, "== pass %d (%s)\noutcome: %q %q %v\n", i+1, name, summary, skipped, err)
		fmt.Fprintf(&out, "searches: %q\nreads: %v\nstub reads: %v\nbranch queries: %v\nthread reads: %v\n", gh.calls, gh.read, gh.stubRefs, gh.branchQs, len(gh.threadReads))
		for _, k := range tally.Keys() {
			st := tally[k]
			fmt.Fprintf(&out, "meter %s/%s: %d calls, %d rows\n", k.Res, k.Op, st.Calls, st.Rows)
		}
		mu.Lock()
		fmt.Fprintf(&out, "notes:\n  %s\n", strings.Join(notes, "\n  "))
		mu.Unlock()
		out.WriteString(storeRows(t, ctx, c.Store))
	}
	return out.String()
}

func storeRows(t *testing.T, ctx context.Context, st *inventory.Store) string {
	t.Helper()
	prs, overrides, err := st.PRsWithOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var refs []github.Ref
	var b strings.Builder
	for _, p := range prs {
		refs = append(refs, p.Ref)
		fmt.Fprintf(&b, "pr %s %q %s reasons %v seen %s workspace %q head %s\n", p.Ref, p.Title, p.State, p.Reasons, p.LastSeenAt.Format(time.RFC3339), p.Workspace.ID, p.HeadOid)
	}
	for _, r := range slices.SortedFunc(maps.Keys(overrides), func(x, y github.Ref) int { return strings.Compare(x.String(), y.String()) }) {
		fmt.Fprintf(&b, "override %s tracked %v\n", r, overrides[r].Tracked)
	}
	evs, err := st.EventsByPR(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		kinds := map[string]int{}
		for _, e := range evs[r] {
			kinds[e.Kind]++
		}
		fmt.Fprintf(&b, "events %s %v\n", r, kinds)
	}
	for _, k := range []string{metaBudget, metaFastAt, metaSlowAt, metaScope} {
		v, err := st.Meta(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "meta %s %s\n", k, v)
	}
	return b.String()
}

func TestTheGitHubIngestJobMatchesItsGolden(t *testing.T) {
	got := fastPassRecord(t, func(ctx context.Context, c *Collector) (string, string, error) {
		return ingest.RunJob(ctx, c.IngestJob(), c.Now())
	})
	path := filepath.Join("testdata", "fast_pass.golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("the ingest job differs from testdata/fast_pass.golden:\n--- got\n%s\n--- want\n%s", got, want)
	}
}
