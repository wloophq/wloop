package daemon

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestPrepareTurnFillsWhatTheActionsPrepareFills(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	got, err := s.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	situation, _ := got["situation"].(string)
	if who, _ := got["who"].([]string); !slices.Equal(who, []string{"@ana"}) || !strings.HasPrefix(situation, "Waiting on: @ana since ") {
		t.Fatalf("inputs %#v", got)
	}
}

func TestPrepareTurnRefusalIsUnfit(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	seedWaitingOnAna(t, s)
	sentNudge(t, s, "t1", "@ana")
	_, err := s.PrepareTurn(context.Background(), string(dintent.Nudge), r9.Unit(), "", nil)
	if !errors.Is(err, src.ErrUnfit) || !strings.Contains(err.Error(), "already nudged") {
		t.Fatalf("a second nudge of the same wait: %v", err)
	}
}

func TestPrepareTurnOfAnIntentWithoutOneKeepsItsInputs(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	in := map[string]any{"self_review": true}
	got, err := s.PrepareTurn(context.Background(), string(dintent.Address), r7.Unit(), "", in)
	if err != nil || !maps.Equal(got, in) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestAHeadlessActionRunsItsIntentsPrepare(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m, err := os.ReadFile(filepath.Join("..", "..", "plugins", "github", "profile", "intents", "nudge", "intent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "intents", "nudge"), 0o755); err != nil {
		t.Fatal(err)
	}
	headlessNudge := strings.Replace(string(m), "\nworkspace ", "\nrunners = [\"headless\", \"session\"]\nworkspace ", 1)
	if err := os.WriteFile(filepath.Join(dir, "intents", "nudge", "intent.toml"), []byte(headlessNudge), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := cacheOver(t, dir)
	s, h := newActServer(t, nil)
	s.deps.Intents = cache.Get
	if broken := cache.Get().Broken; len(broken) > 0 {
		t.Fatalf("broken %v", broken)
	}
	seedWaitingOnAna(t, s)
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{"source":"cli"}`); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(h.got) != 1 {
		t.Fatalf("started %+v", h.got)
	}
	if who, _ := h.got[0].Inputs["who"].([]string); !slices.Equal(who, []string{"@ana"}) {
		t.Fatalf("the headless nudge's inputs %#v", h.got[0].Inputs)
	}
	sentNudge(t, s, "t1", "@ana")
	if rec := post(t, s, "/api/units/github/acme%2Fapi%239/nudge", `{"source":"cli"}`); rec.Code/100 == 2 || len(h.got) != 1 {
		t.Fatalf("a refused prepare still started: %d %s, %d started", rec.Code, rec.Body, len(h.got))
	}
}
