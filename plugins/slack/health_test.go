package slack

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/socket"
)

func hello(at time.Time) socket.State {
	return socket.State{Kind: socket.StateHello, NumConnections: 1, At: at}
}

func (f *fixture) health(t *testing.T) (SourceHealth, map[string]any) {
	t.Helper()
	h, err := f.p.health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var detail map[string]any
	if err := json.Unmarshal(h.Detail, &detail); err != nil {
		t.Fatalf("detail %s: %v", h.Detail, err)
	}
	return h, detail
}

func warned(h SourceHealth, part string) bool {
	for _, w := range h.Warnings {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

// loginGranted stores both tokens with the grant time and lets the plugin read them.
func (f *fixture) loginGranted(t *testing.T, granted time.Time) {
	t.Helper()
	raw, _ := json.Marshal(auth.Token{Team: team, User: me, Access: fakeUserToken, Refresh: "xoxe-1-fakerefresh",
		Expires: f.now.Add(12 * time.Hour), Granted: granted})
	f.kc.Set(f.cfg.Service(), "current", raw)
	f.kc.Set(f.cfg.Service(), "app", []byte(fakeAppToken))
	if skip, err := f.p.ready(context.Background()); err != nil || skip != "" {
		t.Fatalf("ready: %q %v", skip, err)
	}
}

func TestMissedWhileConnectedWarns(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.p.slack.Metrics.Socket(hello(f.now))
	f.p.slack.Metrics.CatchUp("reconnect", 6, 4)
	if h, _ := f.health(t); warned(h, "subscription") {
		t.Fatalf("warned at 4: %v", h.Warnings)
	}
	f.p.slack.Metrics.CatchUp("reconnect", 1, 1)
	h, detail := f.health(t)
	if !warned(h, "subscription may be off") {
		t.Fatalf("warnings %v", h.Warnings)
	}
	if detail["missed_while_connected"] != 5.0 {
		t.Fatalf("detail %v", detail)
	}
	f.now = f.now.Add(61 * time.Minute)
	if h, _ := f.health(t); warned(h, "subscription") {
		t.Fatalf("still warned an hour later: %v", h.Warnings)
	}
}

func TestGrantAgeWarns(t *testing.T) {
	f := newFixture(t)
	f.loginGranted(t, f.now.Add(-25*24*time.Hour))
	h, detail := f.health(t)
	if !warned(h, "log in again before day 30") {
		t.Fatalf("warnings %v", h.Warnings)
	}
	tok := detail["token"].(map[string]any)
	if tok["grant_age_days"] != 25.0 || tok["granted_at"] != f.now.Add(-25*24*time.Hour).Format(time.RFC3339) || tok["expires_at"] == nil {
		t.Fatalf("token %v", tok)
	}
	f2 := newFixture(t)
	f2.loginGranted(t, f2.now.Add(-24*24*time.Hour))
	if h, _ := f2.health(t); warned(h, "log in again") {
		t.Fatalf("warned on day 24: %v", h.Warnings)
	}
}

func TestUnknownGrantDoesNotWarn(t *testing.T) {
	f := newFixture(t)
	f.loginGranted(t, time.Time{})
	h, detail := f.health(t)
	if warned(h, "log in again") {
		t.Fatalf("warnings %v", h.Warnings)
	}
	if tok := detail["token"].(map[string]any); tok["granted_at"] != "unknown" || tok["grant_age_days"] != 0.0 {
		t.Fatalf("token %v", tok)
	}
}

func TestRateLimitsAreCounted(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.searchPages([]any{})
	f.api.RateLimit("search.messages", 0, 1)
	if _, err := f.p.search(context.Background(), "reconnect"); err != nil {
		t.Fatal(err)
	}
	_, detail := f.health(t)
	if got := detail["rate_limited"].(map[string]any); got["search.messages"] != 1.0 {
		t.Fatalf("rate_limited %v", got)
	}
}

func TestWarnSourcesAreListed(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.p.slack.Metrics.Warn("extraction", func() []string { return []string{"slack extraction: breaker open"} })
	if h, _ := f.health(t); !warned(h, "breaker open") {
		t.Fatalf("warnings %v", h.Warnings)
	}
}

func TestSocketNeverUpWarns(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.now = f.now.Add(4 * time.Minute)
	if h, _ := f.health(t); warned(h, "socket") {
		t.Fatalf("warned at 4 min: %v", h.Warnings)
	}
	f.now = f.now.Add(2 * time.Minute)
	h, detail := f.health(t)
	if !warned(h, "socket") || detail["socket"].(map[string]any)["state"] != "down" {
		t.Fatalf("warnings %v, detail %v", h.Warnings, detail)
	}
	f.p.slack.Metrics.Socket(hello(f.now))
	if h, _ := f.health(t); warned(h, "socket") {
		t.Fatalf("warned while connected: %v", h.Warnings)
	}
	f.p.slack.Metrics.Socket(socket.State{Kind: socket.StateDropped, Reason: "read: EOF", At: f.now})
	f.now = f.now.Add(6 * time.Minute)
	if h, _ := f.health(t); !warned(h, "socket has been down") {
		t.Fatalf("no warning 6 min after a drop: %v", h.Warnings)
	}
}

func TestLoggedOutHealth(t *testing.T) {
	f := newFixture(t)
	if h, _ := f.health(t); len(h.Warnings) != 0 {
		t.Fatalf("before any check: %v", h.Warnings)
	}
	if skip, err := f.p.ready(context.Background()); err != nil || skip != SkipNotLoggedIn {
		t.Fatalf("ready: %q %v", skip, err)
	}
	f.now = f.now.Add(10 * time.Minute)
	h, detail := f.health(t)
	if !warned(h, "slack login") || warned(h, "socket") || detail["token"] != nil {
		t.Fatalf("warnings %v, detail %v", h.Warnings, detail)
	}
}

func TestHealthHoldsNoToken(t *testing.T) {
	f := newFixture(t)
	f.loginGranted(t, f.now.Add(-26*24*time.Hour))
	m := f.p.slack.Metrics
	m.Socket(socket.State{Kind: socket.StateDialing, At: f.now})
	m.Socket(hello(f.now))
	m.Socket(socket.State{Kind: socket.StateDisconnect, Reason: "refresh_requested", At: f.now})
	m.Socket(socket.State{Kind: socket.StateDropped, Reason: socket.RedactURL("wss://wss.example/link/?ticket=t-1&app_id=A0001"), At: f.now})
	m.RateLimited("search.messages")
	m.CatchUp("reconnect", 6, 6)
	f.now = f.now.Add(10 * time.Minute)
	h, _ := f.health(t)
	raw, _ := json.Marshal(h)
	for _, secret := range []string{fakeUserToken, fakeAppToken, "xoxe-", "xox", "xapp", "ticket", "?"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("health holds %q: %s", secret, raw)
		}
	}
	if len(h.Warnings) < 3 {
		t.Fatalf("warnings %v", h.Warnings)
	}
}

type countingKeychain struct {
	auth.Keychain
	gets atomic.Int64
}

func (k *countingKeychain) Get(service, account string) ([]byte, error) {
	k.gets.Add(1)
	return k.Keychain.Get(service, account)
}

func TestLoginIsReadOncePerRecheck(t *testing.T) {
	f := newFixture(t)
	kc := &countingKeychain{Keychain: f.kc}
	f.slack.Tokens.Keychain = kc
	f.searchPages([]any{})
	f.login(t)
	ctx := context.Background()
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	reads := kc.gets.Load()
	for range 3 {
		f.now = f.now.Add(time.Minute)
		if _, _, err := f.p.ingestJob(ctx); err != nil {
			t.Fatal(err)
		}
		f.health(t)
	}
	if got := kc.gets.Load() - reads; got != 0 {
		t.Fatalf("%d Keychain reads within %v", got, LoginRecheck)
	}
	f.now = f.now.Add(LoginRecheck)
	if _, _, err := f.p.ingestJob(ctx); err != nil {
		t.Fatal(err)
	}
	if got := kc.gets.Load() - reads; got != 2 {
		t.Fatalf("%d Keychain reads after %v", got, LoginRecheck)
	}
	f.kc.Delete(f.cfg.Service(), "current")
	f.p.logins.forget(auth.ErrNotLoggedIn)
	if summary, skipped, err := f.p.ingestJob(ctx); err != nil || skipped != SkipNotLoggedIn {
		t.Fatalf("after logout: %q %q %v", summary, skipped, err)
	}
}
