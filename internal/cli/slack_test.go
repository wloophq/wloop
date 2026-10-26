package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/slacktest"
)

const testClientID = "1.2"

var slackTokenPattern = regexp.MustCompile(`x(ox[pbaer]|app)-|ticket=`)

// fakeKeychain keeps items in memory and records every service it is asked for.
type fakeKeychain struct {
	mu       sync.Mutex
	items    map[string][]byte
	services []string
	gets     int
}

func newFakeKeychain() *fakeKeychain { return &fakeKeychain{items: map[string][]byte{}} }

func (k *fakeKeychain) note(service string) {
	if !slices.Contains(k.services, service) {
		k.services = append(k.services, service)
	}
}

func (k *fakeKeychain) Get(service, account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.note(service)
	k.gets++
	v, ok := k.items[service+"/"+account]
	if !ok {
		return nil, auth.ErrNotFound
	}
	return slices.Clone(v), nil
}

func (k *fakeKeychain) Set(service, account string, secret []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.note(service)
	k.items[service+"/"+account] = slices.Clone(secret)
	return nil
}

func (k *fakeKeychain) Delete(service, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.note(service)
	if _, ok := k.items[service+"/"+account]; !ok {
		return auth.ErrNotFound
	}
	delete(k.items, service+"/"+account)
	return nil
}

func (k *fakeKeychain) token(t *testing.T, service string) auth.Token {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	var tok auth.Token
	raw, ok := k.items[service+"/current"]
	if !ok {
		t.Fatalf("no token under %s", service)
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

type slackTestEnv struct {
	*Env
	out, err *bytes.Buffer
	kc       *fakeKeychain
	srv      *slacktest.Server
	cfg      config.Config
}

func newSlackTestEnv(t *testing.T) *slackTestEnv {
	t.Helper()
	te := &slackTestEnv{out: &bytes.Buffer{}, err: &bytes.Buffer{}, kc: newFakeKeychain(), srv: slacktest.New(t), cfg: Defaults()}
	te.cfg.Slack.ClientID = testClientID
	te.cfg.Slack.KeychainService = "loop-test-slack"
	port := 0
	te.Env = &Env{
		Out:        te.out,
		Err:        te.err,
		In:         strings.NewReader(""),
		LoadConfig: func() (config.Config, error) { return te.cfg, nil },
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		StateDir:   t.TempDir(),
		Home:       t.TempDir(),
		Now:        func() time.Time { return testNow },
		Sh:         func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no shell in tests") },
		slack: slackEnv{
			Keychain:  te.kc,
			BaseURL:   te.srv.URL,
			LockPath:  filepath.Join(t.TempDir(), "slack.lock"),
			LoginPort: &port,
			Open:      func(string) error { return errors.New("no browser in tests") },
			Now:       func() time.Time { return testNow },
		},
	}
	return te
}

func (te *slackTestEnv) run(args ...string) int {
	return Run(context.Background(), te.Env, args)
}

func (te *slackTestEnv) saveToken(t *testing.T, tok auth.Token) {
	t.Helper()
	raw, _ := json.Marshal(tok)
	if err := te.kc.Set(te.cfg.Slack.Service(), "current", raw); err != nil {
		t.Fatal(err)
	}
}

func fakeToken() auth.Token {
	return auth.Token{Team: "T000", TeamName: "Acme", User: "U000",
		Access: "xoxe.xoxp-1-fakeaccess", Refresh: "xoxe-1-fakerefresh",
		Expires: testNow.Add(12 * time.Hour), Granted: testNow.Add(-50 * time.Hour), Scopes: []string{"chat:write"}}
}

func assertNoSlackToken(t *testing.T, s string) {
	t.Helper()
	if slackTokenPattern.MatchString(s) || strings.Contains(s, "fakeaccess") || strings.Contains(s, "fakerefresh") {
		t.Fatalf("token material leaked: %q", s)
	}
}

// browse plays the browser: it follows the authorize URL's redirect with a code.
func browse(t *testing.T, authorize string) {
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

func (te *slackTestEnv) fakeOAuth() {
	te.srv.Handle("oauth.v2.access", func(url.Values) any {
		return map[string]any{
			"authed_user": map[string]any{"id": "U000", "access_token": "xoxe.xoxp-1-fakeaccess", "refresh_token": "xoxe-1-fakerefresh",
				"expires_in": 43200, "scope": "chat:write,search:read"},
			"team": map[string]any{"id": "T000"},
		}
	})
	te.srv.Handle("auth.test", func(url.Values) any {
		return map[string]any{"team_id": "T000", "team": "Acme", "user_id": "U000", "url": "https://acme.example/"}
	})
}

func TestSlackLoginSavesTheToken(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.fakeOAuth()
	te.slack.Open = func(u string) error { browse(t, u); return nil }
	if code := te.run("slack", "login"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	tok := te.kc.token(t, "loop-test-slack")
	if tok.Access != "xoxe.xoxp-1-fakeaccess" || tok.Team != "T000" || tok.TeamName != "Acme" || tok.User != "U000" || !tok.Granted.Equal(testNow) {
		t.Fatalf("token %+v", tok.TeamName)
	}
	if auths := te.srv.Authorization("auth.test"); len(auths) != 1 || auths[0] != "xoxe.xoxp-1-fakeaccess" {
		t.Fatalf("auth.test did not run with the new token")
	}
	if !strings.Contains(te.out.String(), "Acme") {
		t.Fatalf("out %q", te.out)
	}
	assertNoSlackToken(t, te.out.String()+te.err.String())

	te2 := newSlackTestEnv(t)
	te2.fakeOAuth()
	te2.Out = writerFunc(func(p []byte) {
		if m := regexp.MustCompile(`https://slack\.com/\S+`).Find(p); m != nil {
			browse(t, string(m))
		}
	})
	if code := te2.run("slack", "login", "--no-browser"); code != 0 {
		t.Fatalf("--no-browser exit %d: %s", code, te2.err)
	}
	if tok := te2.kc.token(t, "loop-test-slack"); tok.TeamName != "Acme" {
		t.Fatalf("--no-browser token %+v", tok.TeamName)
	}
}

type writerFunc func(p []byte)

func (w writerFunc) Write(p []byte) (int, error) { w(p); return len(p), nil }

func TestSlackLoginNeedsAClientID(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.cfg.Slack.ClientID = ""
	if code := te.run("slack", "login"); code != 1 || !strings.Contains(te.err.String(), "slack.client_id") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(te.kc.services) != 0 {
		t.Fatalf("keychain touched: %v", te.kc.services)
	}
}

func TestAppTokenFromStdin(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.In = strings.NewReader("xapp-1-fakeapp\n")
	if code := te.run("slack", "login", "--app-token"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := string(te.kc.items["loop-test-slack/app"]); got != "xapp-1-fakeapp" {
		t.Fatalf("app token %q", got)
	}
	assertNoSlackToken(t, te.out.String()+te.err.String())

	for _, args := range [][]string{
		{"slack", "login", "--app-token", "xapp-1-fakeargv"},
		{"slack", "login", "--app-token=xapp-1-fakeargv"},
		{"slack", "login", "xapp-1-fakeargv"},
	} {
		te := newSlackTestEnv(t)
		te.In = strings.NewReader("xapp-1-fakeapp\n")
		if code := te.run(args...); code == 0 {
			t.Errorf("%q: accepted", args)
		}
		if len(te.kc.items) != 0 {
			t.Errorf("%q: saved %v", args, te.kc.items)
		}
		if strings.Contains(te.out.String()+te.err.String(), "fakeargv") {
			t.Errorf("%q: echoed the token: %s", args, te.err)
		}
	}

	te = newSlackTestEnv(t)
	te.In = strings.NewReader("xoxp-not-an-app-token\n")
	if code := te.run("slack", "login", "--app-token"); code != 1 || len(te.kc.items) != 0 {
		t.Fatalf("a user token as app token: exit %d, %v", code, te.kc.items)
	}
}

func TestSlackStatus(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	if code := te.run("slack", "status"); code != 0 || !strings.Contains(te.out.String(), "not logged in") || !strings.Contains(te.out.String(), "slack login") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}

	te.out.Reset()
	tok := fakeToken()
	te.saveToken(t, tok)
	if code := te.run("slack", "status"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{"Acme (T000)", "U000", tok.Expires.Local().Format("2006-01-02 15:04"), "2d 2h", "app token: not stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	assertNoSlackToken(t, out)

	te.out.Reset()
	te.kc.items["loop-test-slack/app"] = []byte("xapp-1-fakeapp")
	te.run("slack", "status")
	if !strings.Contains(te.out.String(), "app token: stored") {
		t.Fatalf("status:\n%s", te.out)
	}
}

func TestSlackStatusNeverDeletesOnLoggedOut(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	tok := fakeToken()
	tok.Expires = testNow.Add(-time.Hour)
	te.saveToken(t, tok)
	te.srv.Handle("oauth.v2.access", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_refresh_token"} })
	te.run("slack", "status")
	te.run("slack", "send", "--yes", "C0000000001", "hi")
	if _, ok := te.kc.items["loop-test-slack/current"]; !ok {
		t.Fatal("the stored token was deleted on a logged-out state")
	}
}

func TestSlackLogout(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	te.kc.items["loop-test-slack/app"] = []byte("xapp-1-fakeapp")
	te.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"revoked": true} })
	if code := te.run("slack", "logout"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if auths := te.srv.Authorization("auth.revoke"); len(auths) != 1 || auths[0] != "xoxe.xoxp-1-fakeaccess" {
		t.Fatalf("revoke calls %d", len(auths))
	}
	if len(te.kc.items) != 0 {
		t.Fatalf("left %v", slices.Collect(mapsKeys(te.kc.items)))
	}

	te = newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	te.kc.items["loop-test-slack/app"] = []byte("xapp-1-fakeapp")
	te.srv.Handle("auth.revoke", func(url.Values) any { return map[string]any{"ok": false, "error": "invalid_auth"} })
	if code := te.run("slack", "logout"); code != 0 {
		t.Fatalf("failed revoke: exit %d: %s", code, te.err)
	}
	if len(te.kc.items) != 0 {
		t.Fatal("a failed revoke kept the tokens")
	}
	if out := te.out.String() + te.err.String(); !strings.Contains(out, "revoke failed") {
		t.Fatalf("a failed revoke went unsaid: %s", out)
	}
	assertNoSlackToken(t, te.out.String()+te.err.String())
}

func mapsKeys(m map[string][]byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func (te *slackTestEnv) fakeChannel() {
	te.srv.Handle("conversations.info", func(url.Values) any {
		return map[string]any{"channel": map[string]any{"id": "C0000000001", "name": "general", "is_channel": true, "is_member": true}}
	})
	te.srv.Handle("chat.postMessage", func(url.Values) any { return map[string]any{"channel": "C0000000001", "ts": "1700000000.000100"} })
	te.srv.Handle("chat.getPermalink", func(url.Values) any {
		return map[string]any{"permalink": "https://acme.example/archives/C0000000001/p1700000000000100"}
	})
}

func TestSlackSendConfirms(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		interactive bool
		answer      string
		yes         bool
		wantCode    int
		wantPosts   int
	}{
		{"non-interactive refuses", false, "y\n", false, 1, 0},
		{"non-interactive with --yes", false, "", true, 0, 1},
		{"asks and sends on yes", true, "y\n", false, 0, 1},
		{"asks and stops on no", true, "n\n", false, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			te := newSlackTestEnv(t)
			te.saveToken(t, fakeToken())
			te.fakeChannel()
			te.In = strings.NewReader(tc.answer)
			te.slack.Interactive = func() bool { return tc.interactive }
			args := []string{"slack", "send", "C0000000001", "**hello**"}
			if tc.yes {
				args = append(args, "--yes")
			}
			if code := te.run(args...); code != tc.wantCode {
				t.Fatalf("exit %d, want %d: %s%s", code, tc.wantCode, te.out, te.err)
			}
			if n := len(te.srv.Calls("chat.postMessage")); n != tc.wantPosts {
				t.Fatalf("posts %d, want %d", n, tc.wantPosts)
			}
			if tc.interactive && !strings.Contains(te.out.String(), "#general") || tc.interactive && !strings.Contains(te.out.String(), "*hello*") {
				t.Fatalf("the prompt lacks the conversation or the text: %s", te.out)
			}
			if tc.wantPosts == 1 && !strings.Contains(te.out.String(), "p1700000000000100") {
				t.Fatalf("out %s", te.out)
			}
		})
	}
}

func TestSlackSendRefusesWhileDisabled(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	te.fakeChannel()
	off := false
	te.cfg.Slack.Enabled = &off
	if code := te.run("slack", "send", "--yes", "C0000000001", "hi"); code != 1 || !strings.Contains(te.err.String(), "slack.enabled") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if n := len(te.srv.Calls("chat.postMessage")); n != 0 {
		t.Fatalf("posted %d", n)
	}
}

func TestSlackAuthCommandsWorkWhileDisabled(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	off := false
	te.cfg.Slack.Enabled = &off
	te.fakeOAuth()
	te.srv.Handle("auth.revoke", func(url.Values) any { return nil })
	te.slack.Open = func(u string) error { browse(t, u); return nil }
	for _, args := range [][]string{{"slack", "login"}, {"slack", "status"}, {"slack", "logout"}} {
		if code := te.run(args...); code != 0 {
			t.Fatalf("%q: exit %d: %s", args, code, te.err)
		}
	}
	if len(te.srv.Calls("auth.revoke")) != 1 || len(te.kc.items) != 0 {
		t.Fatalf("logout while disabled: %d revokes, items %d", len(te.srv.Calls("auth.revoke")), len(te.kc.items))
	}
}

func TestKeychainServiceFromConfig(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.cfg.Slack.KeychainService = "x"
	te.run("slack", "status")
	te.run("slack", "send", "--yes", "C0000000001", "hi")
	if !slices.Equal(te.kc.services, []string{"x"}) {
		t.Fatalf("services %q", te.kc.services)
	}
	kit := te.slack.kit(te.cfg.Slack, nil)
	if kit.Store.Service != "x" {
		t.Fatalf("Service %q", kit.Store.Service)
	}
	if got := te.slack.kit(config.Slack{}, nil).Store.Service; got != brand.StateDir()+"-slack" {
		t.Fatalf("default service %q", got)
	}
}

func TestPostAllowlistFromConfig(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.cfg.Slack.PostAllowlist = []string{"U000"}
	te.saveToken(t, fakeToken())
	te.fakeChannel()
	if code := te.run("slack", "send", "--yes", "C0000000001", "hi"); code != 1 || !strings.Contains(te.err.String(), "allowlist") {
		t.Fatalf("CLI: exit %d: %s", code, te.err)
	}
	if n := len(te.srv.Calls("chat.postMessage")); n != 0 {
		t.Fatalf("CLI posted %d", n)
	}
	if got := te.slack.kit(te.cfg.Slack, nil).Send.PostAllowlist; !slices.Equal(got, []string{"U000"}) {
		t.Fatalf("CLI allowlist %q", got)
	}
	sender := wiredSlack(t, te, nil)
	if got := sender.kit.Send.PostAllowlist; !slices.Equal(got, []string{"U000"}) {
		t.Fatalf("daemon allowlist %q", got)
	}
}

func TestRequestLogFromConfig(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.saveToken(t, fakeToken())
	te.fakeChannel()
	var mu sync.Mutex
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	sender := wiredSlack(t, te, logf)
	if _, err := sender.Send(context.Background(), sendRequest("C0000000001", "hi")); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "slack_call") }) {
		t.Fatalf("request log while off: %q", lines)
	}

	te.cfg.Slack.RequestLog = true
	lines = nil
	sender = wiredSlack(t, te, logf)
	if _, err := sender.Send(context.Background(), sendRequest("C0000000001", "hi")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(lines, "slack_call method=chat.postMessage status=200") {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		assertNoSlackToken(t, l)
	}

	te.out.Reset()
	te.err.Reset()
	if code := te.run("slack", "send", "--yes", "C0000000001", "hi"); code != 0 || !strings.Contains(te.err.String(), "slack_call method=chat.postMessage status=200") {
		t.Fatalf("CLI: exit %d: %s", code, te.err)
	}
}

func TestDoctorSlackChecks(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.cfg.Slack.ClientID = ""
	if got := te.slackChecks(te.cfg); len(got) != 0 {
		t.Fatalf("unconfigured: %+v", got)
	}
	if len(te.kc.services) != 0 {
		t.Fatal("an unconfigured doctor read the keychain")
	}

	te.cfg.Slack.ClientID = testClientID
	got := te.slackChecks(te.cfg)
	if len(got) == 0 || got[0].mark != markMissing || !strings.Contains(got[0].fix, "slack login") {
		t.Fatalf("logged out: %+v", got)
	}
	te.saveToken(t, fakeToken())
	got = te.slackChecks(te.cfg)
	if got[0].mark != markOK || !strings.Contains(got[0].info, "Acme") {
		t.Fatalf("logged in: %+v", got)
	}
	for _, c := range got {
		assertNoSlackToken(t, c.info+c.fix)
	}
}

func TestSlackLogoutPastACorruptItem(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.kc.items["loop-test-slack/current"] = []byte("not a token")
	te.kc.items["loop-test-slack/app"] = []byte("xapp-1-fakeapp")
	if code := te.run("slack", "logout"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(te.kc.items) != 0 || len(te.srv.Calls("auth.revoke")) != 0 {
		t.Fatalf("items %d, revokes %d", len(te.kc.items), len(te.srv.Calls("auth.revoke")))
	}
	if !strings.Contains(te.out.String(), "skipped") {
		t.Fatalf("the skipped revoke went unsaid: %s", te.out)
	}
}

func TestAppTokenAtATerminalIsNotEchoed(t *testing.T) {
	t.Parallel()
	te := newSlackTestEnv(t)
	te.In = strings.NewReader("xapp-1-fromthepipe\n")
	te.slack.Interactive = func() bool { return true }
	te.slack.ReadSecret = func() ([]byte, error) { return []byte("xapp-1-hidden\n"), nil }
	if code := te.run("slack", "login", "--app-token"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if got := string(te.kc.items["loop-test-slack/app"]); got != "xapp-1-hidden" {
		t.Fatalf("app token %q, want the one read without echo", got)
	}
	if !strings.Contains(te.err.String(), "hidden") {
		t.Fatalf("no prompt: %s", te.err)
	}
	assertNoSlackToken(t, te.out.String()+te.err.String())
}

func TestSlackFlagErrorsDoNotEchoTheirValue(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"slack", "login", "--no-browser=xoxp-1-fakeflag"},
		{"slack", "login", "--xapp-1-fakeflag"},
		{"slack", "send", "--yes=xoxp-1-fakeflag", "#general", "hi"},
		{"slack", "send", "--xoxp-1-fakeflag", "#general", "hi"},
	} {
		te := newSlackTestEnv(t)
		if code := te.run(args...); code != 2 {
			t.Errorf("%q: exit %d, want a usage error", args, code)
		}
		if strings.Contains(te.out.String()+te.err.String(), "fakeflag") {
			t.Errorf("%q: echoed the flag: %s", args, te.err)
		}
	}
}

func TestSlackHelpFlagPrintsTheUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"slack", "login", "-h"}, {"slack", "send", "--help"}} {
		te := newSlackTestEnv(t)
		if code := te.run(args...); code != 0 {
			t.Errorf("%q: exit %d", args, code)
		}
		out := te.out.String() + te.err.String()
		if strings.Contains(out, "bad value") || !strings.Contains(out, " slack "+args[1]+" ") {
			t.Errorf("%q: %s", args, out)
		}
	}
}
