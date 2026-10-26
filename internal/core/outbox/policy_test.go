package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type fakeManifests struct {
	intents   map[string]*draft.Policy
	workflows map[string]*draft.Policy
	turns     map[int64][2]string
	turnErr   error
}

func (f fakeManifests) IntentApproval(name string) (*draft.Policy, bool) {
	a, ok := f.intents[name]
	return a, ok
}

func (f fakeManifests) WorkflowApproval(name string, intent *draft.Policy) (*draft.Policy, bool) {
	a, ok := f.workflows[name]
	if a == nil {
		return intent, ok
	}
	return a, ok
}

func (f fakeManifests) Turn(ctx context.Context, u unit.Unit, seq int64) (string, string, bool, error) {
	if f.turnErr != nil {
		return "", "", false, f.turnErr
	}
	t, ok := f.turns[seq]
	return t[0], t[1], ok, nil
}

func window(d time.Duration) *draft.Policy {
	return draft.Fixed(draft.Approval{Mode: draft.Delay, Window: d})
}

var (
	none    = draft.Fixed(draft.Approval{Mode: draft.Send})
	observe = draft.Fixed(draft.Approval{Mode: draft.DryRun})
)

func manifests() fakeManifests {
	return fakeManifests{
		intents:   map[string]*draft.Policy{"reply": nil, "quick": none, "watch": observe},
		workflows: map[string]*draft.Policy{"plain": nil, "timed": window(10 * time.Minute)},
		turns: map[int64][2]string{4: {"reply", ""}, 7: {"quick", ""}, 8: {"reply", "timed"}, 10: {"quick", "timed"},
			11: {"quick", "plain"}, 12: {"watch", ""}, 13: {"reply", "plain"}, 14: {"gone", ""}, 15: {"quick", "gone"}, 16: {"bad", ""}},
	}
}

func TestPolicyResolutionOrder(t *testing.T) {
	for _, c := range []struct {
		name     string
		origin   draft.Origin
		explicit *draft.Policy
		want     draft.Approval
	}{
		{"explicit wins over the workflow", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 10, Intent: "quick", Workflow: "timed"}, observe, observe.Default},
		{"explicit on an auto rule", draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge"}, none, none.Default},
		{"workflow wins over intent", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 10, Intent: "quick", Workflow: "timed"}, nil, window(10 * time.Minute).Default},
		{"a workflow without a block falls to the intent", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 11, Intent: "quick", Workflow: "plain"}, nil, none.Default},
		{"intent", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 12, Intent: "watch"}, nil, observe.Default},
		{"neither has a block", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 13, Intent: "reply", Workflow: "plain"}, nil, waiting},
		{"an auto rule without approval", draft.Origin{Kind: draft.OriginAuto, Rule: "closure"}, nil, waiting},
		{"a user draft", draft.Origin{Kind: draft.OriginUser}, nil, waiting},
		{"a session draft reads its turn", draft.Origin{Kind: draft.OriginSession, TurnSeq: 7}, nil, none.Default},
		{"a session draft reads its turn's workflow", draft.Origin{Kind: draft.OriginSession, TurnSeq: 8}, nil, window(10 * time.Minute).Default},
		{"a session draft without a turn", draft.Origin{Kind: draft.OriginSession}, nil, waiting},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv()
			e.svc.Policies = Resolver{Manifests: manifests()}
			p := reply("x")
			p.Origin, p.Policy = c.origin, c.explicit
			d := e.propose(p)
			if d.Approval != c.want {
				t.Fatalf("approval %+v, want %+v", d.Approval, c.want)
			}
		})
	}
}

func TestPolicyLookupFailureIsAlways(t *testing.T) {
	for _, c := range []struct {
		name   string
		origin draft.Origin
		m      func(*fakeManifests)
		logged string
	}{
		{"unknown intent", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 14, Intent: "gone"}, nil, "gone"},
		{"unknown workflow", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 15, Intent: "quick", Workflow: "gone"}, nil, "gone"},
		{"unknown turn", draft.Origin{Kind: draft.OriginSession, TurnSeq: 99}, nil, "turn 99"},
		{"the book errs", draft.Origin{Kind: draft.OriginSession, TurnSeq: 7}, func(m *fakeManifests) { m.turnErr = errBoom }, "boom"},
		{"a malformed block", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 16, Intent: "bad"}, func(m *fakeManifests) { m.intents["bad"] = draft.Fixed(draft.Approval{Mode: draft.Delay}) }, "window"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv()
			m := manifests()
			if c.m != nil {
				c.m(&m)
			}
			e.svc.Policies = Resolver{Manifests: m}
			p := reply("x")
			p.Origin = c.origin
			d := e.propose(p)
			if d.Approval != waiting || d.State != draft.Proposed {
				t.Fatalf("%+v", d)
			}
			if !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "resolved to wait") && strings.Contains(l, c.logged) }) {
				t.Fatalf("logs %q", e.logs)
			}
		})
	}
}

func TestOriginCannotPickThePolicy(t *testing.T) {
	for _, c := range []struct {
		name   string
		origin draft.Origin
		logged string
	}{
		{"a session draft naming another intent than its turn's", draft.Origin{Kind: draft.OriginSession, TurnSeq: 4, Intent: "quick"}, "turn 4"},
		{"a turn draft naming another workflow than its turn's", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "reply", Workflow: "timed"}, "turn 4"},
		{"a turn draft without its turn", draft.Origin{Kind: draft.OriginTurn, Intent: "quick"}, "no turn"},
		{"an auto rule naming an intent", draft.Origin{Kind: draft.OriginAuto, Rule: "x", Key: "k", Intent: "quick"}, "auto"},
		{"an auto rule naming a workflow", draft.Origin{Kind: draft.OriginAuto, Rule: "x", Key: "k2", TurnSeq: 7, Workflow: "timed"}, "auto"},
		{"a user draft naming an intent", draft.Origin{Kind: draft.OriginUser, Intent: "quick"}, "user"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv()
			e.svc.Policies = Resolver{Manifests: manifests()}
			p := reply("x")
			p.Origin = c.origin
			d := e.propose(p)
			if d.Approval != waiting || d.State != draft.Proposed {
				t.Fatalf("%+v", d)
			}
			e.svc.Send(ctx)
			if len(e.fx.published) != 0 {
				t.Fatalf("published %v", e.fx.published)
			}
			if !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "resolved to wait") && strings.Contains(l, c.logged) }) {
				t.Fatalf("logs %q", e.logs)
			}
		})
	}
}

func TestNoneDraftWhoseApprovalFailsIsReturned(t *testing.T) {
	e := newEnv()
	withPolicy(e, none)
	e.store.beforeUpdate = func(u draft.Draft) {
		e.store.beforeUpdate = nil
		if _, err := e.svc.Discard(ctx, u.ID, ""); err != nil {
			t.Errorf("discard: %v", err)
		}
	}
	d, err := e.svc.Propose(ctx, reply("x"))
	if !errors.Is(err, ErrConflict) || d.ID == 0 || d.State != draft.Proposed {
		t.Fatalf("%+v %v", d, err)
	}
}

func withPolicy(e *env, p *draft.Policy) {
	e.svc.Policies = &fixedPolicy{p: *p}
}

func TestDraftPolicyDefaultsToAlways(t *testing.T) {
	e := newEnv()
	e.svc.Policies = Resolver{Manifests: manifests()}
	d := e.propose(reply("x"))
	if d.Approval != waiting || d.State != draft.Proposed || !d.Deadline.IsZero() || d.Would.Outcome != "" {
		t.Fatalf("%+v", d)
	}
	e.clock.now = t0.Add(48 * time.Hour)
	if n, err := e.svc.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("tick %d %v", n, err)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 || e.get(d.ID).State != draft.Proposed {
		t.Fatalf("sent %d, %s", sent, e.get(d.ID).State)
	}
	if _, ok, err := e.svc.NextDeadline(ctx); ok || err != nil {
		t.Fatalf("a wait draft has a deadline: %v", err)
	}
}

func TestDraftPolicyNoneAutoApproves(t *testing.T) {
	e := newEnv()
	withPolicy(e, none)
	d := e.propose(reply("x"))
	if d.State != draft.Approved || d.Would.Outcome != draft.OutcomeReleased || !d.Deadline.IsZero() {
		t.Fatalf("%+v", d)
	}
	if got := e.get(d.ID); got.State != draft.Approved || got.Would.Outcome != draft.OutcomeReleased {
		t.Fatalf("stored %+v", got)
	}
	for range 3 {
		e.svc.Send(ctx)
	}
	if !slices.Equal(e.fx.published, []draft.ID{d.ID}) || e.get(d.ID).State != draft.Sent {
		t.Fatalf("published %v, %s", e.fx.published, e.get(d.ID).State)
	}
}

func TestDraftPolicyObserveRecordsOnly(t *testing.T) {
	e := newEnv()
	withPolicy(e, observe)
	d := e.propose(reply("x"))
	if d.State != draft.Discarded || d.Reason != string(draft.DryRun) || d.Would.Outcome != draft.OutcomeDryRun || d.Would.Policy != draft.WouldRelease {
		t.Fatalf("%+v", d)
	}
	if got, _ := e.svc.List(ctx, Filter{States: []draft.State{draft.Proposed, draft.Failed}}); len(got) != 0 {
		t.Fatalf("listed %+v", got)
	}
	e.clock.now = t0.Add(time.Hour)
	e.svc.Tick(ctx)
	e.svc.Send(ctx)
	if len(e.fx.published) != 0 {
		t.Fatalf("published %v", e.fx.published)
	}

	auto := Proposal{Unit: thread, Kind: draft.Nudge, Text: "ping", Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge", Key: "k1"}}
	first := e.propose(auto)
	if again, err := e.svc.Propose(ctx, auto); !errors.Is(err, ErrDuplicate) || again.ID != first.ID {
		t.Fatalf("a second sweep observed the same key again: %+v %v", again, err)
	}
}

func TestAutoApprovalHonorsTheGate(t *testing.T) {
	t.Run("disabled plugin at the window's end", func(t *testing.T) {
		e := newEnv()
		withPolicy(e, window(time.Minute))
		d := e.propose(reply("x"))
		e.disabled["slack"] = true
		e.clock.now = t0.Add(2 * time.Minute)
		if n, err := e.svc.Tick(ctx); n != 0 || err != nil {
			t.Fatalf("tick %d %v", n, err)
		}
		e.svc.Send(ctx)
		if got := e.get(d.ID); got.State != draft.Proposed || len(e.fx.published) != 0 {
			t.Fatalf("%s, published %v", got.State, e.fx.published)
		}
		if !slices.ContainsFunc(e.logs, func(l string) bool {
			return strings.Contains(l, fmt.Sprintf("draft %d", d.ID)) && strings.Contains(l, "disabled")
		}) {
			t.Fatalf("logs %q", e.logs)
		}
		e.disabled["slack"] = false
		if n, _ := e.svc.Tick(ctx); n != 1 {
			t.Fatalf("tick after re-enabling %d", n)
		}
	})
	t.Run("validator refuses at the window's end", func(t *testing.T) {
		e := newEnv()
		v := validating{e.fx}
		e.svc.Effects = func(unit.ProviderID) (port.Effects, error) { return v, nil }
		withPolicy(e, window(time.Minute))
		d := e.propose(reply("x"))
		e.fx.refuse = errors.New("channel archived")
		e.clock.now = t0.Add(2 * time.Minute)
		if n, _ := e.svc.Tick(ctx); n != 0 || e.get(d.ID).State != draft.Proposed {
			t.Fatalf("tick %d, %s", n, e.get(d.ID).State)
		}
		if !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "channel archived") }) {
			t.Fatalf("logs %q", e.logs)
		}
	})
	t.Run("none on a disabled plugin stores nothing", func(t *testing.T) {
		e := newEnv()
		withPolicy(e, none)
		e.disabled["slack"] = true
		if _, err := e.svc.Propose(ctx, reply("x")); !errors.Is(err, ErrDisabled) {
			t.Fatalf("propose: %v", err)
		}
		if got, _ := e.store.List(ctx, Filter{}); len(got) != 0 {
			t.Fatalf("stored %+v", got)
		}
	})
}

func TestObservedKeyRecordsAgainAfterRepeatAfter(t *testing.T) {
	e := newEnv()
	withPolicy(e, observe)
	e.svc.RepeatAfter = 24 * time.Hour
	auto := Proposal{Unit: thread, Kind: draft.Nudge, Text: "ping", Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge", Key: "k1"}}
	first := e.propose(auto)
	e.clock.now = t0.Add(23 * time.Hour)
	if again, err := e.svc.Propose(ctx, auto); !errors.Is(err, ErrDuplicate) || again.ID != first.ID {
		t.Fatalf("within RepeatAfter: %+v %v", again, err)
	}
	e.clock.now = t0.Add(24 * time.Hour)
	second, err := e.svc.Propose(ctx, auto)
	if err != nil || second.ID == first.ID || second.Reason != string(draft.DryRun) {
		t.Fatalf("after RepeatAfter: %+v %v", second, err)
	}
}

func TestDraftsTargetingOutsideTheirUnitAlwaysWait(t *testing.T) {
	for _, kind := range []draft.OriginKind{draft.OriginTurn, draft.OriginSession} {
		for name, a := range map[string]*draft.Policy{"none": none, "window": window(time.Minute)} {
			t.Run(string(kind)+" "+name, func(t *testing.T) {
				e := newEnv()
				withPolicy(e, a)
				p := reply("x")
				p.Origin.Kind, p.Target = kind, "#general"
				d := e.propose(p)
				if d.Approval != waiting || d.State != draft.Proposed || !d.Deadline.IsZero() ||
					d.Would.Policy != draft.WouldWait || d.Would.Reason != "targets outside its unit" {
					t.Fatalf("%+v", d)
				}
				e.clock.now = t0.Add(time.Hour)
				e.svc.Tick(ctx)
				e.svc.Send(ctx)
				if len(e.fx.published) != 0 || e.get(d.ID).State != draft.Proposed {
					t.Fatalf("published %v, %s", e.fx.published, e.get(d.ID).State)
				}
			})
		}
	}
	t.Run("explicit approval", func(t *testing.T) {
		e := newEnv()
		p := reply("x")
		p.Target, p.Policy = "U0OTHER", none
		if d := e.propose(p); d.Approval != waiting || d.State != draft.Proposed {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("observe still records", func(t *testing.T) {
		e := newEnv()
		withPolicy(e, observe)
		p := reply("x")
		p.Target = "#general"
		if d := e.propose(p); d.State != draft.Discarded || d.Would.Policy != draft.WouldWait {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("a target naming its own unit", func(t *testing.T) {
		e := newEnv()
		withPolicy(e, none)
		p := reply("x")
		p.Unit, p.Target = pull, "O/R#1"
		if d := e.propose(p); d.State != draft.Approved {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("auto drafts keep their rule's policy", func(t *testing.T) {
		e := newEnv()
		p := Proposal{Unit: thread, Kind: draft.Nudge, Text: "ping", Target: "U0OTHER", Policy: none,
			Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge", Key: "k1"}}
		if d := e.propose(p); d.State != draft.Approved {
			t.Fatalf("%+v", d)
		}
	})
}
