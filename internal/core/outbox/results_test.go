package outbox

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
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

func (r *postResults) Attach(_ context.Context, entry gtd.ID, u unit.Unit) error {
	if entry != "e-1" {
		panic("attached to " + entry)
	}
	r.attached = append(r.attached, u)
	return nil
}

func fromEntry(p Proposal) Proposal {
	p.Origin = draft.Origin{Kind: draft.OriginSession, TurnSeq: 3, Intent: "begin", Unit: gtd.UnitOf("e-1")}
	return p
}

func TestARecordedPostAttachesItsUnitToTheTurnsEntry(t *testing.T) {
	e := newEnv()
	r := &postResults{intents: []string{"begin"}}
	e.svc.Results = r
	if _, err := e.svc.RecordSent(ctx, fromEntry(reply("shipped")), draft.Evidence{Ref: "1.3"}); err != nil {
		t.Fatal(err)
	}
	if len(r.attached) != 1 || r.attached[0] != thread {
		t.Fatalf("attached = %v, want the thread", r.attached)
	}
}

func TestASentPostAttachesItsUnitToTheTurnsEntry(t *testing.T) {
	e := newEnv()
	r := &postResults{intents: []string{"begin"}}
	e.svc.Results = r
	d := e.propose(fromEntry(reply("shipped")))
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

func TestOnlyAPostOfAnAttachingEntryTurnAttaches(t *testing.T) {
	react := fromEntry(Proposal{Unit: thread, Kind: draft.React, Reaction: "eyes", Target: "https://example.test/p1"})
	edit := fromEntry(Proposal{Unit: thread, Kind: draft.Edit, Text: "fixed", Replaced: "fixd", Target: "https://example.test/p1"})
	other := fromEntry(reply("hi"))
	other.Origin.Intent = "reply"
	onUnit := fromEntry(reply("hi"))
	onUnit.Origin.Unit = unit.Unit{}
	auto := reply("hi")
	auto.Origin = draft.Origin{Kind: draft.OriginAuto, Rule: "nudge", Key: "k", Unit: gtd.UnitOf("e-1")}
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
