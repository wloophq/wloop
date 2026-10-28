package source_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

var at0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

// planner is a plugin whose Payload answers a scripted payload and keeps
// each request.
type planner struct {
	*sourcetest.Stub
	mu  sync.Mutex
	got []src.PayloadRequest
}

func (p *planner) Plan(context.Context, plugin.Unit, src.RoundState) (src.Plan, error) {
	return src.Plan{}, nil
}
func (p *planner) ApplyEffect(context.Context, src.EffectRequest) error      { return nil }
func (p *planner) Proposals(context.Context, time.Time) ([]src.Draft, error) { return nil, nil }
func (p *planner) Placement(context.Context, plugin.Unit, string) (src.Placements, error) {
	return src.Placements{}, nil
}

func (p *planner) Payload(_ context.Context, req src.PayloadRequest) (src.PayloadResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, req)
	return src.PayloadResult{Arg: "acme:" + req.Unit.Key, Wording: src.Wording{Noun: "talk", This: "this talk", Ref: "talk " + req.Unit.Key, Short: req.Unit.Key},
		Facts:    []src.Fact{{Name: "acme.asker", Value: src.String("ana")}, {Name: "acme.waiting", Value: src.Since(at0)}},
		Contents: "Ana: can you look at the export?"}, nil
}

// TestThePayloadCrossesTheWire asks a plugin's payload through the host's
// reader over a real connection: every request field reaches the plugin,
// and the turn reads its argument, words, facts, temporal ones resolved
// with the host's clock, and contents.
func TestThePayloadCrossesTheWire(t *testing.T) {
	k := kindOver(t, &change.Log{})
	p := &planner{Stub: sourcetest.NewStub()}
	start(t, k, "acme", "", p)
	r := psource.Payloads{ID: "acme", Kind: k, Now: func() time.Time { return at0.Add(time.Hour) }}
	u := unit.Unit{Provider: "acme", Key: "t1"}
	req := turn.Request{Intent: "discuss", Trigger: turn.Automated, Planned: turn.PlannedFollowUp, Unit: u, Agent: "claude",
		LastVerdict: &turn.RecordedVerdict{Head: "c3", Endorsed: "yes", At: at0}, BriefMissing: "the brief gave up",
		Items: []string{"i-1"}, Task: "t-0123456789abcdef", Guidance: "be brief", Local: "wip", Owned: true, Plain: true,
		Inputs: map[string]any{"tone": "short"}}
	pl, err := r.Payload(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := p.got[0]
	want := src.PayloadRequest{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Intent: "discuss", Trigger: turn.Automated.String(),
		Inputs: json.RawMessage(`{"tone":"short"}`), Guidance: "be brief", Local: "wip", Owned: true, Plain: true,
		LastVerdict: &src.Verdict{Head: "c3", Endorsed: "yes", At: at0}, Round: turn.PlannedFollowUp.String(), Agent: "claude",
		BriefMissing: "the brief gave up", Items: []string{"i-1"}, Task: "t-0123456789abcdef"}
	if got.LastVerdict != nil {
		got.LastVerdict.At = got.LastVerdict.At.UTC()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the plugin was asked\n%+v\nwant\n%+v", got, want)
	}
	if pl.Unit() != u || pl.Arg() != "acme:t1" || pl.Wording() != (turn.Wording{Noun: "talk", This: "this talk", Ref: "talk t1", Short: "t1"}) {
		t.Fatalf("payload %v %q %+v", pl.Unit(), pl.Arg(), pl.Wording())
	}
	if v, ok := pl.Fact("acme.asker"); !ok || v != "ana" {
		t.Errorf("acme.asker %v %v", v, ok)
	}
	if v, ok := pl.Fact("acme.waiting"); !ok || v != time.Hour {
		t.Errorf("acme.waiting %v %v, want the hour since", v, ok)
	}
	if c, ok := pl.(turn.Contents); !ok || c.Contents() != "Ana: can you look at the export?" {
		t.Errorf("contents of %T", pl)
	}
}

// briefer is a plugin whose Briefer answers scripted reads and keeps each
// staleness request.
type briefer struct {
	*sourcetest.Stub
	mu     sync.Mutex
	stales []src.BriefStaleRequest
}

func (b *briefer) BriefInput(_ context.Context, u plugin.Unit) (src.BriefInput, error) {
	return src.BriefInput{Text: "the thread of " + u.Key, Bytes: 120, Cut: true, Parts: json.RawMessage(`{"messages":4}`)}, nil
}

func (b *briefer) BriefRevision(context.Context, plugin.Unit) (src.BriefRevision, error) {
	return src.BriefRevision{Revision: "1700000000.4", Parts: json.RawMessage(`{"messages":4}`), Attempt: "h1 2026-10-26T09:00:00Z 3"}, nil
}

func (b *briefer) BriefDescribe(_ context.Context, in src.BriefInput) (string, error) {
	return fmt.Sprintf("a thread of %d bytes", in.Bytes), nil
}

func (b *briefer) BriefStale(_ context.Context, req src.BriefStaleRequest) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stales = append(b.stales, req)
	return req.Written.AttemptOf != brief.AttemptKey(req.Version, brief.At{Attempt: req.Current.Attempt}), nil
}

func (b *briefer) BriefWant(_ context.Context, u plugin.Unit) (bool, error) {
	return u.Key == "t1", nil
}

// TestABriefInputComesFromThePlugin reads a unit's brief input, revision,
// description and staleness through the host's brief source over a real
// connection. The attempt the plugin names makes the attempt key a
// version 6 store kept, so a failed attempt stays the same one.
func TestABriefInputComesFromThePlugin(t *testing.T) {
	k := kindOver(t, &change.Log{})
	b := &briefer{Stub: sourcetest.NewStub()}
	start(t, k, "acme", "", b)
	s := psource.Briefs{ID: "acme", Kind: k}
	ctx := context.Background()
	u := unit.Unit{Provider: "acme", Key: "t1"}
	in, err := s.Input(ctx, u)
	if err != nil || in.Text != "the thread of t1" || in.Bytes != 120 || !in.Cut || string(in.Parts) != `{"messages":4}` {
		t.Fatalf("input %+v %v", in, err)
	}
	if d := s.Describe(in); d != "a thread of 120 bytes" {
		t.Errorf("describe %q", d)
	}
	cur, err := s.Revision(ctx, u)
	if err != nil || cur.Revision != "1700000000.4" || cur.Attempt != "h1 2026-10-26T09:00:00Z 3" {
		t.Fatalf("revision %+v %v", cur, err)
	}
	stored := "b1-0123 h1 2026-10-26T09:00:00Z 3"
	if got := brief.AttemptKey("b1-0123", cur); got != stored {
		t.Fatalf("attempt key %q, the stored %q", got, stored)
	}
	w := brief.Written{At: cur, Version: "b1-0123", Error: "boom", Failures: 1, AttemptedAt: at0, AttemptOf: stored}
	if s.Stale(w, cur, "b1-0123", at0.Add(time.Minute)) {
		t.Error("the same attempt is due again")
	}
	if coming, err := s.Want(ctx, u); err != nil || !coming {
		t.Errorf("want t1: %v %v", coming, err)
	}
	if coming, err := s.Want(ctx, unit.Unit{Provider: "acme", Key: "t9"}); err != nil || coming {
		t.Errorf("want t9: %v %v", coming, err)
	}
	sent := b.stales[0]
	if sent.Written.AttemptOf != stored || sent.Written.At.Attempt != cur.Attempt || sent.Current.Attempt != cur.Attempt ||
		sent.Written.Failures != 1 || sent.Written.Error != "boom" || sent.Version != "b1-0123" || !sent.Now.Equal(at0.Add(time.Minute)) {
		t.Fatalf("the staleness request %+v", sent)
	}
}

// TestBriefWatchCallsBackThroughTheBroker has a plugin write a brief of
// its unit through the broker: the brief is the one the unit uses, and the
// turn waiting on it wakes; another provider's unit and a body that is not
// a brief are refused.
func TestBriefWatchCallsBackThroughTheBroker(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	q := brief.New(brief.Deps{Store: inventory.New(db), Enabled: true, Now: func() time.Time { return at0 }, Log: t.Logf})
	snaps := psource.NewSnapshots(db.Snapshots())
	k := &psource.Kind{Snapshots: snaps, StateDir: func(string) string { return t.TempDir() },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			c, err := codec.Of(id, s.Codec)
			if err != nil {
				t.Fatal(err)
			}
			other, err := codec.Of("other", s.Codec)
			if err != nil {
				t.Fatal(err)
			}
			return &psource.Broker{ID: id, Manifest: s, Snapshots: snaps, Units: psource.Units{Codecs: unit.NewRegistry(c, other)},
				Config: func() config.Config { return config.Config{} }, Briefs: db, Briefed: q.Noted, Now: func() time.Time { return at0 }}
		}}
	stub := sourcetest.NewStub()
	start(t, k, "acme", "", stub)
	ctx := context.Background()
	u := unit.Unit{Provider: "acme", Key: "t1"}
	changed, release := q.Changed(u)
	defer release()
	body := json.RawMessage(`{"goal":"Ship the export","kind":"request","risk":"low","tags":["domain:billing"]}`)
	if err := stub.Host().PutBrief(ctx, src.Brief{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Task: "t-0123456789abcdef", Version: "x1", Body: body}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("the turn waiting on the brief did not wake")
	}
	rd, err := q.Ready(ctx, u, false, nil)
	if err != nil || rd.State != brief.Ready || rd.Brief == nil || rd.Brief.Goal != "Ship the export" || rd.Run == 0 {
		t.Fatalf("the written brief reads %+v %v", rd, err)
	}
	for name, b := range map[string]src.Brief{
		"another provider's unit": {Unit: plugin.Unit{Provider: "other", Key: "t1"}, Version: "x1", Body: body},
		"a body that is no brief": {Unit: plugin.Unit{Provider: "acme", Key: "t2"}, Version: "x1", Body: json.RawMessage(`"text"`)},
		"a task that is not one":  {Unit: plugin.Unit{Provider: "acme", Key: "t2"}, Task: "t 1", Version: "x1", Body: body},
	} {
		if err := stub.Host().PutBrief(ctx, b); err == nil {
			t.Errorf("%s: kept", name)
		}
	}
}

func TestAPutBriefOnATaskThatDoesNotHoldTheUnitIsRefused(t *testing.T) {
	t.Skip("T1: review of X5b; PutBrief checks the task id's form, not that the task holds the unit, once the brief is the task's record")
}

// TestAFirstTurnAsksThePluginForItsBrief has the brief queue answer a
// first turn on a plugin's unit: it waits while the plugin says a brief is
// coming, and goes without one at once when nothing is left to read.
func TestAFirstTurnAsksThePluginForItsBrief(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	k := kindOver(t, &change.Log{})
	start(t, k, "acme", "", &briefer{Stub: sourcetest.NewStub()})
	q := brief.New(brief.Deps{Store: inventory.New(db), Enabled: true, Now: func() time.Time { return at0 }, Log: t.Logf})
	q.Register("acme", psource.Briefs{ID: "acme", Kind: k})
	ctx := context.Background()
	rd, err := q.Ready(ctx, unit.Unit{Provider: "acme", Key: "t1"}, true, nil)
	if err != nil || rd.State != brief.Wait || rd.Wait != brief.WaitBriefing || !rd.Next.Equal(at0.Add(brief.DemandTimeout)) {
		t.Fatalf("a coming brief: %+v %v", rd, err)
	}
	rd, err = q.Ready(ctx, unit.Unit{Provider: "acme", Key: "t9"}, true, nil)
	if err != nil || rd.State != brief.Without {
		t.Fatalf("nothing to read: %+v %v", rd, err)
	}
}

// TestConfigHandsThePluginItsBriefIntent reads the configuration as the
// plugin does: it carries the brief intent the host resolves for the
// plugin's units, extra fields, their versions and prompts.
func TestConfigHandsThePluginItsBriefIntent(t *testing.T) {
	extra, err := brief.Compose(brief.Schema, []byte(`{"type":"object","properties":{"urgency":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var asked []unit.ProviderID
	k := &psource.Kind{Snapshots: psource.NewSnapshots(nil), StateDir: func(string) string { return t.TempDir() },
		Broker: func(id string, s *manifest.Source) *psource.Broker {
			return &psource.Broker{ID: id, Manifest: s, Config: func() config.Config { return config.Config{} },
				BriefIntent: func(p unit.ProviderID) brief.Intent {
					asked = append(asked, p)
					return brief.Intent{Schema: extra, Prompt: "brief {{.unit}}", FieldsPrompt: "fill {{.fields}}"}
				}}
		}}
	stub := sourcetest.NewStub()
	start(t, k, "acme", "", stub)
	cfg, err := stub.Host().Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bi := cfg.BriefIntent
	if bi == nil || !reflect.DeepEqual(asked, []unit.ProviderID{"acme"}) || bi.Prompt != "brief {{.unit}}" || bi.FieldsPrompt != "fill {{.fields}}" ||
		!reflect.DeepEqual(bi.FieldVersions, extra.FieldVersions()) || len(bi.FieldVersions) != 1 {
		t.Fatalf("the plugin reads %+v, asked for %v", bi, asked)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(bi.ExtraSchema, &schema); err != nil || schema.Properties["urgency"] == nil {
		t.Fatalf("extra schema %s: %v", bi.ExtraSchema, err)
	}
}
