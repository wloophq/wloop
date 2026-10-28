package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// slackModule is the Slack plugin's module outside an Env: what needs no
// running plugin.
func slackModule(config.Config, source.Deps) (source.Module, error) {
	m, _, err := (&Env{}).relayBase(slackPlugin)
	return m, err
}

// slackRelay is the Slack plugin's module: the daemon's jobs, rows, facts
// and capabilities relay to the plugin process serve runs.
func (e *Env) slackRelay(_ config.Config, d source.Deps) (source.Module, error) {
	m, err := e.relayModule(slackPlugin, d)
	m.Reacts = m.Publisher != nil
	return m, err
}

// relayModule is the module of plugin id, which runs as its own process:
// its first manifest job is the ingestion job and the others the daemon's
// jobs, each named <job>:<id>. Outside serve it builds only what needs no
// running plugin.
func (e *Env) relayModule(id unit.ProviderID, d source.Deps) (source.Module, error) {
	m, s, err := e.relayBase(id)
	if err != nil || e.pluginKind == nil {
		return m, err
	}
	r := relay{id: id, kind: e.pluginKind, now: e.Now}
	for _, j := range s.Jobs[1:] {
		m.Jobs = append(m.Jobs, source.Job{Name: j.Name + ":" + string(id), Every: time.Duration(j.Every), Timeout: time.Duration(j.Timeout),
			Run: func(ctx context.Context) (string, string, error) { return r.ingest(ctx, j.Name) }})
	}
	rp := reportedPlugin{id: id, src: s}
	m.Rows = func(rd readmodel.RowDeps) readmodel.Provider {
		return readmodel.NewReported(e.reportedSource(rp, d.Inventory, rd))
	}
	m.Card = readmodel.NewReported(e.reportedSource(rp, d.Inventory, readmodel.RowDeps{})).Card
	facts := psource.SettledFacts{UnitFacts: psource.UnitFacts{Snapshots: e.pluginKind.Snapshots}, Source: s}
	if d.DB != nil {
		facts.Drafts = d.DB.Drafts()
	}
	m.Facts = facts
	units := psource.Units{Codecs: e.unitCodecs()}
	r.units = units
	compiled, err := s.Codec.Compile()
	if err != nil {
		return source.Module{}, err
	}
	m.Track, m.Untrack, m.Identity, m.Health = r.track, r.untrack, r.identity, r.health
	m.Resolve = func(ctx context.Context, address string) (unit.Unit, bool, error) {
		if !compiled.Takes(address) {
			return unit.Unit{}, false, nil
		}
		return r.resolve(ctx, address)
	}
	ids := string(id)
	m.Publisher = psource.Publisher{ID: ids, Source: s, Units: units, Kind: e.pluginKind}
	m.OnPosted = map[string]outbox.Effect{builtin.EffectUnitClosed: r.effect(builtin.EffectUnitClosed)}
	m.Payload = psource.Payloads{ID: ids, Kind: e.pluginKind, Now: e.Now}
	pl := psource.Placements{ID: ids, Kind: e.pluginKind}
	m.PlacementDefaults, m.PlacementHints = pl, pl
	m.Briefs = psource.Briefs{ID: ids, Kind: e.pluginKind}
	return m, nil
}

// relayBase is plugin id's module as it needs no running plugin: its
// codec, its ingestion job and no rows; and the plugin's [source] table.
func (e *Env) relayBase(id unit.ProviderID) (source.Module, *manifest.Source, error) {
	s, err := e.pluginSource(id)
	if err != nil {
		return source.Module{}, nil, err
	}
	c, err := codec.Of(s.Provider, s.Codec)
	if err != nil {
		return source.Module{}, nil, err
	}
	if len(s.Jobs) == 0 {
		return source.Module{}, nil, fmt.Errorf("plugin %s: its manifest declares no ingestion job", id)
	}
	r := relay{id: id, kind: e.pluginKind, now: e.Now}
	m := source.Module{ID: id, Codec: c, Ingest: ingest.Job{Every: time.Duration(s.Jobs[0].Every), Prepare: r.prepare(s.Jobs[0].Name, c)},
		Rows: func(readmodel.RowDeps) readmodel.Provider { return noRows{id} }}
	return m, s, nil
}

// pluginSource is the [source] table of plugin id: the one serve runs,
// else the bundled one.
func (e *Env) pluginSource(id unit.ProviderID) (*manifest.Source, error) {
	for _, p := range e.plugins {
		if p.ID() == string(id) {
			return p.Manifest.Source()
		}
	}
	srcs, err := bundled.Sources()
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(srcs, func(s *manifest.Source) bool { return s.Provider == string(id) }); i >= 0 {
		return srcs[i], nil
	}
	return nil, fmt.Errorf("no manifest of plugin %s", id)
}

// relay calls plugin id's running process.
type relay struct {
	id    unit.ProviderID
	kind  *psource.Kind
	units psource.Units
	now   func() time.Time
}

func (r relay) client() (*src.Client, error) {
	if r.kind == nil {
		return nil, fmt.Errorf("plugin %s: %w", r.id, src.ErrUnavailable)
	}
	return r.kind.Client(string(r.id))
}

// call is ctx as a request to the plugin carries it, at priority.
func (r relay) call(ctx context.Context, priority src.Priority) (context.Context, func()) {
	if r.kind == nil {
		return src.WithCall(ctx, src.Call{Priority: priority}), func() {}
	}
	return r.kind.Meters.Call(ctx, priority)
}

func (r relay) ingest(ctx context.Context, job string) (summary, skipped string, err error) {
	c, err := r.client()
	if err != nil {
		return "", "", err
	}
	ctx, done := r.call(ctx, src.PriorityBackground)
	defer done()
	res, err := c.Ingest(ctx, src.IngestRequest{Job: job, Now: r.now()})
	return res.Summary, res.Skipped, err
}

// prepare runs the plugin's job, which reports what it read itself: the
// pass reads and saves nothing and gives the plugin's outcome. A plugin
// that is not running skips the pass.
func (r relay) prepare(job string, c unit.Codec) func(ctx context.Context, now time.Time) (ingest.Plan, string, error) {
	return func(ctx context.Context, _ time.Time) (ingest.Plan, string, error) {
		summary, skipped, err := r.ingest(ctx, job)
		if errors.Is(err, src.ErrUnavailable) {
			return ingest.Plan{}, err.Error(), nil
		}
		if err != nil {
			return ingest.Plan{}, "", err
		}
		reg := ingest.Registration{Source: reported{r.id}, Store: reported{r.id}, Codec: c}
		return ingest.Plan{Registration: reg, After: func(_ context.Context, _ ingest.Report, err error) (string, string, error) {
			return summary, skipped, err
		}}, "", nil
	}
}

func (r relay) track(ctx context.Context, u unit.Unit) (port.TrackRead, error) {
	c, err := r.client()
	if err != nil {
		return port.TrackRead{}, err
	}
	ctx, done := r.call(ctx, src.PriorityUser)
	defer done()
	t, err := c.Track(ctx, src.TrackRequest{Unit: psource.Wire(u)})
	if errors.Is(err, src.ErrNotFound) {
		return port.TrackRead{}, fmt.Errorf("%w: %v", port.ErrNotFound, err)
	}
	if err != nil {
		return port.TrackRead{}, err
	}
	got, err := r.units.Of(t.Unit)
	if err != nil {
		return port.TrackRead{}, err
	}
	return port.TrackRead{Unit: got, State: t.State, Save: func(ctx context.Context) error {
		ctx, done := r.call(ctx, src.PriorityUser)
		defer done()
		_, err := c.Track(ctx, src.TrackRequest{Unit: t.Unit, Follow: true})
		return err
	}}, nil
}

func (r relay) untrack(ctx context.Context, u unit.Unit) error {
	c, err := r.client()
	if err != nil {
		return err
	}
	ctx, done := r.call(ctx, src.PriorityUser)
	defer done()
	return c.Untrack(ctx, psource.Wire(u))
}

func (r relay) resolve(ctx context.Context, address string) (unit.Unit, bool, error) {
	c, err := r.client()
	if err != nil {
		return unit.Unit{}, false, err
	}
	w, ok, err := c.Resolve(ctx, address)
	if err != nil || !ok {
		return unit.Unit{}, false, err
	}
	u, err := r.units.Of(w)
	return u, err == nil, err
}

func (r relay) identity(ctx context.Context) (items.Person, error) {
	c, err := r.client()
	if err != nil {
		return items.Person{}, err
	}
	id, err := c.Identity(ctx)
	if err != nil {
		return items.Person{}, err
	}
	return items.Person{Key: id.Viewer.Key, Name: id.Viewer.Name, Aliases: id.Viewer.Aliases}, nil
}

// health is the plugin's health view; a plugin that is not running warns
// so.
func (r relay) health(ctx context.Context) (api.SourceHealth, error) {
	out := api.SourceHealth{Provider: r.id}
	c, err := r.client()
	if err == nil {
		var raw json.RawMessage
		if raw, err = c.View(ctx, src.ViewRequest{View: "health"}); err == nil {
			err = json.Unmarshal(raw, &out)
			out.Provider = r.id
		}
	}
	if errors.Is(err, src.ErrUnavailable) {
		out.Warnings = append(out.Warnings, err.Error())
		return out, nil
	}
	return out, err
}

func (r relay) effect(name string) outbox.Effect {
	return func(ctx context.Context, d draft.Draft) error {
		c, err := r.client()
		if err != nil {
			return err
		}
		wd := psource.WireDraft(d)
		return c.ApplyEffect(ctx, src.EffectRequest{Effect: name, Unit: wd.Unit, Draft: &wd})
	}
}

// reported is the source and store of a plugin that reports its units
// itself: an ingestion pass over it reads and saves nothing.
type reported struct{ id unit.ProviderID }

func (r reported) Provider() unit.ProviderID { return r.id }

func (reported) Discover(context.Context, string) ([]port.Stub, string, error) { return nil, "", nil }

func (reported) Stubs(context.Context, []string) ([]port.Stub, error) { return nil, nil }

func (reported) Read(context.Context, []string) (port.Facts, error) { return nil, nil }

func (reported) Viewer(context.Context) (string, error) { return "", nil }

func (reported) Watched(context.Context) ([]port.Stub, error) { return nil, nil }

func (reported) Versions(context.Context, []string) (map[string]string, error) { return nil, nil }

func (reported) Cursor(context.Context) (string, error) { return "", nil }

func (reported) Save(context.Context, []port.Facts, port.Facts, string, time.Time) error { return nil }

func (reported) See(context.Context, []string, time.Time) error { return nil }

// noRows serves no rows: outside serve, no plugin reports any.
type noRows struct{ id unit.ProviderID }

func (n noRows) Provider() unit.ProviderID { return n.id }

func (noRows) Listed(context.Context) ([]unit.Unit, error) { return nil, nil }

func (noRows) Load(context.Context, []unit.Unit) (map[unit.Unit]any, error) { return nil, nil }

func (n noRows) Derive(readmodel.Base, any, time.Time) (api.Row, error) {
	return api.Row{}, fmt.Errorf("plugin %s serves no rows outside serve", n.id)
}
