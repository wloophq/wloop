package collect

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fastPassRecord runs pass four times on one store (a first pass, an
// unchanged one, one under a low budget, one GitHub refuses mid-run) and
// renders what each did: its outcome, the GitHub reads, the pull requests
// each commit changed and the store's rows and the host's events after it.
func fastPassRecord(t *testing.T, pass func(ctx context.Context, c *Collector) (string, string, error)) string {
	t.Helper()
	ctx := context.Background()
	a, b, tracked, linked := snapshot(), snapshot(), snapshot(), snapshot()
	b.Number, b.HeadRefName, b.Title = 8, "feat/export", "Export"
	tracked.Number, tracked.Title = 9, "Tracked"
	linked.Number, linked.Title = 11, "Linked"
	for _, s := range []*api.Snapshot{&b, &tracked, &linked} {
		s.URL = fmt.Sprintf("https://github.com/acme/api/pull/%d", s.Number)
	}
	gh := &fakeGH{remaining: 4000, pages: map[string][]api.StubPage{"author:ana": {{Stubs: []api.Stub{stubOf(a), stubOf(b)}}}},
		byRef: map[api.Ref]api.Snapshot{a.Ref: a, b.Ref: b, tracked.Ref: tracked, linked.Ref: linked}}
	c := newCollector(t, gh)
	c.WS = acmeAPI(
		src.Workspace{ID: "w0", IsMain: true, Branch: "main"},
		src.Workspace{ID: "w1", DisplayName: "linked", Units: linkedTo(11)},
		src.Workspace{ID: "w2", DisplayName: "export", Branch: "feat/export"},
	)
	if err := c.Store.UpsertPR(ctx, pr.PR{Ref: a.Ref, State: "OPEN", Title: "stale", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	if err := setTracked(ctx, c.Store, tracked.Ref); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notes []string
	c.Store.OnChange(func(ch store.Change) {
		mu.Lock()
		defer mu.Unlock()
		notes = append(notes, fmt.Sprintf("changed %v", ch.Refs))
	})

	var out strings.Builder
	for i, name := range []string{"first", "unchanged", "low budget", "refused mid-run"} {
		switch name {
		case "low budget":
			gh.remaining, gh.resetAt = 10, now.Add(time.Hour)
		case "refused mid-run":
			moved := a
			moved.HeadRefOid = "h3"
			gh.remaining, gh.resetAt = 4000, time.Time{}
			gh.pages["author:ana"] = []api.StubPage{{Stubs: []api.Stub{stubOf(moved)}}}
			gh.byRefErr = fmt.Errorf("read: %w", api.ErrBudget)
		}
		gh.calls, gh.read, gh.stubRefs, gh.branchQs, gh.threadReads, gh.served = nil, nil, nil, nil, nil, nil
		mu.Lock()
		notes = nil
		mu.Unlock()
		summary, skipped, err := pass(ctx, c)
		fmt.Fprintf(&out, "== pass %d (%s)\noutcome: %q %q %v\n", i+1, name, summary, skipped, err)
		fmt.Fprintf(&out, "searches: %q\nreads: %v\nstub reads: %v\nbranch queries: %v\nthread reads: %v\n", gh.calls, gh.read, gh.stubRefs, gh.branchQs, len(gh.threadReads))
		mu.Lock()
		fmt.Fprintf(&out, "notes:\n  %s\n", strings.Join(notes, "\n  "))
		mu.Unlock()
		out.WriteString(storeRows(t, ctx, c.Store))
	}
	return out.String()
}

func storeRows(t *testing.T, ctx context.Context, st *store.DB) string {
	t.Helper()
	prs, err := st.ListPRs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var refs []api.Ref
	var b strings.Builder
	for _, p := range prs {
		refs = append(refs, p.Ref)
		fmt.Fprintf(&b, "pr %s %q %s reasons %v seen %s workspace %q head %s\n", p.Ref, p.Title, p.State, p.Reasons, p.LastSeenAt.Format(time.RFC3339), p.Workspace.ID, p.HeadOid)
	}
	for _, r := range slices.SortedFunc(slices.Values(refs), pr.Compare) {
		o, err := st.Override(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if o.Tracked {
			fmt.Fprintf(&b, "override %s tracked %v\n", r, o.Tracked)
		}
	}
	evs, err := st.HostEvents(ctx, refs, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		kinds := map[string]int{}
		for _, e := range evs {
			if e.PR == r {
				kinds[e.Kind]++
			}
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
		return Outcome(c.Fast(ctx))
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
		t.Fatalf("the fast pass differs from testdata/fast_pass.golden:\n--- got\n%s\n--- want\n%s", got, want)
	}
}
