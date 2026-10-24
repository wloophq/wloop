package brief

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeGitHub answers BriefFacts with facts and the diff request with diff,
// or with diffStatus and no body.
type fakeGitHub struct {
	facts, diff string
	diffStatus  int
}

func (g fakeGitHub) client() *github.Client {
	return githubtest.Client(roundTrip(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, g.diff
		if r.URL.Path == "/graphql" {
			body = `{"data":{"pr":{"pullRequest":` + g.facts + `}}}`
		} else if g.diffStatus != 0 {
			status, body = g.diffStatus, `{"message":"diff too large"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
}

var r7 = github.Ref{Owner: "acme", Repo: "api", Number: 7}

const prFacts = `{"title":"Batch export","body":"Exports in batches.","changedFiles":1,
"closingIssuesReferences":{"nodes":[{"title":"Slow export","body":"It times out."}]},
"files":{"pageInfo":{"hasNextPage":false,"endCursor":"x"},"nodes":[{"path":"export.go","additions":40,"deletions":2}]}}`

func TestFactsGathersTheParts(t *testing.T) {
	in, err := Source{GitHub: fakeGitHub{facts: prFacts}.client()}.Facts(context.Background(), r7)
	if err != nil {
		t.Fatal(err)
	}
	if in.Title != "Batch export" || len(in.Files) != 1 || in.Files[0] != "export.go (+40 -2)" || in.Issue != "Slow export\n\nIt times out." {
		t.Fatalf("%+v", in)
	}
	if len(in.Paths) != 1 || in.Paths[0] != "export.go" || in.Diff != "" {
		t.Fatalf("%+v", in)
	}
	long := `{"title":"t","body":"` + strings.Repeat("a", bodyMax+10) + `","files":{"nodes":[]}}`
	if in, _ := (Source{GitHub: fakeGitHub{facts: long}.client()}).Facts(context.Background(), r7); len(in.Body) != bodyMax || in.Issue != "" {
		t.Fatalf("body %d bytes, issue %q", len(in.Body), in.Issue)
	}
}

func TestFactsNamesTheFilesPastTheList(t *testing.T) {
	node := `{"path":"a.go","additions":1,"deletions":0}`
	facts := `{"title":"t","body":"","changedFiles":301,"files":{"pageInfo":{"hasNextPage":true,"endCursor":"c"},"nodes":[` +
		strings.Repeat(node+",", 99) + node + `]}}`
	in, err := Source{GitHub: fakeGitHub{facts: facts}.client()}.Facts(context.Background(), r7)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Files[len(in.Files)-1]; got != "and 1 more files" || len(in.Paths) != github.BriefFilesMax {
		t.Fatalf("last line %q, %d paths", got, len(in.Paths))
	}
}

func TestDiffCutsOnARuneBoundary(t *testing.T) {
	diff := strings.Repeat("é", 100) // 200 bytes
	got, cut, err := Source{GitHub: fakeGitHub{diff: diff}.client()}.Diff(context.Background(), r7, 51)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 || !utf8.ValidString(got) || !cut {
		t.Fatalf("diff %d bytes, cut %v", len(got), cut)
	}
	got, cut, _ = Source{GitHub: fakeGitHub{diff: diff}.client()}.Diff(context.Background(), r7, 200)
	if got != diff || cut {
		t.Fatalf("a diff that fits: %d bytes, cut %v", len(got), cut)
	}
}

func TestInputNotesACutDiff(t *testing.T) {
	r := newRig(t, 1)
	r.src.diff, r.src.diffCut = strings.Repeat("x", 1000), true
	r.q.Run(context.Background())
	if len(r.run.prompts) != 1 || !strings.Contains(r.run.prompts[0], " (the first 1000 bytes; the diff is longer)") {
		t.Fatalf("prompts %q", r.run.prompts)
	}
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].DiffBytes != nil || !runs[0].DiffCut {
		t.Fatalf("runs %+v", runs)
	}
}

func TestInputWithoutADiff(t *testing.T) {
	r := newRig(t, 1)
	r.q.d.Source = Source{GitHub: fakeGitHub{facts: prFacts, diffStatus: http.StatusNotAcceptable}.client()}
	r.q.Run(context.Background())
	if len(r.run.prompts) != 1 || !strings.Contains(r.run.prompts[0], "(unavailable: GitHub HTTP 406") {
		t.Fatalf("prompts %q", r.run.prompts)
	}
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].DiffBytes != nil || runs[0].DiffCut || runs[0].Outcome != OutcomeOK {
		t.Fatalf("runs %+v", runs)
	}
}

func TestABriefSpendsAtMostTwoGitHubCalls(t *testing.T) {
	for trigger, p := range map[string]dgh.Priority{TriggerBackground: dgh.PriorityBackground, TriggerWanted: dgh.PriorityUser} {
		t.Run(trigger, func(t *testing.T) {
			r := newRig(t, 1)
			r.q.d.Source = Source{GitHub: fakeGitHub{facts: prFacts, diff: "diff --git a/export.go b/export.go\n+x\n"}.client()}
			ctx, sc := metertest.Start(t, dgh.WithPriority(context.Background(), p))
			pr, _, _ := r.st.PR(ctx, refN(1))
			if !r.q.claim(pr.Ref) {
				t.Fatal("not claimed")
			}
			if err := r.q.brief(ctx, pr, trigger, time.Minute); err != nil {
				t.Fatal(err)
			}
			metertest.Within(t, sc, metertest.Budget{
				{Res: meter.GitHub}:                2,
				{Res: meter.GitHub, Op: "graphql"}: 1,
				{Res: meter.GitHub, Op: "rest"}:    1,
			})
			if len(r.run.prompts) != 1 || !strings.Contains(r.run.prompts[0], "Slow export") || !strings.Contains(r.run.prompts[0], "diff --git") {
				t.Fatalf("prompts %q", r.run.prompts)
			}
		})
	}
}
