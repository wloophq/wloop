package github

import (
	"context"
	"fmt"
	"slices"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/row"
)

var _ src.Planner = (*Plugin)(nil)

// Proposals are the github-nudge rule's drafts: one per stalled open pull
// request whose nudge would go now, and its withdrawal on each open one no
// longer stalled. None before the store knows the viewer.
func (p *Plugin) Proposals(ctx context.Context, now time.Time) (src.Proposed, error) {
	r, err := p.running()
	if err != nil {
		return src.Proposed{}, err
	}
	if r.host.Reader == nil {
		return src.Proposed{}, nil
	}
	viewer, err := r.db.Viewer(ctx)
	if err != nil || viewer == "" {
		return src.Proposed{}, err
	}
	open, err := r.db.ListPRs(ctx, true)
	if err != nil {
		return src.Proposed{}, err
	}
	refs := make([]pr.Ref, len(open))
	for i, x := range open {
		refs[i] = x.Ref
	}
	ds, err := r.rows(ctx, refs, now)
	if err != nil {
		return src.Proposed{}, err
	}
	rows := make([]row.Row, len(ds))
	for i, d := range ds {
		rows[i] = d.row
	}
	x, err := r.texts(ctx)
	if err != nil {
		return src.Proposed{}, err
	}
	return nudge.Proposals(ctx, rows, r.host.Reader, x, r.settings().Ignore, now)
}

// texts are the nudge's texts in the plugin's profile layer, read when used
// so an edit of the user's layer takes at once.
func (r *running) texts(ctx context.Context) (nudge.Texts, error) {
	if r.host.Services == nil {
		return nudge.Texts{}, fmt.Errorf("%w: no profile layer to read %s from", src.ErrUnavailable, nudge.TextsFile)
	}
	raw, err := r.host.Services.Profile(ctx, nudge.TextsFile)
	if err != nil {
		return nudge.Texts{}, fmt.Errorf("%s: %w", nudge.TextsFile, err)
	}
	return nudge.ParseTexts(string(raw))
}

// ApplyEffect applies an effect of the plugin's turns, and a draft's
// pr.nudged, which the publisher recorded as it posted.
func (p *Plugin) ApplyEffect(ctx context.Context, req src.EffectRequest) error {
	switch {
	case req.Draft != nil && req.Effect == nudge.EffectNudged && slices.Contains(req.Draft.Effects, nudge.EffectNudged):
		return nil
	case req.Draft == nil && req.Turn != nil:
		return p.applyTurnEffect(ctx, req)
	}
	return fmt.Errorf("%w: effect %s", src.ErrUnsupported, req.Effect)
}
