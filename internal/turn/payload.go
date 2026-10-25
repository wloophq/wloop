package turn

import (
	"context"

	"github.com/wloophq/wloop/internal/core/instructions"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// Payload is a unit as its provider hands it to a turn's instructions.
type Payload interface {
	Unit() unit.Unit
	// Arg is how the instructions command names the unit: a PR URL for github, "<provider>:<key>" otherwise.
	Arg() string
	Wording() Wording
	// Fact resolves a provider-namespaced predicate fact ("pr.author"); ok false for a name it does not own.
	Fact(name string) (v any, ok bool)
}

type Wording struct {
	Noun  string // "pull request", "Slack thread"
	This  string // "this pull request"
	Ref   string // "acme/api#7", "#eng thread 1700000000.123456"
	Short string // headline form, as {{.PR.Slug}} today
}

// Contents is a payload that brings its unit's content as text, such as a
// Slack thread's messages; the instructions quote it as data.
type Contents interface {
	Contents() string
}

func contents(p any) string {
	if c, ok := p.(Contents); ok {
		return c.Contents()
	}
	return ""
}

type PayloadReader interface {
	Payload(ctx context.Context, r Request) (Payload, error)
}

// ItemsReader lists a unit's open items for its instructions; internal/cli/turnitems.go implements it.
type ItemsReader interface {
	OpenItems(ctx context.Context, u unit.Unit) ([]instructions.OpenItem, error)
}

var prWording = Wording{Noun: "pull request", This: "this pull request"}

func (p *PR) Unit() unit.Unit { return p.ref().Unit() }

func (p *PR) Arg() string { return p.URL }

func (p *PR) Wording() Wording {
	w := prWording
	w.Ref = p.Slug()
	w.Short = w.Ref
	return w
}

func (p *PR) Fact(name string) (any, bool) {
	switch name {
	case "pr.author":
		return p.Author, true
	case "pr.state":
		return p.State, true
	case "pr.slug":
		return p.Slug(), true
	case "pr.owner":
		return p.Owner, true
	case "pr.repo":
		return p.Repo, true
	case "pr.base":
		return p.Base, true
	case "pr.association":
		return p.Association, true
	case "pr.cross_repository":
		return p.CrossRepository, true
	case "pr.visibility":
		return p.Visibility, true
	case "pr.labels":
		if p.Labels == nil {
			return []string{}, true
		}
		return p.Labels, true
	case "pr.address.reviewers":
		return p.Address.ReviewerLogins(), true
	case "pr.address.checks":
		return p.Address.CheckNames(), true
	}
	return nil, false
}

// PayloadFacts are p's provider facts as a predicate reads them.
func PayloadFacts(p Payload) expr.Facts { return payloadFacts{p} }

type payloadFacts struct{ p Payload }

func (f payloadFacts) Get(field string) any {
	v, _ := f.p.Fact(field)
	return v
}

func (p *PR) ref() github.Ref { return github.Ref{Owner: p.Owner, Repo: p.Repo, Number: p.Number} }

// GitHub is github's PayloadReader: the pull request as PRs reads it.
type GitHub struct{ PRs PRReader }

func (g GitHub) Payload(ctx context.Context, r Request) (Payload, error) {
	ref, err := dpr.ParseKey(r.Unit.Key)
	if err != nil {
		return nil, err
	}
	pr, err := g.PRs.PR(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// anchored is pr at ref with its prior review anchored to the head the loop's record covered.
func anchored(pr PR, ref github.Ref, v *RecordedVerdict) *PR {
	pr.URL, pr.Owner, pr.Repo, pr.Number = ref.URL(), ref.Owner, ref.Repo, ref.Number
	if v != nil && pr.Prior != nil && !pr.Prior.At.After(v.At) {
		prior := *pr.Prior
		if v.Head != "" {
			prior.Head = v.Head
		}
		prior.Endorsed = v.Endorsed
		pr.Prior = &prior
	}
	return &pr
}
