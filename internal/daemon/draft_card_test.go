package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

const reviewPayload = `{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":42,"side":"RIGHT","body":"Return the error."},` +
	`{"path":"api/retry.go","start_line":50,"line":58,"body":"Back off\nbetween tries.","extra":true}]}`

// reviewKinds is the GitHub manifest's review kind, with the schema it ships.
type reviewKinds struct{ t *testing.T }

func (k reviewKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	if provider != "github" || kind != "review" {
		return draft.KindSpec{}, false
	}
	return draft.KindSpec{Provider: "github", Name: "review", Delivery: "review", Schema: "drafts/review/schema.json", Basis: draft.BasisAgent,
		Editable: []string{"text", "/event"}}, true
}

func (k reviewKinds) Schema(spec draft.KindSpec) (json.RawMessage, error) {
	return os.ReadFile("../../plugins/github/" + spec.Schema)
}

// staleCalls is a live basis check that answers stale and counts the drafts it read.
type staleCalls struct {
	mu  sync.Mutex
	ids []draft.ID
}

func (s *staleCalls) check(_ context.Context, d draft.Draft) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, d.ID)
	return true, nil
}

func reviewDraftOf(state draft.State) draft.Draft {
	return draft.Draft{ID: 42, Unit: r7u, Kind: "review", Delivery: "review", Text: "Two findings block the merge.", State: state,
		Payload: json.RawMessage(reviewPayload), Basis: "4f9c2d1", Rev: 1}
}

func flatValues(fs []api.DraftField, out map[string]api.DraftField) {
	for _, f := range fs {
		out[f.Pointer] = f
		flatValues(f.Fields, out)
	}
}

// TestDraftCardServesEveryPayloadValue holds review focus 1 at the daemon: every value of the payload is
// served under its pointer, in the payload's order, an unknown key as itself, and only the kind's editable field
// carries its schema's enum.
func TestDraftCardServesEveryPayloadValue(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.s.deps.DraftKinds = reviewKinds{t}
	v := r.s.draftView(context.Background(), reviewDraftOf(draft.Sent), gate.Gate{})
	got := map[string]api.DraftField{}
	flatValues(v.Fields, got)
	want := map[string]string{"/event": "REQUEST_CHANGES", "/comments/0/path": "api/handler.go", "/comments/0/line": "42", "/comments/0/side": "RIGHT",
		"/comments/0/body": "Return the error.", "/comments/1/path": "api/retry.go", "/comments/1/start_line": "50", "/comments/1/line": "58",
		"/comments/1/body": "Back off\nbetween tries.", "/comments/1/extra": "true"}
	for p, val := range want {
		if got[p].Value != val {
			t.Errorf("%s serves %q, want %q", p, got[p].Value, val)
		}
	}
	if len(v.Fields) != 2 || v.Fields[0].Pointer != "/event" || v.Fields[1].Fields[1].Fields[1].Pointer != "/comments/1/start_line" {
		t.Errorf("fields out of the payload's order: %+v", v.Fields)
	}
	ev := got["/event"]
	if !ev.Editable || len(ev.Options) != 3 || ev.Options[0].Value != "APPROVE" || got["/comments/0/side"].Editable || got["/comments/0/side"].Options != nil {
		t.Errorf("event %+v, side %+v: want only the editable verdict with the schema's enum", ev, got["/comments/0/side"])
	}
	if ev.Label != "Verdict" || ev.Shown != "Request changes" || got["/comments/1/extra"].Label != "extra" {
		t.Errorf("event worded %q %q, extra %q: want the plugin's words, an unworded key as itself", ev.Label, ev.Shown, got["/comments/1/extra"].Label)
	}
	if string(v.Payload) != reviewPayload || v.Basis != "4f9c2d1" {
		t.Errorf("payload %s basis %q", v.Payload, v.Basis)
	}
}

// TestDraftCardReadsStaleLiveForHeldDraftsOnly shows a held draft with a basis stale with the profile's note,
// and reads no basis for a sent draft or one without a basis.
func TestDraftCardReadsStaleLiveForHeldDraftsOnly(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	calls := &staleCalls{}
	r.s.deps.Stale = calls.check
	held := r.s.draftView(context.Background(), reviewDraftOf(draft.Held), gate.Gate{})
	none := reviewDraftOf(draft.Held)
	none.ID, none.Basis = 43, ""
	r.s.draftView(context.Background(), none, gate.Gate{})
	r.s.draftView(context.Background(), reviewDraftOf(draft.Sent), gate.Gate{})
	if !held.Stale || len(held.Notes) != 1 || held.Notes[0] != r.s.loaded().Gates.Drafts.Stale || held.Notes[0] == "" {
		t.Errorf("held draft stale %v notes %q, want stale with the profile's note", held.Stale, held.Notes)
	}
	if len(calls.ids) != 1 || calls.ids[0] != 42 {
		t.Errorf("read the basis of %v, want only the held draft with a basis", calls.ids)
	}
	hold := reviewDraftOf(draft.Held)
	hold.Basis, hold.Hold = "", true
	if v := r.s.draftView(context.Background(), hold, gate.Gate{}); !v.Hold || len(v.Notes) != 1 || v.Notes[0] != r.s.loaded().Gates.Drafts.Hold {
		t.Errorf("held draft notes %q, want the hold note", v.Notes)
	}
}

// TestCardEditPayloadMakesNewRev edits a held draft's payload at the rev the person saw, as a person, through
// the edit verb and through an approval that changes it; a payload edit without the rev is refused.
func TestCardEditPayloadMakesNewRev(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	var seen []outbox.Amendment
	r.s.amender = func(ctx context.Context, id draft.ID, a outbox.Amendment) (draft.Draft, error) {
		seen = append(seen, a)
		return r.o.Amend(ctx, id, a)
	}
	d := r.propose(t, r7u, "Two findings", draft.Wait)
	path := api.TaskPath("draft:"+strconv.FormatInt(int64(d.ID), 10), "edit")
	edit := func(args map[string]string) (int, string) {
		body, _ := json.Marshal(api.TaskRequest{Args: args, Source: "cli"})
		rec := post(t, r.s, path, string(body))
		return rec.Code, rec.Body.String()
	}
	if code, body := edit(map[string]string{"payload": `{"event":"COMMENT"}`}); code != 400 || !strings.Contains(body, "rev") {
		t.Fatalf("a payload edit without its rev: %d %s", code, body)
	}
	if code, body := edit(map[string]string{"payload": `{"event":`, "rev": "0"}); code != 400 {
		t.Fatalf("a payload that is no JSON: %d %s", code, body)
	}
	if code, body := edit(map[string]string{"payload": `{"event":"COMMENT"}`, "rev": "0"}); code != 200 {
		t.Fatalf("edit: %d %s", code, body)
	}
	stored, _ := r.o.Store.Get(context.Background(), d.ID)
	if len(seen) != 1 || seen[0].Payload == nil || string(*seen[0].Payload) != `{"event":"COMMENT"}` || seen[0].Rev != 0 ||
		seen[0].By.Kind != draft.ActorPerson || stored.Rev != 1 {
		t.Fatalf("amended %+v, stored rev %d: want the payload at rev 0 by a person, and rev 1", seen, stored.Rev)
	}
	g, _, _ := r.o.GateOf(context.Background(), stored)
	body, _ := json.Marshal(api.TaskRequest{Args: map[string]string{"rev": "1", "payload": `{"event":"APPROVE"}`}, Source: "cli"})
	if rec := post(t, r.s, api.GatesPath+"/"+strconv.FormatInt(g.ID, 10)+"/approve", string(body)); rec.Code != 200 {
		t.Fatalf("approve with a payload: %d %s", rec.Code, rec.Body.String())
	}
	if len(seen) != 2 || string(*seen[1].Payload) != `{"event":"APPROVE"}` || seen[1].Rev != 1 || seen[1].By.Kind != draft.ActorPerson {
		t.Fatalf("approval amended %+v, want the payload at rev 1 by a person", seen)
	}
	if sent, _ := r.o.Store.Get(context.Background(), d.ID); sent.State != draft.Queued || sent.Rev <= 1 {
		t.Fatalf("approved draft %s at rev %d, want queued past the edit's rev", sent.State, sent.Rev)
	}
}

// alwaysHolds is a rule condition that holds for every draft.
type alwaysHolds struct{}

func (alwaysHolds) Holds(map[string]any, time.Time) (bool, error) { return true, nil }

// TestRuleSentNotifies tells the user that a rule of theirs posted a draft, once it is sent, and its card says
// so; a draft a person approved notifies nothing.
func TestRuleSentNotifies(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	sub := r.s.hub.subscribe("test")
	ctx := context.Background()
	rule := &draft.Policy{Default: draft.Approval{Mode: draft.Wait},
		Rules: []draft.Rule{{When: alwaysHolds{}, Approval: draft.Approval{Mode: draft.Send}, Names: draft.CommentDelivery}}}
	byRule, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Reply, Text: "LGTM with one nit",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "rule"}, Policy: rule})
	if err != nil {
		t.Fatal(err)
	}
	byHand := r.propose(t, r7u, "approved by hand", draft.Wait)
	if code, _, body := r.act(t, byHand.ID, "approve", `{"rev":0}`); code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
	if sent, failed, err := r.o.Send(ctx); err != nil || sent != 2 || failed != 0 {
		t.Fatalf("send: %d sent, %d failed, %v", sent, failed, err)
	}
	sub.mu.Lock()
	var notices []api.Notice
	if sub.pending != nil {
		notices = sub.pending.Notices
	}
	sub.mu.Unlock()
	if len(notices) != 1 || notices[0].ID != "draft:"+strconv.FormatInt(int64(byRule.ID), 10)+":by-rule" || notices[0].Unit != r7u ||
		!strings.Contains(notices[0].Text, "gates.approval rule 0") || !strings.HasPrefix(notices[0].Text, "Posted by your rule") {
		t.Fatalf("notices %+v, want one for the rule-sent draft in the profile's words", notices)
	}
	var res api.DraftsResponse
	get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(byRule.ID), 10)+","+strconv.FormatInt(int64(byHand.ID), 10), &res)
	for _, d := range res.Drafts {
		ruled := d.ID == int64(byRule.ID)
		if ruled != (d.ByRule == "gates.approval rule 0") || ruled != (len(d.Notes) == 1 && d.Notes[0] == notices[0].Text) {
			t.Errorf("draft %d by rule %q notes %q", d.ID, d.ByRule, d.Notes)
		}
	}
}

