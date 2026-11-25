package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// TestTheOutboxListsADraftAnOpenQuestionPinsWithItsQuestion lists every held draft, one an open question pins
// beside the rest, with the question's gate as its card serves it, so the Outbox answers it through that gate;
// the Outbox view counts it, and the CLI's list and the Outbox hold the same drafts.
func TestTheOutboxListsADraftAnOpenQuestionPinsWithItsQuestion(t *testing.T) {
	t.Parallel()
	r, ds, qg := pickRigOf(t, "Ship it.", "One nit first.")
	pinned, other := ds[0], ds[1]
	ctx := context.Background()
	free, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Kind("reply"), Text: "Unrelated",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "free"}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if err != nil {
		t.Fatal(err)
	}
	var page api.OutboxPage
	get(t, r.s, api.OutboxPath, &page)
	var listed []int64
	for _, d := range page.Drafts {
		listed = append(listed, d.ID)
		if want := map[int64]int64{int64(pinned.ID): 9, int64(other.ID): 9}[d.ID]; d.PinnedBy != want {
			t.Errorf("draft %d pinned by %d, want %d", d.ID, d.PinnedBy, want)
		}
	}
	slices.Sort(listed)
	if want := []int64{int64(pinned.ID), int64(other.ID), int64(free.ID)}; !slices.Equal(listed, want) {
		t.Fatalf("the Outbox lists %v, want %v", listed, want)
	}
	if len(page.Questions) != 1 || page.Questions[0].ID != qg.ID {
		t.Fatalf("questions %+v, want the gate %d of the question that pins draft %d", page.Questions, qg.ID, pinned.ID)
	}
	var m api.Message
	if err := json.Unmarshal(page.Questions[0].Subject, &m); err != nil || m.ID != 9 || len(m.Pinned) != 2 || m.Pinned[0].ID != int64(pinned.ID) {
		t.Fatalf("the question's subject %s (%v), want message 9 with its pinned drafts", page.Questions[0].Subject, err)
	}
	var cli api.DraftsResponse
	get(t, r.s, api.Drafts, &cli)
	var ids []int64
	for _, d := range cli.Drafts {
		ids = append(ids, d.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, listed) {
		t.Errorf("the CLI lists %v, the Outbox %v", ids, listed)
	}
	if n := r.s.counters(ctx).Views["outbox"]; n != 3 {
		t.Errorf("the Outbox counts %d, want every held draft", n)
	}
}
