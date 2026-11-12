package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
)

// TestAPinnedQuestionServesItsDrafts serves, with a question, each draft its confirm and options pin as the
// draft's own card serves it now, once each, a gone draft left out; and its notification offers no answer that
// approves one, saying so in the profile's words.
func TestAPinnedQuestionServesItsDrafts(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	a := r.propose(t, r7u, "Ship it.", draft.Wait)
	b := r.propose(t, r7u, "One nit first.", draft.Wait)
	m := dmessage.Message{ID: 9, Unit: r7u, Kind: dmessage.Question, Text: "Which review?", Confirm: false,
		Options: []record.Option{{ID: "a", Label: "Approve", Draft: &record.DraftRef{ID: int64(a.ID), Rev: a.Rev}},
			{ID: "b", Label: "Comment", Draft: &record.DraftRef{ID: int64(b.ID), Rev: b.Rev}},
			{ID: "c", Label: "Again", Draft: &record.DraftRef{ID: int64(a.ID), Rev: a.Rev}},
			{ID: "d", Label: "Gone", Draft: &record.DraftRef{ID: 999, Rev: 0}},
			{ID: "e", Label: "Hold"}}}
	got := r.s.pinnedDrafts(ctx, m)
	if len(got) != 2 || got[0].ID != int64(a.ID) || got[0].Text != "Ship it." || got[1].ID != int64(b.ID) || got[0].Gate == 0 {
		t.Fatalf("pinned %+v, want drafts a and b once each, as their cards serve them", got)
	}
	_, _, att := r.s.messageNotice(task.Task{}, m, 1)
	var ids []string
	for _, x := range att.Actions {
		ids = append(ids, x.ID)
	}
	if slices.Contains(ids, "answer:option:a") || slices.Contains(ids, "answer:option:b") || !slices.Contains(ids, "answer:option:e") {
		t.Errorf("notification answers %q, want only the unpinned option", ids)
	}
	if want := r.s.notice("notify_pinned", nil); want == "notify_pinned" || !strings.Contains(att.Body, want) {
		t.Errorf("body %q lacks the profile's pinned line %q", att.Body, want)
	}
	confirm := dmessage.Message{ID: 10, Unit: r7u, Kind: dmessage.Question, Confirm: true, Draft: &record.DraftRef{ID: int64(a.ID), Rev: a.Rev}}
	var cids []string
	for _, x := range r.s.choices(confirm) {
		cids = append(cids, x.ID)
	}
	if !slices.Equal(cids, []string{"answer:no"}) {
		t.Errorf("a pinned confirm's answers %q, want only no", cids)
	}
	v := r.s.messageView(ctx, confirm)
	if v.Draft == nil || v.Draft.ID != int64(a.ID) || len(v.Pinned) != 1 {
		t.Errorf("the confirm's view %+v, want its pin and the pinned draft", v)
	}
}
