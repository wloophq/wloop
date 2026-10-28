package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const somedayWorkflow = `summary = "Move a next action to Someday"
provider = "entry"
when = 'entry.state == "next"'

[[step]]
use = "entry.verb"
verb = "someday"
`

// entryRig is the entries service over a store, served as the daemon's.
type entryRig struct {
	te *testEnv
	db *sqlite.DB
}

func newEntryRig(t *testing.T, files map[string]string) *entryRig {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	if files != nil {
		userProfile(t, te, files)
		if l := te.loadedIntents(); len(l.Problems) > 0 {
			t.Fatal(l.Problems.Error())
		}
	}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te.gtd = coretasks.New(coretasks.Deps{Store: db.Entries(), Facts: coretasks.CardFacts{Cards: db.Cards()}, Clock: clockFunc(func() time.Time { return te.Now() })})
	return &entryRig{te: te, db: db}
}

func (r *entryRig) put(t *testing.T, id task.ID, state task.State) {
	t.Helper()
	at := testNow.Add(-time.Hour)
	if err := r.db.Entries().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Entry: task.Entry{ID: id, Kind: task.KindCapture, State: state,
		Title: "Ship it", Origin: task.OriginUser, Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
}

func TestEntryVerbMovesTheEntryAndRecordsNoVerbEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newEntryRig(t, map[string]string{"workflows/my-someday/workflow.toml": somedayWorkflow})
	r.put(t, "e-01", task.Next)
	ed := engineDeps{DB: r.db, SessionFacts: noUnits{}, WorkspaceFacts: noUnits{}}
	r.te.wireSubjects(&serveWiring{DB: r.db, Cfg: r.te.cfg, Engine: &ed})
	eng := r.te.newEngine(ed)

	if _, _, err := eng.Sweep(ctx, &sync.Mutex{}); err != nil {
		t.Fatal(err)
	}
	e, err := r.db.Entries().Entry(ctx, "e-01")
	if err != nil {
		t.Fatal(err)
	}
	if e.State != task.Someday {
		items, _ := eng.Ledger.Items(ctx, automation.Filter{Workflow: "my-someday"})
		t.Fatalf("state %s; items %+v", e.State, items)
	}
	evs, err := r.db.Events().Since(ctx, 0, task.VerbEventKinds)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) > 0 {
		t.Fatalf("recorded %+v", evs)
	}
}

func TestEntryVerbCheckRefusesWhatTheEntrysStateRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newEntryRig(t, nil)
	r.put(t, "e-01", task.Done)
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds})
	u := task.UnitOf("e-01")
	refusal, err := s.Check(ctx, wfStep("entry.verb", "verb", "done"), u, workflow.Why{Workflow: "w"}, automation.CheckOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(refusal, "done on a done entry") {
		t.Fatalf("refusal %q", refusal)
	}
	if refusal, err := s.Check(ctx, wfStep("entry.verb", "verb", "reference"), u, workflow.Why{Workflow: "w"}, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Fatalf("reference on a done entry: %q %v", refusal, err)
	}
}

func TestEntryVerbLeavesClosingADerivedEntryToItsItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newEntryRig(t, nil)
	at := testNow.Add(-time.Hour)
	if err := r.db.Entries().Apply(ctx, []task.Change{{Op: task.OpCreate, Entry: task.Entry{ID: "e-02", Kind: task.KindWaiting, State: task.Waiting,
		Title: "Waiting on the reviewer", Origin: task.OriginDerived, Created: at, Updated: at}}}); err != nil {
		t.Fatal(err)
	}
	s := newStepSet(r.te.Env, stepDeps{Units: automationtest.NewUnits(), Rounds: r.te.Rounds})
	why := workflow.Why{Workflow: "w"}
	for _, v := range []string{"done", "dismiss"} {
		refusal, err := s.Check(ctx, wfStep("entry.verb", "verb", v), task.UnitOf("e-02"), why, automation.CheckOpts{})
		if err != nil || !strings.Contains(refusal, "closes when its items resolve") {
			t.Errorf("%s on a derived entry: %q %v", v, refusal, err)
		}
	}
	if refusal, err := s.Check(ctx, wfStep("entry.verb", "verb", "someday"), task.UnitOf("e-02"), why, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Errorf("someday on a derived entry: %q %v", refusal, err)
	}
}

func TestEntryVerbRunsOnlyVerbsThatMoveAStoredEntry(t *testing.T) {
	t.Parallel()
	for _, v := range builtin.EntryStepVerbs {
		moves := false
		for _, s := range []task.State{task.Inbox, task.Next, task.Waiting, task.Scheduled, task.Someday, task.Reference, task.Done, task.Trash} {
			moves = moves || coretasks.Accepts(task.Entry{State: s}, task.Verb(v)) == nil
		}
		if !moves {
			t.Errorf("no state takes %s", v)
		}
	}
	for _, v := range []task.Verb{task.VerbApprove, task.VerbDelegate, task.VerbAnswer, task.VerbReply, task.VerbEdit, task.VerbRetry, task.VerbProject, task.VerbSeen} {
		if slices.Contains(builtin.EntryStepVerbs, string(v)) {
			t.Errorf("entry.verb runs %s", v)
		}
	}
}
