package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// threadPages bounds how many pages of review threads a resolve reads.
const threadPages = 10

// reviewStates are the states GitHub stores a review in, by the event that
// posts it.
var reviewStates = map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}

var sides = []string{"", "LEFT", "RIGHT"}

// Review is a review draft's payload, which drafts/review/schema.json
// shapes.
type Review struct {
	Event    string          `json:"event"`
	Comments []InlineComment `json:"comments,omitempty"`
}

// InlineComment is one of a review's inline comments, as GitHub takes it.
type InlineComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Side      string `json:"side,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
	Body      string `json:"body"`
}

// DecodeReview is a review draft's payload, refused when it does not fit
// the kind's schema.
func DecodeReview(raw json.RawMessage) (Review, error) {
	var r Review
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Review{}, fmt.Errorf("a review's payload: %w", err)
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return Review{}, errors.New("a review's payload: text after the object")
	}
	if _, ok := reviewStates[r.Event]; !ok {
		return Review{}, fmt.Errorf("a review's event is APPROVE, REQUEST_CHANGES or COMMENT, not %q", r.Event)
	}
	for i, c := range r.Comments {
		switch {
		case strings.TrimSpace(c.Path) == "":
			return Review{}, fmt.Errorf("inline comment %d names no path", i+1)
		case c.Line <= 0:
			return Review{}, fmt.Errorf("inline comment %d names no line", i+1)
		case strings.TrimSpace(c.Body) == "":
			return Review{}, fmt.Errorf("inline comment %d is empty", i+1)
		case !slices.Contains(sides, c.Side) || !slices.Contains(sides, c.StartSide):
			return Review{}, fmt.Errorf("inline comment %d: a side is LEFT or RIGHT", i+1)
		}
	}
	return r, nil
}

// CommentTarget is the review comment a target of the form comment:<id>
// names, by its REST id.
func CommentTarget(target string) (int64, error) {
	s, ok := strings.CutPrefix(target, "comment:")
	id, err := strconv.ParseInt(s, 10, 64)
	if !ok || err != nil || id <= 0 {
		return 0, fmt.Errorf("target %q is not comment:<id>, the review comment's REST id", target)
	}
	return id, nil
}

// noPayload refuses a payload on a kind whose schema has no field.
func noPayload(d src.Draft) error {
	switch strings.TrimSpace(string(d.Payload)) {
	case "", "null", "{}":
		return nil
	}
	return fmt.Errorf("a %s draft takes no payload", d.Kind)
}

type reviewRequest struct {
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []InlineComment `json:"comments,omitempty"`
}

type review struct {
	ID          int64     `json:"id"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	CommitID    string    `json:"commit_id"`
	SubmittedAt time.Time `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

type reviewComment struct {
	ID             int64     `json:"id"`
	HTMLURL        string    `json:"html_url"`
	Body           string    `json:"body"`
	InReplyTo      int64     `json:"in_reply_to_id"`
	PullRequestURL string    `json:"pull_request_url"`
	CreatedAt      time.Time `json:"created_at"`
	User           struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (p *Publisher) prepareReview(ctx context.Context, d src.Draft) (pr.Ref, reviewRequest, error) {
	ref, err := selfRef(d)
	if err != nil {
		return pr.Ref{}, reviewRequest{}, err
	}
	if d.Basis == "" {
		return pr.Ref{}, reviewRequest{}, errors.New("a review states as basis the head commit it was written against")
	}
	r, err := DecodeReview(d.Payload)
	if err != nil {
		return pr.Ref{}, reviewRequest{}, err
	}
	body, err := p.body(ctx, d)
	if err != nil {
		return pr.Ref{}, reviewRequest{}, err
	}
	return ref, reviewRequest{CommitID: d.Basis, Body: body, Event: r.Event, Comments: r.Comments}, nil
}

// Head is ref's head commit as GitHub has it now.
func (p *Publisher) Head(ctx context.Context, ref pr.Ref) (string, error) {
	resp, err := p.GH.REST(ctx, api.Request{Path: pullPath(ref)})
	if err != nil {
		return "", fmt.Errorf("reading the head of %s: %w", ref, err)
	}
	var pull struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(resp.Body, &pull); err != nil || pull.Head.SHA == "" {
		return "", fmt.Errorf("reading the head of %s: GitHub's answer names none", ref)
	}
	return pull.Head.SHA, nil
}

// publishReview posts d as one review at its basis, once the pull
// request's head, read now, is still that basis.
func (p *Publisher) publishReview(ctx context.Context, d src.Draft) (src.Evidence, error) {
	ref, req, err := p.prepareReview(ctx, d)
	if err != nil {
		return src.Evidence{}, err
	}
	head, err := p.Head(ctx, ref)
	if err != nil {
		return src.Evidence{}, err
	}
	if head != req.CommitID {
		return src.Evidence{}, fmt.Errorf("%w: the review was written at %s, and the head of %s is %s", src.ErrStale, req.CommitID, ref, head)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return src.Evidence{}, err
	}
	resp, err := p.GH.REST(ctx, api.Request{Method: http.MethodPost, Path: pullPath(ref) + "/reviews", Body: payload, Mutation: true})
	if err != nil {
		if written(err) {
			return src.Evidence{}, fmt.Errorf("%w on %s: %w", src.ErrMayHavePosted, ref, err)
		}
		return src.Evidence{}, fmt.Errorf("reviewing %s: %w", ref, err)
	}
	var rv review
	if err := json.Unmarshal(resp.Body, &rv); err != nil || rv.ID == 0 {
		return src.Evidence{}, fmt.Errorf("%w on %s: GitHub's answer names no review", src.ErrMayHavePosted, ref)
	}
	return src.Evidence{Ref: strconv.FormatInt(rv.ID, 10), Permalink: rv.HTMLURL, At: rv.SubmittedAt}, nil
}

// verifyReview looks for the viewer's review at d's basis, in the state
// d's event posts, submitted since startedAt, with d's body when it has
// one: a review with no body has nothing else to tell it by.
func (p *Publisher) verifyReview(ctx context.Context, d src.Draft, startedAt time.Time) (src.Evidence, bool, error) {
	ref, req, err := p.prepareReview(ctx, d)
	if err != nil {
		return src.Evidence{}, false, err
	}
	viewer, err := p.viewer(ctx)
	if err != nil {
		return src.Evidence{}, false, err
	}
	from := startedAt.Add(-clockSkew)
	for page := 1; page <= verifyPages; page++ {
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		resp, err := p.GH.REST(ctx, api.Request{Path: pullPath(ref) + "/reviews?" + q.Encode()})
		if err != nil {
			return src.Evidence{}, false, fmt.Errorf("listing the reviews of %s: %w", ref, err)
		}
		var rs []review
		if err := json.Unmarshal(resp.Body, &rs); err != nil {
			return src.Evidence{}, false, fmt.Errorf("listing the reviews of %s: %w", ref, err)
		}
		for _, r := range rs {
			if strings.EqualFold(r.User.Login, viewer) && r.CommitID == req.CommitID && r.State == reviewStates[req.Event] &&
				!r.SubmittedAt.Before(from) && sameBody(r.Body, req.Body) {
				return src.Evidence{Ref: strconv.FormatInt(r.ID, 10), Permalink: r.HTMLURL, At: r.SubmittedAt}, true, nil
			}
		}
		if len(rs) < 100 {
			break
		}
	}
	return src.Evidence{}, false, nil
}

func (p *Publisher) prepareReply(ctx context.Context, d src.Draft) (pr.Ref, int64, string, error) {
	ref, err := unitRef(d)
	if err != nil {
		return pr.Ref{}, 0, "", err
	}
	id, err := CommentTarget(d.Target)
	if err != nil {
		return pr.Ref{}, 0, "", err
	}
	if err := noPayload(d); err != nil {
		return pr.Ref{}, 0, "", err
	}
	body, err := p.body(ctx, d)
	if err != nil {
		return pr.Ref{}, 0, "", err
	}
	if strings.TrimSpace(body) == "" {
		return pr.Ref{}, 0, "", errors.New("the reply is empty")
	}
	return ref, id, body, nil
}

// publishReply posts d as a reply in the review thread of the comment its
// target names, once that comment reads as one of the pull request's.
func (p *Publisher) publishReply(ctx context.Context, d src.Draft) (src.Evidence, error) {
	ref, id, body, err := p.prepareReply(ctx, d)
	if err != nil {
		return src.Evidence{}, err
	}
	if _, err := p.reviewComment(ctx, ref, id); err != nil {
		return src.Evidence{}, err
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return src.Evidence{}, err
	}
	path := fmt.Sprintf("%s/comments/%d/replies", pullPath(ref), id)
	resp, err := p.GH.REST(ctx, api.Request{Method: http.MethodPost, Path: path, Body: payload, Mutation: true})
	if err != nil {
		if written(err) {
			return src.Evidence{}, fmt.Errorf("%w on %s: %w", src.ErrMayHavePosted, ref, err)
		}
		return src.Evidence{}, fmt.Errorf("replying to comment %d of %s: %w", id, ref, err)
	}
	var c reviewComment
	if err := json.Unmarshal(resp.Body, &c); err != nil || c.ID == 0 {
		return src.Evidence{}, fmt.Errorf("%w on %s: GitHub's answer names no comment", src.ErrMayHavePosted, ref)
	}
	return replyEvidence(c), nil
}

// verifyReply looks for the viewer's review comment with d's body since
// startedAt in reply to d's target, or to the comment that starts its
// thread.
func (p *Publisher) verifyReply(ctx context.Context, d src.Draft, startedAt time.Time) (src.Evidence, bool, error) {
	ref, id, body, err := p.prepareReply(ctx, d)
	if err != nil {
		return src.Evidence{}, false, err
	}
	viewer, err := p.viewer(ctx)
	if err != nil {
		return src.Evidence{}, false, err
	}
	target, err := p.reviewComment(ctx, ref, id)
	if err != nil {
		return src.Evidence{}, false, err
	}
	parents := []int64{id, rootOf(target)}
	from := startedAt.Add(-clockSkew)
	q := url.Values{"since": {from.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	for page := 1; page <= verifyPages; page++ {
		q.Set("page", strconv.Itoa(page))
		resp, err := p.GH.REST(ctx, api.Request{Path: pullPath(ref) + "/comments?" + q.Encode()})
		if err != nil {
			return src.Evidence{}, false, fmt.Errorf("listing the review comments of %s: %w", ref, err)
		}
		var cs []reviewComment
		if err := json.Unmarshal(resp.Body, &cs); err != nil {
			return src.Evidence{}, false, fmt.Errorf("listing the review comments of %s: %w", ref, err)
		}
		for _, c := range cs {
			if strings.EqualFold(c.User.Login, viewer) && slices.Contains(parents, c.InReplyTo) && !c.CreatedAt.Before(from) && sameBody(c.Body, body) {
				return replyEvidence(c), true, nil
			}
		}
		if len(cs) < 100 {
			break
		}
	}
	return src.Evidence{}, false, nil
}

func replyEvidence(c reviewComment) src.Evidence {
	return src.Evidence{Ref: strconv.FormatInt(c.ID, 10), Permalink: c.HTMLURL, At: c.CreatedAt}
}

func rootOf(c reviewComment) int64 {
	if c.InReplyTo != 0 {
		return c.InReplyTo
	}
	return c.ID
}

// reviewComment is review comment id, refused when it is not one of ref's.
func (p *Publisher) reviewComment(ctx context.Context, ref pr.Ref, id int64) (reviewComment, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/comments/%d", url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), id)
	resp, err := p.GH.REST(ctx, api.Request{Path: path})
	if err != nil {
		return reviewComment{}, fmt.Errorf("reading review comment %d of %s: %w", id, ref, err)
	}
	var c reviewComment
	if err := json.Unmarshal(resp.Body, &c); err != nil || c.ID != id {
		return reviewComment{}, fmt.Errorf("reading review comment %d of %s: GitHub's answer names another", id, ref)
	}
	if !ownPull(ref, c.PullRequestURL) {
		return reviewComment{}, fmt.Errorf("review comment %d is not one of %s", id, ref)
	}
	return c, nil
}

func prepareResolve(d src.Draft) (pr.Ref, int64, error) {
	ref, err := unitRef(d)
	if err != nil {
		return pr.Ref{}, 0, err
	}
	id, err := CommentTarget(d.Target)
	if err != nil {
		return pr.Ref{}, 0, err
	}
	return ref, id, noPayload(d)
}

const threadsQuery = `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $repo) { pullRequest(number: $number) {
    reviewThreads(first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { id isResolved comments(first: 1) { nodes { databaseId } } } }
  } }
}`

type thread struct {
	id       string
	resolved bool
	// comment is the comment the draft names, whose permalink is the
	// thread's.
	comment reviewComment
}

// thread is the review thread of ref the review comment id is in: GitHub's
// REST names a comment's thread only by its first comment, and GraphQL
// names the thread's node id that resolving takes.
func (p *Publisher) thread(ctx context.Context, ref pr.Ref, id int64) (thread, error) {
	if p.Threads == nil {
		return thread{}, errors.New("github: no GraphQL client to read review threads")
	}
	c, err := p.reviewComment(ctx, ref, id)
	if err != nil {
		return thread{}, err
	}
	root := rootOf(c)
	vars := map[string]any{"owner": ref.Owner, "repo": ref.Repo, "number": ref.Number, "after": nil}
	for range threadPages {
		data, errs, err := p.Threads.GraphQL(ctx, threadsQuery, vars)
		if err != nil {
			return thread{}, fmt.Errorf("reading the review threads of %s: %w", ref, err)
		}
		if msg := errs["repository"]; msg != "" {
			return thread{}, fmt.Errorf("reading the review threads of %s: %s", ref, msg)
		}
		var repo struct {
			PullRequest *struct {
				ReviewThreads struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments   struct {
							Nodes []struct {
								DatabaseID int64 `json:"databaseId"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		}
		if err := json.Unmarshal(data["repository"], &repo); err != nil || repo.PullRequest == nil {
			return thread{}, fmt.Errorf("reading the review threads of %s: GitHub's answer names no pull request", ref)
		}
		ts := repo.PullRequest.ReviewThreads
		for _, n := range ts.Nodes {
			if len(n.Comments.Nodes) > 0 && n.Comments.Nodes[0].DatabaseID == root {
				return thread{id: n.ID, resolved: n.IsResolved, comment: c}, nil
			}
		}
		if !ts.PageInfo.HasNextPage {
			break
		}
		vars["after"] = ts.PageInfo.EndCursor
	}
	return thread{}, fmt.Errorf("review comment %d is in no review thread of %s", id, ref)
}

// publishResolve resolves the review thread of the comment d's target
// names; a thread already resolved is left as it is.
func (p *Publisher) publishResolve(ctx context.Context, d src.Draft) (src.Evidence, error) {
	ref, id, err := prepareResolve(d)
	if err != nil {
		return src.Evidence{}, err
	}
	t, err := p.thread(ctx, ref, id)
	if err != nil {
		return src.Evidence{}, err
	}
	if !t.resolved {
		if err := p.Threads.ResolveReviewThread(ctx, t.id); err != nil {
			if written(err) {
				return src.Evidence{}, fmt.Errorf("%w on %s: %w", src.ErrMayHavePosted, ref, err)
			}
			return src.Evidence{}, fmt.Errorf("resolving the thread of comment %d of %s: %w", id, ref, err)
		}
	}
	return p.resolvedEvidence(t), nil
}

// verifyResolve says whether the thread d resolves is resolved.
func (p *Publisher) verifyResolve(ctx context.Context, d src.Draft) (src.Evidence, bool, error) {
	ref, id, err := prepareResolve(d)
	if err != nil {
		return src.Evidence{}, false, err
	}
	t, err := p.thread(ctx, ref, id)
	if err != nil || !t.resolved {
		return src.Evidence{}, false, err
	}
	return p.resolvedEvidence(t), true, nil
}

func (p *Publisher) resolvedEvidence(t thread) src.Evidence {
	return src.Evidence{Ref: t.id, Permalink: t.comment.HTMLURL, At: p.Now()}
}

func pullPath(ref pr.Ref) string {
	return fmt.Sprintf("repos/%s/%s/pulls/%d", url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), ref.Number)
}
