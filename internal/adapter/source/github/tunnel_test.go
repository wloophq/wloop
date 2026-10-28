package ghsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAForwardThatTimesOutAfterThePluginPostedIsNotPostedAgain(t *testing.T) {
	f, srv := newFakeGitHub(t)
	forwards := 0
	gh := PluginClient(func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		forwards++
		var x exchange
		if err := json.Unmarshal(args, &x); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, x.Method, srv.URL+"/"+x.Path, bytes.NewReader(x.Body))
		if err != nil {
			return nil, err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return nil, fmt.Errorf("the plugin's answer: %w", context.DeadlineExceeded)
	}, func() time.Time { return f.now })
	p := &Publisher{GH: gh, Events: &recordedEvents{}, Now: func() time.Time { return f.now }}

	_, err := p.Publish(context.Background(), commentDraft("hello"))
	if err == nil || !strings.HasPrefix(err.Error(), mayHaveBeenPosted) {
		t.Fatalf("error %v, want one that says the comment may have been posted", err)
	}
	if forwards != 1 || f.posts() != 1 || len(f.posted(issue12)) != 1 {
		t.Fatalf("%d forwards, %d POSTs, %d comments; want one of each", forwards, f.posts(), len(f.posted(issue12)))
	}
}
