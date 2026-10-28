package outbox

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// postResults attaches for the intents it lists and records each attach.
type postResults struct {
	intents  []string
	attached []unit.Unit
}

func (r *postResults) AttachesPosts(intent string) bool {
	for _, i := range r.intents {
		if i == intent {
			return true
		}
	}
	return false
}

func (r *postResults) Attach(_ context.Context, tk task.ID, u unit.Unit) error {
	if tk != "e-1" {
		panic("attached to " + tk)
	}
	r.attached = append(r.attached, u)
	return nil
}

func fromTask(p Proposal) Proposal {
	p.Origin = draft.Origin{Kind: draft.OriginSession, TurnSeq: 3, Intent: "begin", Unit: task.UnitOf("e-1")}
	return p
}

func TestARecordedPostAttachesItsUnitToTheTurnsTask(t *testing.T) {
	e := newEnv()
	r := &postResults{intents: []string{"begin"}}
	e.svc.Results = r
	if _, err := e.svc.RecordSent(ctx, fromTask(reply("shipped")), draft.Evidence{Ref: "1.3"}); err != nil {
		t.Fatal(err)
	}
	if len(r.attached) != 1 || r.attached[0] != thread {
		t.Fatalf("attached = %v, want the thread", r.attached)
	}
}

func TestASentPostAttachesItsUnitToTheTurnsTask(t *testing.T) {
	e := newEnv()
	r := &postResults{intents: []string{"begin"}}
	e.svc.Results = r
	d := e.propose(fromTask(reply("shipped")))
	if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.attached) != 0 {
		t.Fatalf("attached before the send: %v", r.attached)
	}
	if _, _, err := e.svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.attached) != 1 || r.attached[0] != thread {
		t.Fatalf("attached = %v, want the thread", r.attached)
	}
}

func TestOnlyAPostOfAnAttachingTaskTurnAttaches(t *testing.T) {
	react := fromTask(Proposal{Unit: thread, Kind: draft.React, Reaction: "eyes", Target: "https://example.test/p1"})
	edit := fromTask(Proposal{Unit: thread, Kind: draft.Edit, Text: "fixed", Replaced: "fixd", Target: "https://example.test/p1"})
	other := fromTask(reply("hi"))
	other.Origin.Intent = "reply"
	onUnit := fromTask(reply("hi"))
	onUnit.Origin.Unit = unit.Unit{}
	auto := reply("hi")
	auto.Origin = draft.Origin{Kind: draft.OriginAuto, Rule: "nudge", Key: "k", Unit: task.UnitOf("e-1")}
	for name, p := range map[string]Proposal{"a reaction": react, "an edit": edit, "an intent that does not attach": other,
		"a turn on the post's own unit": onUnit, "a workflow's draft": auto} {
		t.Run(name, func(t *testing.T) {
			e := newEnv()
			r := &postResults{intents: []string{"begin"}}
			e.svc.Results = r
			if _, err := e.svc.RecordSent(ctx, p, draft.Evidence{Ref: "1.3"}); err != nil {
				t.Fatal(err)
			}
			if len(r.attached) != 0 {
				t.Fatalf("attached = %v, want nothing", r.attached)
			}
		})
	}
}

// landing is a publisher that takes "#" targets and says which unit its post landed in.
type landing struct {
	taking
	landed unit.Unit
}

func (l landing) Landed(context.Context, draft.Draft) (unit.Unit, bool, error) {
	return l.landed, true, nil
}

func TestADraftOnTheTaskAttachesTheUnitItsPostLandedIn(t *testing.T) {
	e := newEnv()
	posted := unit.Unit{Provider: "chat", Key: "T1/C1/1.5"}
	chat := &fakeEffects{}
	e.svc.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if p == "chat" {
			return landing{taking{chat, func(t string) bool { return t == "#general" }}, posted}, nil
		}
		return nil, ErrDisabled
	}
	e.svc.Targets = []unit.ProviderID{"chat"}
	r := &postResults{intents: []string{"begin"}}
	e.svc.Results = r
	p := onTask("#general")
	p.Unit = task.UnitOf("e-1")
	p.Origin = draft.Origin{Kind: draft.OriginSession, TurnSeq: 3, Intent: "begin"}
	d := e.propose(p)
	if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.attached) != 1 || r.attached[0] != posted {
		t.Fatalf("attached = %v, want %v", r.attached, posted)
	}
}
