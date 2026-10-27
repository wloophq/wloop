package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	slackapi "github.com/wloophq/wloop/internal/slack"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/slacktest"
	sourcereg "github.com/wloophq/wloop/internal/source"
)

const (
	me            = "U0001"
	other         = "U0002"
	fakeUserToken = "xoxp-1-fakeuser"
	fakeAppToken  = "xapp-1-fakeapp"
)

type keychain struct {
	mu    sync.Mutex
	items map[string][]byte
	reads map[string]int
}

func (k *keychain) Get(service, account string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.reads == nil {
		k.reads = map[string]int{}
	}
	k.reads[account]++
	v, ok := k.items[service+"/"+account]
	if !ok {
		return nil, auth.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (k *keychain) readsOf(account string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reads[account]
}

func (k *keychain) Set(service, account string, secret []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.items[service+"/"+account] = append([]byte(nil), secret...)
	return nil
}

func (k *keychain) Delete(service, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.items, service+"/"+account)
	return nil
}

func (k *keychain) has(account string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.items["loop-test-slack/"+account]
	return ok
}

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

type fixture struct {
	db    *sqlite.DB
	log   *change.Log
	api   *slacktest.Server
	kc    *keychain
	logs  *logLines
	cfg   config.Config
	deps  sourcereg.Deps
	slack Slack
	p     *plugin
	mod   sourcereg.Module
	now   time.Time
}

type option func(*fixture)

func withConfig(fn func(*config.SlackIngest)) option {
	return func(f *fixture) { fn(&f.cfg.Slack.Ingest) }
}

func newFixture(t testing.TB, opts ...option) *fixture {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "state", "store.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, log: log, api: slacktest.New(t), kc: &keychain{items: map[string][]byte{}}, logs: &logLines{}, now: t0}
	f.cfg = config.Defaults()
	f.cfg.Slack.ClientID = "1.2"
	f.cfg.Slack.KeychainService = "loop-test-slack"
	f.api.Handle("users.info", func(v url.Values) any {
		return map[string]any{"user": map[string]any{"id": v.Get("user"), "name": "person"}}
	})
	f.deps = sourcereg.Deps{DB: db, Log: f.logs.logf, Links: db.Links(),
		Units: func() *unit.Registry { return unit.NewRegistry(dslack.Codec(), pullCodec{}) }}
	for _, o := range opts {
		o(f)
	}
	client := &slackapi.Client{BaseURL: f.api.URL}
	tokens := &auth.Store{Keychain: f.kc, Service: f.cfg.Slack.Service(), LockPath: filepath.Join(t.TempDir(), "slack.lock"),
		Client: client, ClientID: "1.2", Log: f.logs.logf, Now: func() time.Time { return t0 }}
	client.Tokens = tokens
	f.slack = Slack{Tokens: tokens, Client: client, Now: func() time.Time { return f.now }, Debounce: 50 * time.Millisecond}
	f.build(t)
	return f
}

func (f *fixture) build(t testing.TB) {
	t.Helper()
	f.p = newPlugin(f.cfg.Slack, f.deps, f.slack)
	f.mod = f.p.module()
}

// login stores both tokens and makes the plugin learn the viewer.
func (f *fixture) login(t testing.TB) {
	t.Helper()
	raw, _ := json.Marshal(auth.Token{Team: team, User: me, Access: fakeUserToken, Expires: t0.Add(12 * time.Hour)})
	f.kc.Set(f.cfg.Slack.Service(), "current", raw)
	f.kc.Set(f.cfg.Slack.Service(), "app", []byte(fakeAppToken))
	if skip, err := f.p.ready(context.Background()); err != nil || skip != "" {
		t.Fatalf("ready: %q %v", skip, err)
	}
}

// seed stores facts as a pass would.
func (f *fixture) seed(t testing.TB, fs dslack.Facts) {
	t.Helper()
	fs.Team = team
	if err := f.p.src.store.Save(context.Background(), nil, fs, "", f.now); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) knownUser(t testing.TB, id string, bot bool) {
	t.Helper()
	if err := f.db.Slack().PutUsers(context.Background(), []sqlite.UserRow{{ID: id, Name: "n", IsBot: bot, FetchedAt: f.now}}); err != nil {
		t.Fatal(err)
	}
}

// event is a socket payload of one event.
func event(id string, ev map[string]any) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"event_id": id, "type": "event_callback", "event": ev})
	return raw
}

func evMsg(conv, channelType, user, ts, text string, extra ...any) map[string]any {
	ev := map[string]any{"type": "message", "channel": conv, "channel_type": channelType, "ts": ts, "text": text}
	if user != "" {
		ev["user"] = user
	}
	for i := 0; i+1 < len(extra); i += 2 {
		ev[extra[i].(string)] = extra[i+1]
	}
	return ev
}

func delta(t testing.TB, payload json.RawMessage) dslack.Delta {
	t.Helper()
	d, ok, err := dslack.FromEvent(team, payload, dslack.ViaSocket, t0)
	if err != nil || !ok {
		t.Fatalf("FromEvent: ok=%v err=%v", ok, err)
	}
	return d
}

// push delivers the payload and touches what the inbox holds.
func (f *fixture) push(t testing.TB, payloads ...json.RawMessage) {
	t.Helper()
	ctx := context.Background()
	for _, p := range payloads {
		f.p.deliver(ctx, delta(t, p))
	}
	f.flush(t)
}

func (f *fixture) flush(t testing.TB) {
	t.Helper()
	objects := f.pendingObjects()
	if len(objects) == 0 {
		return
	}
	marks := map[string]uint64{}
	f.p.src.inbox.mu.Lock()
	for _, o := range objects {
		es := f.p.src.inbox.pending[o]
		marks[o] = es[len(es)-1].seq
	}
	f.p.src.inbox.mu.Unlock()
	err := f.p.touch(context.Background(), objects)
	if err != nil {
		t.Fatalf("touch %v: %v", objects, err)
	}
	f.p.src.inbox.settle(objects, marks, nil)
}

func (f *fixture) pendingObjects() []string {
	f.p.src.inbox.mu.Lock()
	defer f.p.src.inbox.mu.Unlock()
	var out []string
	for o := range f.p.src.inbox.pending {
		out = append(out, o)
	}
	return out
}

func (f *fixture) stored(t testing.TB, conv string) []sqlite.MessageRow {
	t.Helper()
	ms, err := f.db.Slack().Messages(context.Background(), conv, sqlite.MessageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func (f *fixture) object(t testing.TB, object string) (sqlite.ObjectRow, bool) {
	t.Helper()
	o, ok, err := f.db.Slack().Object(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	return o, ok
}

func (f *fixture) calls(methods ...string) map[string]int {
	out := map[string]int{}
	for _, m := range methods {
		if n := len(f.api.Calls(m)); n > 0 {
			out[m] = n
		}
	}
	return out
}

var apiMethods = []string{"conversations.replies", "conversations.history", "conversations.info", "users.info", "chat.getPermalink",
	"search.messages", "apps.connections.open"}

var _ port.Facts = facts{}

// pullCodec is a forge whose units are pull requests named by their
// address, as the GitHub plugin's codec names them.
type pullCodec struct{}

var pullAddress = regexp.MustCompile(`^https://github\.com/([^/\s]+)/([^/\s]+)/pull/([1-9][0-9]*)(?:[/?#].*)?$`)

func (pullCodec) Provider() unit.ProviderID { return unit.GitHub }
func (pullCodec) Valid(key string) error {
	if !strings.Contains(key, "#") {
		return fmt.Errorf("not a pull request key: %q", key)
	}
	return nil
}
func (pullCodec) URL(key string) string { return "" }
func (pullCodec) KeyOf(raw string) (string, bool) {
	m := pullAddress.FindStringSubmatch(raw)
	if m == nil {
		return "", false
	}
	return m[1] + "/" + m[2] + "#" + m[3], true
}
