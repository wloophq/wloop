package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/echo"
	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/client/harness"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/record"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const (
	slackTeam    = "T0001"
	slackChannel = "C0000000001"
	slackAsker   = "U0002"
	slackParent1 = "1700000000.000100"
	slackParent2 = "1700000000.000300"
)

var (
	slackThread1 = dslack.ThreadUnit(slackTeam, slackChannel, slackParent1)
	slackThread2 = dslack.ThreadUnit(slackTeam, slackChannel, slackParent2)
)

const slackDiscussIntent = `label = "Discuss"
summary = "Answer in a Slack thread"
workspace = "given"
provider = "slack"
session = { strategy = "new", title = "discuss" }

[headline]
resumed = "discussion of {{.Wording.Ref}}"
[[headline.new]]
text = "Discuss {{.Wording.Ref}}, {{.Who}}."

[action]
surfaces = ["row", "cli"]

[record]
states = ["done", "asked", "failed"]
`

var slackDiscussPrompt = regexp.MustCompile("^Discuss #general thread [0-9.]+, asked for by you\\. Run `[^`]* instructions discuss --turn ([0-9a-f]{12}) slack:(\\S+)` and follow what it prints\\.$")

// slackContexts places every thread of #general in acme/api's main checkout
// through the Slack default: no rule matches the units' keys.
var slackContexts = []config.PlacementRule{{Match: "slack:#general", Repo: "acme/api", Checkout: "main", Role: "work"}}

func slackDiscussRound(t *testing.T, prompt string, u unit.Unit) string {
	t.Helper()
	m := slackDiscussPrompt.FindStringSubmatch(prompt)
	if m == nil || m[2] != u.Key {
		t.Fatalf("not a discuss prompt on %s: %q", u, prompt)
	}
	return m[1]
}

// seedSlack stores two threads of #general, each with a parent and a reply.
func seedSlack(t *testing.T, db *sqlite.DB) {
	t.Helper()
	ctx := context.Background()
	if err := db.Slack().PutUsers(ctx, []sqlite.UserRow{{ID: slackAsker, Name: "asker", FetchedAt: testNow}}); err != nil {
		t.Fatal(err)
	}
	st := &slacksource.Store{DB: db.Slack(), Now: func() time.Time { return testNow }}
	thread := func(parent, reply, text string) dslack.Thread {
		return dslack.Thread{Conversation: slackChannel, TS: parent, LatestReply: reply, ReplyCount: 1, Messages: []dslack.Message{
			{TS: parent, ThreadTS: parent, User: slackAsker, Text: text, Permalink: "https://example.slack.com/archives/" + slackChannel + "/p" + strings.ReplaceAll(parent, ".", "")},
			{TS: reply, ThreadTS: parent, User: "U0001", Text: "looking"},
		}}
	}
	facts := dslack.Facts{Team: slackTeam,
		Conversations: []dslack.Conversation{{ID: slackChannel, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{thread(slackParent1, "1700000000.000200", "first question"), thread(slackParent2, "1700000000.000400", "second question")}}
	if err := st.Save(ctx, nil, facts, "", testNow); err != nil {
		t.Fatal(err)
	}
}

// withSlackTurns gives te the Slack module's turn payload and placement over
// te's own store, seeded with two threads.
func withSlackTurns(t *testing.T, te *testEnv) {
	t.Helper()
	seedSlack(t, te.db)
	m, err := slacksource.Module(te.cfg, source.Deps{DB: te.db, Clock: clockFunc(te.Now)}, slacksource.Slack{})
	if err != nil {
		t.Fatal(err)
	}
	te.servePayloads([]source.Module{m})
	te.servePlacement([]source.Module{m})
}

func TestSharedWorkspaceKeepsSlackSessionsApart(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	sessions := turnstest.NewSessions()
	ws := &numberedSessions{Manager: m, sessions: sessions}
	te := newTestEnv(t, ws)
	te.Turns.Sessions = turns.NewRouter(te.reg, handleSessions{m.Name(), sessions})
	te.Rec = storeRecorder{te.Store}
	te.cfg.Placement = slackContexts
	withSlackTurns(t, te)
	userProfile(t, te, map[string]string{"intents/discuss/intent.toml": strings.Replace(slackDiscussIntent, `strategy = "new"`, `strategy = "reuse_live"`, 1),
		"intents/discuss/instructions.md.tmpl": discussBody})
	te.serve(t)
	c := client.New(te.Socket)
	order := []unit.Unit{slackThread1, slackThread2, slackThread1, slackThread2}
	for _, u := range order {
		if _, err := c.Action(context.Background(), u, "discuss", api.ActionRequest{}); err != nil {
			t.Fatalf("%s: %v\n%s", u, err, te.err)
		}
	}
	var handles []string
	for _, p := range m.Prompts {
		handles = append(handles, p.Handle)
	}
	if !slices.Equal(handles, []string{"term-1", "term-2", "term-1", "term-2"}) {
		t.Fatalf("prompts went to %q", handles)
	}
	for i, u := range order {
		round := slackDiscussRound(t, m.Prompts[i].Text, u)
		text := storedText(t, te, round)
		want := map[unit.Unit]string{slackThread1: "first question", slackThread2: "second question"}[u]
		if !strings.Contains(text, want) || strings.Contains(text, "pull request") {
			t.Errorf("prompt %d on %s: instructions\n%s", i, u, text)
		}
	}
	for _, h := range []string{"term-1", "term-2"} {
		ses, ok, err := te.repo.Session(context.Background(), te.sessionOf(t, "fake", h))
		want := map[string]unit.Unit{"term-1": slackThread1, "term-2": slackThread2}[h]
		if err != nil || !ok || ses.Unit != want || ses.WorkspaceID != "ws-main" {
			t.Fatalf("session %s: %+v %v %v", h, ses, ok, err)
		}
	}
}

func TestSlackTurnEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "user"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	echoHome := filepath.Join(root, ".echo")
	mkdir(t, filepath.Join(root, "user"), echoHome)

	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	te := newTestEnv(t, m)
	ws := connectingManager{m, te.Env}
	te.WS = workspace.NewSet(ws)
	env := map[string]string{}
	te.Getenv = func(k string) string { return env[k] }
	te.Agents = map[string]turns.Agent{"echo": &echo.Agent{}}
	te.Homes = map[string]setup.HomeKind{"echo": echo.HomeKind{Root: root}}
	te.Harness = harness.Set{
		SessionEnvs:  map[string]harness.SessionEnv{"fake": fake.SessionEnv{}},
		Agents:       map[string]harness.Protocol{"echo": echo.Protocol{}},
		DefaultAgent: "echo",
	}
	sessions := turnstest.NewSessions()
	sink := testsink.New("fake")
	te.Managers = map[string]ManagerEntry{"fake": {
		Display: "Fake", Binary: "true", SessionVar: fake.HandleVar, Sink: "fake",
		New: func() workspace.Manager { return ws },
		Ports: func(context.Context, workspace.Manager, *inventory.Store, config.Config, *change.Log) (managerSessions, []views.Sink, error) {
			return handleSessions{"fake", sessions}, []views.Sink{{ViewSink: sink, Placements: sink}}, nil
		},
	}}
	te.cfg.Agent = "echo"
	te.cfg.WorkspaceManagers = []string{"fake"}
	te.cfg = withoutWindows(te.cfg)
	te.cfg.Placement = slackContexts
	userProfile(t, te, map[string]string{"intents/discuss/intent.toml": slackDiscussIntent, "intents/discuss/instructions.md.tmpl": discussBody})

	db, err := sqlitetest.Open(filepath.Join(root, "state", storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedSlack(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); <-te.served.done }()
	te.Rounds = sqlite.NewRounds(db)
	deps := serveDeps(ctx, te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, &daemon.AppState{})
	te.Rec = storeRecorder{te.Store}
	if _, err := deps.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	deps.Startup = nil
	te.Socket = serveSocket(t, daemon.New(deps).Handler())

	run := func(args ...string) int {
		t.Helper()
		te.out.Reset()
		te.err.Reset()
		return Run(ctx, te.Env, args)
	}
	hook := func(sub, payload string) (int, string) {
		t.Helper()
		var code int
		withStdin(t, payload, func() { code = run("hook", sub, "--agent", "echo") })
		return code, te.out.String()
	}

	if _, err := client.New(te.Socket).Action(ctx, slackThread1, "discuss", api.ActionRequest{Guidance: "be brief"}); err != nil {
		t.Fatalf("discuss: %v\n%s", err, te.err)
	}
	if len(m.Prompts) != 1 || !slicesEqual(launches(m.Commands), []string{"cat"}) {
		t.Fatalf("discuss started %q and typed %+v", m.Commands, m.Prompts)
	}
	round := slackDiscussRound(t, m.Prompts[0].Text, slackThread1)
	handle := m.Prompts[0].Handle
	env[fake.HandleVar], env[fake.WorktreeVar], env[echo.SessionVar], env[echo.HomeVar] = handle, "ws-main", "s-1", echoHome

	if code, out := hook("session-start", `{"id":"s-1"}`); code != 0 || out != "" {
		t.Fatalf("session-start: exit %d: %q %s", code, out, te.err)
	}
	if code := run("instructions", "discuss", "--turn", round, slackThread1.String()); code != 0 {
		t.Fatalf("instructions: exit %d: %s%s", code, te.out, te.err)
	}
	instructions := te.out.String()
	for _, want := range []string{"### The Slack thread", "[m1 ts=" + slackParent1 + "] asker (" + slackAsker + ")", "first question",
		"https://example.slack.com/archives/" + slackChannel, "this Slack thread"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions lack %q:\n%s", want, instructions)
		}
	}
	if strings.Contains(instructions, "pull request") || strings.Contains(instructions, "second question") {
		t.Fatalf("instructions:\n%s", instructions)
	}
	if code, out := hook("stop", `{"id":"s-1","prompt":"p-1","last":"looked"}`); code != 0 || !strings.HasPrefix(out, "BLOCK ") {
		t.Fatalf("stop owing a record: exit %d: %q %s", code, out, te.err)
	}
	file := filepath.Join(root, "record.json")
	writeFile(t, file, `{"state":"asked","summary":"which answer to post","resume":"post the answer the user picks","question":{"kind":"free","text":"Post the short answer?"}}`)
	if code := run("turn", "end", slackThread1.String(), "--turn", round, "--file", file); code != 0 || !strings.Contains(te.out.String(), "Recorded asked for "+slackThread1.String()) {
		t.Fatalf("turn end: exit %d: %s%s", code, te.out, te.err)
	}
	b := slackBook(t, te, slackThread1)
	if len(b.Turns) != 1 || b.Turns[0].ID != round || b.Turns[0].State != dturn.Asked || b.Turns[0].StartedAt.IsZero() {
		t.Fatalf("turns %+v", b.Turns)
	}

	delete(m.Sessions, "ws-main")
	got, err := te.Turns.Answer(ctx, turns.AnswerRequest{Unit: slackThread1, Text: "yes, the short one", Source: dturn.Source("cli")})
	if err != nil || got.Route != dturn.Resumed {
		t.Fatalf("answer %+v %v\n%s", got, err, te.err)
	}
	b = slackBook(t, te, slackThread1)
	if len(b.Turns) != 2 || b.Turns[1].ResumeOf != b.Turns[0].Seq || b.Turns[1].Guidance != "be brief" {
		t.Fatalf("turns %+v", b.Turns)
	}
	if len(m.Prompts) != 2 || strings.Count(strings.Join(m.Writes, "\n"), "session ws-main") != 2 {
		t.Fatalf("prompts %+v, writes %q", m.Prompts, m.Writes)
	}
	resumed := storedText(t, te, b.Turns[1].ID)
	if !strings.Contains(resumed, "yes, the short one") || !strings.Contains(resumed, "first question") {
		t.Fatalf("resumed instructions:\n%s", resumed)
	}
	if len(te.gh.calls) != 0 {
		t.Errorf("GitHub calls %q", te.gh.calls)
	}
}

func slackBook(t *testing.T, te *testEnv, u unit.Unit) dturn.Book {
	t.Helper()
	books, err := te.Turns.Store.Books(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return books[u]
}

// promptAsker is a scripted headless asker that keeps each call's prompt.
type promptAsker struct {
	mu   sync.Mutex
	asks []turns.Ask
}

func (a *promptAsker) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asks = append(a.asks, q)
	raw, err := json.Marshal(map[string]any{"record": record.Record{State: record.Done, Summary: "nothing to post"}, "drafts": []any{}, "log": ""})
	return turns.Answer{JSON: raw}, err
}

func TestHeadlessPromptCarriesFacts(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	checkout := t.TempDir()
	te.cfg.Placement = []config.PlacementRule{{Match: "slack:#general", Checkout: checkout, Role: "work"}}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedSlack(t, db)
	ctx := context.Background()
	d := serveDeps(ctx, te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, &daemon.AppState{})
	if d.Startup != nil {
		if _, err := d.Startup(ctx); err != nil {
			t.Fatal(err)
		}
		d.Startup = nil
	}
	svc, ok := d.Headless.(*headless.Service)
	if !ok {
		t.Fatalf("headless %T", d.Headless)
	}
	a := &promptAsker{}
	svc.Asker = a
	svc.Go = func(run func()) { run() }
	te.Socket = serveSocket(t, daemon.New(d).Handler())
	if _, err := client.New(te.Socket).Action(ctx, slackThread2, "act", api.ActionRequest{}); err != nil {
		t.Fatalf("act: %v\n%s", err, te.err)
	}
	if len(a.asks) != 1 {
		t.Fatalf("%d calls", len(a.asks))
	}
	prompt := a.asks[0].Prompt
	for _, want := range []string{
		"You may read the checkout at " + checkout,
		"### The Slack thread",
		"[m1 ts=" + slackParent2 + "] asker (" + slackAsker + ")",
		"second question",
		"https://example.slack.com/archives/" + slackChannel + "/p" + strings.ReplaceAll(slackParent2, ".", ""),
		headless.Schema(),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "first question") {
		t.Fatalf("the prompt holds another unit's messages:\n%s", prompt)
	}
	if strings.Index(prompt, "second question") > strings.Index(prompt, headless.Schema()) {
		t.Fatal("the unit's messages follow the schema")
	}
	b := slackBook(t, te, slackThread2)
	if len(b.Turns) != 1 || b.Turns[0].Mode != dturn.ModeHeadless || b.Turns[0].State != dturn.Done {
		t.Fatalf("turns %+v", b.Turns)
	}
}

// TestAnActTurnReadsItsSlackThreadsBrief renders the brief the extraction
// wrote of the thread into an act turn.
func TestAnActTurnReadsItsSlackThreadsBrief(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	checkout := t.TempDir()
	te.cfg.Placement = []config.PlacementRule{{Match: "slack:#general", Checkout: checkout, Role: "work"}}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedSlack(t, db)
	te.cfg.Brief.Enabled = true
	if err := db.Extraction().Save(context.Background(), sqlite.ExtractionSave{States: map[unit.Unit]sqlite.ExtractionState{slackThread2: {
		Brief: `{"goal":"Answer the second question","kind":"question","risk":"low","tags":["domain:billing"]}`, At: testNow}}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d := serveDeps(ctx, te.Env, db, te.cfg, serveLog(t), &sync.Mutex{}, &daemon.AppState{})
	if d.Startup != nil {
		if _, err := d.Startup(ctx); err != nil {
			t.Fatal(err)
		}
		d.Startup = nil
	}
	svc, ok := d.Headless.(*headless.Service)
	if !ok {
		t.Fatalf("headless %T", d.Headless)
	}
	a := &promptAsker{}
	svc.Asker = a
	svc.Go = func(run func()) { run() }
	te.Socket = serveSocket(t, daemon.New(d).Handler())
	if _, err := client.New(te.Socket).Action(ctx, slackThread2, "act", api.ActionRequest{}); err != nil {
		t.Fatalf("act: %v\n%s", err, te.err)
	}
	if len(a.asks) != 1 {
		t.Fatalf("%d calls", len(a.asks))
	}
	prompt := a.asks[0].Prompt
	for _, want := range []string{"## Brief", "- Goal: Answer the second question", "- Kind `question`, risk `low`", "`domain:billing`"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
}
