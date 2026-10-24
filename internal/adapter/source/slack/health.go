package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/ingest"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/socket"
)

// Health thresholds (#slack-health).
const (
	SocketDownWarnAfter = 5 * time.Minute
	MissedWarnAt        = 5
	GrantWarnDays       = 25
	// LoginRecheck is how long a found login is trusted before the Keychain
	// is read again; a call that finds the login gone forgets it at once.
	LoginRecheck = 5 * time.Minute
)

// login is what the module knows of the stored login, never a token.
type login struct {
	Team, User       string
	Expires, Granted time.Time
}

// logins caches the Keychain's answer that both tokens are stored, so the
// job every minute and health spawn no security process each time. A
// missing login is never cached: a login from the CLI is seen on the next check.
type logins struct {
	mu      sync.Mutex
	found   login
	at      time.Time
	skip    string
	checked bool
}

// get is the stored login, read from the Keychain unless a found one is
// younger than LoginRecheck; skip is SkipNotLoggedIn when a token is absent.
func (l *logins) get(tokens *auth.Store, now time.Time) (login, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.at.IsZero() && now.Sub(l.at) < LoginRecheck {
		return l.found, "", nil
	}
	found, skip, err := readLogin(tokens)
	if err != nil {
		return login{}, "", err
	}
	l.checked, l.skip, l.found, l.at = true, skip, found, time.Time{}
	if skip == "" {
		l.at = now
	}
	return found, skip, nil
}

func readLogin(tokens *auth.Store) (login, string, error) {
	tok, err := tokens.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return login{}, SkipNotLoggedIn, nil
	}
	if err != nil {
		return login{}, "", err
	}
	if _, err := tokens.AppToken(); errors.Is(err, auth.ErrNotLoggedIn) {
		return login{}, SkipNotLoggedIn, nil
	} else if err != nil {
		return login{}, "", err
	}
	if tok.Team == "" || tok.User == "" {
		return login{}, "", errors.New("slack: the stored token names no team or user; run slack login again")
	}
	return login{Team: tok.Team, User: tok.User, Expires: tok.Expires, Granted: tok.Granted}, "", nil
}

// last is the latest answer without reading the Keychain; checked is false
// before the first read.
func (l *logins) last() (found login, skip string, checked bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.found, l.skip, l.checked
}

// forget drops a found login when err says it is gone.
func (l *logins) forget(err error) {
	if !loggedOut(err) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.at = time.Time{}
}

func loggedOut(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, auth.ErrNotLoggedIn) || errors.Is(err, socket.ErrInvalidAuth) {
		return true
	}
	var api *slackapi.APIError
	if !errors.As(err, &api) {
		return false
	}
	switch api.Code {
	case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive":
		return true
	}
	return false
}

// after is the ingestion job's outcome; a pass that finds the login gone forgets it.
func (p *plugin) after(_ context.Context, rep ingest.Report, err error) (string, string, error) {
	p.logins.forget(err)
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("%d discovered, %d read", rep.Discovered, rep.Moved), "", nil
}

type tokenDetail struct {
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	GrantedAt    string    `json:"granted_at"`
	GrantAgeDays int       `json:"grant_age_days"`
}

type healthDetail struct {
	snapshot
	Token *tokenDetail `json:"token,omitempty"`
}

// health is the module's Health (#slack-health): the metrics' snapshot,
// the stored login's dates and the warnings. It reads no Keychain.
func (p *plugin) health(context.Context) (api.SourceHealth, error) {
	now := p.now()
	h := api.SourceHealth{Provider: dslack.Provider, Warnings: []string{}}
	d := healthDetail{snapshot: p.slack.Metrics.snapshot()}
	found, skip, checked := p.logins.last()
	switch {
	case !p.cfg.Configured():
	case skip == SkipNotLoggedIn:
		h.Warnings = append(h.Warnings, "slack: not logged in; run "+brand.Command+" slack login")
	case checked:
		d.Token = &tokenDetail{ExpiresAt: found.Expires, GrantedAt: "unknown"}
		if !found.Granted.IsZero() {
			days := int(now.Sub(found.Granted) / (24 * time.Hour))
			d.Token.GrantedAt, d.Token.GrantAgeDays = found.Granted.UTC().Format(time.RFC3339), days
			if days >= GrantWarnDays {
				h.Warnings = append(h.Warnings, fmt.Sprintf("slack: the login is %d days old; log in again before day 30 (%s slack login)",
					days, brand.Command))
			}
		}
		if w := p.socketWarning(d.snapshot, now); w != "" {
			h.Warnings = append(h.Warnings, w)
		}
	}
	if d.MissedWhileConnected >= MissedWarnAt {
		h.Warnings = append(h.Warnings, fmt.Sprintf("slack: %d messages in the last hour reached only the catch-up while the socket was connected; the event subscription may be off",
			d.MissedWhileConnected))
	}
	h.Warnings = append(h.Warnings, d.Warnings...)
	raw, err := json.Marshal(d)
	if err != nil {
		return h, err
	}
	h.Detail = raw
	return h, nil
}

// socketWarning warns when the socket is not connected for more than
// SocketDownWarnAfter; with no state yet, since the module started.
func (p *plugin) socketWarning(s snapshot, now time.Time) string {
	if !p.cfg.Ingest.SocketOn() || s.Socket.State == "connected" {
		return ""
	}
	since := s.DownSince
	if since.IsZero() {
		since = p.started
	}
	if now.Sub(since) <= SocketDownWarnAfter {
		return ""
	}
	return fmt.Sprintf("slack: the socket has been %s for %s; new messages arrive only by polling", s.Socket.State,
		now.Sub(since).Truncate(time.Minute))
}
