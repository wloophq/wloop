package messagestest

import (
	"context"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
)

// Defs are the bundled gates.toml's gates of the messages and its missing-record gate.
var Defs = Profile{
	{Name: "question", Kind: gate.KindQuestion, Label: "The agent asks", Intents: []gates.Intent{
		{Name: "answer", Status: gate.Answered, Answer: true}, {Name: "continue", Status: gate.Answered}, {Name: "dismiss", Status: gate.Dismissed}}},
	{Name: "post", Kind: gate.KindPost, Label: "The agent says", Intents: []gates.Intent{
		{Name: "reply", Status: gate.Answered, Answer: true}, {Name: "dismiss", Status: gate.Dismissed}}},
	{Name: "proposal", Kind: gate.KindProposal, Label: "Apply this proposal?", Intents: []gates.Intent{
		{Name: "approve", Status: gate.Approved}, {Name: "reject", Status: gate.Rejected}}},
	{Name: "missing-record", Kind: gate.KindQuestion, Label: "The run stopped", Intents: []gates.Intent{
		{Name: "answer", Status: gate.Answered, Answer: true}, {Name: "continue", Status: gate.Answered}, {Name: "dismiss", Status: gate.Dismissed}}},
}

// Profile is a gates.toml in memory.
type Profile []gates.Def

func (p Profile) Gate(name string) (gates.Def, bool) {
	i := slices.IndexFunc(p, func(g gates.Def) bool { return g.Name == name })
	if i < 0 {
		return gates.Def{}, false
	}
	return p[i], true
}

func (p Profile) FirstOf(k gate.Kind) (gates.Def, bool) {
	i := slices.IndexFunc(p, func(g gates.Def) bool { return g.Kind == k })
	if i < 0 {
		return gates.Def{}, false
	}
	return p[i], true
}

// Gates are the messages' gates in memory, opened and resolved by a real gates service whose Deliverer and
// Decider are the messages service Bind names.
type Gates struct {
	Service *gates.Service
	Store   *gatestest.Store
	msgs    atomic.Pointer[messages.Service]
}

var _ messages.Gates = (*Gates)(nil)

// NewGates are gates in memory stamped by now, each on the task tasks gives its unit.
func NewGates(now func() time.Time, tasks gates.Tasks) *Gates {
	g := &Gates{Store: gatestest.NewStore(now)}
	g.Service = gates.New(gates.Deps{Store: g.Store, Reader: g.Store, Profile: Defs, Tasks: tasks, Clock: clock(now), Deliverer: consequence{g}, Decider: consequence{g}})
	return g
}

// Bind makes s open its gates on g, and g deliver and decide through s.
func (g *Gates) Bind(s *messages.Service) *messages.Service {
	g.msgs.Store(s)
	s.Gates = g
	return s
}

func (g *Gates) Open(ctx context.Context, gt gate.Gate, p gates.Policy) (gate.Gate, error) {
	return g.Service.Open(ctx, gt, p)
}

func (g *Gates) Supersede(ctx context.Context, id int64, by task.Actor) error {
	return g.Service.Supersede(ctx, id, by)
}

func (g *Gates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	return g.Store.Where(ctx, q)
}

// Answer answers m through its open gate as a person, with its kind's first intent taking text; it returns
// the answer's route.
func (g *Gates) Answer(ctx context.Context, m message.Message, a messages.Answer) (turns.AnswerResult, error) {
	open, err := g.Store.Where(ctx, gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit})
	if err != nil {
		return turns.AnswerResult{}, err
	}
	i := slices.IndexFunc(open, func(x gate.Gate) bool { return x.Subject == strconv.FormatInt(m.ID, 10) })
	if i < 0 {
		return turns.AnswerResult{}, gates.ErrNoGate
	}
	def, _ := Defs.Gate(open[i].Name)
	intent := def.Intents[slices.IndexFunc(def.Intents, func(in gates.Intent) bool { return in.Answer })].Name
	d := &messages.Delivery{Source: a.Source}
	err = g.Service.Resolve(messages.WithDelivery(ctx, d), open[i].ID, gates.Resolution{Intent: intent, Answer: messages.GateAnswer(a), By: task.ActorPerson})
	return d.Route, err
}

type consequence struct{ g *Gates }

func (c consequence) Deliver(ctx context.Context, gt gate.Gate) error {
	return c.g.msgs.Load().Deliver(ctx, gt)
}

func (c consequence) Decide(ctx context.Context, gt gate.Gate) error {
	return c.g.msgs.Load().Decide(ctx, gt)
}

type clock func() time.Time

func (c clock) Now() time.Time { return c() }
