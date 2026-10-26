package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// lockedActions are fakeActions an intake's goroutine may call while the
// test reads them.
type lockedActions struct {
	mu sync.Mutex
	a  fakeActions
}

func (l *lockedActions) do(f func(a *fakeActions) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return f(&l.a)
}

func (l *lockedActions) Open(ctx context.Context, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.Open(ctx, r, c) })
}
func (l *lockedActions) Adopt(ctx context.Context, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.Adopt(ctx, r, c) })
}
func (l *lockedActions) Intent(ctx context.Context, name string, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.Intent(ctx, name, r, c) })
}
func (l *lockedActions) Run(ctx context.Context, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.Run(ctx, r, c) })
}
func (l *lockedActions) TrustAndRun(ctx context.Context, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.TrustAndRun(ctx, r, c) })
}
func (l *lockedActions) Unblock(ctx context.Context, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.Unblock(ctx, r, c) })
}
func (l *lockedActions) Remove(ctx context.Context, r unit.Unit, c Call) error {
	return l.do(func(a *fakeActions) error { return a.Remove(ctx, r, c) })
}

func (l *lockedActions) calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.a.calls)
}

var waitBriefing = intake.Readiness{Wait: "briefing", Attempt: 1}

// newIntakeServer is a server whose review, address and run go through
// intakes, with acme/api#7 open in its store.
func newIntakeServer(t *testing.T, briefs intake.Briefs, change func(*Deps)) (*Server, *lockedActions) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := inventory.New(db)
	if err := st.UpsertPR(context.Background(), dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h2", URL: r7.URL(), ReviewRequests: []string{"me"}, Reasons: []string{"review-requested"}}); err != nil {
		t.Fatal(err)
	}
	fa := &lockedActions{a: fakeActions{out: "review sent\n"}}
	d := withStore(Deps{Activity: db, Cfg: config.Defaults(), Version: "test", Actions: fa, Intakes: db.Intakes(), IntakeBriefs: briefs,
		Viewer: func(context.Context) (string, []string, error) { return "me", nil, nil }, Log: t.Logf}, st)
	if change != nil {
		change(&d)
	}
	s := New(d)
	t.Cleanup(s.stopLife)
	return s, fa
}

func intakeOf(t *testing.T, s *Server, seq int64) intake.Intake {
	t.Helper()
	i, ok, err := s.deps.Intakes.Intake(context.Background(), seq)
	if err != nil || !ok {
		t.Fatalf("intake %d: %v %v", seq, ok, err)
	}
	return i
}

func ask(t *testing.T, s *Server, action, body string) api.Intake {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["source"]; !ok {
		fields["source"] = "cockpit"
	}
	raw, _ := json.Marshal(fields)
	body = string(raw)
	rec := post(t, s, "/api/units/github/acme%2Fapi%237/"+action, body)
	var res api.ActionResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Intake == nil {
		t.Fatalf("%s: %d %s", action, rec.Code, rec.Body)
	}
	return *res.Intake
}

func TestReviewActionAnswersWithTheIntake(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, nil, nil)
	in := ask(t, s, "review", `{"guidance":"look at the migration","source":"cli"}`)
	if in.Seq == 0 || in.PR != r7.URL() || in.Intent != "review" || in.Joined {
		t.Fatalf("intake %+v", in)
	}
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Started })
	got := intakeOf(t, s, in.Seq)
	if got.Source != "cli" || got.Brief != intake.BriefOff {
		t.Fatalf("stored %+v", got)
	}
	if !slices.Equal(fa.calls(), []string{"review acme/api#7 look at the migration"}) {
		t.Fatalf("calls %v", fa.calls())
	}
	if !slices.Contains(got.Log, "starting the review") || !slices.Contains(got.Log, "review sent") {
		t.Fatalf("log %q", got.Log)
	}
	evs, _ := s.store().Events(context.Background(), r7)
	if !slices.ContainsFunc(evs, func(e dinv.Event) bool { return e.Kind == dinv.KindRoundSent && e.Data == askedByYou }) {
		t.Fatalf("no round_sent: %+v", evs)
	}
}

func TestRunTakesTheIntentOfWhoseThePullRequestIs(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, nil, nil)
	holdByCap(t, s)
	if in := ask(t, s, "run_now", `{"source":"cockpit"}`); in.Intent != "review" {
		t.Fatalf("intake %+v", in)
	}
	waitFor(t, func() bool { return slices.Equal(fa.calls(), []string{"run acme/api#7"}) })
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/review", `{"source":"terminal"}`); rec.Code != 400 {
		t.Fatalf("an unknown source: %d %s", rec.Code, rec.Body)
	}
}

func TestSecondReviewJoinsAndReplacesTheGuidance(t *testing.T) {
	t.Parallel()
	briefs := intaketest.NewBriefs(waitBriefing, intake.Readiness{Go: true})
	s, fa := newIntakeServer(t, briefs, nil)
	first := ask(t, s, "review", `{"source":"cockpit","guidance":"first"}`)
	waitFor(t, func() bool { return intakeOf(t, s, first.Seq).State == intake.Briefing })
	second := ask(t, s, "review", `{"source":"cockpit","guidance":"second"}`)
	if second.Seq != first.Seq || !second.Joined || second.Note != intake.NoteGuidance {
		t.Fatalf("second %+v", second)
	}
	briefs.End(pr.Ref(r7).Unit())
	waitFor(t, func() bool { return intakeOf(t, s, first.Seq).State == intake.Started })
	if !slices.Equal(fa.calls(), []string{"review acme/api#7 second"}) {
		t.Fatalf("calls %v", fa.calls())
	}
}

func TestIntakeLongPollWakesOnALogLine(t *testing.T) {
	t.Parallel()
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Briefing })
	var polled api.Intake
	if code := get(t, s, fmt.Sprintf("/api/intakes/%d", in.Seq), &polled); code != 200 {
		t.Fatalf("%d", code)
	}
	if polled.Note != "briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)" || polled.Attempt != 1 || !slices.Equal(polled.Log, []string{"briefing acme/api#7 (attempt 1; briefs take 10 to 20 s)"}) {
		t.Fatalf("polled %+v", polled)
	}
	done := make(chan api.Intake)
	go func() {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, apiRequest("GET", fmt.Sprintf("/api/intakes/%d?after=%d&wait=20", in.Seq, polled.LogLen), nil))
		var got api.Intake
		json.Unmarshal(rec.Body.Bytes(), &got)
		done <- got
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if rec := post(t, s, fmt.Sprintf("/api/intakes/%d/without", in.Seq), ""); rec.Code != 200 {
		t.Fatalf("without: %d %s", rec.Code, rec.Body)
	}
	select {
	case got := <-done:
		if len(got.Log) == 0 || got.Log[0] != intake.NoteWithout || got.LogLen <= polled.LogLen {
			t.Fatalf("woke with %+v", got)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("the poll waited out its time")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the poll never woke")
	}
}

func TestWithoutAndCancelEndpoints(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	review := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, review.Seq).State == intake.Briefing })
	if rec := post(t, s, fmt.Sprintf("/api/intakes/%d/without", review.Seq), ""); rec.Code != 200 {
		t.Fatalf("without: %d %s", rec.Code, rec.Body)
	}
	waitFor(t, func() bool { return intakeOf(t, s, review.Seq).State == intake.Started })
	if got := intakeOf(t, s, review.Seq); got.Brief != intake.BriefWithout || !slices.Equal(fa.calls(), []string{"review acme/api#7 "}) {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
	s.store().SetOverride(context.Background(), dinv.Override{PR: r7, Adopted: true})
	address := ask(t, s, "address", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, address.Seq).State == intake.Briefing })
	if rec := post(t, s, fmt.Sprintf("/api/intakes/%d/cancel", address.Seq), ""); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if got := intakeOf(t, s, address.Seq); got.State != intake.Canceled {
		t.Fatalf("intake %+v", got)
	}
	for path, want := range map[string]int{
		fmt.Sprintf("/api/intakes/%d/cancel", review.Seq): 409,
		"/api/intakes/999/without":                        404,
		"/api/intakes/nope/cancel":                        400,
	} {
		if rec := post(t, s, path, ""); rec.Code != want {
			t.Errorf("%s: %d %s, want %d", path, rec.Code, rec.Body, want)
		}
	}
	if code := get(t, s, "/api/intakes/999", nil); code != 404 {
		t.Errorf("an unknown intake: %d", code)
	}
}

func TestRowsShowTheOpenIntake(t *testing.T) {
	t.Parallel()
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Briefing })
	var rows []api.Row
	if code := get(t, s, "/api/rows", &rows); code != 200 || len(rows) != 1 || payload(rows[0]).Intake == nil {
		t.Fatalf("%d %+v", code, rows)
	}
	if got := payload(rows[0]).Intake; got.Seq != in.Seq || got.State != "briefing" || got.Log != nil {
		t.Fatalf("row intake %+v", got)
	}
	var d api.Detail
	if code := get(t, s, "/api/units/github/acme%2Fapi%237", &d); code != 200 || payload(d.Row).Intake == nil || payload(d.Row).Intake.Seq != in.Seq {
		t.Fatalf("%d %+v", code, payload(d.Row).Intake)
	}
}

func TestRecoveredIntakesAreDrivenAgain(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, nil, nil)
	ctx := context.Background()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	waiting, _, err := s.deps.Intakes.Accept(ctx, intake.Intake{Unit: r7.Unit(), Intent: turn.Intent(dintent.Review), Source: "cli", Request: []byte(`{"action":"review","guidance":"again"}`), State: intake.Briefing, AcceptedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	s.recoverIntakes(ctx)
	waitFor(t, func() bool { return intakeOf(t, s, waiting.Seq).State == intake.Started })
	if !slices.Equal(fa.calls(), []string{"review acme/api#7 again"}) {
		t.Fatalf("calls %v", fa.calls())
	}
}

type byHandCalls struct {
	mu    sync.Mutex
	calls []string
}

func (b *byHandCalls) run(_ context.Context, r api.ByHandRequest, handle string) (api.ByHandResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, r.Intent+" "+handle)
	return api.ByHandResult{Text: "the instructions", Notes: []string{"linked the worktree"}}, nil
}

func (b *byHandCalls) list() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

func TestTurnByHandWaitsUpToSessionWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		briefs := intaketest.NewBriefs(waitBriefing)
		hand := &byHandCalls{}
		s, _ := newIntakeServer(t, briefs, func(d *Deps) {
			d.TurnByHand = hand.run
			d.ByHandRef = func(context.Context, api.ByHandRequest, string) (unit.Unit, error) { return r7.Unit(), nil }
		})
		s.sessionWait = 100 * time.Millisecond
		byHand := func() *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"review","cwd":"/w"}`))
			req.Header.Set(api.SessionHeader, "term-1")
			s.Handler().ServeHTTP(rec, req)
			return rec
		}
		start := time.Now()
		rec := byHand()
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "the brief of acme/api#7 is not ready yet (briefing, attempt 1); run this command again in a minute") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if took := time.Since(start); took != 100*time.Millisecond {
			t.Fatalf("answered after %v", took)
		}
		if len(hand.list()) != 0 {
			t.Fatalf("started without its brief: %v", hand.list())
		}
		briefs.Answer(intake.Readiness{Go: true})
		s.sessionWait = brief.SessionWait
		rec = byHand()
		var res api.ByHandResult
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Text != "the instructions" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if !slices.Equal(hand.list(), []string{"review term-1"}) {
			t.Fatalf("calls %v", hand.list())
		}
		open, _ := s.deps.Intakes.Open(context.Background())
		if len(open) != 0 {
			t.Fatalf("open intakes %+v", open)
		}
	})
}

type openBreaker struct{ stubBriefs }

func (openBreaker) Breaker() (bool, time.Time, string, time.Time) {
	since := time.Date(2026, 10, 8, 13, 40, 0, 0, time.Local)
	return true, since, "claude is rate limited", since.Add(15 * time.Minute)
}

func TestBreakerBanner(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { withBriefs(d, openBreaker{}) })
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != 200 {
		t.Fatalf("%d", code)
	}
	if h.Briefs == nil || !h.Briefs.Open || h.Briefs.Error != "claude is rate limited" {
		t.Fatalf("briefs %+v", h.Briefs)
	}
	if !slices.Contains(h.Warnings, "briefs unavailable since 13:40: claude is rate limited; "+brand.Name+" tries again at 13:55") {
		t.Fatalf("warnings %q", h.Warnings)
	}
}

func byHandAs(t *testing.T, s *Server, handle, intent string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"`+intent+`","cwd":"/w"}`))
	req.Header.Set(api.SessionHeader, handle)
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func newByHandServer(t *testing.T, briefs intake.Briefs, wait time.Duration) (*Server, *lockedActions, *byHandCalls) {
	t.Helper()
	hand := &byHandCalls{}
	s, fa := newIntakeServer(t, briefs, func(d *Deps) {
		d.TurnByHand = hand.run
		d.ByHandRef = func(context.Context, api.ByHandRequest, string) (unit.Unit, error) { return r7.Unit(), nil }
		d.SessionWait = wait
	})
	return s, fa, hand
}

func TestADaemonRequestDoesNotJoinATurnByHand(t *testing.T) {
	t.Parallel()
	s, fa, _ := newByHandServer(t, intaketest.NewBriefs(waitBriefing), time.Hour)
	asked := make(chan int)
	go func() { asked <- byHandAs(t, s, "term-1", "review").Code }()
	var open []intake.Intake
	waitFor(t, func() bool {
		open, _ = s.deps.Intakes.Open(context.Background())
		return len(open) == 1 && open[0].State == intake.Briefing
	})
	t.Cleanup(func() {
		s.intakes.Cancel(context.Background(), open[0].Seq)
		<-asked
	})
	rec := post(t, s, "/api/units/github/acme%2Fapi%237/review", `{"guidance":"mine","source":"cli"}`)
	want := fmt.Sprintf("intake %d already asks for this pull request's review from an agent session; cancel it with "+brand.Command+" intake cancel %d", open[0].Seq, open[0].Seq)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := intakeOf(t, s, open[0].Seq); got.Note == intake.NoteGuidance || len(fa.calls()) != 0 {
		t.Fatalf("the refused join changed the intake: %+v, calls %v", got, fa.calls())
	}
}

func TestATurnByHandDoesNotJoinADaemonRequest(t *testing.T) {
	t.Parallel()
	s, _, hand := newByHandServer(t, intaketest.NewBriefs(waitBriefing), 50*time.Millisecond)
	in := ask(t, s, "review", `{"guidance":"from the cli","source":"cli"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Briefing })
	rec := byHandAs(t, s, "term-1", "review")
	want := fmt.Sprintf("intake %d already asks for this pull request's review from cli; cancel it with "+brand.Command+" intake cancel %d", in.Seq, in.Seq)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := intakeOf(t, s, in.Seq); got.Note == intake.NoteGuidance || len(hand.list()) != 0 {
		t.Fatalf("the refused join changed the intake: %+v", got)
	}
}

func TestAnotherSessionDoesNotJoin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, hand := newByHandServer(t, intaketest.NewBriefs(waitBriefing), 50*time.Millisecond)
		if rec := byHandAs(t, s, "term-1", "review"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "not ready yet") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		rec := byHandAs(t, s, "term-2", "review")
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already asks for this pull request's review from another session") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if len(hand.list()) != 0 {
			t.Fatalf("calls %v", hand.list())
		}
	})
}

func TestARunDoesNotJoinAReview(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	holdByCap(t, s)
	rec := post(t, s, "/api/units/github/acme%2Fapi%237/run_now", `{"source":"cockpit"}`)
	want := fmt.Sprintf("intake %d already asks for this pull request's review as a review; cancel it with "+brand.Command+" intake cancel %d", in.Seq, in.Seq)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) || len(fa.calls()) != 0 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, fa.calls())
	}
}

func TestStaleSessionIntakeExpiresOnTick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		briefs := intaketest.NewBriefs(waitBriefing)
		s, _, _ := newByHandServer(t, briefs, 30*time.Millisecond)
		if rec := byHandAs(t, s, "term-1", "review"); rec.Code != 409 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		open, _ := s.deps.Intakes.Open(context.Background())
		if len(open) != 1 {
			t.Fatalf("open %+v", open)
		}
		seq := open[0].Seq
		var rows []api.Row
		if get(t, s, "/api/rows", &rows); len(rows) != 1 || payload(rows[0]).Intake == nil {
			t.Fatalf("a fresh session intake left the rows: %+v", rows)
		}
		time.Sleep(80 * time.Millisecond)
		get(t, s, "/api/rows", nil)
		if got := intakeOf(t, s, seq); got.State == intake.Canceled {
			t.Fatalf("a read canceled the intake: %+v", got)
		}
		if err := s.readModel().Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		rows = nil
		if get(t, s, "/api/rows", &rows); len(rows) != 1 || payload(rows[0]).Intake != nil {
			t.Fatalf("a canceled session intake shows: %+v", payload(rows[0]).Intake)
		}
		got := intakeOf(t, s, seq)
		if got.State != intake.Canceled || got.Note != noteSessionStopped {
			t.Fatalf("intake %+v", got)
		}
		if !slices.Contains(briefs.Unwanted(), r7.Unit()) {
			t.Fatalf("the want stays: %v", briefs.Unwanted())
		}
		in := ask(t, s, "review", `{"source":"cli"}`)
		if in.Seq == seq || in.Joined {
			t.Fatalf("joined the expired intake: %+v", in)
		}
	})
}

func TestStaleSessionIntakesAreCanceledOnAccept(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := newByHandServer(t, intaketest.NewBriefs(waitBriefing), 30*time.Millisecond)
		if rec := byHandAs(t, s, "term-1", "review"); rec.Code != 409 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		time.Sleep(80 * time.Millisecond)
		in := ask(t, s, "review", `{"source":"cli"}`)
		if in.Joined {
			t.Fatalf("joined %+v", in)
		}
		if got := intakeOf(t, s, 1); got.State != intake.Canceled {
			t.Fatalf("intake 1 %+v", got)
		}
	})
}

func TestStartUpCancelsSessionIntakes(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, nil, nil)
	ctx := context.Background()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	session, _, err := s.deps.Intakes.Accept(ctx, intake.Intake{Unit: r7.Unit(), Intent: turn.Intent(dintent.Review), Source: sourceSession, Request: []byte(`{"intent":"review","handle":"term-1"}`), State: intake.Briefing, AcceptedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	s.sessionAsks.begin(session.Seq, nil)
	s.sessionAsks.end(session.Seq, at)
	s.recoverIntakes(ctx)
	if s.sessionAsks.known(session.Seq) {
		t.Fatal("the canceled intake's ask is kept")
	}
	if got := intakeOf(t, s, session.Seq); got.State != intake.Canceled || got.Note != noteDaemonRestarted {
		t.Fatalf("intake %+v", got)
	}
	if len(fa.calls()) != 0 {
		t.Fatalf("calls %v", fa.calls())
	}
}

func TestWithoutRefusesATurnByHand(t *testing.T) {
	t.Parallel()
	s, _, _ := newByHandServer(t, intaketest.NewBriefs(waitBriefing), time.Hour)
	asked := make(chan int)
	go func() { asked <- byHandAs(t, s, "term-1", "review").Code }()
	waitFor(t, func() bool { open, _ := s.deps.Intakes.Open(context.Background()); return len(open) == 1 })
	rec := post(t, s, "/api/intakes/1/without", "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "only its session starts") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/intakes/1/cancel", ""); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	var in api.Intake
	if code := get(t, s, "/api/intakes/1", &in); code != 200 || in.Source != sourceSession || in.State != "canceled" {
		t.Fatalf("%d %+v", code, in)
	}
	<-asked
	if s.sessionAsks.known(1) {
		t.Fatal("the canceled intake's ask is kept")
	}
}

func (a *sessionAsks) known(seq int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, last := a.last[seq]
	return last || a.asking[seq]
}

func TestAJoinIsNeverExpired(t *testing.T) {
	t.Parallel()
	const wait = 20 * time.Millisecond
	s, _, _ := newByHandServer(t, intaketest.NewBriefs(waitBriefing), wait)
	stop := make(chan struct{})
	read := make(chan struct{})
	go func() {
		defer close(read)
		for {
			select {
			case <-stop:
				return
			default:
				var rows []api.Row
				get(t, s, "/api/rows", &rows)
			}
		}
	}()
	defer func() { close(stop); <-read }()
	for n := range 15 {
		rec := byHandAs(t, s, "term-1", "review")
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "not ready yet") {
			t.Fatalf("call %d: %d %s", n, rec.Code, rec.Body)
		}
		time.Sleep(2*wait - time.Duration(n%5)*time.Millisecond)
	}
}

func TestAStopWhileWaitingForTheLoopLeavesTheIntakeStarting(t *testing.T) {
	t.Parallel()
	mu := &sync.Mutex{}
	s, fa := newIntakeServer(t, nil, func(d *Deps) { d.LoopMu = mu })
	mu.Lock()
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Starting })
	s.stopLife()
	stopped := make(chan struct{})
	go func() { s.driving.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the drive waited for the loop mutex past the server's life")
	}
	mu.Unlock()
	if got := intakeOf(t, s, in.Seq); got.State != intake.Starting || len(fa.calls()) != 0 {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
}

func TestTheStartHoldsTheLoopMutex(t *testing.T) {
	t.Parallel()
	mu := &sync.Mutex{}
	held := make(chan bool, 1)
	s, _ := newIntakeServer(t, nil, func(d *Deps) {
		d.LoopMu = mu
		d.Actions = &heldActions{mu: mu, held: held}
	})
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Started })
	if !<-held {
		t.Fatal("the action ran without the loop mutex")
	}
	if !mu.TryLock() {
		t.Fatal("the loop mutex is still held")
	}
}

// heldActions reports whether the loop mutex is held while a review runs.
type heldActions struct {
	lockedActions
	mu   *sync.Mutex
	held chan bool
}

func (h *heldActions) Intent(context.Context, string, unit.Unit, Call) error {
	free := h.mu.TryLock()
	if free {
		h.mu.Unlock()
	}
	h.held <- !free
	return nil
}

func TestWireAttemptsComeFromTheBriefRecord(t *testing.T) {
	t.Parallel()
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), func(d *Deps) {
		d.Now = func() time.Time { return time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC) }
	})
	ctx := context.Background()
	st := s.store()
	start := time.Date(2026, 10, 8, 9, 59, 0, 0, time.UTC)
	id, err := st.StartBriefRun(ctx, dinv.BriefRun{PR: r7, Trigger: "wanted", Version: "v1", AttemptOf: "h2", StartedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	failed := dinv.BriefRun{ID: id, PR: r7, Trigger: "wanted", Version: "v1", AttemptOf: "h2", StartedAt: start, EndedAt: start.Add(10 * time.Second), Outcome: "timeout", Error: "the brief timed out"}
	retry := time.Date(2026, 10, 8, 10, 1, 0, 0, time.UTC)
	if err := st.EndBriefRun(ctx, failed, dinv.BriefRecord{PR: r7, AttemptOf: "h2", Failures: 1, RetryAt: retry}); err != nil {
		t.Fatal(err)
	}
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	var got api.Intake
	if code := get(t, s, fmt.Sprintf("/api/intakes/%d", in.Seq), &got); code != 200 {
		t.Fatalf("%d", code)
	}
	if got.Attempt != 2 || got.LastError != "the brief timed out" || !got.Next.Equal(retry) {
		t.Fatalf("intake %+v", got)
	}
	if want := "the brief of acme/api#7 is not ready yet (retry, attempt 2); run this command again in a minute"; notReady(pr.Ref(r7).Unit(), got) != want {
		t.Fatalf("%q", notReady(pr.Ref(r7).Unit(), got))
	}
}

type attemptBriefs struct{ stubBriefs }

func (attemptBriefs) Attempt(context.Context, github.Ref) (int, error) { return 3, nil }

func (attemptBriefs) AttemptOf(dinv.PR, dinv.BriefRecord) int { return 3 }

func TestWireAttemptsCountTheQueuesGlobalTries(t *testing.T) {
	t.Parallel()
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), func(d *Deps) { withBriefs(d, attemptBriefs{}) })
	ctx := context.Background()
	st := s.store()
	start := time.Date(2026, 10, 8, 9, 59, 0, 0, time.UTC)
	id, err := st.StartBriefRun(ctx, dinv.BriefRun{PR: r7, Trigger: "wanted", Version: "v1", AttemptOf: "h2", StartedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	failed := dinv.BriefRun{ID: id, PR: r7, Trigger: "wanted", Version: "v1", AttemptOf: "h2", StartedAt: start, EndedAt: start.Add(10 * time.Second), Outcome: "unavailable", Error: "529"}
	if err := st.EndBriefRun(ctx, failed, dinv.BriefRecord{PR: r7, AttemptOf: "h2"}); err != nil {
		t.Fatal(err)
	}
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	var got api.Intake
	if code := get(t, s, fmt.Sprintf("/api/intakes/%d", in.Seq), &got); code != 200 || got.Attempt != 3 {
		t.Fatalf("%d %+v", code, got)
	}
}

func TestBreakerBannerSaysATrialRunsNow(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 8, 13, 40, 0, 0, time.Local)
	b := api.BriefsHealth{Open: true, Since: since, Error: "429", Next: since.Add(15 * time.Minute)}
	if got := breakerWarning(b, since.Add(20*time.Minute)); got != "briefs unavailable since 13:40: 429; a trial runs now" {
		t.Fatalf("%q", got)
	}
	if got := breakerWarning(b, since); got != "briefs unavailable since 13:40: 429; "+brand.Name+" tries again at 13:55" {
		t.Fatalf("%q", got)
	}
}

func TestRowIntakeRetryIsAsOfTheMinute(t *testing.T) {
	t.Parallel()
	retry := time.Date(2026, 10, 8, 10, 0, 30, 0, time.UTC)
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), func(d *Deps) {
		d.Now = func() time.Time { return retry.Add(10 * time.Second) }
	})
	i := intake.Intake{Unit: r7.Unit(), State: intake.Briefing}
	got := s.rowIntake(i, dinv.BriefRecord{PR: r7, Failures: 1, RetryAt: retry}, true, func() (int, error) { return 2, nil })
	if !got.Next.Equal(retry) {
		t.Fatalf("next %v, want %v as of 10:00", got.Next, retry)
	}
}
