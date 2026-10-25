package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
)

// enginePlan is an engine as the daemon's Plan.
type enginePlan struct{ *automation.Engine }

func (p enginePlan) Items(ctx context.Context, f automation.Filter) ([]workflow.Item, error) {
	return p.Ledger.Items(ctx, f)
}

// planPending are the engine's pending turn.start items as the rows'
// pending rounds.
type planPending struct{ e *automation.Engine }

func (p planPending) Pending(ctx context.Context) (map[unit.Unit]round.Pending, error) {
	items, err := p.e.Pending(ctx)
	out := map[unit.Unit]round.Pending{}
	for _, it := range items {
		if slices.ContainsFunc(it.Steps, func(st workflow.Step) bool { return st.Use == builtin.StepTurnStart }) {
			out[it.Unit] = round.Pending{Intent: "follow-up", Reason: it.Reason, Since: it.At, Deadline: it.Deadline}
		}
	}
	return out, err
}

// storeGuidance is the guidance the user keeps for a pull request's next
// round, in the inventory's override.
type storeGuidance struct{ st *inventory.Store }

func (g storeGuidance) Peek(ctx context.Context, u unit.Unit) (string, error) {
	o, err := g.st.Override(ctx, refOf(u))
	return o.NextGuidance, err
}

func (g storeGuidance) Take(ctx context.Context, u unit.Unit) (string, error) {
	return g.st.TakeNextGuidance(ctx, refOf(u))
}

func (g storeGuidance) Keep(ctx context.Context, u unit.Unit, guidance string) error {
	return g.st.KeepNextGuidance(ctx, refOf(u), guidance)
}

// planRig is a daemon whose plan is an engine over its own store: #7 is
// matched by an apply workflow whose turn.start waits five minutes, and a
// session by an observe workflow.
type planRig struct {
	s     *Server
	db    *sqlite.DB
	log   *change.Log
	units *automationtest.Units
	steps *automationtest.Steps
	clock *automationtest.Clock
	eng   *automation.Engine
	sess  unit.Unit
}

func newPlanRig(t *testing.T, opts ...func(*Deps)) *planRig {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &planRig{db: db, log: log, units: automationtest.NewUnits(),
		steps: automationtest.NewSteps(map[string]workflow.Reach{builtin.StepTurnStart: workflow.ReachVisible, "session.close": workflow.ReachLocal}),
		sess:  unit.Unit{Provider: unit.Session, Key: "term_1"}}
	r.units.Put(u7, workflow.Facts{"pr.repo": "acme/api", "pr.fingerprint": "h2"})
	r.units.Put(r.sess, workflow.Facts{"session.repo": "acme/api", "session.fingerprint": "s1"})
	r.s = newServerOn(t, db, log, func(d *Deps) {
		r.clock = automationtest.NewClock(d.Now())
		d.Now = r.clock.Now
		r.eng = r.engine(d)
		d.Plan, d.Sweep, d.Pending = enginePlan{r.eng}, r.eng, planPending{r.eng}
		for _, o := range opts {
			o(d)
		}
	})
	return r
}

// engine is an engine over the rig's store as a daemon restart builds it.
func (r *planRig) engine(d *Deps) *automation.Engine {
	return &automation.Engine{
		Workflows: func() []workflow.Workflow {
			return []workflow.Workflow{
				{Name: "review-follow-up", Provider: unit.GitHub, Slot: "pr", Mode: workflow.ModeApply, AllowVisible: true,
					Trigger:  workflow.Trigger{Kind: workflow.TriggerSweep},
					Steps:    []workflow.Step{r.steps.Step(builtin.StepTurnStart, "intent", "review", "round", "follow-up")},
					Approval: workflow.Approval{Default: workflow.OutcomeRun, Window: 5 * time.Minute}},
				{Name: "session-quiet", Provider: unit.Session, Slot: "session", Mode: workflow.ModeObserve,
					Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{r.steps.Step("session.close")},
					Approval: workflow.Approval{Default: workflow.OutcomeRun}},
			}
		},
		Units: r.units, Steps: r.steps, Ledger: r.db.Ledger(), Events: r.db.Events(), Guidance: storeGuidance{inventory.New(r.db)},
		Changes: r.log, Clock: r.clock, Codecs: unit.NewRegistry(pr.Codec{}),
		Pause: func(ctx context.Context) (automation.Pause, error) {
			p, err := d.Pauses.Pause(ctx)
			return automation.Pause{On: p.On, Repos: p.Repos}, err
		},
	}
}

func (r *planRig) sweep(t *testing.T) {
	t.Helper()
	if _, _, err := r.eng.Sweep(context.Background(), &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
}

func (r *planRig) items(t *testing.T, u unit.Unit, ds ...workflow.Decision) []workflow.Item {
	t.Helper()
	items, err := r.eng.Ledger.Items(context.Background(), automation.Filter{Unit: &u, Decisions: ds})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func (r *planRig) pending7(t *testing.T) workflow.Item {
	t.Helper()
	items := r.items(t, u7, workflow.DecisionPending)
	if len(items) != 1 {
		t.Fatalf("pending items of #7: %+v", items)
	}
	return items[0]
}

func (r *planRig) plan(t *testing.T, query string) api.PlanResponse {
	t.Helper()
	var res api.PlanResponse
	if code := get(t, r.s, api.Plan+query, &res); code != http.StatusOK {
		t.Fatalf("GET %s: %d", api.Plan+query, code)
	}
	return res
}

func (r *planRig) act(t *testing.T, req api.PlanActRequest) api.PlanResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	rec := post(t, r.s, api.Plan, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s: %d %s", api.Plan, rec.Code, rec.Body)
	}
	var res api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// marked says whether the item's key has a run: skip and dismiss mark it,
// a drop does not.
func (r *planRig) marked(t *testing.T, it workflow.Item) (workflow.Run, bool) {
	t.Helper()
	run, ok, err := r.eng.Ledger.Run(context.Background(), it.Key)
	if err != nil {
		t.Fatal(err)
	}
	return run, ok
}

// checksum hashes every row of every table of db.
func checksum(t *testing.T, db *sqlite.DB) string {
	t.Helper()
	ctx := context.Background()
	rows, err := db.R().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	h := sha256.New()
	for _, table := range tables {
		rs, err := db.R().QueryContext(ctx, `SELECT * FROM "`+table+`" ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rs.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%s %v\n", table, vals)
		}
		rs.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestPlanningThroughTheAPIChangesNoStoreRow(t *testing.T) {
	r := newPlanRig(t)
	briefs := &countingBriefs{}
	withBriefs(&r.s.deps, briefs)
	r.sweep(t)
	r.pending7(t)
	if err := r.s.store().SetNextGuidance(context.Background(), r7, "look at the retry"); err != nil {
		t.Fatal(err)
	}
	r8 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#9"}
	r.units.Put(r8, workflow.Facts{"pr.repo": "acme/api", "pr.fingerprint": "no-brief"})
	r.steps.Strict(t)
	sum, gen := checksum(t, r.db), r.log.Gen()

	planned := r.plan(t, "?trigger=sweep")
	stored := r.plan(t, "")

	if got := checksum(t, r.db); got != sum {
		t.Fatal("planning through the API changed a store row")
	}
	if got := r.log.Gen(); got != gen {
		t.Fatalf("planning through the API noted the change log: %d to %d", gen, got)
	}
	if briefs.runs != 0 || len(briefs.touched) != 0 {
		t.Fatalf("planning started %d brief runs, touched %v", briefs.runs, briefs.touched)
	}
	if len(r.steps.Ran()) != 0 || len(r.steps.Noted()) != 0 {
		t.Fatalf("ran %v, noted %v", r.steps.Ran(), r.steps.Noted())
	}
	guided := slices.IndexFunc(planned.Items, func(it api.PlanItem) bool { return it.Unit == u7 })
	if guided < 0 || planned.Items[guided].Decision != "run" || planned.Items[guided].Reason != "guided by you" {
		t.Fatalf("evaluated %+v", planned.Items)
	}
	if !slices.ContainsFunc(planned.Items, func(it api.PlanItem) bool { return it.Unit == r8 }) {
		t.Fatalf("the pull request without a brief is not planned: %+v", planned.Items)
	}
	if i := slices.IndexFunc(stored.Items, func(it api.PlanItem) bool { return it.Unit == u7 }); i < 0 || stored.Items[i].Decision != "pending" {
		t.Fatalf("stored %+v", stored.Items)
	}
	if o, _ := r.s.store().Override(context.Background(), r7); o.NextGuidance != "look at the retry" {
		t.Fatalf("planning took the stored guidance: %q", o.NextGuidance)
	}
}

func TestAVersion1ClientWorks(t *testing.T) {
	r := newPlanRig(t)
	for _, path := range []string{api.Plan, api.Rows} {
		req := apiRequest(http.MethodGet, path, nil)
		req.Header.Set(api.Header, "1")
		rec := httptest.NewRecorder()
		r.s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("a version 1 client on %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if api.Version != "1" {
		t.Fatalf("api %s", api.Version)
	}
}

func TestThePlanFiltersByWorkflowAndProvider(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	for _, q := range []string{"?trigger=sweep&", "?"} {
		res := r.plan(t, q+"provider=session")
		if len(res.Items) != 1 || res.Items[0].Unit != r.sess || res.Items[0].Workflow != "session-quiet" || res.Items[0].Decision != "pending" {
			t.Fatalf("%s: %+v", q, res.Items)
		}
		if res := r.plan(t, q+"workflow=review-follow-up"); len(res.Items) != 1 || res.Items[0].Unit != u7 {
			t.Fatalf("%s: %+v", q, res.Items)
		}
	}
	if rec := httptest.NewRecorder(); true {
		r.s.Handler().ServeHTTP(rec, apiRequest(http.MethodGet, api.Plan+"?trigger=cron", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("an unknown trigger: %d", rec.Code)
		}
	}
}

func TestSkipOnAPendingRoundIsTerminal(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	it := r.pending7(t)
	rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/skip", `{}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "skipped the pending review-follow-up") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if run, ok := r.marked(t, it); !ok || run.Status != workflow.RunDismissed {
		t.Fatalf("run %+v %v", run, ok)
	}
	r.clock.Advance(time.Hour)
	r.sweep(t)
	if items := r.items(t, u7, workflow.DecisionPending, workflow.DecisionRun); len(items) != 0 {
		t.Fatalf("the skipped round came back: %+v", items)
	}
	if len(r.steps.Ran()) != 0 || len(r.steps.Dismissed()) != 1 {
		t.Fatalf("ran %v, dismissed %v", r.steps.Ran(), r.steps.Dismissed())
	}
}

func TestGuideRunsThePendingItemWithGuidance(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	r.pending7(t)
	rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/guide", `{"guidance":"look at the retry"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	ran := r.steps.Ran()
	if len(ran) != 1 || ran[0].Unit != u7 || ran[0].Run.Guidance != "look at the retry" || !ran[0].Run.Confirmed {
		t.Fatalf("ran %+v", ran)
	}
	if items := r.items(t, u7, workflow.DecisionPending); len(items) != 0 {
		t.Fatalf("still pending %+v", items)
	}
}

func TestGuideWithNoRoundPendingKeepsTheGuidance(t *testing.T) {
	r := newPlanRig(t)
	rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/guide", `{"guidance":"look at the retry"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "goes with the next automatic round") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if o, _ := r.s.store().Override(context.Background(), r7); o.NextGuidance != "look at the retry" {
		t.Fatalf("guidance %q", o.NextGuidance)
	}
}

func TestPlanActionsActOnItems(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	sess := r.items(t, r.sess, workflow.DecisionPending)
	if len(sess) != 1 {
		t.Fatalf("session items %+v", sess)
	}
	res := r.act(t, api.PlanActRequest{IDs: []string{sess[0].ID, "nope"}, Action: "apply"})
	if len(r.steps.Ran()) != 1 || r.steps.Ran()[0].Unit != r.sess || !r.steps.Ran()[0].Run.ByHand {
		t.Fatalf("ran %+v", r.steps.Ran())
	}
	if len(res.Items) != 1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "no plan item nope") {
		t.Fatalf("%+v", res)
	}
	it := r.pending7(t)
	res = r.act(t, api.PlanActRequest{IDs: []string{it.ID}, Action: "dismiss"})
	if run, ok := r.marked(t, it); !ok || run.Status != workflow.RunDismissed || len(res.Errors) != 0 {
		t.Fatalf("run %+v %v, %+v", run, ok, res)
	}
}

func TestHoldPauseAndByHandDropWithoutMarking(t *testing.T) {
	for _, c := range []struct {
		name string
		drop func(t *testing.T, r *planRig)
	}{
		{"pause", func(t *testing.T, r *planRig) {
			if rec := post(t, r.s, "/api/units/github/acme%2Fapi%237/pause", `{}`); rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		}},
		{"pause through the plan", func(t *testing.T, r *planRig) {
			if res := r.act(t, api.PlanActRequest{IDs: []string{r.pending7(t).ID}, Action: "pause"}); len(res.Errors) != 0 || len(res.Items) != 0 {
				t.Fatalf("%+v", res)
			}
			if o, _ := r.s.store().Override(context.Background(), r7); !o.Hold {
				t.Fatal("not held")
			}
		}},
		{"pause", func(t *testing.T, r *planRig) {
			if rec := post(t, r.s, "/api/pause", `{"paused":true,"except":["acme/other"]}`); rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		}},
		{"by hand", func(t *testing.T, r *planRig) {
			r.s.deps.Actions = &fakeActions{}
			if err := r.s.act(context.Background(), "run_now", u7, api.ActionRequest{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newPlanRig(t)
			r.sweep(t)
			it := r.pending7(t)
			c.drop(t, r)
			if items := r.items(t, u7, workflow.DecisionPending); len(items) != 0 {
				t.Fatalf("still pending %+v", items)
			}
			if run, ok := r.marked(t, it); ok {
				t.Fatalf("the drop marked the key: %+v", run)
			}
			r.s.deps.Pauses.SetPause(context.Background(), round.Pause{})
			r.sweep(t)
			r.pending7(t)
		})
	}
}

func TestPauseStopsApplyButThePlanKeepsFilling(t *testing.T) {
	r := newPlanRig(t)
	if rec := post(t, r.s, "/api/pause", `{"paused":true}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	r.sweep(t)
	res := r.plan(t, "")
	byUnit := map[unit.Unit]api.PlanItem{}
	for _, it := range res.Items {
		byUnit[it.Unit] = it
	}
	if it := byUnit[u7]; it.Decision != "skipped" || it.Reason != "paused" {
		t.Fatalf("apply item %+v", it)
	}
	if it := byUnit[r.sess]; it.Decision != "pending" || it.Reason != "observe mode" {
		t.Fatalf("observe item %+v", it)
	}
	r.clock.Advance(time.Hour)
	if err := r.s.deps.Plan.DueRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.steps.Ran()) != 0 {
		t.Fatalf("ran while paused: %+v", r.steps.Ran())
	}
}

func TestAPendingItemSurvivesADaemonRestart(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	want := r.pending7(t)
	again := newServerOn(t, r.db, r.log, func(d *Deps) {
		d.Now = r.clock.Now
		r.eng = r.engine(d)
		d.Plan, d.Sweep, d.Pending = enginePlan{r.eng}, r.eng, planPending{r.eng}
	})
	r.s = again
	res := r.plan(t, "?workflow=review-follow-up")
	if len(res.Items) != 1 || res.Items[0].ID != want.ID || !res.Items[0].Deadline.Equal(want.Deadline) || res.Items[0].Decision != "pending" {
		t.Fatalf("after a restart %+v, want %+v", res.Items, want)
	}
	r.clock.Set(want.Deadline.Add(time.Second))
	runDue(t, r.s)
	if ran := r.steps.Ran(); len(ran) != 1 || ran[0].Unit != u7 {
		t.Fatalf("ran %+v", ran)
	}
}

// runDue runs the server's due job once.
func runDue(t *testing.T, s *Server) {
	t.Helper()
	j, ok := s.jobs["due"]
	if !ok {
		t.Fatal("no due job")
	}
	j.once(context.Background(), t.Logf, func(string, string) {})
	if st := j.status(); st.LastErr != "" {
		t.Fatalf("due job: %s", st.LastErr)
	}
}

func TestTheDueJobRunsAnExpiredItemOnce(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	it := r.pending7(t)
	runDue(t, r.s)
	if len(r.steps.Ran()) != 0 {
		t.Fatalf("ran before its window ended: %+v", r.steps.Ran())
	}
	r.clock.Set(it.Deadline.Add(time.Second))
	runDue(t, r.s)
	runDue(t, r.s)
	if ran := r.steps.Ran(); len(ran) != 1 || ran[0].Unit != u7 {
		t.Fatalf("ran %+v", ran)
	}
	if run, ok := r.marked(t, it); !ok || run.Status != workflow.RunDone {
		t.Fatalf("run %+v %v", run, ok)
	}
}

func TestTheDueJobNeedsASweep(t *testing.T) {
	r := newPlanRig(t)
	if _, ok := r.s.jobs["due"]; !ok {
		t.Fatal("no due job beside the sweep")
	}
	s := newServerOn(t, r.db, r.log, func(d *Deps) { d.Plan = enginePlan{r.eng} })
	if _, ok := s.jobs["due"]; ok {
		t.Fatal("a due job without a sweep")
	}
}

func TestABlockedItemOffersApplyDisabledWithItsReason(t *testing.T) {
	r := newPlanRig(t)
	r.steps.Refuse(builtin.StepTurnStart, u7, "held by you")
	r.sweep(t)
	res := r.plan(t, "?workflow=review-follow-up")
	if len(res.Items) != 1 || res.Items[0].Decision != "blocked" {
		t.Fatalf("%+v", res.Items)
	}
	acts := res.Items[0].Actions
	i := slices.IndexFunc(acts, func(st api.ActionState) bool { return st.ID == catalog.PlanApply })
	if i < 0 || acts[i].Enabled || acts[i].Reason != "held by you" {
		t.Fatalf("actions %+v", acts)
	}
	out := r.act(t, api.PlanActRequest{IDs: []string{res.Items[0].ID}, Action: "apply"})
	if len(out.Errors) != 1 || !strings.HasSuffix(out.Errors[0], ": held by you") || len(r.steps.Ran()) != 0 {
		t.Fatalf("%+v, ran %+v", out, r.steps.Ran())
	}
}

func TestPendingRoundsComeFromThePlan(t *testing.T) {
	r := newPlanRig(t)
	deadline := time.Date(2026, 10, 8, 10, 5, 0, 0, time.UTC)
	r.s = newServerOn(t, r.db, r.log, func(d *Deps) {
		d.Plan = enginePlan{r.eng}
		d.Pending = fixedPending{u7: {Intent: "follow-up", Reason: "ana replied", Deadline: deadline}}
	})
	row, ok := row7(t, r.s, api.ViewAll)
	if !ok || row.Pending == nil || !row.Pending.Deadline.Equal(deadline) {
		t.Fatalf("%+v", row)
	}
}

type fixedPending map[unit.Unit]round.Pending

func (f fixedPending) Pending(context.Context) (map[unit.Unit]round.Pending, error) { return f, nil }

// countingBriefs counts the brief runs and touches.
type countingBriefs struct {
	stubBriefs
	runs    int
	touched []pr.Ref
}

func (c *countingBriefs) Run(context.Context) (string, error) {
	c.runs++
	return "", nil
}

func (c *countingBriefs) Touched(refs []pr.Ref) { c.touched = append(c.touched, refs...) }

// interleaved is a ledger that runs between, once, before its next Save:
// what another writer does while a sweep plans unlocked.
type interleaved struct {
	automation.Ledger
	between func()
}

func (l *interleaved) Save(ctx context.Context, p automation.PlanWrite, evs ...port.Event) error {
	if f := l.between; f != nil {
		l.between = nil
		f()
	}
	return l.Ledger.Save(ctx, p, evs...)
}

func TestTheDueJobRunningAnItemDuringASweepRunsItOnce(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	it := r.pending7(t)
	r.clock.Set(it.Deadline.Add(time.Second))
	r.eng.Ledger = &interleaved{Ledger: r.db.Ledger(), between: func() { runDue(t, r.s) }}
	r.sweep(t)
	runDue(t, r.s)
	if ran := r.steps.Ran(); len(ran) != 1 {
		t.Fatalf("ran %d times: %+v", len(ran), ran)
	}
	if items := r.items(t, u7); len(items) != 0 {
		t.Fatalf("the run item came back: %+v", items)
	}
}

func TestADismissDuringASweepStaysDismissed(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	it := r.pending7(t)
	r.eng.Ledger = &interleaved{Ledger: r.db.Ledger(), between: func() {
		if res := r.act(t, api.PlanActRequest{IDs: []string{it.ID}, Action: "dismiss"}); len(res.Errors) != 0 {
			t.Errorf("%+v", res)
		}
	}}
	r.sweep(t)
	if items := r.items(t, u7); len(items) != 0 {
		t.Fatalf("the dismissed item came back: %+v", items)
	}
	r.clock.Set(it.Deadline.Add(time.Second))
	runDue(t, r.s)
	r.sweep(t)
	if ran := r.steps.Ran(); len(ran) != 0 {
		t.Fatalf("a dismissed item ran: %+v", ran)
	}
}

func TestPauseKeepsObserveItems(t *testing.T) {
	r := newPlanRig(t)
	r.sweep(t)
	if rec := post(t, r.s, "/api/pause", `{"paused":true}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if items := r.items(t, r.sess, workflow.DecisionPending); len(items) != 1 {
		t.Fatalf("the pause dropped an observe item: %+v", items)
	}
	if items := r.items(t, u7, workflow.DecisionPending); len(items) != 0 {
		t.Fatalf("the pause kept an apply item: %+v", items)
	}
}

// countingState is a state loader that counts its loads.
type countingState struct{ loads int }

func (c *countingState) Load(context.Context) (automation.State, error) {
	c.loads++
	return savedNothing{}, nil
}

type savedNothing struct{}

func (savedNothing) Save(context.Context) error { return nil }

func TestTheDueJobLoadsTheRoundStateOnlyForADueItem(t *testing.T) {
	r := newPlanRig(t)
	st := &countingState{}
	r.eng.State = st
	runDue(t, r.s)
	r.sweep(t)
	loads := st.loads
	it := r.pending7(t)
	runDue(t, r.s)
	if st.loads != loads {
		t.Fatalf("the due job loaded the round state with nothing due: %d loads", st.loads-loads)
	}
	r.clock.Set(it.Deadline.Add(time.Second))
	runDue(t, r.s)
	if st.loads != loads+1 || len(r.steps.Ran()) != 1 {
		t.Fatalf("loads %d, ran %+v", st.loads-loads, r.steps.Ran())
	}
}

func TestThePlanServesTheSessionsClosedInTheLastDay(t *testing.T) {
	r := newPlanRig(t)
	now := r.clock.Now()
	ctx := context.Background()
	for _, c := range []struct {
		handle, data string
		at           time.Time
	}{
		{"term_old", "its pull request merged" + turns.ResumeWith + "claude --resume a", now.Add(-25 * time.Hour)},
		{"term_1", "its pull request merged" + turns.ResumeWith + "cd /wt/a && claude --resume b", now.Add(-2 * time.Hour)},
		{"term_2", "gone from the manager", now.Add(-time.Hour)},
	} {
		ev := port.Event{Kind: turns.KindSessionClosed, Key: c.handle, Data: c.data, At: c.at}
		if err := sqlite.New(r.db).SaveSession(ctx, dturn.Session{ID: c.handle}, ev); err != nil {
			t.Fatal(err)
		}
	}
	want := []api.ClosedSession{
		{Session: "term_2", At: now.Add(-time.Hour), Reason: "gone from the manager"},
		{Session: "term_1", At: now.Add(-2 * time.Hour), Reason: "its pull request merged", Resume: "cd /wt/a && claude --resume b"},
	}
	sums, gen := checksum(t, r.db), r.log.Gen()
	res := r.plan(t, "?closed=1")
	if checksum(t, r.db) != sums || r.log.Gen() != gen {
		t.Fatal("reading the closed sessions wrote to the store or noted the change log")
	}
	if len(res.Closed) != len(want) {
		t.Fatalf("closed %+v", res.Closed)
	}
	for i, c := range res.Closed {
		if c.Session != want[i].Session || !c.At.Equal(want[i].At) || c.Reason != want[i].Reason || c.Resume != want[i].Resume {
			t.Errorf("closed[%d] %+v, want %+v", i, c, want[i])
		}
	}
	for _, q := range []string{"", "?trigger=sweep&closed=1"} {
		if res := r.plan(t, q); res.Closed != nil {
			t.Errorf("%q: closed %+v", q, res.Closed)
		}
	}
}

func TestTheActionsCarryThePlanSpecs(t *testing.T) {
	r := newPlanRig(t)
	var res api.ActionsResponse
	if code := get(t, r.s, api.Actions, &res); code != http.StatusOK {
		t.Fatalf("GET %s: %d", api.Actions, code)
	}
	if len(res.Plan) != len(catalog.PlanSpecs()) || res.Plan[0].ID != catalog.PlanApply || res.Plan[0].Key != "Enter" {
		t.Fatalf("plan specs %+v", res.Plan)
	}
}

// labeledRows serves a session row labeled apart from its unit key.
type labeledRows struct{ threadRows }

func (labeledRows) Provider() unit.ProviderID { return unit.Session }

func (labeledRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: in.(string), Label: "quiet terminal", Views: []string{}}, nil
}

func TestPlanItemsCarryTheirRowsLabel(t *testing.T) {
	r := newPlanRig(t, func(d *Deps) {
		d.RowProviders = append(d.RowProviders, func(readmodel.RowDeps) readmodel.Provider {
			return labeledRows{threadRows{units: []unit.Unit{{Provider: unit.Session, Key: "term_1"}}}}
		})
	})
	r.sweep(t)
	if _, err := r.s.readModel().Current(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"?trigger=sweep", "", "?unit=session:term_1"} {
		res := r.plan(t, q)
		i := slices.IndexFunc(res.Items, func(it api.PlanItem) bool { return it.Unit == r.sess })
		if i < 0 || res.Items[i].Label != "quiet terminal" {
			t.Fatalf("%q: %+v", q, res.Items)
		}
		if j := slices.IndexFunc(res.Items, func(it api.PlanItem) bool { return it.Unit == u7 }); j >= 0 && res.Items[j].Label != "acme/api#7" {
			t.Fatalf("%q: the pull request is labeled %q", q, res.Items[j].Label)
		}
	}
}
