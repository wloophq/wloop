package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"github.com/pelletier/go-toml/v2"
	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/change"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
)

const (
	headA = "4f9c2d17a0b3e6c58d21f0a9b7e4c3d2a1f0e9b8"
	headB = "9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d"
)

// liveGitHub serves acme/api#7 by path: its head, its issue comments and,
// over GraphQL, the viewer and its review threads.
type liveGitHub struct {
	mu       sync.Mutex
	head     string
	requests []string
}

func (g *liveGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, r.Method+" "+r.URL.Path)
	raw, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/api/pulls/7":
		fmt.Fprintf(w, `{"number":7,"head":{"sha":%q}}`, g.head)
	case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/api/issues/7/comments":
		var in struct{ Body string }
		_ = json.Unmarshal(raw, &in)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 900, "html_url": "https://github.com/acme/api/pull/7#issuecomment-900", "body": in.Body,
			"created_at": testAt, "user": map[string]string{"login": "ana"}})
	case r.Method == http.MethodPost && r.URL.Path == "/graphql" && bytes.Contains(raw, []byte("latestReviews")):
		io.WriteString(w, `{"data":{"repository":{"pullRequest":{
 "latestReviews":{"nodes":[{"author":{"login":"bob"},"state":"CHANGES_REQUESTED","submittedAt":"2026-10-20T10:00:00Z","commit":{"oid":"`+headA+`"}}]},
 "reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
  {"isResolved":true,"isOutdated":false,"path":"a.go","line":3,"comments":{"nodes":[{"databaseId":11,"author":{"login":"bob"},"body":"done","createdAt":"2026-10-20T10:00:00Z"}]}},
  {"isResolved":false,"isOutdated":true,"path":"api/handler.go","line":42,"comments":{"nodes":[{"databaseId":1234567,"author":{"login":"bob"},"body":"Return the error.","createdAt":"2026-10-20T10:00:00Z"}]}}]}}}}}`)
	case r.Method == http.MethodPost && r.URL.Path == "/graphql":
		io.WriteString(w, `{"data":{"viewer":{"login":"ana"}}}`)
	default:
		http.NotFound(w, r)
	}
}

func (g *liveGitHub) setHead(h string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.head = h
}

func (g *liveGitHub) count(req string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, r := range g.requests {
		if r == req {
			n++
		}
	}
	return n
}

type toServer struct{ srv *url.URL }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.srv.Scheme, t.srv.Host
	return http.DefaultTransport.RoundTrip(r)
}

// draftHost is fakeHost with a session on acme/api#7 and the gates the
// plugin proposes through.
type draftHost struct {
	*fakeHost
	gmu      sync.Mutex
	session  src.Session
	opened   []src.GateRequest
	state    string
	sessions []string
}

func (h *draftHost) Session(_ context.Context, handle string) (src.Session, error) {
	h.gmu.Lock()
	defer h.gmu.Unlock()
	h.sessions = append(h.sessions, handle)
	return h.session, nil
}

func (h *draftHost) OpenGate(_ context.Context, req src.GateRequest) (src.GateRef, error) {
	h.gmu.Lock()
	defer h.gmu.Unlock()
	h.opened = append(h.opened, req)
	return src.GateRef{Gate: "draft", Draft: int64(41 + len(h.opened)), State: cmpOr(h.state, "held")}, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (h *draftHost) RecordSent(context.Context, src.Draft, string, string) (src.GateRef, error) {
	return src.GateRef{}, src.ErrUnsupported
}

func (h *draftHost) RehomeDrafts(context.Context, []src.UnitMove) (int, error) { return 0, nil }

func (h *draftHost) SetOverride(context.Context, sdk.Unit, map[string]string, map[string]string) (bool, error) {
	return false, src.ErrUnsupported
}

func (h *draftHost) gates() []src.GateRequest {
	h.gmu.Lock()
	defer h.gmu.Unlock()
	return slices.Clone(h.opened)
}

// startedLive is a plugin over gh's GitHub at its head headA, with a
// session whose turn is on acme/api#7.
func startedLive(t testing.TB) (*Plugin, *liveGitHub, *draftHost) {
	t.Helper()
	gh := &liveGitHub{head: headA}
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	h := &draftHost{fakeHost: &fakeHost{}, session: src.Session{Unit: ref7.Unit(), Turn: "t1", Intent: "review", AllowedEffects: []string{EffectReviewed}}}
	p := &Plugin{GH: &fakeGH{answers: map[string]string{}}, Now: func() time.Time { return testAt }, Git: noCheckouts{},
		API: api.Options{Host: "github.com", Base: toServer{u}, Token: func(context.Context) (string, error) { return "test-token", nil },
			Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}}
	ctx := context.Background()
	host := src.Host{Reporter: h.fakeHost, Recorder: h.fakeHost, History: h.fakeHost, Reader: h, Services: h.fakeHost, Gates: h}
	if err := p.Start(ctx, src.StartRequest{StateDir: t.TempDir(), Host: host}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(context.Background()) })
	p.loading.Wait()
	return p, gh, h
}

func callDraft(t *testing.T, p *Plugin, session, args string) (string, map[string]any) {
	t.Helper()
	res, err := p.CallTool(context.Background(), src.ToolCall{Tool: ToolDraft, Args: json.RawMessage(args), Session: session})
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if len(res.Fields) > 0 {
		if err := json.Unmarshal(res.Fields, &f); err != nil {
			t.Fatal(err)
		}
	}
	return res.Answer, f
}

func TestBasisLive(t *testing.T) {
	t.Parallel()
	p, gh, _ := startedLive(t)
	ctx := context.Background()
	if !slices.Contains(src.Capabilities(p), src.CapabilityDrafter) {
		t.Fatal("the plugin is no basis reader")
	}
	if got, err := p.Basis(ctx, ref7.Unit(), "review"); err != nil || got != headA {
		t.Fatalf("basis %q %v", got, err)
	}
	gh.setHead(headB)
	if got, err := p.Basis(ctx, ref7.Unit(), "review"); err != nil || got != headB {
		t.Fatalf("after a push, basis %q %v", got, err)
	}
	if n := gh.count("GET /repos/acme/api/pulls/7"); n != 2 {
		t.Fatalf("read the head %d times for 2 bases", n)
	}
	if _, err := p.Basis(ctx, sdk.Unit{Provider: "slack", Key: "C0123/1.2"}, "review"); err == nil {
		t.Fatal("a basis of a unit that is no pull request")
	}
}

func TestGithubDraftProposes(t *testing.T) {
	t.Parallel()
	p, gh, h := startedLive(t)
	if !slices.Contains(src.Capabilities(p), src.CapabilityTooler) {
		t.Fatal("the plugin serves no tools, so MCP lists no github_draft")
	}
	review, err := os.ReadFile(filepath.Join("drafts", "testdata", "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(review, &args); err != nil {
		t.Fatal(err)
	}
	delete(args, "unit")
	args["hold"] = true
	args["effects"] = []string{EffectReviewed}
	raw, _ := json.Marshal(args)
	name, f := callDraft(t, p, "s1", string(raw))
	if name != "proposed" || f["ID"] != float64(42) || f["Rev"] != float64(1) || f["Unit"] != "acme/api#7" {
		t.Fatalf("answered %s %v", name, f)
	}
	got := h.gates()
	if len(got) != 1 || got[0].Session != "s1" {
		t.Fatalf("opened %+v", got)
	}
	d := got[0].Draft
	if d.Unit != ref7.Unit() || d.Kind != "review" || d.Basis != headA || !d.Hold || !slices.Equal(d.Effects, []string{EffectReviewed}) ||
		!strings.Contains(string(d.Payload), `"REQUEST_CHANGES"`) || d.Text != args["text"] {
		t.Fatalf("proposed %+v", d)
	}

	h.state = "queued"
	if name, _ := callDraft(t, p, "s1", `{"kind":"comment","text":"Thanks!"}`); name != "approved" {
		t.Fatalf("a rule's approval answered %s", name)
	}
	h.state = "dropped"
	if name, _ := callDraft(t, p, "s1", `{"kind":"thread_reply","target":"comment:1234567","text":"Fixed."}`); name != "dry_run" {
		t.Fatalf("a dry run answered %s", name)
	}
	h.state = ""
	if name, _ := callDraft(t, p, "s1", `{"kind":"request_review","payload":{"reviewers":["bob"]}}`); name != "proposed" {
		t.Fatalf("a review request answered %s", name)
	}

	gh.setHead(headB)
	if name, f := callDraft(t, p, "s1", string(raw)); name != "stale" || f["Basis"] != headA || f["Head"] != headB {
		t.Fatalf("a stale review answered %s %v", name, f)
	}
	for args, want := range map[string]string{
		`{"kind":"merge"}`: "problem_kind",
		`{"kind":"review","payload":{"event":"APPROVE"}}`:                      "problem_basis_required",
		`{"kind":"review","basis":"` + headB + `","payload":{"event":"LGTM"}}`: "problem_payload",
		`{"kind":"comment"}`: "problem_text_required",
		`{"kind":"resolve","target":"comment:1","payload":{"event":"APPROVE"}}`: "problem_payload",
		`{"kind":"request_review","payload":{"reviewers":[]}}`:                  "problem_payload",
	} {
		name, f := callDraft(t, p, "s1", args)
		ps, _ := json.Marshal(f["Problems"])
		if name != "fix_these" || !strings.Contains(string(ps), `"`+want+`"`) {
			t.Errorf("%s answered %s %s, want %s", args, name, ps, want)
		}
	}
	if name, _ := callDraft(t, p, "s1", `{"kind":"comment","text":"x","merge":true}`); name != "bad_arguments" {
		t.Errorf("an unknown argument answered %s", name)
	}
	if name, _ := callDraft(t, p, "", `{"kind":"comment","text":"x"}`); name != "refused" {
		t.Errorf("no session answered %s", name)
	}
	if n := len(h.gates()); n != 4 {
		t.Fatalf("%d gates opened", n)
	}
}

func TestNudgeCommentEffect(t *testing.T) {
	t.Parallel()
	p, _, h := startedLive(t)
	h.session.Intent = "nudge"
	ctx := context.Background()
	if name, f := callDraft(t, p, "s1", `{"kind":"comment","text":"@{github:bob}, could you take a look?","effects":["pr.nudged"]}`); name != "proposed" {
		t.Fatalf("answered %s %v", name, f)
	}
	d := h.gates()[0].Draft
	if !slices.Equal(d.Effects, []string{nudge.EffectNudged}) {
		t.Fatalf("proposed %+v", d)
	}
	d.ID, d.Delivery, d.State = 42, "comment", "sending"
	if _, err := p.Publish(ctx, d); err != nil {
		t.Fatal(err)
	}
	evs, err := h.Events(ctx, src.EventQuery{Units: []sdk.Unit{ref7.Unit()}, Kinds: []string{pr.KindNudged}})
	if err != nil || len(evs) != 1 || string(evs[0].Data) != "@bob" {
		t.Fatalf("nudged events %+v %v", evs, err)
	}
	if err := p.ApplyEffect(ctx, src.EffectRequest{Effect: nudge.EffectNudged, Unit: ref7.Unit(), Draft: &d}); err != nil {
		t.Fatal(err)
	}
	if evs, _ := h.Events(ctx, src.EventQuery{Units: []sdk.Unit{ref7.Unit()}, Kinds: []string{pr.KindNudged}}); len(evs) != 1 {
		t.Fatalf("the effect recorded the nudge again: %+v", evs)
	}
}

func TestReviewedEffectOnSentDraft(t *testing.T) {
	t.Parallel()
	p, _, h := startedLive(t)
	ctx := context.Background()
	submitted := testAt.Add(-time.Minute)
	d := src.Draft{ID: 42, Unit: ref7.Unit(), Kind: "review", Delivery: "review", Basis: headA, State: "sent",
		Evidence: src.Evidence{Ref: "5001", At: submitted}}
	if err := p.ApplyEffect(ctx, src.EffectRequest{Effect: EffectReviewed, Unit: ref7.Unit(), Draft: &d}); err != nil {
		t.Fatal(err)
	}
	want := change.Fingerprint(submitted, headA)
	if got := h.markSets(); len(got) != 1 || got[0][plan.MarkReviewed] != want {
		t.Fatalf("marks %v, want %s", got, want)
	}
	facts := pr.Facts{State: pr.StateOpen, HeadRefOid: headA, AuthorAssociation: "MEMBER", Ref: pr.Ref{Owner: "acme", Repo: "api", Number: 7},
		Reviews: []pr.Review{{Author: "ana", State: pr.ReviewApproved, SubmittedAt: submitted, CommitOid: headA}}}
	if dec := change.Reviewer(change.ReviewerInput{PR: facts, Viewer: "ana", TrustedOwners: []string{"acme"}, LastFingerprint: want}); dec.Action == change.ReviewerFollowup {
		t.Fatalf("the round restarts after its review was sent: %+v", dec)
	}
	facts.HeadRefOid = headB
	if dec := change.Reviewer(change.ReviewerInput{PR: facts, Viewer: "ana", TrustedOwners: []string{"acme"}, LastFingerprint: want}); dec.Action != change.ReviewerFollowup {
		t.Fatalf("a push after the sent review starts no round: %+v", dec)
	}
	for name, edit := range map[string]func(*src.Draft){
		"a comment":  func(d *src.Draft) { d.Kind, d.Delivery = "comment", "comment" },
		"no basis":   func(d *src.Draft) { d.Basis = "" },
		"not posted": func(d *src.Draft) { d.Evidence = src.Evidence{} },
	} {
		bad := d
		edit(&bad)
		if err := p.ApplyEffect(ctx, src.EffectRequest{Effect: EffectReviewed, Unit: ref7.Unit(), Draft: &bad}); err == nil {
			t.Errorf("%s: marked reviewed", name)
		}
	}
	if n := len(h.markSets()); n != 1 {
		t.Fatalf("%d mark sets", n)
	}
}

func TestGithubThreadsReadsOpenThreads(t *testing.T) {
	t.Parallel()
	p, gh, _ := startedLive(t)
	res, err := p.CallTool(context.Background(), src.ToolCall{Tool: ToolThreads, Args: json.RawMessage(`{}`), Session: "s1"})
	if err != nil || res.Answer != "threads" || res.IsError {
		t.Fatalf("answered %+v %v", res, err)
	}
	var f struct{ Unit, JSON string }
	if err := json.Unmarshal(res.Fields, &f); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Threads []struct {
			IsResolved bool `json:"is_resolved"`
			IsOutdated bool `json:"is_outdated"`
			Comments   []struct {
				ID int64 `json:"id"`
			} `json:"comments"`
		} `json:"threads"`
		Reviews []struct{ Author, State string } `json:"latest_reviews"`
	}
	if err := json.Unmarshal([]byte(f.JSON), &c); err != nil {
		t.Fatal(err)
	}
	if f.Unit != "acme/api#7" || len(c.Threads) != 1 || c.Threads[0].IsResolved || !c.Threads[0].IsOutdated || c.Threads[0].Comments[0].ID != 1234567 ||
		len(c.Reviews) != 1 || c.Reviews[0].Author != "bob" {
		t.Fatalf("read %s", f.JSON)
	}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	for _, r := range gh.requests {
		if !strings.HasPrefix(r, "GET ") && r != "POST /graphql" {
			t.Errorf("a read wrote: %s", r)
		}
	}
}

// TestEveryPreviewRendersItsFixtures executes the preview template of each
// kind that has one over the kind's testdata proposal.
func TestEveryPreviewRendersItsFixtures(t *testing.T) {
	t.Parallel()
	var m struct {
		Source struct {
			DraftKinds []struct {
				Name    string `toml:"name"`
				Preview string `toml:"preview"`
			} `toml:"draft_kinds"`
		} `toml:"source"`
	}
	raw, err := os.ReadFile("plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range m.Source.DraftKinds {
		if k.Preview == "" {
			continue
		}
		tmpl, err := template.ParseFiles(k.Preview)
		if err != nil {
			t.Fatalf("%s: %v", k.Name, err)
		}
		raw, err := os.ReadFile(filepath.Join("drafts", "testdata", k.Name+".json"))
		if os.IsNotExist(err) {
			raw = []byte(`{"kind":"` + k.Name + `","text":"x","payload":{}}`)
		} else if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		in := map[string]any{"Kind": data["kind"], "Target": data["target"], "Text": data["text"], "Basis": data["basis"], "Payload": data["payload"]}
		var out strings.Builder
		if err := tmpl.Option("missingkey=error").Execute(&out, in); err != nil || strings.TrimSpace(out.String()) == "" || strings.Contains(out.String(), "<no value>") {
			t.Errorf("%s: rendered %q %v", k.Name, out.String(), err)
		}
	}
}

func BenchmarkGithubDraftReview(b *testing.B) {
	p, gh, _ := startedLive(b)
	args := `{"kind":"review","basis":"` + headA + `","text":"x","payload":{"event":"COMMENT"}}`
	before := gh.count("GET /repos/acme/api/pulls/7")
	for b.Loop() {
		res, err := p.CallTool(context.Background(), src.ToolCall{Tool: ToolDraft, Args: json.RawMessage(args), Session: "s1"})
		if err != nil || res.Answer != "proposed" {
			b.Fatal(res, err)
		}
	}
	b.ReportMetric(float64(gh.count("GET /repos/acme/api/pulls/7")-before)/float64(b.N), "head-reads/op")
}
