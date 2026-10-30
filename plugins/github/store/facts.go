package store

import (
	"slices"
	"strings"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// FactsOf is p with its detail d as the rules read them, the viewer's own
// reviews marked and every login bare.
func FactsOf(p pr.PR, d Detail, viewer string) pr.Facts {
	byViewer := func(login string) bool { return viewer != "" && strings.EqualFold(login, viewer) }
	out := pr.Facts{
		Ref:                     p.Ref,
		State:                   p.State,
		IsDraft:                 p.IsDraft,
		Title:                   p.Title,
		HeadRefName:             p.HeadBranch,
		BaseRefName:             p.BaseBranch,
		HeadRefOid:              p.HeadOid,
		IsCrossRepository:       p.IsFork,
		Visibility:              d.Visibility,
		Author:                  p.Author,
		AuthorAssociation:       d.AuthorAssociation,
		ReviewDecision:          p.ReviewDecision,
		Mergeable:               p.Mergeable,
		ChecksState:             p.ChecksState,
		NewestChangesRequestOid: d.NewestChangesRequestOid,
		LatestReviews:           slices.Clone(d.LatestReviews),
		Comments:                slices.Clone(d.Comments),
		ReviewRequests:          slices.Clone(p.ReviewRequests),
		ViewerRequested:         slices.ContainsFunc(p.ReviewRequests, byViewer),
		Commits:                 slices.Clone(d.Commits),
		OpenThreads:             slices.Clone(d.OpenThreads),
		ThreadsTotal:            d.ThreadsTotal,
		Checks:                  slices.Clone(d.Checks),
		Labels:                  slices.Clone(d.Labels),
	}
	for _, r := range d.Reviews {
		r.Author = bareLogin(r.Author)
		r.ByViewer = byViewer(r.Author)
		out.Reviews = append(out.Reviews, r)
	}
	for i := range out.LatestReviews {
		out.LatestReviews[i].Author = bareLogin(out.LatestReviews[i].Author)
	}
	for i := range out.Comments {
		out.Comments[i].Author = bareLogin(out.Comments[i].Author)
	}
	for i := range out.OpenThreads {
		out.OpenThreads[i].LastAuthor = bareLogin(out.OpenThreads[i].LastAuthor)
	}
	return out
}

func bareLogin(login string) string { return strings.TrimSuffix(login, "[bot]") }
