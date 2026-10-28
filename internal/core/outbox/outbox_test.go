package outbox

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var ctx = context.Background()

func TestConcurrentDecisionsResolveTheGateOnce(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("hi"))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, intent := range []string{"approve", "discard"} {
		wg.Go(func() { errs[i] = e.decide(d.ID, intent) })
	}
	wg.Wait()
	ok, resolved := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, gates.ErrResolved):
			resolved++
		default:
			t.Errorf("unexpected error %v", err)
		}
	}
	got, g := e.get(d.ID), e.gate(d.ID)
	if ok != 1 || resolved != 1 || g.Status == gate.Approved && got.State != draft.Queued || g.Status == gate.Rejected && got.State != draft.Dropped {
		t.Fatalf("ok %d resolved %d, draft %s gate %s (%v)", ok, resolved, got.State, g.Status, errs)
	}
}

func TestEditKeepsTheOriginal(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("first"))
	got, err := e.svc.Edit(ctx, d.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "second" || got.Original != "first" || !got.Edited {
		t.Fatalf("edited %+v", got)
	}
	back, err := e.svc.Edit(ctx, d.ID, "first")
	if err != nil || back.Edited {
		t.Fatalf("edit back to the original: %+v %v", back, err)
	}

	failed := e.propose(reply("will fail"))
	if _, err := e.approve(failed.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Edit(ctx, failed.ID, "x"); !errors.Is(err, draft.ErrTransition) {
		t.Errorf("edit of an approved draft: %v", err)
	}
	e.fx.failWith = errBoom
	e.svc.Send(ctx)
	if got, err := e.svc.Edit(ctx, failed.ID, "fixed"); err != nil || got.Text != "fixed" || got.Original != "will fail" {
		t.Errorf("edit of a failed draft: %+v %v", got, err)
	}
	if _, err := e.svc.Edit(ctx, d.ID, " "); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank edit: %v", err)
	}
}

func TestOnlyApprovedDraftsArePublished(t *testing.T) {
	e := newEnv()
	e.propose(reply("stays"))
	b := e.propose(reply("goes"))
	if _, err := e.approve(b.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.store.beforeUpdate = func(d draft.Draft) {
		if d.State == draft.Sending && d.SendingAt.IsZero() {
			t.Error("sending without SendingAt")
		}
	}
	sent, failed, err := e.svc.Send(ctx)
	if err != nil || sent != 1 || failed != 0 {
		t.Fatalf("send %d %d %v", sent, failed, err)
	}
	if !slices.Equal(e.fx.published, []draft.ID{b.ID}) {
		t.Fatalf("published %v", e.fx.published)
	}
	got := e.get(b.ID)
	if got.State != draft.Sent || got.Evidence.Ref == "" || got.Evidence.At.IsZero() || !got.SendingAt.Equal(t0) {
		t.Fatalf("after send %+v", got)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 || len(e.fx.published) != 1 {
		t.Fatalf("second pass sent %d, published %v", sent, e.fx.published)
	}
}

func TestFailedDraftIsNeverRetriedAutomatically(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("x"))
	e.approve(d.ID, nil)
	e.fx.failWith = errBoom
	if _, failed, _ := e.svc.Send(ctx); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	got := e.get(d.ID)
	if got.State != draft.Failed || got.Error != "boom" {
		t.Fatalf("after failure %+v", got)
	}
	e.fx.failWith = nil
	for range 3 {
		e.svc.Send(ctx)
	}
	if len(e.fx.published) != 1 {
		t.Fatalf("published %v", e.fx.published)
	}
}

func TestRetryByHandPublishesOnce(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("x"))
	e.approve(d.ID, nil)
	e.fx.failWith = errBoom
	e.svc.Send(ctx)
	e.fx.failWith = nil
	got, err := e.svc.Retry(ctx, d.ID, false)
	if err != nil || got.State != draft.Queued || got.Error != "" {
		t.Fatalf("retry %+v %v", got, err)
	}
	e.svc.Send(ctx)
	e.svc.Send(ctx)
	if !slices.Equal(e.fx.published, []draft.ID{d.ID, d.ID}) {
		t.Fatalf("published %v", e.fx.published)
	}
	if e.get(d.ID).State != draft.Sent {
		t.Fatalf("state %s", e.get(d.ID).State)
	}
	if _, err := e.svc.Retry(ctx, d.ID, false); !errors.Is(err, draft.ErrTransition) {
		t.Fatalf("retry of a sent draft: %v", err)
	}
}

func TestRetryOfUnconfirmedPostNeedsConfirm(t *testing.T) {
	for _, cause := range []string{NotConfirmed, "may have been posted: timeout"} {
		e := newEnv()
		d := e.propose(reply("x"))
		e.approve(d.ID, nil)
		e.fx.failWith = errors.New(cause)
		e.svc.Send(ctx)
		if _, err := e.svc.Retry(ctx, d.ID, false); !errors.Is(err, ErrNeedsConfirm) {
			t.Fatalf("%s: retry without confirm: %v", cause, err)
		}
		if e.get(d.ID).State != draft.Failed {
			t.Fatalf("%s: state %s", cause, e.get(d.ID).State)
		}
		if got, err := e.svc.Retry(ctx, d.ID, true); err != nil || got.State != draft.Queued {
			t.Fatalf("%s: retry with confirm %+v %v", cause, got, err)
		}
	}
}

func TestDisabledPluginRefusesApproval(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("x"))
	other := e.propose(reply("y"))
	e.approve(other.ID, nil)
	e.fx.failWith = errBoom
	e.svc.Send(ctx)
	e.fx.failWith = nil

	e.disabled["slack"] = true
	if _, err := e.approve(d.ID, nil); !errors.Is(err, ErrDisabled) || !strings.Contains(err.Error(), "slack") {
		t.Fatalf("approve: %v", err)
	}
	if _, err := e.svc.Retry(ctx, other.ID, true); !errors.Is(err, ErrDisabled) || !strings.Contains(err.Error(), "slack") {
		t.Fatalf("retry: %v", err)
	}
	if _, err := e.svc.Propose(ctx, reply("z")); !errors.Is(err, ErrDisabled) {
		t.Fatalf("propose: %v", err)
	}
	e.svc.Send(ctx)
	if len(e.fx.published) != 1 || e.get(d.ID).State != draft.Held || e.get(other.ID).State != draft.Failed {
		t.Fatalf("published %v, states %s %s", e.fx.published, e.get(d.ID).State, e.get(other.ID).State)
	}
}

func TestApprovedDraftOfADisabledPluginFailsAtSend(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("x"))
	e.approve(d.ID, nil)
	e.disabled["slack"] = true
	if _, failed, _ := e.svc.Send(ctx); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, "slack") || len(e.fx.published) != 0 {
		t.Fatalf("%+v published %v", got, e.fx.published)
	}
}

func sending(e *env, text string) draft.Draft {
	d := e.propose(reply(text))
	d.State, d.SendingAt = draft.Sending, t0
	d, err := e.store.Update(ctx, d, draft.Held)
	if err != nil {
		panic(err)
	}
	return d
}

func TestRecoverKeepsVerifysPointer(t *testing.T) {
	e := newEnv()
	d := sending(e, "x")
	e.fx.verifyErr = errors.New("may have been posted; check https://example.test/p/1")
	if err := e.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Failed || got.Error != e.fx.verifyErr.Error() {
		t.Fatalf("%+v", got)
	}
	if _, err := e.svc.Retry(ctx, d.ID, false); !errors.Is(err, ErrNeedsConfirm) {
		t.Fatalf("retry without confirm: %v", err)
	}
}

func TestConflictAfterPublishIsSurfaced(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("x"))
	e.approve(d.ID, nil)
	e.fx.during = func(p draft.Draft) {
		cur, _ := e.store.Get(ctx, p.ID)
		cur.State, cur.Error = draft.Failed, NotConfirmed
		if _, err := e.store.Update(ctx, cur, draft.Sending); err != nil {
			t.Error(err)
		}
	}
	sent, _, err := e.svc.Send(ctx)
	if !errors.Is(err, ErrConflict) || sent != 0 {
		t.Fatalf("sent %d err %v", sent, err)
	}
	found := false
	for _, l := range e.logs {
		found = found || strings.Contains(l, "published but changed state")
	}
	if !found {
		t.Fatalf("logs %q", e.logs)
	}
}

func TestStaleClaimDoesNotPostOldText(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("old"))
	e.approve(d.ID, nil)
	calls := 0
	effects := e.svc.Effects
	e.svc.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if calls++; calls == 1 {
			cur, _ := e.store.Get(ctx, d.ID)
			cur.Text = "new"
			if _, err := e.store.Update(ctx, cur, draft.Queued); err != nil {
				t.Fatal(err)
			}
		}
		return effects(p)
	}
	if sent, _, err := e.svc.Send(ctx); sent != 0 || err != nil || len(e.fx.published) != 0 {
		t.Fatalf("stale claim sent %d %v %v", sent, err, e.fx.published)
	}
	e.svc.Send(ctx)
	if got := e.get(d.ID); got.State != draft.Sent || got.Text != "new" {
		t.Fatalf("%+v", got)
	}
}

func TestRecoverFindsThePost(t *testing.T) {
	e := newEnv()
	var calls []string
	e.svc.Evidence = func(ctx context.Context, d draft.Draft) error {
		calls = append(calls, "evidence:"+string(d.State))
		return nil
	}
	e.svc.OnSent = []func(context.Context, draft.Draft) error{func(ctx context.Context, d draft.Draft) error {
		calls = append(calls, "onsent:"+d.Evidence.Ref)
		return nil
	}}
	d := sending(e, "x")
	e.fx.found = true
	if err := e.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.get(d.ID)
	if got.State != draft.Sent || got.Evidence.Ref != "found.1" || len(e.fx.published) != 0 {
		t.Fatalf("%+v published %v", got, e.fx.published)
	}
	if !slices.Equal(calls, []string{"evidence:sent", "onsent:found.1"}) {
		t.Fatalf("hooks %v", calls)
	}
}

func TestRecoverNeverRepublishes(t *testing.T) {
	for _, verifyErr := range []error{nil, errBoom} {
		e := newEnv()
		d := sending(e, "x")
		e.fx.verifyErr = verifyErr
		if err := e.svc.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		e.svc.Send(ctx)
		got := e.get(d.ID)
		if got.State != draft.Failed || got.Error != NotConfirmed || len(e.fx.published) != 0 {
			t.Fatalf("verify err %v: %+v published %v", verifyErr, got, e.fx.published)
		}
	}
}

func TestRecoverWaitsForDisabledPlugin(t *testing.T) {
	e := newEnv()
	d := sending(e, "x")
	e.disabled["slack"] = true
	if err := e.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Sending || len(e.fx.verified) != 0 {
		t.Fatalf("%+v verified %v", got, e.fx.verified)
	}
	e.disabled["slack"] = false
	e.fx.found = true
	if err := e.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Sent || len(e.fx.published) != 0 {
		t.Fatalf("%+v published %v", got, e.fx.published)
	}
}

func TestProposeValidatesTheTarget(t *testing.T) {
	e := newEnv()
	v := validating{e.fx}
	e.svc.Effects = func(unit.ProviderID) (port.Effects, error) { return v, nil }
	e.fx.refuse = errors.New("unknown channel #nope")
	p := reply("x")
	p.Target = "#nope"
	if _, err := e.svc.Propose(ctx, p); err == nil || !strings.Contains(err.Error(), "#nope") {
		t.Fatalf("propose: %v", err)
	}
	if got, _ := e.store.List(ctx, Filter{}); len(got) != 0 {
		t.Fatalf("stored %v", got)
	}
	e.fx.refuse = nil
	d := e.propose(p)
	e.fx.refuse = errors.New("channel archived")
	if _, err := e.approve(d.ID, nil); err == nil || e.get(d.ID).State != draft.Held {
		t.Fatalf("approve after the target went away: %v", err)
	}
}

func TestAutoProposalsDoNotStack(t *testing.T) {
	e := newEnv()
	auto := func(key string) Proposal {
		return Proposal{Unit: thread, Kind: draft.Nudge, Text: "ping",
			Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "stalled-nudge", Key: key}}
	}
	first := e.propose(auto("stalled-nudge:e1:slack:U000AAA"))
	got, err := e.svc.Propose(ctx, auto("stalled-nudge:e1:slack:U000AAA"))
	if !errors.Is(err, ErrDuplicate) || got.ID != first.ID {
		t.Fatalf("duplicate: %+v %v", got, err)
	}
	other, err := e.svc.Propose(ctx, auto("stalled-nudge:e1:slack:U000BBB"))
	if err != nil || other.ID == first.ID {
		t.Fatalf("other person: %+v %v", other, err)
	}
	e.decide(first.ID, "discard")
	if again, err := e.svc.Propose(ctx, auto("stalled-nudge:e1:slack:U000AAA")); err != nil || again.ID == first.ID {
		t.Fatalf("after discard: %+v %v", again, err)
	}
	turn := reply("ping")
	e.propose(turn)
	if _, err := e.svc.Propose(ctx, turn); err != nil {
		t.Fatalf("a turn's proposals are not deduped: %v", err)
	}
}

func TestProposeNeedsText(t *testing.T) {
	e := newEnv()
	for _, p := range []Proposal{reply(""), reply("  \n"), {Unit: thread, Kind: "shout", Text: "x"}, {Kind: draft.Reply, Text: "x"},
		{Unit: thread, Kind: draft.React}} {
		if _, err := e.svc.Propose(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if _, err := e.svc.Propose(ctx, Proposal{Unit: thread, Kind: draft.React, Reaction: "eyes"}); err != nil {
		t.Errorf("react without text: %v", err)
	}
}

type fixedPolicy struct {
	p    draft.Policy
	seen []draft.Origin
}

func (f *fixedPolicy) For(ctx context.Context, u unit.Unit, o draft.Origin) (draft.Policy, error) {
	f.seen = append(f.seen, o)
	return f.p, nil
}

func TestProposeDefaultsToAlways(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("x"))
	if e.approval(d.ID) != (draft.Approval{Mode: draft.Wait}) || d.State != draft.Held {
		t.Fatalf("%+v", d)
	}
	pol := &fixedPolicy{p: *window(10 * time.Minute)}
	e.svc.Policies = pol
	w := e.propose(reply("y"))
	if e.approval(w.ID) != pol.p.Default || !e.gate(w.ID).Deadline.Equal(t0.Add(10*time.Minute)) || w.State != draft.Held || len(pol.seen) != 1 {
		t.Fatalf("window %+v", w)
	}
	p := reply("z")
	p.Policy = draft.Fixed(draft.Approval{Mode: draft.Wait})
	if got := e.propose(p); e.approval(got.ID).Mode != draft.Wait || len(pol.seen) != 1 {
		t.Fatalf("explicit approval %+v, policies asked %d times", got, len(pol.seen))
	}
	p.Policy = draft.Fixed(draft.Approval{Mode: draft.Delay})
	if _, err := e.svc.Propose(ctx, p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad approval: %v", err)
	}
}

func TestApprovingWithTextPostsThatText(t *testing.T) {
	e := newEnv()
	asIs, edited, gone := e.propose(reply("done")), e.propose(reply("done")), e.propose(reply("done"))
	e.approve(asIs.ID, nil)
	text := "done, thanks"
	e.approve(edited.ID, &text)
	e.decide(gone.ID, "discard")
	if got := e.get(edited.ID); got.Text != text || got.Original != "done" || !got.Edited || got.State != draft.Queued {
		t.Errorf("approve with text %+v", got)
	}
	if got := e.get(asIs.ID); got.Edited || got.State != draft.Queued {
		t.Errorf("approve as is %+v", got)
	}
	if got := e.get(gone.ID); got.Reason != "user" || got.State != draft.Dropped {
		t.Errorf("discard %+v", got)
	}
	if _, err := e.svc.Prepare(ctx, asIs.ID, e.get(asIs.ID).Rev, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("preparing a queued draft: %v", err)
	}
	held := e.propose(reply("later"))
	if _, err := e.svc.Prepare(ctx, held.ID, held.Rev+1, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("preparing at a stale rev: %v", err)
	}
}

type fakeApplier struct {
	prov items.Provenance
	ops  []items.Op
}

func (f *fakeApplier) apply(ctx context.Context, prov items.Provenance, ops []items.Op) error {
	f.prov, f.ops = prov, append(f.ops, ops...)
	return nil
}

func TestSentDraftBecomesItemEvidence(t *testing.T) {
	e := newEnv()
	ap := &fakeApplier{}
	e.svc.Evidence = EvidenceTo(ap.apply)
	p := reply("closing")
	p.Kind, p.Items = draft.Close, []string{"i1", "i2"}
	d := e.propose(p)
	e.approve(d.ID, nil)
	e.clock.now = t0.Add(time.Minute)
	if sent, _, err := e.svc.Send(ctx); sent != 1 || err != nil {
		t.Fatalf("send %d %v", sent, err)
	}
	got := e.get(d.ID)
	if got.Evidence.Ref != "ts.1" || got.Evidence.Permalink == "" || !got.Evidence.At.Equal(t0.Add(time.Minute)) {
		t.Fatalf("evidence %+v", got.Evidence)
	}
	if ap.prov != items.Effects || len(ap.ops) != 2 {
		t.Fatalf("applied %s %+v", ap.prov, ap.ops)
	}
	for i, op := range ap.ops {
		anchor := unit.Anchor{Unit: thread, Ref: "draft:1", Permalink: got.Evidence.Permalink, At: got.Evidence.At}
		want := items.Op{Op: items.OpEvidence, Unit: thread, Item: p.Items[i], Anchor: anchor,
			Evidence: &items.Evidence{Kind: items.EvidenceSentDraft, Anchor: anchor}}
		if op.Op != want.Op || op.Unit != want.Unit || op.Item != want.Item || op.Anchor != want.Anchor || *op.Evidence != *want.Evidence {
			t.Errorf("op %d = %+v, want %+v", i, op, want)
		}
	}
	none := reply("no items")
	if ops := EvidenceOps(e.propose(none)); len(ops) != 0 {
		t.Errorf("ops of a draft without items %v", ops)
	}
}

func TestRecordSentSkipsPublish(t *testing.T) {
	e := newEnv()
	ap := &fakeApplier{}
	e.svc.Evidence = EvidenceTo(ap.apply)
	ran := 0
	e.svc.OnSent = []func(context.Context, draft.Draft) error{func(context.Context, draft.Draft) error { ran++; return nil }}
	p := reply("posted by the session")
	p.Items = []string{"i1"}
	p.Origin = draft.Origin{Kind: draft.OriginSession, TurnSeq: 9}
	d, err := e.svc.RecordSent(ctx, p, draft.Evidence{Ref: "9.9", Permalink: "https://example.test/9"})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != draft.Sent || d.Evidence.Ref != "9.9" || d.Evidence.At.IsZero() {
		t.Fatalf("%+v", d)
	}
	if len(e.fx.published) != 0 || len(ap.ops) != 1 || ap.ops[0].Anchor.Ref != "draft:1" || ran != 1 {
		t.Fatalf("published %v ops %+v onsent %d", e.fx.published, ap.ops, ran)
	}
	e.disabled["slack"] = true
	if _, err := e.svc.RecordSent(ctx, p, draft.Evidence{Ref: "9.10"}); err != nil {
		t.Fatalf("a post already made is recorded even with the plugin off: %v", err)
	}
}

func TestHookErrorsDoNotUndoTheSend(t *testing.T) {
	e := newEnv()
	e.svc.Evidence = func(context.Context, draft.Draft) error { return errBoom }
	ran := false
	e.svc.OnSent = []func(context.Context, draft.Draft) error{func(context.Context, draft.Draft) error { ran = true; return nil }}
	d := e.propose(reply("x"))
	e.approve(d.ID, nil)
	sent, _, err := e.svc.Send(ctx)
	if sent != 1 || !errors.Is(err, errBoom) || !ran || e.get(d.ID).State != draft.Sent {
		t.Fatalf("sent %d err %v onsent %v state %s", sent, err, ran, e.get(d.ID).State)
	}
}

func TestOutboxLogHasNoText(t *testing.T) {
	e := newEnv()
	secret := "SECRET-TEXT"
	a := e.propose(reply(secret + " a"))
	b := e.propose(reply(secret + " b"))
	c := e.propose(reply(secret + " c"))
	edited := secret + " edited"
	e.svc.Edit(ctx, a.ID, edited)
	e.approve(a.ID, &edited)
	e.approve(b.ID, nil)
	e.decide(c.ID, "discard")
	e.fx.failWith = errors.New("refused: " + secret)
	e.svc.Send(ctx)
	e.svc.Retry(ctx, a.ID, true)
	e.fx.failWith = nil
	e.svc.Send(ctx)
	sending(e, secret+" d")
	e.svc.Recover(ctx)
	e.svc.RecordSent(ctx, reply(secret+" e"), draft.Evidence{Ref: "1"})
	if len(e.logs) < 6 {
		t.Fatalf("only %d log lines", len(e.logs))
	}
	for _, l := range e.logs {
		if strings.Contains(l, secret) {
			t.Errorf("log line holds text: %q", l)
		}
	}
}

func TestNotFound(t *testing.T) {
	e := newEnv()
	if _, err := e.svc.Prepare(ctx, 42, 0, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
