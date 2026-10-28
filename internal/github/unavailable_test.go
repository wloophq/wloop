package github

import (
	"errors"
	"fmt"
	"testing"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestARateLimitOrAnUnreachableGitHubIsTheSourceUnavailable(t *testing.T) {
	for _, err := range []error{ErrRateLimited, ErrUnavailable, &RateLimitedError{Resource: "core"}, fmt.Errorf("reading: %w", ErrUnavailable)} {
		if !errors.Is(err, src.ErrUnavailable) {
			t.Errorf("%v is not the source unavailable", err)
		}
	}
	if !errors.Is(&RateLimitedError{}, ErrRateLimited) || errors.Is(ErrRateLimited, ErrUnavailable) {
		t.Error("the rate limit lost its own name")
	}
	for _, err := range []error{ErrUnauthorized, ErrEmpty} {
		if errors.Is(err, src.ErrUnavailable) {
			t.Errorf("%v is the source unavailable", err)
		}
	}
}
