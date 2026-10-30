package messages_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r7 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	r8 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
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

type launcher struct {
	mu       sync.Mutex
	launches []turns.Launch
}

func (l *launcher) Launch(_ context.Context, x turns.Launch) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.launches = append(l.launches, x)
	return nil
}

func (l *launcher) Answers(context.Context, unit.Unit, time.Time) ([]turns.SourceAnswer, error) {
	return nil, nil
}

type noItems struct{ byUnit map[unit.Unit][]items.Item }

func (n noItems) Items(_ context.Context, u unit.Unit) ([]items.Item, error) { return n.byUnit[u], nil }

type facts struct{ off map[unit.ProviderID]bool }

func (f facts) UnitFacts(_ context.Context, u unit.Unit) (task.UnitFacts, bool, error) {
	return task.UnitFacts{Unit: u}, !f.off[u.Provider], nil
}

type fakeLedger struct {
	mu        sync.Mutex
	proposals []coreitems.Proposal
	decided   []decision
}

type decision struct {
	id     int64
	accept bool
}

func (l *fakeLedger) Proposals(_ context.Context, st coreitems.ProposalState) ([]coreitems.Proposal, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []coreitems.Proposal
	for _, p := range l.proposals {
		if st == "" || p.State == st {
			out = append(out, p)
		}
	}
	return out, nil
}

func (l *fakeLedger) Decide(_ context.Context, id int64, accept bool) (coreitems.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := slices.IndexFunc(l.proposals, func(p coreitems.Proposal) bool { return p.ID == id })
	if i < 0 {
		return coreitems.Result{}, coreitems.ErrNoProposal
	}
	l.decided = append(l.decided, decision{id, accept})
	return coreitems.Result{}, nil
}

type rig struct {
	svc       *messages.Service
	turns     *turns.Service
	books     *turnstest.Store
	reg       *turnstest.Registry
	sess      *turnstest.Sessions
	msgs      *messagestest.Store
	tasks     *coretasks.Service
	taskStore *taskstest.Store
	items     noItems
	facts     facts
	ledger    *fakeLedger
	clock     *clock
	launch    *launcher
	gateStore *gatestest.Store
	gates     *gates.Service
}

// The bundled gates.toml's gates of the messages, and its missing-record gate.
var profileGates = gateDefs{
	{Name: "question", Kind: gate.KindQuestion, Label: "The agent asks", Intents: []gates.Intent{
		{Name: "answer", Status: gate.Answered, Answer: true}, {Name: "continue", Status: gate.Answered}, {Name: "dismiss", Status: gate.Dismissed}}},
	{Name: "post", Kind: gate.KindPost, Label: "The agent says", Intents: []gates.Intent{
		{Name: "reply", Status: gate.Answered, Answer: true}, {Name: "dismiss", Status: gate.Dismissed}}},
	{Name: "proposal", Kind: gate.KindProposal, Label: "Apply this proposal?", Intents: []gates.Intent{
		{Name: "approve", Status: gate.Approved}, {Name: "reject", Status: gate.Rejected}}},
	{Name: "missing-record", Kind: gate.KindQuestion, Label: "The run stopped", Intents: []gates.Intent{
		{Name: "answer", Status: gate.Answered, Answer: true}, {Name: "continue", Status: gate.Answered}, {Name: "dismiss", Status: gate.Dismissed}}},
}

type gateDefs []gates.Def

func (d gateDefs) Gate(name string) (gates.Def, bool) {
	i := slices.IndexFunc(d, func(g gates.Def) bool { return g.Name == name })
	if i < 0 {
		return gates.Def{}, false
	}
	return d[i], true
}

func (d gateDefs) FirstOf(k gate.Kind) (gates.Def, bool) {
	i := slices.IndexFunc(d, func(g gates.Def) bool { return g.Kind == k })
	if i < 0 {
		return gates.Def{}, false
	}
	return d[i], true
}

// gateTasks give a gate the task of its unit from the store, and open one through the tasks service.
type gateTasks struct {
	store *taskstest.Store
	svc   *coretasks.Service
}

func (g gateTasks) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return g.store.TasksOf(ctx, u)
}

func (g gateTasks) Open(ctx context.Context, r coretasks.OpenRequest) (task.Task, error) {
	return g.svc.Open(ctx, r)
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{books: turnstest.NewStore(), reg: turnstest.NewRegistry(), sess: turnstest.NewSessions(), msgs: messagestest.New(),
		taskStore: taskstest.New(), items: noItems{map[unit.Unit][]items.Item{}}, facts: facts{map[unit.ProviderID]bool{}},
		ledger: &fakeLedger{}, clock: &clock{t: t0}, launch: &launcher{}}
	r.turns = &turns.Service{Intents: turnstest.Intents(), Store: r.books, Registry: r.reg, Sessions: r.sess, Launcher: r.launch, Answers: r.launch,
		Clock: r.clock, Command: "loop", FastJob: 2 * time.Minute}
	r.tasks = coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: r.taskStore, Items: r.items, Facts: r.facts, Clock: r.clock})
	r.gateStore = gatestest.NewStore(r.clock.Now)
	r.svc = &messages.Service{Store: r.msgs, Books: r.books, Turns: r.turns, Ledger: r.ledger, Tasks: r.tasks, Intents: turnstest.Intents(),
		Clock: r.clock, Log: t.Logf, ProposalText: func(p coreitems.Proposal) string {
			return "Proposed " + string(p.Op.Op) + " of item " + p.Item + ": " + p.Reason
		}}
	r.gates = gates.New(gates.Deps{Store: r.gateStore, Reader: r.gateStore, Profile: profileGates, Tasks: gateTasks{r.taskStore, r.tasks}, Clock: r.clock,
		Deliverer: r.svc, Decider: r.svc, Log: t.Logf})
	r.svc.Gates = serviceGates{r.gates, r.gateStore}
	return r
}

// gateOf is m's gate, open or closed, failing t when m has none.
func (r *rig) gateOf(t *testing.T, m message.Message) gate.Gate {
	t.Helper()
	all, err := r.gateStore.Where(context.Background(), gates.Query{Kind: m.Kind.Gate(), Statuses: []gate.Status{gate.Open, gate.Answered,
		gate.Approved, gate.Rejected, gate.Dismissed, gate.Superseded}})
	if err != nil {
		t.Fatal(err)
	}
	var found []gate.Gate
	for _, g := range all {
		if g.Subject == strconv.FormatInt(m.ID, 10) {
			found = append(found, g)
		}
	}
	if len(found) == 0 {
		t.Fatalf("message %d has no gate", m.ID)
	}
	return found[len(found)-1]
}

// openGates are the open gates of kind k.
func (r *rig) openGates(t *testing.T, k gate.Kind) []gate.Gate {
	t.Helper()
	open, err := r.gateStore.Where(context.Background(), gates.Query{Kind: k})
	if err != nil {
		t.Fatal(err)
	}
	return open
}

// resolve resolves m's open gate with intent and a as a person, from source; it returns the answer's route.
func (r *rig) resolve(m message.Message, intent string, a messages.Answer) (turns.AnswerResult, error) {
	d := &messages.Delivery{Source: a.Source}
	ctx := messages.WithDelivery(context.Background(), d)
	open, err := r.gateStore.Where(ctx, gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit})
	if err != nil {
		return turns.AnswerResult{}, err
	}
	i := slices.IndexFunc(open, func(g gate.Gate) bool { return g.Subject == strconv.FormatInt(m.ID, 10) })
	if i < 0 {
		return turns.AnswerResult{}, gates.ErrResolved
	}
	err = r.gates.Resolve(ctx, open[i].ID, gates.Resolution{Intent: intent, Answer: messages.GateAnswer(a), By: task.ActorPerson})
	return d.Route, err
}

// answer answers m through its gate, as a person.
func (r *rig) answer(m message.Message, a messages.Answer) (turns.AnswerResult, error) {
	intent := "answer"
	if m.Kind == message.AgentPost {
		intent = "reply"
	}
	return r.resolve(m, intent, a)
}

// live puts term_1 as a warm, capable, idle session.
func (r *rig) live() {
	r.reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws", AgentSession: "s1", StartedAt: t0.Add(-2 * time.Hour), MCPAt: t0.Add(-2 * time.Hour)})
	r.sess.Put("ws", turns.Terminal{Session: "term_1", Key: "tab-1", LastOutput: t0.Add(-time.Minute)})
}

func (r *rig) busy() {
	r.sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws", Pane: "tab-1:leaf-1", State: turn.AgentWorking})
}

func (r *rig) gone() { r.sess.Put("ws") }

func question() *record.Question {
	return &record.Question{Kind: "choice", Text: "Publish the approval?", Recommend: "publish", Options: []record.Option{
		{ID: "publish", Label: "Publish", Consequence: "posts the approval", Publishes: true},
		{ID: "hold", Label: "Hold", Consequence: "keeps the draft"},
	}}
}

func askedRecord(q *record.Question) record.Record {
	return record.Record{State: record.Asked, Summary: "drafted APPROVE", Resume: "the draft is ready", Question: q}
}

// started puts a started review turn of u on term_1.
func (r *rig) started(u unit.Unit) turn.Book {
	tu := turn.Turn{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "abc", Trigger: turn.Automated, Head: "h1", SentTo: "term_1", Session: "term_1",
		State: turn.Started, SentAt: t0.Add(-2 * time.Hour), StartedAt: t0.Add(-time.Hour)}
	return r.books.Put(turn.Book{Unit: u, Turns: []turn.Turn{tu}})
}

// record adds rec as the newest record of turn seq of u, a minute after the clock, and moves the clock there.
func (r *rig) record(t *testing.T, u unit.Unit, seq int64, rec record.Record) {
	t.Helper()
	r.clock.advance(time.Minute)
	b := r.book(t, u)
	b.Record(turn.Record{TurnSeq: seq, Session: "term_1", Hash: rec.Hash() + r.clock.Now().String(), At: r.clock.Now(), Acked: true, Record: rec})
	r.books.Put(b)
}

func (r *rig) asked(t *testing.T, u unit.Unit) {
	t.Helper()
	r.started(u)
	r.record(t, u, 1, askedRecord(question()))
}

func (r *rig) book(t *testing.T, u unit.Unit) turn.Book {
	t.Helper()
	books, err := r.books.Books(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return books[u]
}

func (r *rig) reconcile(t *testing.T, units ...unit.Unit) {
	t.Helper()
	if err := r.svc.Reconcile(context.Background(), units...); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) open(t *testing.T) []message.Message {
	t.Helper()
	open, err := r.msgs.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func (r *rig) message(t *testing.T, id int64) message.Message {
	t.Helper()
	m, ok, err := r.msgs.Message(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("message %d: %v %v", id, ok, err)
	}
	return m
}

func (r *rig) task(t *testing.T, id string) task.Task {
	t.Helper()
	e, err := r.taskStore.Task(context.Background(), task.ID(id))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (r *rig) inbox(t *testing.T) []task.Task {
	t.Helper()
	es, err := r.taskStore.Tasks(context.Background(), task.Query{})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(es, func(e task.Task) bool { return e.Category != task.CategoryInbox })
}

// only is the one open message, failing t otherwise.
func (r *rig) only(t *testing.T) message.Message {
	t.Helper()
	open := r.open(t)
	if len(open) != 1 {
		t.Fatalf("open messages %+v, want one", open)
	}
	return open[0]
}

// subject is the Subject of m's gates.
func subject(m message.Message) string { return strconv.FormatInt(m.ID, 10) }

// gatesResolution is a person's resolution with intent.
func gatesResolution(intent string) gates.Resolution {
	return gates.Resolution{Intent: intent, By: task.ActorPerson}
}

// serviceGates are the gates service, read through its store.
type serviceGates struct {
	*gates.Service
	read *gatestest.Store
}

func (g serviceGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.read.Where(ctx, q)
}
