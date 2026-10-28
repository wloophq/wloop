package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// BriefFilesMax bounds the changed files BriefFacts lists.
const BriefFilesMax = 300

const briefFilesPage = 100

// BriefFacts is what a brief reads of a pull request besides its diff.
type BriefFacts struct {
	Title, Body string
	Files       []BriefFile
	// MoreFiles is how many changed files are past BriefFilesMax.
	MoreFiles int
	// Issue is the first issue the pull request closes, nil when none.
	Issue *BriefIssue
}

type BriefFile struct {
	Path                 string
	Additions, Deletions int
}

type BriefIssue struct{ Title, Body string }

type briefFilesPayload struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []BriefFile `json:"nodes"`
}

// BriefFacts reads ref's title, body, first closing issue and changed files
// in one GraphQL call, and one more per further 100 files up to
// BriefFilesMax.
func (c *Client) BriefFacts(ctx context.Context, ref Ref) (BriefFacts, error) {
	var f BriefFacts
	var changed int
	cursor := ""
	for len(f.Files) < BriefFilesMax {
		first := min(briefFilesPage, BriefFilesMax-len(f.Files))
		after := ""
		if cursor != "" {
			after = ", after: " + lit(cursor)
		}
		head := ""
		if cursor == "" {
			head = "title body changedFiles closingIssuesReferences(first: 1) { nodes { title body } }"
		}
		query := fmt.Sprintf(`query {
  %s
  pr: repository(owner: %s, name: %s) { pullRequest(number: %d) {
    %s
    files(first: %d%s) { pageInfo { hasNextPage endCursor } nodes { path additions deletions } }
  } }
}`, rateLimitSelection, lit(ref.Owner), lit(ref.Repo), ref.Number, head, first, after)
		data, aliasErrs, err := c.query(ctx, query)
		if err != nil {
			return BriefFacts{}, err
		}
		if msg := aliasErrs["pr"]; msg != "" {
			return BriefFacts{}, fmt.Errorf("%s: %s", ref, msg)
		}
		var res struct {
			PullRequest *struct {
				Title        string `json:"title"`
				Body         string `json:"body"`
				ChangedFiles int    `json:"changedFiles"`
				Closing      struct {
					Nodes []BriefIssue `json:"nodes"`
				} `json:"closingIssuesReferences"`
				Files briefFilesPayload `json:"files"`
			} `json:"pullRequest"`
		}
		if err := json.Unmarshal(data["pr"], &res); err != nil {
			return BriefFacts{}, fmt.Errorf("%s: %w", ref, err)
		}
		p := res.PullRequest
		if p == nil {
			return BriefFacts{}, fmt.Errorf("%s: no such pull request", ref)
		}
		if cursor == "" {
			f.Title, f.Body, changed = p.Title, p.Body, p.ChangedFiles
			if len(p.Closing.Nodes) > 0 {
				f.Issue = &p.Closing.Nodes[0]
			}
		}
		f.Files = append(f.Files, p.Files.Nodes...)
		if !p.Files.PageInfo.HasNextPage || p.Files.PageInfo.EndCursor == "" || len(p.Files.Nodes) == 0 {
			return f, nil
		}
		cursor = p.Files.PageInfo.EndCursor
	}
	f.MoreFiles = max(changed-len(f.Files), 0)
	return f, nil
}

// Diff reads ref's diff, at most limit bytes when limit > 0: it reads
// limit+1 bytes and closes the response, with cut true when there were more.
func (c *Client) Diff(ctx context.Context, ref Ref, limit int) (diff []byte, cut bool, err error) {
	resp, err := c.REST(ctx, Request{
		Method:   http.MethodGet,
		Path:     fmt.Sprintf("repos/%s/%s/pulls/%d", ref.Owner, ref.Repo, ref.Number),
		Accept:   "application/vnd.github.diff",
		MaxBytes: int64(limit),
	})
	if err != nil {
		return nil, false, err
	}
	return resp.Body, resp.Truncated, nil
}
