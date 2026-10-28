package turn

import (
	"context"

	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/domain/unit"
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

// PayloadReader is a provider's turn payload: a source plugin's
// Planner.Payload as the turn reads it.
type PayloadReader interface {
	Payload(ctx context.Context, r Request) (Payload, error)
}

// PayloadCompleter is a PayloadReader that completes a payload the caller
// handed the turn, and names a unit without reading it for an intent whose
// context is synthetic, such as a probe.
type PayloadCompleter interface {
	CompletePayload(r Request, p Payload) (Payload, error)
	SyntheticPayload(u unit.Unit, probe bool) (Payload, error)
}

// ItemsReader lists a unit's open items for its instructions; internal/cli/turnitems.go implements it.
type ItemsReader interface {
	OpenItems(ctx context.Context, u unit.Unit) ([]instructions.OpenItem, error)
}

var prWording = Wording{Noun: "pull request", This: "this pull request"}

func (p *PR) Unit() unit.Unit { return unit.Unit{Provider: unit.GitHub, Key: p.Slug()} }

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
		return p.Owner + "/" + p.Repo, true
	case "pr.repo_name":
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
	case "pr.address.conflict":
		return p.Address != nil && p.Address.Conflict != nil, true
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

// Anchored is p with its prior review anchored to the head the loop's
// record v covered, when the review is no newer than the record.
func (p PR) Anchored(v *RecordedVerdict) *PR {
	if v != nil && p.Prior != nil && !p.Prior.At.After(v.At) {
		prior := *p.Prior
		if v.Head != "" {
			prior.Head = v.Head
		}
		prior.Endorsed = v.Endorsed
		p.Prior = &prior
	}
	return &p
}
