package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// The actions the host calls.
const (
	// ActionBasics reads a pull request's basics: its answer is the
	// pull request as JSON.
	ActionBasics = "pr.basics"
	// ActionRefresh reads a pull request in full into the store and
	// reports it.
	ActionRefresh = "pr.refresh"
	// ActionAdopt takes a pull request over into the loop: the store keeps
	// it taken over, and the reviews its address rounds covered are
	// forgotten, so its triage starts from none of them. Its args are the
	// pull request's basics as the host read them, which the store keeps
	// until its next read when it holds none of it.
	ActionAdopt = "pr.adopt"
)

// Action runs one of the actions the host calls.
func (p *Plugin) Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error) {
	r, err := p.running()
	if err != nil {
		return nil, err
	}
	switch req.Action {
	case ActionBasics:
		ref, err := refOf(req.Unit)
		if err != nil {
			return nil, err
		}
		basics, err := r.client.Basics(ctx, ref)
		if err != nil {
			return nil, err
		}
		return json.Marshal(basics)
	case ActionRefresh:
		ref, err := refOf(req.Unit)
		if err != nil {
			return nil, err
		}
		if err := r.col.Refresh(ctx, ref); err != nil {
			return nil, err
		}
		return nil, r.report(ctx, false)
	case ActionNudgePrepare:
		return r.prepareNudge(ctx, req.Unit, req.Args)
	case ActionAdopt:
		ref, err := refOf(req.Unit)
		if err != nil {
			return nil, err
		}
		var basics *pr.PullRequest
		if len(req.Args) > 0 && string(req.Args) != "null" {
			basics = &pr.PullRequest{}
			if err := json.Unmarshal(req.Args, basics); err != nil {
				return nil, fmt.Errorf("%w: %s: %v", src.ErrUnfit, req.Action, err)
			}
		}
		return nil, r.adopt(ctx, ref, basics)
	}
	return nil, fmt.Errorf("%w: no action %s", src.ErrUnsupported, req.Action)
}

// adopt keeps ref taken over, forgets the reviews its address rounds
// covered, and reports it; a pull request the store holds none of is kept
// as its basics say until its next read.
func (r *running) adopt(ctx context.Context, ref pr.Ref, basics *pr.PullRequest) error {
	if err := r.db.SetAdopted(ctx, ref); err != nil {
		return err
	}
	if _, stored, err := r.db.PR(ctx, ref); err != nil {
		return err
	} else if !stored && basics != nil {
		b := basics.Facts
		row := pr.PR{Ref: ref, Title: b.Title, Author: b.Author, State: b.State, IsDraft: b.IsDraft, HeadOid: b.HeadRefOid,
			HeadBranch: b.HeadRefName, BaseBranch: b.BaseRefName, IsFork: b.IsCrossRepository, URL: ref.URL(), LastSeenAt: r.now()}
		if err := r.db.UpsertPR(ctx, row); err != nil {
			return err
		}
	}

	if r.host.Recorder != nil {
		if err := r.host.Recorder.SetMarks(ctx, ref.Unit(), map[string]string{plan.MarkAddressedReviews: ""}); err != nil && !errors.Is(err, src.ErrUnsupported) {
			return err
		}
	}
	return r.report(ctx, false)
}
