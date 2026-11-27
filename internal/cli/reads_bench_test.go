package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// cockpitShape is a store as the cockpit reads it on a busy day: every task open, each linked to a pull request
// or a Slack thread holding up to four items, a question on one task in ten, a held draft on one in twenty, and
// a project every fifty tasks whose next tasks are its children.
type cockpitShape struct{ tasks int }

// seed writes s into db.
func (s cockpitShape) seed(tb testing.TB, db *sqlite.DB) {
	tb.Helper()
	ctx := context.Background()
	snaps := psource.NewSnapshots(db.Snapshots())
	at := testNow.Add(-3 * 24 * time.Hour)
	reported := map[unit.Unit]src.Snapshot{}
	var its []items.Item
	var changes []task.Change
	var project task.ID
	for i := range s.tasks {
		id := task.ID(fmt.Sprintf("t-%016x", i+1))
		e := task.Task{ID: id, Title: "Widget task " + strconv.Itoa(i+1), Notes: "about widget " + strconv.Itoa(i%97), Origin: task.OriginUser,
			Created: at.Add(time.Duration(i) * time.Minute), Updated: at.Add(time.Duration(i) * time.Minute)}
		switch b := i % 10; {
		case b < 2:
			e.State, e.Category = "inbox", task.CategoryInbox
		case b < 7:
			e.State, e.Category = "next", task.CategoryReady
		default:
			e.State, e.Category = "waiting", task.CategoryWaiting
		}
		switch {
		case i%50 == 0:
			e.SetProp(task.PropKind, task.Value{Text: task.KindProject}, task.WriterPerson, at)
			e.State, e.Category = "next", task.CategoryReady
			project = id
		case i%5 == 1 && project != "":
			e.Parent, e.Position = project, i
		}
		n := i % 5
		if i%2 == 0 {
			ref := pr.Ref{Owner: "acme", Repo: "widgets", Number: i + 1}
			p := dinv.PR{Ref: ref, Title: "Widget change " + strconv.Itoa(i+1), Author: "ana", State: "OPEN", HeadOid: fmt.Sprintf("%040x", i+1),
				HeadBranch: "feat/" + strconv.Itoa(i+1), BaseBranch: "main", ChecksState: "SUCCESS", ReviewDecision: "REVIEW_REQUIRED",
				URL: "https://github.example/acme/widgets/pull/" + strconv.Itoa(i+1), CreatedAt: at, UpdatedAt: at, LastSeenAt: testNow,
				Reasons: []string{"review_requested"}}
			reported[ref.Unit()] = ghreporttest.Snapshot(ghreporttest.Env{Viewer: "me", Now: testNow}, ghreporttest.Input{PR: p})
			e.Units = []unit.Unit{ref.Unit()}
		} else {
			u := slackThreadUnit("T0", "C0", fmt.Sprintf("17000%05d.000100", i))
			reported[u] = src.Snapshot{Unit: psource.Wire(u), Rev: 1, At: testNow, Data: []byte(`{"topic":"widgets"}`),
				Card:       &src.Card{Label: "Thread", Title: "Widget thread " + strconv.Itoa(i), State: "open", Why: "asked", Since: at},
				Facts:      []src.Fact{{Name: "slack.live_task", Value: src.Bool(true)}, {Name: "slack.where", Value: src.String("widgets")}},
				Extraction: &src.ExtractionHealth{Pending: 1, Oldest: testNow.Add(-time.Hour)}}
			e.Units = []unit.Unit{u}
		}
		for j := range n {
			u := e.Units[0]
			it := items.Item{ID: fmt.Sprintf("i%d-%d", i, j), Unit: u, Kind: items.Request, Summary: "widget ask " + strconv.Itoa(j),
				Origin:    unit.Anchor{Unit: u, Ref: fmt.Sprintf("m:%d-%d", i, j)},
				Requester: items.Person{Key: "slack:U0009", Name: "Bea"}, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
				Provenance: items.Extracted, WaitingOn: []items.Person{{Key: "slack:U0001"}}, CreatedAt: at, UpdatedAt: at}
			its = append(its, it)
			e.Items = append(e.Items, task.ItemRef{Unit: u, Item: it.ID})
		}
		changes = append(changes, task.Change{Op: task.OpCreate, Task: e})
	}
	if err := snaps.Put(ctx, reported); err != nil {
		tb.Fatal(err)
	}
	if err := db.Items().Commit(ctx, coreitems.Commit{Items: its}); err != nil {
		tb.Fatal(err)
	}
	if err := db.Tasks().Apply(ctx, changes); err != nil {
		tb.Fatal(err)
	}
	gs := db.Gates(func() time.Time { return testNow })
	msgs := inventory.NewMessages(db)
	for i, c := range changes {
		id, u := c.Task.ID, c.Task.Units[0]
		if i%10 == 3 {
			m, err := msgs.Upsert(ctx, message.Message{TaskID: string(id), Unit: task.UnitOf(id), Kind: message.Question, Text: "Which widget first?",
				Origin: message.Origin{Kind: message.OriginWorkflow, Workflow: "widget-ask"}, CreatedAt: testNow.Add(-time.Hour)})
			if err != nil {
				tb.Fatal(err)
			}
			if _, err := gs.Open(ctx, gate.Gate{Task: id, Unit: task.UnitOf(id), Kind: gate.KindQuestion, Subject: strconv.FormatInt(m.ID, 10)}); err != nil {
				tb.Fatal(err)
			}
		}
		if i%20 == 7 {
			d, err := db.Drafts().Insert(ctx, draft.Draft{Unit: u, Task: string(id), Kind: draft.Kind("nudge"), Purpose: "nudge",
				Delivery: draft.Delivery("message"), Text: "a widget nudge", Original: "a widget nudge",
				Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "widget-nudge", Rule: "widget-nudge"},
				State:  draft.Held, CreatedAt: testNow.Add(-time.Hour), UpdatedAt: testNow.Add(-time.Hour)})
			if err != nil {
				tb.Fatal(err)
			}
			if _, err := gs.Open(ctx, gate.Gate{Task: id, Unit: u, Kind: gate.KindDraft, Subject: strconv.FormatInt(int64(d.ID), 10)}); err != nil {
				tb.Fatal(err)
			}
		}
	}
}

// readsDaemon is a daemon over a store shaped as s, wired as serve wires it, and the change log it notes on; the
// sources' rows read the snapshots seed stored, as a running plugin's.
func readsDaemon(tb testing.TB, s cockpitShape) (http.Handler, *change.Log) {
	tb.Helper()
	te := newTestEnv(tb, &fake.Manager{})
	te.Env.changes = change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(tb.TempDir(), "store.db"), te.Env.changes)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { db.Close() })
	te.db, te.Store = db, inventory.New(db)
	te.Rounds = sqlite.NewRounds(db)
	te.Env.reports = func(*inventory.Store) readmodel.Reports { return psource.NewSnapshots(db.Snapshots()) }
	s.seed(tb, db)
	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	te.Env.cfg = te.Env.configHolder(te.cfg)
	te.Env.IntentRecheck = intentRecheck
	te.Env.intents = newEnvIntents(te.Env)
	te.Env.linkStates = new(atomic.Pointer[daemon.Server])
	d := te.serveDeps(ctx, db, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil)
	if _, err := d.Startup(ctx); err != nil {
		tb.Fatal(err)
	}
	d.Startup = nil
	srv := daemon.New(d)
	te.Env.linkStates.Store(srv)
	return srv.Handler(), te.Env.changes
}

// The cockpit's reads: what a pasted address searches, and the three reads a Needs-you client makes on an event.
const (
	readSearch   = api.SearchPath + "?q=acme%2Fwidgets%2Fpull%2F1201"
	readQueue    = "/api/queue"
	readHome     = api.HomePath
	readNeedsYou = api.TasksPath + "?view=needs-you"
)

// read serves GET path on h under ctx.
func read(tb testing.TB, ctx context.Context, h http.Handler, path string) {
	tb.Helper()
	req := httptest.NewRequestWithContext(ctx, "GET", path, nil)
	req.Header.Set(api.Header, api.Version)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		tb.Errorf("GET %s: %d: %s", path, rec.Code, rec.Body)
	}
}

// burst is what three clients read after one event: the Needs-you reads in parallel, with a search beside them.
func burst(tb testing.TB, ctx context.Context, h http.Handler) {
	var wg sync.WaitGroup
	for _, path := range []string{readNeedsYou, readHome, readQueue, readSearch} {
		wg.Go(func() { read(tb, ctx, h, path) })
	}
	wg.Wait()
}

// BenchmarkReads times the cockpit's reads over 2000 open tasks, wired as serve wires them; cold reads follow a
// note of a topic the facts read, so each builds a new pass. queries/op counts the statements the store ran.
func BenchmarkReads(b *testing.B) {
	h, log := readsDaemon(b, cockpitShape{tasks: 2000})
	cold := func() { log.Note(port.TopicTasks) }
	for _, c := range []struct {
		name string
		prep func()
		run  func(context.Context)
	}{
		{"search-warm", nil, func(ctx context.Context) { read(b, ctx, h, readSearch) }},
		{"search-cold", cold, func(ctx context.Context) { read(b, ctx, h, readSearch) }},
		{"queue-cold", cold, func(ctx context.Context) { read(b, ctx, h, readQueue) }},
		{"tasks-needs-you-cold", cold, func(ctx context.Context) { read(b, ctx, h, readNeedsYou) }},
		{"burst", cold, func(ctx context.Context) { burst(b, ctx, h) }},
	} {
		b.Run(c.name, func(b *testing.B) {
			read(b, context.Background(), h, readSearch)
			ctx, sc := meter.Start(context.Background(), "reads")
			defer sc.End()
			for b.Loop() {
				if c.prep != nil {
					c.prep()
				}
				c.run(ctx)
			}
			b.ReportMetric(float64(sc.Tally().Total(meter.SQL).Calls)/float64(b.N), "queries/op")
		})
	}
}

// TestACockpitReadQueriesTheStoreAFixedNumberOfTimes guards BenchmarkReads' cold reads: ten times the tasks
// cost no more statements.
func TestACockpitReadQueriesTheStoreAFixedNumberOfTimes(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds two stores")
	}
	queries := func(n int, path string) (int64, *meter.Scope) {
		h, log := readsDaemon(t, cockpitShape{tasks: n})
		log.Note(port.TopicTasks)
		ctx, sc := metertest.Start(t, context.Background())
		read(t, ctx, h, path)
		return sc.Tally().Total(meter.SQL).Calls, sc
	}
	for _, path := range []string{readSearch, readQueue, readHome, readNeedsYou} {
		t.Run(path, func(t *testing.T) {
			few, _ := queries(20, path)
			many, sc := queries(200, path)
			if many > few {
				t.Errorf("%s: %d statements over 20 tasks, %d over 200", path, few, many)
				metertest.Within(t, sc, metertest.Budget{{Res: meter.SQL}: few})
			}
		})
	}
}
