package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/send"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// briefVersion is the version of the briefs the extraction writes.
const briefVersion = "extraction"

// extraction is the extraction job over the plugin's store and segment
// map: it asks the host's agent, applies its ops to the host's ledger and
// keeps its briefs in the host. Its breaker is reported through Metrics.
func (p *plugin) extraction() *extract.Job {
	writer, _ := p.src.segments.(dslack.SegmentWriter)
	db := p.src.db
	j := &extract.Job{
		Facts: extractionFacts{p.src.store}, Segments: p.src.segments, Writer: writer, Watcher: keepFollows{p.watches, db},
		Parse: parseTarget,
		DM: func(ctx context.Context, conv, from string) ([]dslack.Message, error) {
			rows, err := db.Messages(ctx, conv, store.MessageQuery{From: from})
			out := make([]dslack.Message, len(rows))
			for i, r := range rows {
				out[i] = message(r)
			}
			return out, err
		},
		DMBefore: p.src.store.MessagesBefore,
		Store:    extract.Store{DB: p.db.Extraction(), Segments: p.db.Segments()},
		Clock:    clockFunc(p.now),
		Cfg:      func() config.Config { return p.all },
		Log:      p.logf,
		Prompts:  func(ctx context.Context) (*extract.Prompts, error) { return extract.LoadPrompts(ctx, p.host.Profile) },
		PutBrief: p.putBrief,
		Extra:    p.briefExtra,
	}
	if p.host.Host.Services != nil {
		j.Asker = p.host
	}
	if p.host.Host.Ledger != nil {
		j.Ledger = p.host
	}
	p.slack.Metrics.Warn("extraction", j.Warnings)
	return j
}

// putBrief keeps a unit's brief, the JSON the extraction stored, in the host.
func (p *plugin) putBrief(ctx context.Context, u unit.Unit, brief string, _ time.Time) error {
	if p.host.Host.Recorder == nil {
		return src.ErrUnsupported
	}
	return p.host.Host.Recorder.PutBrief(ctx, src.Brief{Unit: u.Wire(), Version: briefVersion, Body: json.RawMessage(brief)})
}

// briefExtra is the extra fields of the brief intent the host's
// configuration hands the plugin, which every brief carries.
func (p *plugin) briefExtra(ctx context.Context) (extract.Extra, error) {
	if p.host.Host.Services == nil {
		return extract.Extra{}, nil
	}
	c, err := p.host.Host.Services.Config(ctx)
	if err != nil {
		return extract.Extra{}, err
	}
	if c.BriefIntent == nil || len(c.BriefIntent.ExtraSchema) == 0 {
		return extract.Extra{}, nil
	}
	return extract.Extra{Schema: c.BriefIntent.ExtraSchema, Versions: c.BriefIntent.FieldVersions}, nil
}

// parseTarget is the unit an extracted link names: a Slack message, a
// "<provider>:<key>", or an address with no provider, which the host
// resolves with every plugin's codec.
func parseTarget(target string) (unit.Unit, bool) {
	t := strings.TrimSpace(target)
	switch {
	case t == "":
		return unit.Unit{}, false
	case send.IsMessage(t):
		return unit.Unit{}, false
	case strings.Contains(t, "://"):
		return unit.Unit{Key: t}, true
	}
	provider, key, ok := strings.Cut(t, ":")
	if !ok || provider == "" || key == "" || strings.ContainsAny(provider, " /") {
		return unit.Unit{}, false
	}
	return unit.Unit{Provider: unit.ProviderID(provider), Key: key}, true
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
	db *store.SlackStore
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

// BriefInput is empty: the extraction reads the conversation itself and
// writes the unit's brief in the call it makes anyway.
func (p *plugin) BriefInput(context.Context, sdk.Unit) (src.BriefInput, error) {
	return src.BriefInput{Parts: json.RawMessage(`{}`)}, nil
}

// BriefRevision is the unit's extraction cursor.
func (p *plugin) BriefRevision(ctx context.Context, w sdk.Unit) (src.BriefRevision, error) {
	if p.db == nil {
		return src.BriefRevision{}, nil
	}
	u := unit.Of(w)
	st, err := p.db.Extraction().States(ctx, []unit.Unit{u})
	if err != nil {
		return src.BriefRevision{}, err
	}
	return src.BriefRevision{Revision: st[u].LastTS, Parts: json.RawMessage(`{}`), Attempt: st[u].LastTS}, nil
}

func (p *plugin) BriefDescribe(context.Context, src.BriefInput) (string, error) {
	return "a Slack conversation", nil
}

// BriefStale is false: the extraction writes the brief again when the
// conversation moves.
func (p *plugin) BriefStale(context.Context, src.BriefStaleRequest) (bool, error) { return false, nil }
