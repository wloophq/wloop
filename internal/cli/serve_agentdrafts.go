package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// serveAgentDrafts serves an agent session's calls on drafts over d's outbox: the agent route's and the
// MCP draft tools'.
func (e *Env) serveAgentDrafts(d daemon.Deps, db *sqlite.DB, cfg config.Config, logf func(string, ...any)) daemon.Deps {
	if d.Outbox == nil {
		return d
	}
	books := sqlite.New(db)
	d.AgentDrafts = &daemon.AgentDrafts{
		Outbox: d.Outbox,
		Unit: func(ctx context.Context, session string) (unit.Unit, bool, error) {
			b, ok, err := books.BookOf(ctx, turns.Key{Session: session})
			return b.Unit, ok && err == nil, err
		},
		Proposer: e.agentProposer(e.hostedFor(db, cfg, logf)),
	}
	return d
}

// agentProposer proposes through the host path of u's plugin, as the plugin's draft tool does: the host sets
// the draft's origin, task and items from the session's turn.
func (e *Env) agentProposer(hosted func(id string) *psource.Hosted) daemon.AgentProposer {
	return func(ctx context.Context, session string, u unit.Unit, p api.AgentDraftProposal) (draft.ID, error) {
		id := string(u.Provider)
		var b *psource.Broker
		for _, pl := range e.plugins {
			if pl.ID() != id {
				continue
			}
			s, err := pl.Manifest.Source()
			if err != nil {
				return 0, err
			}
			b = &psource.Broker{ID: id, Manifest: s, Units: psource.Units{Codecs: e.unitCodecs()}, Config: e.currentConfig, Hosted: hosted(id)}
		}
		if b == nil {
			return 0, fmt.Errorf("%w: no plugin %s drafts on %s", errNoDraftPlugin, id, u.Label())
		}
		ref, err := b.Host().Gates.OpenGate(ctx, src.GateRequest{Session: session, Draft: src.Draft{
			Unit: plugin.Unit{Provider: id, Key: u.Key}, Kind: p.Kind, Target: p.Target, Text: p.Text, Payload: p.Payload, Basis: p.Basis,
			Hold: p.Hold, Items: p.Items, Effects: p.Effects,
		}})
		return draft.ID(ref.Draft), err
	}
}

var errNoDraftPlugin = errors.New("no plugin proposes this draft")
