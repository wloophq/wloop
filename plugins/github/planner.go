package github

import (
	"context"
	"fmt"
	"slices"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/pr"
	"github.com/wloophq/wloop/plugins/github/row"
)

var _ src.Planner = (*Plugin)(nil)

// Proposals are the github-nudge rule's drafts: one per stalled open pull
// request whose nudge would go now. None before the store knows the viewer.
func (p *Plugin) Proposals(ctx context.Context, now time.Time) ([]src.Draft, error) {
	r, err := p.running()
	if err != nil {
		return nil, err
	}
	if r.host.Reader == nil {
		return nil, nil
	}
	viewer, err := r.db.Viewer(ctx)
	if err != nil || viewer == "" {
		return nil, err
	}
	open, err := r.db.ListPRs(ctx, true)
	if err != nil {
		return nil, err
	}
	refs := make([]pr.Ref, len(open))
	for i, x := range open {
		refs[i] = x.Ref
	}
	ds, err := r.rows(ctx, refs, now)
	if err != nil {
		return nil, err
	}
	rows := make([]row.Row, len(ds))
	for i, d := range ds {
		rows[i] = d.row
	}
	return nudge.Proposals(ctx, rows, r.host.Reader, r.settings().Ignore, now)
}

// ApplyEffect applies a draft's pr.nudged, which the publisher recorded as
// it posted.
func (p *Plugin) ApplyEffect(_ context.Context, req src.EffectRequest) error {
	if req.Draft != nil && req.Effect == nudge.EffectNudged && slices.Contains(req.Draft.Effects, nudge.EffectNudged) {
		return nil
	}
	return fmt.Errorf("%w: effect %s", src.ErrUnsupported, req.Effect)
}

func (p *Plugin) Plan(context.Context, sdk.Unit, src.RoundState) (src.Plan, error) {
	return src.Plan{}, fmt.Errorf("%w: the host plans GitHub's rounds", src.ErrUnsupported)
}

func (p *Plugin) Payload(context.Context, src.PayloadRequest) (src.PayloadResult, error) {
	return src.PayloadResult{}, fmt.Errorf("%w: the host builds GitHub's payloads", src.ErrUnsupported)
}

func (p *Plugin) Placement(context.Context, sdk.Unit, string) (src.Placements, error) {
	return src.Placements{}, fmt.Errorf("%w: the host places GitHub's work", src.ErrUnsupported)
}
