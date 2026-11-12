package daemon

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// The task verb approve, offered on every task (tasks.toml: no from) from rows, keys, the omnibox and
// notifications' task actions, answers a question with its recommended option or a confirm's yes. On a
// question whose recommended option or confirm pins a draft, that approves the draft from a surface that
// shows no pin.
func TestReattackApproveVerbAnswersAPinnedQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		q    *record.Question
	}{
		{"pinned confirm", &record.Question{Kind: "confirm", Text: "Publish?", Draft: &record.DraftRef{ID: 42, Rev: 1}}},
		{"pinned recommended option", &record.Question{Kind: "choice", Text: "Which?", Recommend: "ship", Options: []record.Option{
			{ID: "ship", Label: "Hold, post nothing", Consequence: "c", Draft: &record.DraftRef{ID: 42, Rev: 1}},
			{ID: "hold", Label: "Hold", Consequence: "c"}}}},
	} {
		r := newMessageRig(t)
		ctx := context.Background()
		r.asked(r8u, turn.Intent(dintent.Review), turn.Asked, c.q)
		if err := r.s.deps.Messages.Reconcile(ctx, r8u.Unit()); err != nil {
			t.Fatal(err)
		}
		open, _ := r.msgs.Open(ctx)
		if len(open) != 1 {
			t.Fatalf("%s: open %+v", c.name, open)
		}
		rec := post(t, r.s, api.TaskPath(open[0].TaskID, "approve"), `{"source":"tui"}`)
		books, _ := r.store.Books(ctx, r8u.Unit())
		a := books[r8u.Unit()].Answers
		if rec.Code == 200 && len(a) == 1 && (a[0].Yes || a[0].OptionID == "ship") {
			t.Errorf("%s: approve verb answered %+v: a pin approved from a surface that shows none", c.name, a[0])
		}
	}
}
