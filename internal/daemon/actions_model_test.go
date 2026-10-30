package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func newNotedServer(t *testing.T, edit func(*Deps)) *Server {
	t.Helper()
	return newServerLogged(t, change.NewLog(change.DefaultWindow, nil), edit)
}

func warm(t *testing.T, s *Server) {
	t.Helper()
	if _, _, err := s.readModel().Base(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOfferedIssuesNoQueryWhenFresh(t *testing.T) {
	t.Parallel()
	s := newNotedServer(t, func(*Deps) {})
	warm(t, s)
	ctx, sc := metertest.Start(t, context.Background())
	row, st, err := s.offered(ctx, r7.Unit(), catalog.Asked{Name: "pause"}, api.ActionRequest{})
	if err != nil || !st.Enabled || row.Unit != r7.Unit() {
		t.Fatalf("%+v %+v %v", row, st, err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 0})
}

func TestDetailAtMostTwoQueries(t *testing.T) {
	t.Parallel()
	s := newNotedServer(t, func(d *Deps) {
		withBriefs(d, brief.New(brief.Deps{Store: d.Records.(present.Records).Store, PRs: inventorytest.Of(d.Records.(present.Records).Store), Now: d.Now, Taxonomy: func(github.Ref) string { return brief.DefaultTaxonomy }}))
	})
	warm(t, s)
	ctx, sc := metertest.Start(t, context.Background())
	d, ok, err := s.detail(ctx, r7.Unit())
	if err != nil || !ok || d.Row.Unit != r7.Unit() || len(d.Events) == 0 {
		t.Fatalf("%+v %v %v", d, ok, err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 2})
}

func TestNudgeUnmutedRowNoQuery(t *testing.T) {
	t.Parallel()
	s := newNotedServer(t, func(*Deps) {})
	seedWaitingOnAna(t, s)
	warm(t, s)
	ctx, sc := metertest.Start(t, context.Background())
	row, err := s.unmutedRow(ctx, r9.Unit())
	if err != nil || row.Unit != r9.Unit() {
		t.Fatalf("%+v %v", row, err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 0})
}

type bookCalls struct {
	turns.Store
	calls int
}

func (c *bookCalls) Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	c.calls++
	return c.Store.Books(ctx, units...)
}

func TestExploreBaseDeriverFromSnapshot(t *testing.T) {
	t.Parallel()
	books := &bookCalls{Store: turnstest.NewStore(turnstest.Strict(t))}
	cands := []dgh.Candidate{
		{Ref: r7, Title: "Batch export", Author: "ana", State: dgh.StateOpen},
		{Ref: github.Ref{Owner: "acme", Repo: "web", Number: 99}, Title: "Untracked", Author: "zoe", State: dgh.StateOpen},
	}
	s := newNotedServer(t, func(d *Deps) {
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books, Registry: turnstest.NewRegistry(turnstest.Strict(t)),
			Sessions: turnstest.NewSessions(), Clock: fixedClock(d.Now())}
		exploreOn(d, &fakeExplorer{page: dgh.CandidatePage{Total: 2, Candidates: cands}})
	})
	warm(t, s)
	before := books.calls
	code, got, _ := explorePost(t, s, "is:open")
	if code != 200 || len(got.Candidates) != 2 || len(got.Candidates[0].Actions) == 0 || len(got.Candidates[1].Actions) == 0 {
		t.Fatalf("%d %+v", code, got)
	}
	if books.calls != before {
		t.Fatalf("%d Books calls", books.calls-before)
	}
}

func seedLapsed(t *testing.T, s *Server) github.Ref {
	t.Helper()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 31}
	ctx := context.Background()
	if err := inventorytest.Of(s.store()).UpsertPR(ctx, dinv.PR{Ref: ref, Title: "Lapsed", Author: "ana", State: dgh.StateOpen, URL: ref.URL()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store().AppendEvents(ctx, []dinv.Event{{PR: ref, Kind: dinv.KindPush, Key: "h", Actor: "ana", At: s.deps.Now().Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if pr, ok, _ := inventorytest.Of(s.store()).PR(ctx, ref); !ok || !pr.Lapsed() {
		t.Fatalf("not lapsed: %+v", pr)
	}
	return ref
}

func TestDetailOfALapsedPR(t *testing.T) {
	t.Parallel()
	s := newNotedServer(t, func(*Deps) {})
	ref := seedLapsed(t, s)
	var d api.Detail
	if code := get(t, s, api.UnitPath(ref.Unit()), &d); code != 200 || d.Row.Unit != ref.Unit() || len(d.Events) != 1 {
		t.Fatalf("%d %+v", code, d)
	}
}

func TestUnmutedRowOfALapsedPR(t *testing.T) {
	t.Parallel()
	s := newNotedServer(t, func(*Deps) {})
	ref := seedLapsed(t, s)
	row, err := s.unmutedRow(context.Background(), ref.Unit())
	if err != nil || row.Unit != ref.Unit() {
		t.Fatalf("%+v %v", row, err)
	}
}
