package source_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// publishing is a source whose Publish fails as the draft's text says:
// "posted" after its post may have left, "refused" before, "hang" until
// the caller gives up.
type publishing struct {
	*sourcetest.Stub
	started chan struct{}
}

func (p publishing) Validate(context.Context, source.Draft) error { return nil }

func (p publishing) Where(context.Context, source.Draft) (string, error) { return "", nil }

func (p publishing) Place(context.Context, source.Draft) (string, error) { return "", nil }

func (p publishing) Verify(context.Context, source.Draft, time.Time) (source.Evidence, bool, error) {
	return source.Evidence{}, false, nil
}

func (p publishing) Publish(ctx context.Context, d source.Draft) (source.Evidence, error) {
	switch d.Text {
	case "posted":
		return source.Evidence{}, fmt.Errorf("%w on acme/api#12: HTTP 502", source.ErrMayHavePosted)
	case "refused":
		return source.Evidence{}, errors.New("commenting on acme/api#12: HTTP 422")
	case "unavailable":
		return source.Evidence{}, fmt.Errorf("budget low: %w", source.ErrUnavailable)
	}
	close(p.started)
	<-ctx.Done()
	return source.Evidence{}, ctx.Err()
}

func TestAPublishThatMayHavePostedSaysSoAcrossTheWire(t *testing.T) {
	t.Parallel()
	p := publishing{Stub: sourcetest.NewStub(), started: make(chan struct{})}
	c := sourcetest.Launch(t, "pulls", p, (&recorder{}).host())
	ctx := context.Background()
	_, err := c.Publish(ctx, source.Draft{Unit: pr, Text: "posted"})
	if !errors.Is(err, source.ErrMayHavePosted) || !strings.HasPrefix(err.Error(), "may have been posted on acme/api#12") {
		t.Fatalf("posted: %v", err)
	}
	for _, text := range []string{"refused", "unavailable"} {
		if _, err := c.Publish(ctx, source.Draft{Unit: pr, Text: text}); err == nil || errors.Is(err, source.ErrMayHavePosted) || errors.Is(err, source.ErrNoAnswer) {
			t.Fatalf("%s: %v", text, err)
		}
	}
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := c.Publish(hctx, source.Draft{Unit: pr, Text: "hang"})
		done <- err
	}()
	<-p.started
	cancel()
	if err := <-done; !errors.Is(err, source.ErrNoAnswer) || !errors.Is(err, context.Canceled) {
		t.Fatalf("a call given up on: %v", err)
	}
}
