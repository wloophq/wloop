package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/slacktest"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

const testService = "loop-test-slack"

var tokenPattern = regexp.MustCompile(`x(ox[pbaer]|app)-|ticket=`)

func assertNoToken(t *testing.T, s string) {
	t.Helper()
	if tokenPattern.MatchString(s) || strings.Contains(s, "fakeaccess") || strings.Contains(s, "fakerefresh") {
		t.Fatalf("token material leaked: %q", s)
	}
}

type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) Log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newStore(kc Keychain, lockPath string, srv *slacktest.Server, log *logBuf) *Store {
	s := &Store{
		Keychain: kc,
		Service:  testService,
		LockPath: lockPath,
		Client:   &slack.Client{BaseURL: srv.URL},
		ClientID: "1.2",
		Now:      func() time.Time { return t0 },
	}
	if log != nil {
		s.Log = log.Log
	}
	return s
}

func oldToken(expiresIn time.Duration) Token {
	return Token{
		Team: "T000", User: "U000", TeamName: "Acme",
		Access: "xoxe.xoxp-1-fakeaccess-old", Refresh: "xoxe-1-fakerefresh-old",
		Expires: t0.Add(expiresIn), Granted: t0.Add(-time.Hour),
		Scopes: []string{"chat:write", "search:read"},
	}
}

func refreshHandler(srv *slacktest.Server, delay time.Duration) {
	var n atomic.Int32
	srv.Handle("oauth.v2.access", func(url.Values) any {
		time.Sleep(delay)
		i := n.Add(1)
		return map[string]any{
			"token_type":    "user",
			"access_token":  fmt.Sprintf("xoxe.xoxp-1-fakeaccess-new%d", i),
			"refresh_token": fmt.Sprintf("xoxe-1-fakerefresh-new%d", i),
			"expires_in":    43200,
			"user_id":       "U000",
			"team":          map[string]any{"id": "T000"},
		}
	})
}

func TestStoreRoundTrips(t *testing.T) {
	srv := slacktest.New(t)
	s := newStore(newMemKeychain(), filepath.Join(t.TempDir(), "l"), srv, nil)
	if _, err := s.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Load of an empty store = %v, want ErrNotLoggedIn", err)
	}
	tok := oldToken(time.Hour)
	if err := s.Save(tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, tok) {
		t.Fatalf("Load = %+v, want %+v", got, tok)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Load after Delete = %v, want ErrNotLoggedIn", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("second Delete = %v, want nil", err)
	}
}

func TestStoreRoundTripsThroughSecurity(t *testing.T) {
	bin, _ := fakeSecurity(t)
	srv := slacktest.New(t)
	s := newStore(securityKeychain(t, bin), filepath.Join(t.TempDir(), "l"), srv, nil)
	tok := oldToken(time.Hour)
	if err := s.Save(tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, tok) {
		t.Fatalf("Load = %+v, want %+v", got, tok)
	}
	if err := s.SaveAppToken("xapp-1-fakeapp"); err != nil {
		t.Fatal(err)
	}
	if app, err := s.AppToken(); err != nil || app != "xapp-1-fakeapp" {
		t.Fatalf("AppToken = %q, %v", app, err)
	}
}

func TestAppTokenAccount(t *testing.T) {
	kc := newMemKeychain()
	s := newStore(kc, filepath.Join(t.TempDir(), "l"), slacktest.New(t), nil)
	if _, err := s.AppToken(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("AppToken of an empty store = %v, want ErrNotLoggedIn", err)
	}
	if err := s.SaveAppToken("xapp-1-fakeapp"); err != nil {
		t.Fatal(err)
	}
	if v, _ := kc.Get(testService, "app"); string(v) != "xapp-1-fakeapp" {
		t.Fatalf("account app holds %q", v)
	}
	if got, err := s.AppToken(); err != nil || got != "xapp-1-fakeapp" {
		t.Fatalf("AppToken = %q, %v", got, err)
	}
	err := s.SaveAppToken("xoxe.xoxp-1-fakeaccess")
	if err == nil {
		t.Fatal("SaveAppToken accepted a user token")
	}
	assertNoToken(t, err.Error())
	if got, _ := s.AppToken(); got != "xapp-1-fakeapp" {
		t.Fatalf("a refused value replaced the app token: %q", got)
	}
	if err := s.DeleteAppToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppToken(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("AppToken after delete = %v", err)
	}
	if err := s.Save(oldToken(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAppToken("xapp-1-fakeapp"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppToken(); err != nil {
		t.Fatalf("Delete removed the app token: %v", err)
	}
}

func TestTokenReturnsAFreshTokenWithoutRefresh(t *testing.T) {
	srv := slacktest.New(t)
	refreshHandler(srv, 0)
	s := newStore(newMemKeychain(), filepath.Join(t.TempDir(), "l"), srv, nil)
	if _, err := s.Token(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Token of an empty store = %v", err)
	}
	if err := s.Save(oldToken(6 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Token(context.Background())
	if err != nil || got != "xoxe.xoxp-1-fakeaccess-old" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	if n := len(srv.Calls("oauth.v2.access")); n != 0 {
		t.Fatalf("refreshed %d times a token 6 min from expiry", n)
	}
}

type orderKeychain struct {
	*memKeychain
	saved atomic.Bool
}

func (k *orderKeychain) Set(service, account string, secret []byte) error {
	err := k.memKeychain.Set(service, account, secret)
	k.saved.Store(err == nil)
	return err
}

func TestTokenRefreshesNearExpiry(t *testing.T) {
	srv := slacktest.New(t)
	refreshHandler(srv, 0)
	kc := &orderKeychain{memKeychain: newMemKeychain()}
	s := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, nil)
	if err := s.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	kc.saved.Store(false)

	got, err := s.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !kc.saved.Load() {
		t.Fatal("Token returned before saving the rotated pair")
	}
	if got != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("Token = %q, want the new access token", got)
	}
	calls := srv.Calls("oauth.v2.access")
	if len(calls) != 1 {
		t.Fatalf("refreshes = %d, want 1", len(calls))
	}
	want := url.Values{"client_id": {"1.2"}, "grant_type": {"refresh_token"}, "refresh_token": {"xoxe-1-fakerefresh-old"}}
	if !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("refresh form = %v, want %v", calls[0], want)
	}
	if a := srv.Authorization("oauth.v2.access"); a[0] != "" {
		t.Fatal("refresh sent a bearer")
	}

	stored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	want2 := oldToken(0)
	want2.Access, want2.Refresh = "xoxe.xoxp-1-fakeaccess-new1", "xoxe-1-fakerefresh-new1"
	want2.Expires = t0.Add(43200 * time.Second)
	if !reflect.DeepEqual(stored, want2) {
		t.Fatalf("stored = %+v, want %+v", stored, want2)
	}
}

func TestConcurrentRefreshHappensOnce(t *testing.T) {
	srv := slacktest.New(t)
	refreshHandler(srv, 150*time.Millisecond)
	kc := newMemKeychain()
	lock := filepath.Join(t.TempDir(), "l")
	a, b := newStore(kc, lock, srv, nil), newStore(kc, lock, srv, nil)
	if err := a.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	got := make([]string, 2)
	errs := make([]error, 2)
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = s.Token(context.Background())
		}()
	}
	wg.Wait()
	for i := range got {
		if errs[i] != nil || got[i] != "xoxe.xoxp-1-fakeaccess-new1" {
			t.Fatalf("store %d: Token = %q, %v", i, got[i], errs[i])
		}
	}
	if n := len(srv.Calls("oauth.v2.access")); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
}

const helperEnv = "LOOP_AUTH_REFRESH_HELPER"

func TestHelperProcessRefresh(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("helper process only")
	}
	s := &Store{
		Keychain: fileKeychain{dir: os.Getenv("HELPER_KEYCHAIN")},
		Service:  testService,
		LockPath: os.Getenv("HELPER_LOCK"),
		Client:   &slack.Client{BaseURL: os.Getenv("HELPER_BASE_URL")},
		ClientID: "1.2",
		Now:      func() time.Time { return t0 },
	}
	tok, err := s.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("helper got %q", tok)
	}
}

func TestConcurrentRefreshAcrossProcesses(t *testing.T) {
	srv := slacktest.New(t)
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	var n atomic.Int32
	srv.Handle("oauth.v2.access", func(url.Values) any {
		arrived <- struct{}{}
		<-release
		i := n.Add(1)
		return map[string]any{
			"access_token":  fmt.Sprintf("xoxe.xoxp-1-fakeaccess-new%d", i),
			"refresh_token": fmt.Sprintf("xoxe-1-fakerefresh-new%d", i),
			"expires_in":    43200,
		}
	})
	kc := fileKeychain{dir: t.TempDir()}
	lock := filepath.Join(t.TempDir(), "l")
	s := newStore(kc, lock, srv, nil)
	if err := s.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessRefresh$", "-test.count=1")
	cmd.Env = append(os.Environ(), helperEnv+"=1", "HELPER_KEYCHAIN="+kc.dir, "HELPER_LOCK="+lock, "HELPER_BASE_URL="+srv.URL)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-arrived:
	case <-time.After(20 * time.Second):
		close(release)
		_ = cmd.Wait()
		t.Fatalf("helper never refreshed: %s", out.String())
	}

	type result struct {
		tok string
		err error
	}
	mine := make(chan result, 1)
	go func() {
		tok, err := s.Token(context.Background())
		mine <- result{tok, err}
	}()
	time.Sleep(200 * time.Millisecond)
	close(release)

	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v\n%s", err, out.String())
	}
	r := <-mine
	if r.err != nil || r.tok != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("Token = %q, %v", r.tok, r.err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("refreshes = %d, want 1", got)
	}
}

func TestLockPathFollowsTheService(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	a := &Store{Service: "loop-e2e-slack"}
	b := &Store{Service: "loop-e2e-slack"}
	pa, err := a.lockPath()
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := b.lockPath()
	if want := filepath.Join(dir, "loop-e2e-slack.lock"); pa != want || pb != want {
		t.Fatalf("lock paths = %q, %q, want %q", pa, pb, want)
	}
	c := &Store{Service: "loop-e2e-slack", LockPath: "/elsewhere.lock"}
	if p, _ := c.lockPath(); p != "/elsewhere.lock" {
		t.Fatalf("explicit LockPath ignored: %q", p)
	}
}

func TestDeadRefreshMeansLoggedOut(t *testing.T) {
	srv := slacktest.New(t)
	srv.Handle("oauth.v2.access", func(url.Values) any {
		return map[string]any{"ok": false, "error": "invalid_refresh_token"}
	})
	log := &logBuf{}
	s := newStore(newMemKeychain(), filepath.Join(t.TempDir(), "l"), srv, log)
	tok := oldToken(4 * time.Minute)
	if err := s.Save(tok); err != nil {
		t.Fatal(err)
	}
	_, err := s.Token(context.Background())
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Token = %v, want ErrNotLoggedIn", err)
	}
	assertNoToken(t, err.Error())
	assertNoToken(t, log.String())
	if got, _ := s.Load(); !reflect.DeepEqual(got, tok) {
		t.Fatalf("stored token changed: %+v", got)
	}
}

func TestRotatedSaveRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		wantSets  int
		wantSaved bool
	}{
		{"recovers", 2, 3, true},
		{"gives up", 10, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := slacktest.New(t)
			refreshHandler(srv, 0)
			kc := newMemKeychain()
			log := &logBuf{}
			s := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, log)
			if err := s.Save(oldToken(4 * time.Minute)); err != nil {
				t.Fatal(err)
			}
			kc.sets, kc.setErrs = 0, tc.failures

			got, err := s.Token(context.Background())
			if err != nil || got != "xoxe.xoxp-1-fakeaccess-new1" {
				t.Fatalf("Token = %q, %v", got, err)
			}
			if kc.sets != tc.wantSets {
				t.Fatalf("Set calls = %d, want %d", kc.sets, tc.wantSets)
			}
			stored, _ := s.Load()
			if (stored.Access == "xoxe.xoxp-1-fakeaccess-new1") != tc.wantSaved {
				t.Fatalf("stored access = %q", stored.Access)
			}
			logged := log.String()
			assertNoToken(t, logged)
			if !tc.wantSaved && !strings.Contains(logged, testService) {
				t.Fatalf("log does not name the service: %q", logged)
			}
		})
	}
}

func TestTokenErrorsCarryNoToken(t *testing.T) {
	for name, answer := range map[string]any{
		"http 500":  slacktest.Response{Status: 500, Body: "boom xoxe.xoxp-1-fakeaccess"},
		"not json":  "xoxe-1-fakerefresh <html>",
		"api error": map[string]any{"ok": false, "error": "invalid_client_id"},
		"no access": map[string]any{"refresh_token": "xoxe-1-fakerefresh-x"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := slacktest.New(t)
			srv.Handle("oauth.v2.access", func(url.Values) any { return answer })
			log := &logBuf{}
			s := newStore(newMemKeychain(), filepath.Join(t.TempDir(), "l"), srv, log)
			if err := s.Save(oldToken(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := s.Token(ctx)
			if err == nil {
				t.Fatal("Token succeeded with an expired token and a failing refresh")
			}
			assertNoToken(t, err.Error())
			assertNoToken(t, log.String())
		})
	}
}

func TestTokenKeepsAValidTokenWhenRefreshFails(t *testing.T) {
	srv := slacktest.New(t)
	srv.Handle("oauth.v2.access", func(url.Values) any { return slacktest.Response{Status: 503} })
	log := &logBuf{}
	s := newStore(newMemKeychain(), filepath.Join(t.TempDir(), "l"), srv, log)
	if err := s.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Token(context.Background())
	if err != nil || got != "xoxe.xoxp-1-fakeaccess-old" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	if !strings.Contains(log.String(), testService) {
		t.Fatalf("the failed refresh was not logged: %q", log.String())
	}
	assertNoToken(t, log.String())
}

func TestTokenReadsTheKeychainOncePerWindow(t *testing.T) {
	kc := newMemKeychain()
	srv := slacktest.New(t)
	writer := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, nil)
	if err := writer.Save(oldToken(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, nil)
	kc.gets = 0
	for range 100 {
		if _, err := s.Token(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if kc.gets != 1 {
		t.Fatalf("keychain reads = %d for 100 calls, want 1", kc.gets)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Token after Delete = %v, want ErrNotLoggedIn", err)
	}
}

func TestUnsavedRotatedPairOutlivesTheCache(t *testing.T) {
	srv := slacktest.New(t)
	refreshHandler(srv, 0)
	kc := newMemKeychain()
	log := &logBuf{}
	s := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, log)
	if err := s.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	kc.setErrs = 100

	if got, err := s.Token(context.Background()); err != nil || got != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	expire := func() {
		s.mu.Lock()
		s.cachedAt = s.cachedAt.Add(-time.Hour)
		s.lastRetry = s.lastRetry.Add(-time.Hour)
		s.mu.Unlock()
	}
	expire()
	if got, err := s.Token(context.Background()); err != nil || got != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("Token after the cache window = %q, %v; the rotated pair was lost", got, err)
	}
	if n := len(srv.Calls("oauth.v2.access")); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
	if !strings.Contains(log.String(), "error") || !strings.Contains(log.String(), testService) {
		t.Fatalf("the failed save was not surfaced: %q", log.String())
	}
	assertNoToken(t, log.String())

	kc.setErrs = 0
	expire()
	if _, err := s.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Load()
	if err != nil || stored.Access != "xoxe.xoxp-1-fakeaccess-new1" || stored.Refresh != "xoxe-1-fakerefresh-new1" {
		t.Fatalf("stored = %+v, %v; want the rotated pair saved once the keychain works", stored.Access, err)
	}
}

func TestUnsavedPairYieldsToANewerLogin(t *testing.T) {
	srv := slacktest.New(t)
	refreshHandler(srv, 0)
	kc := newMemKeychain()
	s := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, nil)
	if err := s.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	kc.setErrs = 4
	if _, err := s.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := newStore(kc, filepath.Join(t.TempDir(), "l"), srv, nil)
	fresh := oldToken(time.Hour)
	fresh.Access, fresh.Refresh = "xoxe.xoxp-1-fakeaccess-login", "xoxe-1-fakerefresh-login"
	if err := other.Save(fresh); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.cachedAt, s.lastRetry = s.cachedAt.Add(-time.Hour), s.lastRetry.Add(-time.Hour)
	s.mu.Unlock()
	if got, err := s.Token(context.Background()); err != nil || got != fresh.Access {
		t.Fatalf("Token = %q, %v; want the newer login", got, err)
	}
	if stored, _ := s.Load(); stored.Access != fresh.Access {
		t.Fatalf("the unsaved pair overwrote a newer login: %q", stored.Access)
	}
}

// unsavedStore is a Store holding the rotated pair new1 unsaved; the Keychain holds the old pair.
func unsavedStore(t *testing.T, kc *memKeychain, lock string) (*Store, *slacktest.Server) {
	t.Helper()
	srv := slacktest.New(t)
	refreshHandler(srv, 0)
	s := newStore(kc, lock, srv, nil)
	if err := s.Save(oldToken(4 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	kc.setErrs = rotatedSaveRetries + 1
	if got, err := s.Token(context.Background()); err != nil || got != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	return s, srv
}

func ageRetry(s *Store) {
	s.mu.Lock()
	s.cachedAt, s.lastRetry = s.cachedAt.Add(-time.Hour), s.lastRetry.Add(-time.Hour)
	s.mu.Unlock()
}

func TestUnsavedPairYieldsToALogout(t *testing.T) {
	kc := newMemKeychain()
	lock := filepath.Join(t.TempDir(), "l")
	s, srv := unsavedStore(t, kc, lock)
	if err := newStore(kc, lock, srv, nil).Delete(); err != nil {
		t.Fatal(err)
	}
	ageRetry(s)
	if got, err := s.Token(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Token after a logout elsewhere = %q, %v; want ErrNotLoggedIn", got, err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("the unsaved pair undid the logout: %v", err)
	}
}

func TestSecondUnsavedRefreshKeepsTheNewestPair(t *testing.T) {
	kc := newMemKeychain()
	s, _ := unsavedStore(t, kc, filepath.Join(t.TempDir(), "l"))
	s.Now = func() time.Time { return t0.Add(43200*time.Second - time.Minute) }
	kc.setErrs = 100
	if got, err := s.Token(context.Background()); err != nil || got != "xoxe.xoxp-1-fakeaccess-new2" {
		t.Fatalf("second refresh = %q, %v", got, err)
	}
	kc.setErrs = 0
	ageRetry(s)
	if got, err := s.Token(context.Background()); err != nil || got != "xoxe.xoxp-1-fakeaccess-new2" {
		t.Fatalf("Token after the retry = %q, %v; want the newest pair", got, err)
	}
	if stored, err := s.Load(); err != nil || stored.Refresh != "xoxe-1-fakerefresh-new2" {
		t.Fatalf("stored refresh = %q, %v; want the newest pair", stored.Refresh, err)
	}
}

func TestUnsavedRetryWaitsForTheRefreshLock(t *testing.T) {
	kc := newMemKeychain()
	lock := filepath.Join(t.TempDir(), "l")
	s, _ := unsavedStore(t, kc, lock)
	unlock, err := lockFile(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	kc.setErrs = 0
	ageRetry(s)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, _ = s.Token(ctx)
	cancel()
	if stored, _ := s.Load(); stored.Access != "xoxe.xoxp-1-fakeaccess-old" {
		t.Fatalf("the retry wrote %q while another process held the lock", stored.Access)
	}
	unlock()
	ageRetry(s)
	if _, err := s.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stored, _ := s.Load(); stored.Access != "xoxe.xoxp-1-fakeaccess-new1" {
		t.Fatalf("stored %q after the lock was released", stored.Access)
	}
}
