package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
	"github.com/wloophq/wloop/plugins/github/pr"
)

var testAt = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// prNode is a pull request of acme/api as GitHub's full read answers it.
func prNode(number int, state string) string {
	return fmt.Sprintf(`{"number":%d,"title":"Change %d","state":%q,"isDraft":false,"url":"https://github.com/acme/api/pull/%d",
 "createdAt":"2026-10-19T10:00:00Z","updatedAt":"2026-10-20T09:00:00Z","mergedAt":null,"closedAt":null,
 "headRefName":"feat/%d","baseRefName":"main","headRefOid":"h%d","isCrossRepository":false,
 "repository":{"nameWithOwner":"acme/api","visibility":"PRIVATE"},"author":{"login":"ana"},"authorAssociation":"MEMBER",
 "commits":{"nodes":[{"commit":{"oid":"h%d","committedDate":"2026-10-19T10:00:00Z","parents":{"totalCount":1}}}]},
 "reviews":{"nodes":[]},"comments":{"nodes":[]},"reviewRequests":{"nodes":[]},"timelineItems":{"nodes":[]},"headCommit":{"nodes":[]}}`,
		number, number, state, number, number, number, number)
}

// githubGraphQL answers the plugin's GraphQL reads: the viewer, empty
// searches, and the full read of the pull requests of acme/api states
// holds, by number.
type githubGraphQL struct {
	mu     sync.Mutex
	states map[int]string
	reads  []int
	// basics answers the basics read of a pull request; "" answers none.
	basics string
}

var prAlias = regexp.MustCompile(`(pr\d+): repository\(owner: "acme", name: "api"\) \{ pullRequest\(number: (\d+)\)`)

func (g *githubGraphQL) Run(_ context.Context, args ...string) ([]byte, error) {
	q := args[len(args)-1]
	g.mu.Lock()
	defer g.mu.Unlock()
	data := map[string]json.RawMessage{"rateLimit": json.RawMessage(`{"cost":1,"remaining":4900,"limit":5000,"resetAt":"2026-10-20T13:00:00Z"}`),
		"viewer": json.RawMessage(`{"login":"ana"}`)}
	for _, m := range prAlias.FindAllStringSubmatch(q, -1) {
		n, _ := strconv.Atoi(m[2])
		g.reads = append(g.reads, n)
		if st, ok := g.states[n]; ok {
			data[m[1]] = json.RawMessage(`{"pullRequest":` + prNode(n, st) + `}`)
		}
	}
	if g.basics != "" && strings.Contains(q, "pr: repository(") {
		data["pr"] = json.RawMessage(`{"pullRequest":` + g.basics + `}`)
	}
	for i := range strings.Count(q, "search(") {
		data["s"+strconv.Itoa(i)] = json.RawMessage(`{"issueCount":0,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}`)
	}
	return json.Marshal(map[string]any{"data": data})
}

// fakeHost is the host's broker as a test plays it: the snapshots the
// plugin reports, its events, the configuration and a listing.
type fakeHost struct {
	mu      sync.Mutex
	marks   []map[string]string
	reports [][]src.Snapshot
	events  []src.Event
	topics  [][]sdk.Topic
	listing src.Listing
	samples []src.Sample
	books   []src.Book
}

func (h *fakeHost) host() src.Host {
	return src.Host{Reporter: h, Recorder: h, History: h, Reader: h, Services: h}
}

func (h *fakeHost) Report(_ context.Context, snaps []src.Snapshot) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reports = append(h.reports, snaps)
	return nil
}

func (h *fakeHost) Changed(_ context.Context, _ []sdk.Unit, topics []sdk.Topic) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.topics = append(h.topics, topics)
	return nil
}

func (h *fakeHost) RecordEvents(_ context.Context, evs []src.Event) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range evs {
		if !slices.ContainsFunc(h.events, func(x src.Event) bool {
			return x.Unit == e.Unit && x.Kind == e.Kind && x.Key == e.Key && (e.Key != "" || x.At.Equal(e.At))
		}) {
			h.events = append(h.events, e)
		}
	}
	return nil
}

func (h *fakeHost) SetMarks(_ context.Context, _ sdk.Unit, marks map[string]string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.marks = append(h.marks, maps.Clone(marks))
	return nil
}

// markSets are the marks the plugin set, one map a call.
func (h *fakeHost) markSets() []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.marks)
}

func (*fakeHost) PutBrief(context.Context, src.Brief) error { return nil }

func (h *fakeHost) Events(_ context.Context, q src.EventQuery) ([]src.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []src.Event
	for _, e := range h.events {
		if slices.Contains(q.Units, e.Unit) && (len(q.Kinds) == 0 || slices.Contains(q.Kinds, e.Kind)) && !e.At.Before(q.Since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (*fakeHost) Tasks(context.Context, []sdk.Unit) ([]src.Task, error) { return nil, nil }

func (h *fakeHost) Books(_ context.Context, units []sdk.Unit) ([]src.Book, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []src.Book
	for _, b := range h.books {
		if slices.Contains(units, b.Unit) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (*fakeHost) RoundState(context.Context, sdk.Unit) (src.RoundState, error) {
	return src.RoundState{}, nil
}

func (h *fakeHost) Workspaces(context.Context, string) (src.Listing, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listing, nil
}

func (*fakeHost) Session(context.Context, string) (src.Session, error) { return src.Session{}, nil }

func (*fakeHost) Ask(context.Context, src.AskRequest) (src.AskResult, error) {
	return src.AskResult{}, src.ErrUnsupported
}

func (h *fakeHost) Meter(_ context.Context, s src.Sample) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, s)
	return nil
}

func (*fakeHost) Config(context.Context) (src.Config, error) {
	return src.Config{Plugin: json.RawMessage(`{"scope":{"people":["ana"]}}`), Host: json.RawMessage(`{"sweep":{"fast":"2m","slow":"1h"}}`)}, nil
}

// Profile reads the plugin's own profile layer.
func (*fakeHost) Profile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(filepath.Join("profile", filepath.FromSlash(path)))
}

// reported are the snapshots of ref the plugin reported, in order.
func (h *fakeHost) reported(ref pr.Ref) []Data {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Data
	for _, r := range h.reports {
		for _, s := range r {
			if s.Unit == ref.Unit() {
				var d Data
				if json.Unmarshal(s.Data, &d) == nil {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

func (h *fakeHost) kinds(ref pr.Ref) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, e := range h.events {
		if e.Unit == ref.Unit() {
			out = append(out, e.Kind)
		}
	}
	return out
}

// started is a plugin over gql's GitHub, started on a fresh state
// directory with h as its broker, and loaded.
func started(t *testing.T, gql *githubGraphQL, h *fakeHost) *Plugin {
	t.Helper()
	p := &Plugin{GH: &fakeGH{answers: map[string]string{}}, API: apitest.Options(gql), Now: func() time.Time { return testAt }, Git: noCheckouts{}}
	ctx := context.Background()
	if err := p.Start(ctx, src.StartRequest{StateDir: t.TempDir(), Host: h.host()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(context.Background()) })
	p.loading.Wait()
	return p
}

var ref7 = pr.Ref{Owner: "acme", Repo: "api", Number: 7}

func TestATrackReadsFirstAndStoresAndReportsOnlyWhenFollowed(t *testing.T) {
	t.Parallel()
	gql, h := &githubGraphQL{states: map[int]string{7: "OPEN"}}, &fakeHost{}
	p := started(t, gql, h)
	ctx := context.Background()
	got, err := p.Track(ctx, src.TrackRequest{Unit: ref7.Unit()})
	if err != nil || got.Unit != ref7.Unit() || got.State != "OPEN" {
		t.Fatalf("read %+v %v", got, err)
	}
	if _, ok, _ := p.store().PR(ctx, ref7); ok || len(h.reported(ref7)) != 0 || len(h.kinds(ref7)) != 0 {
		t.Fatalf("a read not followed stored, reported or recorded: reported %v, events %v", h.reported(ref7), h.kinds(ref7))
	}
	if _, err := p.Track(ctx, src.TrackRequest{Unit: ref7.Unit(), Follow: true}); err != nil {
		t.Fatal(err)
	}
	rows := h.reported(ref7)
	if len(rows) == 0 || !rows[len(rows)-1].Tracked || rows[len(rows)-1].PR.Title != "Change 7" || !slices.Contains(rows[len(rows)-1].PR.Reasons, pr.ReasonTracked) {
		t.Fatalf("the follow reported %+v", rows)
	}
	if !slices.Contains(h.kinds(ref7), pr.KindTracked) || !slices.Contains(h.kinds(ref7), pr.KindFollowed) {
		t.Fatalf("the host holds %v, want the track and its flip", h.kinds(ref7))
	}
	if gql.reads[len(gql.reads)-1] != 7 || len(gql.reads) != 1 {
		t.Fatalf("GitHub read %v, want the one read the follow stored", gql.reads)
	}
}

func TestATrackOfAFinishedPullRequestIsRefusedAndStoresNothing(t *testing.T) {
	t.Parallel()
	h := &fakeHost{}
	p := started(t, &githubGraphQL{states: map[int]string{7: "MERGED"}}, h)
	ctx := context.Background()
	if _, err := p.Track(ctx, src.TrackRequest{Unit: ref7.Unit()}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Track(ctx, src.TrackRequest{Unit: ref7.Unit(), Follow: true})
	if !errors.Is(err, src.ErrUnfit) || !strings.Contains(err.Error(), "is merged") {
		t.Fatalf("follow: %v, want a refusal", err)
	}
	if _, ok, _ := p.store().PR(ctx, ref7); ok || len(h.reported(ref7)) != 0 {
		t.Fatal("a refused track stored the pull request")
	}
}

func TestATrackPastTheCapIsRefused(t *testing.T) {
	t.Parallel()
	p := started(t, &githubGraphQL{states: map[int]string{}}, &fakeHost{})
	ctx := context.Background()
	for n := 1; n <= 50; n++ {
		if _, err := p.store().SetTracked(ctx, pr.Ref{Owner: "acme", Repo: "web", Number: n}, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Track(ctx, src.TrackRequest{Unit: ref7.Unit()}); !errors.Is(err, src.ErrUnfit) || !strings.Contains(err.Error(), "already tracking 50") {
		t.Fatalf("read past the cap: %v", err)
	}
	if err := p.Untrack(ctx, pr.Ref{Owner: "ACME", Repo: "Web", Number: 3}.Unit()); err != nil {
		t.Fatalf("untrack under another case: %v", err)
	}
	if err := p.Untrack(ctx, pr.Ref{Owner: "acme", Repo: "web", Number: 3}.Unit()); !errors.Is(err, src.ErrUnfit) {
		t.Fatalf("a second untrack: %v", err)
	}
}

func TestTheFastJobReportsWhatItRead(t *testing.T) {
	t.Parallel()
	gql, h := &githubGraphQL{states: map[int]string{7: "OPEN"}}, &fakeHost{}
	p := started(t, gql, h)
	ctx := context.Background()
	if _, err := p.store().SetTracked(ctx, ref7, true); err != nil {
		t.Fatal(err)
	}
	res, err := p.Ingest(ctx, src.IngestRequest{Job: JobFast})
	if err != nil {
		t.Fatalf("fast: %+v %v", res, err)
	}
	if rows := h.reported(ref7); len(rows) == 0 || rows[len(rows)-1].PR.HeadOid != "h7" || rows[len(rows)-1].Detail == nil {
		t.Fatalf("the fast job reported %+v", rows)
	}
	if _, err := p.Ingest(ctx, src.IngestRequest{Job: "nightly"}); !errors.Is(err, src.ErrUnsupported) {
		t.Fatalf("an unknown job: %v", err)
	}
}

func TestTheForwardIsTheHostsOwn(t *testing.T) {
	t.Parallel()
	p := started(t, &githubGraphQL{}, &fakeHost{})
	args, _ := json.Marshal(map[string]any{"method": "GET", "path": "user"})
	for _, actor := range []string{src.ActorPerson, src.ActorAgent} {
		if _, err := p.Action(context.Background(), src.ActionRequest{Action: ActionForward, Args: args, Actor: actor}); !errors.Is(err, src.ErrUnsupported) {
			t.Errorf("a forward a %s asked for: %v", actor, err)
		}
	}
	if _, err := p.Action(context.Background(), src.ActionRequest{Unit: ref7.Unit(), Action: ActionForward, Args: args}); !errors.Is(err, src.ErrUnsupported) {
		t.Errorf("a forward named as a unit's action: %v", err)
	}
}

func TestTheCheckoutIsTheStoredHeadAndBase(t *testing.T) {
	t.Parallel()
	p := started(t, &githubGraphQL{states: map[int]string{7: "OPEN"}}, &fakeHost{})
	ctx := context.Background()
	if _, err := p.Action(ctx, src.ActionRequest{Unit: ref7.Unit(), Action: ActionRefresh}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := p.Checkout(ctx, ref7.Unit())
	if want := (src.Checkout{Repo: "acme/api", Branch: "feat/7", Head: "h7", Base: "main"}); err != nil || !ok || got != want {
		t.Fatalf("checkout %+v %v %v, want %+v", got, ok, err, want)
	}
	if _, ok, _ := p.Checkout(ctx, sdk.Unit{Provider: "slack", Key: "T/C/1"}); ok {
		t.Fatal("a unit of another provider checks out")
	}
}

// gone are the units the plugin reported gone.
func (h *fakeHost) gone() []sdk.Unit {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []sdk.Unit
	for _, r := range h.reports {
		for _, s := range r {
			if s.Gone {
				out = append(out, s.Unit)
			}
		}
	}
	return out
}

func TestAnEndedPullRequestPastTheWindowIsDroppedAndNeverReportedAgain(t *testing.T) {
	t.Parallel()
	dir, ctx := t.TempDir(), context.Background()
	old := pr.Ref{Owner: "acme", Repo: "api", Number: 2}
	ended := testAt.Add(-401 * 24 * time.Hour)
	first := &Plugin{GH: &fakeGH{answers: map[string]string{}}, API: apitest.Options(&githubGraphQL{}), Now: func() time.Time { return testAt }}
	if err := first.Start(ctx, src.StartRequest{StateDir: dir, Host: (&fakeHost{}).host()}); err != nil {
		t.Fatal(err)
	}
	first.loading.Wait()
	for _, p := range []pr.PR{{Ref: old, State: "MERGED", MergedAt: ended, ClosedAt: ended}, {Ref: ref7, State: "CLOSED", ClosedAt: testAt.Add(-time.Hour)}} {
		if err := first.store().UpsertPR(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	h := &fakeHost{}
	p := &Plugin{GH: &fakeGH{answers: map[string]string{}}, API: apitest.Options(&githubGraphQL{}), Now: func() time.Time { return testAt }}
	if err := p.Start(ctx, src.StartRequest{StateDir: dir, Host: h.host()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(context.Background()) })
	p.loading.Wait()
	if _, ok, _ := p.store().PR(ctx, old); ok {
		t.Fatal("the plugin keeps a pull request the host's retention ended")
	}
	if len(h.reported(old)) != 0 || !slices.Contains(h.gone(), old.Unit()) {
		t.Fatalf("the load reported %v of it, gone %v", h.reported(old), h.gone())
	}
	if len(h.reported(ref7)) == 0 {
		t.Fatal("the load did not report a recently closed pull request")
	}
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: old, State: "MERGED", MergedAt: ended, ClosedAt: ended}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ingest(ctx, src.IngestRequest{Job: JobSlow}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.store().PR(ctx, old); ok || len(h.reported(old)) != 0 {
		t.Fatal("the slow job kept or reported a pull request past the window")
	}
}

// noCheckouts is git over checkouts that hold no branch.
type noCheckouts struct{}

func (noCheckouts) Upstream(context.Context, string) (string, error)               { return "", nil }
func (noCheckouts) LocalBranchTip(context.Context, string, string) (string, error) { return "", nil }
func (noCheckouts) CheckedOutAt(context.Context, string, string) (string, error)   { return "", nil }
func (noCheckouts) HasCommit(context.Context, string, string) (bool, error)        { return false, nil }
func (noCheckouts) IsAncestor(context.Context, string, string, string) (bool, error) {
	return false, nil
}
