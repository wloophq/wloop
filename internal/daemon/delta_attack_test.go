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
	"github.com/wloophq/wloop/internal/domain/unit"
)

// releasesComments is an approval policy of the user's that sends every comment by a rule naming it.
type releasesComments struct{}

func (releasesComments) For(context.Context, unit.Unit, draft.Origin) (draft.Policy, error) {
	return draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{{When: alwaysHolds{}, Names: "comment", Approval: draft.Approval{Mode: draft.Send}}}}, nil
}

// TestADrivenSessionsSendWaitsForTheUsersPolicy is DELTA-D1 inverted: a session the user drives writes a reply
// on the person's routes and asks to send it. The draft is the session's, its gate stamps the session, which
// cannot approve it; under the default policy it waits, and the answer says so; a rule of the user's that
// releases it sends it, and the answer says that.
func TestADrivenSessionsSendWaitsForTheUsersPolicy(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"drafts", "reply"} {
		for _, rule := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, a rule %v", route, rule), func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				r, _, mine, _ := finalRig(t)
				if rule {
					r.o.Policies = releasesComments{}
				}
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
				if !rule {
					if d.State != draft.Held || g.Status != gate.Open || !strings.Contains(answer, "waits") {
						t.Fatalf("under the default policy: %s, gate %s, answer %q", d.State, g.Status, answer)
					}
					rec := postFrom(t, r.s, "term_2", fmt.Sprintf("/api/gates/%d/approve", g.ID), fmt.Sprintf(`{"source":"claude","args":{"rev":"%d"}}`, d.Rev))
					if got, _ := r.o.Store.Get(ctx, d.ID); rec.Code == 200 || got.State != draft.Held {
						t.Fatalf("session %s approved the draft it wrote: %d %s", mine, rec.Code, rec.Body)
					}
					return
				}
				if d.State != draft.Queued || g.By != task.ActorWorkflow || !strings.Contains(answer, "rule") {
					t.Fatalf("under a releasing rule: %s, gate by %s rule %q, answer %q", d.State, g.By, g.Rule, answer)
				}
			})
		}
	}
}
