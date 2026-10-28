package plugin_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/plugintest"
)

type note struct {
	units  []plugin.Unit
	topics []string
}

type recordingKind struct {
	reason string
	mu     sync.Mutex
	starts []plugin.StartRequest
	notes  []note
	stops  int
}

func (k *recordingKind) Health(context.Context) (plugin.Health, error) {
	return plugin.Health{Status: plugin.StatusLoading, Reason: k.reason}, nil
}

func (k *recordingKind) Start(_ context.Context, req plugin.StartRequest) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.starts = append(k.starts, req)
	return nil
}

func (k *recordingKind) Stop(context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stops++
	return nil
}

func (k *recordingKind) Notify(_ context.Context, units []plugin.Unit, topics []string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.notes = append(k.notes, note{units, topics})
	return nil
}

type record struct {
	starts []plugin.StartRequest
	notes  []note
	stops  int
}

func (k *recordingKind) record() record {
	k.mu.Lock()
	defer k.mu.Unlock()
	return record{slices.Clone(k.starts), slices.Clone(k.notes), k.stops}
}

func (*recordingKind) Register(*grpc.Server, *goplugin.GRPCBroker) error { return nil }

func TestAPluginServesSeveralKinds(t *testing.T) {
	alpha, beta := &recordingKind{reason: "importing alpha"}, &recordingKind{reason: "importing beta"}
	l := plugintest.NewLauncher(t)
	l.Add("acme", map[plugin.Kind]plugin.Impl{"alpha": alpha, "beta": beta})
	p, err := l.Launch(t.Context(), plugin.Spec{ID: "acme", Kinds: []plugin.Kind{"alpha", "beta"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol() != plugin.Protocol {
		t.Errorf("protocol %d, want %d", p.Protocol(), plugin.Protocol)
	}
	ctx := t.Context()
	a, err := p.Kind("alpha")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Kind("beta")
	if err != nil {
		t.Fatal(err)
	}
	for c, want := range map[*plugin.Client]string{a: "importing alpha", b: "importing beta"} {
		h, err := c.Health(ctx)
		if err != nil || h != (plugin.Health{Status: plugin.StatusLoading, Reason: want}) {
			t.Errorf("%s Health = %+v, %v", c.Kind(), h, err)
		}
	}
	if err := a.Start(ctx, plugin.StartRequest{StateDir: "/state/plugins/acme", HostBroker: 7}); err != nil {
		t.Fatal(err)
	}
	units := []plugin.Unit{{Provider: "acme", Key: "acme/api#1"}}
	if err := b.Notify(ctx, units, []string{"rounds"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	ra, rb := alpha.record(), beta.record()
	if !slices.Equal(ra.starts, []plugin.StartRequest{{StateDir: "/state/plugins/acme", HostBroker: 7}}) || len(rb.starts) != 0 {
		t.Errorf("starts: alpha %v, beta %v", ra.starts, rb.starts)
	}
	if len(ra.notes) != 0 || len(rb.notes) != 1 || !slices.Equal(rb.notes[0].units, units) || !slices.Equal(rb.notes[0].topics, []string{"rounds"}) {
		t.Errorf("notes: alpha %v, beta %v", ra.notes, rb.notes)
	}
	if ra.stops != 0 || rb.stops != 1 {
		t.Errorf("stops: alpha %d, beta %d", ra.stops, rb.stops)
	}

	if _, err := p.Kind("gamma"); err == nil {
		t.Error("a kind the spec does not name was dispensed")
	}
	l.Add("half", map[plugin.Kind]plugin.Impl{"alpha": alpha})
	h, err := l.Launch(ctx, plugin.Spec{ID: "half", Kinds: []plugin.Kind{"alpha", "beta"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.Kind("beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Health(ctx); err == nil {
		t.Error("a kind the plugin does not serve answered Health")
	}
}
