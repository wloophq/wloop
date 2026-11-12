package source_test

import (
	"context"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// heads is a plugin whose basis is each unit's head as it stands.
type heads struct {
	*sourcetest.Stub
	mu    sync.Mutex
	head  string
	asked []string
}

func (h *heads) Basis(_ context.Context, u plugin.Unit, kind string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, u.Key+" "+kind)
	return h.head, nil
}

func TestBasesReadThePluginLive(t *testing.T) {
	k := kindOver(t, &change.Log{})
	h := &heads{Stub: sourcetest.NewStub(), head: "a1"}
	start(t, k, "acme", "", h)
	start(t, k, "plain", "", sourcetest.NewStub())
	b := psource.Bases{Kind: k}
	ctx := context.Background()
	u := unit.Unit{Provider: "acme", Key: "pr7"}
	if got, err := b.Live(ctx, u, "review"); err != nil || got != "a1" {
		t.Fatalf("basis %q %v", got, err)
	}
	h.mu.Lock()
	h.head = "b2"
	h.mu.Unlock()
	if got, err := b.Live(ctx, u, "review"); err != nil || got != "b2" {
		t.Fatalf("after a push, basis %q %v", got, err)
	}
	if h.asked[0] != "pr7 review" || len(h.asked) != 2 {
		t.Fatalf("asked %v", h.asked)
	}
	if got, err := b.Live(ctx, unit.Unit{Provider: "plain", Key: "x"}, "reply"); err != nil || got != "" {
		t.Fatalf("a plugin with no basis: %q %v", got, err)
	}
	if _, err := b.Live(ctx, unit.Unit{Provider: "gone", Key: "x"}, "review"); err == nil {
		t.Fatal("a basis of a plugin not running")
	}
}
