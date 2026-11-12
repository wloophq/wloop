package draft

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAnAgentsDraftIsOneATurnOrASessionProposed(t *testing.T) {
	t.Parallel()
	for kind, want := range map[OriginKind]bool{OriginTurn: true, OriginSession: true, OriginAuto: false, OriginUser: false, "": false} {
		if got := (Origin{Kind: kind}).ByAgent(); got != want {
			t.Errorf("%q: %v, want %v", kind, got, want)
		}
	}
}

func TestADraftConcernsItsUnitAndItsTurnsUnit(t *testing.T) {
	t.Parallel()
	pr, thread, other := unit.Unit{Provider: "github", Key: "acme/api#7"}, unit.Unit{Provider: "slack", Key: "C0123/1.2"}, unit.Unit{Provider: "github", Key: "acme/api#8"}
	d := Draft{Unit: thread, Origin: Origin{Kind: OriginTurn, Unit: pr}}
	if !d.Concerns(thread) || !d.Concerns(pr) || d.Concerns(other) {
		t.Fatalf("concerns %v %v %v", d.Concerns(thread), d.Concerns(pr), d.Concerns(other))
	}
	if (Draft{Unit: thread}).Concerns(unit.Unit{}) {
		t.Fatal("a draft with no turn's unit concerns the zero unit")
	}
}

func TestTheProposerOfAnOrigin(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		o    Origin
		want Actor
	}{
		"a session":                {Origin{Kind: OriginSession, Session: "s1"}, Actor{Kind: ActorSession, Session: "s1"}},
		"a headless turn":          {Origin{Kind: OriginTurn}, Actor{Kind: ActorSession}},
		"a person's, from a shell": {Origin{Kind: OriginUser, Session: "s2"}, Actor{Kind: ActorSession, Session: "s2"}},
		"a person's":               {Origin{Kind: OriginUser}, Actor{Kind: ActorPerson}},
		"a rule's":                 {Origin{Kind: OriginAuto, Rule: "r"}, Actor{Kind: ActorRule}},
	} {
		if got := ProposerOf(c.o); got != c.want {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}
