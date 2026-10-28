package turn

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// testRef is a pull request as these tests name it.
type testRef struct {
	Owner, Repo string
	Number      int
}

func (r testRef) Unit() unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)}
}

func (r testRef) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", r.Owner, r.Repo, r.Number)
}

func refOf(u unit.Unit) (testRef, error) {
	repo, num, ok := strings.Cut(u.Key, "#")
	owner, name, ok2 := strings.Cut(repo, "/")
	n, err := strconv.Atoi(num)
	if !ok || !ok2 || err != nil {
		return testRef{}, fmt.Errorf("not a pull request: %s", u)
	}
	return testRef{Owner: owner, Repo: name, Number: n}, nil
}

// fakeGitHub is a pull request payload reader as GitHub's turn payload reads
// one: at the request's unit, its prior review anchored.
type fakeGitHub struct{ prs *fakePRs }

func (g fakeGitHub) Payload(ctx context.Context, r Request) (Payload, error) {
	ref, err := refOf(r.Unit)
	if err != nil {
		return nil, err
	}
	pr, err := g.prs.PR(ctx, ref)
	if err != nil {
		return nil, err
	}
	return placed(pr, ref).Anchored(r.LastVerdict), nil
}

func (fakeGitHub) CompletePayload(r Request, p Payload) (Payload, error) {
	pr, ok := p.(*PR)
	if !ok {
		return p, nil
	}
	ref, err := refOf(r.Unit)
	if err != nil {
		return nil, err
	}
	return placed(*pr, ref).Anchored(r.LastVerdict), nil
}

func (fakeGitHub) SyntheticPayload(u unit.Unit, probe bool) (Payload, error) {
	ref, err := refOf(u)
	if err != nil {
		return nil, err
	}
	pr := placed(PR{}, ref)
	if probe {
		pr.Title = ProbeTitle
	}
	return &pr, nil
}

func placed(pr PR, ref testRef) PR {
	pr.URL, pr.Owner, pr.Repo, pr.Number = ref.URL(), ref.Owner, ref.Repo, ref.Number
	return pr
}
