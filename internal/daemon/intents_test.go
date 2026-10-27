package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	turnpkg "github.com/wloophq/wloop/internal/turn"
)

// askAuthorFixture is the profile directory of the fixture user intent.
var askAuthorFixture = filepath.Join("..", "intent", "testdata", "good", "ask-author")

// cacheOver is the registry cache over the bundled intents and dir's.
func cacheOver(t *testing.T, dir string) *intentload.Cache {
	t.Helper()
	c, err := turnpkg.NewCache(func() []profile.Layer { return []profile.Layer{{Name: "user", Dir: dir}} }, intentload.CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// copyIntents copies the intents of the profile directory src into dst.
func copyIntents(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.CopyFS(filepath.Join(dst, "intents"), os.DirFS(filepath.Join(src, "intents"))); err != nil {
		t.Fatal(err)
	}
}

func adopt(t *testing.T, s *Server, ref github.Ref) {
	t.Helper()
	if err := s.store().SetOverride(context.Background(), dinv.Override{PR: ref, Adopted: true}); err != nil {
		t.Fatal(err)
	}
}

func specOf(specs []api.ActionSpec, id string) (api.ActionSpec, bool) {
	i := slices.IndexFunc(specs, func(s api.ActionSpec) bool { return s.ID == id })
	if i < 0 {
		return api.ActionSpec{}, false
	}
	return specs[i], true
}

func rowOf(t *testing.T, s *Server, ref github.Ref) api.Row {
	t.Helper()
	row, err := s.currentRow(context.Background(), ref.Unit())
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestIntentActionsComeFromManifests(t *testing.T) {
	t.Parallel()
	dir, err := filepath.Abs(askAuthorFixture)
	if err != nil {
		t.Fatal(err)
	}
	cache := cacheOver(t, dir)
	s := newServerWith(t, func(d *Deps) { d.Intents = cache.Get })
	var got api.ActionsResponse
	if code := get(t, s, api.Actions, &got); code != 200 {
		t.Fatalf("%d", code)
	}
	spec, ok := specOf(got.Specs, "ask-author")
	want := api.ActionSpec{ID: "ask-author", Label: "Ask author", Summary: "Ask the author one question", Group: "start", Key: "Q", Surfaces: []string{"row", "detail", "tui", "cli"},
		Input: &api.InputSpec{Kind: "text", Label: "Guidance"}, Source: filepath.Join(dir, "intents", "ask-author", "intent.toml"), Scope: "unit", Plugin: "github"}
	if !ok || !reflect.DeepEqual(spec, want) {
		t.Fatalf("spec %+v, want %+v", spec, want)
	}
	if got.Version == catalog.Version(catalogOf(bundledActions())) {
		t.Fatal("the specs version ignores the user intent")
	}
	st, ok := offerOf(rowOf(t, s, r7), "ask-author")
	if !ok || !st.Enabled || st.Note != "Ask ana one question" {
		t.Fatalf("on someone else's pull request: %+v %v", st, ok)
	}
	ctx := context.Background()
	if err := s.store().UpsertPR(ctx, dinv.PR{Ref: r10, Title: "Mine", Author: "me", State: "OPEN", HeadOid: "h1", Reasons: []string{"author"}, URL: r10.URL()}); err != nil {
		t.Fatal(err)
	}
	if st, ok := offerOf(rowOf(t, s, r10), "ask-author"); ok {
		t.Fatalf("visible on the user's own pull request: %+v", st)
	}
	fa := &fakeActions{}
	s.deps.Actions = fa
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/ask-author", `{"source":"cli","guidance":"g","inputs":{"topic":"the cache"}}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(fa.calls, []string{"ask-author acme/api#7 g"}) || fa.inputs[0]["topic"] != "the cache" {
		t.Fatalf("calls %v, inputs %v", fa.calls, fa.inputs)
	}
}

// cachePort is the turns' port over a registry cache.
type cachePort struct{ get func() *intentload.Loaded }

func (p cachePort) Rules(i string) (dintent.RecordRules, error) { return p.get().Rules(i) }
func (p cachePort) Answerable() []string                        { return p.get().Registry.Answerable() }
func (p cachePort) ClosesWhenDone(i string) bool                { return p.get().Registry.ClosesWhenDone(i) }
func (p cachePort) Reconcile(i string) string                   { return p.get().Registry.Reconcile(i) }
func (p cachePort) SessionTitles() []string                     { return p.get().Registry.SessionTitles() }

func TestAProfileEditReachesTheCatalogWithoutARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache := cacheOver(t, dir)
	r := newTurnRig(t)
	r.s.deps.Intents = cache.Get
	r.s.deps.Turns.Intents = cachePort{cache.Get}
	var got api.ActionsResponse
	get(t, r.s, api.Actions, &got)
	if _, ok := specOf(got.Specs, "ask-author"); ok {
		t.Fatal("served before the profile has it")
	}
	copyIntents(t, askAuthorFixture, dir)
	get(t, r.s, api.Actions, &got)
	if _, ok := specOf(got.Specs, "ask-author"); !ok {
		t.Fatalf("the user intent added after the start is not served: %v", got.Specs)
	}
	at := r.s.deps.Now().Add(-10 * time.Minute)
	r.store.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Intent: "ask-author", ID: "ask1", Trigger: turn.ByHand, Session: "term-1", SentTo: "term-1",
		State: turn.Started, SentAt: at, StartedAt: at}}})
	r.reg.Put(turn.Session{ID: "term-1", WorkspaceID: "ws-1"})
	rec := agentCall(t, r.s.Handler(), "POST", api.AgentTurnsEnd, "term-1",
		`{"turn":"ask1","pr":"`+r7.URL()+`","record":{"state":"asked","summary":"push?","resume":"waiting on the push","question":{"kind":"confirm","text":"Push?","recommend":"no"}}}`)
	if rec.Code != 200 {
		t.Fatalf("turn end: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/answer", `{"yes":true,"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
}

func TestPrepareRunsOnBothRoutes(t *testing.T) {
	t.Parallel()
	wantInputs := func(t *testing.T, in map[string]any) {
		t.Helper()
		situation, _ := in["situation"].(string)
		who, _ := in["who"].([]string)
		maintainers, isBool := in["waits_on_maintainers"].(bool)
		if !strings.HasPrefix(situation, "Waiting on: @ana since ") || !slices.Equal(who, []string{"@ana"}) || !isBool || maintainers {
			t.Fatalf("inputs %#v", in)
		}
	}
	t.Run("action", func(t *testing.T) {
		s := newServer(t)
		seedWaitingOnAna(t, s)
		fa := &fakeActions{}
		s.deps.Actions = fa
		if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{"guidance":"g","source":"cli"}`); rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		wantInputs(t, fa.inputs[0])
	})
	t.Run("session", func(t *testing.T) {
		var mu sync.Mutex
		var asked []api.ByHandRequest
		s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), func(d *Deps) {
			d.TurnByHand = func(_ context.Context, r api.ByHandRequest, _ string) (api.ByHandResult, error) {
				mu.Lock()
				defer mu.Unlock()
				asked = append(asked, r)
				return api.ByHandResult{Text: "the nudge"}, nil
			}
			d.ByHandRef = func(context.Context, api.ByHandRequest, string) (unit.Unit, error) { return r9.Unit(), nil }
			d.SessionWait = time.Hour
		})
		seedWaitingOnAna(t, s)
		rec := httptest.NewRecorder()
		req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"nudge","cwd":"/w","guidance":"g"}`))
		req.Header.Set(api.SessionHeader, "term-1")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("a session nudge waited or failed: %d %s", rec.Code, rec.Body)
		}
		if len(asked) != 1 {
			t.Fatalf("asked %+v", asked)
		}
		wantInputs(t, asked[0].Inputs)
		if open, _ := s.deps.Intakes.Open(context.Background()); len(open) != 0 {
			t.Fatalf("a session nudge went through an intake: %+v", open)
		}
	})
}

func TestAClientCannotSendAPrepareInput(t *testing.T) {
	t.Parallel()
	s, _, _ := newByHandServer(t, intaketest.NewBriefs(intake.Readiness{Go: true}), time.Hour)
	seedWaitingOnAna(t, s)
	want := "inputs.situation is filled by " + brand.Name + ", not by the request"
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{"source":"cli","inputs":{"situation":"Waiting on: nobody."}}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("action: %d %s", rec.Code, rec.Body)
	}
	rec := httptest.NewRecorder()
	req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"nudge","cwd":"/w","inputs":{"situation":"x"}}`))
	req.Header.Set(api.SessionHeader, "term-1")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
}

func TestAnActionAskWithADifferentInputDoesNotJoin(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	adopt(t, s, r7)
	first := ask(t, s, "address", `{"source":"cli","inputs":{"self_review":true}}`)
	waitFor(t, func() bool { return intakeOf(t, s, first.Seq).State == intake.Briefing })
	want := fmt.Sprintf("intake %d already asks for this pull request's address with --self-review; cancel it with "+brand.Command+" intake cancel %d", first.Seq, first.Seq)
	rec := post(t, s, "/api/units/github/acme%2Fapi%237/address", `{"source":"cli","guidance":"other"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if open, _ := s.deps.Intakes.Open(context.Background()); len(open) != 1 || intakeOf(t, s, first.Seq).Note == intake.NoteGuidance || len(fa.calls()) != 0 {
		t.Fatalf("the refused join changed something: %+v, calls %v", open, fa.calls())
	}
	if st, _ := offerOf(rowOf(t, s, r7), "address"); st.Enabled || st.Reason != want {
		t.Fatalf("the row offers %+v", st)
	}
}

func TestAnActionAskWithTheSameInputJoins(t *testing.T) {
	t.Parallel()
	s, fa := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	adopt(t, s, r7)
	first := ask(t, s, "address", `{"source":"cli","inputs":{"self_review":true}}`)
	waitFor(t, func() bool { return intakeOf(t, s, first.Seq).State == intake.Briefing })
	again := ask(t, s, "address", `{"source":"cli","inputs":{"self_review":true}}`)
	if again.Seq != first.Seq || !again.Joined {
		t.Fatalf("intake %+v, first %d", again, first.Seq)
	}
	if open, _ := s.deps.Intakes.Open(context.Background()); len(open) != 1 || len(fa.calls()) != 0 {
		t.Fatalf("open %+v, calls %v", open, fa.calls())
	}
}

func TestAPendingRunNowIgnoresAnOpenIntake(t *testing.T) {
	t.Parallel()
	s, _ := newIntakeServer(t, intaketest.NewBriefs(waitBriefing), nil)
	plan := newPendingPlan(s.deps.Now())
	s.deps.Plan, s.deps.Pending = plan, plan
	in := ask(t, s, "review", `{"source":"cockpit"}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Briefing })
	plan.propose(r7.Unit())
	if st, _ := offerOf(rowOf(t, s, r7), "run_now"); !st.Enabled {
		t.Fatalf("run_now %+v", st)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/run_now", `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if plan.isPending(r7.Unit()) || !slices.Equal(plan.acted, []workflow.Action{workflow.ActionApply}) {
		t.Fatalf("the pending round was not sent: %v", plan.acted)
	}
}

func TestASessionAskWithADifferentInputDoesNotJoin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, hand := newByHandServer(t, intaketest.NewBriefs(waitBriefing), 50*time.Millisecond)
		byHand := func(body string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(body))
			req.Header.Set(api.SessionHeader, "term-1")
			s.Handler().ServeHTTP(rec, req)
			return rec
		}
		if rec := byHand(`{"intent":"address","cwd":"/w"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "not ready yet") {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		open, _ := s.deps.Intakes.Open(context.Background())
		if len(open) != 1 {
			t.Fatalf("open %+v", open)
		}
		rec := byHand(`{"intent":"address","cwd":"/w","inputs":{"self_review":true}}`)
		want := fmt.Sprintf("intake %d already asks for this pull request's address without --self-review", open[0].Seq)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if again, _ := s.deps.Intakes.Open(context.Background()); len(again) != 1 || len(hand.list()) != 0 {
			t.Fatalf("open %+v, calls %v", again, hand.list())
		}
	})
}

func TestASweepTurnWithoutTheInputFailsTheIntake(t *testing.T) {
	t.Parallel()
	books := turnstest.NewStore()
	mu := &sync.Mutex{}
	s, fa := newIntakeServer(t, nil, func(d *Deps) {
		d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: books}
		d.LoopMu = mu
	})
	adopt(t, s, r7)
	mu.Lock()
	in := ask(t, s, "address", `{"source":"cli","inputs":{"self_review":true}}`)
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Starting })
	books.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Address), Role: "pr.address", ID: "by-sweep", Trigger: turn.Automated, State: turn.Sent, SentAt: time.Now().Add(time.Second)}}})
	mu.Unlock()
	waitFor(t, func() bool { return intakeOf(t, s, in.Seq).State == intake.Failed })
	got := intakeOf(t, s, in.Seq)
	if !strings.Contains(got.Error, "the sweep sent the address of acme/api#7 (round by-sweep) while this request waited without --self-review") || len(fa.calls()) != 0 {
		t.Fatalf("intake %+v, calls %v", got, fa.calls())
	}
}

func TestInputsAreValidatedAgainstTheDefinition(t *testing.T) {
	t.Parallel()
	dir, _ := filepath.Abs(askAuthorFixture)
	s := newServerWith(t, func(d *Deps) { d.Intents = cacheOver(t, dir).Get; d.Actions = &fakeActions{} })
	adopt(t, s, r7)
	for _, c := range []struct{ action, body, want string }{
		{"review", `{"source":"cli","inputs":{"nope":true}}`, "review takes no input nope"},
		{"address", `{"source":"cockpit","inputs":{"take_over":true}}`, "--take-over is not accepted from cockpit"},
		{"address", `{"source":"cli","inputs":{"take_over":"yes"}}`, "--take-over takes true or false"},
		{"ask-author", `{"source":"cli","inputs":{"topic":"` + strings.Repeat("é", 201) + `"}}`, "--topic takes at most 200 characters"},
		{"pause", `{"inputs":{"take_over":true}}`, "pause takes no inputs"},
	} {
		if rec := post(t, s, "/api/units/github/acme%2Fapi%237/"+c.action, c.body); rec.Code != 400 || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s, want 400 %q", c.action, c.body, rec.Code, rec.Body, c.want)
		}
	}
	reg := (&Server{}).loaded().Registry
	address, _ := reg.Get(dintent.Address)
	if _, err := CheckInputs(reg, address, map[string]any{"self_review": true}, catalog.SurfaceCLI, sourceCLI, true); err == nil || err.Error() != "--self-review is for rounds asked by hand only" {
		t.Errorf("by_hand_only on an automated path: %v", err)
	}
}

func TestByHandRefusesAFlagOfAnotherIntent(t *testing.T) {
	t.Parallel()
	s, _, hand := newByHandServer(t, intaketest.NewBriefs(intake.Readiness{Go: true}), time.Hour)
	rec := httptest.NewRecorder()
	req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"review","cwd":"/w","inputs":{"self_review":true}}`))
	req.Header.Set(api.SessionHeader, "term-1")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "--self-review applies to address") || len(hand.list()) != 0 {
		t.Fatalf("%d %s, calls %v", rec.Code, rec.Body, hand.list())
	}
}

func TestByHandRefusesAnIntentThatIsNotUserFacing(t *testing.T) {
	t.Parallel()
	s, _, hand := newByHandServer(t, intaketest.NewBriefs(intake.Readiness{Go: true}), time.Hour)
	rec := httptest.NewRecorder()
	req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"probe","cwd":"/w","url":"https://github.com/acme/api/pull/7"}`))
	req.Header.Set(api.SessionHeader, "term-1")
	s.Handler().ServeHTTP(rec, req)
	var body struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	want := fmt.Sprintf("unknown intent %q; use %s", "probe", s.loaded().Registry.Names())
	if rec.Code != 400 || body.Error != want || len(hand.list()) != 0 {
		t.Fatalf("%d %s, calls %v", rec.Code, rec.Body, hand.list())
	}
}

// brokenAskAuthor is a cache whose ask-author manifest, at the path it
// returns, has an unknown key.
func brokenAskAuthor(t *testing.T) (*intentload.Cache, string) {
	t.Helper()
	dir := t.TempDir()
	copyIntents(t, askAuthorFixture, dir)
	manifest := filepath.Join(dir, "intents", "ask-author", "intent.toml")
	raw, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, append(raw, []byte("\nunknown_key = 1\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	return cacheOver(t, dir), manifest
}

func TestTurnStartRefusesABrokenIntentWithItsProblems(t *testing.T) {
	t.Parallel()
	cache, _ := brokenAskAuthor(t)
	s, _, _ := newByHandServer(t, intaketest.NewBriefs(intake.Readiness{Go: true}), time.Hour)
	s.deps.Intents = cache.Get
	rec := agentCall(t, s.Handler(), "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"ask-author"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "the intent ask-author has problems") || !strings.Contains(rec.Body.String(), "unknown_key") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestHeldRunNowPicksTheIntentByTheWorkspaceRule(t *testing.T) {
	t.Parallel()
	reg := (&Server{}).loaded().Registry
	for _, c := range []struct {
		name string
		pr   prrow.Payload
		mine bool
		want string
	}{
		{"review workspace", prrow.Payload{Workspace: api.Workspace{ID: "w", IsReview: true}}, true, "review"},
		{"author workspace", prrow.Payload{Workspace: api.Workspace{ID: "w"}}, false, "address"},
		{"mine, no workspace", prrow.Payload{}, true, "address"},
		{"taken over", prrow.Payload{Override: api.Override{Adopted: true}}, false, "address"},
		{"someone else's, no workspace", prrow.Payload{}, false, "review"},
		{"someone else's main checkout", prrow.Payload{Workspace: api.Workspace{ID: "w", IsMain: true}}, false, "review"},
	} {
		if got, err := catalog.RunNowIntent(reg, c.pr.Workspace, c.pr.Override, c.mine); err != nil || got != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, got, err, c.want)
		}
	}
	s, _ := newIntakeServer(t, nil, nil)
	holdByCap(t, s)
	if err := s.store().SetWorkspace(context.Background(), r7, dinv.Workspace{ID: "w7", DisplayName: "feat-batch"}); err != nil {
		t.Fatal(err)
	}
	if in := ask(t, s, "run_now", `{"source":"cockpit"}`); in.Intent != "address" {
		t.Fatalf("intake %+v", in)
	}
}

func TestAnInvalidUserIntentDisablesOnlyItself(t *testing.T) {
	t.Parallel()
	cache, manifest := brokenAskAuthor(t)
	l := cache.Get()
	if _, ok := l.Broken["ask-author"]; !ok {
		t.Fatalf("the fixture loads: %v", l.Problems)
	}
	first := catalog.FirstProblem(l.Broken["ask-author"])
	s, _, _ := newByHandServer(t, intaketest.NewBriefs(intake.Readiness{Go: true}), time.Hour)
	s.deps.Intents = cache.Get
	var specs api.ActionsResponse
	get(t, s, api.Actions, &specs)
	spec, ok := specOf(specs.Specs, "ask-author")
	if !ok || spec.Label != "ask-author" || spec.Source != manifest || !slices.Equal(spec.Surfaces, []string{"row", "detail", "tui", "cli", "explore"}) || spec.Input != nil {
		t.Fatalf("spec %+v", spec)
	}
	row := rowOf(t, s, r7)
	if st, _ := offerOf(row, "ask-author"); st.Enabled || st.Reason != "the profile has errors: "+first {
		t.Fatalf("state %+v, first problem %q", st, first)
	}
	for _, id := range []string{"review", "pause", "open"} {
		if st, ok := offerOf(row, id); !ok || !st.Enabled {
			t.Errorf("%s: %+v", id, st)
		}
	}
	var h api.Health
	get(t, s, "/api/health", &h)
	if !slices.Contains(h.Warnings, "profile: "+first) {
		t.Fatalf("warnings %q", h.Warnings)
	}
	if rec := post(t, s, "/api/units/github/acme%2Fapi%237/ask-author", `{"source":"cli"}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "the profile has errors") {
		t.Fatalf("action: %d %s", rec.Code, rec.Body)
	}
	rec := httptest.NewRecorder()
	req := apiRequest("POST", api.AgentTurnsByHand, strings.NewReader(`{"intent":"ask-author","cwd":"/w"}`))
	req.Header.Set(api.SessionHeader, "term-1")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "the intent ask-author has problems") {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
}

func TestProbeIsExcludedFromReportsByItsManifest(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 7}
	d := dinv.TimingsData{Turns: []dinv.TimedTurn{
		{Seq: 1, PR: ref, Intent: "review", Trigger: "automated", State: "done", SentAt: at, StartedAt: at, EndedAt: at.Add(time.Minute)},
		{Seq: 2, PR: ref, Intent: "probe", Trigger: "automated", State: "done", SentAt: at, StartedAt: at, EndedAt: at.Add(time.Minute)},
	}}
	rep := timings(d, at.Add(-time.Hour), at.Add(time.Hour), me{login: "me"}, reportsOf((&Server{}).loaded().Registry))
	if len(rep.TurnList) != 1 || rep.TurnList[0].Intent != "review" {
		t.Fatalf("turns %+v", rep.TurnList)
	}
}

func TestNudgeRecordsOnlyDoneOrFailed(t *testing.T) {
	t.Parallel()
	s, svc := agentServer(t)
	svc.Intents = s.loaded().Registry
	sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Nudge), "nudge1nudge1")
	h := s.Handler()
	if rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"nudge1nudge1","intent":"nudge"}`); rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	rec := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"nudge1nudge1","pr":"`+agentRef.URL()+`","record":{"state":"asked","summary":"who?","resume":"r","question":{"kind":"confirm","text":"Ask again?","recommend":"no"}}}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `state: done or failed, not \"asked\"`) {
		t.Fatalf("an asked nudge: %d %s", rec.Code, rec.Body)
	}
	rec = agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"nudge1nudge1","pr":"`+agentRef.URL()+`","record":{"state":"done","summary":"nudged","outcome":{"url":"https://github.com/acme/api/pull/7#issuecomment-1"}}}`)
	if rec.Code != 200 {
		t.Fatalf("a done nudge: %d %s", rec.Code, rec.Body)
	}
}

func TestActionRouteRunsTodaysEffects(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		intent    string
		ref       github.Ref
		dropped   bool
		event     string
		eventData string
	}{
		{"review", r7, true, dinv.KindRoundSent, askedByYou},
		{"address", r7, true, dinv.KindRoundSent, askedByYou},
		{"nudge", r9, false, "", ""},
	} {
		t.Run(c.intent, func(t *testing.T) {
			t.Parallel()
			rig := newCatalogRig(t)
			rig.s.intakes = nil
			fa := &fakeActions{}
			rig.s.deps.Actions = fa
			seedWaitingOnAna(t, rig.s)
			adopt(t, rig.s, r7)
			rig.plan.propose(c.ref.Unit())
			path := api.UnitPath(pr.Ref{Owner: "acme", Repo: "api", Number: c.ref.Number}.Unit(), c.intent)
			if rec := post(t, rig.s, path, `{"source":"cli"}`); rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if pending := rig.plan.isPending(c.ref.Unit()); pending == c.dropped {
				t.Errorf("pending round after the %s: %v", c.intent, pending)
			}
			evs, _ := rig.st.Events(context.Background(), c.ref)
			if c.event != "" && !slices.ContainsFunc(evs, func(e dinv.Event) bool { return e.Kind == c.event && e.Data == c.eventData }) {
				t.Errorf("no %s %q: %+v", c.event, c.eventData, evs)
			}
			if c.event == "" && slices.ContainsFunc(evs, func(e dinv.Event) bool { return e.Kind == dinv.KindNudged }) {
				t.Errorf("the daemon recorded a nudge the command line records with its turn: %+v", evs)
			}
			if len(fa.calls) != 1 {
				t.Errorf("calls %v", fa.calls)
			}
		})
	}
}

func TestTheRowsNewestTurnIsADrivesRowIntents(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t)
	at := r.s.deps.Now().Add(-time.Hour)
	r.store.Put(turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{
		{Intent: turn.Intent(dintent.Review), Role: "pr.review", ID: "a", Trigger: turn.Automated, State: turn.Done, SentAt: at},
		{Intent: turn.Intent(dintent.Nudge), ID: "b", Trigger: turn.ByHand, State: turn.Sent, SentAt: at.Add(time.Minute)},
	}})
	bundled := r.s.loaded().Registry
	facts, err := r.s.turnFacts(context.Background(), bundled)
	if err != nil {
		t.Fatal(err)
	}
	if got := facts[r7].Facts.Intent; got != "review" {
		t.Fatalf("the row follows the %s turn", got)
	}
	var defs []dintent.Def
	for _, d := range bundled.All() {
		d.DrivesRow = d.Name == dintent.Nudge
		defs = append(defs, d)
	}
	if facts, _ = r.s.turnFacts(context.Background(), dintent.NewRegistry(defs)); facts[r7].Facts.Intent != "nudge" {
		t.Fatalf("with nudge driving the row, it follows the %s turn", facts[r7].Facts.Intent)
	}
}

func TestASessionTurnThatWaitsForNoBriefTakesNoIntake(t *testing.T) {
	t.Parallel()
	b := (&Server{}).loaded()
	var defs []dintent.Def
	for _, d := range b.Registry.All() {
		if d.Name == dintent.Review {
			d.Brief.Wait = false
		}
		defs = append(defs, d)
	}
	loaded := &intentload.Loaded{Registry: dintent.NewRegistry(defs), Templates: b.Templates, Programs: b.Programs, Broken: map[dintent.Name]intentload.Problems{}}
	s, _, hand := newByHandServer(t, intaketest.NewBriefs(waitBriefing), time.Hour)
	s.deps.Intents = func() *intentload.Loaded { return loaded }
	if rec := byHandAs(t, s, "term-1", "review"); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if open, _ := s.deps.Intakes.Open(context.Background()); len(open) != 0 || !slices.Equal(hand.list(), []string{"review term-1"}) {
		t.Fatalf("open %+v, calls %v", open, hand.list())
	}
}
