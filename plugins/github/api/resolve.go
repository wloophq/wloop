package api

import "context"

// ResolveReviewThread resolves review thread threadID, by its GraphQL node id, in an exchange of its own:
// the one mutation the client sends, which GraphQL refuses.
func (c *Client) ResolveReviewThread(ctx context.Context, threadID string) error {
	panic("not implemented: T5")
}
