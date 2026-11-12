package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// reviewView is a held review draft as the daemon serves it: its preview line, and a verdict and two inline
// comments, one of two lines, in the plugin's words.
func reviewView() api.Draft {
	leaf := func(p, label, v string) api.DraftField { return api.DraftField{Pointer: p, Label: label, Value: v} }
	return api.Draft{ID: 42, Unit: "github/acme/api#7", Kind: "review", Purpose: "review", State: "held", Target: "acme/api#7", Text: "Two findings block the merge.",
		Preview: "Request changes with 2 inline comments", Payload: json.RawMessage(`{"event":"REQUEST_CHANGES"}`),
		Fields: []api.DraftField{
			{Pointer: "/event", Label: "Verdict", Value: "REQUEST_CHANGES", Shown: "Request changes"},
			{Pointer: "/comments", Label: "Inline comments", Fields: []api.DraftField{
				{Pointer: "/comments/0", Label: "1", Fields: []api.DraftField{leaf("/comments/0/path", "File", "api/handler.go"), leaf("/comments/0/line", "Line", "42"),
					leaf("/comments/0/body", "Comment", "Return the error.")}},
				{Pointer: "/comments/1", Label: "2", Fields: []api.DraftField{leaf("/comments/1/path", "File", "api/retry.go"), leaf("/comments/1/line", "Line", "58"),
					leaf("/comments/1/body", "Comment", "Back off\nbetween tries.")}},
			}},
		}}
}

// TestTheCLIShowsADraftsWholePayload holds review focus 1 on the CLI: outbox show and the draft approve prints
// before it asks both carry the preview line and every value of the payload, each inline comment's file, line
// and every line of its body.
func TestTheCLIShowsADraftsWholePayload(t *testing.T) {
	d := reviewView()
	var show, approve bytes.Buffer
	writeDraftView(&show, termfmt.Term{Width: 100}, d, outboxNow)
	printDraft(&approve, d, d.Text)
	for name, out := range map[string]string{"show": show.String(), "approve": approve.String()} {
		for _, want := range []string{"Request changes with 2 inline comments", "Verdict: Request changes (REQUEST_CHANGES)", "Inline comments:",
			"File: api/handler.go", "Line: 42", "Comment: Return the error.", "File: api/retry.go", "Line: 58", "Comment: Back off", "between tries."} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, out)
			}
		}
	}
}

// TestOutboxShowReadsAStoredPayload runs outbox show against the daemon on a stored review draft: the payload it
// holds is printed, not only its text.
func TestOutboxShowReadsAStoredPayload(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	db := outboxDaemon(t, te, true)
	insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "Two findings block the merge.", State: draft.Held,
		Payload: json.RawMessage(`{"event":"REQUEST_CHANGES","comments":[{"path":"api/handler.go","line":42,"body":"Return the error."}]}`)})
	if code := te.run("outbox", "show", "1"); code != 0 {
		t.Fatalf("show: exit %d: %s", code, te.err)
	}
	for _, want := range []string{"REQUEST_CHANGES", "api/handler.go", "42", "Return the error."} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, te.out)
		}
	}
}
