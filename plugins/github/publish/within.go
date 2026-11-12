package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
)

const threadQuery = `query($id: ID!) { node(id: $id) { ... on PullRequestReviewThread { pullRequest { number repository { nameWithOwner } } } } }`

// Within says whether target lies inside ref: ref itself, comment:<id> for
// one of its review comments, or thread:<id> for one of its review threads
// by node id. A target GitHub does not know is outside.
func (p *Publisher) Within(ctx context.Context, ref pr.Ref, target string) (bool, error) {
	switch {
	case target == "":
		return true, nil
	case strings.HasPrefix(target, "comment:"):
		id, err := CommentTarget(target)
		if err != nil {
			return false, nil
		}
		return p.commentWithin(ctx, ref, id)
	case strings.HasPrefix(target, "thread:"):
		return p.threadWithin(ctx, ref, strings.TrimPrefix(target, "thread:"))
	}
	t, err := pr.ParseKey(target)
	return err == nil && strings.EqualFold(t.String(), ref.String()), nil
}

func (p *Publisher) commentWithin(ctx context.Context, ref pr.Ref, id int64) (bool, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/comments/%d", url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), id)
	resp, err := p.GH.REST(ctx, api.Request{Path: path})
	var se *api.StatusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading review comment %d of %s: %w", id, ref, err)
	}
	var c reviewComment
	if err := json.Unmarshal(resp.Body, &c); err != nil {
		return false, fmt.Errorf("reading review comment %d of %s: %w", id, ref, err)
	}
	return c.ID == id && ownPull(ref, c.PullRequestURL), nil
}

// ownPull says whether a pull_request_url names ref.
func ownPull(ref pr.Ref, pullURL string) bool {
	u, err := url.Parse(pullURL)
	own := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number)
	return err == nil && strings.HasSuffix(strings.ToLower(u.Path), strings.ToLower(own))
}

func (p *Publisher) threadWithin(ctx context.Context, ref pr.Ref, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	if p.Threads == nil {
		return false, errors.New("github: no GraphQL client to read review threads")
	}
	data, errs, err := p.Threads.GraphQL(ctx, threadQuery, map[string]any{"id": id})
	if err != nil {
		return false, fmt.Errorf("reading review thread %s: %w", id, err)
	}
	if errs["node"] != "" {
		return false, nil
	}
	var node *struct {
		PullRequest *struct {
			Number     int `json:"number"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
		} `json:"pullRequest"`
	}
	if err := json.Unmarshal(data["node"], &node); err != nil {
		return false, fmt.Errorf("reading review thread %s: %w", id, err)
	}
	return node != nil && node.PullRequest != nil && node.PullRequest.Number == ref.Number &&
		strings.EqualFold(node.PullRequest.Repository.NameWithOwner, ref.Owner+"/"+ref.Repo), nil
}
