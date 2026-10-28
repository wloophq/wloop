package source_test

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/meter"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// meteringSource meters one request and its reported cost under the scope
// of the host's call every job it runs, and keeps what that call carried.
type meteringSource struct {
	*sourcetest.Stub
	host src.Host
	call src.Call
}

func (m *meteringSource) Start(ctx context.Context, req src.StartRequest) error {
	m.host = req.Host
	return m.Stub.Start(ctx, req)
}

func (m *meteringSource) Ingest(ctx context.Context, _ src.IngestRequest) (src.IngestResult, error) {
	m.call = src.CallOf(ctx)
	scope := m.call.MeterScope
	if err := m.host.Services.Meter(ctx, src.Sample{Scope: scope, Resource: "github", Op: "graphql", Duration: 40 * time.Millisecond}); err != nil {
		return src.IngestResult{}, err
	}
	return src.IngestResult{}, m.host.Services.Meter(ctx, src.Sample{Scope: scope, Resource: "github", Op: "graphql", Points: 3})
}

func (m *meteringSource) Track(context.Context, src.TrackRequest) (src.Tracked, error) {
	return src.Tracked{}, src.ErrUnsupported
}

func (m *meteringSource) Untrack(context.Context, plugin.Unit) error { return src.ErrUnsupported }

func (m *meteringSource) Resolve(context.Context, string) (plugin.Unit, bool, error) {
	return plugin.Unit{}, false, nil
}

func (m *meteringSource) Locate(context.Context, string) (plugin.Unit, bool, error) {
	return plugin.Unit{}, false, nil
}

func TestAPluginsCallsAreMeteredUnderTheHostsScope(t *testing.T) {
	t.Parallel()
	b, _ := brokerOver(t)
	reg := meter.NewRegistry(func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) })
	b.Meters = &psource.Meters{Registry: reg}
	plug := &meteringSource{Stub: sourcetest.NewStub()}
	c := sourcetest.Launch(t, "acme", plug, b.Host())

	ctx, scope := meter.Start(meter.WithRegistry(context.Background(), reg), meter.Job("ingest:acme"))
	call, done := b.Meters.Call(ctx, src.PriorityBackground)
	if _, err := c.Ingest(call, src.IngestRequest{Job: "ingest"}); err != nil {
		t.Fatal(err)
	}
	done()
	if plug.call.MeterScope != "job:ingest:acme" || plug.call.Priority != src.PriorityBackground {
		t.Fatalf("the plugin's call carried %+v", plug.call)
	}
	st := scope.End()[meter.Key{Res: meter.GitHub, Op: "graphql"}]
	if st.Calls != 1 || st.Points != 3 || st.Dur != 40*time.Millisecond {
		t.Fatalf("the host's scope holds %+v, want one call of 40ms that cost 3 points", st)
	}

	if _, err := c.Ingest(call, src.IngestRequest{Job: "ingest"}); err != nil {
		t.Fatal(err)
	}
	var total meter.Stat
	for _, tt := range reg.Totals() {
		if tt.Name == "job:ingest:acme" {
			total = tt.Tally[meter.Key{Res: meter.GitHub, Op: "graphql"}]
		}
	}
	if total.Calls != 2 || total.Points != 6 {
		t.Fatalf("the registry holds %+v, want the two calls, the second after the scope ended", total)
	}
	if err := b.Host().Services.Meter(context.Background(), src.Sample{Scope: "job:x", Resource: "sql", Op: "query"}); err == nil {
		t.Fatal("a plugin metered the host's own store")
	}
}
