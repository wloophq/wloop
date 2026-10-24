package daemon

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestALaterTurnNeverWaits(t *testing.T) {
	books := turnstest.NewStore()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Done, SentAt: at}}})
	briefs := intaketest.NewBriefs(intake.Readiness{Go: true, Without: true, Note: "this pull request has no brief yet"})
	s, _ := newIntakeServer(t, briefs, func(d *Deps) { d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books} })
	rd, err := s.BriefReady(context.Background(), pr.Ref(r7).Unit())
	if err != nil || !rd.Go {
		t.Fatalf("%+v %v", rd, err)
	}
	other := pr.Ref{Owner: "acme", Repo: "api", Number: 20}
	if _, err := s.BriefReady(context.Background(), other.Unit()); err != nil {
		t.Fatal(err)
	}
	if got := briefs.Asked(); !slices.Equal(got, []bool{false, true}) {
		t.Fatalf("asked with first %v, want a follow-up asked as a later turn", got)
	}
}

func TestOpenIntakesForTheSweep(t *testing.T) {
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	open, err := s.OpenIntakes(context.Background())
	if err != nil || len(open) != 1 || open[0].Seq != in.Seq {
		t.Fatalf("%+v %v", open, err)
	}
	none := newServerWith(t, func(*Deps) {})
	if open, err := none.OpenIntakes(context.Background()); err != nil || open != nil {
		t.Fatalf("%+v %v", open, err)
	}
}

// missingActions records why each review went without its brief.
type missingActions struct {
	lockedActions
	mu      sync.Mutex
	missing []string
}

func (m *missingActions) Intent(ctx context.Context, _ string, _ unit.Unit, _ Call) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.missing = append(m.missing, BriefMissing(ctx))
	return nil
}

func (m *missingActions) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.missing)
}

func TestAnIntakeWithoutItsBriefSaysWhy(t *testing.T) {
	gaveUp := intake.Readiness{Go: true, Without: true, Cause: intake.CauseGaveUp, Attempt: 3, LastError: "unfit", Note: "the brief gave up after 3 attempts: unfit"}
	fa := &missingActions{}
	s, _ := newIntakeServer(t, intaketest.NewBriefs(gaveUp), func(d *Deps) { d.Actions = fa })
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Started })
	if got := fa.seen(); !slices.Equal(got, []string{gaveUp.Note}) {
		t.Fatalf("brief missing %q", got)
	}
}

func TestUnavailableBriefsAreReported(t *testing.T) {
	fa := &missingActions{}
	s, _ := newIntakeServer(t, nil, func(d *Deps) {
		d.Actions = fa
		d.BriefsUnavailable = `agent "plain" cannot answer headless calls`
	})
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != 200 || h.Briefs == nil || h.Briefs.Unavailable == "" {
		t.Fatalf("%d %+v", code, h.Briefs)
	}
	if !slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.HasPrefix(w, `brief: agent "plain" cannot answer headless calls`) }) {
		t.Fatalf("warnings %q", h.Warnings)
	}
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Started })
	if got := fa.seen(); len(got) != 1 || !strings.HasPrefix(got[0], "briefs are unavailable: ") {
		t.Fatalf("brief missing %q", got)
	}
}

func TestAnIntakeTheSweepAnsweredSendsNothingMore(t *testing.T) {
	books := turnstest.NewStore()
	mu := &sync.Mutex{}
	s, fa := newIntakeServer(t, nil, func(d *Deps) {
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books}
		d.LoopMu = mu
	})
	mu.Lock()
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Starting })
	books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "by-sweep", Trigger: turn.Automated, State: turn.Sent, SentAt: time.Now().Add(time.Second)}}})
	mu.Unlock()
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Started })
	got := intakeOf(t, s, in.Seq)
	if got.TurnID != "by-sweep" || len(fa.calls()) != 0 {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
	if !slices.ContainsFunc(got.Log, func(l string) bool { return strings.Contains(l, "the sweep sent the review") }) {
		t.Fatalf("log %q", got.Log)
	}
}

// raceRun holds the loop mutex while an intake reaches starting, adds the
// turns sent during the wait, and lets the start go.
func raceRun(t *testing.T, body string, before []turn.Turn, during []turn.Turn) (*Server, *lockedActions, intake.Intake) {
	t.Helper()
	books := turnstest.NewStore()
	if len(before) > 0 {
		books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: before})
	}
	mu := &sync.Mutex{}
	s, fa := newIntakeServer(t, nil, func(d *Deps) {
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books}
		d.LoopMu = mu
	})
	mu.Lock()
	in := ask(t, s, "review", body)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Starting })
	all := append(slices.Clone(before), during...)
	books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: all})
	mu.Unlock()
	waitFor(t, func() bool { st := intakeOf(t, s, in.Seq).State; return st == intake.Started || st == intake.Failed })
	return s, fa, intakeOf(t, s, in.Seq)
}

func TestAByHandTurnAfterTheAcceptStillRunsTheAction(t *testing.T) {
	later := time.Now().Add(time.Second)
	_, fa, got := raceRun(t, `{}`, nil, []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "by-hand", Trigger: turn.ByHand, State: turn.Started, SentAt: later}})
	if got.State != intake.Started || len(fa.calls()) != 1 {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
}

func TestAGuidedIntakeIsNotSwallowedByTheSweep(t *testing.T) {
	later := time.Now().Add(time.Second)
	_, fa, got := raceRun(t, `{"guidance":"check the migration"}`, nil, []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "by-sweep", Trigger: turn.Automated, State: turn.Sent, SentAt: later}})
	if got.State != intake.Failed || !strings.Contains(got.Error, "your guidance was not sent, ask again once that turn ends") || len(fa.calls()) != 0 {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
}

func TestASweepTurnBeforeTheAcceptIsIgnored(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	before := []turn.Turn{{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "earlier", Trigger: turn.Automated, State: turn.Done, SentAt: at}}
	_, fa, got := raceRun(t, `{}`, before, nil)
	if got.State != intake.Started || len(fa.calls()) != 1 {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
}
