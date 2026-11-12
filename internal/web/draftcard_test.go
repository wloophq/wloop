package web

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// reviewDraft is a held review draft of acme/api#7 as the daemon serves it: a verdict a person may change, and
// two inline comments.
func reviewDraft() api.Draft {
	d := pendingDraft()
	d.Unit, d.Kind, d.Purpose, d.Delivery, d.Where = "github/acme/api#7", "review", "", "review", "acme/api#7"
	d.Text, d.Effects, d.Basis = "Two findings block the merge.", []string{}, "4f9c2d1"
	d.Payload = json.RawMessage(`{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":42,"side":"RIGHT","body":"Return the error."},` +
		`{"path":"api/retry.go","start_line":50,"line":58,"body":"Back off\nbetween tries."}]}`)
	comment := func(i string, fs ...api.DraftField) api.DraftField {
		return api.DraftField{Pointer: "/comments/" + i, Label: i, Fields: fs}
	}
	leaf := func(p, label, v string) api.DraftField { return api.DraftField{Pointer: p, Label: label, Value: v} }
	d.Fields = []api.DraftField{
		{Pointer: "/event", Label: "Verdict", Value: "REQUEST_CHANGES", Shown: "Request changes", Editable: true,
			Options: []api.DraftOption{{Value: "APPROVE", Label: "Approve"}, {Value: "REQUEST_CHANGES", Label: "Request changes"}, {Value: "COMMENT", Label: "Comment"}}},
		{Pointer: "/comments", Label: "Inline comments", Fields: []api.DraftField{
			comment("1", leaf("/comments/0/path", "File", "api/handler.go"), leaf("/comments/0/line", "Line", "42"),
				leaf("/comments/0/side", "Side", "RIGHT"), leaf("/comments/0/body", "Comment", "Return the error.")),
			comment("2", leaf("/comments/1/path", "File", "api/retry.go"), leaf("/comments/1/start_line", "start_line", "50"),
				leaf("/comments/1/line", "Line", "58"), leaf("/comments/1/body", "Comment", "Back off\nbetween tries.")),
		}},
	}
	return d
}

// leaves are the scalar values of fs, each as its card must show it.
func leaves(fs []api.DraftField) []string {
	var out []string
	for _, f := range fs {
		if len(f.Fields) > 0 {
			out = append(out, leaves(f.Fields)...)
			continue
		}
		out = append(out, f.Value)
	}
	return out
}

// TestCardShowsFullPayload holds review focus 1 on the card: every value the draft posts shows, the multi-line
// one whole, under its field's word, with the raw value of a worded one kept beside its word.
func TestCardShowsFullPayload(t *testing.T) {
	var got struct {
		Values, Labels []string
		Text           string
	}
	d := reviewDraft()
	d.By, d.Author = "session", "Last written by an agent"
	runGate(t, `
const c = draft(input.draft);
const shown = cls(c, "pv");
done({ Values: shown.map((x) => attr(x, "data-value")), Labels: walk(c, (x) => x.tag === "dt").map((x) => x.textContent), Text: c.textContent });`,
		map[string]any{"draft": d}, &got)
	if want := leaves(d.Fields); !slices.Equal(got.Values, want) {
		t.Errorf("the card shows the values\n %q\nwant every value the draft posts\n %q", got.Values, want)
	}
	for _, s := range []string{"Last written by an agent", "Request changes", "Back off\nbetween tries.", "api/retry.go", "Two findings block the merge."} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("the card lacks %q: %q", s, got.Text)
		}
	}
	if !slices.Contains(got.Labels, "Verdict") || !slices.Contains(got.Labels, "Inline comments") || !slices.Contains(got.Labels, "start_line") {
		t.Errorf("labels %q, want the plugin's words and an unworded key as itself", got.Labels)
	}
}

// TestCardEditableFromSchema edits the verdict through the control its schema's enum gives, and approves the
// draft with the edited payload at the rev it showed; a field the kind does not make editable has no control.
func TestCardEditableFromSchema(t *testing.T) {
	var got struct {
		Before   int
		Options  []string
		Selected []string
		Controls []string
		Posts    []map[string]any
	}
	runGate(t, `
const controls = (c) => walk(c, (x) => ["select", "input"].includes(x.tag) && String(x.attrs.class || "").includes("d-field"));
const before = controls(draft(input.draft)).length;
await focus(draft(input.draft), "outbox:open:approve:draft:7").on.click();
let c = draft(input.draft);
const sel = controls(c)[0];
const options = walk(sel, (x) => x.tag === "option");
await sel.on.change({ target: { value: "COMMENT" } });
c = draft(input.draft);
await focus(c, "outbox:approve:draft:7").on.click();
done({ Before: before, Options: options.map((o) => attr(o, "value") + "=" + o.textContent),
  Selected: options.filter((o) => "selected" in o.attrs).map((o) => attr(o, "value")),
  Controls: controls(c).map((x) => attr(x, "aria-label")), Posts: posts });`, map[string]any{"draft": reviewDraft()}, &got)
	if got.Before != 0 {
		t.Errorf("%d controls before Edit, want none", got.Before)
	}
	if !slices.Equal(got.Options, []string{"APPROVE=Approve", "REQUEST_CHANGES=Request changes", "COMMENT=Comment"}) || !slices.Equal(got.Selected, []string{"REQUEST_CHANGES"}) {
		t.Errorf("options %q, selected %q: want the schema's enum in its words, the current verdict picked", got.Options, got.Selected)
	}
	if !slices.Equal(got.Controls, []string{"Verdict"}) {
		t.Errorf("controls %q, want only the editable verdict", got.Controls)
	}
	if len(got.Posts) != 1 || got.Posts[0]["path"] != "/api/gates/12/approve" {
		t.Fatalf("posts %v, want one approval through the gate", got.Posts)
	}
	body := got.Posts[0]["body"].(map[string]any)["args"].(map[string]any)
	var payload map[string]any
	if err := json.Unmarshal([]byte(body["payload"].(string)), &payload); err != nil {
		t.Fatalf("payload %v: %v", body["payload"], err)
	}
	if payload["event"] != "COMMENT" || len(payload["comments"].([]any)) != 2 || body["rev"] != "3" || body["text"] != "Two findings block the merge." {
		t.Errorf("approved with %v, want the edited verdict, the comments untouched, the text and rev 3", body)
	}
}

// TestCardStaleDisablesApprove shows why a stale draft cannot be approved, offers no approval, and keeps edit and
// discard.
func TestCardStaleDisablesApprove(t *testing.T) {
	var got struct {
		Notes        []string
		Off, On      []string
		ApproveTitle string
		Posts        []any
	}
	d := reviewDraft()
	d.Stale, d.Notes = true, []string{"Written against an older head of the pull request."}
	runGate(t, `
const c = draft(input.draft);
const buttons = walk(c, (x) => x.tag === "button");
const approve = focus(c, "outbox:approve:draft:7");
if (approve && !("disabled" in approve.attrs)) await approve.on.click();
done({ Notes: cls(c, "d-note").map((x) => x.textContent), Off: buttons.filter((b) => "disabled" in b.attrs).map((b) => b.textContent),
  On: buttons.filter((b) => !("disabled" in b.attrs)).map((b) => b.textContent), ApproveTitle: approve ? attr(approve, "title") : "", Posts: posts });`,
		map[string]any{"draft": d}, &got)
	if !slices.Equal(got.Notes, d.Notes) {
		t.Errorf("notes %q, want %q", got.Notes, d.Notes)
	}
	if len(got.Off) != 1 || got.Off[0] != "approve" || got.ApproveTitle != d.Notes[0] {
		t.Errorf("disabled %q titled %q, want approve off with the stale note", got.Off, got.ApproveTitle)
	}
	if len(got.On) != 2 || len(got.Posts) != 0 {
		t.Errorf("enabled %q, posted %v: want edit and discard, nothing posted", got.On, got.Posts)
	}
}

// TestCardRuleSentNotice shows that a rule of the user's posted a sent draft, with the payload it posted.
func TestCardRuleSentNotice(t *testing.T) {
	var got struct {
		Notes, Buttons []string
		Values         int
	}
	d := reviewDraft()
	d.State, d.Gate, d.Intents, d.Notes = "sent", 0, nil, []string{"Posted by your rule: gates.approval rule 0."}
	runGate(t, `
const c = draft(input.draft);
done({ Notes: cls(c, "d-note").map((x) => x.textContent), Buttons: walk(c, (x) => x.tag === "button").map((b) => b.textContent),
  Values: cls(c, "pv").length });`, map[string]any{"draft": d}, &got)
	if !slices.Equal(got.Notes, d.Notes) {
		t.Errorf("notes %q, want the rule's notice", got.Notes)
	}
	if len(got.Buttons) != 0 || got.Values != len(leaves(d.Fields)) {
		t.Errorf("buttons %q, %d values: want none, and the payload it posted", got.Buttons, got.Values)
	}
}

// TestCardLocksTextItsKindLocks offers no text editor on a draft whose kind locks its text, no Edit at all when
// nothing of it is editable, and approves an edited field without sending a text.
func TestCardLocksTextItsKindLocks(t *testing.T) {
	var got struct {
		Bare      []string
		Textareas int
		Selects   int
		Posts     []map[string]any
	}
	bare := pendingDraft()
	bare.Kind, bare.Text, bare.TextLocked = "resolve", "", true
	review := reviewDraft()
	review.TextLocked = true
	runGate(t, `
const bare = walk(draft(input.bare), (x) => x.tag === "button").map((b) => b.textContent);
await focus(draft(input.review), "outbox:open:approve:draft:7").on.click();
const c = draft(input.review);
const sel = walk(c, (x) => x.tag === "select")[0];
await sel.on.change({ target: { value: "COMMENT" } });
await focus(draft(input.review), "outbox:approve:draft:7").on.click();
done({ Bare: bare, Textareas: walk(c, (x) => x.tag === "textarea").length, Selects: walk(c, (x) => x.tag === "select").length, Posts: posts });`,
		map[string]any{"bare": bare, "review": review}, &got)
	if slices.Contains(got.Bare, "edit") {
		t.Errorf("a draft with nothing editable offers %q", got.Bare)
	}
	if got.Textareas != 0 || got.Selects != 1 {
		t.Errorf("%d text editors, %d selects: want only the verdict's", got.Textareas, got.Selects)
	}
	if len(got.Posts) != 1 {
		t.Fatalf("posts %v", got.Posts)
	}
	args := got.Posts[0]["body"].(map[string]any)["args"].(map[string]any)
	if _, ok := args["text"]; ok || args["payload"] == nil || args["rev"] != "3" {
		t.Errorf("approved with %v, want the payload and rev, no text", args)
	}
}

// TestCardShowsARevisionMadeWhileEditing shows, on a draft revised since its edit began, that it changed, who
// wrote it and its new text, drops the edits of its fields, and approves at the rev now shown.
func TestCardShowsARevisionMadeWhileEditing(t *testing.T) {
	var got struct {
		Conflict string
		Selected []string
		Posts    []map[string]any
	}
	revised := reviewDraft()
	revised.Rev, revised.Text, revised.Author = 4, "One finding left.", "Last written by an agent"
	runGate(t, `
await focus(draft(input.draft), "outbox:open:approve:draft:7").on.click();
await walk(draft(input.draft), (x) => x.tag === "select")[0].on.change({ target: { value: "COMMENT" } });
const c = draft(input.revised);
const conflict = cls(c, "d-conflict").map((x) => x.textContent).join("");
const selected = walk(c, (x) => x.tag === "option" && "selected" in x.attrs).map((o) => attr(o, "value"));
await focus(c, "outbox:approve:draft:7").on.click();
done({ Conflict: conflict, Selected: selected, Posts: posts });`, map[string]any{"draft": reviewDraft(), "revised": revised}, &got)
	for _, s := range []string{"changed since you began editing", "Last written by an agent", "One finding left."} {
		if !strings.Contains(got.Conflict, s) {
			t.Errorf("the conflict says %q, lacks %q", got.Conflict, s)
		}
	}
	if !slices.Equal(got.Selected, []string{"REQUEST_CHANGES"}) {
		t.Errorf("selected %q, want the revision's verdict, the stale edit dropped", got.Selected)
	}
	args := got.Posts[0]["body"].(map[string]any)["args"].(map[string]any)
	if args["rev"] != "4" || args["payload"] != nil {
		t.Errorf("approved with %v, want rev 4 and no dropped payload edit", args)
	}
}

// TestCardOffersNoControlForAFieldWithoutOptions keeps a field the kind makes editable but whose schema gives
// no enum as its value, so no edit posts a value of the wrong type.
func TestCardOffersNoControlForAFieldWithoutOptions(t *testing.T) {
	var got int
	d := reviewDraft()
	d.Fields[1].Fields[0].Fields[1].Editable = true
	runGate(t, `
await focus(draft(input.draft), "outbox:open:approve:draft:7").on.click();
done(walk(draft(input.draft), (x) => ["input", "select"].includes(x.tag)).length);`, map[string]any{"draft": d}, &got)
	if got != 1 {
		t.Errorf("%d controls, want the verdict's only", got)
	}
}
