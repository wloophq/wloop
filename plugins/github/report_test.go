package github

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/situation"
)

// lastReport is the newest snapshot of ref the plugin reported.
func (h *fakeHost) lastReport(t *testing.T, ref pr.Ref) src.Snapshot {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.reports) - 1; i >= 0; i-- {
		for _, s := range h.reports[i] {
			if s.Unit == ref.Unit() {
				return s
			}
		}
	}
	t.Fatalf("%s was never reported", ref)
	return src.Snapshot{}
}

func (h *fakeHost) record(e src.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
}

// mine is an open pull request of the viewer the plugin stores, in scope.
func mine(t *testing.T, p *Plugin) pr.Ref {
	t.Helper()
	ctx := context.Background()
	if err := p.store().SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if err := p.store().UpsertPR(ctx, pr.PR{Ref: ref7, Title: "Change 7", Author: "ana", State: "OPEN", HeadOid: "h7", HeadBranch: "feat/7",
		BaseBranch: "main", ChecksState: "SUCCESS", CreatedAt: testAt.Add(-48 * time.Hour), Reasons: []string{"author"}}); err != nil {
		t.Fatal(err)
	}
	return ref7
}

func TestAReportCarriesTheSourceRowOfItsPullRequest(t *testing.T) {
	h := &fakeHost{}
	p := started(t, &githubGraphQL{}, h)
	ref := mine(t, p)
	h.record(src.Event{Unit: ref.Unit(), Kind: pr.KindReview, Key: "r1", Actor: "bruno", At: testAt.Add(-time.Hour), Data: []byte("CHANGES_REQUESTED:h7")})
	if err := p.Notify(context.Background(), []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicEvents}); err != nil {
		t.Fatal(err)
	}
	s := h.lastReport(t, ref)
	if s.Situation == nil || s.Situation.Kind != string(situation.KindChanges) || s.Situation.WaitingOn != "author" ||
		s.Situation.Reason != "changes requested by bruno" || s.Situation.Stalled.Kind != src.KindAfter {
		t.Fatalf("situation %+v", s.Situation)
	}
	if s.Card == nil || s.Card.Title != "Change 7" || !s.Card.StartedByMe || len(s.Items) != 1 || s.Items[0].ID != "gh:address" {
		t.Fatalf("card %+v, items %+v", s.Card, s.Items)
	}
	if s.Facets["repo"] != "acme/api" || !slices.Equal(s.Views, []string{"mine"}) {
		t.Fatalf("facets %v, views %v", s.Facets, s.Views)
	}
	var stalled *src.Signal
	for i, sig := range s.Signals {
		if sig.Name == "stalled" {
			stalled = &s.Signals[i]
		}
	}
	if stalled == nil || stalled.On {
		t.Fatalf("signals %+v", s.Signals)
	}
}

func TestAnAddressTurnInTheBooksEndsTheReviewToAddress(t *testing.T) {
	h := &fakeHost{}
	p := started(t, &githubGraphQL{}, h)
	ref := mine(t, p)
	h.record(src.Event{Unit: ref.Unit(), Kind: pr.KindWouldAddress, Key: "w1", At: testAt.Add(-2 * time.Hour), Data: []byte("reviewed by bruno")})
	ctx := context.Background()
	if err := p.Notify(ctx, []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicEvents}); err != nil {
		t.Fatal(err)
	}
	if s := h.lastReport(t, ref); s.Situation.Kind != string(situation.KindToAddress) {
		t.Fatalf("before the turn: %+v", s.Situation)
	}
	h.mu.Lock()
	h.books = []src.Book{{Unit: ref.Unit(), Turns: []src.Turn{{Seq: 1, Intent: "address", Role: "pr.address", State: "done", At: testAt.Add(-time.Hour)}}}}
	h.mu.Unlock()
	if err := p.Notify(ctx, []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicTurns}); err != nil {
		t.Fatal(err)
	}
	if s := h.lastReport(t, ref); s.Situation.Kind == string(situation.KindToAddress) {
		t.Fatalf("an address turn after it and it still waits: %+v", s.Situation)
	}
}

func TestEachPullRequestIsReportedAgainOnlyWhenItsOwnDueTimePasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &running{now: time.Now, dues: map[pr.Ref]time.Time{}, dueWake: make(chan struct{}, 1)}
		type report struct {
			at   time.Time
			refs []pr.Ref
		}
		var mu sync.Mutex
		var reports []report
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.watchDue(ctx, func(_ context.Context, refs []pr.Ref) error {
				mu.Lock()
				defer mu.Unlock()
				reports = append(reports, report{time.Now(), refs})
				return nil
			})
		}()
		start := time.Now()
		stalls, waits, ended := pr.Ref{Owner: "acme", Repo: "api", Number: 1}, pr.Ref{Owner: "acme", Repo: "api", Number: 2},
			pr.Ref{Owner: "acme", Repo: "api", Number: 3}
		r.dueAt(stalls, start.Add(time.Hour))
		r.dueAt(waits, start.Add(3*time.Hour))
		r.dueAt(ended, start.Add(2*time.Hour))
		r.dueAt(ended, time.Time{})
		time.Sleep(10 * time.Hour)
		synctest.Wait()
		mu.Lock()
		got := slices.Clone(reports)
		mu.Unlock()
		want := []report{{start.Add(time.Hour), []pr.Ref{stalls}}, {start.Add(3 * time.Hour), []pr.Ref{waits}}}
		if len(got) != len(want) {
			t.Fatalf("reports %+v, want %+v", got, want)
		}
		for i := range want {
			if !got[i].at.Equal(want[i].at) || !slices.Equal(got[i].refs, want[i].refs) {
				t.Errorf("report %d: %+v, want %+v", i, got[i], want[i])
			}
		}
		cancel()
		<-done
	})
}

func TestAReportRecordsNothingThatNotifiesThePluginAgain(t *testing.T) {
	h := &fakeHost{}
	p := started(t, &githubGraphQL{}, h)
	ref := mine(t, p)
	h.record(src.Event{Unit: ref.Unit(), Kind: pr.KindReview, Key: "r1", Actor: "bruno", At: testAt.Add(-time.Hour), Data: []byte("CHANGES_REQUESTED:h7")})
	h.mu.Lock()
	reports, events, topics := len(h.reports), len(h.events), len(h.topics)
	h.mu.Unlock()
	if err := p.Notify(context.Background(), []sdk.Unit{ref.Unit()}, []sdk.Topic{sdk.TopicEvents}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.reports) != reports+1 || len(h.events) != events || len(h.topics) != topics {
		t.Fatalf("one notice made %d reports, %d events and %d changes; want 1, 0 and 0",
			len(h.reports)-reports, len(h.events)-events, len(h.topics)-topics)
	}
}
