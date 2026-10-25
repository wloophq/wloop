package cli

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func planItem(wf string, u unit.Unit, value, decision, reason string) api.PlanItem {
	return api.PlanItem{ID: wf + "/" + string(u.Provider) + "/" + u.Key + "/" + value, Workflow: wf, Decision: decision, Reason: reason,
		Mode: "apply", Unit: u, Steps: []string{}, Actions: []api.ActionState{}}
}

// planFixture is a plan of every decision: rounds of the review workflows
// beside a session and a workspace item.
func planFixture() []api.PlanItem {
	followup := planItem("review-follow-up", unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}, "1f2e3d4c5b6a7980", "pending", "new commits since your review")
	followup.Default, followup.Deadline, followup.Steps = "run", testNow.Add(4*time.Minute+10*time.Second), []string{"turn.start"}
	followup.Label, followup.Title = "acme/api#12", "Retry webhooks with backoff"
	manual := planItem("session-manual-quiet", unit.Unit{Provider: unit.Session, Key: "term_3"}, "aa00bb11cc22dd33", "pending", "!session.managed, session.agent_state = done, session.idle_for = 30h0m0s")
	manual.Default, manual.Steps, manual.Gain = "wait", []string{"session.close"}, api.Gain{MemoryBytes: 300 << 20}
	temp := planItem("workspace-temporary", unit.Unit{Provider: unit.Workspace, Key: "wt-9"}, "0099887766554433", "pending", "workspace.temporary = true")
	temp.Default, temp.Mode, temp.Steps, temp.Gain = "wait", "observe", []string{"workspace.remove"}, api.Gain{DiskBytes: 2 << 30}
	capped := planItem("address", unit.Unit{Provider: unit.GitHub, Key: "acme/api#3"}, "3c3c3c3c3c3c3c3c", "blocked", "per-PR daily cap reached (3)")
	capped.Steps, capped.Attempts, capped.Error = []string{"turn.start"}, 1, "launching: the app\nwas closed"
	first := planItem("review-first-round", unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, "7a7a7a7a7a7a7a7a", "run", "my review was requested")
	first.Steps = []string{"turn.start"}
	paused := planItem("address", unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}, "8b8b8b8b8b8b8b8b", "skipped", "automatic rounds paused")
	return []api.PlanItem{first, capped, followup, paused, manual, temp}
}

// planDaemon serves the plan API from items and keeps every query and
// action it was asked.
type planDaemon struct {
	mu      sync.Mutex
	queries []string
	acts    []api.PlanActRequest
	closed  []api.ClosedSession
}

func servePlan(t *testing.T, te *testEnv, items []api.PlanItem, errs []string) *planDaemon {
	t.Helper()
	d := &planDaemon{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(api.Health{}) })
	mux.HandleFunc("GET "+api.Plan, func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.queries = append(d.queries, r.URL.RawQuery)
		res := api.PlanResponse{Items: items, Errors: append([]string{}, errs...)}
		if q := r.URL.Query(); q.Get("trigger") == "" && q.Get("closed") == "1" {
			res.Closed = d.closed
		}
		d.mu.Unlock()
		json.NewEncoder(w).Encode(res)
	})
	mux.HandleFunc("POST "+api.Plan, func(w http.ResponseWriter, r *http.Request) {
		var req api.PlanActRequest
		json.NewDecoder(r.Body).Decode(&req)
		d.mu.Lock()
		d.acts = append(d.acts, req)
		d.mu.Unlock()
		res := api.PlanResponse{Items: []api.PlanItem{}, Errors: []string{}}
		for _, id := range req.IDs {
			i := slices.IndexFunc(items, func(it api.PlanItem) bool { return it.ID == id })
			if items[i].Decision == "blocked" {
				res.Errors = append(res.Errors, id+": "+items[i].Reason)
				continue
			}
			it := items[i]
			it.Decision, it.Reason = "run", "applied by you"
			res.Items = append(res.Items, it)
		}
		json.NewEncoder(w).Encode(res)
	})
	te.Socket = serveSocket(t, mux)
	return d
}

func TestPlanGroupsByDecision(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	d := servePlan(t, te, planFixture(), nil)
	if code := te.run("plan"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	golden(t, "plan.golden", te.out.String())
	if !slices.Equal(d.queries, []string{"trigger=sweep", "closed=1"}) {
		t.Fatalf("queries %q", d.queries)
	}

}

func TestPlanFiltersAndReadsTheStoredPlan(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	d := servePlan(t, te, nil, nil)
	if code := te.run("plan", "--workflow", "address", "--provider", "github", "--stored"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if te.out.String() != "nothing planned\n" || !slices.Equal(d.queries, []string{"provider=github&workflow=address"}) {
		t.Fatalf("queries %q, printed %q", d.queries, te.out)
	}
}

func TestPlanListsTheSessionsClosedInTheLastDay(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	d := servePlan(t, te, nil, nil)
	d.closed = []api.ClosedSession{
		{Session: "term_2", At: testNow.Add(-90 * time.Minute), Reason: "gone from the manager"},
		{Session: "term_1", At: testNow.Add(-26 * time.Minute), Reason: "its pull request merged", Resume: "cd /wt/a && claude --resume b"},
	}
	for _, args := range [][]string{{"plan"}, {"plan", "--stored"}} {
		te.out.Reset()
		if code := te.run(args...); code != 0 {
			t.Fatalf("%q: exit %d: %s", args, code, te.err)
		}
		want := "nothing planned\n" +
			"closed in the last day (2)\n" +
			"  term_2  1h 30m ago: gone from the manager\n" +
			"  term_1  26m ago: its pull request merged\n" +
			"          resume: cd /wt/a && claude --resume b\n"
		if te.out.String() != want {
			t.Errorf("%q printed\n%s\nwant\n%s", args, te.out, want)
		}
	}
	if want := []string{"trigger=sweep", "closed=1", "closed=1"}; !slices.Equal(d.queries, want) {
		t.Errorf("queries %q, want %q", d.queries, want)
	}
	te.out.Reset()
	if code := te.run("plan", "--workflow", "address"); code != 0 || strings.Contains(te.out.String(), "closed in the last day") {
		t.Errorf("a filtered plan: exit %d, printed %s", code, te.out)
	}
}

func TestPlanPrintsItsErrorsAndFails(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	servePlan(t, te, planFixture()[:1], []string{"address: github:acme/api#5: boom"})
	if code := te.run("plan"); code == 0 || !strings.Contains(te.err.String(), "error: address: github:acme/api#5: boom") || !strings.Contains(te.out.String(), "review-first-round") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestPlanApplyAndDismiss(t *testing.T) {
	t.Parallel()
	items := planFixture()
	te := newTestEnv(t, &fake.Manager{})
	d := servePlan(t, te, items, nil)
	if code := te.run("plan", "apply", "1f2e3d4c", items[4].ID); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !slices.Equal(d.queries, []string{""}) || len(d.acts) != 1 || d.acts[0].Action != "apply" || !slices.Equal(d.acts[0].IDs, []string{items[2].ID, items[4].ID}) {
		t.Fatalf("queries %q, acts %+v", d.queries, d.acts)
	}
	if want := "1f2e3d4c  review-follow-up  acme/api#12: run applied by you\naa00bb11  session-manual-quiet  session:term_3: run applied by you\n"; te.out.String() != want {
		t.Fatalf("printed %q", te.out)
	}

	te.out.Reset()
	if code := te.run("plan", "dismiss", "00998877"); code != 0 || d.acts[1].Action != "dismiss" || !slices.Equal(d.acts[1].IDs, []string{items[5].ID}) {
		t.Fatalf("exit %d: %+v %s", code, d.acts, te.err)
	}

	te.err.Reset()
	if code := te.run("plan", "apply", "3c3c3c3c"); code == 0 || !strings.Contains(te.err.String(), "per-PR daily cap reached (3)") || !strings.Contains(te.err.String(), "1 of 1 item(s) not applied") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, args := range [][]string{{"apply", "ffffffff"}, {"apply", "1f2e3d4"}, {"dismiss", "1f2e"}, {"apply"}} {
		te.err.Reset()
		before := len(d.acts)
		if code := te.run(append([]string{"plan"}, args...)...); code == 0 || len(d.acts) != before {
			t.Fatalf("%q: exit %d, acts %+v", args, code, d.acts)
		}
	}
	if !strings.Contains(te.err.String(), "usage: ") {
		t.Fatalf("stderr %s", te.err)
	}
}

func TestPlanApplyRefusesAShortAmbiguousOrUnknownID(t *testing.T) {
	t.Parallel()
	a := planItem("session-duplicate", unit.Unit{Provider: unit.Session, Key: "term_1"}, "abcd1234000000000", "pending", "")
	b := planItem("session-pr-finished", unit.Unit{Provider: unit.Session, Key: "term_1"}, "abcd1234111111111", "pending", "")
	c := planItem("session-pr-finished", unit.Unit{Provider: unit.Session, Key: "term_2"}, "abcd9999000000000", "pending", "")
	te := newTestEnv(t, &fake.Manager{})
	d := servePlan(t, te, []api.PlanItem{a, b, c}, nil)
	for _, tc := range []struct{ id, want string }{
		{"abcd", "abcd is shorter than the 8 characters plan prints"},
		{"abcd999", "shorter than the 8 characters"},
		{"abcd1234", "abcd1234 names 2 items: " + a.ID + ", " + b.ID},
		{"abcd0000", "no item abcd0000 in the daemon's plan"},
	} {
		te.err.Reset()
		if code := te.run("plan", "apply", tc.id); code == 0 || len(d.acts) != 0 || !strings.Contains(te.err.String(), tc.want) {
			t.Fatalf("%s: exit %d: %s", tc.id, code, te.err)
		}
	}
	if code := te.run("plan", "dismiss", "abcd9999"); code != 0 || len(d.acts) != 1 || d.acts[0].IDs[0] != c.ID {
		t.Fatalf("exit %d: %+v %s", code, d.acts, te.err)
	}
}

func TestPlanApplyAndDismissTakeAPullRequestsPendingItem(t *testing.T) {
	t.Parallel()
	items := planFixture()
	second := planItem("nudge-silent", unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}, "2e2e2e2e2e2e2e2e", "pending", "silent for 30 days")
	te := newTestEnv(t, &fake.Manager{})
	d := servePlan(t, te, items, nil)
	if code := te.run("plan", "apply", "https://github.com/ACME/api/pull/12"); code != 0 || len(d.acts) != 1 || !slices.Equal(d.acts[0].IDs, []string{items[2].ID}) {
		t.Fatalf("exit %d: %+v %s", code, d.acts, te.err)
	}
	for url, want := range map[string]string{
		"https://github.com/acme/api/pull/7": "acme/api#7 has no pending item; '" + brand.Command + " plan --unit https://github.com/acme/api/pull/7' says why",
		"https://github.com/acme/api/pull/3": "acme/api#3 has no pending item",
	} {
		te.err.Reset()
		if code := te.run("plan", "dismiss", url); code == 0 || len(d.acts) != 1 || !strings.Contains(te.err.String(), want) {
			t.Fatalf("%s: exit %d: %s", url, code, te.err)
		}
	}

	te = newTestEnv(t, &fake.Manager{})
	d = servePlan(t, te, append(items, second), nil)
	if code := te.run("plan", "dismiss", "https://github.com/acme/api/pull/12"); code == 0 || len(d.acts) != 0 ||
		!strings.Contains(te.err.String(), "acme/api#12 has 2 pending items: 1f2e3d4c (review-follow-up), 2e2e2e2e (nudge-silent); give the id of one") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestPlanOfAUnitSaysWhyEachWorkflowGivesNoItem(t *testing.T) {
	t.Parallel()
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	followup := planItem("review-follow-up", u, "1f2e3d4c5b6a7980", "pending", "new commits since your review")
	followup.Default, followup.Deadline, followup.Steps = "run", testNow.Add(4*time.Minute), []string{"turn.start"}
	followup.Label, followup.Title = "acme/api#7", "Batch export"
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(api.Health{}) })
	mux.HandleFunc("GET "+api.Plan, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("unit"))
		json.NewEncoder(w).Encode(api.PlanResponse{Items: []api.PlanItem{followup}, Errors: []string{}, Unit: "github:acme/api#7", Label: "acme/api#7", Title: "Batch export", Workflows: []api.UnitWorkflow{
			{Workflow: "after-merge", Trigger: "event", Outcome: "waits", Reason: "decides when a merged or closed event arrives"},
			{Workflow: "review-first-round", Trigger: "sweep", Outcome: "not matched",
				Facts: []api.Fact{{Name: "pr.first.due", Value: "false"}, {Name: "pr.first.reason", Value: "the author is a bot"}}},
		}})
	})
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveSocket(t, mux)
	for _, arg := range []string{"https://github.com/acme/api/pull/7", "acme/api#7", "github:acme/api#7"} {
		te.out.Reset()
		if code := te.run("plan", "--unit", arg); code != 0 {
			t.Fatalf("%s: exit %d: %s", arg, code, te.err)
		}
	}
	if !slices.Equal(queries, []string{"github:acme/api#7", "github:acme/api#7", "github:acme/api#7"}) {
		t.Fatalf("queries %q", queries)
	}
	want := "acme/api#7  Batch export\n" +
		"pending (1)\n" +
		"  1f2e3d4c  review-follow-up  acme/api#7: new commits since your review\n" +
		"            Batch export · runs in 4m0s · steps turn.start\n" +
		"no item (2)\n" +
		"  after-merge  event, waits: decides when a merged or closed event arrives\n" +
		"  review-first-round  sweep, not matched\n" +
		"            pr.first.due = false · pr.first.reason = the author is a bot\n"
	if te.out.String() != want {
		t.Fatalf("printed\n%s\nwant\n%s", te.out, want)
	}
	for _, args := range [][]string{{"plan", "--unit", "acme/api"}, {"plan", "--unit", "acme/api#7", "--stored"}} {
		if code := te.run(args...); code == 0 {
			t.Fatalf("%q: exit 0", args)
		}
	}
}

func TestPlanPrintsNoColonForAnItemWithoutReason(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	writePlan(&b, []api.PlanItem{planItem("session-pr-finished", unit.Unit{Provider: unit.Session, Key: "term_1"}, "0011223344556677", "run", "")}, testNow)
	if want := "run (1)\n  00112233  session-pr-finished  session:term_1\n"; b.String() != want {
		t.Fatalf("%q, want %q", b.String(), want)
	}
}

func TestPlanNamesAnItemByItsRowsLabelAndTitle(t *testing.T) {
	var b strings.Builder
	thread := planItem("slack-follow-up", unit.Unit{Provider: "slack", Key: "T1/C1/171.1"}, "ee00ff11ee00ff11", "run", "")
	thread.Label, thread.Title = "#general", "Launch plan for the export"
	writePlan(&b, []api.PlanItem{thread}, testNow)
	if want := "run (1)\n  ee00ff11  slack-follow-up  #general\n            Launch plan for the export\n"; b.String() != want {
		t.Fatalf("%q, want %q", b.String(), want)
	}
}

// TestPlanOfAUnitTakesAnyProviderOrAnItemID names a Slack thread by
// provider:key, then by the ids of its item: the short one plan printed,
// found in the daemon's plan with no sweep, the whole one, and one only a
// sweep plans.
func TestPlanOfAUnitTakesAnyProviderOrAnItemID(t *testing.T) {
	th := unit.Unit{Provider: "slack", Key: "T1/C1/171.1"}
	thread := planItem("slack-follow-up", th, "ee00ff11ee00ff11", "pending", "asked you a question")
	thread.Label, thread.Title = "#general", "Launch plan for the export"
	later := planItem("slack-follow-up", th, "aa11bb22aa11bb22", "pending", "asked you a question")
	later.Label, later.Title = thread.Label, thread.Title
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(api.Health{}) })
	mux.HandleFunc("GET "+api.Plan, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		switch {
		case r.URL.Query().Get("unit") != "":
			json.NewEncoder(w).Encode(api.PlanResponse{Items: []api.PlanItem{thread}, Errors: []string{}, Unit: th.String(), Label: "#general", Title: "Launch plan for the export"})
		case r.URL.Query().Get("trigger") == "sweep":
			json.NewEncoder(w).Encode(api.PlanResponse{Items: []api.PlanItem{thread, later}, Errors: []string{}})
		default:
			json.NewEncoder(w).Encode(api.PlanResponse{Items: []api.PlanItem{thread}, Errors: []string{}})
		}
	})
	te := newTestEnv(t, &fake.Manager{})
	te.Socket = serveSocket(t, mux)
	want := "#general  Launch plan for the export  (slack:T1/C1/171.1)\n" +
		"pending (1)\n" +
		"  ee00ff11  slack-follow-up  #general: asked you a question\n" +
		"            Launch plan for the export · waits for you\n"
	asked := "unit=slack%3AT1%2FC1%2F171.1"
	for arg, sent := range map[string][]string{
		"slack:T1/C1/171.1": {asked},
		"ee00ff11":          {"", asked},
		thread.ID:           {"", asked},
		"aa11bb22":          {"", "trigger=sweep", asked},
	} {
		te.out.Reset()
		queries = nil
		if code := te.run("plan", "--unit", arg); code != 0 {
			t.Fatalf("%s: exit %d: %s", arg, code, te.err)
		}
		if te.out.String() != want || !slices.Equal(queries, sent) {
			t.Fatalf("%s: queries %q, printed\n%s\nwant\n%s", arg, queries, te.out, want)
		}
	}
	for arg, says := range map[string]string{"ffffffff": "no item ffffffff", "term_3": "term_3 names no unit"} {
		te.err.Reset()
		queries = nil
		if code := te.run("plan", "--unit", arg); code == 0 || !strings.Contains(te.err.String(), says) {
			t.Fatalf("%s: exit %d: %s", arg, code, te.err)
		}
		if arg == "term_3" && len(queries) != 0 {
			t.Fatalf("a word that is no id asked the daemon %q", queries)
		}
	}
}

func TestThePlanHeaderComparesTheLabelWithTheExplainedUnit(t *testing.T) {
	for _, c := range []struct {
		res  api.PlanResponse
		want string
	}{
		{api.PlanResponse{Unit: "github:acme/api#7", Label: "acme/api#7", Title: "Batch export"}, "acme/api#7  Batch export"},
		{api.PlanResponse{Unit: "session:term_1", Label: "session:term_1"}, "session:term_1"},
		{api.PlanResponse{Unit: "slack:T1/C1/171.1", Label: "#general\x1b[31m\n", Title: "Launch\nplan"}, "#general [31m  Launch plan  (slack:T1/C1/171.1)"},
		{api.PlanResponse{Unit: "github:acme/api#7"}, "github:acme/api#7"},
	} {
		if got := unitHeader(c.res); got != c.want {
			t.Errorf("unitHeader(%+v) = %q, want %q", c.res, got, c.want)
		}
	}
}

func TestPlanCleansARowsLabel(t *testing.T) {
	var b strings.Builder
	it := planItem("slack-follow-up", unit.Unit{Provider: "slack", Key: "T1/C1/171.1"}, "ee00ff11ee00ff11", "run", "")
	it.Label = "DM with ana\npending (9)"
	writePlan(&b, []api.PlanItem{it}, testNow)
	if want := "run (1)\n  ee00ff11  slack-follow-up  DM with ana pending (9)\n"; b.String() != want {
		t.Fatalf("%q, want %q", b.String(), want)
	}
}
