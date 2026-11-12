package source_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
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

func (h *heads) Within(_ context.Context, u plugin.Unit, target string) (bool, error) {
	return target == "comment:1" && u.Key == "pr7", nil
}

func TestAPublisherReadsItsPluginsBasisLive(t *testing.T) {
	k := kindOver(t, &change.Log{})
	h := &heads{Stub: sourcetest.NewStub(), head: "a1"}
	start(t, k, "acme", "", h)
	start(t, k, "plain", "", sourcetest.NewStub())
	b := psource.Publisher{ID: "acme", Kind: k}
	plain, gone := psource.Publisher{ID: "plain", Kind: k}, psource.Publisher{ID: "gone", Kind: k}
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
	if got, err := plain.Live(ctx, unit.Unit{Provider: "plain", Key: "x"}, "reply"); err != nil || got != "" {
		t.Fatalf("a plugin with no basis: %q %v", got, err)
	}
	if _, err := gone.Live(ctx, unit.Unit{Provider: "gone", Key: "x"}, "review"); err == nil {
		t.Fatal("a basis of a plugin not running")
	}
	if in, err := b.Within(ctx, u, "comment:1"); err != nil || !in {
		t.Fatalf("a target the plugin places inside: %v %v", in, err)
	}
	if in, err := b.Within(ctx, u, "comment:2"); err != nil || in {
		t.Fatalf("a target the plugin places outside: %v %v", in, err)
	}
	if in, err := plain.Within(ctx, unit.Unit{Provider: "plain", Key: "x"}, "comment:1"); err != nil || in {
		t.Fatalf("a plugin with no drafter: %v %v", in, err)
	}
}

// TestAPluginWhoseKindsTakeABasisMustReadIt refuses to start a plugin whose manifest declares a kind with
// basis = "agent" while the plugin serves no Drafter: every draft of that kind would read stale.
func TestAPluginWhoseKindsTakeABasisMustReadIt(t *testing.T) {
	const kinds = "default_delivery = \"comment\"\n[[source.deliveries]]\nname = \"comment\"\nposts = true\n" +
		"[[source.draft_kinds]]\nname = \"react\"\ndelivery = \"comment\"\nschema = \"drafts/react/schema.json\"\nbasis = \"agent\"\n"
	launch := func(impl src.Source) error {
		k := kindOver(t, &change.Log{})
		m, err := manifest.Parse([]byte("id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{name}\"]}\n" + kinds))
		if err != nil {
			t.Fatal(err)
		}
		m.Files = acmeFiles
		l := plugintest.NewLauncher(t)
		l.Add("acme", map[plugin.Kind]plugin.Impl{src.Kind: src.New(impl)})
		p, err := l.Launch(t.Context(), plugin.Spec{ID: "acme", Kinds: []plugin.Kind{src.Kind}})
		if err != nil {
			t.Fatal(err)
		}
		c, err := p.Kind(src.Kind)
		if err != nil {
			t.Fatal(err)
		}
		err = k.Start(t.Context(), m, c)
		t.Cleanup(func() { k.Stop(context.Background(), "acme") })
		return err
	}
	if err := launch(sourcetest.NewStub()); err == nil || !strings.Contains(err.Error(), "react") {
		t.Fatalf("a plugin with a basis kind and no Drafter started: %v", err)
	}
	if err := launch(&heads{Stub: sourcetest.NewStub(), head: "a1"}); err != nil {
		t.Fatalf("a plugin that reads its basis: %v", err)
	}
}
