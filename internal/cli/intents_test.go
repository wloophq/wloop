package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func TestAConfigThatStopsLoadingKeepsTheLastProfileIntents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	at := filepath.Join(dir, "intents", "ask-author")
	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := "label = \"Ask author\"\nsummary = \"Ask\"\nworkspace = \"review\"\nsession = { strategy = \"new\", title = \"ask\" }\n" +
		"[headline]\nresumed = \"ask\"\n[[headline.new]]\ntext = \"Ask.\"\n[record]\nstates = [\"done\", \"failed\"]\n"
	if err := os.WriteFile(filepath.Join(at, "intent.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "instructions.md.tmpl"), []byte("# {{.Headline}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var loadErr error
	var warned bytes.Buffer
	e := &Env{Err: &warned, LoadConfig: func() (config.Config, error) { return config.Config{Profile: dir}, loadErr }}
	e.intents = newEnvIntents(e)
	if _, ok := e.loadedIntents().Registry.Get(dintent.Name("ask-author")); !ok {
		t.Fatal("the profile's intent did not load")
	}
	loadErr = errors.New("config.json: bad json")
	if err := os.Chtimes(filepath.Join(at, "intent.toml"), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.loadedIntents().Registry.Get(dintent.Name("ask-author")); !ok {
		t.Fatal("a config failure dropped the profile's intents")
	}
	e.loadedIntents()
	if n := strings.Count(warned.String(), "config.json: bad json"); n != 1 {
		t.Fatalf("warned %d times: %q", n, warned.String())
	}
}

func TestATurnParsesTheProfileAtMostOnce(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	dir := userProfile(t, te, map[string]string{"intent=review.md": "Be kind.\n"})
	te.cfg.Profile = dir
	loads := 0
	c, err := turn.NewCache(func() []profile.Layer { return profile.Chain(te.cfg) }, intentload.CacheOptions{
		Recheck: time.Hour,
		LoadTree: func(chain []profile.Layer, r *dintent.Registry, broken []dintent.Name) (profile.Tree, error) {
			loads++
			return profile.Load(chain, r, broken)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	te.Env.intents = &envIntents{cache: c}
	round := func() {
		t.Helper()
		if err := te.roundReady(); err != nil {
			t.Fatal(err)
		}
		req := turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Batch export"}}
		if _, _, err := te.renderTurn(context.Background(), te.cfg, req); err != nil {
			t.Fatal(err)
		}
		req = turn.Request{Intent: turn.Intent(dintent.Address), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Batch export"}, Owned: true, Inputs: map[string]any{"self_review": true}}
		c, _, err := te.renderTurn(context.Background(), te.cfg, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Guides) != 2 {
			t.Fatalf("%d guides, want address and review", len(c.Guides))
		}
	}
	round()
	if loads != 1 {
		t.Fatalf("%d profile loads on a cold cache", loads)
	}
	round()
	if loads != 1 {
		t.Fatalf("%d profile loads after a warm round", loads)
	}
}

func TestABrokenBlockStillStopsTheRound(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	te.cfg.Profile = userProfile(t, te, map[string]string{"intent=review.md": "\n+++\noverride = true\n+++\nX"})
	if err := te.roundReady(); err == nil {
		t.Fatal("a broken block did not stop the round")
	}
}
