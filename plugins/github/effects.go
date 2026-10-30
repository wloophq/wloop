package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/change"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/store"
)

// The effects plugin.toml declares.
const (
	EffectReviewed  = "pr.reviewed"
	EffectAddressed = "pr.addressed"
)

// applyTurnEffect applies a turn effect of the plugin's rounds: pr.reviewed
// marks the reviews a launched review round covered once its agent took
// it, pr.addressed the reviews so far and the head's checks and conflict an
// address round covered, and pr.nudged records whom a nudge turn asked.
func (p *Plugin) applyTurnEffect(ctx context.Context, req src.EffectRequest) error {
	r, err := p.running()
	if err != nil {
		return err
	}
	ref, err := refOf(req.Unit)
	if err != nil {
		return err
	}
	var marks map[string]string
	switch req.Effect {
	case EffectReviewed:
		if req.Route != src.RouteLaunch || !req.Taken {
			return nil
		}
		if marks, err = r.reviewedMarks(ctx, ref); err != nil {
			return err
		}
	case EffectAddressed:
		if req.Turn.Head == "" {
			return nil
		}
		marks = map[string]string{plan.MarkAddressedReviews: r.now().UTC().Format(time.RFC3339),
			plan.MarkAddressedChecks: req.Turn.Head, plan.MarkAddressedConflict: req.Turn.Head}
	case nudge.EffectNudged:
		return r.recordNudged(ctx, req)
	default:
		return fmt.Errorf("%w: no turn effect %s", src.ErrUnsupported, req.Effect)
	}
	if len(marks) == 0 || r.host.Recorder == nil {
		return nil
	}
	return r.host.Recorder.SetMarks(ctx, req.Unit, marks)
}

// reviewedMarks cover the reviews of the stored pull request a review round
// saw; none while the store has not read it in full.
func (r *running) reviewedMarks(ctx context.Context, ref pr.Ref) (map[string]string, error) {
	row, ok, err := r.db.PR(ctx, ref)
	if err != nil || !ok {
		return nil, err
	}
	details, err := r.db.Details(ctx, []pr.Ref{ref})
	if err != nil {
		return nil, err
	}
	viewer, err := r.db.Viewer(ctx)
	if err != nil {
		return nil, err
	}
	d, has := details[ref]
	if !has || viewer == "" {
		return nil, nil
	}
	r.cfgMu.Lock()
	cfg := r.cfg.Plan.For(ref.Owner, ref.Repo)
	r.cfgMu.Unlock()
	fp := change.Reviewer(change.ReviewerInput{PR: store.FactsOf(row, d, viewer), Viewer: viewer, TrustedOwners: []string{ref.Owner},
		TrustedAuthors: cfg.TrustedAuthors}).Fingerprint
	if fp == "" {
		return nil, nil
	}
	return map[string]string{plan.MarkReviewed: fp}, nil
}

// nudgedWho is the input nudge.prepare fills with whom the nudge asks.
const nudgedWho = "who"

// recordNudged records the nudged event of a nudge turn, keyed by the turn
// so that a turn that ended before its agent started it does not count.
func (r *running) recordNudged(ctx context.Context, req src.EffectRequest) error {
	if r.host.Recorder == nil {
		return nil
	}
	var inputs map[string]any
	if len(req.Inputs) > 0 {
		if err := json.Unmarshal(req.Inputs, &inputs); err != nil {
			return fmt.Errorf("%w: the turn's inputs: %v", src.ErrUnfit, err)
		}
	}
	var who []string
	switch l := inputs[nudgedWho].(type) {
	case string:
		who = []string{l}
	case []any:
		for _, x := range l {
			if s, ok := x.(string); ok {
				who = append(who, s)
			}
		}
	}
	return r.host.Recorder.RecordEvents(ctx, []src.Event{{Unit: req.Unit, Kind: pr.KindNudged, Key: req.Turn.ID, At: r.now(),
		Data: []byte(strings.Join(who, ", "))}})
}
