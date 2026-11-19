package builtin

import (
	"testing"

	"github.com/wloophq/wloop/internal/intent/expr"
)

func TestDraftViewEnvHoldsDraftFacts(t *testing.T) {
	env := DraftViewEnv()
	want := map[string]expr.Type{
		"draft.proposer": expr.String, "draft.workflow": expr.String, "draft.kind": expr.String,
		"draft.delivery": expr.String, "draft.provider": expr.String, "draft.state": expr.String,
		"draft.where": expr.String, "draft.people": expr.List, "draft.created_at": expr.Time,
	}
	for name, typ := range want {
		if got, ok := env[name]; !ok || got != typ {
			t.Errorf("%s = %v (present %v), want %v", name, got, ok, typ)
		}
	}
	for _, name := range env.Names() {
		if _, ok := want[name]; !ok {
			t.Errorf("DraftViewEnv holds %s", name)
		}
	}
	approval := ApprovalEnv(false)
	for _, name := range AutoOnly {
		if _, ok := approval[name]; ok {
			t.Errorf("ApprovalEnv(false) holds %s", name)
		}
	}
	for _, name := range []string{"draft.where", "draft.people", "draft.kind"} {
		if _, ok := approval[name]; ok {
			t.Errorf("ApprovalEnv(false) holds %s", name)
		}
	}
}
