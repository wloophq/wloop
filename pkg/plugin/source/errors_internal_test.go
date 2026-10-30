package source

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAFailureNoHandlerAnsweredIsNoAnswer(t *testing.T) {
	t.Parallel()
	for _, c := range []codes.Code{codes.Internal, codes.Unknown, codes.ResourceExhausted} {
		if err := fromStatus(status.Error(c, "stream reset")); !errors.Is(err, ErrNoAnswer) || !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: %v", c, err)
		}
	}
	if err := fromStatus(unsupported("publisher")); !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrNoAnswer) {
		t.Errorf("unsupported: %v", err)
	}
	if err := fromStatus(toStatus(errors.New("HTTP 422"))); err == nil || errors.Is(err, ErrNoAnswer) {
		t.Errorf("answered: %v", err)
	}
}
