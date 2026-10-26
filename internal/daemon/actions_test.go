package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type fakeActions struct {
	calls       []string
	profiles    []string
	out         string
	err         error
	refuse      map[github.Ref]string
	broken      map[github.Ref]error
	ids         []string
	forced      []bool
	takeOver    []bool
	maintainers []bool
	inputs      []map[string]any
	// bound are the items and entry of each intent call, as "i1,i2@entry".
	bound []string
}

func (f *fakeActions) record(ctx context.Context, call string, c Call) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "action"})
	f.calls = append(f.calls, call)
	f.profiles = append(f.profiles, c.AgentHome)
	if f.out != "" {
		io.WriteString(c.Out, f.out)
	}
	return f.err
}

func (f *fakeActions) Open(ctx context.Context, u unit.Unit, c Call) error {
	r := refOf(u)
	return f.record(ctx, "open "+r.String(), c)
}
func (f *fakeActions) Adopt(ctx context.Context, u unit.Unit, c Call) error {
	r := refOf(u)
	return f.record(ctx, "adopt "+r.String(), c)
}
func (f *fakeActions) Intent(ctx context.Context, name string, u unit.Unit, c Call) error {
	r := refOf(u)
	f.inputs = append(f.inputs, c.Inputs)
	f.bound = append(f.bound, strings.Join(c.Items, ",")+"@"+c.Entry)
	switch name {
	case "address":
		f.takeOver = append(f.takeOver, inputFlag(c.Inputs, "take_over"))
	case "nudge":
		f.maintainers = append(f.maintainers, inputFlag(c.Inputs, "waits_on_maintainers"))
		situation, _ := c.Inputs["situation"].(string)
		return f.record(ctx, "nudge "+r.String()+" "+situation+"|"+c.Guidance, c)
	}
	return f.record(ctx, name+" "+r.String()+" "+c.Guidance, c)
}
func (f *fakeActions) Run(ctx context.Context, u unit.Unit, c Call) error {
	r := refOf(u)
	return f.record(ctx, "run "+r.String(), c)
}
func (f *fakeActions) TrustAndRun(ctx context.Context, u unit.Unit, c Call) error {
	r := refOf(u)
	return f.record(ctx, "trust_run "+r.String(), c)
}
func (f *fakeActions) Unblock(ctx context.Context, u unit.Unit, c Call) error {
	r := refOf(u)
	return f.record(ctx, "unblock "+r.String(), c)
}

func (f *fakeActions) Remove(ctx context.Context, u unit.Unit, c Call) error {
	r := refOf(u)
	f.ids = append(f.ids, c.WorkspaceID)
	f.forced = append(f.forced, c.Force)
	if why, ok := f.refuse[r]; ok {
		return Refusal{Reason: why}
	}
	if err, ok := f.broken[r]; ok {
		return err
	}
	return f.record(ctx, "remove "+r.String(), c)
}

func post(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := apiRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	return rec
}

var r7 = github.Ref{Owner: "acme", Repo: "api", Number: 7}

func TestMuteWritesOverrideAndEvent(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{}`); rec.Code != 400 {
		t.Fatalf("mute without a duration: %d", rec.Code)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until":"2d"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	o, _ := s.store().Override(context.Background(), r7)
	if o.MutedUntil.IsZero() {
		t.Fatalf("not muted: %+v", o)
	}
	evs, _ := s.store().Events(context.Background(), r7)
	if evs[len(evs)-1].Kind != dinv.KindMuted {
		t.Fatalf("last event %+v", evs[len(evs)-1])
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until":"2d"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no mute on acme/api#7") {
		t.Fatalf("mute a muted pull request: %d %s", rec.Code, rec.Body)
	}
}

func TestPauseAndResumeOfAPullRequest(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	post(t, s, "/api/units/github/acme%2Fapi%237/pause", `{}`)
	o, _ := s.store().Override(context.Background(), r7)
	if !o.Hold {
		t.Fatal("not paused")
	}
	post(t, s, "/api/units/github/acme%2Fapi%237/resume", `{}`)
	o, _ = s.store().Override(context.Background(), r7)
	if o.Hold {
		t.Fatal("still paused")
	}
}

func TestPauseRequestNamesPullRequestsAndResumingEverythingResumesThem(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/pause", `{"paused":true,"prs":["acme/api#7"]}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if o, _ := s.store().Override(context.Background(), r7); !o.Hold {
		t.Fatal("the pull request is not paused")
	}
	post(t, s, "/api/pause", `{"paused":true,"repos":["acme/api"]}`)
	if rec := post(t, s, "/api/pause", `{"paused":false,"prs":["acme/api#7"]}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "acme/api is paused as a whole") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/pause", `{"paused":true,"prs":["acme/api"]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	post(t, s, "/api/pause", `{"paused":false}`)
	if o, _ := s.store().Override(context.Background(), r7); o.Hold {
		t.Fatal("resuming everything left the pull request paused")
	}
	if p, _ := s.deps.Pauses.Pause(context.Background()); p.Any() {
		t.Fatalf("pause %+v", p)
	}
}

func TestDelegatedActions(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	s.store().AppendEvents(context.Background(), []dinv.Event{{PR: r7, Kind: dinv.KindRoundHeld, At: s.deps.Now().Add(time.Second), Data: "daily cap"}})
	post(t, s, "/api/units/github/acme%2Fapi%237/review", `{"source":"cockpit","guidance":"look at the migration"}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/open", `{}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/run_now", `{"source":"cockpit"}`)
	if !slices.Equal(fa.calls, []string{"review acme/api#7 look at the migration", "open acme/api#7", "run acme/api#7"}) {
		t.Fatalf("%v", fa.calls)
	}
	evs, _ := s.store().Events(context.Background(), r7)
	sent := 0
	for _, e := range evs {
		if e.Kind == dinv.KindRoundSent {
			sent++
		}
	}
	if sent != 2 {
		t.Fatalf("round_sent events: %d", sent)
	}
}

func TestAStartBlockOnAnUntrustedFolderOffersTrustAndDismiss(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		action, call, kind, data string
	}{
		{"trust_run", "trust_run acme/api#7", dinv.KindRoundSent, "trusted its folder and ran"},
		{"unblock", "unblock acme/api#7", dinv.KindRoundUnblocked, "dismissed by you"},
	} {
		t.Run(c.action, func(t *testing.T) {
			t.Parallel()
			s := newServer(t)
			fa := &fakeActions{}
			s.deps.Actions = fa
			b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/src/api", Homes: []string{"/home/.claude"}, Round: "triage"}
			at := s.deps.Now().Add(-time.Minute)
			s.store().AppendEvents(context.Background(), []dinv.Event{{PR: r7, Kind: dinv.KindRoundBlocked, Key: b.Key(at), At: at, Data: "could not start: /src/api is not trusted in the agent home of its terminal"}})
			row, err := s.currentRow(context.Background(), r7.Unit())
			if err != nil {
				t.Fatal(err)
			}
			if row.Kind != "blocked" || row.Summary.Reason != "round blocked: could not start: /src/api is not trusted in the agent home of its terminal" {
				t.Fatalf("row %s: %s", row.Kind, row.Summary.Reason)
			}
			if st, ok := offerOf(row, c.action); !ok || !st.Enabled {
				t.Fatalf("%s not offered: %+v", c.action, row.Actions)
			}
			post(t, s, "/api/units/github/acme%2Fapi%237/"+c.action, `{"source":"cockpit"}`)
			if !slices.Equal(fa.calls, []string{c.call}) {
				t.Fatalf("%v", fa.calls)
			}
			evs, _ := s.store().Events(context.Background(), r7)
			if last := evs[len(evs)-1]; last.Kind != c.kind || last.Data != c.data {
				t.Fatalf("last event %+v", last)
			}
		})
	}
}

// inputFlag is the flag input name in inputs, false when unset or not a bool.
func inputFlag(inputs map[string]any, name string) bool {
	v, _ := inputs[name].(bool)
	return v
}

func TestAddressCarriesTheTakeOver(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	address, _ := s.loaded().Registry.Get(dintent.Address)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cockpit"}`); len(address.Action.Refuse) == 0 || rec.Code != 409 || !strings.Contains(rec.Body.String(), address.Action.Refuse[0].Why.Src) {
		t.Fatalf("address on a pull request not mine: %d %s", rec.Code, rec.Body)
	}
	post(t, s, "/api/units/github/acme%2Fapi%237/take_over", `{}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cli","inputs":{"take_over":true}}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cockpit"}`)
	if !slices.Equal(fa.takeOver, []bool{true, false}) {
		t.Fatalf("%v", fa.takeOver)
	}
}

func TestAddressCarriesTheSelfReview(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	post(t, s, "/api/units/github/acme%2Fapi%237/take_over", `{}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cli","inputs":{"self_review":true}}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cli"}`)
	var got []bool
	for _, in := range fa.inputs {
		got = append(got, inputFlag(in, "self_review"))
	}
	if !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("%v", fa.inputs)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cockpit","inputs":{"self_review":true}}`); rec.Code != 400 || len(fa.inputs) != 2 {
		t.Fatalf("a cockpit self-review: %d %s", rec.Code, rec.Body)
	}
}

func TestActionsUnavailableIs503(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/review", `{"source":"cockpit"}`); rec.Code != 503 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestUnknownActionIs404(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/explode", `{}`); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func countEvents(t *testing.T, s *Server, kind string) int {
	t.Helper()
	evs, err := s.store().Events(context.Background(), r7)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestDelegateErrorIs500AndRecordsNothing(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.deps.Actions = &fakeActions{err: errors.New("orca is down")}
	for _, action := range []string{"review", "take_over"} {
		if rec := post(t, s, "/api/units/github/acme%2Fapi%237/"+action, `{"source":"cockpit"}`); rec.Code != 500 {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body)
		}
	}
	if n := countEvents(t, s, dinv.KindRoundSent) + countEvents(t, s, dinv.KindAdopted); n != 0 {
		t.Fatalf("events recorded for failed actions: %d", n)
	}
	if o, _ := s.store().Override(context.Background(), r7); o.Adopted {
		t.Fatal("adopted after a failed adopt")
	}
}

func TestAdoptMarksOverride(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/take_over", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if o, _ := s.store().Override(context.Background(), r7); !o.Adopted {
		t.Fatalf("not adopted: %+v", o)
	}
	if countEvents(t, s, dinv.KindAdopted) != 1 || !slices.Equal(fa.calls, []string{"adopt acme/api#7"}) {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestActionOutlivesTheRequest(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	ctx, cancel := context.WithCancel(context.Background())
	s.deps.LoopMu.Lock()
	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		req := apiRequest("POST", "/api/units/github/acme%2Fapi%237/review", strings.NewReader(`{"source":"cockpit"}`)).WithContext(ctx)
		s.Handler().ServeHTTP(rec, req)
		done <- rec.Code
	}()
	time.Sleep(20 * time.Millisecond)
	if countEvents(t, s, dinv.KindRoundSent) != 0 {
		t.Fatal("the action ran while the loop mutex was held")
	}
	cancel()
	s.deps.LoopMu.Unlock()
	if code := <-done; code != 200 {
		t.Fatalf("%d", code)
	}
	if countEvents(t, s, dinv.KindRoundSent) != 1 || len(fa.calls) != 1 {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestMuteWithBothFieldsIs400(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until":"2d","until_state":true}`); rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestMuteUntilStateChangeLastsUntilTheSituationChanges(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until_state":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	o, _ := s.store().Override(ctx, r7)
	if !o.MuteUntilStateChange || o.MutedKind != "review" || o.MutedWaitingOn != "me" {
		t.Fatalf("override %+v", o)
	}
	var d api.Detail
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	if d.Row.Kind != "muted" {
		t.Fatalf("not muted: %+v", d.Row)
	}
	s.store().AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindReview, Key: "r1", Actor: "me", At: s.deps.Now(), Data: "CHANGES_REQUESTED:h2"}})
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	if d.Row.Kind != "changes" {
		t.Fatalf("mute outlived the state change: %+v", d.Row)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until_state":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	post(t, s, "/api/units/github/acme%2Fapi%237/unmute", `{}`)
	o, _ = s.store().Override(ctx, r7)
	if o.MuteUntilStateChange || o.MutedKind != "" || o.MutedWaitingOn != "" {
		t.Fatalf("unmute left %+v", o)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%2399/mute", `{"until_state":true}`); rec.Code != 409 {
		t.Fatalf("unknown pull request: %d", rec.Code)
	}
}

func TestDelegatedActionGetsTheCallerProfileAndReportsItsOutput(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{out: "created feat/theirs\nwarning: acme/api#7 comes from a fork\n"}
	s.deps.Actions = fa
	rec := post(t, s, "/api/units/github/acme%2Fapi%237/take_over", `{"agent_home":"/home/me/.claude-personal"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(fa.profiles, []string{"/home/me/.claude-personal"}) {
		t.Fatalf("profiles %q", fa.profiles)
	}
	var res api.ActionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if want := []string{"created feat/theirs", "warning: acme/api#7 comes from a fork"}; !res.OK || !slices.Equal(res.Output, want) {
		t.Fatalf("result %+v", res)
	}
	post(t, s, "/api/units/github/acme%2Fapi%237/open", `{}`)
	if !slices.Equal(fa.profiles, []string{"/home/me/.claude-personal", ""}) {
		t.Fatalf("the cockpit's empty profile was not passed as empty: %q", fa.profiles)
	}
}

func TestQuickActionsSkipTheLoopMutex(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.deps.Actions = &fakeActions{}
	s.deps.LoopMu.Lock()
	defer s.deps.LoopMu.Unlock()
	for _, a := range []struct{ name, body string }{
		{"open", `{}`}, {"mute", `{"until":"2d"}`}, {"unmute", `{}`}, {"pause", `{}`}, {"resume", `{}`},
	} {
		done := make(chan int, 1)
		go func() { done <- post(t, s, "/api/units/github/acme%2Fapi%237/"+a.name, a.body).Code }()
		select {
		case code := <-done:
			if code != 200 {
				t.Fatalf("%s: %d", a.name, code)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s waited on the loop mutex", a.name)
		}
	}
}

func TestLoopActionsWaitOnTheSharedLoopMutex(t *testing.T) {
	t.Parallel()
	stDB, err := sqlitetest.Open(t.TempDir()+"/inv.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := inventory.New(stDB)
	t.Cleanup(func() { stDB.Close() })
	var loop sync.Mutex
	fa := &fakeActions{}
	s := New(withStore(Deps{Actions: fa, LoopMu: &loop, Log: func(string, ...any) {}}, st))
	loop.Lock()
	done := make(chan error, 1)
	go func() { done <- s.act(context.Background(), "review", r7.Unit(), api.ActionRequest{}, io.Discard) }()
	select {
	case err := <-done:
		loop.Unlock()
		t.Fatalf("review ran while the shared loop mutex was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	loop.Unlock()
	if err := <-done; err != nil || len(fa.calls) != 1 {
		t.Fatalf("%v %v", err, fa.calls)
	}
}

func TestMuteUntilStateLapsesOnce(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/mute", `{"until_state":true}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	s.store().AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindReview, Key: "r1", Actor: "me", At: s.deps.Now(), Data: "CHANGES_REQUESTED:h2"}})
	mctx, sc := metertest.Start(t, ctx)
	rows, err := s.rows(mctx, api.ViewAll)
	if err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL, Op: "exec"}: 0, {Res: meter.Tx, Op: "write"}: 0})
	i := slices.IndexFunc(rows, func(r api.Row) bool { return r.Unit == r7.Unit() })
	if i < 0 || rows[i].Kind != "changes" || payload(rows[i]).Override.MuteUntilStateChange || !slices.ContainsFunc(rows[i].Actions, func(a api.ActionState) bool { return a.ID == "mute" }) {
		t.Fatalf("the read did not show the lapsed mute's row unmuted: %+v", rows[i])
	}
	if o, _ := s.store().Override(ctx, r7); !o.MuteUntilStateChange {
		t.Fatalf("a read cleared the stored mute: %+v", o)
	}
	mctx, sc = metertest.Start(t, ctx)
	if err := s.readModel().Refresh(mctx); err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Tx, Op: "write"}); n < 1 {
		t.Fatal("the keeper wrote nothing")
	}
	if err := s.readModel().Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	o, _ := s.store().Override(ctx, r7)
	if o.MuteUntilStateChange || o.MutedKind != "" || o.MutedWaitingOn != "" {
		t.Fatalf("expired mute kept: %+v", o)
	}
	if n := countEvents(t, s, dinv.KindUnmuted); n != 1 {
		t.Fatalf("%d unmuted events, want 1", n)
	}
	pr, _, _ := s.store().PR(ctx, r7)
	pr.HeadOid = "h3"
	s.store().UpsertPR(ctx, pr)
	s.store().AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindPush, Key: "h3", Actor: "ana", At: s.deps.Now().Add(time.Minute)}})
	var d api.Detail
	get(t, s, "/api/units/github/acme%2Fapi%237", &d)
	if d.Row.Kind != "review" || d.Row.Summary.WaitsOn != "me" {
		t.Fatalf("the old situation re-armed the mute: %+v", d.Row)
	}
	if rows, _ = s.rows(ctx, api.ViewAll); rows[slices.IndexFunc(rows, func(r api.Row) bool { return r.Unit == r7.Unit() })].Kind == "muted" {
		t.Fatal("the row came back muted")
	}
}

func TestExpireMutesClearsAnExpiredMute(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	s.store().SetOverride(ctx, dinv.Override{PR: r7, MuteUntilStateChange: true, MutedKind: "changes", MutedWaitingOn: "author"})
	if err := s.expireMutes(ctx); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.store().Override(ctx, r7); o.MuteUntilStateChange {
		t.Fatalf("expired mute kept: %+v", o)
	}
}

var r9 = github.Ref{Owner: "acme", Repo: "api", Number: 9}

// holdByCap holds today's automatic round of acme/api#7 by the cap.
func holdByCap(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.store().AppendEvents(context.Background(), []dinv.Event{{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: s.deps.Now(), Data: "daily cap"}}); err != nil {
		t.Fatal(err)
	}
}

func seedWaitingOnAna(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	now := s.deps.Now()
	if err := s.store().UpsertPR(ctx, dinv.PR{Ref: r9, Title: "Cache warmup", Author: "ana", State: "OPEN", HeadOid: "h1",
		Reasons: []string{"repo"}, URL: r9.URL()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store().AppendEvents(ctx, []dinv.Event{{PR: r9, Kind: dinv.KindPush, Key: "h1", Actor: "ana", At: now.Add(-72 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
}

func TestNudgeAsksWhoItWaitsOn(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	fa := &fakeActions{}
	s.deps.Actions = fa
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{"guidance":"be brief"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(fa.calls) != 1 || !strings.HasPrefix(fa.calls[0], "nudge acme/api#9 Waiting on: @ana since ") || !strings.HasSuffix(fa.calls[0], "|be brief") || fa.maintainers[0] {
		t.Fatalf("calls %v, maintainers %v", fa.calls, fa.maintainers)
	}
	if since := s.deps.Now().Add(-72 * time.Hour).Local().Format(time.RFC3339); !strings.Contains(fa.calls[0], " since "+since+", ") {
		t.Fatalf("the wait is not dated to the second: %v, want %s", fa.calls, since)
	}
	evs, _ := s.store().Events(context.Background(), r9)
	last := evs[len(evs)-1]
	if last.Kind != dinv.KindNudged || last.Data != "@ana" {
		t.Fatalf("last event %+v", last)
	}
}

func TestNudgeAsksTheMaintainersOfMyPullRequestWithNoReviewer(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	ctx := context.Background()
	pr, _, _ := s.store().PR(ctx, r9)
	pr.Author = "me"
	if err := s.store().UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	fa := &fakeActions{}
	s.deps.Actions = fa
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(fa.calls) != 1 || !strings.HasPrefix(fa.calls[0], "nudge acme/api#9 Waiting on: the maintainers (no reviewer requested) since ") ||
		!strings.Contains(fa.calls[0], ", no reviewer.") || strings.Contains(fa.calls[0], "@") || !fa.maintainers[0] {
		t.Fatalf("calls %v, maintainers %v", fa.calls, fa.maintainers)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "already nudged the maintainers") {
		t.Fatalf("a second nudge within a day: %d %s", rec.Code, rec.Body)
	}
}

func TestNudgeRefusesWhenItWaitsOnMe(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	rec := post(t, s, "/api/units/github/acme%2Fapi%237/nudge", `{}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "waits on you") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestNudgeRefusesAClosedPullRequest(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	fa := &fakeActions{}
	s.deps.Actions = fa
	rec := post(t, s, "/api/units/github/acme%2Fapi%238/nudge", `{}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "no nudge on acme/api#8") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%2399/nudge", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "not tracked") {
		t.Fatalf("unknown pull request: %d %s", rec.Code, rec.Body)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestNudgeSituationWithoutATime(t *testing.T) {
	t.Parallel()
	if got := nudgeSituation([]string{"@ana"}, api.Row{Kind: "draft", Summary: api.Summary{Reason: "draft"}}); got != "Waiting on: @ana, draft." {
		t.Fatalf("%q", got)
	}
}

func TestNudgeSituationCarriesNoCIText(t *testing.T) {
	t.Parallel()
	r := api.Row{Kind: "checks", Summary: api.Summary{Reason: "checks failing: lint, @acme/everyone"}}
	if got := nudgeSituation([]string{"@ana"}, r); got != "Waiting on: @ana, checks failing." {
		t.Fatalf("%q", got)
	}
}

func TestNudgeRefusesABotAuthor(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	if err := s.store().UpsertPR(ctx, dinv.PR{Ref: r9, Title: "Bump x", Author: "dependabot[bot]", State: "OPEN", HeadOid: "h1", Reasons: []string{"repo"}}); err != nil {
		t.Fatal(err)
	}
	fa := &fakeActions{}
	s.deps.Actions = fa
	rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "dependabot[bot], a bot") || len(fa.calls) != 0 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, fa.calls)
	}
}

func TestNudgeOncePerWait(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	fa := &fakeActions{}
	s.deps.Actions = fa
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	start := s.deps.Now()
	s.deps.Now = func() time.Time { return start.Add(23 * time.Hour) }
	rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`)
	again := start.Add(24 * time.Hour).Local().Format("2006-01-02 15:04")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already nudged @ana at ") || !strings.Contains(rec.Body.String(), "again after "+again) || len(fa.calls) != 1 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, fa.calls)
	}
	s.deps.Now = func() time.Time { return start.Add(25 * time.Hour) }
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`); rec.Code != 200 || len(fa.calls) != 2 {
		t.Fatalf("a day later: %d %s %v", rec.Code, rec.Body, fa.calls)
	}
}

func TestNudgeRefusesWhileTheLoopWorks(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	if err := s.store().SetWorkspace(context.Background(), r9, dinv.Workspace{ID: "w", IsReview: true, AgentStates: []turn.AgentState{turn.AgentWorking}}); err != nil {
		t.Fatal(err)
	}
	fa := &fakeActions{}
	s.deps.Actions = fa
	rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "waits on the review loop") || len(fa.calls) != 0 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, fa.calls)
	}
}

func TestGuidanceIsCutAtItsMax(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	fa := &fakeActions{}
	s.deps.Actions = fa
	nudge, _ := s.loaded().Registry.Get("nudge")
	if nudge.Guidance.Max == 0 {
		t.Fatal("the nudge manifest sets no guidance.max")
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{"guidance":"`+strings.Repeat("é", nudge.Guidance.Max+100)+`"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if g := fa.calls[0][strings.Index(fa.calls[0], "|")+1:]; utf8.RuneCountInString(g) != nudge.Guidance.Max {
		t.Fatalf("guidance of %d runes", utf8.RuneCountInString(g))
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/pause", `{"guidance":"`+strings.Repeat("a", 70<<10)+`"}`); rec.Code != 400 {
		t.Fatalf("a 70 KB body: %d", rec.Code)
	}
}

func TestNudgeIgnoresAMute(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	fa := &fakeActions{}
	s.deps.Actions = fa
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/mute", `{"until":"2d"}`); rec.Code != 200 {
		t.Fatalf("mute: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{}`); rec.Code != 200 || len(fa.calls) != 1 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, fa.calls)
	}
}

func TestTrackAddsAReasonAndUntrackDropsOnlyThatOne(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	reasons := func() []string {
		pr, _, _ := s.store().PR(ctx, r7)
		return pr.Reasons
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/track", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := reasons(); !slices.Equal(got, []string{"review-requested", dinv.ReasonTracked}) {
		t.Fatalf("after track: %v", got)
	}
	post(t, s, "/api/units/github/acme%2Fapi%237/untrack", `{}`)
	if got := reasons(); !slices.Equal(got, []string{"review-requested"}) {
		t.Fatalf("after untrack: %v", got)
	}
	evs, _ := s.store().Events(ctx, r7)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Contains(kinds, dinv.KindTracked) || !slices.Contains(kinds, dinv.KindUntracked) {
		t.Fatalf("events %v", kinds)
	}
}

func TestTrackRefusesPastTheCap(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	for n := 1; n <= trackMax; n++ {
		s.store().SetOverride(ctx, dinv.Override{PR: github.Ref{Owner: "acme", Repo: "big", Number: n}, Tracked: true})
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/track", `{}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "untrack one first") || !strings.Contains(rec.Body.String(), "acme/big#50") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fbig%231/track", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no track on acme/big#1") {
		t.Fatalf("tracking again what is tracked: %d %s", rec.Code, rec.Body)
	}
}

func TestActionsUseTheInventorysName(t *testing.T) {
	t.Parallel()
	fa := &fakeActions{}
	s := newServerWith(t, func(d *Deps) { d.Actions = fa })
	s.track = func(_ context.Context, u unit.Unit) (port.TrackRead, error) {
		ref := refOf(u)
		if ref != r7 {
			t.Errorf("read %s", ref)
		}
		return port.TrackRead{Unit: r7.Unit()}, nil
	}
	ctx := context.Background()
	if rec := post(t, s, "/api/units/github/Acme%2FAPI%237/track", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/units/github/Acme%2FAPI%237/review", `{"source":"cockpit","guidance":"g"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if o, _ := s.store().Override(ctx, r7); !o.Tracked || !slices.Equal(fa.calls, []string{"review acme/api#7 g"}) {
		t.Fatalf("override %+v, calls %v", o, fa.calls)
	}
	if got, _ := s.store().Tracked(ctx); !slices.Equal(got, []github.Ref{r7}) {
		t.Fatalf("tracked %v", got)
	}
	if rec := post(t, s, "/api/units/github/ACME%2Fapi%237/track", `{}`); rec.Code != 409 {
		t.Fatalf("tracking again under another case: %d %s", rec.Code, rec.Body)
	}
	if got, _ := s.store().Tracked(ctx); !slices.Equal(got, []github.Ref{r7}) {
		t.Fatalf("tracked twice: %v", got)
	}
}

func TestTrackAnswersWhyItCouldNotRead(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err  error
		code int
		says string
	}{
		{fmt.Errorf("%w: acme/api#7: Could not resolve to a PullRequest", port.ErrNotFound), 404, "Could not resolve"},
		{fmt.Errorf("%w: acme/api#7: Resource protected by organization SAML enforcement", port.ErrGitHub), 502, "SAML"},
		{fmt.Errorf("%w: read acme/api#7: %w", port.ErrGitHub, &dgh.BudgetError{Limit: dgh.Limit{Resource: dgh.ResourceGraphQL, Remaining: 50, Limit: 5000, ResetAt: time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)}}), 429, "until"},
		{errors.New("disk I/O error"), 500, "disk"},
	} {
		s := newServer(t)
		s.track = func(context.Context, unit.Unit) (port.TrackRead, error) {
			return port.TrackRead{Unit: r7.Unit()}, c.err
		}
		rec := post(t, s, "/api/units/github/acme%2Fapi%237/track", `{}`)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.says) {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
		if got, _ := s.store().Tracked(context.Background()); len(got) != 0 {
			t.Errorf("%v: tracked unread: %v", c.err, got)
		}
	}
}

func TestTrackRefusesAFinishedPullRequest(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	s.track = func(context.Context, unit.Unit) (port.TrackRead, error) { return port.TrackRead{Unit: r8.Unit()}, nil }
	if rec := post(t, s, "/api/units/github/acme%2Fapi%238/track", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no track on acme/api#8") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestTrackAndUntrackRecordOnlyAChange(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	for range 2 {
		post(t, s, "/api/units/github/acme%2Fapi%237/track", `{}`)
	}
	post(t, s, "/api/units/github/acme%2Fapi%238/untrack", `{}`)
	post(t, s, "/api/units/github/acme%2Fapi%238/untrack", `{"track":true}`)
	count := func(ref github.Ref, kind string) int {
		evs, _ := s.store().Events(ctx, ref)
		n := 0
		for _, e := range evs {
			if e.Kind == kind {
				n++
			}
		}
		return n
	}
	if n := count(r7, dinv.KindTracked); n != 1 {
		t.Fatalf("%d tracked events", n)
	}
	if n := count(r8, dinv.KindUntracked) + count(r8, dinv.KindTracked); n != 0 {
		t.Fatalf("%d events on an untracked pull request", n)
	}
	all, _ := s.store().Overrides(ctx)
	if _, ok := all[r8]; ok {
		t.Fatalf("an empty override row: %+v", all)
	}
}

func TestOverrideWritersDoNotLoseUpdates(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	for range 20 {
		s.store().SetOverride(ctx, dinv.Override{PR: r7})
		var wg sync.WaitGroup
		for _, action := range []string{"pause", "track"} {
			wg.Go(func() { post(t, s, "/api/units/github/acme%2Fapi%237/"+action, `{}`) })
		}
		wg.Wait()
		if o, _ := s.store().Override(ctx, r7); !o.Hold || !o.Tracked {
			t.Fatalf("lost an update: %+v", o)
		}
	}
}

func TestTheTrackCapHoldsUnderConcurrentTracks(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	for n := 1; n < trackMax; n++ {
		s.store().SetOverride(ctx, dinv.Override{PR: github.Ref{Owner: "acme", Repo: "big", Number: n}, Tracked: true})
	}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, path := range []string{"/api/units/github/acme%2Fapi%237/track", "/api/units/github/acme%2Fweb%233/track"} {
		wg.Go(func() { codes[i] = post(t, s, path, `{}`).Code })
	}
	wg.Wait()
	slices.Sort(codes)
	if got, _ := s.store().Tracked(ctx); len(got) != trackMax || !slices.Equal(codes, []int{200, 400}) {
		t.Fatalf("%d tracked, codes %v", len(got), codes)
	}
}

func TestAFinishedPullRequestIsUntracked(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	for _, ref := range []github.Ref{r7, r8} {
		s.store().SetOverride(ctx, dinv.Override{PR: ref, Tracked: true, Hold: true})
	}
	s.untrackFinished(ctx)
	if got, _ := s.store().Tracked(ctx); !slices.Equal(got, []github.Ref{r7}) {
		t.Fatalf("tracked %v", got)
	}
	if o, _ := s.store().Override(ctx, r8); !o.Hold {
		t.Fatalf("untracking dropped the hold: %+v", o)
	}
	evs, _ := s.store().Events(ctx, r8)
	if len(evs) == 0 || evs[len(evs)-1].Kind != dinv.KindUntracked || evs[len(evs)-1].Data != "merged" {
		t.Fatalf("events %+v", evs)
	}
}

func TestUntrackFinishedBudget(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx, sc := metertest.Start(t, context.Background())
	for n := 1; n <= 7; n++ {
		ref := github.Ref{Owner: "acme", Repo: "big", Number: n}
		if err := s.store().UpsertPR(ctx, dinv.PR{Ref: ref, State: dgh.StateOpen, Reasons: []string{"author"}}); err != nil {
			t.Fatal(err)
		}
		s.store().SetOverride(ctx, dinv.Override{PR: ref, Tracked: true})
	}
	before := metertest.Count(sc, metertest.Limit{Res: meter.SQL})
	s.untrackFinished(ctx)
	if n := metertest.Count(sc, metertest.Limit{Res: meter.SQL}) - before; n != 2 {
		t.Fatalf("%d statements at 7 tracked, want 2\n%s", n, metertest.Report(sc.Tally()))
	}
}

func TestASlowTrackReadDoesNotBlockTheRows(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	s.store().SetOverride(ctx, dinv.Override{PR: r7, MuteUntilStateChange: true, MutedKind: "changes", MutedWaitingOn: "ana"})
	reading, release := make(chan struct{}), make(chan struct{})
	s.track = func(_ context.Context, u unit.Unit) (port.TrackRead, error) {
		ref := refOf(u)
		close(reading)
		<-release
		return port.TrackRead{Unit: ref.Unit()}, nil
	}
	done := make(chan int)
	go func() { done <- post(t, s, "/api/units/github/acme%2Fweb%233/track", `{}`).Code }()
	<-reading
	listed := make(chan int)
	go func() { listed <- get(t, s, "/api/rows", nil) }()
	select {
	case code := <-listed:
		if code != 200 {
			t.Fatalf("rows: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the rows waited on a track's GitHub read")
	}
	close(release)
	if code := <-done; code != 200 {
		t.Fatalf("track: %d", code)
	}
}

func TestANudgeThatTracksAsksWhomTheFreshReadNames(t *testing.T) {
	t.Parallel()
	fa := &fakeActions{}
	s := newServerWith(t, func(d *Deps) { d.Actions = fa })
	ctx := context.Background()
	web := github.Ref{Owner: "acme", Repo: "web", Number: 3}
	old := s.deps.Now().Add(-72 * time.Hour)
	s.track = func(context.Context, unit.Unit) (port.TrackRead, error) {
		return port.TrackRead{Unit: web.Unit(), State: dgh.StateOpen, Save: func(ctx context.Context) error {
			s.store().UpsertPR(ctx, dinv.PR{Ref: web, Title: "Other", Author: "zoe", State: "OPEN", HeadOid: "h1",
				ChecksState: "SUCCESS", ReviewRequests: []string{"bob", "renovate[bot]"}, URL: web.URL()})
			_, err := s.store().AppendEvents(ctx, []dinv.Event{
				{PR: web, Kind: dinv.KindPush, Key: "h1", Actor: "zoe", At: old},
				{PR: web, Kind: dinv.KindReviewRequest, Key: "bob", Actor: "zoe", At: old},
				{PR: web, Kind: dinv.KindReviewRequest, Key: "renovate[bot]", Actor: "zoe", At: old},
			})
			return err
		}}, nil
	}
	if rec := post(t, s, "/api/units/github/acme%2Fweb%233/track", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fweb%233/nudge", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(fa.calls) != 1 || !strings.Contains(fa.calls[0], "@bob") || strings.Contains(fa.calls[0], "renovate") {
		t.Fatalf("calls %v", fa.calls)
	}
	if o, _ := s.store().Override(ctx, web); !o.Tracked {
		t.Fatal("not tracked")
	}
}

func TestARefusedTrackStoresNothing(t *testing.T) {
	t.Parallel()
	web := github.Ref{Owner: "acme", Repo: "web", Number: 3}
	for _, c := range []struct {
		name  string
		state string
		race  bool
		says  string
	}{
		{name: "merged", state: dgh.StateMerged, says: "merged"},
		{name: "the cap filled during the read", state: dgh.StateOpen, race: true, says: "untrack one first"},
	} {
		s := newServer(t)
		ctx := context.Background()
		saved := false
		s.track = func(context.Context, unit.Unit) (port.TrackRead, error) {
			if c.race {
				for n := 1; n <= trackMax; n++ {
					s.store().SetOverride(ctx, dinv.Override{PR: github.Ref{Owner: "acme", Repo: "big", Number: n}, Tracked: true})
				}
			}
			return port.TrackRead{Unit: web.Unit(), State: c.state, Save: func(context.Context) error { saved = true; return nil }}, nil
		}
		if rec := post(t, s, "/api/units/github/acme%2Fweb%233/track", `{}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), c.says) || saved {
			t.Errorf("%s: %d %s, saved %v", c.name, rec.Code, rec.Body, saved)
		}
	}
}

func TestUntrackFindsTheTrackUnderAnyCase(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/track", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, s, "/api/units/github/ACME%2FApi%237/untrack", `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if o, _ := s.store().Override(ctx, r7); o.Tracked {
		t.Fatalf("still tracked: %+v", o)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/untrack", `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no untrack on acme/api#7") {
		t.Fatalf("untracking what is not tracked: %d %s", rec.Code, rec.Body)
	}
}

func TestUntrackingWhatOnlyTheTrackHeldTakesItOutOfAll(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	ctx := context.Background()
	web := github.Ref{Owner: "acme", Repo: "web", Number: 3}
	s.track = func(context.Context, unit.Unit) (port.TrackRead, error) {
		return port.TrackRead{Unit: web.Unit(), State: dgh.StateOpen, Save: func(ctx context.Context) error {
			return s.store().UpsertPR(ctx, dinv.PR{Ref: web, Title: "Other", Author: "zoe", State: "OPEN", URL: web.URL()})
		}}, nil
	}
	listed := func() bool {
		rows, err := s.rows(ctx, api.ViewAll)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(rows, func(r api.Row) bool { return r.Unit == web.Unit() })
	}
	if rec := post(t, s, "/api/units/github/acme%2Fweb%233/track", `{}`); rec.Code != 200 || !listed() {
		t.Fatalf("tracked: %d %s, listed %v", rec.Code, rec.Body, listed())
	}
	if rec := post(t, s, "/api/units/github/acme%2Fweb%233/untrack", `{}`); rec.Code != 200 || listed() {
		t.Fatalf("untracked: %d %s, listed %v", rec.Code, rec.Body, listed())
	}
}

type fakeLauncher struct {
	mu       sync.Mutex
	loopMu   *sync.Mutex
	launches []turns.Launch
	// loopHeld records, per launch, whether the loop mutex was held.
	loopHeld []bool
}

func (l *fakeLauncher) Launch(_ context.Context, x turns.Launch) error {
	held := !l.loopMu.TryLock()
	if !held {
		l.loopMu.Unlock()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.launches = append(l.launches, x)
	l.loopHeld = append(l.loopHeld, held)
	return nil
}

type turnRig struct {
	s        *Server
	store    *turnstest.Store
	sessions *turnstest.Sessions
	reg      *turnstest.Registry
	launcher *fakeLauncher
}

// newTurnRig is a server whose turns live in strict fakes; nothing of the
// inventory is needed by the turn actions.
func newTurnRig(t *testing.T) *turnRig {
	t.Helper()
	s := newServer(t)
	r := &turnRig{s: s, store: turnstest.NewStore(turnstest.Strict(t)), sessions: turnstest.NewSessions(),
		reg: turnstest.NewRegistry(turnstest.Strict(t)), launcher: &fakeLauncher{loopMu: s.deps.LoopMu}}
	s.deps.Turns = &turns.Service{Intents: turnstest.Intents(), Store: r.store, Registry: r.reg, Sessions: r.sessions, Launcher: r.launcher,
		Clock: fixedClock(s.deps.Now()), Command: brand.Command}
	return r
}

// asked puts an asked turn of intent on ref, its last record asking q.
func (r *turnRig) asked(ref pr.Ref, intent turn.Intent, state turn.State, q *record.Question) turn.Book {
	at := r.s.deps.Now().Add(-10 * time.Minute)
	return r.store.Put(turn.Book{Unit: ref.Unit(),
		Turns: []turn.Turn{{Intent: intent, ID: "r1", Trigger: turn.Automated, Session: "term-1", State: state, SentAt: at, StartedAt: at}},
		Records: []turn.Record{{TurnSeq: 0, Session: "term-1", Hash: "h", At: at,
			Record: record.Record{State: record.State(state), Summary: "publish?", Question: q}}}})
}

func eventsOf(evs []port.Event, kind string) []port.Event {
	var out []port.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

var publishQuestion = &record.Question{Kind: "choice", Text: "Publish?", Recommend: "publish", Options: []record.Option{
	{ID: "publish", Label: "Publish APPROVE", Consequence: "approves", Publishes: true},
	{ID: "hold", Label: "Hold", Consequence: "nothing"},
}}

func TestAnswerActionUsesTheTurn(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	r8 := github.Ref{Owner: "acme", Repo: "api", Number: 8}
	b := r.asked(pr.Ref(r8), turn.Intent(dintent.Review), turn.Asked, publishQuestion)
	rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", `{"option_id":"publish","source":"notification"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := eventsOf(r.store.Events(), turns.KindAnswered); len(got) != 1 || got[0].Data != "Publish APPROVE (option publish)" {
		t.Fatalf("answered events %+v", got)
	}
	if len(r.launcher.launches) != 1 || r.launcher.launches[0].ResumeOf != b.Turns[0].Seq || r.launcher.launches[0].Answer != "Publish APPROVE (option publish)" {
		t.Fatalf("launches %+v", r.launcher.launches)
	}
	if !r.launcher.loopHeld[0] {
		t.Fatal("the resume ran without the loop mutex")
	}
	books, _ := r.store.Books(context.Background(), pr.Ref(r8).Unit())
	answers := books[pr.Ref(r8).Unit()].Answers
	if len(answers) != 1 || answers[0].OptionID != "publish" || answers[0].Source != turn.Notification || answers[0].Route != turn.Resumed {
		t.Fatalf("answers %+v", answers)
	}
}

func TestAnswerTypesIntoAWarmCapableSession(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	now := r.s.deps.Now()
	r.asked(pr.Ref{Owner: "acme", Repo: "api", Number: 8}, turn.Intent(dintent.Review), turn.Asked, publishQuestion)
	r.reg.Put(turn.Session{ID: "term-1", WorkspaceID: "ws-1", StartedAt: now.Add(-time.Hour), MCPAt: now.Add(-time.Hour)})
	r.sessions.Put("ws-1", turns.Terminal{Session: "term-1", LastOutput: now.Add(-time.Minute)})
	rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", `{"guidance":"  publish it, but say thanks  "}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if typed := r.sessions.Typed(); len(typed) != 1 || typed[0].Line != "The user answered: publish it, but say thanks" {
		t.Fatalf("typed %+v", typed)
	}
	var res api.ActionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || !slices.Equal(res.Output, []string{"answer sent to the agent session"}) {
		t.Fatalf("%s %v", rec.Body, err)
	}
}

// busyAsked is an asked turn on r8 whose warm, capable session is working.
func (r *turnRig) busyAsked(q *record.Question) pr.Ref {
	now := r.s.deps.Now()
	r8 := pr.Ref{Owner: "acme", Repo: "api", Number: 8}
	r.asked(r8, turn.Intent(dintent.Review), turn.Asked, q)
	r.reg.Put(turn.Session{ID: "term-1", Pane: "tab-1:leaf-1", WorkspaceID: "ws-1", StartedAt: now.Add(-time.Hour), MCPAt: now.Add(-time.Hour)})
	r.sessions.Put("ws-1", turns.Terminal{Session: "term-1", Key: "tab-1", LastOutput: now.Add(-time.Minute)})
	r.sessions.SetAgents(turns.AgentObservation{WorkspaceID: "ws-1", Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentWorking})
	return r8
}

func (r *turnRig) routes(ref pr.Ref) []turn.Route {
	books, _ := r.store.Books(context.Background(), ref.Unit())
	var out []turn.Route
	for _, a := range books[ref.Unit()].Answers {
		out = append(out, a.Route)
	}
	return out
}

func TestAnAnswerToABusyAgentIsQueuedAndTypedAtItsStop(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	r8 := r.busyAsked(publishQuestion)
	rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", `{"guidance":"publish it"}`)
	var res api.ActionResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || !slices.Equal(res.Output, []string{api.AnswerQueued}) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := r.routes(r8); !slices.Equal(got, []turn.Route{turn.Queued}) || len(r.sessions.Typed()) != 0 {
		t.Fatalf("routes %v, typed %+v", got, r.sessions.Typed())
	}
	if rec := agentCall(t, r.s.Handler(), "POST", api.AgentSessionsStop, "term-1", `{"prompt_id":"p1"}`); rec.Code != 200 {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body)
	}
	r.s.driving.Wait()
	if typed := r.sessions.Typed(); len(typed) != 1 || typed[0] != (turnstest.Typed{Handle: "term-1", Line: "The user answered: publish it"}) {
		t.Fatalf("typed %+v", typed)
	}
	if got := r.routes(r8); !slices.Equal(got, []turn.Route{turn.Typed}) {
		t.Fatalf("routes %v", got)
	}
}

func TestTheFastJobDeliversAQueuedAnswerOnceTheAgentIsIdle(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	r8 := r.busyAsked(&record.Question{Kind: "confirm", Text: "Publish?"})
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", `{"no":true,"guidance":"the draft misreads the diff","source":"notification"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	r.sessions.SetAgents(turns.AgentObservation{WorkspaceID: "ws-1", Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentDone})
	r.s.deps.LoopMu.Lock()
	r.s.reconcileTurns(context.Background())
	r.s.deps.LoopMu.Unlock()
	if len(r.sessions.Typed()) != 0 {
		t.Fatalf("delivered while the loop was held: %+v", r.sessions.Typed())
	}
	r.s.reconcileTurns(context.Background())
	if typed := r.sessions.Typed(); len(typed) != 1 || typed[0].Line != "The user answered: no. the draft misreads the diff" {
		t.Fatalf("typed %+v", typed)
	}
	if got := r.routes(r8); !slices.Equal(got, []turn.Route{turn.Typed}) {
		t.Fatalf("routes %v", got)
	}
}

func TestAnswerWithoutATurnIsRefused(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	for _, body := range []string{`{"guidance":"yes"}`, `{"option_id":"publish"}`} {
		rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/answer", body)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "no open question on acme/api#7") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	r.store.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), Role: "pr.review", ID: "r1", State: turn.Done, SentAt: r.s.deps.Now()}}})
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/answer", `{"guidance":"yes"}`); rec.Code != 409 {
		t.Fatalf("a done turn: %d %s", rec.Code, rec.Body)
	}
	if len(r.sessions.Typed()) != 0 || len(r.launcher.launches) != 0 || len(eventsOf(r.store.Events(), turns.KindAnswered)) != 0 {
		t.Fatalf("typed %+v launched %+v events %+v", r.sessions.Typed(), r.launcher.launches, r.store.Events())
	}
}

func TestAnswerRefusesWhatTheQuestionDoesNotTake(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	r.asked(pr.Ref{Owner: "acme", Repo: "api", Number: 8}, turn.Intent(dintent.Review), turn.Asked, publishQuestion)
	for _, body := range []string{`{"guidance":"   "}`, `{"option_id":"merge"}`, `{"yes":true}`, `{"guidance":"x","source":"pager"}`} {
		if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", body); rec.Code != 400 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if len(r.launcher.launches) != 0 || len(eventsOf(r.store.Events(), turns.KindAnswered)) != 0 {
		t.Fatalf("a refused answer acted: %+v", r.launcher.launches)
	}
}

func TestANoToAConfirmNeedsNoText(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	r.asked(pr.Ref{Owner: "acme", Repo: "api", Number: 8}, turn.Intent(dintent.Review), turn.Asked, &record.Question{Kind: "confirm", Text: "Push?"})
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/answer", `{"source":"tui"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(r.launcher.launches) != 1 || r.launcher.launches[0].Answer != "no" {
		t.Fatalf("launches %+v", r.launcher.launches)
	}
}

func TestTurnActionsNeedNoInventoryRow(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	probe := pr.Ref{Owner: "acme", Repo: "sandbox", Number: 3}
	r.asked(probe, turn.Intent(dintent.Probe), turn.Asked, &record.Question{Kind: "free", Text: "Which file?"})
	if rec := post(t, r.s, "/api/units/github/acme%2Fsandbox%233/answer", `{"guidance":"README.md"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(r.launcher.launches) != 1 || r.launcher.launches[0].Unit != probe.Unit() || r.launcher.launches[0].Intent != turn.Intent(dintent.Probe) {
		t.Fatalf("launches %+v", r.launcher.launches)
	}
}

func TestContinueAndDismissReachTheService(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	r8 := pr.Ref{Owner: "acme", Repo: "api", Number: 8}
	b := r.asked(r8, turn.Intent(dintent.Address), turn.Failed, nil)
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/continue", `{}`); rec.Code != 200 {
		t.Fatalf("continue: %d %s", rec.Code, rec.Body)
	}
	if len(r.launcher.launches) != 1 || r.launcher.launches[0].ResumeOf != b.Turns[0].Seq || r.launcher.launches[0].Answer != "" || !r.launcher.loopHeld[0] {
		t.Fatalf("launches %+v held %v", r.launcher.launches, r.launcher.loopHeld)
	}
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/dismiss", `{}`); rec.Code != 200 {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	books, _ := r.store.Books(context.Background(), r8.Unit())
	if st := books[r8.Unit()].Turns[0].State; st != turn.Dismissed || len(eventsOf(r.store.Events(), turns.KindQuestionDismissed)) != 1 {
		t.Fatalf("state %s events %+v", st, r.store.Events())
	}
	for _, name := range []string{"continue", "dismiss"} {
		if rec := post(t, r.s, "/api/units/github/acme%2Fapi%238/"+name, `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no open question") {
			t.Fatalf("%s with nothing open: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestTurnActionsWithoutTurnsAreUnavailable(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/dismiss", `{}`); rec.Code != 503 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// aheadActions records each read ahead and whether the loop mutex was free.
type aheadActions struct {
	*fakeActions
	loop  *sync.Mutex
	ahead []string
}

func (a *aheadActions) ReadAhead(ctx context.Context, u unit.Unit, full bool) context.Context {
	ref := refOf(u)
	free := a.loop.TryLock()
	if free {
		a.loop.Unlock()
	}
	a.ahead = append(a.ahead, fmt.Sprintf("%s full=%v free=%v", ref, full, free))
	return ctx
}

func TestLoopActionsReadAheadBeforeTheLoopMutex(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	a := &aheadActions{fakeActions: &fakeActions{}, loop: s.deps.LoopMu}
	s.deps.Actions = a
	s.store().AppendEvents(context.Background(), []dinv.Event{{PR: r7, Kind: dinv.KindRoundHeld, At: s.deps.Now().Add(time.Second), Data: "daily cap"}})
	post(t, s, "/api/units/github/acme%2Fapi%237/review", `{"source":"cockpit"}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/open", `{}`)
	post(t, s, "/api/units/github/acme%2Fapi%237/run_now", `{"source":"cockpit"}`)
	want := []string{"acme/api#7 full=false free=true", "acme/api#7 full=true free=true"}
	if !slices.Equal(a.ahead, want) || len(a.calls) != 3 {
		t.Fatalf("read ahead %q, calls %v", a.ahead, a.calls)
	}
}
