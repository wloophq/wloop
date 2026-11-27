package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/termfmt"
)

var draftNow = time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC)

func draftFixture() []api.Draft {
	actions := []api.ActionState{{ID: "draft.edit", Enabled: true}, {ID: "draft.retry", Reason: "the draft is held"}}
	intents := map[string]string{"approved": "approve", "rejected": "discard"}
	return []api.Draft{
		{ID: 14, Unit: "slack/T1/C1/1700000000.000100", Kind: "reply", Purpose: "reply", State: "held", Gate: 3, Intents: intents,
			Label: "#ops", Title: "When does the export ship?", Link: "https://acme.slack.com/archives/C1/p1700000000000100",
			Text: "Friday, after the freeze.\nI will post the notes here.", Origin: api.DraftOrigin{Kind: "user"}, Actions: actions,
			CreatedAt: draftNow.Add(-5 * time.Minute)},
		{ID: 12, Unit: "github/acme/api#7", Kind: "nudge", Purpose: "nudge", Effects: []string{"task.nudged"}, State: "held", Gate: 3, Intents: intents,
			Label: "acme/api#7", Title: "Batch export for large accounts", Link: "https://github.com/acme/api/pull/7",
			Text: "@{github:ana}, any news on this?\n\n- Review the schema change", Names: map[string]string{"github:ana": "ana"},
			Items:       []string{"i1"},
			ItemDetails: []api.ItemView{{ID: "i1", Summary: "Review the schema change", State: "open"}},
			Origin:      api.DraftOrigin{Kind: "auto", Rule: "stalled-nudge"}, Actions: actions, CreatedAt: draftNow.Add(-26 * time.Hour)},
		{ID: 11, Unit: "slack/T1/D9", Kind: "close", Purpose: "close", Effects: []string{"unit.closed"}, State: "held", Gate: 3, Intents: intents,
			Label: "DM with Ana Lima", Title: "DM with Ana Lima: rollout", Items: []string{"i2", "i3"},
			Text:   "Closing this out:\n\n- Rollout date: resolved\n- Feature flag owner: answered",
			Origin: api.DraftOrigin{Kind: "auto", Rule: "closure"}, Actions: actions, CreatedAt: draftNow.Add(-3 * time.Hour)},
		{ID: 9, Unit: "slack/T1/C1/1700000000.000100", Kind: "reply", Purpose: "reply", State: "held", Gate: 3, Intents: intents,
			Label: "#ops", Title: "When does the export ship?", Text: "cc @{slack:U0BOB} for the notes", Names: map[string]string{"slack:U0BOB": "Bob Reis"},
			Origin: api.DraftOrigin{Kind: "turn", TurnSeq: 4, Intent: "act"}, Actions: actions, CreatedAt: draftNow.Add(-50 * time.Hour)},
	}
}

func TestDraftViewReadsTheDraft(t *testing.T) {
	t.Parallel()
	d := draftFixture()[1]
	d.Where = "acme/api#7"
	for _, tc := range []struct {
		golden string
		term   termfmt.Term
	}{
		{"draft_show.golden", termfmt.Term{Width: 80}},
		{"draft_show_color.golden", termfmt.Term{TTY: true, Color: true, Width: 80}},
	} {
		var out bytes.Buffer
		writeDraftView(&out, tc.term, d, draftNow)
		golden(t, tc.golden, bracketed(out.String()))
	}
	var closing bytes.Buffer
	writeDraftView(&closing, termfmt.Term{Width: 80}, draftFixture()[2], draftNow)
	golden(t, "draft_show_closing.golden", closing.String())
}

func TestDraftViewSaysWhichConversationADraftQuotes(t *testing.T) {
	t.Parallel()
	d := draftFixture()[0]
	d.Quoted = "Quotes an item of the DM with Ana, a conversation they are not in."
	var out bytes.Buffer
	writeDraftView(&out, termfmt.Term{Width: 120}, d, draftNow)
	if !strings.Contains(out.String(), "Quotes") || !strings.Contains(out.String(), d.Quoted) {
		t.Fatalf("show:\n%s", out.String())
	}
}

func TestADraftClosesWhatItsEffectsSayWhateverItsPurpose(t *testing.T) {
	t.Parallel()
	closing := draftFixture()[2]
	wrapUp := closing
	wrapUp.Purpose = "wrap_up"
	if got := gist(wrapUp); !strings.HasPrefix(got, "closes 2 items") {
		t.Errorf("a wrap_up draft that closes its unit: %q", got)
	}
	closing.Effects = nil
	if got := gist(closing); strings.HasPrefix(got, "closes") {
		t.Errorf("a close draft that closes nothing: %q", got)
	}
}
