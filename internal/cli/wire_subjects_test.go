package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"

	factslack "github.com/wloophq/wloop/internal/adapter/facts/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var subjectThread = dslack.ThreadUnit("T0001", "C0001", "1700000000.000100")

const (
	waitingWorkflow = `summary = "Watch waiting entries"
provider = "entry"
when = 'entry.state == "waiting" && entry.items_owed > 0 && entry.settled_at < now'
key = ["entry.settled_at", "entry.waiting_on"]

[[step]]
use = "notify.self"
`
	closeWorkflow = `summary = "Watch close entries with a resolved item"
provider = "entry"
when = 'entry.kind == "close" && entry.items_resolved > 0'

[[step]]
use = "notify.self"
`
	slackWorkflow = `summary = "Watch Slack units with unowned items"
provider = "slack"
when = 'slack.live_entry && slack.items_unowned > 0'

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
	te  *testEnv
	db  *sqlite.DB
	log *change.Log
	eng *automation.Engine
}

// newSubjectsRig is a plan engine wired as the daemon wires it, over a store
// and the Slack plugin's facts, with the user's workflows on the entry,
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
	mods := []source.Module{{ID: dslack.Provider, Facts: factslack.New(db, clockFunc(func() time.Time { return te.Now() }), config.DefaultSlackEndedAfter)}}
	ed := engineDeps{DB: db, Modules: mods, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	te.wireSubjects(&serveWiring{DB: db, Cfg: te.cfg, Modules: mods, Engine: &ed})
	return &subjectsRig{te: te, db: db, log: log, eng: te.planEngine(ed)}
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

func (r *subjectsRig) entry(t *testing.T, e task.Entry, op task.ChangeOp) {
	t.Helper()
	e.Origin, e.Created, e.Updated = task.OriginUser, testNow.Add(-72*time.Hour), testNow.Add(-72*time.Hour)
	if err := r.db.Entries().Apply(context.Background(), []task.Change{{Op: op, Entry: e}}); err != nil {
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

func TestPlanEvaluatesUserWorkflowsOnEntryAndSlackSubjects(t *testing.T) {
	t.Parallel()
	r := newSubjectsRig(t)
	owed := r.item(t, subjectThread, "i1", items.Open, items.Person{Key: "slack:U0001"})
	r.item(t, subjectThread, "i2", items.Open)
	r.entry(t, task.Entry{ID: "e-01", Kind: task.KindWaiting, State: task.Waiting, Title: "w", Units: []unit.Unit{subjectThread},
		Items:   []task.ItemRef{{Unit: owed.Unit, Item: owed.ID}},
		Waiting: &task.WaitingOn{On: []items.Person{{Key: "slack:U0001"}}, Since: testNow.Add(-72 * time.Hour)}}, task.OpCreate)
	want := []string{"my-slack " + subjectThread.String(), "my-waiting entry:e-01"}
	if got := r.planned(t); !slices.Equal(got, want) {
		t.Fatalf("planned %v, want %v", got, want)
	}
	if _, err := r.db.Drafts().Insert(context.Background(), draft.Draft{Unit: subjectThread, Entry: "e-01", Kind: draft.Nudge, Purpose: "nudge",
		Delivery: draft.Message, Text: "x", Original: "x", Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-waiting", Rule: "my-waiting"},
		Approval: draft.Approval{Mode: draft.Wait}, State: draft.Sent, CreatedAt: testNow.Add(time.Hour), UpdatedAt: testNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := r.planned(t); !slices.Equal(got, want[:1]) {
		t.Fatalf("a draft my-waiting settled after now keeps it quiet: planned %v", got)
	}
}

func TestTheSweepWakesOnEntryItemAndDraftChanges(t *testing.T) {
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
	close := task.Entry{ID: "e-01", Kind: task.KindClose, State: task.Inbox, Title: "c", Units: []unit.Unit{subjectThread}}
	woke("a new entry", func() { r.entry(t, close, task.OpCreate) })
	if got := r.planned(t); slices.Contains(got, "my-close entry:e-01") {
		t.Fatalf("nothing resolved yet: %v", got)
	}
	woke("a resolved item", func() { r.item(t, it.Unit, it.ID, items.Resolved) })
	if got := r.planned(t); !slices.Contains(got, "my-close entry:e-01") {
		t.Fatalf("the next pass after the item resolved plans the close entry: %v", got)
	}
	woke("a draft", func() {
		if _, err := r.db.Drafts().Insert(ctx, draft.Draft{Unit: subjectThread, Kind: draft.Close, Purpose: "close", Delivery: draft.Message, Text: "x", Original: "x",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-close"}, Approval: draft.Approval{Mode: draft.Wait}, State: draft.Proposed,
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

func TestACapturedEntryWhoseUnitsEndedPlansItsClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newSubjectsRig(t)
	r.entry(t, task.Entry{ID: "e-01", Kind: task.KindCapture, State: task.Next, Title: "ship it", Units: []unit.Unit{subjectThread}}, task.OpCreate)
	derived := task.Entry{ID: "e-02", Kind: task.KindClose, State: task.Inbox, Title: "close", Units: []unit.Unit{subjectThread},
		Origin: task.OriginDerived, Created: testNow, Updated: testNow}
	if err := r.db.Entries().Apply(ctx, []task.Change{{Op: task.OpCreate, Entry: derived}}); err != nil {
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
			if it.Workflow == "entry-results-ended" {
				out = append(out, it.Unit.String())
			}
		}
		return out
	}
	if got := closes(); len(got) != 0 {
		t.Fatalf("closes %v before the thread ended", got)
	}
	if err := r.db.Extraction().Save(ctx, sqlite.ExtractionSave{States: map[unit.Unit]sqlite.ExtractionState{
		subjectThread: {ClosurePosted: true, At: testNow}}}); err != nil {
		t.Fatal(err)
	}
	if got := closes(); len(got) != 0 {
		t.Fatalf("closes %v, an entry no turn ran on", got)
	}
	start := dturn.Book{Unit: task.UnitOf("e-01"), Turns: []dturn.Turn{{Intent: "start", ID: "s1", Trigger: dturn.ByHand, State: dturn.Done, SentAt: testNow}}}
	if _, err := sqlite.New(r.db).Save(ctx, start); err != nil {
		t.Fatal(err)
	}
	if got := closes(); !slices.Equal(got, []string{"entry:e-01"}) {
		t.Fatalf("closes %v, want the captured entry only", got)
	}
}
