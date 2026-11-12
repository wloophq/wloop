package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// DELTA-D1: a session the user drives writes a reply on a person's route and sends it at once: the session
// that wrote the draft resolves its gate.
func TestDeltaAttackDrivenSessionSendsItsOwnReply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _, mine, _ := finalRig(t)
	body := fmt.Sprintf(`{"unit":{"provider":%q,"key":%q},"text":"written and sent by the session","send":true,"source":"cockpit"}`, r7u.Provider, r7u.Key)
	rec := postFrom(t, r.s, "term_2", "/api/drafts", body)
	t.Logf("%d %s", rec.Code, rec.Body)
	var got struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID == 0 {
		return
	}
	d, err := r.o.Store.Get(ctx, draft.ID(got.ID))
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := r.o.GateOf(ctx, d)
	if d.State == draft.Queued || d.State == draft.Sent {
		t.Errorf("session %s wrote draft %d and approved it: %s, gate by %s/%s session=%q sessions=%v, draft by %+v", mine, d.ID, d.State, g.By, g.BySession, g.Session, g.Sessions, d.By)
	}
}
