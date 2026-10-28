package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/wloophq/wloop/plugins/slack/slackapi"
)

const (
	authorizeURL = "https://slack.com/oauth/v2/authorize"
	callbackPath = "/slack/callback"
	maxReason    = 64
)

type callback struct {
	code string
	err  error
}

// Run sets Granted = Now(), Scopes from the exchange.
func (l Login) Run(ctx context.Context) (Token, error) {
	if l.ClientID == "" || l.Client == nil || l.Open == nil {
		return Token{}, errors.New("slack login: client id, client and Open are required")
	}
	host := l.RedirectHost
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(l.Port)))
	if err != nil {
		return Token{}, fmt.Errorf("slack login: callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + callbackPath

	verifier := randomURLSafe(32)
	sum := sha256.Sum256([]byte(verifier))
	state := randomURLSafe(16)
	scopes := l.Scopes
	if scopes == "" {
		scopes = UserScopes
	}
	q := url.Values{
		"client_id":             {l.ClientID},
		"scope":                 {""},
		"user_scope":            {scopes},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}

	done := make(chan callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		res := readCallback(r.URL.Query(), state)
		if res.err != nil {
			http.Error(w, "Slack login failed: "+res.err.Error(), http.StatusBadRequest)
		} else {
			fmt.Fprintln(w, "Slack login received. You can close this tab.")
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(ln)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		// Shutdown only closes the listeners Serve has registered; Serve closes ln itself on return.
		<-served
	}()

	if err := l.Open(authorizeURL + "?" + q.Encode()); err != nil {
		return Token{}, fmt.Errorf("slack login: open the browser: %w", err)
	}

	var res callback
	select {
	case res = <-done:
	case <-ctx.Done():
		return Token{}, fmt.Errorf("slack login: waiting for the browser: %w", ctx.Err())
	}
	if res.err != nil {
		return Token{}, fmt.Errorf("slack login: %w", res.err)
	}

	resp, err := l.Client.OAuthAccess(ctx, slack.OAuthArgs{
		ClientID: l.ClientID, Code: res.code, CodeVerifier: verifier, RedirectURI: redirectURI,
	})
	if err != nil {
		return Token{}, fmt.Errorf("slack login: exchange: %w", err)
	}
	if resp.AccessToken == "" {
		return Token{}, errors.New("slack login: the exchange returned no user token")
	}
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	granted := now()
	tok := Token{
		Team: resp.Team, User: resp.User,
		Access: resp.AccessToken, Refresh: resp.RefreshToken,
		Granted: granted, Scopes: splitScopes(resp.Scope),
	}
	if resp.ExpiresIn > 0 {
		tok.Expires = granted.Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	return tok, nil
}

func readCallback(q url.Values, state string) callback {
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		return callback{err: errors.New("the callback state does not match this login")}
	}
	if reason := q.Get("error"); reason != "" {
		if len(reason) > maxReason {
			reason = reason[:maxReason]
		}
		return callback{err: fmt.Errorf("authorization refused: %q", reason)}
	}
	if q.Get("code") == "" {
		return callback{err: errors.New("the callback carries no code")}
	}
	return callback{code: q.Get("code")}
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
