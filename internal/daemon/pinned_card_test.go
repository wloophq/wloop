package daemon

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
)

// openMessages are open messages in memory.
type openMessages []dmessage.Message

func (o openMessages) Upsert(_ context.Context, m dmessage.Message) (dmessage.Message, error) {
	return m, nil
}
func (o openMessages) Message(_ context.Context, id int64) (dmessage.Message, bool, error) {
	for _, m := range o {
		if m.ID == id {
			return m, true, nil
		}
	}
	return dmessage.Message{}, false, nil
}
func (o openMessages) Open(context.Context) ([]dmessage.Message, error)      { return o, nil }
func (o openMessages) Close(context.Context, int64, string, time.Time) error { return nil }

// outboxDrafts are the rig's outbox drafts as a question's options reach them.
type outboxDrafts struct{ r *outboxRig }

func (o outboxDrafts) DraftGate(ctx context.Context, id int64) (draft.Draft, gate.Gate, bool, error) {
	d, err := o.r.o.Store.Get(ctx, draft.ID(id))
	if err != nil {
		return draft.Draft{}, gate.Gate{}, false, nil
	}
	g, ok, err := o.r.o.GateOf(ctx, d)
	return d, g, ok, err
}

func (o outboxDrafts) WithdrawHeld(context.Context, int64, string) error { return nil }

// TestAPinnedDraftIsItsQuestionsOnly serves a held draft an open question of its turn pins as that question's:
// its card names the message, the Outbox leaves it out and it notifies nothing of its own; a draft no question
// pins is served as before.
func TestAPinnedDraftIsItsQuestionsOnly(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	ctx := context.Background()
	propose := func(text string) draft.Draft {
		d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Kind("reply"), Text: text,
			Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 3, Session: "term_1"}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	pinned, free := propose("Approve it"), propose("Unrelated")
	q := dmessage.Message{ID: 9, Unit: r7u, Kind: dmessage.Question, Text: "Approve?", Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: 3},
		Options: []record.Option{{ID: "approve", Label: "Approve", Draft: &record.DraftRef{ID: int64(pinned.ID), Rev: pinned.Rev}}, {ID: "hold", Label: "Hold"}}}
	r.s.deps.Messages = &messages.Service{Store: openMessages{q}, Drafts: outboxDrafts{r}}
	r.s.deps.Drafts = r.o
	var res api.DraftsResponse
	get(t, r.s, api.Drafts+"?id="+strconv.FormatInt(int64(pinned.ID), 10)+","+strconv.FormatInt(int64(free.ID), 10), &res)
	for _, d := range res.Drafts {
		if want := map[int64]int64{int64(pinned.ID): 9, int64(free.ID): 0}[d.ID]; d.PinnedBy != want {
			t.Errorf("draft %d pinned by %d, want %d", d.ID, d.PinnedBy, want)
		}
	}
	var page api.OutboxPage
	get(t, r.s, api.OutboxPath, &page)
	var listed []int64
	for _, g := range page.Groups {
		for _, d := range g.Drafts {
			listed = append(listed, d.ID)
		}
	}
	if len(listed) != 1 || listed[0] != int64(free.ID) {
		t.Errorf("the Outbox lists %v, want only the draft no question pins", listed)
	}
	var notified []draft.ID
	for _, d := range r.s.taskViews(ctx, r.s.deps.Tasks, false).openDrafts(ctx) {
		notified = append(notified, d.ID)
	}
	if len(notified) != 1 || notified[0] != free.ID {
		t.Errorf("the drafts that notify %v, want only the draft no question pins", notified)
	}
}
