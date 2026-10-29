package client

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

// Review is the current Review.
func (c *Client) Review(ctx context.Context) (api.Review, error) {
	var r api.Review
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.ReviewPath, nil, &r)
	return r, err
}

// ReviewStepDone marks step done in the current Review.
func (c *Client) ReviewStepDone(ctx context.Context, step string) (api.Review, error) {
	var r api.Review
	err := c.do(ctx, ReadTimeout, http.MethodPost, api.ReviewStepPath(step), struct{}{}, &r)
	return r, err
}
