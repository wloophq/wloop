package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/github/present"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
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
	intentload "github.com/wloophq/wloop/internal/intent"
)

var catalogNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

// knownOnGitHub reads the pull requests it holds, and any other as an open
// one of zoe's.
type knownOnGitHub map[github.Ref]dgh.Candidate

func (k knownOnGitHub) SearchCandidates(context.Context, string, string, int) (dgh.CandidatePage, error) {
	return dgh.CandidatePage{}, nil
}

func (knownOnGitHub) SearchCandidatesEach(_ context.Context, qs []string, _ string, _ int) ([]dgh.CandidatePage, []error, error) {
	return make([]dgh.CandidatePage, len(qs)), make([]error, len(qs)), nil
}

func (k knownOnGitHub) CandidateByRef(_ context.Context, ref github.Ref, _ string) (dgh.CandidatePage, error) {
	c, ok := k[ref]
	if !ok {
		c = dgh.Candidate{Ref: ref, URL: ref.URL(), Title: "Untracked", Author: "zoe", State: dgh.StateOpen}
	}
	return dgh.CandidatePage{Candidates: []dgh.Candidate{c}}, nil
}

func (knownOnGitHub) IsUser(context.Context, string) (bool, error) { return true, nil }

type noLaunch struct{}

func (noLaunch) Launch(context.Context, turns.Launch) error { return nil }

type catalogRig struct {
	s    *Server
	db   *sqlite.DB
	st   *inventory.Store
	repo *sqlite.Store
	plan *pendingPlan
}

var (
	webRef  = github.Ref{Owner: "acme", Repo: "web", Number: 3}
	mineRef = github.Ref{Owner: "acme", Repo: "web", Number: 4}
	r10     = github.Ref{Owner: "acme", Repo: "api", Number: 10}
)

// newCatalogRig is a daemon with every part an action reads: the plan,
// turns, intakes that wait for their brief, briefs and GitHub.
func newCatalogRig(t *testing.T) catalogRig {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := inventory.New(db)
	st.UseClock(func() time.Time { return catalogNow })
	ctx := context.Background()
	st.UpsertPR(ctx, dinv.PR{Ref: r7, Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h2", ReviewRequests: []string{"me"},
		Reasons: []string{"review-requested"}, URL: r7.URL()})
	st.AppendEvents(ctx, []dinv.Event{
		{PR: r7, Kind: dinv.KindPush, Key: "h2", Actor: "ana", At: catalogNow.Add(-50 * time.Hour)},
		{PR: r7, Kind: dinv.KindReviewRequest, Key: "me", Actor: "ana", At: catalogNow.Add(-49 * time.Hour)},
	})
	st.UpsertPR(ctx, dinv.PR{Ref: github.Ref{Owner: "acme", Repo: "api", Number: 8}, Title: "Done", Author: "me", State: "MERGED", Reasons: []string{"author"}})
	repo := sqlite.New(db)
	sessions := turnstest.NewSessions()
	plan := newPendingPlan(catalogNow)
	ts := &turns.Service{Store: repo, Registry: repo, Sessions: sessions, Intents: turnstest.Intents(), Launcher: noLaunch{}, Clock: clockFunc(func() time.Time { return catalogNow })}
	inbox := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: db.Tasks(), Items: itemsNone{}, Facts: enabledUnits{map[unit.ProviderID]bool{}}, Clock: fixedClock(catalogNow)})
	d := withStore(Deps{Pauses: sqlite.NewRounds(db), Activity: db, Cfg: config.Defaults(), Version: "test", Now: func() time.Time { return catalogNow },
		Identity: viewerIs("me"),
		Log:      func(string, ...any) {},
		Actions:  &fakeActions{}, Intakes: db.Intakes(), IntakeBriefs: intaketest.NewBriefs(waitBriefing),
		Turns:    ts,
		Messages: &messages.Service{Store: inventory.NewMessages(db), Books: repo, Turns: ts, Tasks: inbox, Intents: turnstest.Intents(), Clock: fixedClock(catalogNow)},
		Sessions: sessions,
		StateDir: t.TempDir(),
	}, st)
	githubOf(&d).Explorer = knownOnGitHub{mineRef: {Ref: mineRef, URL: mineRef.URL(), Title: "Mine", Author: "me", State: dgh.StateOpen}}
	withBriefs(&d, stubBriefs{})
	d.Plan, d.Pending = plan, plan
	s := New(d)
	s.token = ""
	stopOnCleanup(t, s)
	return catalogRig{s: s, db: db, st: st, repo: repo, plan: plan}
}

func (c catalogRig) propose(t *testing.T) {
	t.Helper()
	c.plan.propose(r7.Unit())
}

func (c catalogRig) events(t *testing.T, evs ...dinv.Event) {
	t.Helper()
	if _, err := c.st.AppendEvents(context.Background(), evs); err != nil {
		t.Fatal(err)
	}
}

func (c catalogRig) override(t *testing.T, o dinv.Override) {
	t.Helper()
	if err := c.st.SetOverride(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

// asks stores a review turn of r7 in state whose last record is rec.
func (c catalogRig) asks(t *testing.T, state turn.State, rec record.Record) {
	t.Helper()
	at := catalogNow.Add(-30 * time.Minute)
	b := turn.Book{Unit: pr.Ref(r7).Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), Role: "pr.review", ID: "r7", Trigger: turn.Automated, Head: "h2", Session: "term",
		State: state, SentAt: at, StartedAt: at}},
		Records: []turn.Record{{Session: "term", Hash: "x", At: at, Record: rec}}}
	if _, err := c.repo.Save(context.Background(), b, port.Event{Unit: pr.Ref(r7).Unit(), Kind: turns.KindTurnSent, Key: "r", At: at}); err != nil {
		t.Fatal(err)
	}
}

func (c catalogRig) intake(t *testing.T, source string) {
	t.Helper()
	payload, _ := json.Marshal(actionPayload{Action: "review", ActionRequest: api.ActionRequest{Source: source}})
	if _, _, err := c.s.intakes.Accept(context.Background(), intake.Request{Unit: r7.Unit(), Intent: turn.Intent(dintent.Review), Source: source, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

type catalogFixture struct {
	name string
	ref  github.Ref
	seed func(t *testing.T, c catalogRig)
}

// catalogFixtures are a row in each situation the catalog offers actions in,
// plus an untracked pull request and an explore candidate.
var catalogFixtures = []catalogFixture{
	{"pending", r7, func(t *testing.T, c catalogRig) { c.propose(t) }},
	{"pending_muted", r7, func(t *testing.T, c catalogRig) {
		c.override(t, dinv.Override{PR: r7, MutedUntil: catalogNow.Add(time.Hour)})
		c.propose(t)
	}},
	{"held", r7, func(t *testing.T, c catalogRig) {
		c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: catalogNow, Data: "daily cap"})
	}},
	{"blocked", r7, func(t *testing.T, c catalogRig) {
		c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundBlocked, Key: "b", At: catalogNow, Data: "no workspace"})
	}},
	{"blocked_untrusted", r7, func(t *testing.T, c catalogRig) {
		b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/src/api", Home: "/home/.claude-work", Homes: []string{"/home/.claude-work"}, Round: "triage", Fingerprint: "f1"}
		c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundBlocked, Key: b.Key(catalogNow), At: catalogNow, Data: "could not start: /src/api is not trusted in /home/.claude-work"})
	}},
	{"blocked_untrusted_terminal_home", r7, func(t *testing.T, c catalogRig) {
		b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/src/api/wt", Homes: []string{"/home/.claude", "/home/orca/profiles/p1/home"}, Round: "follow-up"}
		c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundBlocked, Key: b.Key(catalogNow), At: catalogNow, Data: "could not start: /src/api/wt is not trusted in the agent home of its terminal"})
	}},
	{"blocked_untrusted_lifted", r7, func(t *testing.T, c catalogRig) {
		b := inventory.StartBlock{Question: turns.QuestionTrust, Root: "/src/api", Folder: "/src/api", Round: "triage"}
		c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundBlocked, Key: b.Key(catalogNow.Add(-time.Hour)), At: catalogNow.Add(-time.Hour), Data: "could not start"},
			dinv.Event{PR: r7, Kind: dinv.KindRoundUnblocked, At: catalogNow, Data: "dismissed by you"})
	}},
	{"asked_confirm", r7, func(t *testing.T, c catalogRig) {
		c.asks(t, turn.Asked, record.Record{State: record.Asked, Summary: "push?", Question: &record.Question{Kind: "confirm", Text: "Push?", Recommend: "no"}})
	}},
	{"asked_choice", r7, func(t *testing.T, c catalogRig) { c.asks(t, turn.Asked, draftQuestion("publish?")) }},
	{"stopped", r7, func(t *testing.T, c catalogRig) {
		c.asks(t, turn.Unrecorded, record.Record{State: record.UnrecordedState, Summary: "stopped"})
	}},
	{"done_muted", github.Ref{Owner: "acme", Repo: "api", Number: 8}, func(t *testing.T, c catalogRig) {
		c.override(t, dinv.Override{PR: github.Ref{Owner: "acme", Repo: "api", Number: 8}, MutedUntil: catalogNow.Add(time.Hour)})
	}},
	{"done_with_workspace", github.Ref{Owner: "acme", Repo: "api", Number: 8}, func(t *testing.T, c catalogRig) {
		c.st.SetWorkspace(context.Background(), github.Ref{Owner: "acme", Repo: "api", Number: 8}, dinv.Workspace{ID: "w8", DisplayName: "feat-done"})
	}},
	{"mine_no_reviewer", r10, func(t *testing.T, c catalogRig) {
		c.st.UpsertPR(context.Background(), dinv.PR{Ref: r10, Title: "Mine", Author: "me", State: "OPEN", HeadOid: "h1", Reasons: []string{"author"}, URL: r10.URL()})
		c.events(t, dinv.Event{PR: r10, Kind: dinv.KindPush, Key: "h1", Actor: "me", At: catalogNow.Add(-72 * time.Hour)})
	}},
	{"others_adopted", r7, func(t *testing.T, c catalogRig) { c.override(t, dinv.Override{PR: r7, Adopted: true}) }},
	{"others_not_adopted", r7, func(*testing.T, catalogRig) {}},
	{"nudged_recently", r9, func(t *testing.T, c catalogRig) {
		seedWaitingOnAna(t, c.s)
		c.events(t, dinv.Event{PR: r9, Kind: dinv.KindNudged, Key: "n", At: catalogNow.Add(-time.Hour), Data: "@ana"})
	}},
	{"tracked", r7, func(t *testing.T, c catalogRig) { c.override(t, dinv.Override{PR: r7, Tracked: true}) }},
	{"intake_waiting_cli", r7, func(t *testing.T, c catalogRig) { c.intake(t, sourceCLI) }},
	{"intake_waiting_session", r7, func(t *testing.T, c catalogRig) { c.intake(t, sourceSession) }},
	{"forceable_refusal", r7, func(t *testing.T, c catalogRig) {
		c.st.SetWorkspace(context.Background(), r7, dinv.Workspace{ID: "w7", DisplayName: "REVIEW batch", IsReview: true})
		c.s.forceable.Store(forceableKey(r7.Unit()), "the workspace holds uncommitted files")
	}},
	{"user_hold", r7, func(t *testing.T, c catalogRig) { c.override(t, dinv.Override{PR: r7, Hold: true}) }},
	{"gate_off", r7, func(_ *testing.T, c catalogRig) { c.s.deps.Plan, c.s.deps.Pending = nil, nil }},
	{"brief_off", r7, func(_ *testing.T, c catalogRig) { withBriefs(&c.s.deps, nil) }},
	{"run_now_join_refused", r7, func(t *testing.T, c catalogRig) {
		c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: catalogNow, Data: "daily cap"})
		c.intake(t, sourceCLI)
	}},
	{"untracked_open_not_mine", webRef, func(*testing.T, catalogRig) {}},
	{"explore_candidate_mine", mineRef, func(*testing.T, catalogRig) {}},
}

func seededRow(t *testing.T, f catalogFixture) (catalogRig, api.Row) {
	t.Helper()
	c := newCatalogRig(t)
	f.seed(t, c)
	row, err := c.s.currentRow(context.Background(), f.ref.Unit())
	if err != nil {
		t.Fatalf("%s: %v", f.name, err)
	}
	return c, row
}

// localStamp is a time a refusal words in the daemon's zone, which a golden
// cannot hold.
var localStamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

func goldenJSON(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = localStamp.ReplaceAll(append(got, '\n'), []byte("<local time>"))
	path := filepath.Join("testdata", "wire", name)
	if *updateWire {
		brandtest.CanUpdate(t)
		if err := os.WriteFile(path, []byte(brandtest.Collapse(string(got))), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(brandtest.Expand(string(want)))) {
		t.Errorf("%s changed:\n%s", name, got)
	}
}

func TestActionsGolden(t *testing.T) {
	t.Parallel()
	specs := catalogOf(bundledActions())
	goldenJSON(t, "specs.json", api.ActionsResponse{Version: catalog.Version(specs), Specs: specs, Plan: catalog.PlanSpecs(bundledActions())})
	rows := map[string]api.Row{}
	for _, f := range catalogFixtures {
		_, rows[f.name] = seededRow(t, f)
	}
	goldenJSON(t, "actions.json", rows)
}

func TestSelfReviewIsNeverACatalogFlag(t *testing.T) {
	t.Parallel()
	address, _ := (&Server{}).loaded().Registry.Get(dintent.Address)
	if !slices.ContainsFunc(address.Inputs, func(in dintent.Input) bool { return in.Name == "self_review" }) {
		t.Fatal("the bundled address declares no self_review")
	}
	for _, name := range []string{"specs.json", "actions.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "wire", name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("self_review")) || bytes.Contains(raw, []byte("self-review")) {
			t.Errorf("%s offers the self-review", name)
		}
	}
}

func TestTheServedSpecsAreTheGolden(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	var got api.ActionsResponse
	if code := get(t, c.s, api.Actions, &got); code != 200 {
		t.Fatalf("%d", code)
	}
	var want api.ActionsResponse
	raw, err := os.ReadFile(filepath.Join("testdata", "wire", "specs.json"))
	if err != nil || json.Unmarshal([]byte(brandtest.Expand(string(raw))), &want) != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || len(got.Specs) != len(want.Specs) || len(got.Plan) != len(want.Plan) {
		t.Fatalf("served %s with %d specs and %d plan specs, golden %s with %d and %d", got.Version, len(got.Specs), len(got.Plan), want.Version, len(want.Specs), len(want.Plan))
	}
	var rows []api.Row
	get(t, c.s, "/api/rows?view=all", &rows)
	for _, r := range rows {
		if r.SpecsVersion != got.Version {
			t.Errorf("%v follows specs %q, served %q", r.Unit, r.SpecsVersion, got.Version)
		}
	}
}

// requestFor is a body every action takes as valid input, with the basis
// the client saw.
func requestFor(id string, st api.ActionState) string {
	body := map[string]any{"source": sourceCockpit, "guidance": "go"}
	if id == "mute" {
		body["until"] = "2d"
	}
	if st.Basis != "" {
		body["basis"] = st.Basis
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func TestEveryOfferAgreesWithTheHandler(t *testing.T) {
	t.Parallel()
	for _, f := range catalogFixtures {
		for _, spec := range catalogOf(bundledActions()) {
			if spec.Scope == catalog.ScopeDraft {
				continue
			}
			t.Run(f.name+"/"+spec.ID, func(t *testing.T) {
				t.Parallel()
				c, row := seededRow(t, f)
				st, offered := offerOf(row, spec.ID)
				path := api.UnitPath(f.ref.Unit(), spec.ID)
				rec := post(t, c.s, path, requestFor(spec.ID, st))
				var body struct{ Error string }
				json.Unmarshal(rec.Body.Bytes(), &body)
				_, isTurn := turnActions[spec.ID]
				switch {
				case isTurn && (!offered || !st.Enabled):
					if rec.Code != 409 || body.Error != st.Reason {
						t.Errorf("offered %+v (%v), the turn service answered %d %q", st, offered, rec.Code, body.Error)
					}
				case !offered:
					if want := fmt.Sprintf("no %s on %s", spec.ID, f.ref); rec.Code != 409 || body.Error != want {
						t.Errorf("absent, answered %d %q, want 409 %q", rec.Code, body.Error, want)
					}
				case !st.Enabled:
					if rec.Code != 409 || body.Error != st.Reason {
						t.Errorf("disabled with %q, answered %d %q", st.Reason, rec.Code, body.Error)
					}
				case rec.Code == 409:
					t.Errorf("enabled, answered 409 %q", body.Error)
				}
			})
		}
	}
}

func TestAReviewOfAnUntrackedPullRequestIsAccepted(t *testing.T) {
	t.Parallel()
	fa := &fakeActions{}
	s := newServerWith(t, func(d *Deps) { d.Actions = fa })
	if rec := post(t, s, "/api/units/github/acme%2Fweb%233/review", `{"source":"cli","guidance":"g"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(fa.calls, []string{"review acme/web#3 g"}) {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestAnUntrackedPullRequestWithoutAnExplorerIs503(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) { d.Actions, githubOf(d).Explorer = &fakeActions{}, nil })
	rec := post(t, s, "/api/units/github/acme%2Fweb%233/review", `{"source":"cli"}`)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "cannot read acme/web#3 from GitHub") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestEverySpecHasADispatchAndBack(t *testing.T) {
	t.Parallel()
	ids := map[string]bool{}
	for _, spec := range catalogOf(bundledActions()) {
		ids[spec.ID] = true
		if spec.Scope == catalog.ScopeDraft {
			if !slices.Contains(api.DraftActions, strings.TrimPrefix(spec.ID, "draft.")) {
				t.Errorf("%s has no draft route", spec.ID)
			}
			continue
		}
		plugin := slices.Contains([]string{present.ActionMute, present.ActionUnmute}, spec.ID)
		if !isBuiltin(spec.ID) && !isIntent((&Server{}).loaded(), spec.ID) && !plugin {
			t.Errorf("%s has no dispatch", spec.ID)
		}
	}
	dispatched := slices.Concat(slices.Collect(maps.Keys(delegatedActions)), slices.Collect(maps.Keys(holdActions)), slices.Collect(maps.Keys(followActions)),
		slices.Collect(maps.Keys(roundActions)), slices.Collect(maps.Keys(turnActions)), slices.Collect(maps.Keys(intakeRouteActions)), []string{"brief_refresh"})
	for _, id := range dispatched {
		if !ids[id] {
			t.Errorf("%s is dispatched with no spec", id)
		}
	}
}

func TestAttentionActionsAreCatalogIds(t *testing.T) {
	t.Parallel()
	ids := map[string]bool{}
	for _, s := range catalogOf(bundledActions()) {
		ids[s.ID] = true
	}
	seen := 0
	for _, f := range catalogFixtures {
		c, row := seededRow(t, f)
		att := rowAttention(t, c.s, row)
		if att == nil {
			continue
		}
		for _, a := range att.Actions {
			seen++
			if !ids[a.ID] && !strings.HasPrefix(a.ID, "answer:") {
				t.Errorf("%s: %s is no catalog id", f.name, a.ID)
			}
			if st, ok := offerOf(row, strings.Split(a.ID, ":")[0]); !ok || !st.Enabled {
				t.Errorf("%s: the notification offers %s, the row %+v", f.name, a.ID, st)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no fixture notifies")
	}
}

func TestRunNowWithAStaleBasisIs409(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	c.propose(t)
	row, _ := c.s.currentRow(context.Background(), r7.Unit())
	st, _ := offerOf(row, "run_now")
	if err := c.plan.Drop(context.Background(), r7.Unit(), "skipped"); err != nil {
		t.Fatal(err)
	}
	rec := post(t, c.s, "/api/units/github/acme%2Fapi%237/run_now", fmt.Sprintf(`{"source":"cockpit","basis":%q}`, st.Basis))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "the row changed: the pending round already went out") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRunNowNeverFallsFromPendingToHeld(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	fa := &fakeActions{}
	c.s.deps.Actions = fa
	c.propose(t)
	row, _ := c.s.currentRow(context.Background(), r7.Unit())
	st, _ := offerOf(row, "run_now")
	if err := c.plan.Drop(context.Background(), r7.Unit(), "skipped"); err != nil {
		t.Fatal(err)
	}
	c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: catalogNow.Add(time.Second), Data: "daily cap"})
	rec := post(t, c.s, "/api/units/github/acme%2Fapi%237/run_now", fmt.Sprintf(`{"source":"cockpit","basis":%q}`, st.Basis))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "the row changed") || len(fa.calls) != 0 {
		t.Fatalf("%d %s, calls %v", rec.Code, rec.Body, fa.calls)
	}
	if open, _ := c.s.deps.Intakes.Open(context.Background()); len(open) != 0 {
		t.Fatalf("a held run was accepted: %+v", open)
	}
}

func TestSkipWithNothingPendingIsRefused(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	rec := post(t, c.s, "/api/units/github/acme%2Fapi%237/skip", `{}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "no skip on acme/api#7") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestIntakeActionsNeedASource(t *testing.T) {
	t.Parallel()
	c := newCatalogRig(t)
	c.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: catalogNow, Data: "daily cap"})
	c.override(t, dinv.Override{PR: r7, Adopted: true})
	for _, spec := range catalogOf(bundledActions()) {
		if !spec.Intake {
			continue
		}
		id := spec.ID
		if rec := post(t, c.s, "/api/units/github/acme%2Fapi%237/"+id, `{}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "is not one of cli, cockpit, menubar, tui") {
			t.Errorf("%s: %d %s", id, rec.Code, rec.Body)
		}
	}
}

// bundledActions are the bundled intents alone.
func bundledActions() *intentload.Loaded { return (&Server{}).loaded() }

func catalogOf(l *intentload.Loaded) []api.ActionSpec { return catalog.Specs(l) }
