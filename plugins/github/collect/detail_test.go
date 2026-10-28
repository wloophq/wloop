package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/scope"
	"github.com/wloophq/wloop/plugins/github/store"
)

func TestThreadsOnlyForAuthoredOrDevelopedWithThreads(t *testing.T) {
	l := liveFixture(t)
	moved := now.Add(time.Minute)
	var changed, withThreads []api.Ref
	for i, r := range l.mine[:10] {
		l.gh.edit(r, func(p *hostedPR) {
			p.updated = moved
			if i < 7 {
				p.threads = 3 + i*10
			}
		})
		changed = append(changed, r)
		if i < 7 {
			withThreads = append(withThreads, r)
		}
	}
	for _, r := range append(slices.Clone(l.requested[:5]), l.linked[:5]...) {
		l.gh.edit(r, func(p *hostedPR) { p.updated, p.threads = moved, 4 })
		changed = append(changed, r)
	}
	withThreads = append(withThreads, l.linked[:5]...)
	rep, _ := l.fast(t)
	if rep.Fetched != 20 || l.gh.readReqs != 1 || len(l.gh.reads) != 20 {
		t.Fatalf("fetched %d in %d full-read requests, want 20 in 1", rep.Fetched, l.gh.readReqs)
	}
	if !slices.Equal(l.gh.threads, []int{12}) {
		t.Fatalf("thread requests %v, want one of 12 aliases: 7 authored, 5 linked to a workspace", l.gh.threads)
	}
	details, err := l.c.Store.Details(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range changed {
		d := details[r]
		read := slices.Contains(withThreads, r)
		want := (d.ThreadsTotal + 1) / 2
		if d.ThreadsTotal > api.MaxThreads {
			want = api.MaxThreads / 2
		}
		switch {
		case read && (!d.ThreadsRead || len(d.OpenThreads) != want):
			t.Errorf("%s: %d open of %d threads (read %v), want %d", r, len(d.OpenThreads), d.ThreadsTotal, d.ThreadsRead, want)
		case !read && slices.Contains(l.mine, r) && (!d.ThreadsRead || d.ThreadsTotal != 0):
			t.Errorf("%s: authored without threads: total %d, read %v", r, d.ThreadsTotal, d.ThreadsRead)
		case !read && !slices.Contains(l.mine, r) && (d.ThreadsRead || d.ThreadsTotal != 4 || d.OpenThreads != nil):
			t.Errorf("%s: neither authored nor developed: total %d, read %v, open %v", r, d.ThreadsTotal, d.ThreadsRead, d.OpenThreads)
		}
	}
}

func TestDetailVersionForcesRead(t *testing.T) {
	l := liveFixture(t)
	ctx := context.Background()
	ref := l.requested[4]
	stored, _, err := l.c.Store.PR(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.c.Store.SaveSnapshots(ctx, []store.Snapshot{{PR: stored, Detail: &store.Detail{Version: 0, ReadAt: l.at}}}, l.at); err != nil {
		t.Fatal(err)
	}
	rep, _ := l.fast(t)
	if rep.Fetched != 1 || !slices.Equal(l.gh.reads, []api.Ref{ref}) {
		t.Fatalf("read %v, want %s", l.gh.reads, ref)
	}
	d, err := l.c.Store.Details(ctx, []api.Ref{ref})
	if err != nil || d[ref].Version != store.DetailVersion || !d[ref].ReadAt.Equal(l.at) {
		t.Fatalf("stored %+v: %v", d[ref], err)
	}
	l.gh.reset()
	if rep, _ := l.fast(t); rep.Fetched != 0 {
		t.Fatalf("read %v again", l.gh.reads)
	}
}

func TestStaleDetailIsReRead(t *testing.T) {
	l := liveFixture(t)
	maxAge := l.c.DetailMaxAge()
	if maxAge != l.c.Cfg().Slow || maxAge <= 0 {
		t.Fatalf("DetailMaxAge %v, sweep.slow %v", maxAge, l.c.Cfg().Slow)
	}
	l.at = now.Add(maxAge - time.Minute)
	if rep, _ := l.fast(t); rep.Fetched != 0 {
		t.Fatalf("a detail younger than %v was read: %v", maxAge, l.gh.reads)
	}
	l.at = now.Add(maxAge + time.Minute)
	mark, events := l.changes.mark(), l.host.count()
	rep, _ := l.fast(t)
	if rep.Fetched != 69 || len(l.gh.reads) != 69 || l.gh.readReqs != 4 {
		t.Fatalf("fetched %d in %d requests, want 69 in 4", rep.Fetched, l.gh.readReqs)
	}
	if got := l.changes.since(mark); len(got) != 0 || l.host.count() != events {
		t.Fatalf("re-reading unchanged pull requests changed %v and recorded %d events", got, l.host.count()-events)
	}
	d, err := l.c.Store.Details(context.Background(), l.tracked)
	if err != nil || !d[l.tracked[0]].ReadAt.Equal(l.at) {
		t.Fatalf("read_at %v, want %v: %v", d[l.tracked[0]].ReadAt, l.at, err)
	}
	l.gh.reset()
	l.at = l.at.Add(2 * time.Minute)
	if rep, _ := l.fast(t); rep.Fetched != 0 {
		t.Fatalf("re-read %v right after the resync", l.gh.reads)
	}
}

type obj = map[string]any

func list(items ...obj) obj {
	if items == nil {
		items = []obj{}
	}
	return obj{"nodes": items}
}

// cannedPR is a pull request with every field the reviewer, author and full
// reads select, in the shape of the cli harness payloads.
func cannedPR(number int, author, decision, crOid string, threads []bool) obj {
	head := "c" + author
	actor := func(login string) obj {
		if strings.HasSuffix(login, "-bot") {
			return obj{"__typename": "Bot", "login": login}
		}
		return obj{"__typename": "User", "login": login}
	}
	review := func(id, state, by, at string, comments int) obj {
		return obj{"id": id, "state": state, "submittedAt": at, "author": actor(by), "viewerDidAuthor": by == "me",
			"commit": obj{"oid": head}, "comments": obj{"totalCount": comments}}
	}
	reviews := []obj{
		review("r1", "COMMENTED", "bob", "2026-10-08T09:00:00Z", 2),
		review("r2", "APPROVED", "carol", "2026-10-08T11:00:00Z", 1),
		review("r3", "CHANGES_REQUESTED", "me", "2026-10-08T12:00:00Z", 0),
		review("r4", "APPROVED", "dan", "2026-10-08T12:30:00Z", 0),
		review("r5", "COMMENTED", "lint-bot", "2026-10-08T12:40:00Z", 3),
	}
	cr := list()
	if crOid != "" {
		cr = list(obj{"id": "x", "state": "CHANGES_REQUESTED", "submittedAt": "2026-10-08T10:00:00Z", "author": obj{"login": "bob"}, "commit": obj{"oid": crOid}})
	}
	var threadNodes []obj
	for i, resolved := range threads {
		by := "bob"
		if i == 0 {
			by = "lint-bot"
		}
		threadNodes = append(threadNodes, obj{"isResolved": resolved, "comments": list(obj{"createdAt": "2026-10-08T1" + string(rune('0'+i)) + ":15:00Z", "author": actor(by)})})
	}
	return obj{
		"number": number, "url": "", "title": "t", "state": "OPEN", "isDraft": false,
		"headRefName": "feat/" + author, "headRefOid": head, "baseRefName": "main", "isCrossRepository": false, "authorAssociation": "MEMBER",
		"reviewDecision": decision, "mergeable": "MERGEABLE",
		"createdAt": "2026-10-07T09:00:00Z", "updatedAt": "2026-10-08T13:00:00Z",
		"repository":          obj{"nameWithOwner": "acme/api"},
		"headRepositoryOwner": obj{"login": "acme"},
		"author":              obj{"__typename": "User", "login": author},
		"labels":              list(obj{"name": "backend"}, obj{"name": "needs-qa"}),
		"reviews":             list(reviews...),
		"latestReviews":       list(reviews[1:]...),
		"changesRequested":    cr,
		"comments":            list(obj{"id": "c1", "createdAt": "2026-10-08T13:00:00Z", "author": actor("alice")}, obj{"id": "c2", "createdAt": "2026-10-08T13:05:00Z", "author": actor("lint-bot")}),
		"reviewRequests":      list(obj{"requestedReviewer": obj{"__typename": "User", "login": "me"}}, obj{"requestedReviewer": obj{"__typename": "Team", "combinedSlug": "acme/core"}}),
		"commits":             list(obj{"commit": obj{"oid": "c0", "committedDate": "2026-10-07T09:00:00Z", "parents": obj{"totalCount": 1}}}, obj{"commit": obj{"oid": head, "committedDate": "2026-10-08T09:00:00Z", "parents": obj{"totalCount": 1}}}),
		"reviewThreads":       obj{"totalCount": len(threads), "nodes": threadNodes},
		"headCommit": list(obj{"commit": obj{"oid": head, "statusCheckRollup": obj{"state": "FAILURE", "contexts": list(
			obj{"name": "lint", "status": "COMPLETED", "conclusion": "FAILURE", "title": "2 errors", "detailsUrl": "https://ci/1"},
			obj{"name": "test", "status": "IN_PROGRESS", "conclusion": ""},
			obj{"context": "deploy", "state": "SUCCESS"},
		)}}}),
	}
}

// canned answers the reviewer, author, full and thread reads from cannedPR
// payloads, keeping of each what its query selects that the others do not.
type canned struct{ prs map[int]obj }

var cannedAlias = regexp.MustCompile(`(pr\d+): repository\(owner: "[^"]*", name: "[^"]*"\) \{ pullRequest\(number: (\d+)\) \{ ([^ ]+)`)

func (c canned) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload struct {
		Query string `json:"query"`
	}
	raw, _ := io.ReadAll(req.Body)
	req.Body.Close()
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	data := obj{"viewer": obj{"login": "me"}, "rateLimit": obj{"cost": 1, "remaining": 4900, "limit": 5000, "resetAt": now.Add(time.Hour)}}
	for _, m := range cannedAlias.FindAllStringSubmatch(payload.Query, -1) {
		var n int
		json.Unmarshal([]byte(m[2]), &n)
		pr, err := project(c.prs[n], m[3])
		if err != nil {
			return nil, err
		}
		data[m[1]] = obj{"pullRequest": pr}
	}
	body, _ := json.Marshal(obj{"data": data})
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

// project drops from pr what the selection does not read: the full read
// counts threads and asks nothing of the viewer; the thread read is threads
// only; the reviewer and author reads ask __typename of the pull request's
// author alone.
func project(pr obj, selection string) (obj, error) {
	raw, err := json.Marshal(pr)
	if err != nil {
		return nil, err
	}
	var out obj
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	switch {
	case selection == "...inv":
		out["reviewThreads"] = obj{"totalCount": out["reviewThreads"].(obj)["totalCount"]}
		for _, r := range out["reviews"].(obj)["nodes"].([]any) {
			delete(r.(obj), "viewerDidAuthor")
		}
	case strings.HasPrefix(selection, "reviewThreads"):
		return obj{"reviewThreads": out["reviewThreads"]}, nil
	default:
		author := out["author"]
		stripTypenames(out)
		out["author"] = author
	}
	return out, nil
}

func stripTypenames(v any) {
	switch v := v.(type) {
	case obj:
		delete(v, "__typename")
		for _, c := range v {
			stripTypenames(c)
		}
	case []any:
		for _, c := range v {
			stripTypenames(c)
		}
	}
}

// sweepReads are what the sweep's reviewer and author reads by reference
// returned for the cannedPR payloads, before the sweep planned from the
// store.
func sweepReads(t *testing.T) (viewer string, reviewer, author map[api.Ref]*pr.PullRequest) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "sweep_reads.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Viewer string
		PRs    map[string]struct{ Reviewer, Author *pr.PullRequest }
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	reviewer, author = map[api.Ref]*pr.PullRequest{}, map[api.Ref]*pr.PullRequest{}
	for key, p := range golden.PRs {
		ref, err := pr.ParseKey(key)
		if err != nil {
			t.Fatal(err)
		}
		reviewer[ref], author[ref] = p.Reviewer, p.Author
	}
	return golden.Viewer, reviewer, author
}

func TestDetailGivesWhatTheSweepRead(t *testing.T) {
	ctx := context.Background()
	gh := canned{prs: map[int]obj{
		7:  cannedPR(7, "alice", "REVIEW_REQUIRED", "", []bool{false, true}),
		9:  cannedPR(9, "me", "APPROVED", "", nil),
		10: cannedPR(10, "me", "CHANGES_REQUESTED", "cme", []bool{false, true, false}),
		11: cannedPR(11, "zoe", "", "", nil),
	}}
	gh.prs[11]["isCrossRepository"], gh.prs[11]["headRepositoryOwner"] = true, obj{"login": "zoe"}
	client := apitest.Client(gh)
	refs := []api.Ref{{Owner: "acme", Repo: "api", Number: 7}, {Owner: "acme", Repo: "api", Number: 9}, {Owner: "acme", Repo: "api", Number: 10}, {Owner: "acme", Repo: "api", Number: 11}}
	viewer, reviewer, author := sweepReads(t)

	c := &Collector{GH: client, Cfg: configured(Config{ReviewPrefix: "REVIEW ", RateLimitFraction: 0.5, Slow: time.Hour}), Now: func() time.Time { return now }}
	c.Store, _ = openStore(t, c.Now)
	var rep Report
	if unread, err := c.read(ctx, refs, scope.Resolved{Viewer: "me"}, now, &rep); err != nil || len(unread) != 0 {
		t.Fatalf("full read: %v %v %v", unread, err, rep.Warnings)
	}
	rows, err := c.Store.PRs(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	details, err := c.Store.Details(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}

	noContent := func(rs []pr.Review) []pr.Review {
		out := slices.Clone(rs)
		for i := range out {
			out[i].HasContent = false
		}
		return out
	}
	noViewer := func(rs []pr.Review) []pr.Review {
		out := slices.Clone(rs)
		for i := range out {
			out[i].ByViewer = false
		}
		return out
	}
	type verdict struct {
		Name            string
		Pending, Failed bool
	}
	verdicts := func(cs []pr.Check) []verdict {
		var out []verdict
		for _, c := range cs {
			out = append(out, verdict{c.Name, c.Pending, c.Failed})
		}
		return out
	}
	for _, ref := range refs {
		row, ok := rows[ref]
		d, hasDetail := details[ref]
		if !ok || !hasDetail {
			t.Fatalf("%s: row %v, detail %v", ref, ok, hasDetail)
		}
		got, rev, auth := githubOf(row, d, "me"), reviewer[ref], author[ref]
		headOwner := rev.HeadRepositoryOwner
		if rev.IsCrossRepository {
			headOwner = ""
		}
		fields := []struct {
			name      string
			got, want any
		}{
			{"Ref", got.Ref, rev.Ref},
			{"State", got.State, rev.State},
			{"IsDraft", got.IsDraft, rev.IsDraft},
			{"Title", got.Title, rev.Title},
			{"HeadRefName", got.HeadRefName, rev.HeadRefName},
			{"HeadRefOid", got.HeadRefOid, rev.HeadRefOid},
			{"IsCrossRepository", got.IsCrossRepository, rev.IsCrossRepository},
			{"HeadRepositoryOwner", got.HeadRepositoryOwner, headOwner},
			{"Author", got.Author, rev.Author},
			{"AuthorAssociation", got.AuthorAssociation, rev.AuthorAssociation},
			{"Comments", got.Comments, rev.Comments},
			{"ReviewRequests", got.ReviewRequests, rev.ReviewRequests},
			{"ViewerRequested", got.ViewerRequested, slices.Contains(rev.ReviewRequests, viewer)},
			{"Commits", got.Commits, rev.Commits},
			{"Reviews (reviewer)", noContent(got.Reviews), noContent(rev.Reviews)},
			{"Reviews (author)", noViewer(got.Reviews), noViewer(auth.Reviews)},
			{"BaseRefName", got.BaseRefName, auth.BaseRefName},
			{"ReviewDecision", got.ReviewDecision, auth.ReviewDecision},
			{"Mergeable", got.Mergeable, auth.Mergeable},
			{"ChecksState", got.ChecksState, auth.ChecksState},
			{"Checks", verdicts(got.Checks), verdicts(auth.Checks)},
			{"LatestReviews", got.LatestReviews, auth.LatestReviews},
			{"NewestChangesRequestOid", got.NewestChangesRequestOid, auth.NewestChangesRequestOid},
			{"ThreadsTotal", got.ThreadsTotal, auth.ThreadsTotal},
			{"Labels", got.Labels, []string{"backend", "needs-qa"}},
		}
		if got.Author == viewer {
			fields = append(fields, struct {
				name      string
				got, want any
			}{"OpenThreads", got.OpenThreads, auth.OpenThreads})
		} else if got.OpenThreads != nil || (d.ThreadsRead && d.ThreadsTotal > 0) {
			t.Errorf("%s: threads read for a pull request the viewer did not author: %v", ref, got.OpenThreads)
		}
		for _, f := range fields {
			g, _ := json.Marshal(f.got)
			w, _ := json.Marshal(f.want)
			if !bytes.Equal(g, w) {
				t.Errorf("%s %s:\n got %s\nwant %s", ref, f.name, g, w)
			}
		}
	}
	bot := func(login string) bool { return login == "lint-bot" }
	if pr10 := githubOf(rows[refs[2]], details[refs[2]], "me"); len(pr10.OpenThreads) != 2 || !slices.ContainsFunc(pr10.Reviews, func(r pr.Review) bool { return r.ByViewer }) ||
		pr10.OpenThreads[0].LastAuthor != "lint-bot" || !slices.ContainsFunc(pr10.Comments, func(c pr.Comment) bool { return bot(c.Author) }) ||
		!slices.ContainsFunc(pr10.LatestReviews, func(r pr.Review) bool { return bot(r.Author) }) || !rows[refs[3]].IsFork {
		t.Fatalf("the fixture exercises no open thread or viewer review: %+v", pr10)
	}
}

func TestFailedThreadReadKeepsTheStoredThreads(t *testing.T) {
	l := liveFixture(t)
	ctx := context.Background()
	ref := l.mine[0]
	firstAt := l.at
	l.gh.edit(ref, func(p *hostedPR) { p.updated, p.threads = now.Add(time.Minute), 3 })
	l.fast(t)
	before, err := l.c.Store.Details(ctx, []api.Ref{ref})
	if err != nil || len(before[ref].OpenThreads) != 2 || !before[ref].ThreadsRead {
		t.Fatalf("first read %+v: %v", before[ref], err)
	}

	l.gh.reset()
	l.gh.failThreads = true
	l.at = firstAt.Add(2 * time.Minute)
	l.gh.edit(ref, func(p *hostedPR) { p.updated, p.threads = now.Add(3*time.Minute), 5 })
	fresh := l.gh.add(hostedPR{ref: api.Ref{Owner: "acme", Repo: "api", Number: 900}, author: "me", state: api.StateOpen, updated: now.Add(3 * time.Minute), head: "x", checks: "SUCCESS", threads: 2}).ref
	if rep, _ := l.fast(t); rep.Fetched != 2 || len(l.gh.threads) != 1 {
		t.Fatalf("fetched %d, thread requests %v", rep.Fetched, l.gh.threads)
	}
	after, err := l.c.Store.Details(ctx, []api.Ref{ref, fresh})
	if err != nil {
		t.Fatal(err)
	}
	if d := after[ref]; d.ThreadsTotal != 5 || !d.ThreadsRead || len(d.OpenThreads) != 2 || !d.ReadAt.Equal(firstAt) {
		t.Fatalf("after a failed thread read: total %d, read %v, open %d, read at %v (first %v)", d.ThreadsTotal, d.ThreadsRead, len(d.OpenThreads), d.ReadAt, firstAt)
	}
	if d := after[fresh]; d.ThreadsRead || d.Fresh(l.at, l.c.DetailMaxAge()) {
		t.Fatalf("a never-read thread set is fresh: %+v", d)
	}

	l.gh.reset()
	l.gh.failThreads = false
	l.at = l.at.Add(2 * time.Minute)
	l.fast(t)
	if !slices.Equal(l.gh.reads, []api.Ref{fresh}) || !slices.Equal(l.gh.threads, []int{1}) {
		t.Fatalf("next run read %v with thread requests %v, want %s again", l.gh.reads, l.gh.threads, fresh)
	}
	if d, _ := l.c.Store.Details(ctx, []api.Ref{fresh}); !d[fresh].ThreadsRead || len(d[fresh].OpenThreads) != 1 {
		t.Fatalf("retried thread read: %+v", d[fresh])
	}
}

// githubOf is p as a by-ref read would have returned it to the sweep, from
// the row and its detail d, as the host reads them: viewer sets ByViewer on
// reviews and ViewerRequested, a bot author loses the "[bot]" the full read
// gives it, and a same-repository head is owned by the base's owner.
func githubOf(p pr.PR, d store.Detail, viewer string) *pr.PullRequest {
	byViewer := func(login string) bool { return viewer != "" && strings.EqualFold(login, viewer) }
	bare := func(login string) string { return strings.TrimSuffix(login, "[bot]") }
	f := pr.Facts{
		Ref: p.Ref, State: p.State, IsDraft: p.IsDraft, Title: p.Title,
		HeadRefName: p.HeadBranch, BaseRefName: p.BaseBranch, HeadRefOid: p.HeadOid, IsCrossRepository: p.IsFork,
		Visibility: d.Visibility, Author: p.Author, AuthorAssociation: d.AuthorAssociation,
		ReviewDecision: p.ReviewDecision, Mergeable: p.Mergeable, ChecksState: p.ChecksState,
		NewestChangesRequestOid: d.NewestChangesRequestOid,
		LatestReviews:           slices.Clone(d.LatestReviews),
		Comments:                slices.Clone(d.Comments),
		ReviewRequests:          slices.Clone(p.ReviewRequests),
		ViewerRequested:         slices.ContainsFunc(p.ReviewRequests, byViewer),
		Commits:                 slices.Clone(d.Commits),
		OpenThreads:             slices.Clone(d.OpenThreads),
		ThreadsTotal:            d.ThreadsTotal,
		Checks:                  slices.Clone(d.Checks),
		Labels:                  slices.Clone(d.Labels),
	}
	for _, r := range d.Reviews {
		r.Author = bare(r.Author)
		r.ByViewer = byViewer(r.Author)
		f.Reviews = append(f.Reviews, r)
	}
	for i := range f.LatestReviews {
		f.LatestReviews[i].Author = bare(f.LatestReviews[i].Author)
	}
	for i := range f.Comments {
		f.Comments[i].Author = bare(f.Comments[i].Author)
	}
	for i := range f.OpenThreads {
		f.OpenThreads[i].LastAuthor = bare(f.OpenThreads[i].LastAuthor)
	}
	out := &pr.PullRequest{Facts: f}
	if !p.IsFork {
		out.HeadRepositoryOwner = p.Ref.Owner
	}
	return out
}
