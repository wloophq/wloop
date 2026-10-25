package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

type holderEnv struct {
	*testing.T
	e     *Env
	loads int
	now   time.Time
	notes []string
}

func newHolderEnv(t *testing.T, cfg map[string]any) *holderEnv {
	t.Helper()
	h := &holderEnv{T: t, e: &Env{Err: os.Stderr}, now: time.Unix(5000, 0)}
	h.e.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	h.e.LoadConfig = func() (config.Config, error) { h.loads++; return config.LoadFile(h.e.ConfigPath) }
	h.e.Now = func() time.Time { return h.now }
	h.write(cfg)
	start, err := h.e.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	h.e.cfg = config.NewHolder(start, config.HolderOptions{
		Path: h.e.ConfigPath, Load: h.e.readConfig, Now: h.e.Now,
		OnProfileChange: func(p string) { h.notes = append(h.notes, p) },
	})
	h.e.intents = newEnvIntents(h.e)
	h.loads = 0
	return h
}

func (h *holderEnv) write(cfg map[string]any) {
	h.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		h.Fatal(err)
	}
	if err := os.WriteFile(h.e.ConfigPath, raw, 0o600); err != nil {
		h.Fatal(err)
	}
	h.now = h.now.Add(time.Second)
	if err := os.Chtimes(h.e.ConfigPath, h.now, h.now); err != nil {
		h.Fatal(err)
	}
}

func intentProfile(t *testing.T) string {
	t.Helper()
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
	return dir
}

func TestServeIntentLayersReadNoConfigFile(t *testing.T) {
	t.Parallel()
	h := newHolderEnv(t, map[string]any{})
	for range 50 {
		h.now = h.now.Add(config.StatEvery)
		h.e.loadedIntents()
	}
	if h.loads != 0 {
		t.Fatalf("%d config reads for intents", h.loads)
	}
}

func TestAProfileEditReloadsLive(t *testing.T) {
	t.Parallel()
	h := newHolderEnv(t, map[string]any{})
	if _, ok := h.e.loadedIntents().Registry.Get(dintent.Name("ask-author")); ok {
		t.Fatal("the intent exists before the profile names it")
	}
	h.write(map[string]any{"profile": intentProfile(t)})
	h.now = h.now.Add(config.StatEvery)
	if _, ok := h.e.loadedIntents().Registry.Get(dintent.Name("ask-author")); !ok {
		t.Fatal("a profile edit did not reach the intents")
	}
	if h.e.cfg.Stale() {
		t.Fatal("a profile edit asked for a restart")
	}
	if len(h.notes) != 1 {
		t.Fatalf("profile change notes: %v", h.notes)
	}
}

func TestAJobIntervalEditWarns(t *testing.T) {
	t.Parallel()
	h := newHolderEnv(t, map[string]any{})
	h.write(map[string]any{"sweep": map[string]any{"fast": "7m"}})
	h.now = h.now.Add(config.StatEvery)
	if !h.e.cfg.Stale() {
		t.Fatal("a job interval edit raised no restart warning")
	}
}

func TestServeReadersReadNoConfigFile(t *testing.T) {
	t.Parallel()
	h := newHolderEnv(t, map[string]any{})
	for range 20 {
		h.now = h.now.Add(config.StatEvery)
		if _, err := h.e.loadConfig(); err != nil {
			t.Fatal(err)
		}
	}
	if h.loads != 0 {
		t.Fatalf("%d config reads under a holder", h.loads)
	}
}
