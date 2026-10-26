package slack

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// briefs is the Slack plugin's BriefSource: the extraction writes a unit's
// brief, in the call it makes anyway, so the queue only asks it to read a
// unit now. Its revision is the unit's extraction cursor.
type briefs struct {
	p *plugin

	mu     sync.Mutex
	job    *extract.Job
	noted  func(unit.Unit)
	intent func() brief.Intent
}

var (
	_ brief.BriefSource = (*briefs)(nil)
	_ brief.OnDemand    = (*briefs)(nil)
)

func (b *briefs) setJob(j *extract.Job) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.job = j
	j.Briefed = func(u unit.Unit) {
		b.mu.Lock()
		noted := b.noted
		b.mu.Unlock()
		if noted != nil {
			noted(u)
		}
	}
	j.Extra = b.extra
}

// extra is the brief intent's extra fields as the extraction asks them.
func (b *briefs) extra() extract.Extra {
	b.mu.Lock()
	intent := b.intent
	b.mu.Unlock()
	if intent == nil {
		return extract.Extra{}
	}
	c := intent().Schema
	if c.Extra == nil {
		return extract.Extra{}
	}
	return extract.Extra{Schema: json.RawMessage(c.Extra.Source()), Versions: c.FieldVersions()}
}

func (b *briefs) Watch(noted func(unit.Unit)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.noted = noted
}

// UseIntent has the extraction ask the brief intent's extra fields.
func (b *briefs) UseIntent(intent func() brief.Intent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.intent = intent
}

func (b *briefs) Want(_ context.Context, u unit.Unit) (bool, error) {
	b.mu.Lock()
	j := b.job
	b.mu.Unlock()
	if j == nil {
		return false, nil
	}
	return j.Want(u), nil
}

// Input is empty: the extraction reads the conversation itself.
func (b *briefs) Input(context.Context, unit.Unit) (brief.UnitInput, error) {
	return brief.UnitInput{Parts: json.RawMessage(`{}`)}, nil
}

func (b *briefs) Revision(ctx context.Context, u unit.Unit) (brief.At, error) {
	if b.p.deps.DB == nil {
		return brief.At{}, nil
	}
	st, err := b.p.deps.DB.Extraction().States(ctx, []unit.Unit{u})
	if err != nil {
		return brief.At{}, err
	}
	return brief.At{Revision: st[u].LastTS, Parts: json.RawMessage(`{}`)}, nil
}

func (b *briefs) Describe(brief.UnitInput) string { return "a Slack conversation" }

// Stale is false: the extraction writes the brief again when the
// conversation moves.
func (b *briefs) Stale(brief.Written, brief.At, string, time.Time) bool { return false }

func (b *briefs) AttemptKey(version string, cur brief.At) string { return version + " " + cur.Revision }
