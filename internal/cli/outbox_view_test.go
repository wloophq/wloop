package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

var outboxNow = time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC)

func outboxFixture() []api.Draft {
	actions := []api.ActionState{{ID: "draft.approve", Enabled: true}, {ID: "draft.edit", Enabled: true}, {ID: "draft.discard", Enabled: true},
		{ID: "draft.retry", Reason: "the draft is proposed"}}
	return []api.Draft{
		{ID: 14, Unit: "slack/T1/C1/1700000000.000100", Kind: "reply", State: "proposed", Approval: api.DraftApproval{Mode: "always"},
			Label: "#ops", Title: "When does the export ship?", Link: "https://acme.slack.com/archives/C1/p1700000000000100",
			Text: "Friday, after the freeze.\nI will post the notes here.", Origin: api.DraftOrigin{Kind: "user"}, Actions: actions,
			CreatedAt: outboxNow.Add(-5 * time.Minute)},
		{ID: 12, Unit: "github/acme/api#7", Kind: "nudge", State: "proposed", Approval: api.DraftApproval{Mode: "always"},
			Label: "acme/api#7", Title: "Batch export for large accounts", Link: "https://github.com/acme/api/pull/7",
			Text: "@{github:ana}, any news on this?\n\n- Review the schema change", Names: map[string]string{"github:ana": "ana"},
			Items:       []string{"i1"},
			ItemDetails: []api.ItemView{{ID: "i1", Summary: "Review the schema change", State: "open"}},
			Origin:      api.DraftOrigin{Kind: "auto", Rule: "stalled-nudge"}, Actions: actions, CreatedAt: outboxNow.Add(-26 * time.Hour)},
		{ID: 11, Unit: "slack/T1/D9", Kind: "close", State: "proposed", Approval: api.DraftApproval{Mode: "always"},
			Label: "DM with Ana Lima", Title: "DM with Ana Lima: rollout", Items: []string{"i2", "i3"},
			Text:   "Closing this out:\n\n- Rollout date: resolved\n- Feature flag owner: answered",
			Origin: api.DraftOrigin{Kind: "auto", Rule: "closure"}, Actions: actions, CreatedAt: outboxNow.Add(-3 * time.Hour)},
		{ID: 9, Unit: "slack/T1/C1/1700000000.000100", Kind: "reply", State: "proposed", Approval: api.DraftApproval{Mode: "always"},
			Label: "#ops", Title: "When does the export ship?", Text: "cc @{slack:U0BOB} for the notes", Names: map[string]string{"slack:U0BOB": "Bob Reis"},
			Origin: api.DraftOrigin{Kind: "turn", TurnSeq: 4, Intent: "act"}, Actions: actions, CreatedAt: outboxNow.Add(-50 * time.Hour)},
	}
}

func TestOutboxListReadsByKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		golden string
		term   termfmt.Term
	}{
		{"outbox_list_tty.golden", termfmt.Term{TTY: true, Width: 110}},
		{"outbox_list_color.golden", termfmt.Term{TTY: true, Color: true, Width: 110}},
		{"outbox_list_narrow.golden", termfmt.Term{TTY: true, Width: 72}},
	} {
		var out bytes.Buffer
		writeOutbox(&out, tc.term, outboxFixture(), outboxNow)
		golden(t, tc.golden, bracketed(out.String()))
	}
	var plain bytes.Buffer
	writeOutboxPlain(&plain, outboxFixture())
	golden(t, "outbox_list_plain.golden", plain.String())
}

func TestOutboxListShowsTheColumnsThatDiffer(t *testing.T) {
	t.Parallel()
	ds := outboxFixture()
	ds[2].State, ds[2].Error, ds[2].Target = "failed", "may have been posted: timeout", "#general"
	var out bytes.Buffer
	writeOutbox(&out, termfmt.Term{TTY: true, Width: 120}, ds, outboxNow)
	golden(t, "outbox_list_mixed.golden", out.String())
}

func TestOutboxShowReadsTheDraft(t *testing.T) {
	t.Parallel()
	d := outboxFixture()[1]
	d.Where = "acme/api#7"
	for _, tc := range []struct {
		golden string
		term   termfmt.Term
	}{
		{"outbox_show.golden", termfmt.Term{Width: 80}},
		{"outbox_show_color.golden", termfmt.Term{TTY: true, Color: true, Width: 80}},
	} {
		var out bytes.Buffer
		writeDraftView(&out, tc.term, d, outboxNow)
		golden(t, tc.golden, bracketed(out.String()))
	}
	var closing bytes.Buffer
	writeDraftView(&closing, termfmt.Term{Width: 80}, outboxFixture()[2], outboxNow)
	golden(t, "outbox_show_closing.golden", closing.String())
}
