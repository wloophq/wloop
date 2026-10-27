package cli

import (
	"context"
	"net/url"
	"testing"

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
	te.saveToken(t, fakeToken())
	te.srv.Handle("conversations.replies", func(v url.Values) any {
		return map[string]any{"messages": []any{map[string]any{"type": "message", "ts": v.Get("ts"), "thread_ts": "1700000000.000100", "user": "U001"}}}
	})
	u, err := te.unitTarget(context.Background(), "C000/1700000050.000200")
	if want := dslack.ThreadUnit("T000", "C000", "1700000000.000100"); err != nil || u != want {
		t.Fatalf("unitTarget = %+v, %v; want %+v", u, err, want)
	}
}
