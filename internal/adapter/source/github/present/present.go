// Package present is the GitHub plugin's source logic the daemon reaches
// through Action and View: the user's mutes, the rows
// of pull requests the read model does not list, the detail, the checks,
// Explore and the timings; and its bookkeeping on each snapshot of the
// rows.
package present

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/intake"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// The views the plugin serves besides the daemon's unit views.
const (
	ViewExplore       = "explore"
	ViewExploreRecent = "explore.recent"
)

// The actions the plugin runs.
const (
	ActionMute         = "mute"
	ActionUnmute       = "unmute"
	ActionNudgePrepare = "nudge.prepare"
)

// Briefs are the brief queue's views of pull requests.
type Briefs interface {
	Views(ctx context.Context, prs ...dinv.PR) (map[pr.Ref]*brief.View, error)
	RefreshIfStale(ctx context.Context, p dinv.PR) error
}

// Deps are what the presenter reads and writes: the plugin's store, the
// broker the host serves it, and GitHub.
type Deps struct {
	Store *inventory.Store
	// Reports are what the GitHub plugin last reported of its pull
	// requests, which a row derives from.
	Reports readmodel.Reports
	// Host is the broker: the host's overrides and round state.
	Host src.Host
	// Briefs nil shows no brief.
	Briefs Briefs
	// Plugin runs an action of the running GitHub plugin, which serves the
	// nudge's prepare; nil serves none.
	Plugin func(ctx context.Context, req src.ActionRequest) (json.RawMessage, error)
	// View reads a view of the running GitHub plugin, which searches for
	// Explore and reads the failed steps of the checks; nil serves none.
	View func(ctx context.Context, req src.ViewRequest) (json.RawMessage, error)
}

// Host is what the daemon serves the presenter beside the broker: the
// rows as its read model derives them.
type Host struct {
	Model    *readmodel.Model
	Settings func() readmodel.RowSettings
	BriefOn  bool
	Intakes  func(ctx context.Context) (map[unit.Unit][]intake.Intake, error)
	Books    func(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
	Viewer   func(ctx context.Context) (src.Identity, error)
	Units    *unit.Registry
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

// Presenter is the GitHub plugin's Action, View and bookkeeping.
type Presenter struct {
	d Deps
	h Host
	// overrides serializes the presenter's override writes, which also
	// compare and set.
	overrides sync.Mutex
	// situationsFailing keeps the first failure of a streak logged only.
	situationsFailing atomic.Bool
}

// New is the presenter over d, served by the host h.
func New(d Deps, h Host) *Presenter {
	if h.Logf == nil {
		h.Logf = func(string, ...any) {}
	}
	if h.Now == nil {
		h.Now = time.Now
	}
	return &Presenter{d: d, h: h}
}

func unitOf(u plugin.Unit) unit.Unit {
	return unit.Unit{Provider: unit.ProviderID(u.Provider), Key: u.Key}
}

func wire(u unit.Unit) plugin.Unit { return plugin.Unit{Provider: string(u.Provider), Key: u.Key} }

// Action runs one of the plugin's actions on a pull request.
func (p *Presenter) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	var args api.PluginActionArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return nil, fmt.Errorf("%w: args: %v", src.ErrUnfit, err)
		}
	}
	u := unitOf(req.Unit)
	var res api.PluginActionResult
	var err error
	switch req.Action {
	case ActionMute:
		err = p.mute(ctx, u, args)
	case ActionUnmute:
		err = p.unmute(ctx, u)
	case ActionNudgePrepare:
		if p.d.Plugin == nil {
			return nil, fmt.Errorf("%w: %s is the GitHub plugin's", src.ErrUnavailable, req.Action)
		}
		return p.d.Plugin(ctx, req)
	default:
		return nil, fmt.Errorf("%w: no action %s", src.ErrUnsupported, req.Action)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

// View serves one of the plugin's views.
func (p *Presenter) View(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
	switch req.View {
	case api.ViewUnitRow:
		var q api.UnitRowQuery
		if err := decode(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(p.unitRow(ctx, q))
	case api.ViewUnitDetail:
		var q api.UnitQuery
		if err := decode(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(p.detail(ctx, q.Unit))
	case api.ViewUnitChecks:
		var q api.UnitQuery
		if err := decode(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(p.checks(ctx, q.Unit))
	case api.ViewTimings:
		var q api.TimingsQuery
		if err := decode(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(p.timings(ctx, q.Days))
	case ViewExplore:
		var q api.ExploreRequest
		if err := decode(req.Query, &q); err != nil {
			return nil, err
		}
		return marshal(p.explore(ctx, q))
	case ViewExploreRecent:
		return marshal(p.recentSearches(ctx))
	}
	return nil, fmt.Errorf("%w: no view %s", src.ErrUnsupported, req.View)
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return api.Refuse(http.StatusBadRequest, "invalid query: %v", err)
	}
	return nil
}

func marshal[T any](v T, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// Keep is the plugin's bookkeeping on each new snapshot of the rows: the
// situations that changed and the mutes whose situation changed.
func (p *Presenter) Keep(ctx context.Context, k readmodel.Kept) error {
	p.recordSituations(ctx, k)
	return p.expireLapsedMutes(ctx, k)
}

func (p *Presenter) recordEvent(ctx context.Context, u unit.Unit, kind, data string) error {
	ref, _ := pr.RefOf(u)
	return p.d.Store.RecordEvent(ctx, dinv.Event{PR: ref, Kind: kind, At: p.h.Now(), Data: data})
}

func (p *Presenter) same(a, b unit.Unit) bool {
	if p.h.Units == nil {
		return a == b
	}
	return p.h.Units.Same(a, b)
}
