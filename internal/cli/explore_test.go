package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestExplorePrintsCandidatesThroughTheDaemon(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveJSON(t, map[string]any{
		"GET /api/health": api.Health{Version: "test"},
		"GET /api/rows":   api.RowsPage{Full: true, Rows: []api.Row{{Unit: r7.Unit(), Label: r7.String(), Kind: "review", Summary: api.Summary{WaitsOn: "me", Since: testNow.Add(-50 * time.Hour)}}}},
		"POST /api/explore": api.ExploreResult{Query: "is:pr is:open repo:acme/api", Read: `read "acme/api" as repo:acme/api`, Total: 2, Cost: 1,
			RateLimit: api.RateLimit{Remaining: 4900, Limit: 5000},
			Candidates: []api.Candidate{
				{Ref: pr.Ref(r7), Title: "Batch export", Author: "ana", Additions: 120, Deletions: 4, ChangedFiles: 3,
					ReviewDecision: "REVIEW_REQUIRED", ChecksState: "SUCCESS", UpdatedAt: testNow.Add(-2 * time.Hour)},
				{Ref: pr.Ref{Owner: "acme", Repo: "api", Number: 9}, Title: "Cache", Author: "zoe", IsDraft: true, ChangedFiles: 1,
					UpdatedAt: testNow.Add(-3 * time.Hour)},
			}},
	})
	if code := te.run("explore", "acme/api"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := te.out.String()
	if !strings.HasPrefix(out, "read \"acme/api\" as repo:acme/api\n") {
		t.Errorf("the header does not say how the input was read:\n%s", out)
	}
	for _, want := range []string{"acme/api#7", "+120 -4", "3 files", "review required", "checks success", "by ana", "waiting 2d", "by zoe  waiting 3h", "  Batch export",
		"in the inventory: review, waiting on me", "acme/api#9", "1 file", "draft", "2 of 2 for is:pr is:open repo:acme/api; cost 1, budget 4900/5000"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "updated") || strings.Contains(out, "waiting 2h") {
		t.Errorf("a candidate in the inventory waits since its row says, not since its last update:\n%s", out)
	}
}

func TestExploreSaysAnAuthorHasNothingOpen(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveJSON(t, map[string]any{
		"GET /api/health": api.Health{Version: "test"},
		"GET /api/rows":   api.RowsPage{Full: true, Rows: []api.Row{}},
		"POST /api/explore": api.ExploreResult{Query: "is:pr author:zoe", Read: `read "zoe" as author:zoe`,
			Empty: "zoe has no open pull requests you did not open", Candidates: []api.Candidate{}},
	})
	if code := te.run("explore", "zoe"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out := te.out.String(); out != "read \"zoe\" as author:zoe\nzoe has no open pull requests you did not open\n" {
		t.Fatalf("output:\n%s", out)
	}
}
