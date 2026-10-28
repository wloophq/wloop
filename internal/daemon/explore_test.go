package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/change"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type fakeExplorer struct {
	queries  []string
	viewer   string
	page     dgh.CandidatePage
	err      error
	errs     []error // returned by the next searches, in order, before err
	users    map[string]bool
	asked    []string
	pages    func(q string) dgh.CandidatePage // nil answers page
	batches  int
	aliasErr func(q string) error // nil: no search fails alone
}

func (f *fakeExplorer) IsUser(_ context.Context, login string) (bool, error) {
	f.asked = append(f.asked, login)
	if login == "flaky" {
		return false, fmt.Errorf("HTTP 502")
	}
	return f.users[login], nil
}

func (f *fakeExplorer) SearchCandidates(_ context.Context, q, viewer string, first int) (dgh.CandidatePage, error) {
	f.queries, f.viewer = append(f.queries, fmt.Sprintf("%s|%d", q, first)), viewer
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return dgh.CandidatePage{}, err
	}
	if f.pages != nil {
		return f.pages(q), f.err
	}
	return f.page, f.err
}

func (f *fakeExplorer) SearchCandidatesEach(ctx context.Context, qs []string, viewer string, first int) ([]dgh.CandidatePage, []error, error) {
	f.batches++
	pages, errs := make([]dgh.CandidatePage, len(qs)), make([]error, len(qs))
	for i, q := range qs {
		if f.aliasErr != nil {
			if errs[i] = f.aliasErr(q); errs[i] != nil {
				continue
			}
		}
		page, err := f.SearchCandidates(ctx, q, viewer, first)
		if err != nil {
			return nil, nil, err
		}
		pages[i] = page
	}
	return pages, errs, nil
}

func (f *fakeExplorer) CandidateByRef(_ context.Context, ref github.Ref, viewer string) (dgh.CandidatePage, error) {
	f.queries, f.viewer = append(f.queries, "ref "+ref.String()), viewer
	return f.page, f.err
}

func explorePost(t *testing.T, s *Server, query string) (int, api.ExploreResult, string) {
	t.Helper()
	body, _ := json.Marshal(api.ExploreRequest{Query: query})
	rec := post(t, s, "/api/explore", string(body))
	var res api.ExploreResult
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, res, rec.Body.String()
}

func TestExploreSearchesGitHubAndAddsWhatTheInventoryKnows(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{page: dgh.CandidatePage{Total: 41, RateLimit: dgh.RateLimit{Cost: 1, Remaining: 4900, Limit: 5000},
		Candidates: []dgh.Candidate{
			{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN"},
			{Ref: github.Ref{Owner: "acme", Repo: "web", Number: 3}, Title: "Other", Author: "zoe", State: "OPEN"},
		}}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe; withBriefs(d, stubBriefs{}) })
	code, res, body := explorePost(t, s, "acme/api")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	want := "is:pr is:open archived:false -author:@me repo:acme/api sort:updated-asc|30"
	if !slices.Equal(fe.queries, []string{want}) || fe.viewer != "me" {
		t.Fatalf("asked %v as %q", fe.queries, fe.viewer)
	}
	if res.Total != 41 || res.Cost != 1 || res.RateLimit.Remaining != 4900 || len(res.Candidates) != 2 {
		t.Fatalf("result %+v", res)
	}
	if res.Candidates[0].Brief == nil || res.Candidates[0].Brief.Brief.Goal != "Export in batches" || res.Candidates[1].Brief != nil {
		t.Fatalf("a brief only for what the inventory holds: %+v", res.Candidates)
	}
	if code, _, _ := explorePost(t, s, "https://github.com/acme/api/pull/7"); code != 200 || fe.queries[1] != "ref acme/api#7" {
		t.Fatalf("a URL reads one pull request: %d %v", code, fe.queries)
	}
}

func TestExploreSanitizesWhatGitHubSends(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{page: dgh.CandidatePage{Candidates: []dgh.Candidate{
		{Ref: r7, Title: "Batch\x1b[2J export\u202e", HeadBranch: "ana/\x07bell"},
	}}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	_, res, body := explorePost(t, s, "acme/api")
	c := res.Candidates[0]
	if strings.ContainsAny(c.Title+c.HeadBranch, "\x1b\x07\u202e") {
		t.Fatalf("control or bidi characters reached the client: %s", body)
	}
}

func TestExploreRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{err: fmt.Errorf("graphql: boom")}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	for query, want := range map[string]int{"": 400, strings.Repeat("x", 300): 400, "acme/api": 502} {
		if code, _, body := explorePost(t, s, query); code != want {
			t.Errorf("%.20q: %d %s, want %d", query, code, body, want)
		}
	}
	if code, _, _ := explorePost(t, newServerWith(t, func(d *Deps) { d.Explorer = nil }), "acme/api"); code != 503 {
		t.Errorf("no explorer: %d", code)
	}
}

func TestExploreWaitsWhenTheBudgetIsLow(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	seedRateLimit(t, s, 100, 5000, s.deps.Now().Add(time.Hour))
	if code, _, body := explorePost(t, s, "acme/api"); code != 429 || !strings.Contains(body, "budget") || len(fe.queries) != 0 {
		t.Fatalf("%d %s, %d searches", code, body, len(fe.queries))
	}
}

func TestExploreRemembersTheLastSearches(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { d.Explorer = &fakeExplorer{} })
	for i := range recentMax + 1 {
		explorePost(t, s, fmt.Sprintf("org:acme label:l%d", i))
	}
	_, res, _ := explorePost(t, s, "org:acme   label:l3")
	var recent []string
	get(t, s, "/api/explore/recent", &recent)
	if len(recent) != recentMax || recent[0] != "org:acme label:l3" || recent[1] != fmt.Sprintf("org:acme label:l%d", recentMax) || !slices.Equal(recent, res.Recent) {
		t.Fatalf("recent %v", recent)
	}
}

type goalBriefs struct{ stubBriefs }

func (goalBriefs) Views(_ context.Context, prs ...dinv.PR) (map[github.Ref]*brief.View, error) {
	return viewsOf(&brief.View{State: "ready", Brief: &brief.Brief{Goal: "Export\x1b[2J in\nbatches\u202e", Kind: "feature\n", Size: "m", Risk: "low"}}, prs), nil
}

func TestExploreSanitizesTheBrief(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{page: dgh.CandidatePage{Candidates: []dgh.Candidate{{Ref: r7, Title: "Batch export"}}}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe; withBriefs(d, goalBriefs{}) })
	_, res, body := explorePost(t, s, "acme/api")
	b := res.Candidates[0].Brief.Brief
	if b.Goal != "Export [2J in batches" || b.Kind != "feature" {
		t.Fatalf("brief %q %q: %s", b.Goal, b.Kind, body)
	}
}

type blockingExplorer struct {
	fakeExplorer
	started, release chan struct{}
}

func (b *blockingExplorer) SearchCandidates(ctx context.Context, q, viewer string, first int) (dgh.CandidatePage, error) {
	close(b.started)
	<-b.release
	return b.fakeExplorer.SearchCandidates(ctx, q, viewer, first)
}

func TestExploreRunsOneSearchAtATime(t *testing.T) {
	t.Parallel()
	be := &blockingExplorer{started: make(chan struct{}), release: make(chan struct{})}
	s := newServerWith(t, func(d *Deps) { d.Explorer = be })
	first := make(chan int)
	go func() { code, _, _ := explorePost(t, s, "acme/api"); first <- code }()
	<-be.started
	if code, _, body := explorePost(t, s, "acme/web"); code != 409 {
		t.Fatalf("a second search while one runs: %d %s", code, body)
	}
	close(be.release)
	if code := <-first; code != 200 {
		t.Fatalf("first: %d", code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExploreKeepsTheBudgetItSpent(t *testing.T) {
	t.Parallel()
	var sent int
	reset := time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)
	body := fmt.Sprintf(`{"data":{"search":{"issueCount":0,"nodes":[]},"rateLimit":{"cost":1,"remaining":100,"limit":5000,"resetAt":%q}}}`, reset.Format(time.RFC3339))
	gh := github.NewClient(github.Options{Host: "github.com", Now: func() time.Time { return time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC) },
		Token: func(context.Context) (string, error) { return "t", nil },
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sent++
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})})
	s := newServerWith(t, func(d *Deps) { d.Explorer, d.Budget = gh, ghsource.Budget{Limiter: gh} })
	if code, _, body := explorePost(t, s, "acme/api"); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, _, body := explorePost(t, s, "acme/api"); code != 429 || sent != 1 {
		t.Fatalf("the search's own budget did not hold the next: %d %s, %d requests", code, body, sent)
	}
	if _, err := s.readUntracked(context.Background(), github.Ref{Owner: "acme", Repo: "api", Number: 9}.Unit(), "me"); err == nil || !strings.Contains(err.Error(), "budget") || sent != 1 {
		t.Fatalf("a candidate read below the background floor: %v, %d requests", err, sent)
	}
}

func TestExploreRemembersAPullRequestByItsURL(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { d.Explorer = &fakeExplorer{} })
	_, res, _ := explorePost(t, s, "  https://github.com/acme/api/pull/7/files?diff=split  ")
	if len(res.Recent) != 1 || res.Recent[0] != "https://github.com/acme/api/pull/7" {
		t.Fatalf("recent %q", res.Recent)
	}
}

func TestExploreReadsWhatTheUserMeant(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{users: map[string]bool{"zoe": true}, page: dgh.CandidatePage{Total: 1, Candidates: []dgh.Candidate{{Ref: r7}}}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	const base = "is:pr is:open archived:false -author:@me "
	for _, c := range []struct{ in, query, read string }{
		{"API", base + "repo:acme/api sort:updated-asc", `read "API" as repo:acme/api`},
		{"zoe", base + "author:zoe sort:updated-asc", `read "zoe" as author:zoe`},
		{"zoe", base + "author:zoe sort:updated-asc", `read "zoe" as author:zoe`},
		{"nobody", base + "nobody org:acme sort:updated-asc", `read "nobody" as text in org:acme`},
		{"nobody", base + "nobody org:acme sort:updated-asc", `read "nobody" as text in org:acme`},
		{"flaky", base + "flaky org:acme sort:updated-asc", `read "flaky" as text in org:acme`},
		{"flaky", base + "flaky org:acme sort:updated-asc", `read "flaky" as text in org:acme`},
		{"@acme/search", base + "team-review-requested:acme/search sort:updated-asc", `read "@acme/search" as team-review-requested:acme/search`},
		{"org:acme label:bug", base + "org:acme label:bug sort:updated-asc", `read "org:acme label:bug" as a GitHub search`},
		{"https://github.com/acme/api/pull/7", "ref acme/api#7", `read "https://github.com/acme/api/pull/7" as the pull request acme/api#7`},
	} {
		fe.queries = nil
		code, res, body := explorePost(t, s, c.in)
		if code != 200 || len(fe.queries) != 1 || strings.TrimSuffix(fe.queries[0], "|30") != c.query || res.Read != c.read {
			t.Errorf("%q: %d %v %q: %s", c.in, code, fe.queries, res.Read, body)
		}
	}
	if !slices.Equal(fe.asked, []string{"zoe", "nobody", "flaky", "flaky"}) {
		t.Errorf("GitHub asked about %v: an answer is kept for the daemon's life, a failure is not", fe.asked)
	}
}

func TestExploreSaysWhoHasNothingOpen(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{users: map[string]bool{"zoe": true}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	if _, res, body := explorePost(t, s, "zoe"); res.Empty != "zoe has no open pull requests you did not open in the 1 repository "+brand.Name+" knows" {
		t.Fatalf("%q: %s", res.Empty, body)
	}
	if _, res, _ := explorePost(t, s, "@acme/serch"); !strings.HasPrefix(res.Empty, "no open pull request asks acme/serch for a review; GitHub knows a team by its slug") {
		t.Fatalf("an unknown team: %q", res.Empty)
	}
	if _, res, _ := explorePost(t, s, "acme/api"); res.Empty != "" {
		t.Fatalf("a repository's empty search: %q", res.Empty)
	}
	fe.page = dgh.CandidatePage{Total: 1, Candidates: []dgh.Candidate{{Ref: r7}}}
	if _, res, _ := explorePost(t, s, "@zoe"); res.Empty != "" {
		t.Fatalf("an author with pull requests: %q", res.Empty)
	}
}

func TestExploreRetriesAGatewayErrorOnce(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{errs: []error{&github.StatusError{Status: 502}}, page: dgh.CandidatePage{Total: 3}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	s.exploreRetryPause = time.Millisecond
	if code, res, body := explorePost(t, s, "acme/api"); code != 200 || res.Total != 3 || len(fe.queries) != 2 {
		t.Fatalf("%d %s after %d searches", code, body, len(fe.queries))
	}
	fe.queries, fe.errs = nil, []error{&github.StatusError{Status: 502}, &github.StatusError{Status: 504}}
	if code, _, body := explorePost(t, s, "acme/api"); code != 502 || len(fe.queries) != 2 || !strings.Contains(body, "narrow the search") {
		t.Fatalf("%d %s after %d searches", code, body, len(fe.queries))
	}
	fe.queries, fe.errs = nil, []error{fmt.Errorf("graphql: boom")}
	if code, _, body := explorePost(t, s, "acme/api"); code != 502 || len(fe.queries) != 1 || strings.Contains(body, "narrow") {
		t.Fatalf("a lasting error retried: %d %s after %d searches", code, body, len(fe.queries))
	}
}

type slowExplorer struct {
	fakeExplorer
	slow int
}

func (e *slowExplorer) SearchCandidates(ctx context.Context, q, viewer string, first int) (dgh.CandidatePage, error) {
	if e.slow > 0 {
		e.slow--
		e.queries = append(e.queries, q)
		<-ctx.Done()
		return dgh.CandidatePage{}, fmt.Errorf("gh api graphql: signal: killed")
	}
	return e.fakeExplorer.SearchCandidates(ctx, q, viewer, first)
}

func TestExploreRetriesATimeoutOnce(t *testing.T) {
	t.Parallel()
	se := &slowExplorer{slow: 1}
	s := newServerWith(t, func(d *Deps) { d.Explorer = se })
	s.exploreRetryPause, s.exploreAttempt = time.Millisecond, 20*time.Millisecond
	if code, _, body := explorePost(t, s, "acme/api"); code != 200 || len(se.queries) != 2 {
		t.Fatalf("%d %s after %d searches", code, body, len(se.queries))
	}
	se.slow, se.queries = 2, nil
	if code, _, body := explorePost(t, s, "acme/api"); code != 502 || len(se.queries) != 2 || !strings.Contains(body, "narrow the search") {
		t.Fatalf("%d %s after %d searches", code, body, len(se.queries))
	}
}

func TestExploreSendsTheViewerToTheirOwnView(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{users: map[string]bool{"me": true, "Me": true}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	for _, in := range []string{"me", "@Me"} {
		if code, _, body := explorePost(t, s, in); code != 400 || !strings.Contains(body, "My PRs") || len(fe.queries) != 0 {
			t.Fatalf("%q: %d %s, %d searches", in, code, body, len(fe.queries))
		}
	}
}

func TestExploreFindsAnAuthorSearchHidesInTheKnownRepositories(t *testing.T) {
	t.Parallel()
	at := func(h int) time.Time { return time.Date(2026, 10, 8, h, 0, 0, 0, time.UTC) }
	fe := &fakeExplorer{users: map[string]bool{"ivanova": true}, pages: func(q string) dgh.CandidatePage {
		page := dgh.CandidatePage{RateLimit: dgh.RateLimit{Cost: 1, Remaining: 4000, Limit: 5000}}
		for n := range 20 {
			repo := fmt.Sprintf("repository-%02d", n)
			if strings.Contains(q, "repo:acme-labs/"+repo+" ") {
				for i := range 3 {
					page.Candidates = append(page.Candidates, dgh.Candidate{Ref: github.Ref{Owner: "acme-labs", Repo: repo, Number: i + 1}, UpdatedAt: at(n), Author: "ivanova"})
				}
			}
		}
		page.Total = len(page.Candidates)
		return page
	}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	ctx := context.Background()
	for n := range 20 {
		s.store().UpsertPR(ctx, dinv.PR{Ref: github.Ref{Owner: "acme-labs", Repo: fmt.Sprintf("repository-%02d", n), Number: 100}, State: "OPEN"})
	}
	code, res, body := explorePost(t, s, "@ivanova")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if len(fe.queries) < 3 || !strings.HasPrefix(fe.queries[0], "is:pr is:open archived:false -author:@me author:ivanova sort:updated-asc") {
		t.Fatalf("searches %v", fe.queries)
	}
	for _, q := range fe.queries[1:] {
		if !strings.HasPrefix(q, "is:pr is:open archived:false -author:@me author:ivanova repo:") {
			t.Fatalf("a retry outside the known repositories: %q", q)
		}
	}
	searches := len(fe.queries) - 1
	requests := (searches + github.SearchesPerRequest - 1) / github.SearchesPerRequest
	if searches <= github.SearchesPerRequest || fe.batches != requests {
		t.Fatalf("the %d fallback searches ran in %d requests, want %d", searches, fe.batches, requests)
	}
	if len(res.Candidates) != 30 || res.Total != 60 || res.Cost != 1+requests || !strings.HasPrefix(res.Candidates[0].Label, "acme-labs/repository-00#") {
		t.Fatalf("%d candidates of %d, cost %d, first %v", len(res.Candidates), res.Total, res.Cost, res.Candidates[0].Label)
	}
	want := fmt.Sprintf(`read "@ivanova" as author:ivanova; GitHub search cannot see ivanova without a repository; searched the 21 repositories `+brand.Name+` knows in %d searches`, searches)
	if res.Read != want || res.Empty != "" {
		t.Fatalf("read %q, empty %q", res.Read, res.Empty)
	}

	fe.queries = nil
	if _, res, _ := explorePost(t, s, "@ivanova sort:updated-desc"); !strings.HasPrefix(res.Candidates[0].Label, "acme-labs/repository-19#") || !strings.HasSuffix(fe.queries[1], " sort:updated-desc|30") {
		t.Fatalf("the merge does not follow the sort asked: first %v, searches %v", res.Candidates[0].Label, fe.queries)
	}

	fe.queries, fe.pages = nil, func(string) dgh.CandidatePage { return dgh.CandidatePage{} }
	_, res, _ = explorePost(t, s, "ivanova")
	if res.Empty != "ivanova has no open pull requests you did not open in the 21 repositories "+brand.Name+" knows" || strings.Contains(res.Read, "cannot see") ||
		!strings.HasSuffix(res.Read, "; none across GitHub, then searched the 21 repositories "+brand.Name+" knows in "+plural(searches, "search", "searches")) {
		t.Fatalf("read %q, empty %q", res.Read, res.Empty)
	}

	fe.queries, fe.pages = nil, nil
	fe.page = dgh.CandidatePage{Total: 1, Candidates: []dgh.Candidate{{Ref: r7}}}
	if _, res, _ := explorePost(t, s, "ivanova"); len(fe.queries) != 1 || strings.Contains(res.Read, "cannot see") {
		t.Fatalf("an author search finds results: %v %q", fe.queries, res.Read)
	}
}

func TestExploreKeepsTheSearchesThatDidNotFailAlone(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{users: map[string]bool{"ivanova": true}, pages: func(q string) dgh.CandidatePage {
		page := dgh.CandidatePage{}
		for n := range 20 {
			repo := fmt.Sprintf("repository-%02d", n)
			if strings.Contains(q, "repo:acme-labs/"+repo+" ") {
				page.Candidates = append(page.Candidates, dgh.Candidate{Ref: github.Ref{Owner: "acme-labs", Repo: repo, Number: 1}, Author: "ivanova"})
			}
		}
		page.Total = len(page.Candidates)
		return page
	}}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe })
	ctx := context.Background()
	for n := range 20 {
		if err := s.store().UpsertPR(ctx, dinv.PR{Ref: github.Ref{Owner: "acme-labs", Repo: fmt.Sprintf("repository-%02d", n), Number: 100}, State: "OPEN"}); err != nil {
			t.Fatal(err)
		}
	}
	fe.aliasErr = func(q string) error {
		if strings.Contains(q, "repo:acme-labs/repository-00 ") {
			return fmt.Errorf("the listed repositories cannot be searched")
		}
		return nil
	}
	code, res, body := explorePost(t, s, "@ivanova")
	if code != 200 || len(res.Candidates) == 0 || slices.ContainsFunc(res.Candidates, func(c api.Candidate) bool { return strings.HasPrefix(c.Label, "acme-labs/repository-00#") }) {
		t.Fatalf("%d %s", code, body)
	}
	if !strings.Contains(res.Read, "; 1 of the ") || !strings.HasSuffix(res.Read, " failed: the listed repositories cannot be searched") {
		t.Fatalf("read %q", res.Read)
	}

	fe.aliasErr = func(q string) error {
		if strings.Contains(q, " repo:") {
			return fmt.Errorf("the listed repositories cannot be searched")
		}
		return nil
	}
	if code, _, body := explorePost(t, s, "@ivanova"); code != 502 || !strings.Contains(body, "cannot be searched") {
		t.Fatalf("every search failed: %d %s", code, body)
	}
}

type refreshCounting struct {
	goalBriefs
	refreshes int
}

func (b *refreshCounting) RefreshIfStale(context.Context, dinv.PR) error {
	b.refreshes++
	return nil
}

func TestExploreStartsNoBriefRefresh(t *testing.T) {
	t.Parallel()
	fe := &fakeExplorer{page: dgh.CandidatePage{Candidates: []dgh.Candidate{{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN"}}}}
	b := &refreshCounting{}
	s := newServerWith(t, func(d *Deps) { d.Explorer = fe; withBriefs(d, b) })
	if code, _, body := explorePost(t, s, "acme/api"); code != 200 || b.refreshes != 0 {
		t.Fatalf("%d %s, %d refreshes", code, body, b.refreshes)
	}
}

func TestExploreStoreBudget(t *testing.T) {
	t.Parallel()
	var cands []dgh.Candidate
	s := newServerLogged(t, change.NewLog(change.DefaultWindow, nil), func(d *Deps) {
		withBriefs(d, brief.New(brief.Deps{Store: d.PRs.(inventory.ByUnit).Store, Now: d.Now, Taxonomy: func(github.Ref) string { return brief.DefaultTaxonomy }}))
	})
	ctx := context.Background()
	st := s.store()
	at := s.deps.Now().Add(-time.Hour)
	for n := 1; n <= 30; n++ {
		ref := github.Ref{Owner: "acme", Repo: "web", Number: n}
		cands = append(cands, dgh.Candidate{Ref: ref, Title: "T", Author: "ana", State: dgh.StateOpen})
		if n%2 == 1 {
			continue
		}
		if err := st.UpsertPR(ctx, dinv.PR{Ref: ref, Title: "T", Author: "ana", State: dgh.StateOpen, Reasons: []string{"review-requested"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindPush, Key: "h", Actor: "ana", At: at}}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetOverride(ctx, dinv.Override{PR: ref, Tracked: true, Hold: n == 4}); err != nil {
			t.Fatal(err)
		}
		if err := st.WantBrief(ctx, ref, at); err != nil {
			t.Fatal(err)
		}
	}
	briefed := cands[3].Ref
	run := dinv.BriefRun{PR: briefed, Trigger: brief.TriggerBackground, StartedAt: at}
	id, err := st.StartBriefRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	run.ID, run.EndedAt, run.Outcome, run.Brief = id, at.Add(time.Second), "ok", `{"goal":"Export in batches"}`
	if err := st.EndBriefRun(ctx, run, dinv.BriefRecord{}); err != nil {
		t.Fatal(err)
	}
	base, at, err := s.readModel().Base(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := s.deriverAt(base, at)
	mctx, sc := metertest.Start(t, ctx)
	got, err := s.wireCandidates(mctx, cands)
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 6})
	if len(got) != 30 {
		t.Fatalf("%d candidates", len(got))
	}
	for i, c := range cands {
		if got[i].Unit != c.Ref.Unit() {
			t.Fatalf("%s: unit %+v", c.Ref, got[i].Unit)
		}
		if (got[i].Brief != nil) != (c.Ref == briefed) {
			t.Fatalf("%s: brief %+v", c.Ref, got[i].Brief)
		}
		pr, tracked, _ := st.PR(ctx, c.Ref)
		o, _ := st.Override(ctx, c.Ref)
		o.PR = c.Ref
		want := d.UntrackedRow(c, o).Actions
		if tracked {
			evs, _ := st.Events(ctx, c.Ref)
			want = d.Row(pr, evs, o).Actions
		}
		if !reflect.DeepEqual(got[i].Actions, want) {
			t.Fatalf("%s: actions\n got %+v\nwant %+v", c.Ref, got[i].Actions, want)
		}
	}
	if got[3].Brief.Brief.Goal != "Export in batches" {
		t.Fatalf("brief %+v", got[3].Brief)
	}
}
