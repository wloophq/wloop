package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/extract"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
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

// changes records the store's commits.
type changes struct {
	mu   sync.Mutex
	seen []store.Change
}

func (c *changes) add(ch store.Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, ch)
}

// gen is how many commits changed units so far.
func (c *changes) gen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

// noted is what the commits after a gen changed.
type noted struct {
	Commits int
	Units   []unit.Unit
	All     bool
}

func (n noted) Empty() bool { return n.Commits == 0 }

func (c *changes) since(gen int) noted {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out noted
	for _, ch := range c.seen[gen:] {
		out.Commits++
		out.Units = append(out.Units, ch.Units...)
		out.All = out.All || ch.All
	}
	slices.SortFunc(out.Units, unit.Compare)
	out.Units = slices.Compact(out.Units)
	return out
}

type fixture struct {
	db      *store.DB
	changes *changes
	host    *hosttest.Host
	api     *slacktest.Server
	kc      *keychain
	logs    *logLines
	cfg     config.Config
	slack   Slack
	p       *plugin
	now     time.Time
}

type option func(*fixture)

func withConfig(fn func(*config.SlackIngest)) option {
	return func(f *fixture) { fn(&f.cfg.Slack.Ingest) }
}

func newFixture(t testing.TB, opts ...option) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, changes: &changes{}, api: slacktest.New(t), kc: &keychain{items: map[string][]byte{}}, logs: &logLines{}, now: t0}
	db.OnChange(f.changes.add)
	f.host = &hosttest.Host{Now: func() time.Time { return f.now }, ProfileFiles: profilePrompts(t)}
	f.cfg = config.Config{Brand: "loop"}
	f.cfg.Slack.ClientID = "1.2"
	f.cfg.Slack.KeychainService = "loop-test-slack"
	f.api.Handle("users.info", func(v url.Values) any {
		return map[string]any{"user": map[string]any{"id": v.Get("user"), "name": "person"}}
	})
	for _, o := range opts {
		o(f)
	}
	client := &slackapi.Client{BaseURL: f.api.URL}
	tokens := &auth.Store{Keychain: f.kc, Service: f.cfg.Service(), LockPath: filepath.Join(t.TempDir(), "slack.lock"),
		Client: client, ClientID: "1.2", Log: f.logs.logf, Now: func() time.Time { return t0 }}
	client.Tokens = tokens
	f.slack = Slack{Tokens: tokens, Client: client, Now: func() time.Time { return f.now }, Debounce: 50 * time.Millisecond}
	f.build(t)
	return f
}

func (f *fixture) build(t testing.TB) {
	t.Helper()
	f.p = newPlugin(f.cfg, f.host.Broker(), f.db, f.slack, f.logs.logf)
}

// login stores both tokens and makes the plugin learn the viewer.
func (f *fixture) login(t testing.TB) {
	t.Helper()
	raw, _ := json.Marshal(auth.Token{Team: team, User: me, Access: fakeUserToken, Expires: t0.Add(12 * time.Hour)})
	f.kc.Set(f.cfg.Service(), "current", raw)
	f.kc.Set(f.cfg.Service(), "app", []byte(fakeAppToken))
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
	if err := f.db.Slack().PutUsers(context.Background(), []store.UserRow{{ID: id, Name: "n", IsBot: bot, FetchedAt: f.now}}); err != nil {
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

func (f *fixture) stored(t testing.TB, conv string) []store.MessageRow {
	t.Helper()
	ms, err := f.db.Slack().Messages(context.Background(), conv, store.MessageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func (f *fixture) object(t testing.TB, object string) (store.ObjectRow, bool) {
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

// profilePrompts are the extraction's prompts of the plugin's profile layer.
func profilePrompts(t testing.TB) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, f := range []string{extract.PromptFile, extract.BriefSchemaFile} {
		b, err := os.ReadFile(filepath.Join("profile", f))
		if err != nil {
			t.Fatal(err)
		}
		out[f] = b
	}
	return out
}
