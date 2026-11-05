package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
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
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// liveShape is a store shaped as a daemon that has run for months holds it: most tasks ended, every task linked
// to a pull request or a Slack thread whose last snapshot the plugin reported, threads with items and messages
// not yet extracted.
type liveShape struct {
	tasks              int
	inbox, ready, done int // per 50 tasks; the rest are dropped
}

var monthsOld = liveShape{tasks: 1200, inbox: 4, ready: 7, done: 28}

// seed writes s into db and returns how many tasks the inbox lists.
func (s liveShape) seed(tb testing.TB, db *sqlite.DB) int {
	tb.Helper()
	ctx := context.Background()
	snaps := psource.NewSnapshots(db.Snapshots())
	pad := strings.Repeat("x", 4096)
	at := testNow.Add(-10 * 24 * time.Hour)
	reported := map[unit.Unit]src.Snapshot{}
	var its []items.Item
	var changes []task.Change
	inbox := 0
	for i := range s.tasks {
		e := task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), Title: "Task " + strconv.Itoa(i+1), Origin: task.OriginUser,
			Created: at.Add(time.Duration(i) * time.Minute), Updated: at.Add(time.Duration(i) * time.Minute)}
		switch b := i % 50; {
		case b < s.inbox:
			e.State, e.Category = "inbox", task.CategoryInbox
			inbox++
		case b < s.inbox+s.ready:
			e.State, e.Category = "next", task.CategoryReady
		case b < s.inbox+s.ready+s.done:
			e.State, e.Category = "done", task.CategoryDone
		default:
			e.State, e.Category = "trash", task.CategoryDropped
		}
		if i%2 == 0 {
			ref := pr.Ref{Owner: "acme", Repo: "api", Number: i + 1}
			p := dinv.PR{Ref: ref, Title: "Change " + strconv.Itoa(i+1), Author: "ana", State: "OPEN", HeadOid: fmt.Sprintf("%040x", i+1),
				HeadBranch: "feat/" + strconv.Itoa(i+1), BaseBranch: "main", ChecksState: "SUCCESS", ReviewDecision: "REVIEW_REQUIRED",
				URL: "https://github.com/acme/api/pull/" + strconv.Itoa(i+1), CreatedAt: at, UpdatedAt: at, LastSeenAt: testNow,
				Reasons: []string{"review_requested"}}
			snap := ghreporttest.Snapshot(ghreporttest.Env{Viewer: "me", Now: testNow}, ghreporttest.Input{PR: p})
			reported[ref.Unit()] = snap
			e.Units = []unit.Unit{ref.Unit()}
		} else {
			u := slackThreadUnit("T0001", "C0001", fmt.Sprintf("17000%05d.000100", i))
			snap := src.Snapshot{Unit: psource.Wire(u), Rev: 1, At: testNow, Data: []byte(`{"topic":"t","pad":"` + pad + `"}`),
				Card:       &src.Card{Label: "Thread", Title: "Thread " + strconv.Itoa(i), State: "open", Why: "asked", Since: at},
				Facts:      []src.Fact{{Name: "slack.live_task", Value: src.Bool(true)}, {Name: "slack.where", Value: src.String("general")}},
				Extraction: &src.ExtractionHealth{Pending: 2, Oldest: testNow.Add(-time.Hour)}}
			reported[u] = snap
			e.Units = []unit.Unit{u}
			for j := range 2 {
				it := items.Item{ID: fmt.Sprintf("i%d-%d", i, j), Unit: u, Kind: items.Request, Summary: "ask", Origin: unit.Anchor{Unit: u, Ref: fmt.Sprintf("m:%d-%d", i, j)},
					Requester: items.Person{Key: "slack:U0009"}, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
					Provenance: items.Extracted, WaitingOn: []items.Person{{Key: "slack:U0001"}}, CreatedAt: at, UpdatedAt: at}
				its = append(its, it)
				if j == 0 {
					e.Items = append(e.Items, task.ItemRef{Unit: u, Item: it.ID})
				}
			}
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
	return inbox
}

// liveDaemon is te's daemon over a store shaped as s, wired as serve wires it, and the inbox's size.
func liveDaemon(tb testing.TB, s liveShape) (http.Handler, int) {
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
	inbox := s.seed(tb, db)
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
	return srv.Handler(), inbox
}

func tasksPage(tb testing.TB, h http.Handler, view string) {
	tb.Helper()
	req := httptest.NewRequest("GET", api.TasksPath+"?view="+view, nil)
	req.Header.Set(api.Header, api.Version)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		tb.Fatalf("%d: %s", rec.Code, rec.Body)
	}
}

// BenchmarkInboxOfALiveStore times a full page of the inbox over a store shaped as a daemon's after months of
// use, wired as serve wires it: every task's facts read its units' snapshots, items, links and placement.
func BenchmarkInboxOfALiveStore(b *testing.B) {
	h, _ := liveDaemon(b, monthsOld)
	tasksPage(b, h, "inbox")
	for b.Loop() {
		tasksPage(b, h, "inbox")
	}
}
