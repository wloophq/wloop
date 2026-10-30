package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/config"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
)

const cliClientID = "1.2"

var (
	cliNow          = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	cliTokenPattern = regexp.MustCompile(`x(ox[pbaer]|app)-|ticket=`)
)

// cliKeychain keeps items in memory and records every service it is asked for.
type cliKeychain struct {
	mu       sync.Mutex
	items    map[string][]byte
	services []string
}

func newCLIKeychain() *cliKeychain { return &cliKeychain{items: map[string][]byte{}} }

func (k *cliKeychain) note(service string) {
	if !slices.Contains(k.services, service) {
		k.services = append(k.services, service)
	}
}

func (k *cliKeychain) Get(service, account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.note(service)
	v, ok := k.items[service+"/"+account]
	if !ok {
		return nil, auth.ErrNotFound
	}
	return slices.Clone(v), nil
}

func (k *cliKeychain) Set(service, account string, secret []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.note(service)
	k.items[service+"/"+account] = slices.Clone(secret)
	return nil
}

func (k *cliKeychain) Delete(service, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.note(service)
	if _, ok := k.items[service+"/"+account]; !ok {
		return auth.ErrNotFound
	}
	delete(k.items, service+"/"+account)
	return nil
}

func (k *cliKeychain) keys() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Sorted(maps.Keys(k.items))
}

func (k *cliKeychain) item(key string) ([]byte, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.items[key]
	return v, ok
}

func (k *cliKeychain) token(t *testing.T, service string) auth.Token {
	t.Helper()
	raw, ok := k.item(service + "/current")
	if !ok {
		t.Fatalf("no token under %s", service)
	}
	var tok auth.Token
	if err := json.Unmarshal(raw, &tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

type cliEnv struct {
	p        *Plugin
	out, err bytes.Buffer
	stdout   interface{ Write([]byte) (int, error) }
	kc       *cliKeychain
	srv      *slacktest.Server
	cfg      config.Slack
	stdin    string
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	ce := &cliEnv{kc: newCLIKeychain(), srv: slacktest.New(t)}
	ce.cfg = config.Slack{ClientID: cliClientID, KeychainService: "loop-test-slack"}
	port := 0
	ce.p = &Plugin{Env: Env{
		Keychain:  ce.kc,
		BaseURL:   ce.srv.URL,
		LockPath:  filepath.Join(t.TempDir(), "slack.lock"),
		LoginPort: &port,
		Open:      func(context.Context, string) error { return errors.New("no browser in tests") },
		Now:       func() time.Time { return cliNow },
	}}
	return ce
}

func (ce *cliEnv) run(t *testing.T, args ...string) int {
	t.Helper()
	raw, err := json.Marshal(ce.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var stdout interface{ Write([]byte) (int, error) } = &ce.out
	if ce.stdout != nil {
		stdout = ce.stdout
	}
	code, err := ce.p.RunCLI(context.Background(), src.CLIRequest{Args: args, Stdin: []byte(ce.stdin), Config: src.Config{Plugin: raw}},
		stdout, &ce.err)
	if err != nil {
		ce.err.WriteString(err.Error() + "\n")
	}
	return code
}

func (ce *cliEnv) saveToken(t *testing.T, tok auth.Token) {
	t.Helper()
	raw, _ := json.Marshal(tok)
	if err := ce.kc.Set(ce.cfg.KeychainService, "current", raw); err != nil {
		t.Fatal(err)
	}
}

func cliToken() auth.Token {
	return auth.Token{Team: "T000", TeamName: "Acme", User: "U000",
		Access: "xoxe.xoxp-1-fakeaccess", Refresh: "xoxe-1-fakerefresh",
		Expires: cliNow.Add(12 * time.Hour), Granted: cliNow.Add(-50 * time.Hour), Scopes: []string{"chat:write"}}
}

func assertNoToken(t *testing.T, s string) {
	t.Helper()
	if cliTokenPattern.MatchString(s) || strings.Contains(s, "fakeaccess") || strings.Contains(s, "fakerefresh") {
		t.Fatalf("token material leaked: %q", s)
	}
}

// cliBrowse plays the browser: it follows the authorize URL's redirect with a code.
func cliBrowse(t *testing.T, authorize string) {
	u, err := url.Parse(authorize)
	if err != nil {
		t.Error(err)
		return
	}
	q := u.Query()
	cb := q.Get("redirect_uri") + "?" + url.Values{"code": {"fake-code"}, "state": {q.Get("state")}}.Encode()
	go func() {
		resp, err := http.Get(cb)
		if err != nil {
			t.Error(err)
			return
		}
		resp.Body.Close()
	}()
}

func (ce *cliEnv) fakeOAuth() {
	ce.srv.Handle("oauth.v2.access", func(url.Values) any {
		return map[string]any{
			"authed_user": map[string]any{"id": "U000", "access_token": "xoxe.xoxp-1-fakeaccess", "refresh_token": "xoxe-1-fakerefresh",
				"expires_in": 43200, "scope": "chat:write,search:read"},
			"team": map[string]any{"id": "T000"},
		}
	})
	ce.srv.Handle("auth.test", func(url.Values) any {
		return map[string]any{"team_id": "T000", "team": "Acme", "user_id": "U000", "url": "https://acme.example/"}
	})
}

type cliWriterFunc func(p []byte)

func (w cliWriterFunc) Write(p []byte) (int, error) { w(p); return len(p), nil }

func TestSlackLoginSavesTheToken(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	ce.fakeOAuth()
	var opened []string
	ce.p.Env.Open = func(_ context.Context, u string) error { opened = append(opened, u); cliBrowse(t, u); return nil }
	if code := ce.run(t, "login"); code != 0 {
		t.Fatalf("exit %d: %s", code, &ce.err)
	}
	if len(opened) != 1 || !strings.HasPrefix(opened[0], "https://slack.com/") {
		t.Fatalf("opened %q", opened)
	}
	tok := ce.kc.token(t, "loop-test-slack")
	if tok.Access != "xoxe.xoxp-1-fakeaccess" || tok.Team != "T000" || tok.TeamName != "Acme" || tok.User != "U000" || !tok.Granted.Equal(cliNow) {
		t.Fatalf("token %+v", tok.TeamName)
	}
	if auths := ce.srv.Authorization("auth.test"); len(auths) != 1 || auths[0] != "xoxe.xoxp-1-fakeaccess" {
		t.Fatalf("auth.test did not run with the new token")
	}
	if !strings.Contains(ce.out.String(), "Acme") {
		t.Fatalf("out %q", &ce.out)
	}
	assertNoToken(t, ce.out.String()+ce.err.String())

	ce2 := newCLIEnv(t)
	ce2.fakeOAuth()
	ce2.stdout = cliWriterFunc(func(p []byte) {
		if m := regexp.MustCompile(`https://slack\.com/\S+`).Find(p); m != nil {
			cliBrowse(t, string(m))
		}
	})
	if code := ce2.run(t, "login", "--no-browser"); code != 0 {
		t.Fatalf("--no-browser exit %d: %s", code, &ce2.err)
	}
	if tok := ce2.kc.token(t, "loop-test-slack"); tok.TeamName != "Acme" {
		t.Fatalf("--no-browser token %+v", tok.TeamName)
	}
}

func TestSlackLoginNeedsAClientID(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	ce.cfg.ClientID = ""
	if code := ce.run(t, "login"); code != 1 || !strings.Contains(ce.err.String(), "plugins.slack.client_id") {
		t.Fatalf("exit %d: %s", code, &ce.err)
	}
	if len(ce.kc.services) != 0 {
		t.Fatalf("keychain touched: %v", ce.kc.services)
	}
}

func TestAppTokenFromStdin(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	ce.stdin = "xapp-1-fakeapp\n"
	if code := ce.run(t, "login", "--app-token"); code != 0 {
		t.Fatalf("exit %d: %s", code, &ce.err)
	}
	if got, _ := ce.kc.item("loop-test-slack/app"); string(got) != "xapp-1-fakeapp" {
		t.Fatalf("app token %q", got)
	}
	assertNoToken(t, ce.out.String()+ce.err.String())

	for _, args := range [][]string{
		{"login", "--app-token", "xapp-1-fakeargv"},
		{"login", "--app-token=xapp-1-fakeargv"},
		{"login", "xapp-1-fakeargv"},
	} {
		ce := newCLIEnv(t)
		ce.stdin = "xapp-1-fakeapp\n"
		if code := ce.run(t, args...); code == 0 {
			t.Errorf("%q: accepted", args)
		}
		if keys := ce.kc.keys(); len(keys) != 0 {
			t.Errorf("%q: saved %v", args, keys)
		}
		if strings.Contains(ce.out.String()+ce.err.String(), "fakeargv") {
			t.Errorf("%q: echoed the token: %s", args, &ce.err)
		}
	}

	ce = newCLIEnv(t)
	ce.stdin = "xoxp-not-an-app-token\n"
	if code := ce.run(t, "login", "--app-token"); code != 1 || len(ce.kc.keys()) != 0 {
		t.Fatalf("a user token as app token: exit %d, %v", code, ce.kc.keys())
	}
}

func TestSlackStatus(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	if code := ce.run(t, "status"); code != 0 || !strings.Contains(ce.out.String(), "not logged in") || !strings.Contains(ce.out.String(), "slack login") {
		t.Fatalf("exit %d: %s%s", code, &ce.out, &ce.err)
	}

	ce.out.Reset()
	tok := cliToken()
	ce.saveToken(t, tok)
	if code := ce.run(t, "status"); code != 0 {
		t.Fatalf("exit %d: %s", code, &ce.err)
	}
	out := ce.out.String()
	for _, want := range []string{"Acme (T000)", "U000", tok.Expires.Local().Format("2006-01-02 15:04"), "2d 2h", "app token: not stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	assertNoToken(t, out)

	ce.out.Reset()
	ce.kc.Set("loop-test-slack", "app", []byte("xapp-1-fakeapp"))
	ce.run(t, "status")
	if !strings.Contains(ce.out.String(), "app token: stored") {
		t.Fatalf("status:\n%s", &ce.out)
	}
}

func TestSlackStatusNeverDeletesOnLoggedOut(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	tok := cliToken()
	tok.Expires = cliNow.Add(-time.Hour)
	ce.saveToken(t, tok)
	ce.srv.Handle("oauth.v2.access", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_refresh_token"} })
	ce.run(t, "status")
	if _, ok := ce.kc.item("loop-test-slack/current"); !ok {
		t.Fatal("the stored token was deleted on a logged-out state")
	}
}

func TestSlackLogout(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	ce.saveToken(t, cliToken())
	ce.kc.Set("loop-test-slack", "app", []byte("xapp-1-fakeapp"))
	ce.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"revoked": true} })
	if code := ce.run(t, "logout"); code != 0 {
		t.Fatalf("exit %d: %s", code, &ce.err)
	}
	if auths := ce.srv.Authorization("auth.revoke"); len(auths) != 1 || auths[0] != "xoxe.xoxp-1-fakeaccess" {
		t.Fatalf("revoke calls %d", len(auths))
	}
	if keys := ce.kc.keys(); len(keys) != 0 {
		t.Fatalf("left %v", keys)
	}

	ce = newCLIEnv(t)
	ce.saveToken(t, cliToken())
	ce.kc.Set("loop-test-slack", "app", []byte("xapp-1-fakeapp"))
	ce.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	if code := ce.run(t, "logout"); code != 0 {
		t.Fatalf("failed revoke: exit %d: %s", code, &ce.err)
	}
	if len(ce.kc.keys()) != 0 {
		t.Fatal("a failed revoke kept the tokens")
	}
	if out := ce.out.String() + ce.err.String(); !strings.Contains(out, "revoke failed") {
		t.Fatalf("a failed revoke went unsaid: %s", out)
	}
	assertNoToken(t, ce.out.String()+ce.err.String())
}

func TestSlackLogoutPastACorruptItem(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	ce.kc.Set("loop-test-slack", "current", []byte("not a token"))
	ce.kc.Set("loop-test-slack", "app", []byte("xapp-1-fakeapp"))
	if code := ce.run(t, "logout"); code != 0 {
		t.Fatalf("exit %d: %s", code, &ce.err)
	}
	if len(ce.kc.keys()) != 0 || len(ce.srv.Calls("auth.revoke")) != 0 {
		t.Fatalf("items %d, revokes %d", len(ce.kc.keys()), len(ce.srv.Calls("auth.revoke")))
	}
	if !strings.Contains(ce.out.String(), "skipped") {
		t.Fatalf("the skipped revoke went unsaid: %s", &ce.out)
	}
}

func TestSlackFlagErrorsDoNotEchoTheirValue(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"login", "--no-browser=xoxp-1-fakeflag"},
		{"login", "--xapp-1-fakeflag"},
	} {
		ce := newCLIEnv(t)
		if code := ce.run(t, args...); code != 2 {
			t.Errorf("%q: exit %d, want a usage error", args, code)
		}
		if strings.Contains(ce.out.String()+ce.err.String(), "fakeflag") {
			t.Errorf("%q: echoed the flag: %s", args, &ce.err)
		}
	}
}

func TestKeychainServiceFromConfig(t *testing.T) {
	t.Parallel()
	ce := newCLIEnv(t)
	ce.cfg.KeychainService = "x"
	ce.run(t, "status")
	if !slices.Equal(ce.kc.services, []string{"x"}) {
		t.Fatalf("services %q", ce.kc.services)
	}
	if kit := ce.p.Env.Kit(config.Config{Slack: ce.cfg, Brand: brand()}, nil, nil); kit.Store.Service != "x" {
		t.Fatalf("Service %q", kit.Store.Service)
	}

	ce = newCLIEnv(t)
	ce.cfg.KeychainService = ""
	ce.run(t, "status")
	if want := brand() + "-slack"; !slices.Equal(ce.kc.services, []string{want}) {
		t.Fatalf("default services %q, want %q", ce.kc.services, want)
	}
}

func TestRequestLogFromConfig(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		ce := newCLIEnv(t)
		ce.cfg.RequestLog = on
		ce.saveToken(t, cliToken())
		ce.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"revoked": true} })
		if code := ce.run(t, "logout"); code != 0 {
			t.Fatalf("request_log=%t: exit %d: %s", on, code, &ce.err)
		}
		logged := strings.Contains(ce.err.String(), "slack_call method=auth.revoke status=200")
		if logged != on {
			t.Fatalf("request_log=%t: stderr %q", on, &ce.err)
		}
		assertNoToken(t, ce.err.String())
	}

	logf := func(string, ...any) {}
	ce := newCLIEnv(t)
	if kit := ce.p.Env.Kit(config.Config{Slack: config.Slack{RequestLog: true}}, logf, nil); kit.Client.RequestLog == nil {
		t.Fatal("the daemon's kit logs no request with request_log on")
	}
	if kit := ce.p.Env.Kit(config.Config{}, logf, nil); kit.Client.RequestLog != nil {
		t.Fatal("the daemon's kit logs requests with request_log off")
	}
}

// TestSlackLoginRunsWithoutTheDaemon runs the commands on a plugin that was
// never started: they read and write only the Keychain and call Slack, and
// open no store anywhere.
func TestSlackLoginRunsWithoutTheDaemon(t *testing.T) {
	dirs := map[string]string{"cwd": t.TempDir(), "home": t.TempDir(), "tmp": t.TempDir(), "lock": t.TempDir()}
	t.Chdir(dirs["cwd"])
	t.Setenv("HOME", dirs["home"])
	t.Setenv("TMPDIR", dirs["tmp"])
	ce := newCLIEnv(t)
	ce.p.Env.LockPath = filepath.Join(dirs["lock"], "slack.lock")
	ce.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"revoked": true} })

	ce.stdin = "xapp-1-fakeapp\n"
	if code := ce.run(t, "login", "--app-token"); code != 0 || !strings.Contains(ce.out.String(), "Saved the Slack app token in Keychain service loop-test-slack") {
		t.Fatalf("login --app-token: exit %d: %s%s", code, &ce.out, &ce.err)
	}
	if got, _ := ce.kc.item("loop-test-slack/app"); string(got) != "xapp-1-fakeapp" {
		t.Fatalf("app token %q", got)
	}

	ce.stdin = ""
	ce.out.Reset()
	if code := ce.run(t, "status"); code != 0 || !strings.Contains(ce.out.String(), "not logged in") {
		t.Fatalf("status: exit %d: %s%s", code, &ce.out, &ce.err)
	}
	ce.saveToken(t, cliToken())
	ce.out.Reset()
	if code := ce.run(t, "status"); code != 0 || !strings.Contains(ce.out.String(), "Acme (T000)") || !strings.Contains(ce.out.String(), "app token: stored") {
		t.Fatalf("status logged in: exit %d: %s%s", code, &ce.out, &ce.err)
	}

	ce.out.Reset()
	if code := ce.run(t, "logout"); code != 0 || !strings.Contains(ce.out.String(), "Revoked the Slack token.") ||
		!strings.Contains(ce.out.String(), "Deleted the Slack tokens from Keychain service loop-test-slack.") {
		t.Fatalf("logout: exit %d: %s%s", code, &ce.out, &ce.err)
	}
	if keys := ce.kc.keys(); len(keys) != 0 {
		t.Fatalf("keychain kept %v", keys)
	}
	if n := len(ce.srv.Calls("auth.revoke")); n != 1 {
		t.Fatalf("revokes %d", n)
	}
	if _, err := ce.p.running(); !errors.Is(err, errNotStarted) {
		t.Fatalf("the plugin runs after the commands: %v", err)
	}
	for name, dir := range dirs {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path == ce.p.Env.LockPath {
				return err
			}
			t.Errorf("%s: the commands wrote %s", name, path)
			return nil
		})
	}
}

func TestEvalRunsOnlyInTheRunningPlugin(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if _, err := (&Plugin{}).RunCLI(context.Background(), src.CLIRequest{Args: []string{CommandEval, "score"}}, &out, &errOut); err == nil ||
		!strings.Contains(err.Error(), "running daemon") {
		t.Fatalf("eval in CLI mode: %v", err)
	}
}
