package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var threadUnit = unit.Unit{Provider: ingesttest.Provider, Key: "C1 0001"}

// recordingPublisher posts into a list; Verify finds what found says.
type recordingPublisher struct {
	mu    sync.Mutex
	calls []string
	found bool
}

func (p *recordingPublisher) Publish(_ context.Context, d draft.Draft) (draft.Evidence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fmt.Sprintf("publish %d", d.ID))
	return draft.Evidence{Ref: fmt.Sprint(d.ID)}, nil
}

func (p *recordingPublisher) Verify(_ context.Context, d draft.Draft, _ time.Time) (draft.Evidence, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fmt.Sprintf("verify %d", d.ID))
	return draft.Evidence{Ref: fmt.Sprint(d.ID)}, p.found, nil
}

// Where names a draft's target as Slack would, and knows no destination for a draft without one.
func (p *recordingPublisher) Where(_ context.Context, d draft.Draft) (string, error) {
	if d.Target == "" {
		return "", errors.New("no destination")
	}
	return d.Target + " (C0GEN)", nil
}

func (p *recordingPublisher) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// publishing is the thread plugin with pub as its Publisher.
func publishing(pub *recordingPublisher) source.Plugin {
	p := threadModule(threadUnit)
	build := p.Build
	p.Build = func(cfg config.Config, d source.Deps) (source.Module, error) {
		m, err := build(cfg, d)
		m.Publisher = pub
		return m, err
	}
	return p
}

func insertDraft(t *testing.T, db *sqlite.DB, d draft.Draft) draft.Draft {
	t.Helper()
	if d.Kind == "" {
		d.Kind = draft.Reply
	}
	if d.Original == "" {
		d.Original = d.Text
	}
	if d.Approval.Mode == "" {
		d.Approval.Mode = draft.Always
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt, d.UpdatedAt = testNow, testNow
	}
	got, err := db.Drafts().Insert(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestServeRecoversBeforeSending(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	dir := shortDir(t)
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(dir, "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pub := &recordingPublisher{found: true}
	stuck := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "left sending", State: draft.Sending, SendingAt: testNow})
	ready := insertDraft(t, db, draft.Draft{Unit: threadUnit, Text: "approved", State: draft.Approved})
	cfg := te.cfg
	cfg.HTTP.Port = 0
	mods, err := buildSources([]source.Plugin{publishing(pub)}, cfg, source.Deps{DB: db, Clock: clockFunc(te.Now)})
	if err != nil {
		t.Fatal(err)
	}
	te.changes = log
	d := te.serveOutbox(daemon.Deps{Cfg: cfg, Version: "test", Now: te.Now, Log: t.Logf, Changes: log,
		StateDir: dir, Socket: filepath.Join(dir, "d.sock"), LockPath: filepath.Join(dir, "d.lock"), InfoPath: filepath.Join(dir, "http.json")},
		db, cfg, mods, t.Logf)
	if !d.SendDrafts || d.Outbox == nil {
		t.Fatalf("no sender: %+v", d.Outbox)
	}
	srv := daemon.New(d)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	want := []string{fmt.Sprintf("verify %d", stuck.ID), fmt.Sprintf("publish %d", ready.ID)}
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Equal(pub.seen(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("calls %v, want %v", pub.seen(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := client.New(d.Socket)
	got, err := c.Draft(ctx, int64(stuck.ID))
	if err != nil || got.State != "sent" {
		t.Fatalf("the recovered draft: %+v %v", got, err)
	}
	time.Sleep(50 * time.Millisecond)
	if calls := pub.seen(); !slices.Equal(calls, want) {
		t.Fatalf("published again: %v", calls)
	}
}

func TestNoJobsServesNoSender(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.noJobs = true
	d := te.serveOutbox(daemon.Deps{}, te.db, te.cfg, nil, t.Logf)
	if d.Outbox == nil || d.SendDrafts {
		t.Fatalf("outbox %v, sender %v", d.Outbox != nil, d.SendDrafts)
	}
}

func TestOutboxEffectsFollowThePlugins(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	cfg := te.cfg
	off := false
	cfg.Plugins = map[string]config.Plugin{"slack": {Enabled: &off}}
	pub := &recordingPublisher{}
	mods, err := buildSources([]source.Plugin{publishing(pub)}, cfg, source.Deps{DB: te.db, Clock: clockFunc(te.Now)})
	if err != nil {
		t.Fatal(err)
	}
	d := te.serveOutbox(daemon.Deps{}, te.db, cfg, mods, t.Logf)
	if _, err := d.Outbox.Effects(ingesttest.Provider); err != nil {
		t.Fatalf("thread: %v", err)
	}
	for p, says := range map[unit.ProviderID]string{"slack": "disabled", unit.GitHub: "publishes nothing"} {
		if _, err := d.Outbox.Effects(p); !errors.Is(err, outbox.ErrDisabled) || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if d.Outbox.Items == nil || d.Outbox.Evidence == nil || d.Outbox.Policies == nil {
		t.Fatal("the outbox is not wired to the ledger and the policies")
	}
}

func TestTurnDraftIsPinnedToItsUnit(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	other := unit.Unit{Provider: ingesttest.Provider, Key: "C1 0002"}
	books := policyBooks{threadUnit: {Unit: threadUnit, Turns: []dturn.Turn{{Seq: 7, Intent: "review"}}}}
	pub := &recordingPublisher{}
	fx := pinnedEffects{Effects: pub, turns: outboxManifests{loaded: te.loadedIntents, books: books}}
	ctx := context.Background()
	turnOn := func(u unit.Unit, seq int64) draft.Draft {
		return draft.Draft{ID: 1, Unit: u, Kind: draft.Reply, Text: "x", Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: seq}}
	}
	if err := fx.Validate(ctx, turnOn(threadUnit, 7)); err != nil {
		t.Fatalf("its own unit: %v", err)
	}
	for name, d := range map[string]draft.Draft{"another unit": turnOn(other, 7), "no turn": turnOn(threadUnit, 0), "another turn": turnOn(threadUnit, 8)} {
		if err := fx.Validate(ctx, d); err == nil {
			t.Errorf("%s: validated", name)
		}
		if _, err := fx.Publish(ctx, d); err == nil || strings.HasPrefix(err.Error(), "may have been posted") {
			t.Errorf("%s: published: %v", name, err)
		}
	}
	session := draft.Draft{Unit: other, Kind: draft.Reply, Text: "x", Origin: draft.Origin{Kind: draft.OriginSession, TurnSeq: 7}}
	if err := fx.Validate(ctx, session); err == nil {
		t.Error("a session draft with another unit's turn validated")
	}
	for _, o := range []draft.Origin{{Kind: draft.OriginAuto, Rule: "r"}, {Kind: draft.OriginUser}, {Kind: draft.OriginSession}} {
		if err := fx.Validate(ctx, draft.Draft{Unit: other, Kind: draft.Reply, Text: "x", Origin: o}); err != nil {
			t.Errorf("%s: %v", o.Kind, err)
		}
	}
	if calls := pub.seen(); len(calls) != 0 {
		t.Fatalf("published %v", calls)
	}
	o := &outbox.Service{Store: te.db.Drafts(), Clock: clockFunc(te.Now), Effects: func(unit.ProviderID) (port.Effects, error) { return fx, nil }}
	if _, err := o.Propose(ctx, outbox.Proposal{Unit: other, Kind: draft.Reply, Text: "x", Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 7}}); !errors.Is(err, outbox.ErrInvalid) {
		t.Fatalf("propose on another unit: %v", err)
	}
}
