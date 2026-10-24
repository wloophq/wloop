package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/slack"
)

const (
	userAccount = "current"
	appAccount  = "app"

	refreshWindow      = 5 * time.Minute
	rotatedSaveRetries = 3
	rotatedSaveDelay   = 50 * time.Millisecond

	// cacheTTL bounds how long Token trusts its last Keychain read: each read
	// execs security, and another process may log out meanwhile.
	cacheTTL = 30 * time.Second

	unsavedRetryEvery = 5 * time.Second
)

// deadRefreshCodes mean the grant is gone and only a new login brings it back.
var deadRefreshCodes = []string{"invalid_refresh_token", "token_revoked", "token_expired", "account_inactive"}

// Load returns the user token; ErrNotLoggedIn when absent.
func (s *Store) Load() (Token, error) {
	if err := s.check(); err != nil {
		return Token{}, err
	}
	raw, err := s.Keychain.Get(s.Service, userAccount)
	if errors.Is(err, ErrNotFound) {
		return Token{}, fmt.Errorf("slack token %s: %w", s.Service, ErrNotLoggedIn)
	}
	if err != nil {
		return Token{}, err
	}
	var tok Token
	if err := json.Unmarshal(raw, &tok); err != nil {
		return Token{}, fmt.Errorf("slack token %s: the keychain item is not a token", s.Service)
	}
	if tok.Access == "" {
		return Token{}, fmt.Errorf("slack token %s: %w", s.Service, ErrNotLoggedIn)
	}
	return tok, nil
}

func (s *Store) Save(tok Token) error {
	if err := s.check(); err != nil {
		return err
	}
	raw, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	if err := s.Keychain.Set(s.Service, userAccount, raw); err != nil {
		return err
	}
	s.remember(tok)
	return nil
}

// Delete deletes the user token only.
func (s *Store) Delete() error {
	s.remember(Token{})
	return s.delete(userAccount)
}

// AppToken reads account "app"; ErrNotLoggedIn when absent.
func (s *Store) AppToken() (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	raw, err := s.Keychain.Get(s.Service, appAccount)
	if errors.Is(err, ErrNotFound) || (err == nil && len(raw) == 0) {
		return "", fmt.Errorf("slack app token %s: %w", s.Service, ErrNotLoggedIn)
	}
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// SaveAppToken refuses a value that is not an app-level token.
func (s *Store) SaveAppToken(token string) error {
	if err := s.check(); err != nil {
		return err
	}
	if !strings.HasPrefix(token, "xapp-") {
		return errors.New("slack app token: the value is not an app-level token")
	}
	return s.Keychain.Set(s.Service, appAccount, []byte(token))
}

func (s *Store) DeleteAppToken() error {
	return s.delete(appAccount)
}

func (s *Store) delete(account string) error {
	if err := s.check(); err != nil {
		return err
	}
	if err := s.Keychain.Delete(s.Service, account); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Token implements slack.TokenSource; refreshes within 5 min of expiry under an flock on LockPath.
func (s *Store) Token(ctx context.Context) (string, error) {
	s.retryUnsaved(ctx)
	tok, ok := s.recall()
	if !ok {
		var err error
		if tok, err = s.load(); err != nil {
			return "", err
		}
	}
	if !s.nearExpiry(tok) {
		return tok.Access, nil
	}
	path, err := s.lockPath()
	if err != nil {
		return "", err
	}
	unlock, err := lockFile(ctx, path)
	if err != nil {
		return s.keepOrFail(tok, fmt.Errorf("slack token %s: lock: %w", s.Service, err))
	}
	defer unlock()

	if tok, err = s.settleUnsaved(); err != nil {
		return "", err
	}
	if !s.nearExpiry(tok) {
		return tok.Access, nil
	}
	return s.refresh(ctx, tok)
}

func (s *Store) refresh(ctx context.Context, tok Token) (string, error) {
	if tok.Refresh == "" {
		return s.keepOrFail(tok, fmt.Errorf("slack token %s: expired and not refreshable: %w", s.Service, ErrNotLoggedIn))
	}
	if s.Client == nil {
		return s.keepOrFail(tok, fmt.Errorf("slack token %s: no client to refresh with", s.Service))
	}
	resp, err := s.Client.OAuthAccess(ctx, slack.OAuthArgs{ClientID: s.ClientID, RefreshToken: tok.Refresh})
	if err != nil {
		var apiErr *slack.APIError
		if errors.As(err, &apiErr) && slices.Contains(deadRefreshCodes, apiErr.Code) {
			return "", fmt.Errorf("slack token %s: refresh rejected (%s): %w", s.Service, apiErr.Code, ErrNotLoggedIn)
		}
		return s.keepOrFail(tok, fmt.Errorf("slack token %s: refresh: %w", s.Service, err))
	}
	if resp.AccessToken == "" {
		return s.keepOrFail(tok, fmt.Errorf("slack token %s: refresh returned no access token", s.Service))
	}

	next := tok
	next.Access = resp.AccessToken
	if resp.RefreshToken != "" {
		next.Refresh = resp.RefreshToken
	}
	next.Expires = time.Time{}
	if resp.ExpiresIn > 0 {
		next.Expires = s.now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	if resp.Scope != "" {
		next.Scopes = splitScopes(resp.Scope)
	}
	if resp.User != "" {
		next.User = resp.User
	}
	if resp.Team != "" {
		next.Team = resp.Team
	}
	s.saveRotated(next, tok.Refresh)
	return next.Access, nil
}

// saveRotated retries because Slack revokes the old refresh token shortly after
// rotation: a pair not saved now is a logout later. A pair it cannot save stays
// in memory, past the cache window, until retryUnsaved saves it.
func (s *Store) saveRotated(tok Token, staleRefresh string) {
	var err error
	for attempt := 0; attempt <= rotatedSaveRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(rotatedSaveDelay)
		}
		if err = s.Save(tok); err == nil {
			return
		}
	}
	s.mu.Lock()
	if !s.unsaved {
		s.staleRefresh = staleRefresh
	}
	s.cached, s.cachedAt = tok, time.Now()
	s.unsaved, s.lastRetry = true, time.Now()
	s.mu.Unlock()
	s.logf("error: slack token %s: saving the rotated token failed after %d retries, keeping it in memory: %v", s.Service, rotatedSaveRetries, err)
}

// retryUnsaved settles the unsaved pair under the refresh lock.
func (s *Store) retryUnsaved(ctx context.Context) {
	s.mu.Lock()
	if !s.unsaved || time.Since(s.lastRetry) < unsavedRetryEvery {
		s.mu.Unlock()
		return
	}
	s.lastRetry = time.Now()
	s.mu.Unlock()

	path, err := s.lockPath()
	if err != nil {
		s.logf("error: slack token %s: retrying the rotated token: %v", s.Service, err)
		return
	}
	unlock, err := lockFile(ctx, path)
	if err != nil {
		s.logf("error: slack token %s: retrying the rotated token: lock: %v", s.Service, err)
		return
	}
	defer unlock()
	_, _ = s.settleUnsaved()
}

// settleUnsaved runs under the refresh lock. The Keychain decides unless it
// still holds the pair this Store rotated away from: an absent item is a logout
// and a different one a newer login, and either drops the unsaved pair.
// Otherwise it saves the unsaved pair.
func (s *Store) settleUnsaved() (Token, error) {
	s.mu.Lock()
	tok, stale, unsaved := s.cached, s.staleRefresh, s.unsaved
	s.mu.Unlock()
	if !unsaved {
		return s.load()
	}
	stored, err := s.Load()
	switch {
	case errors.Is(err, ErrNotLoggedIn):
		s.remember(Token{})
		return Token{}, err
	case err == nil && stored.Refresh != stale:
		s.remember(stored)
		return stored, nil
	}
	if err := s.Save(tok); err != nil {
		s.logf("error: slack token %s: saving the rotated token still fails, keeping it in memory: %v", s.Service, err)
	}
	return tok, nil
}

// keepOrFail returns the current access token while it is still valid, so a
// failed refresh inside the window costs nothing until the token really expires.
func (s *Store) keepOrFail(tok Token, err error) (string, error) {
	if errors.Is(err, ErrNotLoggedIn) || !s.now().Before(tok.Expires) {
		return "", err
	}
	s.logf("%v; using the current token until it expires", err)
	return tok.Access, nil
}

func (s *Store) load() (Token, error) {
	tok, err := s.Load()
	if err != nil {
		return Token{}, err
	}
	s.remember(tok)
	return tok, nil
}

func (s *Store) remember(tok Token) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached, s.cachedAt = tok, time.Now()
	s.unsaved, s.staleRefresh = false, ""
}

func (s *Store) recall() (Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsaved {
		return s.cached, true
	}
	if s.cached.Access == "" || time.Since(s.cachedAt) >= cacheTTL {
		return Token{}, false
	}
	return s.cached, true
}

func (s *Store) nearExpiry(tok Token) bool {
	return !tok.Expires.IsZero() && tok.Expires.Sub(s.now()) <= refreshWindow
}

func (s *Store) lockPath() (string, error) {
	if s.LockPath != "" {
		return s.LockPath, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("slack token %s: lock path: %w", s.Service, err)
	}
	return filepath.Join(dir, s.Service+".lock"), nil
}

func (s *Store) check() error {
	if s.Keychain == nil || s.Service == "" {
		return errors.New("slack token store: keychain and service are required")
	}
	return nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func splitScopes(scope string) []string {
	var out []string
	for _, sc := range strings.Split(scope, ",") {
		if sc = strings.TrimSpace(sc); sc != "" {
			out = append(out, sc)
		}
	}
	return out
}
