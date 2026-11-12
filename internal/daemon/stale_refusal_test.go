package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// TestAStaleRefusalKeepsTheCardStale answers an approval of a stale draft with a conflict, and from then on its
// card says it is stale and offers no approval, though the live read a page makes cannot tell; a revision clears it.
func TestAStaleRefusalKeepsTheCardStale(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	bases := &outboxtest.Bases{}
	bases.Set(r7u, "c1")
	r.o.Bases = bases
	r.o.Kinds = outboxtest.Kinds{Specs: []draft.KindSpec{{Provider: "github", Name: "review", Delivery: "review", Basis: draft.BasisAgent, Editable: []string{"text"}}},
		Defaults: map[string]string{"github": "comment"}}
	d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Kind("review"), Text: "Looks good", Basis: "c1", Payload: json.RawMessage(`{"event":"COMMENT"}`),
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "k"}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if err != nil {
		t.Fatal(err)
	}
	bases.Set(r7u, "c2")
	bases.Err = errors.New("the page's lookup ran out of time")
	var before api.DraftsResponse
	get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(d.ID), 10), &before)
	if before.Drafts[0].Stale {
		t.Fatal("the card is stale before any refusal, though the live read could not tell")
	}
	bases.Err = nil
	code, _, body := r.act(t, d.ID, "approve", `{"rev":`+strconv.FormatInt(d.Rev, 10)+`}`)
	if code != 409 {
		t.Fatalf("approving a stale draft: %d %s, want a conflict", code, body)
	}
	bases.Err = errors.New("the page's lookup ran out of time")
	var after api.DraftsResponse
	get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(d.ID), 10), &after)
	if v := after.Drafts[0]; !v.Stale || len(v.Notes) == 0 || v.Notes[0] != r.s.loaded().Gates.Drafts.Stale {
		t.Fatalf("after the refusal the card is stale %v with notes %q, want stale with the profile's note", v.Stale, v.Notes)
	}
	text, basis := "Rebased, looks good", "c2"
	bases.Err = nil
	cur, _ := r.o.Store.Get(ctx, d.ID)
	if _, err := r.o.Amend(ctx, d.ID, outbox.Amendment{Text: &text, Basis: &basis, Rev: cur.Rev}); err != nil {
		t.Fatal(err)
	}
	var revised api.DraftsResponse
	get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(d.ID), 10), &revised)
	if revised.Drafts[0].Stale {
		t.Error("a revision kept the refusal's stale mark")
	}
}
