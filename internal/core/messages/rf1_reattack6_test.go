package messages_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestReattackSessionsThroughTheHook(t *testing.T) {
	for _, s := range []string{"stranger", "term_1", "reviser", "driver"} {
		r, od, m := draftRig(t, "driver", "reviser", "term_1")
		ctx := context.Background()
		open, _ := r.gateStore.Where(ctx, gates.Query{Kind: m.Kind.Gate(), Unit: m.Unit})
		i := slices.IndexFunc(open, func(g gate.Gate) bool { return g.Subject == strconv.FormatInt(m.ID, 10) })
		rerr := r.gates.Resolve(messages.WithDelivery(ctx, &messages.Delivery{}), open[i].ID,
			gates.Resolution{Intent: "answer", Answer: "approve", By: task.ActorAgent, Session: s})
		t.Logf("%s: err %v, draft gate %s, approvals %+v", s, rerr, od.gateOf(42).Status, od.approvals)
		if s != "driver" && od.gateOf(42).Status == gate.Approved {
			t.Errorf("%s approved draft 42", s)
		}
	}
}
