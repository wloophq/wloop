package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const head = "c0ffee1"

// specKinds are kinds in memory, as outboxtest.Kinds, which imports this package.
type specKinds struct {
	Specs            []draft.KindSpec
	Fields, Required map[string][]string
}

func (k specKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	i := slices.IndexFunc(k.Specs, func(s draft.KindSpec) bool { return s.Provider == provider && s.Name == kind })
	if i < 0 {
		return draft.KindSpec{}, false
	}
	return k.Specs[i], true
}

func (k specKinds) ForDelivery(provider, delivery string) (draft.KindSpec, bool) {
	i := slices.IndexFunc(k.Specs, func(s draft.KindSpec) bool { return s.Provider == provider && (delivery == "" || s.Delivery == delivery) })
	if i < 0 {
		return draft.KindSpec{}, false
	}
	return k.Specs[i], true
}

func (k specKinds) Validate(spec draft.KindSpec, payload json.RawMessage) error {
	key := spec.Provider + "/" + spec.Name
	var fields map[string]json.RawMessage
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
			return fmt.Errorf("%w: not an object", ErrInvalid)
		}
	}
	for _, f := range k.Required[key] {
		if _, ok := fields[f]; !ok {
			return fmt.Errorf("%w: no %s", ErrInvalid, f)
		}
	}
	if allowed, ok := k.Fields[key]; ok {
		for f := range fields {
			if !slices.Contains(allowed, f) {
				return fmt.Errorf("%w: no field %s", ErrInvalid, f)
			}
		}
	}
	return nil
}

// liveBases are each unit's live basis, as outboxtest.Bases.
type liveBases struct {
	mu    sync.Mutex
	live  map[unit.Unit]string
	calls int
	Err   error
}

func (b *liveBases) Set(u unit.Unit, basis string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.live == nil {
		b.live = map[unit.Unit]string{}
	}
	b.live[u] = basis
}

func (b *liveBases) Live(_ context.Context, u unit.Unit, _ string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return b.live[u], b.Err
}

func (b *liveBases) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// reviewEnv is an outbox whose GitHub plugin declares kind review, written against the pull request's head,
// and whose live head is head.
func reviewEnv() (*env, *liveBases) {
	e := newEnv()
	bases := &liveBases{}
	bases.Set(pull, head)
	e.svc.Bases = bases
	e.svc.Kinds = specKinds{
		Specs: []draft.KindSpec{
			{Provider: string(unit.GitHub), Name: "review", Delivery: "review", Basis: draft.BasisAgent, Editable: []string{"text", "/event"}},
			{Provider: string(unit.GitHub), Name: "comment", Delivery: "comment", TextRequired: true},
			{Provider: "slack", Name: "reply", Delivery: "message", TextRequired: true},
		},
		Fields:   map[string][]string{"github/review": {"event", "comments"}},
		Required: map[string][]string{"github/review": {"event"}},
	}
	e.svc.Posts = func(d draft.Delivery) bool { return d == "comment" || d == "review" || d == "message" }
	return e, bases
}

// sendsPosts sends every review and comment by a rule that names its delivery.
var sendsPosts = &draft.Policy{Default: waiting, Rules: []draft.Rule{
	{When: factIs{"draft.delivery", "review"}, Names: "review", Approval: noneA},
	{When: factIs{"draft.delivery", "comment"}, Names: "comment", Approval: noneA}}}

func review(event string) Proposal {
	return Proposal{Unit: pull, Kind: "review", Text: "Two findings.", Payload: json.RawMessage(`{"event":"` + event + `"}`), Basis: head,
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review", Session: "s-worker"}}
}

var worker = Caller{Session: "s-worker", Units: []unit.Unit{pull}}

func payload(s string) *json.RawMessage {
	r := json.RawMessage(s)
	return &r
}

func ptr(s string) *string { return &s }

func TestProposeVerifiesBasisLive(t *testing.T) {
	e, bases := reviewEnv()
	d, err := e.svc.Propose(ctx, review("COMMENT"))
	if err != nil || d.Basis != head || d.State != draft.Held || bases.Calls() != 1 {
		t.Fatalf("Propose = %+v, %v (live reads %d)", d, err, bases.Calls())
	}
	bases.Set(pull, "beef002")
	if _, err := e.svc.Propose(ctx, review("COMMENT")); !errors.Is(err, ErrStale) {
		t.Fatalf("a basis the head moved past: %v", err)
	}
	none := review("COMMENT")
	none.Basis = ""
	if _, err := e.svc.Propose(ctx, none); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no basis: %v", err)
	}
	bases.Err = errBoom
	if _, err := e.svc.Propose(ctx, review("COMMENT")); !errors.Is(err, ErrStale) {
		t.Fatalf("a live read that fails: %v", err)
	}
	e.svc.Bases = nil
	if _, err := e.svc.Propose(ctx, review("COMMENT")); !errors.Is(err, ErrStale) {
		t.Fatalf("no live reader: %v, want it to fail closed", err)
	}
	if ds, _ := e.svc.List(ctx, Filter{}); len(ds) != 1 {
		t.Fatalf("stored %d drafts, want the fresh one alone", len(ds))
	}
}

func TestProposeRefusesInvalidPayload(t *testing.T) {
	e, _ := reviewEnv()
	for name, p := range map[string]string{"no event": `{}`, "a field the schema lacks": `{"event":"COMMENT","verdict":"x"}`, "no object": `[1]`} {
		pr := review("COMMENT")
		pr.Payload = json.RawMessage(p)
		if _, err := e.svc.Propose(ctx, pr); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	pr := review("COMMENT")
	pr.Payload = nil
	if _, err := e.svc.Propose(ctx, pr); !errors.Is(err, ErrInvalid) {
		t.Errorf("no payload: %v", err)
	}
	unknown := review("COMMENT")
	unknown.Kind = "approve_everything"
	if _, err := e.svc.Propose(ctx, unknown); !errors.Is(err, ErrInvalid) {
		t.Errorf("an undeclared kind: %v", err)
	}
	if ds, _ := e.svc.List(ctx, Filter{}); len(ds) != 0 {
		t.Fatalf("stored %+v", ds)
	}
}

func TestHoldWaits(t *testing.T) {
	e, _ := reviewEnv()
	withPolicy(e, sendsPosts)
	if d := e.propose(review("COMMENT")); d.State != draft.Queued {
		t.Fatalf("without hold the rule sends: %+v", d)
	}
	held := review("COMMENT")
	held.Hold = true
	d := e.propose(held)
	if !d.Hold || d.State != draft.Held || e.approval(d.ID) != waiting {
		t.Fatalf("a held draft under a sending rule: %+v %+v", d, e.approval(d.ID))
	}
	g := e.gate(d.ID)
	g.Status, g.By, g.Rule = gate.Approved, task.ActorWorkflow, "gates.approval rule 0"
	if err := e.svc.Publish(ctx, g); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, "held for a person") {
		t.Fatalf("a rule's release of a held draft: %+v", got)
	}
}

func TestHoldUnderDryRun(t *testing.T) {
	e, _ := reviewEnv()
	withPolicy(e, draft.Fixed(draft.Approval{Mode: draft.DryRun}))
	held := review("REQUEST_CHANGES")
	held.Hold = true
	d := e.propose(held)
	if d.State != draft.Dropped || d.Reason != reasonDryRun {
		t.Fatalf("dry_run keeps its say over hold: %+v", d)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 || len(e.fx.published) != 0 {
		t.Fatalf("published %v", e.fx.published)
	}
}

func TestHoldReappliedOnRevision(t *testing.T) {
	e, _ := reviewEnv()
	withPolicy(e, sendsPosts)
	held := review("COMMENT")
	held.Hold = true
	d := e.propose(held)
	got, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("One finding."), Rev: d.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Hold || got.State != draft.Held || e.gate(d.ID).Status != gate.Open || e.approval(d.ID) != waiting {
		t.Fatalf("revised: %+v, gate %+v", got, e.gate(d.ID))
	}
	if _, err := e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("No findings.")}); err != nil {
		t.Fatal(err)
	}
	if !e.get(d.ID).Hold || e.approval(d.ID) != waiting {
		t.Fatalf("a person's edit lost the hold: %+v", e.get(d.ID))
	}
}

func TestAgentRevisionOpensWaitingGate(t *testing.T) {
	e, _ := reviewEnv()
	withPolicy(e, &draft.Policy{Default: waiting, Rules: []draft.Rule{{When: factIs{"draft.delivery", "review"}, Names: "review", Approval: window5}}})
	d := e.propose(review("COMMENT"))
	first := e.gate(d.ID)
	if first.Deadline.IsZero() || first.ApprovedRev != d.Rev {
		t.Fatalf("the proposal's gate %+v", first)
	}
	reviser := Caller{Session: "s-reviser", Units: []unit.Unit{pull}}
	got, err := e.svc.AgentAmend(ctx, reviser, d.ID, Amendment{Payload: payload(`{"event":"APPROVE"}`), Rev: d.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != `{"event":"APPROVE"}` || got.By != (draft.Actor{Kind: draft.ActorSession, Session: "s-reviser"}) ||
		d.By != (draft.Actor{Kind: draft.ActorSession, Session: "s-worker"}) {
		t.Fatalf("revised payload %s by %+v, proposed by %+v", got.Payload, got.By, d.By)
	}
	g := e.gate(d.ID)
	if old, _ := e.gates.Get(ctx, first.ID); old.Status != gate.Superseded {
		t.Fatalf("the proposal's gate is %s", old.Status)
	}
	if g.ID == first.ID || g.Status != gate.Open || !g.Deadline.IsZero() || g.ApprovedRev != got.Rev ||
		!slices.Contains(g.Sessions, "s-worker") || !slices.Contains(g.Sessions, "s-reviser") {
		t.Fatalf("the revision's gate %+v (draft rev %d)", g, got.Rev)
	}
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 || e.get(d.ID).State != draft.Held {
		t.Fatalf("the old window released the revision: sent %d, %s", sent, e.get(d.ID).State)
	}
	if _, err := e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("Edited by hand.")}); err != nil {
		t.Fatal(err)
	}
	if by := e.get(d.ID).By; by.Kind != draft.ActorPerson {
		t.Fatalf("a person's edit is by %+v", by)
	}
	if after := e.gate(d.ID); !slices.Contains(after.Sessions, "s-reviser") || !slices.Contains(after.Sessions, "s-worker") {
		t.Fatalf("a person's edit dropped the sessions its gate stamps: %+v", after)
	}
}

type driven map[string]bool

func (d driven) UserDriven(_ context.Context, id string) (bool, error) { return d[id], nil }

func TestFailedRevisedGoesHeld(t *testing.T) {
	e, bases := reviewEnv()
	withPolicy(e, sendsPosts)
	d := e.propose(review("REQUEST_CHANGES"))
	if d.State != draft.Queued {
		t.Fatalf("the rule did not send it: %+v", d)
	}
	bases.Set(pull, "beef002")
	if _, failed, _ := e.svc.Send(ctx); failed != 1 || len(e.fx.published) != 0 {
		t.Fatalf("failed %d, published %v", failed, e.fx.published)
	}
	d = e.get(d.ID)
	nb := "beef002"
	got, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Payload: payload(`{"event":"APPROVE"}`), Basis: &nb, Rev: d.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != draft.Held || got.Error != "" || e.gate(d.ID).Status != gate.Open || e.approval(d.ID) != waiting {
		t.Fatalf("revised: %+v, gate %+v", got, e.gate(d.ID))
	}
	if _, err := e.svc.Retry(ctx, d.ID, true); err == nil {
		t.Fatal("a retry sent the agent's revision")
	}
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 || len(e.fx.published) != 0 {
		t.Fatalf("the revision posted without a person: %v", e.fx.published)
	}
	e.decider = gates.New(gates.Deps{Store: e.gates, Reader: e.gates, Profile: draftProfile{}, Clock: e.clock, Publisher: publishTo{e},
		Sessions: driven{"s-worker": true}})
	if err := e.decider.Resolve(ctx, e.gate(d.ID).ID, gates.Resolution{Intent: "approve", By: task.ActorAgent, Session: "s-worker"}); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("the reviser approved its revision: %v", err)
	}
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := e.svc.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("a person's approval: sent %d, %v", sent, err)
	}
}

// TestSendPostsOnlyApprovedRev asserts at Publish, where the draft leaves held: a gate whose ApprovedRev is not
// the held draft's Rev, or is 0, queues nothing.
func TestSendPostsOnlyApprovedRev(t *testing.T) {
	e, _ := reviewEnv()
	zero := e.propose(review("COMMENT"))
	g0 := e.gate(zero.ID)
	g0.ApprovedRev, g0.Status, g0.By = 0, gate.Approved, task.ActorPerson
	at0 := e.get(zero.ID)
	at0.Rev = 0
	if e.svc.atGateRev(at0, g0) {
		t.Fatal("a gate of rev 0 matched")
	}
	d := e.propose(review("COMMENT"))
	if g := e.gate(d.ID); g.ApprovedRev != d.Rev {
		t.Fatalf("the gate opened for rev %d, the draft is at %d", g.ApprovedRev, d.Rev)
	}
	sneaked := e.get(d.ID)
	sneaked.Payload = json.RawMessage(`{"event":"APPROVE"}`)
	if _, err := e.store.Update(ctx, sneaked, draft.Held); err != nil {
		t.Fatal(err)
	}
	if err := e.decide(d.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, "changed after its gate opened") {
		t.Fatalf("a gate approved another rev than its draft's: %+v", got)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 || len(e.fx.published) != 0 {
		t.Fatalf("published %v", e.fx.published)
	}
}

type noPlacer struct{ Gates }

func TestAGateWithNoTaskPlacerToleratesOnlyTheAdoptionOfItsTask(t *testing.T) {
	e, _ := reviewEnv()
	d := e.propose(review("COMMENT"))
	g := e.gate(d.ID)
	g.Task = "t-1"
	e.svc.Gates = noPlacer{e.svc.Gates}
	adopted := e.get(d.ID)
	adopted.Task = "t-1"
	cur, err := e.store.Update(ctx, adopted, draft.Held)
	if err != nil {
		t.Fatal(err)
	}
	if !e.svc.atGateRev(cur, g) {
		t.Fatal("the adoption of its gate's task refused the draft")
	}
	other := g
	other.Task = "t-2"
	if e.svc.atGateRev(cur, other) {
		t.Fatal("another task passed")
	}
	cur.Text = "sneaked"
	if cur, err = e.store.Update(ctx, cur, draft.Held); err != nil {
		t.Fatal(err)
	}
	if e.svc.atGateRev(cur, g) {
		t.Fatal("a second write passed")
	}
}

func prComment(text string) Proposal {
	return Proposal{Unit: pull, Kind: "comment", Text: text, Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review"}}
}

func TestApproveRefusesStale(t *testing.T) {
	e, bases := reviewEnv()
	d := e.propose(review("COMMENT"))
	bases.Set(pull, "beef002")
	if _, err := e.svc.Prepare(ctx, d.ID, e.get(d.ID).Rev); !errors.Is(err, ErrStale) {
		t.Fatalf("Prepare = %v, want ErrStale", err)
	}
	if stale, err := e.svc.Stale(ctx, e.get(d.ID)); !stale || err != nil {
		t.Fatalf("Stale = %v, %v", stale, err)
	}
	if err := e.decide(d.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, ErrStale.Error()) {
		t.Fatalf("an approval of a stale draft: %+v", got)
	}
	if stale, err := e.svc.Stale(ctx, e.propose(prComment("hi"))); stale || err != nil {
		t.Fatalf("a draft with no basis: %v, %v", stale, err)
	}
	e.svc.Bases = nil
	if stale, err := e.svc.Stale(ctx, e.get(d.ID)); !stale || !errors.Is(err, ErrStale) {
		t.Fatalf("no live reader: %v, %v", stale, err)
	}
}

func TestSendFailsStaleWithoutPublishing(t *testing.T) {
	e, bases := reviewEnv()
	d := e.propose(review("REQUEST_CHANGES"))
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	bases.Set(pull, "beef002")
	sent, failed, err := e.svc.Send(ctx)
	if sent != 0 || failed != 1 || err != nil || len(e.fx.published) != 0 {
		t.Fatalf("Send = %d, %d, %v, published %v", sent, failed, err, e.fx.published)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, ErrStale.Error()) || !got.SendingAt.IsZero() {
		t.Fatalf("%+v", got)
	}
}

func TestAmendRevConflict(t *testing.T) {
	e, _ := reviewEnv()
	d := e.propose(review("COMMENT"))
	if _, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("x")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rev 0: %v", err)
	}
	if _, err := e.svc.AgentWithdraw(ctx, worker, d.ID, 0, "moot"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("withdraw at rev 0: %v", err)
	}
	read := e.get(d.ID).Rev
	if _, err := e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("a person's words")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("the agent's words"), Rev: read}); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("an agent at the rev before a person's edit: %v", err)
	}
	if _, err := e.svc.AgentWithdraw(ctx, worker, d.ID, read, "moot"); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("a withdrawal at the rev before a person's edit: %v", err)
	}
	if _, err := e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("y"), Rev: read}); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("a person at an old rev: %v", err)
	}
	cur := e.get(d.ID)
	e.store.beforeUpdate = func(draft.Draft) {
		e.store.beforeUpdate = nil
		racing := e.get(d.ID)
		racing.Text = "a person's racing words"
		if _, err := e.store.Update(ctx, racing, draft.Held); err != nil {
			t.Error(err)
		}
	}
	if _, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("the agent's words"), Rev: cur.Rev}); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("a race: %v", err)
	}
	if got := e.get(d.ID); got.Text != "a person's racing words" {
		t.Fatalf("the race overwrote the person's edit: %q", got.Text)
	}
	if g := e.gate(d.ID); g.Status != gate.Open || g.ApprovedRev != e.get(d.ID).Rev {
		t.Fatalf("after the race the draft waits on %+v", g)
	}
}

func TestAgentScopeTurnOrSessionOrigin(t *testing.T) {
	e, _ := reviewEnv()
	mine := e.propose(review("COMMENT"))
	person := prComment("hi")
	person.Origin = draft.Origin{Kind: draft.OriginUser}
	auto := prComment("any news?")
	auto.Origin = draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "k"}
	other := prComment("elsewhere")
	other.Unit = unit.Unit{Provider: unit.GitHub, Key: "acme/web#2"}
	elsewhere := e.propose(other)
	for name, d := range map[string]draft.Draft{"a person's": e.propose(person), "a workflow's": e.propose(auto), "another unit's": elsewhere} {
		if _, err := e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("x"), Rev: d.Rev}); !errors.Is(err, ErrOutOfScope) {
			t.Errorf("amend %s: %v", name, err)
		}
		if _, err := e.svc.AgentWithdraw(ctx, worker, d.ID, d.Rev, "moot"); !errors.Is(err, ErrOutOfScope) {
			t.Errorf("withdraw %s: %v", name, err)
		}
		if got := e.get(d.ID); got.State != draft.Held || got.Text == "x" {
			t.Errorf("%s changed: %+v", name, got)
		}
	}
	if _, _, err := e.svc.AgentRead(ctx, worker, elsewhere.ID); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("read another unit's: %v", err)
	}
	if _, _, err := e.svc.AgentRead(ctx, Caller{Units: worker.Units}, mine.ID); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("read with no session: %v", err)
	}
	if _, err := e.svc.AgentAmend(ctx, Caller{Units: worker.Units}, mine.ID, Amendment{Text: ptr("x"), Rev: mine.Rev}); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("amend with no session: %v", err)
	}
	if got, _, err := e.svc.AgentRead(ctx, worker, mine.ID); err != nil || got.ID != mine.ID {
		t.Fatalf("read its own: %+v %v", got, err)
	}
	cross := reply("on it")
	cross.Origin.Unit, cross.Origin.Session = pull, "s-worker"
	x := e.propose(cross)
	if _, err := e.svc.AgentAmend(ctx, worker, x.ID, Amendment{Text: ptr("done"), Rev: x.Rev}); err != nil {
		t.Fatalf("a draft proposed from the caller's unit into a thread: %v", err)
	}
	gid := e.gate(mine.ID).ID
	got, err := e.svc.AgentWithdraw(ctx, worker, mine.ID, mine.Rev, draft.WithdrawMoot)
	if err != nil || got.State != draft.Dropped || got.Reason != draft.WithdrawMoot {
		t.Fatalf("withdraw its own: %+v %v", got, err)
	}
	if old, _ := e.gates.Get(ctx, gid); old.Status != gate.Superseded || old.BySession != "s-worker" {
		t.Fatalf("the withdrawn draft's gate %+v", old)
	}
}

// scoping is a publisher whose Scoper says which targets are within the unit.
type scoping struct {
	*fakeEffects
	within func(target string) (bool, error)
}

func (s scoping) Within(_ context.Context, _ unit.Unit, target string) (bool, error) { return s.within(target) }

func TestTargeterDecidesWithinUnit(t *testing.T) {
	isComment := func(t string) (bool, error) { return strings.HasPrefix(t, "comment:"), nil }
	for _, c := range []struct {
		name   string
		within func(string) (bool, error)
		target string
		want   draft.State
	}{
		{"a comment of the pull request", isComment, "comment:991", draft.Queued},
		{"another pull request", isComment, "acme/web#2", draft.Held},
		{"a scoper that cannot say", func(string) (bool, error) { return true, errBoom }, "comment:991", draft.Held},
		{"no scoper", nil, "comment:991", draft.Held},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, _ := reviewEnv()
			withPolicy(e, sendsPosts)
			if c.within != nil {
				sc := scoping{e.fx, c.within}
				e.svc.Effects = func(unit.ProviderID) (port.Effects, error) { return sc, nil }
			}
			p := prComment("Fixed in the last push.")
			p.Target = c.target
			if d := e.propose(p); d.State != c.want {
				t.Fatalf("%s: %+v", c.target, d)
			}
		})
	}
}

func TestAttachPostCoversNewDeliveries(t *testing.T) {
	e, _ := reviewEnv()
	r := &postResults{intents: []string{"review"}}
	e.svc.Results = r
	p := review("COMMENT")
	p.Origin.Unit = task.UnitOf("t-1")
	d := e.propose(p)
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := e.svc.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("Send = %d, %v", sent, err)
	}
	if len(r.attached) != 1 || r.attached[0] != pull {
		t.Fatalf("attached %v, want the pull request a review posts on", r.attached)
	}
	e.svc.Posts = func(d draft.Delivery) bool { return d == "comment" }
	d = e.propose(p)
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.attached) != 1 {
		t.Fatalf("a delivery the manifests do not mark posts attached: %v", r.attached)
	}
}

func TestWithinErrorIsOutside(t *testing.T) {
	yes := func(string) (bool, error) { return true, nil }
	for _, c := range []struct {
		name   string
		within func(string) (bool, error)
		unit   unit.Unit
	}{
		{"a scoper that fails", func(string) (bool, error) { return true, errBoom }, pull},
		{"a publisher of another provider than the unit's", yes, taskUnit},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, _ := reviewEnv()
			withPolicy(e, sendsPosts)
			sc := scoping{e.fx, c.within}
			e.svc.Effects = func(p unit.ProviderID) (port.Effects, error) {
				if p == unit.Task {
					return nil, ErrDisabled
				}
				return taking{sc.fakeEffects, func(string) bool { return true }}, nil
			}
			e.svc.Targets = []unit.ProviderID{unit.GitHub}
			p := prComment("Fixed in the last push.")
			p.Unit, p.Target = c.unit, "comment:991"
			d := e.propose(p)
			if d.State != draft.Held || e.approval(d.ID) != waiting {
				t.Fatalf("%+v", d)
			}
		})
	}
}
