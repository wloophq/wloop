package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// basingPublisher is a recordingPublisher that reads a unit's live head, and fails every post with fail when set.
type basingPublisher struct {
	recordingPublisher
	head string
	fail error
}

func (p *basingPublisher) Live(_ context.Context, u unit.Unit, kind string) (string, error) {
	return p.head + " " + u.Key + " " + kind, nil
}

func (p *basingPublisher) Within(_ context.Context, u unit.Unit, target string) (bool, error) {
	return target == u.Key+" 0002", nil
}

func (p *basingPublisher) Publish(ctx context.Context, d draft.Draft) (draft.Evidence, error) {
	if p.fail != nil {
		return draft.Evidence{}, p.fail
	}
	return p.recordingPublisher.Publish(ctx, d)
}

func servedOutboxOf(t *testing.T, pub *basingPublisher) (*testEnv, daemon.Deps, *change.Log) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(shortDir(t), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te.db, te.changes = db, log
	mods, err := buildSources([]compiledPlugin{publishing(&pub.recordingPublisher)}, te.cfg, moduleDeps{DB: db, Clock: clockFunc(te.Now)}, bundledUI)
	if err != nil {
		t.Fatal(err)
	}
	for i := range mods {
		if mods[i].Publisher != nil {
			mods[i].Publisher = pub
		}
	}
	return te, te.serveOutbox(daemon.Deps{}, db, te.cfg, mods, t.Logf), log
}

func TestServeWiresKindsAndBases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pub := &basingPublisher{head: "c2"}
	te, d, _ := servedOutboxOf(t, pub)
	proposing, ok := te.proposalsJob(te.db, te.cfg, nil, t.Logf).Outbox.(*outbox.Service)
	if !ok {
		t.Fatal("the proposals job proposes through no outbox service")
	}
	for name, b := range map[string]outbox.Bases{"sender": d.Outbox.Bases, "proposals": proposing.Bases} {
		if b == nil {
			t.Fatalf("%s: the outbox reads no live basis", name)
		}
	}
	if got, err := d.Outbox.Bases.Live(ctx, threadUnit, "review"); err != nil || got != "c2 "+threadUnit.Key+" review" {
		t.Fatalf("the served outbox read the basis %q %v, want the publisher's live head", got, err)
	}
	if got, err := d.Outbox.Bases.Live(ctx, unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, "review"); err == nil {
		t.Fatalf("a provider that publishes nothing answered the basis %q", got)
	}
	fx, err := d.Outbox.Effects(threadUnit.Provider)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := fx.(port.Scoper)
	if !ok {
		t.Fatal("the served publisher says nothing of a target's unit")
	}
	for target, want := range map[string]bool{threadUnit.Key + " 0002": true, "C2 0001": false} {
		if in, err := sc.Within(ctx, threadUnit, target); err != nil || in != want {
			t.Errorf("%s within %s: %v %v", target, threadUnit.Key, in, err)
		}
	}
	t.Run("kinds", func(t *testing.T) {
		for name, k := range map[string]outbox.Kinds{"sender": d.Outbox.Kinds, "proposals": proposing.Kinds} {
			if k == nil {
				t.Fatalf("%s: the outbox checks no draft kind", name)
			}
			spec, ok := k.Lookup(string(unit.GitHub), "review")
			if !ok || spec.Delivery != "review" {
				t.Fatalf("%s: the GitHub review kind is %+v %v", name, spec, ok)
			}
			if err := k.Validate(spec, json.RawMessage(`{"event":"MERGE"}`)); !errors.Is(err, outbox.ErrInvalid) {
				t.Fatalf("%s: a review with no such event: %v", name, err)
			}
		}
	})
	t.Run("through the daemon", func(t *testing.T) {
		pub := &livePublisher{head: "c2"}
		td := servedWithGitHub(t, pub)
		s := td.Start(t, draftsPR, "review")
		propose := func(payload, basis string) error {
			_, err := td.Client().ProposeDraft(context.Background(), s.Handle, api.AgentDraftProposal{Kind: "review", Text: "One finding.",
				Payload: json.RawMessage(payload), Basis: basis})
			return err
		}
		if err := propose(`{"event":"MERGE"}`, "c2"); err == nil {
			t.Error("a review with no such event was proposed")
		}
		if err := propose(`{"event":"COMMENT"}`, "c1"); err == nil {
			t.Error("a review read at c1 was proposed while the head is c2")
		}
		d, err := td.Client().ProposeDraft(context.Background(), s.Handle, api.AgentDraftProposal{Kind: "review", Text: "One finding.",
			Payload: json.RawMessage(`{"event":"COMMENT"}`), Basis: "c2", Why: "the export reads a dropped column"})
		if err != nil {
			t.Fatalf("a review read at the live head: %v", err)
		}
		if got, err := td.Client().Draft(context.Background(), d.ID); err != nil || got.Why != "the export reads a dropped column" {
			t.Errorf("the draft's why %q %v, want the proposal's", got.Why, err)
		}
	})
}

func TestFailedSendWakesWorkflows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pub := &basingPublisher{fail: errors.New("Unprocessable Entity: line must be part of the diff")}
	te, d, log := servedOutboxOf(t, pub)
	queued := insertDraft(t, te.db, draft.Draft{Unit: threadUnit, Text: "one finding", State: draft.Queued})
	from := log.Gen()
	if _, _, err := d.Outbox.Send(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := te.db.Drafts().Get(ctx, queued.ID)
	if err != nil || got.State != draft.Failed {
		t.Fatalf("draft %+v %v, want failed", got, err)
	}
	ch := log.Since(from, port.SweepInputs)
	if !ch.Has(port.TopicDrafts) || !ch.All && !slices.Contains(ch.Units, threadUnit) {
		t.Fatalf("the failed send woke no sweep of %s: %+v", threadUnit, ch)
	}
	t.Run("the unit's facts", func(t *testing.T) {
		pub := &livePublisher{head: "c2", fail: fmt.Errorf("%w: the head is c3", outbox.ErrStale)}
		td := servedWithGitHub(t, pub)
		s := td.Start(t, draftsPR, "review")
		d, err := td.Client().ProposeDraft(ctx, s.Handle, api.AgentDraftProposal{Kind: "review", Text: "One finding.",
			Payload: json.RawMessage(`{"event":"COMMENT"}`), Basis: "c2"})
		if err != nil {
			t.Fatal(err)
		}
		if err := td.Send(ctx); err != nil {
			t.Fatal(err)
		}
		facts, err := td.e.engine.Units.Facts(ctx, draftsPR)
		if err != nil {
			t.Fatal(err)
		}
		if facts["draft.failed"] != true || facts["draft.stale"] != true || facts["draft.failed_kind"] != "review" ||
			!strings.HasPrefix(fmt.Sprint(facts["draft.failed_ref"]), fmt.Sprintf("%d:", d.ID)) {
			t.Fatalf("the unit's draft facts after a stale send: %v %v %v %v", facts["draft.failed"], facts["draft.stale"], facts["draft.failed_kind"], facts["draft.failed_ref"])
		}
	})
}

func TestDraftSentCompletesTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pub := &livePublisher{head: "c2"}
	td := servedWithGitHub(t, pub)
	s := td.Start(t, draftsPR, "review")
	d, err := td.Client().ProposeDraft(ctx, s.Handle, api.AgentDraftProposal{Kind: "review", Text: "Two changes before this merges.",
		Payload: json.RawMessage(`{"event":"REQUEST_CHANGES"}`), Basis: "c2"})
	if err != nil {
		t.Fatal(err)
	}
	td.EndTurn(t, s, api.TurnEndRequest{Turn: s.Round, PR: "https://github.com/acme/api/pull/7", Record: json.RawMessage(fmt.Sprintf(
		`{"state":"done","summary":"requested changes","outcome":{"verdict":"REQUEST_CHANGES","draft":{"id":%d,"rev":%d}}}`, d.ID, d.Rev))})
	if err := td.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if len(pub.sent()) != 1 {
		t.Fatalf("posted %d drafts, want the rule-sent review", len(pub.sent()))
	}
	b := td.Book(t, draftsPR)
	if len(b.Turns) != 1 || b.Turns[0].State != dturn.Done {
		t.Fatalf("turns %+v", b.Turns)
	}
	last, ok := b.LastRecord(b.Turns[0].Seq)
	if !ok || last.Record.Outcome == nil || last.Record.Outcome.URL != livePermalink(d.ID) || last.Record.Outcome.Verdict != "REQUEST_CHANGES" {
		t.Fatalf("the turn's last record %+v", last.Record.Outcome)
	}
}

var draftsPR = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}

func livePermalink(id int64) string {
	return fmt.Sprintf("https://github.com/acme/api/pull/7#pullrequestreview-%d", 9000+id)
}

// livePublisher posts into a list, reads its head as the live basis and fails every post with fail when set.
type livePublisher struct {
	mu    sync.Mutex
	head  string
	fail  error
	posts []draft.Draft
}

func (p *livePublisher) Publish(_ context.Context, d draft.Draft) (draft.Evidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return draft.Evidence{}, p.fail
	}
	p.posts = append(p.posts, d)
	return draft.Evidence{Ref: fmt.Sprint(d.ID), Permalink: livePermalink(int64(d.ID))}, nil
}

func (p *livePublisher) Verify(context.Context, draft.Draft, time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

func (p *livePublisher) Live(context.Context, unit.Unit, string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.head, nil
}

func (p *livePublisher) sent() []draft.Draft {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.posts)
}

// holds is a rule condition that holds for every draft.
type holds struct{}

func (holds) Holds(map[string]any, time.Time) (bool, error) { return true, nil }

func TestServeNotifiesRuleSent(t *testing.T) {
	t.Parallel()
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	now := func() time.Time { return testNow }
	d := ServeForTest(t, TestOptions{Now: now, Publishers: map[unit.ProviderID]port.Effects{unit.GitHub: &basingPublisher{head: "c2"}},
		Plugins: map[string]src.Source{string(unit.GitHub): fakeGitHubPlugin(&fakeGH{basics: openBasics}, now)}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := d.Client().Events(ctx, "test", api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	rule := &draft.Policy{Default: draft.Approval{Mode: draft.Wait},
		Rules: []draft.Rule{{When: holds{}, Approval: draft.Approval{Mode: draft.Send}, Names: draft.CommentDelivery}}}
	sent, err := d.e.outbox.Propose(ctx, outbox.Proposal{Unit: pr, Kind: draft.Reply, Text: "Thanks, merged the fix.",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "k"}, Policy: rule})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Send(ctx); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("draft:%d:by-rule", sent.ID)
	deadline := time.After(time.Minute)
	for {
		select {
		case ch, ok := <-events:
			if !ok {
				t.Fatal("the event stream ended before the notice")
			}
			if slices.ContainsFunc(ch.Notices, func(n api.Notice) bool { return n.ID == want }) {
				return
			}
		case <-deadline:
			t.Fatalf("no notice %s", want)
		}
	}
}

func TestAResumePointsToItsTurnsOpenDrafts(t *testing.T) {
	t.Parallel()
	te, _, _ := servedOutboxOf(t, &basingPublisher{})
	of := func(seq int64, state draft.State, basis string) draft.Draft {
		return insertDraft(t, te.db, draft.Draft{Unit: threadUnit, Text: "x", State: state, Basis: basis,
			Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: seq}})
	}
	held, failed := of(3, draft.Held, "c1"), of(4, draft.Failed, "")
	of(5, draft.Held, "")
	of(4, draft.Sent, "")
	insertDraft(t, te.db, draft.Draft{Unit: threadUnit, Text: "a rule's", State: draft.Held, Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "r", Key: "k"}})
	b := dturn.Book{Unit: threadUnit, Turns: []dturn.Turn{{Seq: 3, Intent: "review"}, {Seq: 4, Intent: "review", ResumeOf: 3}, {Seq: 5, Intent: "review"}}}
	got := te.resumeDrafts(context.Background(), b, 4, "c2")
	var ids []int64
	for _, p := range got {
		ids = append(ids, p.ID)
		if p.ID == int64(held.ID) && (!p.Stale || p.CurrentBasis != "c2") {
			t.Errorf("draft %d written at c1 is not stale at c2: %+v", p.ID, p)
		}
	}
	slices.Sort(ids)
	if want := []int64{int64(held.ID), int64(failed.ID)}; !slices.Equal(ids, want) {
		t.Fatalf("pointers to %v, want the chain's open drafts %v", ids, want)
	}
}

// servedWithGitHub is a test daemon whose GitHub drafts post through pub, with the real GitHub plugin over a
// fake GitHub.
func servedWithGitHub(t *testing.T, pub port.Effects) *TestDaemon {
	t.Helper()
	now := func() time.Time { return testNow }
	return ServeForTest(t, TestOptions{Now: now, Publishers: map[unit.ProviderID]port.Effects{unit.GitHub: pub},
		Plugins: map[string]src.Source{string(unit.GitHub): fakeGitHubPlugin(&fakeGH{basics: openBasics}, now)}})
}
