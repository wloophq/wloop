package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
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
	t.Run("kinds", func(t *testing.T) {
		t.Skip("T13 waits on T1: the manifests' draft kinds as outbox.Kinds")
		if d.Outbox.Kinds == nil || proposing.Kinds == nil {
			t.Fatal("the outbox checks no draft kind")
		}
	})
	t.Run("through the daemon", func(t *testing.T) {
		t.Skip("T13 waits on T3 and T7: propose checks the payload and the basis, and the agent route serves it")
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
		t.Skip("T13 waits on T9: the facts/drafts provider that reads draft.failed")
	})
}

func TestDraftSentCompletesTurn(t *testing.T) {
	t.Parallel()
	t.Skip("T13 waits on T9: turns.(*Service).DraftSent")
	te, d, _ := servedOutboxOf(t, &basingPublisher{})
	if te.Turns == nil || len(d.Outbox.OnSent) == 0 {
		t.Fatal("a sent draft completes no turn")
	}
}
