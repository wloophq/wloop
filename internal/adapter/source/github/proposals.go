package ghsource

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/internal/situation"
)

// NudgeRule is the Origin.Rule of the nudge drafts the plugin proposes.
const NudgeRule = "github-nudge"

// nudgePurpose is the purpose of the plugin's nudge drafts.
const nudgePurpose = "nudge"

// nudges proposes a nudge draft per stalled pull request the nudge intent
// would nudge, reading rows as the daemon's read model does.
type nudges struct {
	viewer func(ctx context.Context) (string, error)
	teams  func(ctx context.Context) ([]string, error)

	mu    sync.Mutex
	build func(readmodel.RowDeps) readmodel.Provider
	deps  readmodel.RowDeps
}

// rows keeps the row dependencies the daemon hands build, so Proposals
// derives rows over the same books, sessions, intakes and settings.
func (n *nudges) rows(build func(readmodel.RowDeps) readmodel.Provider) func(readmodel.RowDeps) readmodel.Provider {
	return func(rd readmodel.RowDeps) readmodel.Provider {
		n.mu.Lock()
		n.build, n.deps = build, rd
		n.mu.Unlock()
		return build(rd)
	}
}

// Proposals is one nudge proposal per stalled pull request whose nudge
// would go now; none before the daemon built the rows or knows the viewer.
func (n *nudges) Proposals(ctx context.Context, now time.Time) ([]outbox.Proposal, error) {
	n.mu.Lock()
	build, rd := n.build, n.deps
	n.mu.Unlock()
	if build == nil || rd.Settings == nil {
		return nil, nil
	}
	viewer, err := n.viewer(ctx)
	if err != nil || viewer == "" {
		return nil, err
	}
	teams, err := n.teams(ctx)
	if err != nil {
		return nil, err
	}
	p := build(rd)
	units, err := p.Listed(ctx)
	if err != nil {
		return nil, err
	}
	ins, err := p.Load(ctx, units)
	if err != nil {
		return nil, err
	}
	at := now.Truncate(time.Minute)
	base := readmodel.Base{Viewers: map[unit.ProviderID]string{unit.GitHub: viewer}, Teams: teams}
	ignore := prrow.IgnoreOf(rd.Settings().Cfg)
	var out []outbox.Proposal
	for _, u := range units {
		in, ok := ins[u]
		if !ok {
			continue
		}
		row, err := p.Derive(base, in, at)
		if err != nil {
			return nil, err
		}
		if !nudgeable(row) {
			continue
		}
		unmuted, err := p.Derive(base, prrow.Unmuted(in), at)
		if err != nil {
			return nil, err
		}
		if prop, ok := nudgeProposal(row, unmuted, prrow.EventsOf(in), viewer, ignore, now); ok {
			out = append(out, prop)
		}
	}
	return out, nil
}

// nudgeable says whether row is stalled and not one the card alone shows
// (draft, approved by the viewer, on hold, muted).
func nudgeable(row api.Row) bool {
	p, ok := prrow.PayloadOf(row)
	if !ok || !slices.Contains(row.Views, api.ViewStalled) {
		return false
	}
	switch situation.Kind(row.Kind) {
	case situation.KindMuted, situation.KindDraft, situation.KindDone:
		return false
	}
	return !p.Approved && !p.Override.Hold && p.Mute == nil
}

// nudgeProposal is the nudge of row, whom the nudge intent would ask from
// its unmuted row, when that intent would not refuse it now.
func nudgeProposal(row, unmuted api.Row, evs []dinv.Event, viewer string, ignore []string, now time.Time) (outbox.Proposal, bool) {
	ref, err := pr.ParseKey(row.Unit.Key)
	if err != nil {
		return outbox.Proposal{}, false
	}
	p, _ := prrow.PayloadOf(row)
	who, why := prrow.NudgeTargets(unmuted, p.Author, viewer, ignore)
	if prrow.NudgeRefusal(ref, who, why, unmuted.Summary.Since, evs, now) != "" {
		return outbox.Proposal{}, false
	}
	return outbox.Proposal{
		Unit:    row.Unit,
		Items:   waitingItems(row, who),
		Purpose: nudgePurpose,
		Effects: []string{builtin.EffectPRNudged, builtin.EffectEntryNudged},
		Text:    nudgeText(who, unmuted),
		Origin:  draft.Origin{Kind: draft.OriginAuto, Rule: NudgeRule, Key: row.Unit.String()},
	}, true
}

// nudgeText asks who for the next step; a person is a mention, a team is
// written as GitHub mentions it, since its key would post as plain text.
func nudgeText(who []string, r api.Row) string {
	since := ""
	if !r.Summary.Since.IsZero() {
		since = " since " + r.Summary.Since.UTC().Format("2006-01-02")
	}
	if slices.Equal(who, []string{prrow.NudgeMaintainers}) {
		return fmt.Sprintf("Friendly nudge: this pull request has had no reviewer%s. Could a maintainer take a look or suggest a reviewer?", since)
	}
	mentions := make([]string, len(who))
	for i, w := range who {
		login := strings.TrimPrefix(w, "@")
		if strings.Contains(login, "/") {
			mentions[i] = w
			continue
		}
		mentions[i] = "@{github:" + login + "}"
	}
	return fmt.Sprintf("%s friendly nudge: this pull request has been waiting on you%s (%s). Could you take a look when you have a moment?",
		strings.Join(mentions, " "), since, situation.Kind(r.Kind).Label())
}

// waitingItems are the row's open derived items waiting on someone the nudge asks.
func waitingItems(row api.Row, who []string) []string {
	keys := make([]string, 0, len(who))
	for _, w := range who {
		login := strings.TrimPrefix(w, "@")
		if strings.Contains(login, "/") {
			keys = append(keys, "group:"+login)
		} else {
			keys = append(keys, "github:"+login)
		}
	}
	var out []string
	for _, it := range Items(row) {
		if it.State == items.Open && slices.ContainsFunc(it.WaitingOn, func(x items.Person) bool {
			return slices.ContainsFunc(keys, func(k string) bool { return strings.EqualFold(k, x.Key) })
		}) {
			out = append(out, it.ID)
		}
	}
	return out
}
