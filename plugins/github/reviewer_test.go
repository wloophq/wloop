package github

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/row"
)

// ownNoReviewer stores ana's own pull request 7, green, with no reviewer asked, opened ten days before
// testAt.
func ownNoReviewer(t *testing.T, p *Plugin, checks, mergeable string) {
	t.Helper()
	ctx := context.Background()
	if err := p.store().SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: ref7, Title: "Change 7", Author: "ana", State: pr.StateOpen, HeadOid: "h7", URL: ref7.URL(),
		CreatedAt: testAt.Add(-10 * 24 * time.Hour), ChecksState: checks, Mergeable: mergeable, BaseBranch: "main", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
}

// snapshotOf is the plugin's snapshot of ref7 at testAt.
func snapshotOf(t *testing.T, p *Plugin) src.Snapshot {
	t.Helper()
	r, err := p.running()
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := r.snapshots(context.Background(), []pr.Ref{ref7})
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots %+v %v", snaps, err)
	}
	return snaps[0]
}

// soloRef is ana's pull request in a repository nobody reviews.
var soloRef = pr.Ref{Owner: "solo", Repo: "api", Number: 8}

// kindsOfAChunk stores ana's green pull requests ref7, of acme, and soloRef with no reviewer asked, and
// is the situation of each as one report chunk derives them, by owner.
func kindsOfAChunk(t *testing.T, h *fakeHost) map[string]string {
	t.Helper()
	p := started(t, &githubGraphQL{states: map[int]string{}}, h)
	ownNoReviewer(t, p, "SUCCESS", "MERGEABLE")
	if err := p.store().UpsertPR(context.Background(), pr.PR{Ref: soloRef, Title: "Change 8", Author: "ana", State: pr.StateOpen, HeadOid: "h8", URL: soloRef.URL(),
		CreatedAt: testAt.Add(-10 * 24 * time.Hour), ChecksState: "SUCCESS", Mergeable: "MERGEABLE", BaseBranch: "main", Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	r, err := p.running()
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := r.snapshots(context.Background(), []pr.Ref{ref7, soloRef})
	if err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots %+v %v", snaps, err)
	}
	out := map[string]string{}
	for _, s := range snaps {
		ref, _ := pr.RefOf(s.Unit)
		out[ref.Owner] = s.Situation.Kind
	}
	return out
}

func TestEachPullRequestOfAChunkTakesItsOwnVerdict(t *testing.T) {
	t.Parallel()
	got := kindsOfAChunk(t, &fakeHost{holds: func(fs []src.Fact) bool { return !ownerIs(fs, "solo") }})
	if got["solo"] != "merge" || got["acme"] != "no_reviewer" {
		t.Fatalf("situations %v, want solo merge and acme no_reviewer", got)
	}
}

func TestASetTheHostCannotEvaluateKeepsTheDefaultAlone(t *testing.T) {
	t.Parallel()
	h := &fakeHost{holds: func([]src.Fact) bool { return false }, failOn: func(fs []src.Fact) bool { return ownerIs(fs, "solo") }}
	if got := kindsOfAChunk(t, h); got["solo"] != "no_reviewer" || got["acme"] != "merge" {
		t.Fatalf("situations %v, want the failed solo set to require a reviewer and acme's verdict to stand", got)
	}
}

func TestAShortAnswerKeepsTheDefaultForEveryPullRequest(t *testing.T) {
	t.Parallel()
	h := &fakeHost{holds: func([]src.Fact) bool { return false }, short: true}
	if got := kindsOfAChunk(t, h); got["solo"] != "no_reviewer" || got["acme"] != "no_reviewer" {
		t.Fatalf("situations %v, want every pull request to require a reviewer", got)
	}
}

// TestReviewerRequiredReadsTheFactsThePluginHands checks that the facts plugin.toml lets reviewer_required
// read are the ones FactsOf hands the host, every one it can, so a predicate over any other is a profile
// problem.
func TestReviewerRequiredReadsTheFactsThePluginHands(t *testing.T) {
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(s.Predicates, func(p manifest.Predicate) bool { return p.Name == predicateReviewerRequired })
	if i < 0 {
		t.Fatalf("plugin.toml declares no %s", predicateReviewerRequired)
	}
	at := testAt.Add(-time.Hour)
	in := row.Input{PR: pr.PR{Ref: ref7, Author: "ana", State: pr.StateOpen, CreatedAt: at, MergedAt: at, ClosedAt: at},
		Events: []pr.Event{{PR: ref7, Kind: pr.KindComment, Actor: "bob", At: at}}, Viewer: "ana", Now: testAt}
	var handed []string
	for _, f := range row.FactsOf(in) {
		handed = append(handed, f.Name)
	}
	want := slices.Clone(s.Predicates[i].Facts)
	slices.Sort(handed)
	slices.Sort(want)
	if !slices.Equal(handed, want) {
		t.Errorf("FactsOf hands\n%v\nplugin.toml lets %s read\n%v", handed, predicateReviewerRequired, want)
	}
}

func itemIDs(s src.Snapshot) []string {
	var out []string
	for _, it := range s.Items {
		out = append(out, it.ID+" "+it.Summary)
	}
	return out
}

func TestAnOwnPullRequestWithNoReviewerWaitsForOneWhileTheProfileRequiresIt(t *testing.T) {
	t.Parallel()
	for name, h := range map[string]*fakeHost{
		"the bundled true":                {holds: func([]src.Fact) bool { return true }},
		"a host that cannot say":          {},
		"another owner of a solo profile": {holds: func(fs []src.Fact) bool { return !ownerIs(fs, "solo") }},
	} {
		t.Run(name, func(t *testing.T) {
			p := started(t, &githubGraphQL{states: map[int]string{}}, h)
			ownNoReviewer(t, p, "SUCCESS", "MERGEABLE")
			s := snapshotOf(t, p)
			if s.Situation.Kind != "no_reviewer" || !slices.ContainsFunc(itemIDs(s), func(id string) bool { return strings.HasPrefix(id, "gh:reviewer Pick a reviewer for") }) {
				t.Fatalf("situation %+v items %v, want no_reviewer asking to pick a reviewer", s.Situation, itemIDs(s))
			}
			got, err := p.Proposals(context.Background(), testAt)
			if err != nil || len(got.Drafts) != 1 {
				t.Fatalf("proposals %+v %v, want the nudge for a reviewer", got, err)
			}
		})
	}
}

func TestAnOwnPullRequestTheProfileNeedsNoReviewerForIsReadyToMerge(t *testing.T) {
	t.Parallel()
	solo := func(fs []src.Fact) bool { return !ownerIs(fs, "acme") }
	for _, c := range []struct {
		checks, mergeable, kind string
	}{
		{"SUCCESS", "MERGEABLE", "merge"},
		{"PENDING", "MERGEABLE", "merge"},
		{"FAILURE", "MERGEABLE", "checks"},
		{"SUCCESS", "CONFLICTING", "conflicts"},
	} {
		t.Run(c.kind+" "+c.checks, func(t *testing.T) {
			h := &fakeHost{holds: solo}
			p := started(t, &githubGraphQL{states: map[int]string{}}, h)
			ownNoReviewer(t, p, c.checks, c.mergeable)
			s := snapshotOf(t, p)
			if s.Situation.Kind != c.kind || slices.ContainsFunc(itemIDs(s), func(id string) bool { return strings.HasPrefix(id, "gh:reviewer") }) {
				t.Fatalf("situation %+v items %v, want %s and no reviewer to pick", s.Situation, itemIDs(s), c.kind)
			}
			if got, err := p.Proposals(context.Background(), testAt); err != nil || len(got.Drafts) != 0 {
				t.Fatalf("proposals %+v %v, want no nudge for a reviewer", got, err)
			}
			if asked := h.asked(); len(asked) == 0 || !ownerIs(asked[0], "acme") {
				t.Fatalf("the host was asked over %v, want the pull request's facts", asked)
			}
		})
	}
}

func ownerIs(fs []src.Fact, owner string) bool {
	return slices.ContainsFunc(fs, func(f src.Fact) bool { return f.Name == "pr.owner" && f.Value.String == owner })
}
