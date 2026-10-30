package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// newLiveServer is a server over a store seeded at live size, with the
// change log and the snapshot the read model runs on in production.
func newLiveServer(t *testing.T) (*Server, *storetest.Fixture) {
	t.Helper()
	f := storetest.Seeded(t, storetest.Live)
	if err := ghsource.KeepIdentity(context.Background(), f.Inventory, identityOf("user-01")); err != nil {
		t.Fatal(err)
	}
	keepReports(t, f.Inventory)
	now := f.Now
	sessions := turnstest.NewSessions()
	d := withStore(Deps{Activity: f.DB, Changes: f.Log, Snapshot: f.DB, Intakes: f.DB.Intakes(), Pauses: f.Rounds, Cfg: config.Defaults(), Version: "test",
		Now:      func() time.Time { return now },
		Identity: viewerIs("user-01"),
		Log:      func(string, ...any) {},
		Turns:    &turns.Service{Intents: turnstest.Intents(), Store: f.Turns, Registry: f.Turns, Sessions: sessions, Clock: fixedClock(now)},
		Sessions: sessions, Agents: snapshotOf(sessions), Items: &coreitems.Ledger{Store: f.DB.Items()}, Cards: f.DB.Cards()}, f.Inventory)
	githubOf(&d).View = func(context.Context, src.ViewRequest) (json.RawMessage, error) { return json.RawMessage(`[]`), nil }
	s := New(d)
	s.token = ""
	stopOnCleanup(t, s)
	return s, f
}

func writes() metertest.Budget {
	return metertest.Budget{{Res: meter.SQL, Op: "exec"}: 0, {Res: meter.Tx, Op: "write"}: 0}
}

func TestGetPathWritesNothing(t *testing.T) {
	t.Parallel()
	s, f := newLiveServer(t)
	ctx := context.Background()
	ref := f.Open[0]
	if err := muteBy(f.Inventory, ref, time.Time{}); err != nil {
		t.Fatal(err)
	}
	fill := strings.NewReplacer("{provider}", string(unit.GitHub), "{key}", url.PathEscape(ref.String()), "{seq}", "1")
	query := map[string]string{api.AdminTurn: "?pr=" + ref.URL(), api.AgentAnswer: "?pr=" + ref.URL(), api.AdminFacts: "?pr=" + ref.URL()}
	mctx, sc := metertest.Start(t, ctx)
	routes := 0
	for _, op := range api.Operations {
		if op.Method != "GET" || op.Path == "/api/events" {
			continue
		}
		path := fill.Replace(op.Path) + query[op.Path]
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", path, nil).WithContext(mctx))
		t.Logf("GET %s: %d", path, rec.Code)
		if strings.HasPrefix(op.Path, "/api/units/") && rec.Code != 200 {
			t.Errorf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		routes++
	}
	metertest.Within(t, sc, writes())
	if routes < 15 {
		t.Fatalf("only %d GET routes ran", routes)
	}
	if ws, _ := f.DB.Wakes().UnitWakes(ctx, []unit.Unit{ref.Unit()}); ws[ref.Unit()].When == "" {
		t.Fatalf("a GET cleared the mute: %+v", ws)
	}
}

func TestNoMuteNoWork(t *testing.T) {
	t.Parallel()
	s, _ := newLiveServer(t)
	if _, err := s.readModel().Rows(context.Background(), api.Since{}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.keepCurrent(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, sc := metertest.Start(t, context.Background())
	if err := s.keepCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: 0, {Res: meter.Tx}: 0})
}

func TestQuietCycleRecordsNoSituation(t *testing.T) {
	t.Parallel()
	s, f := newLiveServer(t)
	ctx := context.Background()
	if err := s.readModel().Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	mctx, sc := metertest.Start(t, ctx)
	for range 3 {
		if err := s.readModel().Refresh(mctx); err != nil {
			t.Fatal(err)
		}
	}
	metertest.Within(t, sc, writes())
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "read"}); n != 1 {
		t.Errorf("%d rebuilds after the first snapshot's situations, want 1", n)
	}
	if got := situationsOf(t, s, f.Open[0]); len(got) == 0 {
		t.Fatal("the first snapshot recorded no situation of a followed open pull request")
	}
}

func TestChangedSituationsOneTx(t *testing.T) {
	t.Parallel()
	s, f := newLiveServer(t)
	ctx := context.Background()
	m := s.readModel()
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	refs := f.Open[:3]
	before := map[string]int{}
	for _, ref := range refs {
		before[ref.String()] = len(situationsOf(t, s, ref))
		if err := muteBy(f.Inventory, ref, f.Now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	mctx, sc := metertest.Start(t, ctx)
	if err := m.Refresh(mctx); err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "write"}); n != 1 {
		t.Fatalf("%d write transactions for 3 changed situations, want 1", n)
	}
	for _, ref := range refs {
		got := situationsOf(t, s, ref)
		if len(got) != before[ref.String()]+1 || got[len(got)-1] != "muted:nobody" {
			t.Fatalf("%s: situations %v", ref, got)
		}
	}
}

func TestCheckReportsAStaleRowInDoctorAndHealth(t *testing.T) {
	t.Parallel()
	s, f := newLiveServer(t)
	log := &lineLog{}
	s.deps.Log = log.logf
	if _, err := s.readModel().Rows(context.Background(), api.Since{}); err != nil {
		t.Fatal(err)
	}
	var rep api.DoctorReport
	get(t, s, api.AdminDoctor, &rep)
	if c := checkNamed(rep, "read model"); c.Mark != api.DoctorInfo {
		t.Fatalf("a clean read model: %+v", c)
	}
	ref := f.Open[0]
	sqlitetest.Exec(t, f.DB, `UPDATE unit_snapshots SET snapshot = json_set(snapshot, '$.data.pr.Title', 'Written behind the log') WHERE provider = ? AND unit_key = ?`,
		string(unit.GitHub), ref.String())
	get(t, s, api.AdminDoctor, &rep)
	if c := checkNamed(rep, "read model"); c.Mark != api.DoctorMissing || !strings.Contains(c.Info, ref.String()) || !strings.Contains(c.Info, "title") {
		t.Fatalf("a row written without a note: %+v", c)
	}
	if n := log.count("readmodel: mismatch " + ref.Unit().String() + ": differs in title"); n != 1 {
		t.Fatalf("logged the mismatch %d times: %q", n, log.lines)
	}
	h := getHealth(t, s)
	if !strings.Contains(strings.Join(h.Warnings, "\n"), "read model: 1 rows differ") {
		t.Fatalf("health warnings %q", h.Warnings)
	}
}

func checkNamed(rep api.DoctorReport, name string) api.DoctorCheck {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	return api.DoctorCheck{}
}
