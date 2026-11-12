package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// Conversation is what a pull request's review asks still: its unresolved
// review threads and each reviewer's latest review.
type Conversation struct {
	Threads []OpenThread   `json:"threads"`
	Reviews []LatestReview `json:"latest_reviews"`
}

// OpenThread is an unresolved review thread; a comment's ID is the REST id
// a thread_reply or a resolve targets as comment:<id>.
type OpenThread struct {
	Path       string          `json:"path"`
	Line       int             `json:"line,omitempty"`
	IsResolved bool            `json:"is_resolved"`
	IsOutdated bool            `json:"is_outdated"`
	Comments   []ThreadComment `json:"comments"`
}

// ThreadComment is a comment of a review thread.
type ThreadComment struct {
	ID     int64     `json:"id"`
	Author string    `json:"author"`
	Body   string    `json:"body"`
	At     time.Time `json:"created_at"`
}

// LatestReview is a reviewer's latest review.
type LatestReview struct {
	Author      string    `json:"author"`
	State       string    `json:"state"`
	Commit      string    `json:"commit"`
	SubmittedAt time.Time `json:"submitted_at"`
}

const conversationQuery = `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $repo) { pullRequest(number: $number) {
    latestReviews(first: 100) { nodes { author { login } state submittedAt commit { oid } } }
    reviewThreads(first: 50, after: $after) { pageInfo { hasNextPage endCursor }
      nodes { isResolved isOutdated path line comments(first: 100) { nodes { databaseId author { login } body createdAt } } } }
  } }
}`

type login struct {
	Login string `json:"login"`
}

// Conversation reads ref's unresolved review threads and latest reviews
// through GraphQL reads.
func (p *Publisher) Conversation(ctx context.Context, ref pr.Ref) (Conversation, error) {
	if p.Threads == nil {
		return Conversation{}, errors.New("github: no GraphQL client to read review threads")
	}
	out := Conversation{Threads: []OpenThread{}, Reviews: []LatestReview{}}
	vars := map[string]any{"owner": ref.Owner, "repo": ref.Repo, "number": ref.Number, "after": nil}
	for page := range threadPages {
		data, errs, err := p.Threads.GraphQL(ctx, conversationQuery, vars)
		if err != nil {
			return Conversation{}, fmt.Errorf("reading the review threads of %s: %w", ref, err)
		}
		if msg := errs["repository"]; msg != "" {
			return Conversation{}, fmt.Errorf("reading the review threads of %s: %s", ref, msg)
		}
		var repo struct {
			PullRequest *struct {
				LatestReviews struct {
					Nodes []struct {
						Author      login     `json:"author"`
						State       string    `json:"state"`
						SubmittedAt time.Time `json:"submittedAt"`
						Commit      struct {
							Oid string `json:"oid"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"latestReviews"`
				ReviewThreads struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						IsResolved bool   `json:"isResolved"`
						IsOutdated bool   `json:"isOutdated"`
						Path       string `json:"path"`
						Line       int    `json:"line"`
						Comments   struct {
							Nodes []struct {
								DatabaseID int64     `json:"databaseId"`
								Author     login     `json:"author"`
								Body       string    `json:"body"`
								CreatedAt  time.Time `json:"createdAt"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		}
		if err := json.Unmarshal(data["repository"], &repo); err != nil || repo.PullRequest == nil {
			return Conversation{}, fmt.Errorf("reading the review threads of %s: GitHub's answer names no pull request", ref)
		}
		if page == 0 {
			for _, r := range repo.PullRequest.LatestReviews.Nodes {
				out.Reviews = append(out.Reviews, LatestReview{Author: r.Author.Login, State: r.State, Commit: r.Commit.Oid, SubmittedAt: r.SubmittedAt})
			}
		}
		ts := repo.PullRequest.ReviewThreads
		for _, n := range ts.Nodes {
			if n.IsResolved {
				continue
			}
			t := OpenThread{Path: n.Path, Line: n.Line, IsOutdated: n.IsOutdated, Comments: []ThreadComment{}}
			for _, c := range n.Comments.Nodes {
				t.Comments = append(t.Comments, ThreadComment{ID: c.DatabaseID, Author: c.Author.Login, Body: c.Body, At: c.CreatedAt})
			}
			out.Threads = append(out.Threads, t)
		}
		if !ts.PageInfo.HasNextPage {
			return out, nil
		}
		vars["after"] = ts.PageInfo.EndCursor
	}
	return out, nil
}
