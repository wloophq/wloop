package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// TestADrivenSessionsSendPostsAtOnce: a session the user drives writes a reply on the person's routes and asks to
// send it. The draft is the session's, stamped with it, and the user, driving the session, approves it at once
// through it, whatever their approval policy says; the answer says it goes out.
func TestADrivenSessionsSendPostsAtOnce(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"drafts", "reply"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, _, mine, _ := finalRig(t)
			var id int64
			var answer string
			switch route {
			case "drafts":
				body := fmt.Sprintf(`{"unit":{"provider":%q,"key":%q},"text":"written by the session","send":true,"source":"cockpit"}`, r7u.Provider, r7u.Key)
				rec := postFrom(t, r.s, "term_2", api.Drafts, body)
				var got api.Draft
				if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != 200 || err != nil {
					t.Fatalf("%d %s", rec.Code, rec.Body)
				}
				id, answer = got.ID, strings.Join(got.Notes, " ")
			case "reply":
				r.put(t, unitTask("t-1", task.OriginDerived, r7u))
				rec := postFrom(t, r.s, "term_2", api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"written by the session","send":"now"}}`)
				var res api.TaskResult
				if err := json.Unmarshal(rec.Body.Bytes(), &res); rec.Code != 200 || err != nil || len(res.Lines) != 1 {
					t.Fatalf("%d %s", rec.Code, rec.Body)
				}
				answer = res.Lines[0]
				ds := r.drafts(t)
				if len(ds) != 1 {
					t.Fatalf("drafts %+v", ds)
				}
				id = int64(ds[0].ID)
			}
			d, err := r.o.Store.Get(ctx, draft.ID(id))
			if err != nil {
				t.Fatal(err)
			}
			g, _, _ := r.o.GateOf(ctx, d)
			if d.Origin.Session != mine || d.By != (draft.Actor{Kind: draft.ActorSession, Session: mine}) || g.Session != mine {
				t.Fatalf("the session's draft is %+v by %+v, its gate stamps %q", d.Origin, d.By, g.Session)
			}
			if d.State != draft.Queued || g.Status != gate.Approved || g.By != task.ActorAgent || g.BySession != mine || !strings.Contains(answer, "goes out") {
				t.Fatalf("sent: %s, gate %+v, answer %q", d.State, g, answer)
			}
		})
	}
}
