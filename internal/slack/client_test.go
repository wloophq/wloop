package slack_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/slacktest"
)

const fakeToken = "xoxp-0000-fake-user-token"

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func newClient(t *testing.T) (*slack.Client, *slacktest.Server) {
	t.Helper()
	fake := slacktest.New(t)
	return &slack.Client{BaseURL: fake.URL, Tokens: staticToken(fakeToken)}, fake
}

func TestCallReturnsAPIError(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	fake.Handle("conversations.info", func(url.Values) any {
		return map[string]any{"ok": false, "error": "channel_not_found"}
	})
	err := c.Call(context.Background(), "conversations.info", url.Values{"channel": {"C000"}}, nil)
	var apiErr *slack.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != "channel_not_found" || apiErr.Method != "conversations.info" {
		t.Fatalf("APIError = %+v", apiErr)
	}
	if got := err.Error(); got != "slack conversations.info: channel_not_found" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestCallDecodesTheBody(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	fake.Handle("chat.getPermalink", func(url.Values) any {
		return map[string]any{"permalink": "https://example.slack.com/archives/C000/p1"}
	})
	var out struct{ Permalink string }
	if err := c.Call(context.Background(), "chat.getPermalink", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Permalink == "" {
		t.Fatal("permalink not decoded")
	}
}

func TestMissingScopeNamesTheScope(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	fake.Handle("search.messages", func(url.Values) any {
		return map[string]any{"ok": false, "error": "missing_scope", "needed": "search:read", "provided": "chat:write,users:read"}
	})
	err := c.Call(context.Background(), "search.messages", nil, nil)
	var apiErr *slack.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Code != "missing_scope" || apiErr.Needed != "search:read" || apiErr.Provided != "chat:write,users:read" {
		t.Fatalf("APIError = %+v", apiErr)
	}
	if !strings.Contains(err.Error(), "search:read") {
		t.Fatalf("Error() = %q, want the needed scope", err)
	}
}

func TestErrorsNeverHoldTheToken(t *testing.T) {
	t.Parallel()
	const secretArg = "xoxb-0000-fake-arg-secret"
	args := url.Values{"token": {secretArg}, "text": {"private words"}}
	cases := map[string]func(*slacktest.Server) any{
		"api error": func(*slacktest.Server) any {
			return map[string]any{"ok": false, "error": "invalid_auth", "token": fakeToken}
		},
		"http error": func(*slacktest.Server) any {
			return slacktest.Response{Status: 500, Body: "internal " + fakeToken + " " + secretArg}
		},
		"decode error": func(*slacktest.Server) any {
			return fakeToken + " not json " + secretArg
		},
		"transport error": func(s *slacktest.Server) any {
			s.Close()
			return nil
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			c, fake := newClient(t)
			fake.Handle("chat.postMessage", func(url.Values) any { return answer(fake) })
			if name == "transport error" {
				answer(fake)
			}
			err := c.Call(context.Background(), "chat.postMessage", args, nil)
			if err == nil {
				t.Fatal("want an error")
			}
			assertClean(t, err.Error(), args)
		})
	}
	t.Run("bad token header", func(t *testing.T) {
		fake := slacktest.New(t)
		c := &slack.Client{BaseURL: fake.URL, Tokens: staticToken(fakeToken + "\n")}
		err := c.Call(context.Background(), "chat.postMessage", args, nil)
		if err == nil {
			t.Fatal("want an error")
		}
		assertClean(t, err.Error(), args)
	})
}

func assertClean(t *testing.T, msg string, args url.Values) {
	t.Helper()
	for _, bad := range []string{fakeToken, "xoxp-", "xoxb-", args.Encode(), "private words", "private+words"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("error %q holds %q", msg, bad)
		}
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	var mu sync.Mutex
	var limited []string
	c.OnRateLimited = func(m string) { mu.Lock(); limited = append(limited, m); mu.Unlock() }
	fake.Handle("conversations.replies", func(url.Values) any { return map[string]any{"messages": []any{}} })
	fake.RateLimit("conversations.replies", 1, 1)

	start := time.Now()
	if err := c.Call(context.Background(), "conversations.replies", url.Values{"channel": {"C000"}}, nil); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 900*time.Millisecond || took > 3*time.Second {
		t.Fatalf("took %s, want about 1s", took)
	}
	if n := len(fake.Calls("conversations.replies")); n != 2 {
		t.Fatalf("requests = %d, want 2", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(limited) != 1 || limited[0] != "conversations.replies" {
		t.Fatalf("OnRateLimited got %v", limited)
	}
}

func TestSecondRateLimitIsReturned(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	calls := 0
	c.OnRateLimited = func(string) { calls++ }
	fake.Handle("conversations.history", func(url.Values) any { return nil })
	fake.RateLimit("conversations.history", 1, 5)

	err := c.Call(context.Background(), "conversations.history", nil, nil)
	var apiErr *slack.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "ratelimited" {
		t.Fatalf("err = %v, want ratelimited", err)
	}
	if n := len(fake.Calls("conversations.history")); n != 2 {
		t.Fatalf("requests = %d, want 2", n)
	}
	if calls != 2 {
		t.Fatalf("OnRateLimited calls = %d, want 2", calls)
	}
}

func TestRetryWaitStopsWithTheContext(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	fake.RateLimit("search.messages", 30, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Call(ctx, "search.messages", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("wait ignored the context")
	}
}

func TestLimiterLowDuringRetryAfter(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	c.Limits = &slack.Limiter{}
	fake.RateLimit("conversations.replies", 1, 2)
	if c.Limits.Low("conversations.replies") {
		t.Fatal("Low before any call")
	}
	_ = c.Call(context.Background(), "conversations.replies", nil, nil)
	if !c.Limits.Low("conversations.replies") {
		t.Fatal("Low = false inside the Retry-After window")
	}
	if c.Limits.Low("conversations.history") {
		t.Fatal("the window leaked to another method")
	}
	time.Sleep(1100 * time.Millisecond)
	if c.Limits.Low("conversations.replies") {
		t.Fatal("Low = true after the window")
	}
}

func TestLimiterPacesByTier(t *testing.T) {
	t.Parallel()
	l := &slack.Limiter{PerMinute: map[string]int{"search.messages": 3}}
	ctx := context.Background()
	for range 3 {
		if l.Low("search.messages") {
			t.Fatal("Low before the budget is spent")
		}
		if err := l.Wait(ctx, "search.messages"); err != nil {
			t.Fatal(err)
		}
	}
	if !l.Low("search.messages") {
		t.Fatal("Low = false with the minute's budget spent")
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := l.Wait(short, "search.messages"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want it to block past the budget", err)
	}
}

func TestRequestLogIsMethodOnly(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	type entry struct {
		method string
		status int
	}
	var logged []entry
	c.RequestLog = func(m string, s int) { logged = append(logged, entry{m, s}) }
	fake.Handle("auth.test", func(url.Values) any { return nil })
	fake.RateLimit("chat.postMessage", 0, 1)
	fake.Handle("chat.postMessage", func(url.Values) any { return map[string]any{"channel": "C000", "ts": "1.2"} })

	ctx := context.Background()
	_ = c.Call(ctx, "auth.test", nil, nil)
	_ = c.Call(ctx, "chat.postMessage", url.Values{"channel": {"C000"}, "text": {"hi"}}, nil)
	want := []entry{{"auth.test", 200}, {"chat.postMessage", 429}, {"chat.postMessage", 200}}
	if len(logged) != len(want) {
		t.Fatalf("logged %v, want %v", logged, want)
	}
	for i := range want {
		if logged[i] != want[i] {
			t.Fatalf("logged %v, want %v", logged, want)
		}
		if strings.ContainsAny(logged[i].method, "?=&/ ") {
			t.Fatalf("logged method %q holds more than the method", logged[i].method)
		}
	}
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	t.Parallel()
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var reached atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached.Add(1)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer target.Close()
			redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+r.URL.Path, code)
			}))
			defer redirect.Close()
			c := &slack.Client{BaseURL: redirect.URL + "/api/", Tokens: staticToken(fakeToken)}
			args := url.Values{"refresh_token": {"xoxe-1-fake-refresh"}}
			err := c.Call(context.Background(), "oauth.v2.access", args, nil)
			if err == nil || !strings.Contains(err.Error(), strconv.Itoa(code)) {
				t.Fatalf("err = %v, want HTTP %d", err, code)
			}
			assertClean(t, err.Error(), args)
			if n := reached.Load(); n != 0 {
				t.Fatalf("redirect target got %d requests, want none", n)
			}
		})
	}
}

func TestOKFalseRatelimitedIsARateLimit(t *testing.T) {
	t.Parallel()
	c, fake := newClient(t)
	c.Limits = &slack.Limiter{}
	var limited atomic.Int32
	c.OnRateLimited = func(string) { limited.Add(1) }
	var n atomic.Int32
	fake.Handle("conversations.replies", func(url.Values) any {
		if n.Add(1) == 1 {
			return slacktest.Response{Header: http.Header{"Retry-After": {"1"}}, Body: map[string]any{"ok": false, "error": "ratelimited"}}
		}
		return map[string]any{"messages": []any{}}
	})
	go func() {
		time.Sleep(300 * time.Millisecond)
		if !c.Limits.Low("conversations.replies") {
			t.Error("Low = false inside the window of an ok:false ratelimited answer")
		}
	}()
	if err := c.Call(context.Background(), "conversations.replies", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.Calls("conversations.replies")); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if limited.Load() != 1 {
		t.Fatalf("OnRateLimited calls = %d, want 1", limited.Load())
	}
}
