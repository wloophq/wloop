package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const resolveThreadMutation = `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id isResolved } } }`

// ResolveReviewThread resolves review thread threadID, by its GraphQL node id, in an exchange of its own:
// the one mutation the client sends, which GraphQL refuses. Like a REST mutation, it is never retried once
// its request was written.
func (c *Client) ResolveReviewThread(ctx context.Context, threadID string) error {
	if threadID == "" {
		return errors.New("graphql: resolving a review thread needs its id")
	}
	body, err := json.Marshal(map[string]any{"query": resolveThreadMutation, "variables": map[string]any{"id": threadID}})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, exchange{op: "graphql", res: ResourceGraphQL, attempts: c.retry.Attempts, mutation: true, build: func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphql, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.GetBody = nil
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		return req, nil
	}})
	if err != nil {
		return err
	}
	if resp.Status != http.StatusOK {
		return statusError(resp)
	}
	if emptyAnswer(resp) {
		return fmt.Errorf("resolving review thread %s: %w", threadID, ErrEmpty)
	}
	var out struct {
		Data struct {
			Resolve *struct {
				Thread struct {
					IsResolved bool `json:"isResolved"`
				} `json:"thread"`
			} `json:"resolveReviewThread"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return fmt.Errorf("resolving review thread %s: unexpected response: %.200s", threadID, resp.Body)
	}
	if len(out.Errors) > 0 {
		return fmt.Errorf("resolving review thread %s: %w", threadID, c.queryError(resp, out.Errors[0]))
	}
	if out.Data.Resolve == nil || !out.Data.Resolve.Thread.IsResolved {
		return fmt.Errorf("resolving review thread %s: GitHub answered it is not resolved", threadID)
	}
	return nil
}
