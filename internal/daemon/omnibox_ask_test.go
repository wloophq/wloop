package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/i18n"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// askAgent answers reply or err and keeps each call and the tasks.json it read.
type askAgent struct {
	reply string
	err   error
	asks  []turns.Ask
	tasks []api.TaskView
}

func (a *askAgent) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	a.asks = append(a.asks, q)
	if len(q.Dirs) == 1 {
		if b, err := os.ReadFile(filepath.Join(q.Dirs[0], "tasks.json")); err == nil {
			_ = json.Unmarshal(b, &a.tasks)
		}
	}
	return turns.Answer{JSON: []byte(a.reply)}, a.err
}

type askRig struct {
	*tasksRig
	agent *askAgent
	asker *omnibox.Asker
	spent float64
}

func newAskRig(t *testing.T, reply string) *askRig {
	t.Helper()
	r := &askRig{agent: &askAgent{reply: reply}}
	raw, err := os.ReadFile("../../profiles/default/" + omnibox.WrapperFile)
	if err != nil {
		t.Fatal(err)
	}
	w, err := omnibox.ParseWrapper(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.tasksRig = newTasksRig(t, func(d *Deps) {
		r.asker = &omnibox.Asker{Agent: r.agent, Dir: t.TempDir(),
			Budget: &turns.Budget{Caps: turns.BudgetCaps{PerCallUSD: 0.25, PerDayUSD: 1}, Clock: fixedClock(tasksNow),
				Spent: func(context.Context, time.Time) (float64, error) { return r.spent, nil }},
			Record: func(ctx context.Context, cost float64) error {
				return sqlite.New(r.db).RecordEvents(ctx, port.Event{Kind: omnibox.KindCall, Key: "c" + strconv.Itoa(len(r.agent.asks)), At: tasksNow,
					Data: strconv.FormatFloat(cost, 'f', -1, 64)})
			},
			Allowed: func(omnibox.AskOp) (string, map[string]string) { return "", nil },
			Wrapper: func() (omnibox.Wrapper, error) { return w, nil }}
		d.OmniboxAsk = r.asker
	})
	return r
}

func (r *askRig) ask(t *testing.T, body string) *api.OmniboxAnswer {
	t.Helper()
	rec := post(t, r.s, api.OmniboxAskPath, body)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got api.OmniboxAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return &got
}

// rowCounts counts the rows of every table of the store.
func rowCounts(t *testing.T, db *sqlite.DB) map[string]int {
	t.Helper()
	ctx := context.Background()
	rows, err := db.R().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err := db.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+n+`"`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[n] = c
	}
	return out
}

func callEvents(t *testing.T, db *sqlite.DB) int {
	t.Helper()
	var n int
	if err := db.R().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE kind = ?`, omnibox.KindCall).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var askUnit = unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}

func TestAskAnswersFromTheReadModel(t *testing.T) {
	r := newAskRig(t, `{"answer":{"text":"One waits on you.","tasks":["t-aaaa1111","t-nope"]}}`)
	r.put(t, task.Task{ID: "t-aaaa1111", Title: "Review the retries", State: "next", Category: task.CategoryReady, Units: []unit.Unit{askUnit}})
	got := r.ask(t, `{"text":"what waits on me?"}`)
	if got.Answer == nil || len(got.Answer.Tasks) != 1 || got.Answer.Tasks[0].ID != "t-aaaa1111" || got.Answer.Tasks[0].Title != "Review the retries" {
		t.Fatalf("%+v", got.Answer)
	}
	if len(r.agent.tasks) != 1 || r.agent.tasks[0].ID != "t-aaaa1111" || len(r.agent.tasks[0].UnitLabels) != 1 {
		t.Fatalf("the agent read %+v", r.agent.tasks)
	}
	if q := r.agent.asks[0]; !strings.Contains(q.Prompt, "what waits on me?") || len(q.Tools) == 0 {
		t.Fatalf("%+v", q)
	}
}

func TestAskOverCap(t *testing.T) {
	r := newAskRig(t, `{"answer":{"text":"a","tasks":[]}}`)
	r.spent = 1
	rec := post(t, r.s, api.OmniboxAskPath, `{"text":"what is stalled?"}`)
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), api.OmniboxAskOverCap) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(r.agent.asks) != 0 || callEvents(t, r.db) != 0 {
		t.Fatalf("%d calls, %d cost events", len(r.agent.asks), callEvents(t, r.db))
	}
}

func TestAskAgentFails(t *testing.T) {
	r := newAskRig(t, "")
	r.agent.err = errors.New("the model is overloaded")
	before := rowCounts(t, r.db)
	rec := post(t, r.s, api.OmniboxAskPath, `{"text":"what is stalled?"}`)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "the model is overloaded") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertOnlyTheCost(t, r, before)
}

func TestAskPastItsDeadlineIsAGatewayTimeout(t *testing.T) {
	r := newAskRig(t, "")
	r.agent.err = context.DeadlineExceeded
	rec := post(t, r.s, api.OmniboxAskPath, `{"text":"x"}`)
	if rec.Code != 504 || !strings.Contains(rec.Body.String(), api.OmniboxAskTimedOut) || callEvents(t, r.db) != 1 {
		t.Fatalf("%d %s, %d cost events", rec.Code, rec.Body, callEvents(t, r.db))
	}
}

func TestAskUnfitAnswerIsABadGateway(t *testing.T) {
	r := newAskRig(t, `{"answer":{"text":"a","tasks":[]},"proposal":{"summary":"s","operations":[]}}`)
	if rec := post(t, r.s, api.OmniboxAskPath, `{"text":"x"}`); rec.Code != 502 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAskStoresNothing(t *testing.T) {
	r := newAskRig(t, `{"proposal":{"summary":"Close it.","operations":[{"op":"verb","task":"t-aaaa1111","verb":"done","reason":"merged"}]}}`)
	r.put(t, task.Task{ID: "t-aaaa1111", Title: "Review the retries", State: "next", Category: task.CategoryReady})
	before := rowCounts(t, r.db)
	got := r.ask(t, `{"text":"close the tasks of merged PRs"}`)
	if got.Proposal == nil || len(got.Proposal.Operations) != 1 {
		t.Fatalf("%+v", got)
	}
	assertOnlyTheCost(t, r, before)
}

// assertOnlyTheCost checks that the store holds what it held before but one cost event.
func assertOnlyTheCost(t *testing.T, r *askRig, before map[string]int) {
	t.Helper()
	after := rowCounts(t, r.db)
	for name, n := range after {
		want := before[name]
		if name == "events" {
			want++
		}
		if n != want {
			t.Errorf("table %s holds %d rows, want %d", name, n, want)
		}
	}
	if callEvents(t, r.db) != 1 {
		t.Errorf("%d cost events, want 1", callEvents(t, r.db))
	}
}

func TestAskWithoutTextAsksNothing(t *testing.T) {
	r := newAskRig(t, `{"answer":{"text":"a","tasks":[]}}`)
	rec := post(t, r.s, api.OmniboxAskPath, `{"text":"  "}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), api.OmniboxAskEmpty) || len(r.agent.asks) != 0 {
		t.Fatalf("%d %s, %d calls", rec.Code, rec.Body, len(r.agent.asks))
	}
}

type restrictedLess struct{ askAgent }

func (*restrictedLess) Ready(context.Context, turns.Ask) error { return turns.ErrNoRestricted }

func TestAskWithNoAgentThatCanAnswerIsUnavailable(t *testing.T) {
	r := newAskRig(t, "")
	r.asker.Agent = &restrictedLess{}
	rec := post(t, r.s, api.OmniboxAskPath, `{"text":"x"}`)
	want := i18n.Interpolate(api.OmniboxAskNoAgent, map[string]any{"error": turns.ErrNoRestricted.Error()})
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), want) || callEvents(t, r.db) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAskUnwiredIsUnavailable(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, api.OmniboxAskPath, `{"text":"x"}`)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), api.OmniboxAskUnwired) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAskErrorsReadInTheLoadedLocale(t *testing.T) {
	l := *bundledLoaded()
	l.Strings = map[string]i18n.Forms{api.OmniboxAskFailed: {"other": "O agente não respondeu: {error}"}}
	r := newAskRig(t, "")
	r.s.deps.Intents = func() *intentload.Loaded { return &l }
	r.agent.err = errors.New("boom")
	rec := post(t, r.s, api.OmniboxAskPath, `{"text":"x"}`)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "O agente não respondeu: boom") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAskSearchAndOtherRoutesKeepAnsweringWhileTheAgentIsSlow(t *testing.T) {
	r := newAskRig(t, `{"answer":{"text":"a","tasks":[]}}`)
	release := make(chan struct{})
	started := make(chan struct{})
	r.asker.Agent = slowAgent{started: started, release: release}
	done := make(chan int, 1)
	go func() { done <- post(t, r.s, api.OmniboxAskPath, `{"text":"x"}`).Code }()
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); <-done })
	<-started
	if code := get(t, r.s, api.TasksPath+"?view="+r.s.viewsCatalog().Views[0].ID, nil); code != 200 {
		t.Fatalf("the tasks route answered %d while an ask ran", code)
	}
	once.Do(func() { close(release) })
	if code := <-done; code != 200 {
		t.Fatalf("the ask answered %d", code)
	}
	done <- 200
}

func TestAClientThatGoesAwayCancelsTheAsk(t *testing.T) {
	r := newAskRig(t, "")
	started := make(chan struct{})
	r.asker.Agent = slowAgent{started: started, release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := apiRequest("POST", api.OmniboxAskPath, strings.NewReader(`{"text":"x"}`)).WithContext(ctx)
		r.s.Handler().ServeHTTP(rec, req)
		done <- rec.Code
	}()
	<-started
	cancel()
	if code := <-done; code != 502 {
		t.Fatalf("the abandoned ask answered %d", code)
	}
	if n := callEvents(t, r.db); n != 1 || r.asker.Budget.Reserved() != 0 {
		t.Fatalf("%d cost events, %v reserved; want the cap recorded and nothing held", n, r.asker.Budget.Reserved())
	}
}

type slowAgent struct{ started, release chan struct{} }

func (a slowAgent) Ask(ctx context.Context, _ turns.Ask) (turns.Answer, error) {
	close(a.started)
	select {
	case <-a.release:
	case <-ctx.Done():
		return turns.Answer{}, ctx.Err()
	}
	return turns.Answer{JSON: []byte(`{"answer":{"text":"a","tasks":[]}}`)}, nil
}

func BenchmarkOmniboxSnapshot(b *testing.B) {
	r := newTasksRig(b, nil)
	es := make([]task.Task, 5000)
	for i := range es {
		es[i] = task.Task{ID: task.ID(fmt.Sprintf("t-%08x", i)), Title: fmt.Sprintf("Task %d", i), State: "next", Category: task.CategoryReady,
			Units: []unit.Unit{{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", i)}}}
	}
	r.put(b, es...)
	ctx := context.Background()
	for b.Loop() {
		if snap, _, err := r.s.omniboxSnapshot(ctx, r.g); err != nil || len(snap.Tasks) != 5000 {
			b.Fatalf("%d tasks: %v", len(snap.Tasks), err)
		}
	}
}

func TestAskDropCodesAreTheAPIs(t *testing.T) {
	for core, wire := range map[string]string{
		omnibox.DropUnknownOperation:    api.OmniboxDropUnknownOperation,
		omnibox.DropIncompleteOperation: api.OmniboxDropIncompleteOperation,
		omnibox.DropUnknownTask:         api.OmniboxDropUnknownTask,
		omnibox.DropUnknownUnit:         api.OmniboxDropUnknownUnit,
		omnibox.DropOutsideVocabulary:   api.OmniboxDropOutsideVocabulary,
	} {
		if core != wire {
			t.Errorf("the core drops %q and the API words %q", core, wire)
		}
	}
}

func TestAskServesTheDroppedOperations(t *testing.T) {
	r := newAskRig(t, `{"proposal":{"summary":"s","operations":[{"op":"verb","task":"t-gone","verb":"done","reason":"r"}]}}`)
	got := r.ask(t, `{"text":"close t-gone"}`)
	if len(got.Proposal.Operations) != 0 || len(got.Dropped) != 1 || got.Dropped[0].Why != api.OmniboxDropUnknownTask ||
		got.Dropped[0].Operation.Task != "t-gone" || got.Dropped[0].Params["id"] != "t-gone" {
		t.Fatalf("%+v", got)
	}
}
