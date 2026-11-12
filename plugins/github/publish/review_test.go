package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api/apitest"
	"github.com/wloophq/wloop/plugins/github/pr"
)

const (
	headA = "4f9c2d17a0b3e6c58d21f0a9b7e4c3d2a1f0e9b8"
	headB = "9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d"
	pulls = "/repos/acme/api/pulls/7"
)

var pr7 = pr.Ref{Owner: "acme", Repo: "api", Number: 7}

type ghUser struct {
	Login string `json:"login"`
}

type ghReview struct {
	ID          int64     `json:"id"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
	User        ghUser    `json:"user"`
}

type ghComment struct {
	ID             int64     `json:"id"`
	HTMLURL        string    `json:"html_url"`
	Body           string    `json:"body"`
	InReplyTo      int64     `json:"in_reply_to_id,omitempty"`
	PullRequestURL string    `json:"pull_request_url"`
	CreatedAt      time.Time `json:"created_at"`
	User           ghUser    `json:"user"`
}

type ghThread struct {
	id       string
	resolved bool
	first    int64
}

// fakePulls serves the pull request endpoints of acme/api#7 a review, a
// thread reply and a resolve use, by path: its head, its reviews, its
// review comments and, over GraphQL, its review threads.
type fakePulls struct {
	mu       sync.Mutex
	viewer   string
	head     string
	now      time.Time
	next     int64
	reviews  []ghReview
	comments []ghComment
	threads  []ghThread
	requests []string
	posted   []json.RawMessage
	resolved []string
	// fail answers the next write with this status and body.
	fail     int
	failBody string
}

func newFakePulls(t *testing.T) (*fakePulls, *Publisher) {
	t.Helper()
	f := &fakePulls{viewer: "octocat", head: headA, now: time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC), next: 5000}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := apitest.Client(toServer{u})
	p := &Publisher{GH: c, Threads: c, Viewer: func(context.Context) (string, error) { return "octocat", nil }, Now: func() time.Time { return f.now }}
	return f, p
}

var (
	replyPath   = regexp.MustCompile(`^` + pulls + `/comments/(\d+)/replies$`)
	commentPath = regexp.MustCompile(`^/repos/acme/api/pulls/comments/(\d+)$`)
)

func (f *fakePulls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	raw, _ := io.ReadAll(r.Body)
	if r.Method == http.MethodPost && f.fail != 0 {
		w.WriteHeader(f.fail)
		io.WriteString(w, f.failBody)
		f.fail = 0
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == pulls:
		fmt.Fprintf(w, `{"number":7,"head":{"sha":%q}}`, f.head)
	case r.Method == http.MethodPost && r.URL.Path == pulls+"/reviews":
		f.posted = append(f.posted, raw)
		var in struct{ CommitID, Body, Event string }
		var fields struct {
			CommitID string `json:"commit_id"`
		}
		_ = json.Unmarshal(raw, &in)
		_ = json.Unmarshal(raw, &fields)
		f.next++
		rv := ghReview{ID: f.next, HTMLURL: fmt.Sprintf("https://github.com/acme/api/pull/7#pullrequestreview-%d", f.next), Body: in.Body,
			State: map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}[in.Event],
			CommitID: fields.CommitID, SubmittedAt: f.now, User: ghUser{f.viewer}}
		f.reviews = append(f.reviews, rv)
		_ = json.NewEncoder(w).Encode(rv)
	case r.Method == http.MethodGet && r.URL.Path == pulls+"/reviews":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page > 1 {
			io.WriteString(w, `[]`)
			return
		}
		_ = json.NewEncoder(w).Encode(f.reviews)
	case r.Method == http.MethodPost && replyPath.MatchString(r.URL.Path):
		f.posted = append(f.posted, raw)
		parent, _ := strconv.ParseInt(replyPath.FindStringSubmatch(r.URL.Path)[1], 10, 64)
		var in struct{ Body string }
		_ = json.Unmarshal(raw, &in)
		f.next++
		c := ghComment{ID: f.next, HTMLURL: fmt.Sprintf("https://github.com/acme/api/pull/7#discussion_r%d", f.next), Body: in.Body, InReplyTo: parent,
			PullRequestURL: "https://api.github.com" + pulls, CreatedAt: f.now, User: ghUser{f.viewer}}
		f.comments = append(f.comments, c)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	case r.Method == http.MethodGet && r.URL.Path == pulls+"/comments":
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		out := []ghComment{}
		for _, c := range f.comments {
			if !c.CreatedAt.Before(since) {
				out = append(out, c)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && commentPath.MatchString(r.URL.Path):
		id, _ := strconv.ParseInt(commentPath.FindStringSubmatch(r.URL.Path)[1], 10, 64)
		for _, c := range f.comments {
			if c.ID == id {
				_ = json.NewEncoder(w).Encode(c)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/graphql":
		f.graphQL(w, raw)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePulls) graphQL(w http.ResponseWriter, raw []byte) {
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(raw, &in)
	if strings.Contains(in.Query, "resolveReviewThread") {
		id, _ := in.Variables["id"].(string)
		f.resolved = append(f.resolved, id)
		for i := range f.threads {
			if f.threads[i].id == id {
				f.threads[i].resolved = true
				fmt.Fprintf(w, `{"data":{"resolveReviewThread":{"thread":{"id":%q,"isResolved":true}}}}`, id)
				return
			}
		}
		io.WriteString(w, `{"data":{"resolveReviewThread":null},"errors":[{"message":"Could not resolve to a node","path":["resolveReviewThread"]}]}`)
		return
	}
	if in.Variables["owner"] != "acme" || in.Variables["repo"] != "api" || in.Variables["number"] != float64(7) {
		io.WriteString(w, `{"data":{"repository":null}}`)
		return
	}
	var nodes []string
	for _, t := range f.threads {
		nodes = append(nodes, fmt.Sprintf(`{"id":%q,"isResolved":%t,"comments":{"nodes":[{"databaseId":%d}]}}`, t.id, t.resolved, t.first))
	}
	fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[%s]}}}}}`, strings.Join(nodes, ","))
}

func (f *fakePulls) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, http.MethodPost) && !strings.HasSuffix(r, "/graphql") {
			out = append(out, r)
		}
	}
	return append(out, f.resolved...)
}

func (f *fakePulls) lastPost(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posted) == 0 {
		t.Fatal("nothing posted")
	}
	var out map[string]any
	if err := json.Unmarshal(f.posted[len(f.posted)-1], &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// comment adds a review comment of who on acme/api#7, a reply to parent
// when not zero.
func (f *fakePulls) comment(id, parent int64, who, body string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, ghComment{ID: id, HTMLURL: fmt.Sprintf("https://github.com/acme/api/pull/7#discussion_r%d", id), Body: body, InReplyTo: parent,
		PullRequestURL: "https://api.github.com" + pulls, CreatedAt: at, User: ghUser{who}})
}

func readFixture(kind string) ([]byte, error) {
	return os.ReadFile(filepath.Join("..", "drafts", "testdata", kind+".json"))
}

// fixture is the draft the testdata proposal of kind holds, as the host
// sends it.
func fixture(t *testing.T, kind, delivery string) src.Draft {
	t.Helper()
	raw, err := readFixture(kind)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Kind    string          `json:"kind"`
		Target  string          `json:"target"`
		Text    string          `json:"text"`
		Basis   string          `json:"basis"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return src.Draft{ID: 42, Unit: pr7.Unit(), Kind: p.Kind, Delivery: delivery, Target: p.Target, Text: p.Text, Basis: p.Basis, Payload: p.Payload, State: "sending"}
}

func withEvent(t *testing.T, d src.Draft, event string) src.Draft {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(d.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["event"] = event
	d.Payload, _ = json.Marshal(payload)
	return d
}

func TestReviewPostsOneCallWithBasisAsCommit(t *testing.T) {
	f, p := newFakePulls(t)
	d := fixture(t, "review", "review")
	ev, err := p.Publish(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.writes(); len(w) != 1 || w[0] != "POST "+pulls+"/reviews" {
		t.Fatalf("writes %v", w)
	}
	got := f.lastPost(t)
	if got["commit_id"] != headA || got["body"] != d.Text || got["event"] != "REQUEST_CHANGES" {
		t.Fatalf("posted %v", got)
	}
	var want struct {
		Comments []any `json:"comments"`
	}
	_ = json.Unmarshal(d.Payload, &want)
	if gotC, _ := json.Marshal(got["comments"]); string(gotC) != string(mustJSON(t, want.Comments)) {
		t.Fatalf("inline comments %s, want %s", gotC, mustJSON(t, want.Comments))
	}
	if ev.Ref != "5001" || !strings.HasSuffix(ev.Permalink, "#pullrequestreview-5001") || !ev.At.Equal(f.now) {
		t.Fatalf("evidence %+v", ev)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReviewRecheckLiveHead(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	d := fixture(t, "review", "review")
	if head, err := p.Head(ctx, pr7); err != nil || head != headA {
		t.Fatalf("head %q %v", head, err)
	}
	f.mu.Lock()
	f.head = headB
	f.mu.Unlock()
	if head, err := p.Head(ctx, pr7); err != nil || head != headB {
		t.Fatalf("a push, then head %q %v", head, err)
	}
	_, err := p.Publish(ctx, d)
	if !errors.Is(err, ErrStale) || errors.Is(err, src.ErrMayHavePosted) || !strings.Contains(err.Error(), headB) {
		t.Fatalf("a review written at %s on head %s: %v", headA, headB, err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("a stale review posted: %v", w)
	}
	noBasis := d
	noBasis.Basis = ""
	if err := p.Validate(ctx, noBasis); err == nil {
		t.Fatal("validated a review with no basis")
	}
	if _, err := p.Publish(ctx, noBasis); err == nil || len(f.writes()) != 0 {
		t.Fatalf("published a review with no basis: %v", err)
	}
}

func TestReviewSendsEventVerbatim(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	for _, event := range []string{"APPROVE", "REQUEST_CHANGES", "COMMENT"} {
		d := withEvent(t, fixture(t, "review", "review"), event)
		if _, err := p.Publish(ctx, d); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if got := f.lastPost(t)["event"]; got != event {
			t.Fatalf("posted event %v, want %s", got, event)
		}
	}
	for _, bad := range []string{"approve", "DISMISS", ""} {
		d := withEvent(t, fixture(t, "review", "review"), bad)
		if err := p.Validate(ctx, d); err == nil {
			t.Errorf("validated event %q", bad)
		}
	}
	extra := fixture(t, "review", "review")
	extra.Payload = json.RawMessage(`{"event":"COMMENT","merge":true}`)
	if err := p.Validate(ctx, extra); err == nil {
		t.Error("validated a payload with a field the schema has not")
	}
	if n := len(f.writes()); n != 3 {
		t.Fatalf("%d writes", n)
	}
}

func TestReview422Fails(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	f.mu.Lock()
	f.fail, f.failBody = http.StatusUnprocessableEntity, `{"message":"Unprocessable Entity","errors":["Line could not be resolved"],"status":"422"}`
	f.mu.Unlock()
	_, err := p.Publish(ctx, fixture(t, "review", "review"))
	if err == nil || errors.Is(err, src.ErrMayHavePosted) || !strings.Contains(err.Error(), "Line could not be resolved") {
		t.Fatalf("a refused review: %v", err)
	}
	f.mu.Lock()
	f.fail, f.failBody = http.StatusBadGateway, `{}`
	f.mu.Unlock()
	if _, err := p.Publish(ctx, fixture(t, "review", "review")); !errors.Is(err, src.ErrMayHavePosted) {
		t.Fatalf("a review GitHub may have taken: %v", err)
	}
}

func TestReplyPosts(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	f.comment(1234567, 0, "ana", "Is this safe?", f.now.Add(-time.Hour))
	d := fixture(t, "thread_reply", "reply")
	ev, err := p.Publish(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.writes(); len(w) != 1 || w[0] != "POST "+pulls+"/comments/1234567/replies" || f.lastPost(t)["body"] != d.Text {
		t.Fatalf("writes %v, posted %v", w, f.lastPost(t))
	}
	if ev.Ref != "5001" || !strings.HasSuffix(ev.Permalink, "#discussion_r5001") {
		t.Fatalf("evidence %+v", ev)
	}
	for name, edit := range map[string]func(d *src.Draft){
		"no target":         func(d *src.Draft) { d.Target = "" },
		"the pull request":  func(d *src.Draft) { d.Target = "acme/api#7" },
		"a thread node":     func(d *src.Draft) { d.Target = "thread:PRRT_kwDOAbCdEf4AAAAB" },
		"no comment id":     func(d *src.Draft) { d.Target = "comment:abc" },
		"no text":           func(d *src.Draft) { d.Text = " " },
		"a payload":         func(d *src.Draft) { d.Payload = json.RawMessage(`{"event":"APPROVE"}`) },
		"another provider":  func(d *src.Draft) { d.Unit.Provider = "slack" },
		"an unknown target": func(d *src.Draft) { d.Target = "comment:0" },
	} {
		bad := d
		edit(&bad)
		if err := p.Validate(ctx, bad); err == nil {
			t.Errorf("%s: validated", name)
		}
		if _, err := p.Publish(ctx, bad); err == nil {
			t.Errorf("%s: published", name)
		}
	}
	if n := len(f.writes()); n != 1 {
		t.Fatalf("%d writes", n)
	}
}

func TestVerifyReviewByCommitStateTime(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	d := withEvent(t, fixture(t, "review", "review"), "APPROVE")
	d.Text = ""
	started := f.now
	review := func(id int64, who, state, commit string, at time.Time) ghReview {
		return ghReview{ID: id, HTMLURL: fmt.Sprintf("https://github.com/acme/api/pull/7#pullrequestreview-%d", id), State: state, CommitID: commit, SubmittedAt: at, User: ghUser{who}}
	}
	f.mu.Lock()
	f.reviews = []ghReview{
		review(1, "octocat", "APPROVED", headA, started.Add(-time.Hour)),
		review(2, "octocat", "APPROVED", headB, started.Add(time.Second)),
		review(3, "octocat", "COMMENTED", headA, started.Add(time.Second)),
		review(4, "ana", "APPROVED", headA, started.Add(time.Second)),
	}
	f.mu.Unlock()
	if _, found, err := p.Verify(ctx, d, started); err != nil || found {
		t.Fatalf("found among reviews of another commit, state, time or author: %v %v", found, err)
	}
	f.mu.Lock()
	f.reviews = append(f.reviews, review(5, "octocat", "APPROVED", headA, started.Add(2*time.Second)))
	f.mu.Unlock()
	ev, found, err := p.Verify(ctx, d, started)
	if err != nil || !found || ev.Ref != "5" || !ev.At.Equal(started.Add(2*time.Second)) {
		t.Fatalf("verified %+v %v %v", ev, found, err)
	}
	withBody := withEvent(t, fixture(t, "review", "review"), "APPROVE")
	if _, found, err := p.Verify(ctx, withBody, started); err != nil || found {
		t.Fatalf("a review with a body matched one without: %v %v", found, err)
	}
	f.mu.Lock()
	f.head = headB
	f.mu.Unlock()
	if _, err := p.Publish(ctx, withBody); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if _, found, err := p.Verify(ctx, d, started); err != nil || !found {
		t.Fatalf("a push after the review hid it: %v %v", found, err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("writes %v", w)
	}
}

func TestVerifyReply(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	d := fixture(t, "thread_reply", "reply")
	started := f.now
	f.comment(1234567, 0, "ana", "Is this safe?", started.Add(-time.Hour))
	f.comment(1234568, 1234567, "octocat", d.Text, started.Add(-time.Hour))
	f.comment(1234569, 1234567, "ana", d.Text, started.Add(time.Second))
	f.comment(1234570, 999, "octocat", d.Text, started.Add(time.Second))
	f.comment(1234571, 1234567, "octocat", "Something else.", started.Add(time.Second))
	if _, found, err := p.Verify(ctx, d, started); err != nil || found {
		t.Fatalf("found among replies of another time, author, thread or text: %v %v", found, err)
	}
	f.comment(1234572, 1234567, "octocat", d.Text+"\r\n", started.Add(time.Second))
	ev, found, err := p.Verify(ctx, d, started)
	if err != nil || !found || ev.Ref != "1234572" {
		t.Fatalf("verified %+v %v %v", ev, found, err)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("writes %v", w)
	}
}

func TestVerifyResolved(t *testing.T) {
	f, p := newFakePulls(t)
	ctx := context.Background()
	f.comment(1234567, 0, "ana", "Is this safe?", f.now.Add(-time.Hour))
	f.comment(1234580, 1234567, "octocat", "Yes.", f.now.Add(-time.Minute))
	f.comment(2000000, 0, "ana", "Rename this.", f.now.Add(-time.Hour))
	f.mu.Lock()
	f.threads = []ghThread{{id: "PRRT_other", first: 2000000}, {id: "PRRT_kwDOAbCdEf4AAAAB", first: 1234567}}
	f.mu.Unlock()
	d := fixture(t, "resolve", "resolve")
	if d.Target != "comment:1234567" {
		t.Fatalf("the fixture resolves %q", d.Target)
	}
	if _, found, err := p.Verify(ctx, d, f.now); err != nil || found {
		t.Fatalf("an open thread verified: %v %v", found, err)
	}
	reply := d
	reply.Target = "comment:1234580"
	ev, err := p.Publish(ctx, reply)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	resolved := append([]string(nil), f.resolved...)
	f.mu.Unlock()
	if len(resolved) != 1 || resolved[0] != "PRRT_kwDOAbCdEf4AAAAB" || ev.Ref != "PRRT_kwDOAbCdEf4AAAAB" || !strings.HasSuffix(ev.Permalink, "#discussion_r1234580") {
		t.Fatalf("resolved %v, evidence %+v", resolved, ev)
	}
	if _, found, err := p.Verify(ctx, d, f.now); err != nil || !found {
		t.Fatalf("a resolved thread: %v %v", found, err)
	}
	if _, err := p.Publish(ctx, d); err != nil || len(f.writes()) != 1 {
		t.Fatalf("resolving a resolved thread wrote again: %v %v", err, f.writes())
	}

	f.mu.Lock()
	f.comments = append(f.comments, ghComment{ID: 3000000, PullRequestURL: "https://api.github.com/repos/acme/api/pulls/8", User: ghUser{"ana"}})
	f.threads = append(f.threads, ghThread{id: "PRRT_pr8", first: 3000000})
	f.mu.Unlock()
	other := d
	other.Target = "comment:3000000"
	if _, err := p.Publish(ctx, other); err == nil || len(f.writes()) != 1 {
		t.Fatalf("resolved a thread of another pull request: %v %v", err, f.writes())
	}
	for _, target := range []string{"", "thread:PRRT_kwDOAbCdEf4AAAAB", "comment:x"} {
		bad := d
		bad.Target = target
		if err := p.Validate(ctx, bad); err == nil {
			t.Errorf("validated target %q", target)
		}
	}
}

func TestCommentKindUnchanged(t *testing.T) {
	for _, kind := range []string{"comment", "reply", "nudge", "close", "ask_owner"} {
		p, f, _ := testPublisher(t)
		d := commentDraft("The migration runs before the deploy; can you confirm the order?")
		d.Kind, d.Payload = kind, json.RawMessage(`{}`)
		ev, err := p.Publish(context.Background(), d)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if got := f.posted(issue12); len(got) != 1 || got[0].Body != d.Text || ev.Ref != strconv.FormatInt(got[0].ID, 10) {
			t.Fatalf("%s: posted %+v, evidence %+v", kind, got, ev)
		}
		if f.requests[0] != "POST "+issue12+"/comments" {
			t.Fatalf("%s: requests %v", kind, f.requests)
		}
		legacy := d
		legacy.Delivery = ""
		if err := p.Validate(context.Background(), legacy); err != nil {
			t.Fatalf("%s with no delivery: %v", kind, err)
		}
	}
}
