package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/domain/view"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

const everyTopic = port.Topic(math.MaxUint32)

var (
	noteRef = pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	noteAt  = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
)

// noteEnv is one store under test; seeds leave what calls need in it.
type noteEnv struct {
	db  *DB
	seq int64
}

// writeCase pins what one write method, named Type.Method, notes: seed
// runs first and its notes are left out; call must note exactly once, or
// never when topics is zero.
type writeCase struct {
	method string
	name   string
	seed   func(t *testing.T, e *noteEnv)
	call   func(ctx context.Context, e *noteEnv) error
	topics port.Topic
	units  []unit.Unit
}

var (
	u7    = noteRef.Unit()
	term1 = unit.Unit{Provider: unit.Session, Key: "term_1"}
)

func loopEvent(kind string) port.Event {
	return port.Event{Unit: u7, Kind: kind, Key: "k", At: noteAt}
}

// writes is every exported write method of the sqlite store types.
var writes = []writeCase{
	{
		method: "DB.SaveAgentSession",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.SaveAgentSession(ctx, AgentSession{ID: "a1", Kind: "claude", Handle: "term_1", FirstSeen: noteAt})
		},
		topics: port.TopicSessions, units: []unit.Unit{term1},
	},
	{
		method: "Store.Save",
		call: func(ctx context.Context, e *noteEnv) error {
			_, err := New(e.db).Save(ctx, turn.Book{Unit: u7, Turns: []turn.Turn{{ID: "r1", State: turn.Sent, SentAt: noteAt}}}, loopEvent("round_sent"))
			return err
		},
		topics: port.TopicTurns | port.TopicLoopEvents, units: []unit.Unit{u7},
	},
	{
		method: "Store.SaveSession",
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).SaveSession(ctx, turn.Session{ID: "term_1"}, loopEvent("session_launched"))
		},
		topics: port.TopicSessions | port.TopicLoopEvents, units: []unit.Unit{u7, term1},
	},
	{
		method: "Store.LogStop",
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).LogStop(ctx, turn.Stop{Session: "term_1", PromptID: "p1", At: noteAt})
		},
		topics: port.TopicSessions, units: []unit.Unit{term1},
	},
	{
		method: "Store.BindHandle",
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).BindHandle(ctx, "term_1", "orca", "h1", "", noteAt)
		},
		topics: port.TopicSessions, units: []unit.Unit{term1},
	},
	{
		method: "Store.EndHandle",
		seed: func(t *testing.T, e *noteEnv) {
			if err := New(e.db).BindHandle(context.Background(), "term_1", "orca", "h1", "", noteAt); err != nil {
				t.Fatal(err)
			}
		},
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).EndHandle(ctx, "term_1", noteAt.Add(time.Minute))
		},
		topics: port.TopicSessions, units: []unit.Unit{term1},
	},
	{
		method: "Worktrees.Keep",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Worktrees().Keep(ctx, wsdomain.Custody{Path: "/wt", Manager: "orca", Units: []unit.Unit{u7}, At: noteAt})
		},
		topics: port.TopicPRs, units: []unit.Unit{wsdomain.UnitOf("/wt")},
	},
	{
		method: "Worktrees.LinkUnit",
		seed:   seedWorktree,
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Worktrees().LinkUnit(ctx, "/wt", u7)
		},
		topics: port.TopicPRs, units: []unit.Unit{wsdomain.UnitOf("/wt")},
	},
	{
		method: "Worktrees.Drop",
		seed:   seedWorktree,
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Worktrees().Drop(ctx, "/wt")
		},
		topics: port.TopicPRs, units: []unit.Unit{wsdomain.UnitOf("/wt")},
	},
	{
		method: "Store.SaveApplied",
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).SaveApplied(ctx, "orca", view.Applied{Unit: u7, Role: view.ReviewRole, Status: view.Working, At: noteAt})
		},
		topics: port.TopicViews, units: []unit.Unit{u7},
	},
	{
		method: "Store.SaveFailure",
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).SaveFailure(ctx, port.Event{Unit: u7, Kind: "view_failed", At: noteAt})
		},
		topics: port.TopicLoopEvents, units: []unit.Unit{u7},
	},
	{
		method: "Store.RecordEvents",
		call: func(ctx context.Context, e *noteEnv) error {
			return New(e.db).RecordEvents(ctx, port.Event{Unit: u7, Kind: "notify_self", Key: "ping@1", At: noteAt})
		},
		topics: port.TopicLoopEvents, units: []unit.Unit{u7},
	},
	{
		method: "Rounds.Save", name: "/fields and a count",
		call: func(ctx context.Context, e *noteEnv) error {
			return saveRounds(ctx, e.db, func(st *round.Store) { st.RecordFollowup(noteRef.String(), "fp", noteAt) })
		},
		topics: port.TopicRounds, units: []unit.Unit{u7},
	},
	{
		method: "Rounds.Save", name: "/a day note",
		call: func(ctx context.Context, e *noteEnv) error {
			return saveRounds(ctx, e.db, func(st *round.Store) { st.FirstCapHit(noteRef.String(), noteAt) })
		},
		topics: port.TopicRounds,
	},
	{
		method: "Rounds.SetPause",
		call: func(ctx context.Context, e *noteEnv) error {
			return NewRounds(e.db).SetPause(ctx, round.Pause{On: true, At: noteAt})
		},
		topics: port.TopicPause | port.TopicLoopEvents,
	},
	{
		method: "intakeStore.Accept",
		call: func(ctx context.Context, e *noteEnv) error {
			i := newIntake(noteRef, "review")
			_, _, err := e.db.Intakes().Accept(ctx, i, accepted(i))
			return err
		},
		topics: port.TopicIntakes | port.TopicLoopEvents, units: []unit.Unit{u7},
	},
	{
		method: "intakeStore.Save",
		seed: func(t *testing.T, e *noteEnv) {
			i, _, err := e.db.Intakes().Accept(context.Background(), newIntake(noteRef, "review"))
			if err != nil {
				t.Fatal(err)
			}
			e.seq = i.Seq
		},
		call: func(ctx context.Context, e *noteEnv) error {
			i := newIntake(noteRef, "review")
			i.Seq, i.Note = e.seq, "briefing"
			return e.db.Intakes().Save(ctx, i)
		},
		topics: port.TopicIntakes, units: []unit.Unit{u7},
	},
	{
		method: "usageStore.SaveUsage",
		seed:   seedTurn,
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Usage().SaveUsage(ctx, e.seq, []dusage.Model{{Model: "opus", Messages: 1}}, "transcript", noteAt)
		},
		topics: port.TopicUsage, units: []unit.Unit{u7},
	},
	{
		method: "usageStore.Attempted",
		seed:   seedTurn,
		call:   func(ctx context.Context, e *noteEnv) error { return e.db.Usage().Attempted(ctx, e.seq) },
		topics: port.TopicUsage, units: []unit.Unit{u7},
	},
	{
		method: "ledger.Save",
		call: func(ctx context.Context, e *noteEnv) error {
			it := noteItem(workflow.DecisionPending)
			return e.db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{it.Workflow}, Items: []workflow.Item{it}}, loopEvent(workflow.KindPlanned))
		},
		topics: port.TopicGate | port.TopicLoopEvents, units: []unit.Unit{u7, term1},
	},
	{
		method: "ledger.Record",
		call: func(ctx context.Context, e *noteEnv) error {
			it := noteItem(workflow.DecisionRun)
			return e.db.Ledger().Record(ctx, workflow.Run{Key: it.Key, Status: workflow.RunDone, At: noteAt}, it, loopEvent(workflow.KindStepRan))
		},
		topics: port.TopicLoopEvents, units: []unit.Unit{u7},
	},
	{
		method: "events.Advance",
		call:   func(ctx context.Context, e *noteEnv) error { return e.db.Events().Advance(ctx, "w", 1, noteAt) },
	},
	{
		method: "Classifications.Save",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Classifications().Save(ctx, u7, "w", "fp", json.RawMessage(`{}`), noteAt)
		},
	},
	{
		method: "Classifications.Spend",
		call: func(ctx context.Context, e *noteEnv) error {
			_, err := e.db.Classifications().Spend(ctx, noteAt, 1)
			return err
		},
	},
	{
		method: "entryStore.Apply",
		call: func(ctx context.Context, e *noteEnv) error {
			return e.db.Entries().Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: gtd.Entry{ID: "e-1", Kind: gtd.KindCapture,
				State: gtd.Inbox, Units: []unit.Unit{u7}, Origin: gtd.OriginUser, Created: noteAt, Updated: noteAt}}})
		},
		topics: port.TopicEntries, units: []unit.Unit{u7},
	},
	{
		method: "Fetches.Mark",
		call:   func(ctx context.Context, e *noteEnv) error { return e.db.Fetches().Mark(ctx, "ws1", noteAt) },
	},
}

func noteItem(d workflow.Decision) workflow.Item {
	k := workflow.Key{Workflow: "w", Unit: term1, Value: "v"}
	it := workflow.Item{ID: k.ID(), Key: k, Workflow: "w", Unit: term1, Decision: d, Mode: workflow.ModeApply, At: noteAt}
	if d == workflow.DecisionPending {
		it.Default, it.Deadline = workflow.OutcomeRun, noteAt.Add(time.Minute)
	}
	return it
}

// reads are the exported methods of the sqlite store types that note
// nothing: reads, and the handle's own plumbing.
var reads = []string{
	"DB.Close", "DB.R", "reader.QueryContext", "reader.QueryRowContext", "DB.Read", "DB.Snapshot", "DB.Write", "DB.Intakes", "DB.Usage", "DB.HeadlessSpent", "DB.DossierSpent", "DB.UnitEventKeys",
	"Store.Books", "Store.BookOf", "Store.UnitsOf", "Store.Open", "Store.WithTurns", "Store.Applied", "Store.Session", "Store.Sessions",
	"Store.LastStops", "Store.SessionOf", "Store.SessionOfHandle",
	"DB.Worktrees", "Worktrees.Worktree", "Worktrees.Worktrees",
	"Rounds.AgentHome", "Rounds.Load", "Rounds.Pause",
	"intakeStore.Intake", "intakeStore.Open",
	"usageStore.Pending",
	"DB.Ledger", "DB.Events", "DB.Classifications", "DB.Fetches",
	"ledger.Items", "ledger.Run", "ledger.Count",
	"events.Since", "events.Head", "events.Cursor",
	"Classifications.Output", "Classifications.Spent",
	"Fetches.Last",
	"DB.AgentTranscript", "DB.WorkflowActivity",
	"DB.Entries", "entryStore.Entries", "entryStore.EntriesSince", "entryStore.Entry", "entryStore.EntriesOf", "entryStore.Counts",
}

// notesAll are the write methods that note Change.All.
var notesAll = []string{"DB.Retain"}

func saveRounds(ctx context.Context, db *DB, change func(*round.Store)) error {
	r := NewRounds(db)
	st, err := r.Load(ctx)
	if err != nil {
		return err
	}
	change(st)
	return r.Save(ctx, st)
}

func seedWorktree(t *testing.T, e *noteEnv) {
	if err := e.db.Worktrees().Keep(context.Background(), wsdomain.Custody{Path: "/wt", Manager: "orca", At: noteAt}); err != nil {
		t.Fatal(err)
	}
}

func seedTurn(t *testing.T, e *noteEnv) {
	b, err := New(e.db).Save(context.Background(), turn.Book{Unit: u7, Turns: []turn.Turn{{ID: "r1", State: turn.Done, SentAt: noteAt, EndedAt: noteAt}}})
	if err != nil {
		t.Fatal(err)
	}
	e.seq = b.Turns[0].Seq
}

func openNoted(t *testing.T) (*noteEnv, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := Open(filepath.Join(t.TempDir(), "notes.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &noteEnv{db: db}, log
}

// noted runs fn and returns the merge of what it noted and how many notes.
func noted(log *change.Log, fn func() error) (port.Change, int, error) {
	from := log.Gen()
	err := fn()
	return log.Since(from, everyTopic), int(log.Gen() - from), err
}

func sortedUnits(us []unit.Unit) []unit.Unit {
	out := slices.Clone(us)
	slices.SortFunc(out, func(a, b unit.Unit) int {
		if a.Less(b) {
			return -1
		}
		if b.Less(a) {
			return 1
		}
		return 0
	})
	return out
}

func TestEveryWriteNotes(t *testing.T) {
	t.Parallel()
	for _, c := range writes {
		t.Run(c.method+c.name, func(t *testing.T) {
			t.Parallel()
			e, log := openNoted(t)
			if c.seed != nil {
				c.seed(t, e)
			}
			got, n, err := noted(log, func() error { return c.call(context.Background(), e) })
			if err != nil {
				t.Fatal(err)
			}
			if want := min(int(c.topics), 1); n != want {
				t.Fatalf("%d notes, want %d: %+v", n, want, got)
			}
			if got.Topics != c.topics || got.All {
				t.Errorf("noted topics %b all %v, want %b", got.Topics, got.All, c.topics)
			}
			if want := sortedUnits(c.units); !slices.Equal(got.Units, want) {
				t.Errorf("noted units %v, want %v", got.Units, want)
			}
		})
	}
}

func TestEveryExportedMethodIsReadOrNoted(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	types := []reflect.Type{reflect.TypeOf(db), reflect.TypeOf(New(db)), reflect.TypeOf(NewRounds(db))}
	types = append(types, accessorTypes(db)...)
	known := map[string]bool{}
	for _, m := range reads {
		known[m] = true
	}
	for _, c := range writes {
		known[c.method] = true
	}
	for _, m := range notesAll {
		known[m] = true
	}
	listed := map[string]bool{}
	for _, typ := range types {
		name := typ.Name()
		if typ.Kind() == reflect.Pointer {
			name = typ.Elem().Name()
		}
		for i := range typ.NumMethod() {
			m := name + "." + typ.Method(i).Name
			listed[m] = true
			if !known[m] {
				t.Errorf("%s is neither in reads nor in writes", m)
			}
		}
	}
	for m := range known {
		if !listed[m] {
			t.Errorf("%s is listed but no such method exists", m)
		}
	}
}

// accessorTypes are the pointer types of the values the *DB methods without
// arguments return, when those are defined in this package.
func accessorTypes(db *DB) []reflect.Type {
	pkg := reflect.TypeOf(db).Elem().PkgPath()
	v := reflect.ValueOf(db)
	var out []reflect.Type
	for i := range v.NumMethod() {
		m := v.Method(i)
		if m.Type().NumIn() != 0 || m.Type().NumOut() != 1 || m.Type().Out(0) == reflect.TypeFor[error]() {
			continue
		}
		got := m.Call(nil)[0]
		if got.Kind() == reflect.Interface {
			got = got.Elem()
		}
		typ := got.Type()
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.PkgPath() == pkg {
			out = append(out, reflect.PointerTo(typ))
		}
	}
	return out
}

func TestUntouchedWriteNotesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seed func(t *testing.T, e *noteEnv)
		call func(ctx context.Context, e *noteEnv) error
	}{
		{"a Write that touches nothing", nil, func(ctx context.Context, e *noteEnv) error {
			_, err := e.db.Write(ctx, func(tx *Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`)
				return err
			})
			return err
		}},
		{"Store.SaveFailure without events", nil, func(ctx context.Context, e *noteEnv) error {
			return New(e.db).SaveFailure(ctx)
		}},
		{"Rounds.Save with no change", nil, func(ctx context.Context, e *noteEnv) error {
			return saveRounds(ctx, e.db, func(*round.Store) {})
		}},
		{"intakeStore.Accept joining an open intake", func(t *testing.T, e *noteEnv) {
			if _, _, err := e.db.Intakes().Accept(context.Background(), newIntake(noteRef, "review")); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, e *noteEnv) error {
			i := newIntake(noteRef, "review")
			_, joined, err := e.db.Intakes().Accept(ctx, i, accepted(i))
			if err == nil && !joined {
				err = errors.New("not joined")
			}
			return err
		}},
		{"an event already stored", func(t *testing.T, e *noteEnv) {
			if err := New(e.db).SaveFailure(context.Background(), loopEvent("session_launched")); err != nil {
				t.Fatal(err)
			}
		}, func(ctx context.Context, e *noteEnv) error {
			got, err := e.db.Write(ctx, func(tx *Tx) error { return writeEvents(ctx, tx, []port.Event{loopEvent("session_launched")}) })
			if err == nil && !got.Empty() {
				err = fmt.Errorf("noted %+v", got)
			}
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e, log := openNoted(t)
			if c.seed != nil {
				c.seed(t, e)
			}
			got, n, err := noted(log, func() error { return c.call(context.Background(), e) })
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("%d notes: %+v", n, got)
			}
		})
	}
}

func TestRollbackNotesNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seed func(t *testing.T, e *noteEnv)
		call func(ctx context.Context, e *noteEnv) error
	}{
		{"a Write whose fn fails", nil, func(ctx context.Context, e *noteEnv) error {
			_, err := e.db.Write(ctx, func(tx *Tx) error {
				tx.Touch(port.TopicMeta, u7)
				tx.TouchAll(port.TopicPRs)
				return errors.New("rolled back")
			})
			return err
		}},
		{"a book with a turn the store lacks", nil, func(ctx context.Context, e *noteEnv) error {
			_, err := New(e.db).Save(ctx, turn.Book{Unit: u7, Turns: []turn.Turn{{Seq: 42, ID: "r1", State: turn.Sent, SentAt: noteAt}}},
				port.Event{Unit: unit.Unit{Provider: "slack", Key: "t1"}, Kind: "round_sent", At: noteAt})
			return err
		}},
		{"usage of a turn that is not stored", nil, func(ctx context.Context, e *noteEnv) error {
			return e.db.Usage().SaveUsage(ctx, 999, nil, "transcript", noteAt, loopEvent("turn_usage"))
		}},
		{"a write the database refuses", func(t *testing.T, e *noteEnv) {
			seedTurn(t, e)
		}, func(ctx context.Context, e *noteEnv) error {
			_, err := e.db.Write(ctx, func(tx *Tx) error {
				if err := writeEvents(ctx, tx, []port.Event{loopEvent("round_sent")}); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO turns (seq) VALUES (?)`, e.seq)
				return err
			})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e, log := openNoted(t)
			if c.seed != nil {
				c.seed(t, e)
			}
			got, n, err := noted(log, func() error { return c.call(context.Background(), e) })
			if err == nil {
				t.Fatal("the write did not fail")
			}
			if n != 0 {
				t.Fatalf("%d notes after a rollback: %+v", n, got)
			}
		})
	}
}

// TestSubscriberWriteDeadlockDetected: a subscriber that writes the store
// runs under the write lock; it panics instead of hanging every write, and
// the log drops it.
func TestSubscriberWriteDeadlockDetected(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var logged []string
	log := change.NewLog(change.DefaultWindow, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	db, err := Open(filepath.Join(t.TempDir(), "notes.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	calls := 0
	log.Subscribe(func(port.Change) {
		calls++
		_ = db.SaveAgentSession(ctx, AgentSession{ID: "from-a-subscriber", FirstSeen: noteAt})
	})
	done := make(chan error, 1)
	go func() {
		done <- New(db).SaveApplied(ctx, "orca", view.Applied{Unit: u7, Role: view.ReviewRole, At: noteAt})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a write with a writing subscriber hung")
	}
	mu.Lock()
	got := strings.Join(logged, "\n")
	mu.Unlock()
	if !strings.Contains(got, "sqlite: Write inside Write") {
		t.Fatalf("the log reported %q", got)
	}
	if err := db.SaveAgentSession(ctx, AgentSession{ID: "a2", FirstSeen: noteAt}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the writing subscriber was called %d times, want once and then dropped", calls)
	}
	if n := rowsOf(t, db, `SELECT agent_session FROM agent_sessions`); !slices.Equal(n, []string{"a2"}) {
		t.Fatalf("agent sessions %v: the subscriber's write went through", n)
	}
}
