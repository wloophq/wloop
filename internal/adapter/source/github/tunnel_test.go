package ghsource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/github"
)

func TestAForwardThatTimesOutAfterThePluginPostedIsNotPostedAgain(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
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
	}, func() time.Time { return time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC) })

	_, err := gh.REST(context.Background(), github.Request{Method: http.MethodPost, Path: "repos/acme/api/issues/12/comments", Body: []byte(`{"body":"hello"}`), Mutation: true})
	var se *github.StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadGateway {
		t.Fatalf("error %v, want the plugin's 502", err)
	}
	if forwards != 1 || posts.Load() != 1 {
		t.Fatalf("%d forwards, %d POSTs; want one of each", forwards, posts.Load())
	}
}
