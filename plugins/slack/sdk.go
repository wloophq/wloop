package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	"github.com/wloophq/wloop/plugins/slack/draft"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// RestartAfter is how long the plugin waits before it streams again after
// the socket failed.
const RestartAfter = 30 * time.Second

// Plugin is the Slack source plugin as the SDK serves it.
type Plugin struct {
	Env Env
	// Slack, when set, replaces the handles Start builds over the
	// configuration: tests give a fake Slack.
	Slack func(cfg config.Config, kit *Kit) Slack
	// Logger nil is the SDK's.
	Logger hclog.Logger

	mu     sync.Mutex
	p      *plugin
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var (
	_ src.Source    = (*Plugin)(nil)
	_ src.Ingester  = (*Plugin)(nil)
	_ src.Presenter = (*Plugin)(nil)
	_ src.Publisher = (*Plugin)(nil)
	_ src.Lander    = (*Plugin)(nil)
	_ src.Briefer   = (*Plugin)(nil)
	_ src.Planner   = (*Plugin)(nil)
	_ src.Tooler    = (*Plugin)(nil)
	_ src.CLIRunner = (*Plugin)(nil)
)

func (s *Plugin) logger() hclog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return sdk.Logger()
}

func (s *Plugin) logf(format string, args ...any) { s.logger().Info(fmt.Sprintf(format, args...)) }

// brand is the host's name, which the host passes in the environment.
func brand() string {
	if b := os.Getenv(sdk.BrandEnv); b != "" {
		return b
	}
	return "loop"
}

var errNotStarted = errors.New("slack: the plugin has not started")

func (s *Plugin) running() (*plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return nil, errNotStarted
	}
	return s.p, nil
}

// Start opens the plugin's store in its state directory, importing what
// the host exported once, and starts the socket stream and the reports.
func (s *Plugin) Start(ctx context.Context, req src.StartRequest) error {
	raw := src.Config{}
	if req.Host.Services != nil {
		c, err := req.Host.Services.Config(ctx)
		if err != nil && !errors.Is(err, src.ErrUnsupported) {
			return err
		}
		raw = c
	}
	cfg, err := config.Parse(raw, brand())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(req.StateDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(ctx, req.StateDir)
	if err != nil {
		return err
	}
	b := host.Broker{Host: req.Host}
	meter := func(sm src.Sample) {
		if req.Host.Services != nil {
			_ = req.Host.Services.Meter(context.Background(), sm)
		}
	}
	kit := s.Env.Kit(cfg, s.logf, meter)
	sl := Slack{Tokens: kit.Store, Client: kit.Client, Now: s.Env.Now}
	if s.Slack != nil {
		sl = s.Slack(cfg, kit)
	}
	p := newPlugin(cfg, b, db, sl, s.logf)
	life, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.p, s.cancel = p, cancel
	s.mu.Unlock()
	s.wg.Go(func() { p.reports.run(life, RestartAfter) })
	s.wg.Go(func() {
		for {
			if err := p.run(life); err != nil && life.Err() == nil {
				p.logf("slack stream: %v; again in %s", err, RestartAfter)
			}
			if !sleep(life, RestartAfter) {
				return
			}
		}
	})
	return nil
}

// Stop ends the stream and the reports and closes the store.
func (s *Plugin) Stop(context.Context) error {
	s.mu.Lock()
	p, cancel := s.p, s.cancel
	s.p, s.cancel = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	s.wg.Wait()
	return p.db.Close()
}

// Health is loading until every unit was reported once.
func (s *Plugin) Health(ctx context.Context) (sdk.Health, error) {
	p, err := s.running()
	if err != nil {
		return sdk.Health{Status: sdk.StatusLoading, Reason: "starting"}, nil
	}
	if !p.reports.loaded() {
		return sdk.Health{Status: sdk.StatusLoading, Reason: "reporting the Slack units"}, nil
	}
	h, err := p.health(ctx)
	if err != nil {
		return sdk.Health{}, err
	}
	if len(h.Warnings) > 0 {
		return sdk.Health{Status: sdk.StatusDegraded, Reason: h.Warnings[0]}, nil
	}
	return sdk.Health{Status: sdk.StatusServing}, nil
}

// Notify reports units again: their tasks, gates or turns moved.
func (s *Plugin) Notify(_ context.Context, units []sdk.Unit, _ []sdk.Topic) error {
	p, err := s.running()
	if err != nil {
		return nil
	}
	if len(units) == 0 {
		p.reports.markAll()
		return nil
	}
	for _, w := range units {
		p.reports.mark(unit.Of(w))
	}
	return nil
}

// Ingest runs a job of the manifest.
func (s *Plugin) Ingest(ctx context.Context, req src.IngestRequest) (src.IngestResult, error) {
	p, err := s.running()
	if err != nil {
		return src.IngestResult{}, err
	}
	var summary, skipped string
	switch req.Job {
	case JobIngest:
		summary, skipped, err = p.ingestJob(ctx)
	case JobExtract:
		summary, skipped, err = p.job.Run(ctx)
	default:
		return src.IngestResult{}, fmt.Errorf("slack: no job %q", req.Job)
	}
	return src.IngestResult{Summary: summary, Skipped: skipped}, err
}

func (s *Plugin) Track(ctx context.Context, req src.TrackRequest) (src.Tracked, error) {
	p, err := s.running()
	if err != nil {
		return src.Tracked{}, err
	}
	if err := p.track(ctx, unit.Of(req.Unit), req.Follow); err != nil {
		if errors.Is(err, errNotFound) {
			return src.Tracked{}, fmt.Errorf("%w: %v", src.ErrNotFound, err)
		}
		return src.Tracked{}, err
	}
	return src.Tracked{Unit: req.Unit}, nil
}

func (s *Plugin) Untrack(ctx context.Context, w sdk.Unit) error {
	p, err := s.running()
	if err != nil {
		return err
	}
	err = p.untrack(ctx, unit.Of(w))
	if errors.Is(err, errNotFound) {
		return fmt.Errorf("%w: %v", src.ErrNotFound, err)
	}
	return err
}

func (s *Plugin) Resolve(ctx context.Context, address string) (sdk.Unit, bool, error) {
	p, err := s.running()
	if err != nil {
		return sdk.Unit{}, false, err
	}
	u, ok, err := p.resolve(ctx, address)
	return u.Wire(), ok, err
}

// Locate finds no unit in a checkout: Slack units have none.
func (s *Plugin) Locate(context.Context, string) (sdk.Unit, bool, error) {
	return sdk.Unit{}, false, nil
}

func (s *Plugin) View(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
	p, err := s.running()
	if err != nil {
		return nil, err
	}
	if req.View != ViewHealth {
		return nil, fmt.Errorf("%w: no view %q", src.ErrNotFound, req.View)
	}
	h, err := p.health(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(h)
}

func (s *Plugin) Identity(ctx context.Context) (src.Identity, error) {
	p, err := s.running()
	if err != nil {
		return src.Identity{}, err
	}
	return p.Identity(ctx)
}

func (s *Plugin) publisher() (*Publisher, error) {
	p, err := s.running()
	if err != nil {
		return nil, err
	}
	return p.publisher()
}

func (s *Plugin) Validate(ctx context.Context, d src.Draft) error {
	pub, err := s.publisher()
	if err != nil {
		return err
	}
	return pub.Validate(ctx, DraftOf(d))
}

func (s *Plugin) Where(ctx context.Context, d src.Draft) (string, error) {
	pub, err := s.publisher()
	if err != nil {
		return "", err
	}
	return pub.Where(ctx, DraftOf(d))
}

func (s *Plugin) Place(ctx context.Context, d src.Draft) (string, error) {
	pub, err := s.publisher()
	if err != nil {
		return "", err
	}
	return pub.Place(ctx, DraftOf(d))
}

func (s *Plugin) Publish(ctx context.Context, d src.Draft) (src.Evidence, error) {
	pub, err := s.publisher()
	if err != nil {
		return src.Evidence{}, err
	}
	ev, err := pub.Publish(ctx, DraftOf(d))
	return src.Evidence(ev), err
}

func (s *Plugin) Verify(ctx context.Context, d src.Draft, startedAt time.Time) (src.Evidence, bool, error) {
	pub, err := s.publisher()
	if err != nil {
		return src.Evidence{}, false, err
	}
	ev, ok, err := pub.Verify(ctx, DraftOf(d), startedAt)
	return src.Evidence(ev), ok, err
}

func (s *Plugin) Landed(ctx context.Context, d src.Draft) (sdk.Unit, bool, error) {
	pub, err := s.publisher()
	if err != nil {
		return sdk.Unit{}, false, err
	}
	u, ok, err := pub.Landed(ctx, DraftOf(d))
	return u.Wire(), ok, err
}

// Checkout is none: a Slack unit's work checks out no repository.
func (s *Plugin) Checkout(context.Context, sdk.Unit) (src.Checkout, bool, error) {
	return src.Checkout{}, false, nil
}

func (s *Plugin) WriteState(context.Context, src.StateWrite) (src.Evidence, error) {
	return src.Evidence{}, src.ErrUnsupported
}

func (s *Plugin) BriefInput(ctx context.Context, u sdk.Unit) (src.BriefInput, error) {
	p, err := s.running()
	if err != nil {
		return src.BriefInput{}, err
	}
	return p.BriefInput(ctx, u)
}

func (s *Plugin) BriefRevision(ctx context.Context, u sdk.Unit) (src.BriefRevision, error) {
	p, err := s.running()
	if err != nil {
		return src.BriefRevision{}, err
	}
	return p.BriefRevision(ctx, u)
}

func (s *Plugin) BriefDescribe(ctx context.Context, in src.BriefInput) (string, error) {
	return (&plugin{}).BriefDescribe(ctx, in)
}

func (s *Plugin) BriefStale(ctx context.Context, req src.BriefStaleRequest) (bool, error) {
	return (&plugin{}).BriefStale(ctx, req)
}

// BriefWant asks the extraction to read u now, past its quiet time: true
// when u has changes left to read, so a brief of u is coming through
// Recorder.PutBrief.
func (s *Plugin) BriefWant(_ context.Context, u sdk.Unit) (bool, error) {
	p, err := s.running()
	if err != nil {
		return false, err
	}
	if p.job == nil {
		return false, nil
	}
	return p.job.Want(unit.Of(u)), nil
}

// Plan plans no round: Slack units have no automatic rounds.
func (s *Plugin) Plan(context.Context, sdk.Unit, src.RoundState) (src.Plan, error) {
	return src.Plan{}, nil
}

func (s *Plugin) Payload(ctx context.Context, req src.PayloadRequest) (src.PayloadResult, error) {
	p, err := s.running()
	if err != nil {
		return src.PayloadResult{}, err
	}
	return Payloads{Facts: p.src.store, MaxChars: p.cfg.Turns.MaxChars}.Payload(ctx, req)
}

// EffectUnitClosed is the draft effect that marks a unit's closure, which
// no extraction pass clears.
const EffectUnitClosed = "unit.closed"

func (s *Plugin) ApplyEffect(ctx context.Context, req src.EffectRequest) error {
	p, err := s.running()
	if err != nil {
		return err
	}
	if req.Effect != EffectUnitClosed || req.Draft == nil {
		return fmt.Errorf("%w: effect %s", src.ErrUnsupported, req.Effect)
	}
	at := req.Draft.Evidence.At
	if at.IsZero() {
		at = p.now()
	}
	return p.db.Extraction().MarkClosed(ctx, unit.Of(req.Unit), at)
}

// Proposals are none: the plugin's drafts come from the profile's workflows.
func (s *Plugin) Proposals(context.Context, time.Time) ([]src.Draft, error) { return nil, nil }

func (s *Plugin) Placement(ctx context.Context, u sdk.Unit, item string) (src.Placements, error) {
	p, err := s.running()
	if err != nil {
		return src.Placements{}, err
	}
	return p.places().Placement(ctx, unit.Of(u), item)
}

// DraftOf is the plugin's draft of what the host carries.
func DraftOf(d src.Draft) draft.Draft {
	return draft.Draft{ID: draft.ID(d.ID), Unit: unit.Of(d.Unit), Items: d.Items, Task: d.Task, Kind: draft.Kind(d.Kind), Purpose: d.Purpose,
		Delivery: draft.Delivery(d.Delivery), Effects: d.Effects, Target: d.Target, Text: d.Text, Reaction: d.Reaction, Replaced: d.Replaced,
		Origin: draft.Origin{Kind: draft.OriginKind(d.Origin.Kind), TurnSeq: d.Origin.TurnSeq, Intent: d.Origin.Intent,
			Workflow: d.Origin.Workflow, Rule: d.Origin.Rule, Key: d.Origin.Key, Unit: unit.Of(d.Origin.Unit)},
		State: draft.State(d.State), Evidence: draft.Evidence(d.Evidence)}
}

// WireDraft is d as the host carries it.
func WireDraft(d draft.Draft) src.Draft {
	out := src.Draft{ID: int64(d.ID), Unit: d.Unit.Wire(), Items: d.Items, Task: d.Task, Kind: string(d.Kind), Purpose: d.Purpose,
		Delivery: string(d.Delivery), Effects: d.Effects, Target: d.Target, Text: d.Text, Reaction: d.Reaction, Replaced: d.Replaced,
		Origin: src.DraftOrigin{Kind: string(d.Origin.Kind), TurnSeq: d.Origin.TurnSeq, Intent: d.Origin.Intent, Workflow: d.Origin.Workflow,
			Rule: d.Origin.Rule, Key: d.Origin.Key},
		State: string(d.State), Evidence: src.Evidence(d.Evidence)}
	if !d.Origin.Unit.IsZero() {
		out.Origin.Unit = d.Origin.Unit.Wire()
	}
	return out
}
