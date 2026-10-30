package ghsource

import (
	"context"

	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
)

// TurnPayloads is GitHub's turn payload: the GitHub plugin's, which Plugin
// reads, and the pull request a host round hands the turn, its prior review
// anchored to the head the loop's last record covered.
type TurnPayloads struct{ Plugin turn.PayloadReader }

var _ turn.PayloadCompleter = TurnPayloads{}

func (g TurnPayloads) Payload(ctx context.Context, r turn.Request) (turn.Payload, error) {
	return g.Plugin.Payload(ctx, r)
}

// CompletePayload is the caller's pull request at the request's unit, its
// prior review anchored; a payload of another kind stays as it is.
func (TurnPayloads) CompletePayload(r turn.Request, p turn.Payload) (turn.Payload, error) {
	pr, ok := p.(*turn.PR)
	if !ok {
		return p, nil
	}
	ref, err := dpr.ParseKey(r.Unit.Key)
	if err != nil {
		return nil, err
	}
	return at(*pr, ref).Anchored(r.LastVerdict), nil
}

// SyntheticPayload is the pull request u names, read from nowhere; a
// probe's carries the probe's title.
func (TurnPayloads) SyntheticPayload(u unit.Unit, probe bool) (turn.Payload, error) {
	ref, err := dpr.ParseKey(u.Key)
	if err != nil {
		return nil, err
	}
	pr := at(turn.PR{}, ref)
	if probe {
		pr.Title = turn.ProbeTitle
	}
	return &pr, nil
}

func at(pr turn.PR, ref github.Ref) turn.PR {
	pr.URL, pr.Owner, pr.Repo, pr.Number = ref.URL(), ref.Owner, ref.Repo, ref.Number
	return pr
}
