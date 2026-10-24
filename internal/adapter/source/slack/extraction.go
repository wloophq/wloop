package slack

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

// extraction is the extraction job over the plugin's store and segment map, its breaker
// reported through Metrics; none without a store.
func (p *plugin) extraction(cfg config.Config) []sourcereg.Job {
	if p.src == nil {
		return nil
	}
	writer, _ := p.src.segments.(dslack.SegmentWriter)
	db := p.src.db
	j := &extract.Job{
		Facts: extractionFacts{p.src.store}, Segments: p.src.segments, Writer: writer, Watcher: keepFollows{p.watches, db},
		Parse: p.parseTarget,
		DM: func(ctx context.Context, conv, from string) ([]dslack.Message, error) {
			rows, err := db.Messages(ctx, conv, sqlite.MessageQuery{From: from})
			out := make([]dslack.Message, len(rows))
			for i, r := range rows {
				out[i] = message(r)
			}
			return out, err
		},
		DMBefore: p.src.store.MessagesBefore,
		Store:    extract.Store{DB: p.deps.DB.Extraction(), Segments: p.deps.DB.Segments()},
		Clock:    p.deps.Clock,
		Cfg:      func() config.Config { return cfg },
		Log:      p.logf,
	}
	if t := p.deps.Turns; t != nil && t.Items != nil {
		j.Ledger = t.Items
	}
	if a, ok := p.deps.Agents[cfg.Agent].(turns.AskingAgent); ok {
		if asker, err := a.Asker(filepath.Join(config.StateDir(), "extract"), cfg.AgentModels[cfg.Agent]); err == nil {
			j.Asker = asker
		} else {
			p.logf("slack extraction: %v", err)
		}
	}
	p.slack.Metrics.Warn("extraction", j.Warnings)
	return []sourcereg.Job{{Name: extract.JobName, Every: extract.JobEvery, Timeout: extract.JobTimeout, Run: j.Run}}
}

// parseTarget is the unit an extracted link names: a pull request URL, or
// "<provider>:<key>" of a registered codec.
func (p *plugin) parseTarget(target string) (unit.Unit, bool) {
	if u, ok := pullUnit(target); ok {
		return u, true
	}
	provider, key, ok := strings.Cut(target, ":")
	if !ok || p.deps.Units == nil {
		return unit.Unit{}, false
	}
	reg := p.deps.Units()
	if reg == nil {
		return unit.Unit{}, false
	}
	u, err := reg.Parse(provider, key)
	return u, err == nil
}

// extractionFacts tells the job a conversation the store lacks from a failed read.
type extractionFacts struct{ *Store }

func (f extractionFacts) Conversation(ctx context.Context, id string) (dslack.Conversation, error) {
	rows, err := f.DB.Conversations(ctx, []string{id})
	if err != nil {
		return dslack.Conversation{}, err
	}
	if len(rows) == 0 {
		return dslack.Conversation{}, fmt.Errorf("%w: conversation %s", dslack.ErrUnknownUnit, id)
	}
	return f.Store.Conversation(ctx, id)
}

// keepFollows is the job's Watcher: its Unwatch leaves an object the user follows or a
// turn watches.
type keepFollows struct {
	Watches
	db *sqlite.SlackStore
}

func (w keepFollows) Unwatch(ctx context.Context, obj dslack.ObjectRef) error {
	o, ok, err := w.db.Object(ctx, obj.Object())
	if err != nil {
		return err
	}
	if ok && slices.ContainsFunc(o.Reasons, func(r string) bool {
		return r == string(dslack.ReasonUserFollow) || r == string(dslack.ReasonTurn)
	}) {
		return nil
	}
	return w.Watches.Unwatch(ctx, obj)
}
