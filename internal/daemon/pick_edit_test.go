package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
)

// pickRig is an Outbox rig whose open question 9 pins a held review reply in its approve option, with the question's
// gate open and the gates service reading its options.
func pickRig(t *testing.T) (*outboxRig, draft.Draft, gate.Gate) {
	t.Helper()
	r := newOutboxRig(t)
	ctx := context.Background()
	d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Kind("reply"), Text: "Approve it",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 3, Session: "term_1"}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if err != nil {
		t.Fatal(err)
	}
	q := dmessage.Message{ID: 9, Unit: r7u, Kind: dmessage.Question, Text: "Post it?", Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: 3},
		Options: []record.Option{{ID: "approve", Label: "Post it", Draft: &record.DraftRef{ID: int64(d.ID), Rev: d.Rev}}, {ID: "hold", Label: "Hold"}}}
	m := &messages.Service{Store: openMessages{q}, Books: noBooks{}, Drafts: outboxDrafts{r}}
	r.s.deps.Messages = m
	st := r.db.Gates(r.clock.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock, Options: m,
		Publisher: publishTo(func(ctx context.Context, g gate.Gate) error { return r.o.Publish(ctx, g) })})
	r.s.deps.Gates, r.s.deps.GateReader, r.o.Gates = svc, st, rigGates{svc, st}
	qg, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: r7u, Subject: "9"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r, d, qg
}

func pickBody(t *testing.T, args map[string]string) string {
	t.Helper()
	args["answer"] = messages.GateAnswer(messages.Answer{OptionID: "approve"})
	raw, err := json.Marshal(api.TaskRequest{Args: args, Source: "cockpit"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A person edits the draft a question's option pins and picks the option in one call: the question is answered
// with it and the draft is approved as they edited it, so the post carries their edit.
func TestAPickCarriesThePersonsEditOfThePinnedDraft(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, d, q := pickRig(t)
	rec := post(t, r.s, gatePath(q.ID, "answer"), pickBody(t, map[string]string{"rev": strconv.FormatInt(d.Rev, 10), "text": "Approve it, with a note"}))
	if rec.Code != 200 {
		t.Fatalf("the pick with an edit: %d %s", rec.Code, rec.Body)
	}
	got, err := r.o.Store.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Approve it, with a note" || got.State != draft.Queued || got.Rev != d.Rev+1 {
		t.Fatalf("the draft %+v, want the person's edit queued at rev %d", got, d.Rev+1)
	}
	if qg, _ := r.s.deps.GateReader.Get(ctx, q.ID); qg.Status != gate.Answered {
		t.Fatalf("the question %+v, want answered", qg)
	}
	if sent, _, err := r.o.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("sent %d, %v", sent, err)
	}
	if posted, _ := r.o.Store.Get(ctx, d.ID); posted.Text != "Approve it, with a note" || posted.State != draft.Sent {
		t.Fatalf("posted %+v", posted)
	}
}

// A pick at a rev the draft is no longer at refuses with a conflict and changes nothing: the person saw another
// version of the draft.
func TestAPickOfAnotherRevIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, d, q := pickRig(t)
	rec := post(t, r.s, gatePath(q.ID, "answer"), pickBody(t, map[string]string{"rev": strconv.FormatInt(d.Rev+1, 10), "text": "stale edit"}))
	if rec.Code != 409 {
		t.Fatalf("a pick at a rev the draft is not at: %d %s, want a conflict", rec.Code, rec.Body)
	}
	if got, _ := r.o.Store.Get(ctx, d.ID); got.Text != "Approve it" || got.State != draft.Held {
		t.Fatalf("the draft %+v after a refused pick", got)
	}
	if qg, _ := r.s.deps.GateReader.Get(ctx, q.ID); qg.Status != gate.Open {
		t.Fatalf("the question %+v after a refused pick, want open", qg)
	}
}
