package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/profile"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// hostedLate are the daemon's services a plugin spends that serve builds
// after the plugins start.
type hostedLate struct {
	outbox  atomic.Pointer[outbox.Service]
	resolve atomic.Pointer[func(context.Context, string) (unit.Unit, bool, error)]
}

func (h *hostedLate) setResolve(r func(context.Context, string) (unit.Unit, bool, error)) {
	if h != nil && r != nil {
		h.resolve.Store(&r)
	}
}

func (h *hostedLate) setOutbox(ob *outbox.Service) {
	if h != nil {
		h.outbox.Store(ob)
	}
}

// hostedFor are the host's services each plugin spends, over db, by
// plugin id.
func (e *Env) hostedFor(db *sqlite.DB, cfg config.Config, logf func(string, ...any)) func(id string) *psource.Hosted {
	items := e.itemsLedger(db, cfg, logf)
	if e.hosted == nil {
		e.hosted = &hostedLate{}
	}
	h := e.hosted
	return func(id string) *psource.Hosted { return h.of(e, id, db, items) }
}

func (h *hostedLate) of(e *Env, id string, db *sqlite.DB, items psource.ItemLedger) *psource.Hosted {
	return &psource.Hosted{
		Items:    items,
		Ask:      e.pluginAsk(id),
		Profile:  pluginProfile(id, e.currentConfig),
		Sessions: sessionBooks{sqlite.New(db)},
		Intents: func() *dintent.Registry {
			if l := e.loadedIntents(); l != nil {
				return l.Registry
			}
			return nil
		},
		Drafts: lateDrafts{h},
		Tasks:  db.Tasks(),
		Resolve: func(ctx context.Context, address string) (unit.Unit, bool, error) {
			if r := h.resolve.Load(); r != nil {
				return (*r)(ctx, address)
			}
			return unit.Unit{}, false, nil
		},
		Now: e.Now,
	}
}

// pluginAsk asks the configured agent for plugin id, in the plugin's own
// agent directory.
func (e *Env) pluginAsk(id string) func(ctx context.Context, req src.AskRequest) (src.AskResult, error) {
	return func(ctx context.Context, req src.AskRequest) (src.AskResult, error) {
		a, err := e.asker(e.currentConfig(), filepath.Join(e.StateDir, "plugins", id, "agent"))
		if err != nil {
			return src.AskResult{}, err
		}
		tier := turns.Tier(req.Tier)
		if req.Tier != "" {
			if tier, err = turns.ParseTier(req.Tier); err != nil {
				return src.AskResult{}, err
			}
		}
		ans, err := a.Ask(ctx, turns.Ask{Tier: tier, Prompt: req.Prompt, Schema: string(req.Schema), MaxCostUSD: req.MaxCostUSD})
		switch {
		case errors.Is(err, turns.ErrUnfit):
			return src.AskResult{}, fmt.Errorf("%w: %v", src.ErrUnfit, err)
		case errors.Is(err, turns.ErrOverBudget):
			return src.AskResult{}, status.Error(codes.ResourceExhausted, err.Error())
		case err != nil:
			return src.AskResult{}, err
		}
		out := src.AskResult{Text: string(ans.JSON), Model: ans.Model, CostUSD: ans.CostUSD, Duration: ans.Duration}
		if ans.Tokens != nil {
			out.Tokens = ans.Tokens.Input + ans.Tokens.Output
		}
		return out, nil
	}
}

// pluginProfile reads a path of plugin id's profile layer: the user's
// plugins/<id>/ over the plugin's own.
func pluginProfile(id string, cfg func() config.Config) func(ctx context.Context, path string) ([]byte, error) {
	return func(_ context.Context, path string) ([]byte, error) {
		var layers []profile.Layer
		for _, l := range profile.Chain(cfg()) {
			if l.Name == profile.PluginLayer(id) || l.Name == "user:"+id {
				layers = append(layers, l)
			}
		}
		r, err := profile.Resolve(layers, path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %v", src.ErrNotFound, err)
		}
		return r.Body, err
	}
}

// sessionBooks are the store's sessions and books by session id.
type sessionBooks struct{ s *sqlite.Store }

func (b sessionBooks) Session(ctx context.Context, id string) (dturn.Session, bool, error) {
	return b.s.Session(ctx, id)
}

func (b sessionBooks) BookOf(ctx context.Context, session string) (dturn.Book, bool, error) {
	return b.s.BookOf(ctx, turns.Key{Session: session})
}

// lateDrafts is the daemon's outbox once serve built it.
type lateDrafts struct{ h *hostedLate }

var errNoOutbox = errors.New("the daemon's outbox is not running yet")

func (d lateDrafts) Propose(ctx context.Context, p outbox.Proposal) (draft.Draft, error) {
	ob := d.h.outbox.Load()
	if ob == nil {
		return draft.Draft{}, errNoOutbox
	}
	return ob.Propose(ctx, p)
}

func (d lateDrafts) RecordSent(ctx context.Context, p outbox.Proposal, ev draft.Evidence) (draft.Draft, error) {
	ob := d.h.outbox.Load()
	if ob == nil {
		return draft.Draft{}, errNoOutbox
	}
	return ob.RecordSent(ctx, p, ev)
}

func (d lateDrafts) PublisherOf(dr draft.Draft) unit.ProviderID {
	if ob := d.h.outbox.Load(); ob != nil {
		return ob.PublisherOf(dr)
	}
	return dr.Unit.Provider
}
