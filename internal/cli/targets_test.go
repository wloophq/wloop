package cli

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAProviderKeyTargetNamesEveryCompiledInProvider(t *testing.T) {
	e := &Env{}
	for _, p := range e.sourcePlugins() {
		arg := string(p.ID) + ":acme-key"
		u, err := e.unitTarget(context.Background(), arg)
		if err != nil || u != (unit.Unit{Provider: p.ID, Key: "acme-key"}) {
			t.Errorf("%s: %+v, %v", arg, u, err)
		}
	}
	if _, err := e.unitTarget(context.Background(), "nowhere:acme-key"); err == nil {
		t.Error("an unknown provider's key named a unit")
	}
}

func TestAnIntentsUsageNamesItsTargetThroughItsProvidersCodec(t *testing.T) {
	for _, p := range plugins {
		d := dintent.Def{Name: "sum-up", Provider: p.ID}
		if got, want := intentTarget(d), unit.NewRegistry(p.Codec).Target(p.ID); got != want {
			t.Errorf("%s: target %q, want the codec's %q", p.ID, got, want)
		}
	}
	if got := intentTarget(dintent.Def{Name: "sum-up", Provider: "forge"}); got != "<forge:key>" {
		t.Errorf("an unknown provider's target %q", got)
	}
}

func TestASlackMessageRefTargetNamesItsThread(t *testing.T) {
	te := newSlackTestEnv(t)
	startSlackDaemon(t, te)
	te.saveToken(t, fakeToken())
	te.srv.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []any{map[string]any{"type": "message", "ts": v.Get("ts"), "thread_ts": "1700000000.000100", "user": "U001"}}}
	})
	u, err := te.unitTarget(context.Background(), "C000/1700000050.000200")
	if want := dslack.ThreadUnit("T000", "C000", "1700000000.000100"); err != nil || u != want {
		t.Fatalf("unitTarget = %+v, %v; want %+v", u, err, want)
	}
}

func TestASlackDirectMessageRefTargetNamesItsSegment(t *testing.T) {
	te := newSlackTestEnv(t)
	db := startSlackDaemon(t, te)
	te.saveToken(t, fakeToken())
	ctx := context.Background()
	facts := dslack.Facts{Team: "T000", Conversations: []dslack.Conversation{{ID: "D000", Kind: dslack.KindIM, DMUser: "U001"}}}
	if err := (&slacksource.Store{DB: db.Slack()}).Save(ctx, nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}
	segs, err := (slacksource.Segments{Store: db.Segments(), Clock: clockFunc(func() time.Time { return testNow })}).Apply(ctx, "T000", "D000",
		[]dslack.Span{{FromTS: "1700000000.000000"}})
	if err != nil || len(segs) != 1 {
		t.Fatalf("segments %v %v", segs, err)
	}
	u, err := te.unitTarget(ctx, "D000/1700000050.000200")
	if err != nil || u != segs[0] {
		t.Fatalf("unitTarget = %+v, %v; want the segment %+v", u, err, segs[0])
	}
	if n := len(te.srv.Calls("conversations.replies")); n != 0 {
		t.Fatalf("conversations.replies calls %d, want none", n)
	}
}

func TestTheDaemonResolvesASlackRefWithoutAskingItself(t *testing.T) {
	te := newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	seg := dslack.ThreadUnit("T000", "C000", "1700000000.000100")
	te.resolve = func(_ context.Context, address string) (unit.Unit, bool, error) {
		return seg, address == "C000/1700000050.000200", nil
	}
	if u, err := te.unitTarget(context.Background(), "C000/1700000050.000200"); err != nil || u != seg {
		t.Fatalf("unitTarget = %+v, %v", u, err)
	}
	if _, err := te.unitTarget(context.Background(), "C000/1700000060.000200"); err == nil || !strings.Contains(err.Error(), "resolves no Slack unit") {
		t.Fatalf("an address the daemon does not resolve: %v", err)
	}
}
