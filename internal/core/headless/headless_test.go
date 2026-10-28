package headless_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

var (
	t0     = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	thread = unit.Unit{Provider: "thread", Key: "C1/1700000000.000100"}
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// asker is a scripted turns.Asker: it answers each call with the next script entry and keeps the asks.
type asker struct {
	mu     sync.Mutex
	script []scripted
	asks   []turns.Ask
}

type scripted struct {
	json string
	cost float64
	err  error
}

func (a *asker) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asks = append(a.asks, q)
	if len(a.script) == 0 {
		return turns.Answer{}, errors.New("no scripted answer")
	}
	s := a.script[0]
	a.script = a.script[1:]
	if s.err != nil {
		return turns.Answer{}, s.err
	}
	cost := s.cost
	return turns.Answer{JSON: []byte(s.json), Model: "claude-test", CostUSD: &cost, Tokens: &dusage.Tokens{Input: 1200, Output: 300}}, nil
}

type builder struct{}

func (builder) Instructions(_ context.Context, u unit.Unit, i turn.Intent, its []string) (string, error) {
	return "# Act on " + u.Key + " for " + string(i) + " on " + strings.Join(its, ","), nil
}

type placer struct{ dir string }

func (p placer) Checkout(context.Context, unit.Unit, string) (string, bool, error) {
	return p.dir, p.dir != "", nil
}

type effects struct{}

func (effects) Publish(context.Context, draft.Draft) (draft.Evidence, error) {
	return draft.Evidence{}, errors.New("never published in this test")
}

func (effects) Verify(context.Context, draft.Draft, time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

type rig struct {
	db     *sqlite.DB
	store  *sqlite.Store
	turns  *turns.Service
	ledger *turnstest.Ledger
	outbox *outbox.Service
	asker  *asker
	clock  *clock
	sess   *turnstest.Sessions
	svc    *headless.Service
}

func newRig(t *testing.T, script ...scripted) *rig {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{db: db, store: sqlite.New(db), ledger: &turnstest.Ledger{Open: map[unit.Unit][]items.Item{thread: {{ID: "i1", Unit: thread}, {ID: "i2", Unit: thread}}}},
		asker: &asker{script: script}, clock: &clock{t: t0}, sess: turnstest.NewSessions()}
	r.turns = r.newTurns()
	r.outbox = &outbox.Service{Store: db.Drafts(), Effects: func(unit.ProviderID) (port.Effects, error) { return effects{}, nil }, Clock: r.clock}
	reg := turnstest.Intents()
	r.svc = &headless.Service{Turns: r.turns, Build: builder{}, Place: placer{dir: "/work/checkout"}, Asker: r.asker, Ledger: r.ledger, Outbox: r.outbox,
		Intents: func(n turn.Intent) (dintent.Def, bool) { return reg.Get(dintent.Name(n)) },
		Default: func(p unit.ProviderID) (dintent.Def, bool) { return reg.ByRole("headless", p) },
		Caps:    headless.Caps{PerCallUSD: 0.5, PerDayUSD: 5}, Spent: r.spent, Clock: r.clock, Go: func(run func()) { run() }, Log: t.Logf}
	w := bundledWrapper(t)
	r.svc.Wrapper = func() (headless.Wrapper, error) { return w, nil }
	r.turns.Headless = r.svc.Again
	return r
}

// newTurns is a turns service over the rig's store, as a second daemon over the same store would build it.
func (r *rig) newTurns() *turns.Service {
	return &turns.Service{Intents: turnstest.Intents(), Store: r.store, Registry: r.store, Sessions: r.sess, Launcher: noLauncher{},
		Clock: r.clock, Command: "loop", FastJob: 2 * time.Minute, Items: r.ledger, Usage: r.db.Usage()}
}

func (r *rig) spent(ctx context.Context, day time.Time) (float64, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return r.db.HeadlessSpent(ctx, from, from.AddDate(0, 0, 1))
}

type noLauncher struct{}

func (noLauncher) Launch(context.Context, turns.Launch) error {
	return errors.New("a headless turn never launches a session")
}

func (r *rig) book(t *testing.T, u unit.Unit) turn.Book {
	t.Helper()
	books, err := r.store.Books(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return books[u]
}

func (r *rig) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := r.db.R().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func answerJSON(t *testing.T, rec record.Record, drafts ...map[string]any) string {
	t.Helper()
	if drafts == nil {
		drafts = []map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"record": rec, "drafts": drafts, "log": ""})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var done = record.Record{State: record.Done, Summary: "replied to the open question"}

func TestHeadlessRunIsATurn(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done), cost: 0.12})
	got, err := r.svc.Run(context.Background(), headless.Request{Unit: thread, Items: []string{"i1"}, Guidance: "keep it short", Trigger: turn.ByHand})
	if err != nil {
		t.Fatal(err)
	}
	b := r.book(t, thread)
	if len(b.Turns) != 1 || len(b.Records) != 1 {
		t.Fatalf("book %+v", b)
	}
	tu := b.Turns[0]
	if tu.Seq != got.Seq || tu.Runner != turn.RunnerHeadless || tu.Intent != turn.Intent(dintent.Act) || tu.ID == "" || tu.Trigger != turn.ByHand ||
		tu.Guidance != "keep it short" || tu.State != turn.Done || tu.Session != "" || tu.SentTo != "" || !slices.Equal(turns.ItemsOf(tu), []string{"i1"}) {
		t.Fatalf("turn %+v", tu)
	}
	if b.Records[0].Record.Summary != done.Summary || got.Record.Summary != done.Summary || got.CostUSD == nil || *got.CostUSD != 0.12 {
		t.Fatalf("record %+v result %+v", b.Records[0], got)
	}
	var cost float64
	var in, out int
	var source string
	if err := r.db.R().QueryRowContext(context.Background(), `SELECT u.cost_usd, u.input_tokens, u.output_tokens, t.usage_source FROM turn_usage u JOIN turns t ON t.seq = u.turn_seq WHERE t.seq = ?`, got.Seq).
		Scan(&cost, &in, &out, &source); err != nil {
		t.Fatal(err)
	}
	if cost != 0.12 || in != 1200 || out != 300 || source != turns.UsageHeadless {
		t.Fatalf("usage %v %d %d %q", cost, in, out, source)
	}
	if n := r.count(t, `SELECT count(*) FROM events WHERE kind LIKE 'session_%'`); n != 0 {
		t.Fatalf("%d session events", n)
	}
	if n := r.count(t, `SELECT count(*) FROM events WHERE kind IN ('turn_sent', 'turn_started', 'turn_ended')`); n != 3 {
		t.Fatalf("%d turn events, want sent, started, ended", n)
	}
}

func TestOneHeadlessRunPerUnit(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)})
	ctx := context.Background()
	if _, err := r.turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: thread, Intent: turn.Intent(dintent.Act), Turn: "first", Trigger: turn.ByHand}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread}); !errors.Is(err, headless.ErrBusy) {
		t.Fatalf("second run: %v", err)
	}
	other := &headless.Service{Turns: r.newTurns(), Build: r.svc.Build, Place: r.svc.Place, Asker: r.svc.Asker, Ledger: r.svc.Ledger, Outbox: r.svc.Outbox,
		Intents: r.svc.Intents, Default: r.svc.Default, Caps: r.svc.Caps, Spent: r.svc.Spent, Clock: r.svc.Clock, Wrapper: r.svc.Wrapper}
	if _, err := other.Run(ctx, headless.Request{Unit: thread}); !errors.Is(err, headless.ErrBusy) {
		t.Fatalf("a second service: %v", err)
	}
	if len(r.asker.asks) != 0 {
		t.Fatalf("%d calls", len(r.asker.asks))
	}
}

func TestHeadlessTurnInterruptedByRestart(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	seq, err := r.turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: thread, Intent: turn.Intent(dintent.Act), Turn: "cut", Trigger: turn.ByHand, Items: []string{"i1"}})
	if err != nil {
		t.Fatal(err)
	}
	restarted := r.newTurns()
	n, err := restarted.EndInterruptedHeadless(ctx, 0.5)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	b := r.book(t, thread)
	if b.Turns[0].State != turn.Failed || b.Records[0].TurnSeq != seq || b.Records[0].Record.Summary != turns.Interrupted || len(r.asker.asks) != 0 {
		t.Fatalf("book %+v asks %d", b, len(r.asker.asks))
	}
	if n, err := restarted.EndInterruptedHeadless(ctx, 0.5); err != nil || n != 0 {
		t.Fatalf("again: %d %v", n, err)
	}
}

func TestSessionPathsSkipHeadlessTurns(t *testing.T) {
	r := newRig(t)
	r.turns.Usage = nil
	ctx := context.Background()
	started, err := r.turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: thread, Intent: turn.Intent(dintent.Act), Turn: "live", Trigger: turn.ByHand})
	if err != nil {
		t.Fatal(err)
	}
	other := unit.Unit{Provider: "thread", Key: "C1/1700000000.000200"}
	if _, err := r.turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: other, Intent: turn.Intent(dintent.Act), Turn: "ended", Trigger: turn.ByHand}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.turns.EndHeadless(ctx, mustSeq(t, r, other), turns.HeadlessEnd{Err: errors.New("boom")}); err != nil {
		t.Fatal(err)
	}
	before := r.book(t, thread)
	r.clock.advance(3 * time.Hour)
	if _, err := r.turns.Reconcile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.turns.Reconcile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := r.turns.Deliver(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if err := r.turns.Census(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.turns.CloseRefusal(ctx, "term_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.turns.RecordTurnEnd(ctx, turns.RecordRequest{Turn: "live", Record: done}); err == nil {
		t.Fatal("a turn_end without a handle recorded a headless turn")
	}
	after := r.book(t, thread)
	if after.Turns[0].Seq != started || after.Turns[0].State != turn.Started || len(after.Records) != len(before.Records) {
		t.Fatalf("a session path touched the headless turn: %+v", after)
	}
	pending, err := r.db.Usage().Pending(ctx, t0.Add(-time.Hour), 100)
	if err != nil || len(pending) != 0 {
		t.Fatalf("the usage job lists %+v %v", pending, err)
	}
}

func mustSeq(t *testing.T, r *rig, u unit.Unit) int64 {
	t.Helper()
	b := r.book(t, u)
	return b.Turns[len(b.Turns)-1].Seq
}

func TestIntentMustAllowHeadless(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)})
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	if _, err := r.svc.Run(context.Background(), headless.Request{Unit: pr, Intent: turn.Intent(dintent.Review)}); !errors.Is(err, headless.ErrNotHeadless) {
		t.Fatalf("review: %v", err)
	}
	if len(r.asker.asks) != 0 || len(r.book(t, pr).Turns) != 0 {
		t.Fatal("a refused run stored or called")
	}
}

func TestHeadlessRunsWhereItIsPlaced(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)}, scripted{json: answerJSON(t, done)})
	ctx := context.Background()
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread}); err != nil {
		t.Fatal(err)
	}
	r.svc.Place = placer{}
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread}); err != nil {
		t.Fatal(err)
	}
	placed, none := r.asker.asks[0], r.asker.asks[1]
	if !slices.Equal(placed.Dirs, []string{"/work/checkout"}) || !slices.Equal(placed.Tools, turns.ReadOnlyTools) || placed.MaxCostUSD != 0.5 || placed.Tier != turns.TierBalanced {
		t.Fatalf("placed ask %+v", placed)
	}
	if len(none.Dirs) != 0 || len(none.Tools) != 0 {
		t.Fatalf("unplaced ask dirs %v tools %v", none.Dirs, none.Tools)
	}
}

func TestHeadlessPromptIsReadOnly(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)})
	if _, err := r.svc.Run(context.Background(), headless.Request{Unit: thread, Items: []string{"i1", "i2"}, Guidance: "ask for the date"}); err != nil {
		t.Fatal(err)
	}
	ask := r.asker.asks[0]
	for _, want := range []string{"# Act on " + thread.Key + " for act on i1,i2", "/work/checkout", "read-only", "post nothing", headless.Schema(), "ask for the date", "`i1`, `i2`"} {
		if !strings.Contains(ask.Prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, ask.Prompt)
		}
	}
	if ask.Schema != headless.Schema() || !json.Valid([]byte(ask.Schema)) {
		t.Fatalf("schema %q", ask.Schema)
	}
}

func resolveOp() items.Op {
	return items.Op{Op: items.OpResolve, Item: "i1", Anchor: unit.Anchor{Ref: "C1/1700000000.000300"}, Resolution: items.Answered}
}

func reply(text string, its ...string) map[string]any {
	return map[string]any{"kind": "reply", "text": text, "items": its}
}

func TestHeadlessResultAppliesOpsAndDrafts(t *testing.T) {
	rec := done
	rec.Ledger = []items.Op{resolveOp()}
	r := newRig(t, scripted{json: answerJSON(t, rec, reply("Friday works.", "i1"))})
	r.ledger.Propose = map[int]bool{0: true}
	got, err := r.svc.Run(context.Background(), headless.Request{Unit: thread, Items: []string{"i1"}, Workflow: "triage"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ledger.Calls) != 1 || r.ledger.Calls[0].Provenance != items.Turn || len(r.ledger.Calls[0].Ops) != 1 || r.ledger.Calls[0].Ops[0].Unit != thread {
		t.Fatalf("ledger calls %+v", r.ledger.Calls)
	}
	drafts, err := r.outbox.List(context.Background(), outbox.Filter{})
	if err != nil || len(drafts) != 1 || len(got.Drafts) != 1 {
		t.Fatalf("drafts %+v %v", drafts, err)
	}
	d := drafts[0]
	want := draft.Origin{Kind: draft.OriginTurn, TurnSeq: got.Seq, Intent: string(dintent.Act), Workflow: "triage"}
	if d.Unit != thread || d.Origin != want || d.State != draft.Proposed || d.Approval.Mode != draft.Wait || !slices.Equal(d.Items, []string{"i1"}) {
		t.Fatalf("draft %+v", d)
	}
	if b := r.book(t, thread); b.Turns[0].Workflow != "triage" {
		t.Fatalf("turn workflow %q", b.Turns[0].Workflow)
	}
}

func TestUnfitAnswerAppliesNothing(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":                   "{nope",
		"unknown field":              `{"record":{"state":"done","summary":"s"},"drafts":[],"log":"","extra":1}`,
		"a draft names its approval": `{"record":{"state":"done","summary":"s"},"drafts":[{"kind":"reply","text":"hi","approval":{"mode":"send"}}],"log":""}`,
		"a draft names its origin":   `{"record":{"state":"done","summary":"s"},"drafts":[{"kind":"reply","text":"hi","origin":{"kind":"auto","turn_seq":9}}],"log":""}`,
		"bad record":                 `{"record":{"state":"settled","summary":"s","ledger":[{"op":"resolve","item":"i1","anchor":{"ref":"x"}}]},"drafts":[{"kind":"reply","text":"hi"}],"log":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, scripted{json: raw})
			got, err := r.svc.Run(context.Background(), headless.Request{Unit: thread})
			if err != nil {
				t.Fatal(err)
			}
			b := r.book(t, thread)
			if b.Turns[0].State != turn.Failed || !strings.Contains(b.Records[0].Record.Summary, "does not fit") || got.Record.State != record.Failed {
				t.Fatalf("book %+v", b)
			}
			drafts, _ := r.outbox.List(context.Background(), outbox.Filter{})
			if len(r.ledger.Calls) != 0 || len(drafts) != 0 {
				t.Fatalf("applied %+v drafts %d", r.ledger.Calls, len(drafts))
			}
		})
	}
}

func TestHeadlessDraftLimits(t *testing.T) {
	ds := []map[string]any{reply("one"), reply("two"), reply("three"), reply("four")}
	ds[1]["unit"] = "thread:C9/1700000000.000999"
	ds[2] = reply("three", "i9")
	r := newRig(t, scripted{json: answerJSON(t, done, ds...)})
	got, err := r.svc.Run(context.Background(), headless.Request{Unit: thread})
	if err != nil {
		t.Fatal(err)
	}
	drafts, _ := r.outbox.List(context.Background(), outbox.Filter{})
	if len(drafts) != 1 || drafts[0].Text != "one" || drafts[0].Unit != thread {
		t.Fatalf("drafts %+v", drafts)
	}
	next := r.book(t, thread).Records[0].Record.Next
	for _, want := range []string{"drafts[1]", "another unit", "drafts[2]", "item i9", "drafts[3]", "over the limit of 3"} {
		if !strings.Contains(next, want) || !strings.Contains(got.Record.Next, want) {
			t.Errorf("the record does not name %q: %q", want, next)
		}
	}
}

func TestHeadlessDailyCap(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done), cost: 5}, scripted{json: answerJSON(t, done)})
	ctx := context.Background()
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread}); !errors.Is(err, headless.ErrOverCap) {
		t.Fatalf("over the cap: %v", err)
	}
	if len(r.asker.asks) != 1 || len(r.book(t, thread).Turns) != 1 {
		t.Fatalf("asks %d", len(r.asker.asks))
	}
	r.clock.advance(24 * time.Hour)
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread}); err != nil {
		t.Fatalf("the next day: %v", err)
	}
}

type facts struct{}

func (facts) UnitFacts(_ context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return task.UnitFacts{Unit: u}, true, nil
}

func (r *rig) inbox() *messages.Service {
	g := coretasks.New(coretasks.Deps{Store: taskstest.New(), Items: r.ledger, Facts: facts{}, Clock: r.clock, StaleWaitingDays: 2})
	return &messages.Service{Store: messagestest.New(), Books: r.store, Turns: r.turns, Tasks: g, Intents: turnstest.Intents(), Clock: r.clock}
}

func asked() record.Record {
	return record.Record{State: record.Asked, Summary: "which date?", Resume: "the reply waits on the date",
		Question: &record.Question{Kind: "choice", Text: "Which date do I offer?", Recommend: "fri", Options: []record.Option{
			{ID: "fri", Label: "Friday", Consequence: "offers Friday"}, {ID: "mon", Label: "Monday", Consequence: "offers Monday"}}}}
}

func questions(t *testing.T, m *messages.Service) []message.Message {
	t.Helper()
	open, err := m.Store.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []message.Message
	for _, msg := range open {
		if msg.Kind == message.Question {
			out = append(out, msg)
		}
	}
	return out
}

func TestHeadlessQuestionReachesTheInbox(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, asked())})
	got, err := r.svc.Run(context.Background(), headless.Request{Unit: thread, Items: []string{"i1"}})
	if err != nil {
		t.Fatal(err)
	}
	m := r.inbox()
	if err := m.Reconcile(context.Background(), thread); err != nil {
		t.Fatal(err)
	}
	qs := questions(t, m)
	if len(qs) != 1 || qs[0].Unit != thread || qs[0].Origin.TurnSeq != got.Seq || len(qs[0].Options) != 2 {
		t.Fatalf("questions %+v", qs)
	}
}

func TestHeadlessAnswerRunsAgain(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, asked())}, scripted{json: answerJSON(t, done)})
	ctx := context.Background()
	first, err := r.svc.Run(ctx, headless.Request{Unit: thread, Items: []string{"i1", "i2"}, Workflow: "triage", Trigger: turn.Automated})
	if err != nil {
		t.Fatal(err)
	}
	m := r.inbox()
	if err := m.Reconcile(ctx, thread); err != nil {
		t.Fatal(err)
	}
	q := questions(t, m)[0]
	route, err := m.Answer(ctx, q.ID, messages.Answer{OptionID: "mon", Source: turn.Cockpit})
	if err != nil || route.Route != turn.Resumed {
		t.Fatalf("route %+v %v", route, err)
	}
	if len(r.asker.asks) != 2 || !strings.Contains(r.asker.asks[1].Prompt, "Monday") || !strings.Contains(r.asker.asks[1].Prompt, "act on i1,i2") {
		t.Fatalf("the second call: %d asks\n%s", len(r.asker.asks), r.asker.asks[len(r.asker.asks)-1].Prompt)
	}
	b := r.book(t, thread)
	again := b.Turns[len(b.Turns)-1]
	if len(b.Turns) != 2 || again.Runner != turn.RunnerHeadless || again.ResumeOf != first.Seq || again.Trigger != turn.Automated || again.Workflow != "triage" ||
		again.State != turn.Done || !slices.Equal(turns.ItemsOf(again), []string{"i1", "i2"}) || !strings.Contains(again.Guidance, "Monday") {
		t.Fatalf("the run again %+v", again)
	}
	if typed := r.sess.Typed(); len(typed) != 0 {
		t.Fatalf("typed into a session: %+v", typed)
	}
}
