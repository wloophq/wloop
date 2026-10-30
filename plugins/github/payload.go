package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pelletier/go-toml/v2"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// wordingFile is the profile layer's file of how a turn names a pull
// request.
const wordingFile = "wording.toml"

// Payload is the data a turn's templates read of a pull request, under the
// payload root plugin.toml names: its basics as GitHub reads them now and
// your prior review, anchored to the head the loop's last record covered.
type Payload struct {
	URL, Owner, Repo  string
	Number            int
	Title, Author     string
	State             string
	Association       string
	Head, Base        string
	Labels            []string
	CrossRepository   bool
	Visibility        string
	Prior             *PriorReview
	ReviewRequested   bool
	LastReviewVerdict string
}

// PriorReview is your latest submitted review: the head it covered, when,
// its newest verdict that is not a comment, and whether you stand behind
// the loop's record of it.
type PriorReview struct {
	Verdict, Head, Endorsed string
	At                      time.Time
}

// Payload is a pull request as a turn reads it.
func (p *Plugin) Payload(ctx context.Context, req src.PayloadRequest) (src.PayloadResult, error) {
	r, err := p.running()
	if err != nil {
		return src.PayloadResult{}, err
	}
	ref, err := refOf(req.Unit)
	if err != nil {
		return src.PayloadResult{}, err
	}
	basics, err := r.client.Basics(ctx, ref)
	if err != nil {
		return src.PayloadResult{}, err
	}
	pl := payloadOf(ref, basics.Facts).anchored(req.LastVerdict)
	data, err := json.Marshal(pl)
	if err != nil {
		return src.PayloadResult{}, err
	}
	w, err := r.wording(ctx)
	if err != nil {
		return src.PayloadResult{}, err
	}
	w.Ref, w.Short = ref.String(), ref.String()
	return src.PayloadResult{Arg: pl.URL, Wording: w, Facts: pl.facts(), Data: data}, nil
}

// wording is how the profile layer words a pull request.
func (r *running) wording(ctx context.Context) (src.Wording, error) {
	w, err := r.words(ctx)
	if err != nil {
		return src.Wording{}, err
	}
	if w.Noun == "" || w.This == "" {
		return src.Wording{}, errors.New("the profile layer's " + wordingFile + " names no noun or this")
	}
	return src.Wording{Noun: w.Noun, This: w.This}, nil
}

// words are the profile layer's words of a pull request.
type words struct {
	Noun       string `toml:"noun"`
	This       string `toml:"this"`
	BriefInput string `toml:"brief_input"`
}

// words reads the profile layer's wording file, when used, so an edit of
// the user's layer takes at once.
func (r *running) words(ctx context.Context) (words, error) {
	if r.host.Services == nil {
		return words{}, fmt.Errorf("%w: no profile layer to word a pull request", src.ErrUnavailable)
	}
	raw, err := r.host.Services.Profile(ctx, wordingFile)
	if err != nil {
		return words{}, fmt.Errorf("the profile layer's %s: %w", wordingFile, err)
	}
	var w words
	if err := toml.Unmarshal(raw, &w); err != nil {
		return words{}, fmt.Errorf("the profile layer's %s: %w", wordingFile, err)
	}
	return w, nil
}

func payloadOf(ref pr.Ref, f pr.Facts) Payload {
	out := Payload{URL: ref.URL(), Owner: ref.Owner, Repo: ref.Repo, Number: ref.Number, Title: f.Title, Author: f.Author, State: f.State,
		Association: f.AuthorAssociation, Head: f.HeadRefOid, Base: f.BaseRefName, Labels: f.Labels, CrossRepository: f.IsCrossRepository,
		Visibility: f.Visibility, ReviewRequested: f.ViewerRequested}
	var verdictAt, headAt, newest time.Time
	for _, rv := range f.Reviews {
		if !rv.ByViewer || rv.SubmittedAt.IsZero() {
			continue
		}
		if rv.SubmittedAt.After(newest) {
			out.LastReviewVerdict, newest = rv.State, rv.SubmittedAt
		}
		switch rv.State {
		case pr.ReviewApproved, pr.ReviewChangesRequested, pr.ReviewCommented, pr.ReviewDismissed:
		default:
			continue
		}
		if out.Prior == nil {
			out.Prior = &PriorReview{}
		}
		if rv.CommitOid != "" && rv.SubmittedAt.After(headAt) {
			out.Prior.Head, headAt = rv.CommitOid, rv.SubmittedAt
		}
		if rv.SubmittedAt.After(out.Prior.At) {
			out.Prior.At = rv.SubmittedAt
		}
		if rv.State != pr.ReviewCommented && rv.SubmittedAt.After(verdictAt) {
			out.Prior.Verdict, verdictAt = rv.State, rv.SubmittedAt
		}
	}
	return out
}

// anchored is p with its prior review anchored to the head the loop's
// record v covered, when the review is no newer than the record.
func (p Payload) anchored(v *src.Verdict) Payload {
	if v != nil && p.Prior != nil && !p.Prior.At.After(v.At) {
		prior := *p.Prior
		if v.Head != "" {
			prior.Head = v.Head
		}
		prior.Endorsed = v.Endorsed
		p.Prior = &prior
	}
	return p
}

// facts are the turn's pr facts the payload answers.
func (p Payload) facts() []src.Fact {
	labels := p.Labels
	if labels == nil {
		labels = []string{}
	}
	return []src.Fact{
		{Name: "pr.author", Value: src.String(p.Author)},
		{Name: "pr.state", Value: src.String(p.State)},
		{Name: "pr.slug", Value: src.String(pr.Ref{Owner: p.Owner, Repo: p.Repo, Number: p.Number}.String())},
		{Name: "pr.owner", Value: src.String(p.Owner)},
		{Name: "pr.repo", Value: src.String(p.Owner + "/" + p.Repo)},
		{Name: "pr.repo_name", Value: src.String(p.Repo)},
		{Name: "pr.base", Value: src.String(p.Base)},
		{Name: "pr.association", Value: src.String(p.Association)},
		{Name: "pr.cross_repository", Value: src.Bool(p.CrossRepository)},
		{Name: "pr.visibility", Value: src.String(p.Visibility)},
		{Name: "pr.labels", Value: src.List(labels...)},
		{Name: "pr.address.reviewers", Value: src.List()},
		{Name: "pr.address.checks", Value: src.List()},
		{Name: "pr.address.conflict", Value: src.Bool(false)},
	}
}
