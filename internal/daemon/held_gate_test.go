package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestApprovingAHeldRoundsGateRunsTheRound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	holdByCap(t, s)
	st := gatestest.NewStore(s.deps.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{s.loaded().Gates}, Clock: clockFunc(s.deps.Now), Releaser: s})
	s.deps.Gates, s.deps.GateReader = svc, st
	g, err := svc.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: r7.Unit(), Subject: round.HeldSubject(s.deps.Now())}, holdForYou{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/run_now", `{"source":"cockpit"}`); rec.Code != http.StatusOK {
		t.Fatalf("run_now on the held round: %d %s", rec.Code, rec.Body)
	}
	got, _ := st.Get(ctx, g.ID)
	if got.Status != gate.Approved || got.By != task.ActorPerson {
		t.Fatalf("the held round's gate %+v, want approved by a person", got)
	}
	if !slices.ContainsFunc(fa.calls, func(c string) bool { return strings.HasPrefix(c, "run ") }) {
		t.Fatalf("calls %v, want the held round run", fa.calls)
	}
}

func TestASessionTheUserDrivesApprovesAHeldRoundsGateAsItself(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := newServerOn(t, db, nil, func(*Deps) {})
	s.deps.Actions = &fakeActions{}
	svcT := sessionsOn(db)
	s.deps.Turns = svcT
	mine := adoptSession(t, svcT, "term_2", true)
	holdByCap(t, s)
	st := gatestest.NewStore(s.deps.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{s.loaded().Gates}, Clock: clockFunc(s.deps.Now), Releaser: s, Sessions: svcT})
	s.deps.Gates, s.deps.GateReader = svc, st
	own, err := svc.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: r7.Unit(), Subject: round.HeldSubject(s.deps.Now()), Session: mine}, holdForYou{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/units/github/acme%2Fapi%237/run_now"
	if rec := postFrom(t, s, "term_2", path, `{"source":"cockpit"}`); rec.Code == http.StatusOK {
		t.Fatalf("a session approved the held round its own turn produced: %d %s", rec.Code, rec.Body)
	}
	if got, _ := st.Get(ctx, own.ID); got.Status != gate.Open {
		t.Fatalf("the refused approval left the gate %+v", got)
	}
	if err := svc.Supersede(ctx, own.ID, task.ActorPerson); err != nil {
		t.Fatal(err)
	}
	g, err := svc.Open(ctx, gate.Gate{Kind: gate.KindPlan, Unit: r7.Unit(), Subject: round.HeldSubject(s.deps.Now())}, holdForYou{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := postFrom(t, s, "term_2", path, `{"source":"cockpit"}`); rec.Code != http.StatusOK {
		t.Fatalf("run_now on the held round from a session the user drives: %d %s", rec.Code, rec.Body)
	}
	if got, _ := st.Get(ctx, g.ID); got.Status != gate.Approved || got.By != task.ActorAgent || got.BySession != mine {
		t.Fatalf("the held round's gate %+v, want approved by an agent through %s", got, mine)
	}
}
