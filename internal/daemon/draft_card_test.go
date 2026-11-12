package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	intentload "github.com/wloophq/wloop/internal/intent"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

const reviewPayload = `{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":42,"side":"RIGHT","body":"Return the error."},` +
	`{"path":"api/retry.go","start_line":50,"line":58,"body":"Back off\nbetween tries.","extra":true}]}`

// reviewKinds is the GitHub manifest's review kind, with the schema it ships, as the manifest loader reads it.
type reviewKinds struct{}

func (k reviewKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	if provider != "github" || kind != "review" {
		return draft.KindSpec{}, false
	}
	schema, err := os.ReadFile("../../plugins/github/drafts/review/schema.json")
	if err != nil {
		return draft.KindSpec{}, false
	}
	return draft.KindSpec{Provider: "github", Name: "review", Delivery: "review", Schema: "drafts/review/schema.json", Basis: draft.BasisAgent,
		Editable: []string{"text", "/event"}, SchemaJSON: schema}, true
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
	r.s.deps.DraftKinds = reviewKinds{}
	d := reviewDraftOf(draft.Sent)
	d.By = draft.Actor{Kind: draft.ActorSession, Session: "s-1"}
	v := r.s.draftView(context.Background(), d, gate.Gate{})
	if v.By != "session" || v.Author == "" || v.Author != r.s.loaded().Gates.Drafts.Authors["session"] {
		t.Errorf("by %q author %q, want the session and the profile's words for it", v.By, v.Author)
	}
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

// eventEditable are kinds whose payload's /event a person may change too.
type eventEditable struct{ outboxtest.Shapes }

func (k eventEditable) Lookup(provider, kind string) (draft.KindSpec, bool) {
	spec, ok := k.Shapes.Lookup(provider, kind)
	spec.Editable = append(spec.Editable, "/event")
	return spec, ok
}

// TestCardEditPayloadMakesNewRev edits a held draft's payload at the rev the person saw, as a person, through
// the edit verb and through an approval that changes it; a payload edit without the rev is refused.
func TestCardEditPayloadMakesNewRev(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.o.Kinds = eventEditable{outboxtest.Shapes{}}
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
	if code, body := edit(map[string]string{"payload": `{"event":`, "rev": "1"}); code != 400 {
		t.Fatalf("a payload that is no JSON: %d %s", code, body)
	}
	if code, body := edit(map[string]string{"payload": `{"event":"COMMENT"}`, "rev": "1"}); code != 200 {
		t.Fatalf("edit: %d %s", code, body)
	}
	stored, _ := r.o.Store.Get(context.Background(), d.ID)
	if len(seen) != 1 || seen[0].Payload == nil || string(*seen[0].Payload) != `{"event":"COMMENT"}` || seen[0].Rev != 1 ||
		seen[0].By.Kind != draft.ActorPerson || stored.Rev != 2 {
		t.Fatalf("amended %+v, stored rev %d: want the payload at rev 1 by a person, and rev 2", seen, stored.Rev)
	}
	g, _, _ := r.o.GateOf(context.Background(), stored)
	body, _ := json.Marshal(api.TaskRequest{Args: map[string]string{"rev": "2", "payload": `{"event":"APPROVE"}`}, Source: "cli"})
	if rec := post(t, r.s, api.GatesPath+"/"+strconv.FormatInt(g.ID, 10)+"/approve", string(body)); rec.Code != 200 {
		t.Fatalf("approve with a payload: %d %s", rec.Code, rec.Body.String())
	}
	if len(seen) != 2 || string(*seen[1].Payload) != `{"event":"APPROVE"}` || seen[1].Rev != 2 || seen[1].By.Kind != draft.ActorPerson {
		t.Fatalf("approval amended %+v, want the payload at rev 2 by a person", seen)
	}
	if sent, _ := r.o.Store.Get(context.Background(), d.ID); sent.State != draft.Queued || sent.Rev <= 2 {
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
		Rules: []draft.Rule{{When: alwaysHolds{}, Approval: draft.Approval{Mode: draft.Send}, Names: draft.Delivery("comment")}}}
	byRule, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Kind("reply"), Text: "LGTM with one nit",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "rule"}, Policy: rule})
	if err != nil {
		t.Fatal(err)
	}
	byHand := r.propose(t, r7u, "approved by hand", draft.Wait)
	if code, _, body := r.act(t, byHand.ID, "approve", fmt.Sprintf(`{"rev":%d}`, byHand.Rev)); code != 200 {
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
		notices[0].Text != "Posted by one of your approval rules to acme/api#7." {
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

// BenchmarkDraftCardFields is the cost the card's fields add to a page of 500 review drafts: each payload decoded,
// worded and checked for its editable fields.
func BenchmarkDraftCardFields(b *testing.B) {
	r := newOutboxRig(b)
	r.s.deps.DraftKinds = reviewKinds{}
	d := reviewDraftOf(draft.Sent)
	for b.Loop() {
		for range 500 {
			var v api.Draft
			r.s.cardFields(context.Background(), d, gate.Gate{}, &v)
		}
	}
}

// agentRevised is a held draft a person saw at rev 1 under gate seen, which an agent then revised to rev 2.
func agentRevised(t *testing.T, r *outboxRig) (draft.Draft, int64) {
	t.Helper()
	ctx := context.Background()
	d := r.propose(t, r7u, "Original", draft.Wait)
	seen, _, err := r.o.GateOf(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	text := "Agent revision"
	if d, err = r.o.Amend(ctx, d.ID, outbox.Amendment{Text: &text, Rev: d.Rev}); err != nil || d.Rev != 2 {
		t.Fatalf("agent revision: %+v %v", d, err)
	}
	return d, seen.ID
}

func taskArgs(args map[string]string) string {
	body, _ := json.Marshal(api.TaskRequest{Args: args, Source: "cli"})
	return string(body)
}

// TestGateApproveEditWithoutRevRefused holds review focus 5 on the gate route: an approval that changes the
// draft needs the rev the person saw, and a gate an agent's revision superseded resolves nothing, so the
// agent's revision is never replaced unseen.
func TestGateApproveEditWithoutRevRefused(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d, seen := agentRevised(t, r)
	path := api.GatesPath + "/" + strconv.FormatInt(seen, 10) + "/approve"
	for _, c := range []struct {
		args map[string]string
		code int
	}{
		{map[string]string{"text": "Person's edit of rev 1"}, 400},
		{map[string]string{"payload": `{"event":"COMMENT"}`}, 400},
		{map[string]string{"text": "Person's edit of rev 1", "rev": "1"}, 409},
		{map[string]string{"text": "Person's edit of rev 1", "rev": "2"}, 409},
	} {
		if rec := post(t, r.s, path, taskArgs(c.args)); rec.Code != c.code {
			t.Errorf("approve %v on the superseded gate: %d %s, want %d", c.args, rec.Code, rec.Body.String(), c.code)
		}
	}
	stored, _ := r.o.Store.Get(context.Background(), d.ID)
	if stored.Text != "Agent revision" || stored.State != draft.Held || stored.Rev != 2 {
		t.Fatalf("stored %q %s rev %d, want the agent's revision still held", stored.Text, stored.State, stored.Rev)
	}
}

// TestEditWithoutRevRefused refuses every edit that does not name the rev the person saw, and an edit of a rev
// an agent has since revised, so neither replaces the agent's text.
func TestEditWithoutRevRefused(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	d, _ := agentRevised(t, r)
	path := api.TaskPath("draft:"+strconv.FormatInt(int64(d.ID), 10), "edit")
	for _, c := range []struct {
		args map[string]string
		code int
	}{
		{map[string]string{"text": "Person's edit of rev 1"}, 400},
		{map[string]string{"broadcast": "true"}, 400},
		{map[string]string{"text": "Person's edit of rev 1", "rev": "1"}, 409},
	} {
		if rec := post(t, r.s, path, taskArgs(c.args)); rec.Code != c.code {
			t.Errorf("edit %v: %d %s, want %d", c.args, rec.Code, rec.Body.String(), c.code)
		}
	}
	if stored, _ := r.o.Store.Get(context.Background(), d.ID); stored.Text != "Agent revision" {
		t.Fatalf("stored %q, want the agent's revision", stored.Text)
	}
	if rec := post(t, r.s, path, taskArgs(map[string]string{"text": "Seen and changed", "rev": "2"})); rec.Code != 200 {
		t.Fatalf("an edit at the rev seen: %d %s", rec.Code, rec.Body.String())
	}
}

// lockedShapes are the outbox's kinds with a reply's text locked, as lockedKinds declare it.
type lockedShapes struct{ outboxtest.Shapes }

func (k lockedShapes) Lookup(provider, kind string) (draft.KindSpec, bool) {
	spec, ok := k.Shapes.Lookup(provider, kind)
	if kind == "reply" {
		spec.Editable = nil
	}
	return spec, ok
}

// lockedKinds declares a reply locked as GitHub's resolve is, its text no person's to change, and its review, whose text and /event are a
// person's to change.
type lockedKinds struct{ reviewKinds }

func (k lockedKinds) Lookup(provider, kind string) (draft.KindSpec, bool) {
	if kind == string(draft.Kind("reply")) {
		return draft.KindSpec{Provider: provider, Name: kind, Delivery: "resolve"}, true
	}
	return k.reviewKinds.Lookup(provider, kind)
}

// TestEditRefusesWhatTheKindLocks refuses a person's text on a kind whose editable lacks text, through the edit
// verb and an approval, and a payload change outside the kind's editable pointers; the card says the text is
// locked.
func TestEditRefusesWhatTheKindLocks(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.s.deps.DraftKinds = lockedKinds{}
	r.o.Kinds = lockedShapes{outboxtest.Shapes{}}
	d := r.propose(t, r7u, "thread resolved", draft.Wait)
	id := strconv.FormatInt(int64(d.ID), 10)
	if rec := post(t, r.s, api.TaskPath("draft:"+id, "edit"), taskArgs(map[string]string{"text": "a sentence nobody posts", "rev": "1"})); rec.Code != 400 {
		t.Errorf("text on a resolve draft: %d %s", rec.Code, rec.Body.String())
	}
	g, _, _ := r.o.GateOf(context.Background(), d)
	if rec := post(t, r.s, api.GatesPath+"/"+strconv.FormatInt(g.ID, 10)+"/approve", taskArgs(map[string]string{"text": "a sentence", "rev": "1"})); rec.Code != 400 {
		t.Errorf("approving a resolve draft with text: %d %s", rec.Code, rec.Body.String())
	}
	if stored, _ := r.o.Store.Get(context.Background(), d.ID); stored.Text != d.Text || stored.State != draft.Held || stored.Rev != d.Rev {
		t.Fatalf("stored %q %s, want no text, still held", stored.Text, stored.State)
	}
	if v := r.s.draftView(context.Background(), d, g); !v.TextLocked {
		t.Error("the resolve draft's card does not lock its text")
	}
	review := reviewDraftOf(draft.Held)
	for p, ok := range map[string]bool{
		`{"event":"COMMENT","comments":[{"path":"api/handler.go","line":42,"side":"RIGHT","body":"Return the error."},{"path":"api/retry.go","start_line":50,"line":58,"body":"Back off\nbetween tries.","extra":true}]}`:         true,
		`{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":43,"side":"RIGHT","body":"Return the error."},{"path":"api/retry.go","start_line":50,"line":58,"body":"Back off\nbetween tries.","extra":true}]}`: false,
		`{"event":"REQUEST_CHANGES","comments":[]}`: false,
		`{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":42,"side":"RIGHT","body":"Return the error."},{"path":"api/retry.go","start_line":50,"line":58,"body":"Back off\nbetween tries.","extra":true}],"draft":true}`: false,
	} {
		spec, _ := r.s.kindSpec(review)
		after := review
		after.Payload = json.RawMessage(p)
		if err := spec.CheckEdit(review, after); (err == nil) != ok {
			t.Errorf("payload %s: %v, want allowed %v", p, err, ok)
		}
	}
}

// TestDraftCardRendersItsKindsPreview renders the kind's preview template from the profile over the draft and
// its payload, and serves none for a kind without one or a template that does not render.
func TestDraftCardRendersItsKindsPreview(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	l := *r.s.loaded()
	l.Previews = map[string]map[string]string{"github": {
		"review": `{{.Payload.event}} on {{.Where}} with {{len .Payload.comments}} inline comments`,
		"reply":  `{{.Nope}}`,
	}}
	r.s.deps.Intents = func() *intentload.Loaded { return &l }
	held := reviewDraftOf(draft.Held)
	if got := r.s.draftView(context.Background(), held, gate.Gate{}).Preview; got != `REQUEST_CHANGES on #general in the thread "acme/api#7" with 2 inline comments` {
		t.Errorf("review preview %q", got)
	}
	reply := held
	reply.Kind = draft.Kind("reply")
	if got := r.s.draftView(context.Background(), reply, gate.Gate{}).Preview; got != "" {
		t.Errorf("a preview that does not render shows %q, want none", got)
	}
	other := held
	other.Kind = "close"
	if got := r.s.draftView(context.Background(), other, gate.Gate{}).Preview; got != "" {
		t.Errorf("a kind without a preview shows %q", got)
	}
}

// TestTheBundledReviewPreviewRendersAReviewWithNoInlineComments renders GitHub's shipped review preview over a
// payload with no comments field, which its schema leaves optional.
func TestTheBundledReviewPreviewRendersAReviewWithNoInlineComments(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	if r.s.loaded().Previews["github"]["review"] == "" {
		t.Fatal("no bundled GitHub review preview")
	}
	d := reviewDraftOf(draft.Held)
	d.Payload, d.Basis = json.RawMessage(`{"event":"COMMENT"}`), "c2abcdef0"
	if got := r.s.draftView(context.Background(), d, gate.Gate{}).Preview; got != "COMMENT review at c2abcde" {
		t.Errorf("review preview %q", got)
	}
}
