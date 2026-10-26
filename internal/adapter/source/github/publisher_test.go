package ghsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github/githubtest"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// fakeGitHub serves the issue comments REST endpoints of every pull request.
type fakeGitHub struct {
	mu       sync.Mutex
	viewer   string
	comments map[string][]issueComment
	requests []string
	next     int64
	now      time.Time
	// fail answers the next POST with this status; -1 resets the connection.
	fail int
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	f := &fakeGitHub{viewer: "octocat", comments: map[string][]issueComment{}, next: 1000, now: time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	issue, ok := strings.CutSuffix(r.URL.Path, "/comments")
	if !ok || !strings.HasPrefix(issue, "/repos/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		switch {
		case f.fail == -1:
			f.fail = 0
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		case f.fail != 0:
			status := f.fail
			f.fail = 0
			http.Error(w, `{"message":"failed"}`, status)
			return
		}
		var in struct{ Body string }
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.next++
		c := issueComment{ID: f.next, HTMLURL: fmt.Sprintf("https://github.com%s#issuecomment-%d", strings.Replace(issue, "/repos", "", 1), f.next), Body: in.Body, CreatedAt: f.now}
		c.User.Login = f.viewer
		f.comments[issue] = append(f.comments[issue], c)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	case http.MethodGet:
		since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		out := []issueComment{}
		for _, c := range f.comments[issue] {
			if !c.CreatedAt.Before(since) {
				out = append(out, c)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (f *fakeGitHub) add(issue, login, body string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	c := issueComment{ID: f.next, Body: body, CreatedAt: at}
	c.User.Login = login
	f.comments[issue] = append(f.comments[issue], c)
}

func (f *fakeGitHub) posted(issue string) []issueComment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]issueComment(nil), f.comments[issue]...)
}

func (f *fakeGitHub) posts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, http.MethodPost) {
			n++
		}
	}
	return n
}

// toServer sends every request to srv, whatever its host.
type toServer struct{ srv *url.URL }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.srv.Scheme, t.srv.Host
	return http.DefaultTransport.RoundTrip(r)
}

type recordedEvents struct {
	mu  sync.Mutex
	evs []dinv.Event
}

func (r *recordedEvents) RecordEvent(_ context.Context, ev dinv.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, ev)
	return nil
}

func testPublisher(t *testing.T, people ...items.Person) (*Publisher, *fakeGitHub, *recordedEvents) {
	t.Helper()
	f, srv := newFakeGitHub(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	evs := &recordedEvents{}
	p := &Publisher{
		GH:     githubtest.Client(toServer{u}),
		Viewer: func(context.Context) (string, error) { return "octocat", nil },
		Events: evs,
		People: func(context.Context, unit.Unit) ([]items.Person, error) { return people, nil },
		Now:    func() time.Time { return f.now },
	}
	return p, f, evs
}

var pr12 = pr.Ref{Owner: "owner", Repo: "repo", Number: 12}

const issue12 = "/repos/owner/repo/issues/12"

func commentDraft(text string) draft.Draft {
	return draft.Draft{ID: 7, Unit: pr12.Unit(), Kind: draft.Comment, Target: "owner/repo#12", Text: text, State: draft.Sending}
}

func TestGitHubPublishPostsAComment(t *testing.T) {
	p, f, evs := testPublisher(t)
	ev, err := p.Publish(context.Background(), commentDraft("Looks good, merging after CI."))
	if err != nil {
		t.Fatal(err)
	}
	got := f.posted(issue12)
	if len(got) != 1 || got[0].Body != "Looks good, merging after CI." {
		t.Fatalf("posted %+v", got)
	}
	if ev.Ref != fmt.Sprint(got[0].ID) || ev.Permalink != got[0].HTMLURL || ev.Permalink == "" || !ev.At.Equal(f.now) {
		t.Fatalf("evidence %+v, comment %+v", ev, got[0])
	}
	if len(evs.evs) != 0 {
		t.Fatalf("a comment recorded %+v", evs.evs)
	}
}

func TestGitHubPublishIsAMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fail   int
		posted bool
	}{
		{"bad gateway", http.StatusBadGateway, true},
		{"server error", http.StatusInternalServerError, true},
		{"reset", -1, true},
		{"unprocessable", http.StatusUnprocessableEntity, false},
		{"not found", http.StatusNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, f, _ := testPublisher(t)
			f.fail = tc.fail
			_, err := p.Publish(context.Background(), commentDraft("hello"))
			if err == nil {
				t.Fatal("published through a failure")
			}
			if got := strings.HasPrefix(err.Error(), mayHaveBeenPosted); got != tc.posted {
				t.Fatalf("error %q: may have been posted %v, want %v", err, got, tc.posted)
			}
			if n := f.posts(); n != 1 {
				t.Fatalf("%d POSTs, want 1: a mutation is never retried once written", n)
			}
			if strings.Contains(err.Error(), "test-token") {
				t.Fatalf("token in %q", err)
			}
		})
	}
}

func TestGitHubPublishConvertsMentions(t *testing.T) {
	p, f, _ := testPublisher(t, items.Person{Key: "slack:U000AAA", Name: "Ana"})
	d := commentDraft("@{github:octocat} could you check what @{slack:U000AAA} asked? cc @{group:acme/core}")
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got := f.posted(issue12)
	want := "@octocat could you check what Ana asked? cc acme/core"
	if len(got) != 1 || got[0].Body != want {
		t.Fatalf("posted %+v, want body %q", got, want)
	}
}

func TestGitHubPublishRefusesAnUnknownPerson(t *testing.T) {
	p, f, _ := testPublisher(t)
	_, err := p.Publish(context.Background(), commentDraft("ask @{slack:U000BBB}"))
	if err == nil || !strings.Contains(err.Error(), "names nobody") {
		t.Fatalf("err %v", err)
	}
	if f.posts() != 0 {
		t.Fatal("posted a comment with an unknown mention")
	}
}

func TestGitHubVerifyFindsTheComment(t *testing.T) {
	p, f, _ := testPublisher(t, items.Person{Key: "slack:U000AAA", Name: "Ana"})
	ctx := context.Background()
	d := commentDraft("@{github:alice} see what @{slack:U000AAA} said")
	started := f.now.Add(-time.Second)
	f.add(issue12, "alice", "@alice see what Ana said", f.now)
	f.add(issue12, "octocat", "@alice see what Ana said", started.Add(-time.Hour))
	if _, found, err := p.Verify(ctx, d, started); err != nil || found {
		t.Fatalf("found %v, err %v before the post", found, err)
	}
	ev, err := p.Publish(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := p.Verify(ctx, d, started)
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	if got != ev {
		t.Fatalf("verified %+v, published %+v", got, ev)
	}
	other := d
	other.Text = "something else"
	if _, found, err := p.Verify(ctx, other, started); err != nil || found {
		t.Fatalf("matched another text: found %v, err %v", found, err)
	}
}

func TestGitHubValidateRefusesForeignTargets(t *testing.T) {
	p, f, _ := testPublisher(t)
	ctx := context.Background()
	ok := commentDraft("hi")
	if err := p.Validate(ctx, ok); err != nil {
		t.Fatal(err)
	}
	self := ok
	self.Target = ""
	if err := p.Validate(ctx, self); err != nil {
		t.Fatalf("empty target: %v", err)
	}
	for name, edit := range map[string]func(d *draft.Draft){
		"other pull request": func(d *draft.Draft) { d.Target = "owner/repo#13" },
		"other repository":   func(d *draft.Draft) { d.Target = "owner/other#12" },
		"not a target":       func(d *draft.Draft) { d.Target = "#general" },
		"react":              func(d *draft.Draft) { d.Kind, d.Delivery, d.Reaction = draft.React, draft.Reaction, "eyes" },
		"slack unit":         func(d *draft.Draft) { d.Unit = unit.Unit{Provider: "slack", Key: "C000AAA/1.2"} },
		"empty":              func(d *draft.Draft) { d.Text = "  " },
	} {
		d := ok
		edit(&d)
		if err := p.Validate(ctx, d); err == nil {
			t.Errorf("%s: validated", name)
		}
		if _, err := p.Publish(ctx, d); err == nil {
			t.Errorf("%s: published", name)
		}
	}
	if n := f.posts(); n != 0 {
		t.Fatalf("%d posts of refused drafts", n)
	}
}

type failingEvents struct{}

func (failingEvents) RecordEvent(context.Context, dinv.Event) error {
	return errors.New("store closed")
}

func TestGitHubNudgeRecordsOnlyWhatMayHaveGone(t *testing.T) {
	nudge := func() draft.Draft {
		d := commentDraft("@{github:alice} @{github:bob} friendly nudge")
		d.Kind, d.Effects = draft.Nudge, []string{builtin.EffectPRNudged}
		return d
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		fail  int
		event bool
	}{
		{"locked conversation", http.StatusUnprocessableEntity, false},
		{"no permission", http.StatusForbidden, false},
		{"not found", http.StatusNotFound, false},
		{"posted", 0, true},
		{"bad gateway", http.StatusBadGateway, true},
		{"reset", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, f, evs := testPublisher(t)
			created := f.now
			p.Now = func() time.Time { return created.Add(-time.Second) }
			f.fail = tc.fail
			_, err := p.Publish(ctx, nudge())
			if (err == nil) != (tc.fail == 0) {
				t.Fatalf("err %v", err)
			}
			if !tc.event {
				if len(evs.evs) != 0 {
					t.Fatalf("a refused nudge recorded %+v", evs.evs)
				}
				return
			}
			if len(evs.evs) != 1 {
				t.Fatalf("events %+v", evs.evs)
			}
			e := evs.evs[0]
			want := created.Add(-time.Second)
			if tc.fail == 0 {
				want = created
			}
			if e.Kind != dinv.KindNudged || e.Data != "@alice, @bob" || e.PR != pr12 || !e.At.Equal(want) {
				t.Fatalf("event %+v, want at %s", e, want)
			}
		})
	}
	t.Run("comment", func(t *testing.T) {
		p, _, evs := testPublisher(t)
		if _, err := p.Publish(ctx, commentDraft("@{github:alice} hi")); err != nil || len(evs.evs) != 0 {
			t.Fatalf("err %v, events %+v", err, evs.evs)
		}
	})
	t.Run("found after a crash", func(t *testing.T) {
		p, f, evs := testPublisher(t)
		d := nudge()
		f.add(issue12, "octocat", "@alice @bob friendly nudge", f.now)
		ev, found, err := p.Verify(ctx, d, f.now.Add(-time.Second))
		if err != nil || !found || ev.Ref == "" {
			t.Fatalf("found %v err %v", found, err)
		}
		if len(evs.evs) != 1 || !evs.evs[0].At.Equal(f.now) || evs.evs[0].Data != "@alice, @bob" {
			t.Fatalf("events %+v", evs.evs)
		}
	})
	t.Run("not found after a crash", func(t *testing.T) {
		p, f, evs := testPublisher(t)
		if _, found, err := p.Verify(ctx, nudge(), f.now); err != nil || found || len(evs.evs) != 0 {
			t.Fatalf("found %v err %v events %+v", found, err, evs.evs)
		}
	})
	t.Run("record fails after the post", func(t *testing.T) {
		p, f, _ := testPublisher(t)
		var logged []string
		p.Events, p.Log = failingEvents{}, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
		ev, err := p.Publish(ctx, nudge())
		if err != nil || ev.Ref == "" || len(f.posted(issue12)) != 1 {
			t.Fatalf("evidence %+v, err %v", ev, err)
		}
		if len(logged) != 1 || !strings.Contains(logged[0], "store closed") {
			t.Fatalf("logged %q", logged)
		}
	})
}

func TestGitHubPublishNeverPingsThroughAnotherProvidersName(t *testing.T) {
	p, f, _ := testPublisher(t, items.Person{Key: "slack:U000AAA", Name: "Ana @alice (ana@corp.test)"})
	d := commentDraft("ask @{slack:U000AAA} and @{group:@acme/core}")
	if _, err := p.Publish(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got := f.posted(issue12)
	want := "ask Ana @\u200balice (ana@\u200bcorp.test) and @\u200bacme/core"
	if len(got) != 1 || got[0].Body != want {
		t.Fatalf("posted %+v, want body %q", got, want)
	}
}

func TestGitHubWhereNamesThePullRequest(t *testing.T) {
	p, _, _ := testPublisher(t)
	if w, err := p.Where(context.Background(), commentDraft("hi")); err != nil || w != "owner/repo#12" {
		t.Fatalf("%q %v", w, err)
	}
	if _, err := p.Where(context.Background(), commentDraft("ask @{slack:U000BBB}")); err == nil {
		t.Fatal("a draft that cannot post has a destination")
	}
}

func TestGitHubDeliversEveryTextDraftAsAComment(t *testing.T) {
	p := &Publisher{}
	for in, want := range map[draft.Delivery]draft.Delivery{draft.Message: draft.CommentDelivery, draft.CommentDelivery: draft.CommentDelivery, draft.Reaction: draft.Reaction,
		draft.EditDelivery: draft.EditDelivery} {
		if got := p.Delivers(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
