package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
)

func exchangeHandler(srv *slacktest.Server) {
	srv.Handle("oauth.v2.access", func(url.Values) any {
		return map[string]any{
			"app_id": "A000",
			"team":   map[string]any{"id": "T000", "name": "Acme"},
			"authed_user": map[string]any{
				"id":            "U000",
				"scope":         "chat:write,search:read",
				"access_token":  "xoxe.xoxp-1-fakeaccess",
				"refresh_token": "xoxe-1-fakerefresh",
				"expires_in":    43200,
				"token_type":    "user",
			},
		}
	})
}

// browse answers the authorize URL the way a browser would after the user decides.
type browse func(authorize *url.URL) url.Values

func runLogin(t *testing.T, srv *slacktest.Server, answer browse) (Token, error, *url.URL) {
	t.Helper()
	var authorize *url.URL
	l := Login{
		ClientID:     "1.2",
		RedirectHost: "127.0.0.1",
		Port:         0,
		Client:       &slack.Client{BaseURL: srv.URL},
		Now:          func() time.Time { return t0 },
		Open: func(raw string) error {
			u, err := url.Parse(raw)
			if err != nil {
				return err
			}
			authorize = u
			go func() {
				q := answer(u)
				resp, err := http.Get(u.Query().Get("redirect_uri") + "?" + q.Encode())
				if err == nil {
					resp.Body.Close()
				}
			}()
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tok, err := l.Run(ctx)
	return tok, err, authorize
}

func approve(u *url.URL) url.Values {
	return url.Values{"code": {"thecode"}, "state": {u.Query().Get("state")}}
}

func TestLoginExchangesWithPKCE(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	tok, err, authorize := runLogin(t, srv, approve)
	if err != nil {
		t.Fatal(err)
	}
	q := authorize.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Fatalf("authorize query lacks PKCE: %v", q)
	}
	redirect := q.Get("redirect_uri")
	if !strings.HasPrefix(redirect, "http://127.0.0.1:") || !strings.HasSuffix(redirect, "/slack/callback") {
		t.Fatalf("redirect_uri = %q", redirect)
	}

	calls := srv.Calls("oauth.v2.access")
	if len(calls) != 1 {
		t.Fatalf("exchanges = %d", len(calls))
	}
	form := calls[0]
	if _, ok := form["client_secret"]; ok {
		t.Fatal("exchange sent a client_secret")
	}
	if form.Get("code") != "thecode" || form.Get("redirect_uri") != redirect || form.Get("client_id") != "1.2" ||
		form.Get("grant_type") != "authorization_code" {
		t.Fatalf("exchange form = %v", form)
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Fatal("code_verifier does not match code_challenge")
	}

	want := Token{
		Team: "T000", User: "U000",
		Access: "xoxe.xoxp-1-fakeaccess", Refresh: "xoxe-1-fakerefresh",
		Expires: t0.Add(43200 * time.Second), Granted: t0,
		Scopes: []string{"chat:write", "search:read"},
	}
	if !reflect.DeepEqual(tok, want) {
		t.Fatalf("token = %+v, want %+v", tok, want)
	}
}

func TestLoginAsksForTheUserScopes(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	_, err, authorize := runLogin(t, srv, approve)
	if err != nil {
		t.Fatal(err)
	}
	q := authorize.Query()
	if q.Get("user_scope") != UserScopes {
		t.Fatalf("user_scope = %q", q.Get("user_scope"))
	}
	if q.Get("scope") != "" {
		t.Fatalf("bot scope = %q, want none", q.Get("scope"))
	}
	if authorize.Scheme != "https" || authorize.Host != "slack.com" || authorize.Path != "/oauth/v2/authorize" {
		t.Fatalf("authorize URL = %s", authorize.Redacted())
	}
}

func TestLoginRefusesAForeignState(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	_, err, _ := runLogin(t, srv, func(*url.URL) url.Values {
		return url.Values{"code": {"thecode"}, "state": {"forged"}}
	})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("Run = %v, want a state error", err)
	}
	if n := len(srv.Calls("oauth.v2.access")); n != 0 {
		t.Fatalf("exchanged %d times on a foreign state", n)
	}
}

func TestLoginReportsDenial(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	_, err, _ := runLogin(t, srv, func(u *url.URL) url.Values {
		return url.Values{"error": {"access_denied"}, "state": {u.Query().Get("state")}}
	})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("Run = %v, want access_denied", err)
	}
	if n := len(srv.Calls("oauth.v2.access")); n != 0 {
		t.Fatalf("exchanged %d times after a denial", n)
	}
}

func TestLoginChecksStateBeforeDenial(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	_, err, _ := runLogin(t, srv, func(*url.URL) url.Values {
		return url.Values{"error": {"injected_text"}}
	})
	if err == nil || !strings.Contains(err.Error(), "state") || strings.Contains(err.Error(), "injected_text") {
		t.Fatalf("Run = %v, want a state error", err)
	}
}

func TestLoginFailsWhenTheExchangeFails(t *testing.T) {
	srv := slacktest.New(t)
	srv.Handle("oauth.v2.access", func(url.Values) any {
		return map[string]any{"ok": false, "error": "invalid_code"}
	})
	_, err, _ := runLogin(t, srv, approve)
	if err == nil || !strings.Contains(err.Error(), "invalid_code") {
		t.Fatalf("Run = %v", err)
	}
	assertNoToken(t, err.Error())
	if strings.Contains(err.Error(), "thecode") {
		t.Fatalf("error holds the code: %v", err)
	}
}

func TestLoginStopsOnCancel(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	l := Login{
		ClientID: "1.2", RedirectHost: "127.0.0.1", Port: 0,
		Client: &slack.Client{BaseURL: srv.URL},
		Open: func(raw string) error {
			u, _ := url.Parse(raw)
			opened <- u.Query().Get("redirect_uri")
			return nil
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.Run(ctx)
		done <- err
	}()
	redirect, _ := url.Parse(<-opened)
	conn, err := net.Dial("tcp", redirect.Host)
	if err != nil {
		t.Fatalf("listener not up: %v", err)
	}
	conn.Close()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if conn, err := net.Dial("tcp", redirect.Host); err == nil {
		conn.Close()
		t.Fatal("listener still accepts after cancel")
	}
}

func TestLoginIgnoresOtherPaths(t *testing.T) {
	srv := slacktest.New(t)
	exchangeHandler(srv)
	_, err, _ := runLogin(t, srv, func(u *url.URL) url.Values {
		base, _ := url.Parse(u.Query().Get("redirect_uri"))
		base.Path = "/favicon.ico"
		if resp, err := http.Get(base.String()); err == nil {
			resp.Body.Close()
		}
		return approve(u)
	})
	if err != nil {
		t.Fatalf("a request to another path broke the login: %v", err)
	}
}
