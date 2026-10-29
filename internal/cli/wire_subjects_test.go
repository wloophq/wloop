package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

var subjectThread = slackThreadUnit("T0001", "C0001", "1700000000.000100")

const (
	waitingWorkflow = `summary = "Watch waiting entries"
provider = "task"
when = 'task.state == "waiting" && task.owed_by != "me" && task.owed_by != "" && task.settled_at < now'
key = ["task.settled_at", "task.owed_by"]

[[step]]
use = "notify.self"
`
	closeWorkflow = `summary = "Watch close entries with a resolved item"
provider = "task"
when = 'task.state == "inbox" && task.items_resolved > 0'

[[step]]
use = "notify.self"
`
	slackWorkflow = `summary = "Watch Slack units with unowned items"
provider = "slack"
when = 'slack.live_task && slack.items_unowned > 0'

[[step]]
use = "notify.self"
`
	githubWorkflow = `summary = "Watch pull requests"
provider = "github"
when = 'pr.mine'

[[step]]
use = "notify.self"
`
)

type subjectsRig struct {
	te    *testEnv
	db    *sqlite.DB
	log   *change.Log
	eng   *automation.Engine
	snaps *psource.Snapshots
	rev   uint64
}

// report keeps facts as the Slack plugin's last snapshot of u.
func (r *subjectsRig) report(t *testing.T, u unit.Unit, facts ...src.Fact) {
	t.Helper()
	r.rev++
	if err := r.snaps.Put(context.Background(), map[unit.Unit]src.Snapshot{u: {Unit: psource.Wire(u), Rev: r.rev, At: testNow, Facts: facts}}); err != nil {
		t.Fatal(err)
	}
}

// newSubjectsRig is a plan engine wired as the daemon wires it, over a store
// and the facts the Slack plugin reports, with the user's workflows on the entry,
// slack and github subjects; github's plugin is not built. planned lists
// the user's workflows only.
func newSubjectsRig(t *testing.T) *subjectsRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{
		"workflows/my-waiting/workflow.toml": waitingWorkflow,
		"workflows/my-close/workflow.toml":   closeWorkflow,
		"workflows/my-slack/workflow.toml":   slackWorkflow,
		"workflows/my-prs/workflow.toml":     githubWorkflow,
		"workflows/my-waiting/text.md.tmpl":  "waiting",
		"workflows/my-close/text.md.tmpl":    "resolved",
		"workflows/my-slack/text.md.tmpl":    "unowned",
		"workflows/my-prs/text.md.tmpl":      "mine",
	})
	if l := te.loadedIntents(); len(l.Problems) > 0 {
		t.Fatal(l.Problems.Error())
	}
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	snaps := psource.NewSnapshots(db.Snapshots())
	s, err := te.pluginSource(slackPlugin)
	if err != nil {
		t.Fatal(err)
	}
	mods := []pluginModule{{ID: slackPlugin, Facts: psource.SettledFacts{UnitFacts: psource.UnitFacts{Snapshots: snaps}, Source: s, Drafts: db.Drafts()}}}
	ed := engineDeps{DB: db, Modules: mods, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	te.wireSubjects(&serveWiring{DB: db, Cfg: te.cfg, Modules: mods, Engine: &ed})
	return &subjectsRig{te: te, db: db, log: log, eng: te.planEngine(ed), snaps: snaps}
}

func (r *subjectsRig) item(t *testing.T, u unit.Unit, id string, s items.State, waitingOn ...items.Person) items.Item {
	t.Helper()
	at := testNow.Add(-72 * time.Hour)
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "s " + id, Origin: unit.Anchor{Unit: u, Ref: "m:" + id},
		Requester: items.Person{Key: "slack:U0009"}, Basis: items.NoBasis, State: s, Confidence: items.High,
		Provenance: items.Extracted, WaitingOn: waitingOn, CreatedAt: at, UpdatedAt: at}
	if s == items.Resolved {
		it.Resolution = items.Done
	}
	if err := r.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	return it
}

func (r *subjectsRig) task(t *testing.T, e task.Task, op task.ChangeOp) {
	t.Helper()
	e.Origin, e.Created, e.Updated = task.OriginUser, testNow.Add(-72*time.Hour), testNow.Add(-72*time.Hour)
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: op, Task: e}}); err != nil {
		t.Fatal(err)
	}
}

func (r *subjectsRig) planned(t *testing.T) []string {
	t.Helper()
	p, err := r.eng.Evaluate(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Errors) > 0 {
		t.Fatalf("plan errors: %v", p.Errors)
	}
	var out []string
	for _, it := range p.Items {
		if strings.HasPrefix(it.Workflow, "my-") {
			out = append(out, it.Workflow+" "+it.Unit.String())
		}
	}
	slices.Sort(out)
	return out
}

func TestPlanEvaluatesUserWorkflowsOnTaskAndSlackSubjects(t *testing.T) {
	t.Parallel()
	r := newSubjectsRig(t)
	owed := r.item(t, subjectThread, "i1", items.Open, items.Person{Key: "slack:U0001"})
	r.item(t, subjectThread, "i2", items.Open)
	r.report(t, subjectThread, src.Fact{Name: "slack.live_task", Value: src.Bool(true)}, src.Fact{Name: "slack.items_unowned", Value: src.Int(1)})
	r.task(t, task.Task{ID: "e-01", State: "waiting", Category: task.CategoryWaiting, Title: "w", Units: []unit.Unit{subjectThread},
		Items: []task.ItemRef{{Unit: owed.Unit, Item: owed.ID}}}, task.OpCreate)
	want := []string{"my-slack " + subjectThread.String(), "my-waiting task:e-01"}
	if got := r.planned(t); !slices.Equal(got, want) {
		t.Fatalf("planned %v, want %v", got, want)
	}
	if _, err := r.db.Drafts().Insert(context.Background(), draft.Draft{Unit: subjectThread, Task: "e-01", Kind: draft.Nudge, Purpose: "nudge",
		Delivery: draft.Message, Text: "x", Original: "x", Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-waiting", Rule: "my-waiting"},
		State: draft.Sent, CreatedAt: testNow.Add(time.Hour), UpdatedAt: testNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := r.planned(t); !slices.Equal(got, want[:1]) {
		t.Fatalf("a draft my-waiting settled after now keeps it quiet: planned %v", got)
	}
}

func TestTheSweepWakesOnTaskItemAndDraftChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSubjectsRig(t)
	woke := func(what string, write func()) {
		t.Helper()
		gen := r.log.Gen()
		write()
		if ch := r.log.Since(gen, port.SweepInputs); ch.Empty() {
			t.Errorf("%s did not wake the sweep", what)
		}
	}
	var it items.Item
	woke("a new item", func() { it = r.item(t, subjectThread, "i1", items.Open) })
	close := task.Task{ID: "e-01", State: "inbox", Category: task.CategoryInbox, Title: "c", Units: []unit.Unit{subjectThread}}
	woke("a new entry", func() { r.task(t, close, task.OpCreate) })
	if got := r.planned(t); slices.Contains(got, "my-close task:e-01") {
		t.Fatalf("nothing resolved yet: %v", got)
	}
	woke("a resolved item", func() { r.item(t, it.Unit, it.ID, items.Resolved) })
	if got := r.planned(t); !slices.Contains(got, "my-close task:e-01") {
		t.Fatalf("the next pass after the item resolved plans the close entry: %v", got)
	}
	woke("a draft", func() {
		if _, err := r.db.Drafts().Insert(ctx, draft.Draft{Unit: subjectThread, Kind: draft.Close, Purpose: "close", Delivery: draft.Message, Text: "x", Original: "x",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-close"}, State: draft.Held,
			CreatedAt: testNow, UpdatedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestADisabledSlackPluginListsNoUnits(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ed := engineDeps{DB: db}
	te.wireSubjects(&serveWiring{DB: db, Cfg: te.cfg, Engine: &ed})
	for _, p := range te.sourcePlugins() {
		us, err := te.planEngine(ed).Units.List(context.Background(), p.ID)
		if err != nil || len(us) > 0 {
			t.Errorf("%s, not built: listed %v, %v", p.ID, us, err)
		}
	}
	if us, err := te.planEngine(ed).Units.List(context.Background(), task.Codec{}.Provider()); err != nil || len(us) > 0 {
		t.Errorf("entries of an empty store: %v, %v", us, err)
	}
}

func TestACapturedTaskWhoseUnitsEndedPlansItsClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSubjectsRig(t)
	r.task(t, task.Task{ID: "e-01", State: "next", Category: task.CategoryReady, Title: "ship it", Units: []unit.Unit{subjectThread}}, task.OpCreate)
	derived := task.Task{ID: "e-02", State: "inbox", Category: task.CategoryInbox, Title: "close", Units: []unit.Unit{subjectThread},
		Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: derived}}); err != nil {
		t.Fatal(err)
	}
	closes := func() []string {
		t.Helper()
		p, err := r.eng.Evaluate(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range p.Items {
			if it.Workflow == "task-results-ended" {
				out = append(out, it.Unit.String())
			}
		}
		return out
	}
	if got := closes(); len(got) != 0 {
		t.Fatalf("closes %v before the thread ended", got)
	}
	r.report(t, subjectThread, src.Fact{Name: "slack.ended", Value: src.Bool(true)})
	if got := closes(); len(got) != 0 {
		t.Fatalf("closes %v, an entry no turn ran on", got)
	}
	start := dturn.Book{Unit: task.UnitOf("e-01"), Turns: []dturn.Turn{{Intent: "start", ID: "s1", Trigger: dturn.ByHand, State: dturn.Done, SentAt: testNow}}}
	if _, err := sqlite.New(r.db).Save(ctx, start); err != nil {
		t.Fatal(err)
	}
	if got := closes(); !slices.Equal(got, []string{"task:e-01"}) {
		t.Fatalf("closes %v, want the captured entry only", got)
	}
}
