package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/plugins/source/fakesource"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var thread1 = unit.Unit{Provider: ingesttest.Provider, Key: "1700000000.1"}

const discussIntent = `label = "Discuss"
summary = "Answer in a thread"
workspace = "given"
provider = "thread"
session = { strategy = "new", title = "discuss" }

[headline]
resumed = "discussion of {{.unit.ref}}"
[[headline.new]]
line = 'Discuss {{.unit.ref}}, {{template "started_by" .}}.'

[action]
surfaces = ["row", "cli"]

[record]
states = ["done", "asked", "failed"]
`

const discussBody = `# {{.Headline}}
{{if .Resume}}{{template "resume" .}}{{end}}
{{template "history" .}}
{{template "record" .}}{{template "brief" .}}`

// discussPrompt is the prompt a discuss turn types, with its round.
var discussPrompt = regexp.MustCompile("^Discuss thread 1700000000\\.1, asked for by you\\. Run `[^`]* instructions discuss --turn ([0-9a-f]{12}) thread:1700000000\\.1` and follow what it prints\\.$")

// withThreads registers the fake thread provider, with its turn payload,
// for the daemon a test starts afterwards.
func withThreads(t *testing.T, te *testEnv) {
	t.Helper()
	saved := plugins
	plugins = append(slices.Clone(plugins), threadModule(thread1))
	t.Cleanup(func() { plugins = saved })
	te.payloads = map[unit.ProviderID]turn.PayloadReader{ingesttest.Provider: fakesource.Payloads{}}
	userProfile(t, te, map[string]string{"intents/discuss/intent.toml": discussIntent, "intents/discuss/instructions.md.tmpl": discussBody})
}

// threadTurnEnv serves a daemon whose config places every thread in the
// main checkout of acme/api.
func threadTurnEnv(t *testing.T) (*testEnv, *fake.Manager) {
	t.Helper()
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	te := newTestEnv(t, m)
	te.Rec = storeRecorder{te.Store}
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	withThreads(t, te)
	te.serve(t)
	return te, m
}

func discussRound(t *testing.T, prompt string) string {
	t.Helper()
	m := discussPrompt.FindStringSubmatch(prompt)
	if m == nil {
		t.Fatalf("not a discuss prompt: %q", prompt)
	}
	return m[1]
}

func (te *testEnv) threadBook(t *testing.T) dturn.Book {
	t.Helper()
	books, err := te.repo.Books(context.Background(), thread1)
	if err != nil {
		t.Fatal(err)
	}
	return books[thread1]
}

func TestThreadTurnRunsWithoutGitHub(t *testing.T) {
	te, m := threadTurnEnv(t)
	out, err := client.New(te.Socket).Action(context.Background(), thread1, "discuss", api.ActionRequest{Guidance: "be brief"})
	if err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle != "term-ws-main" {
		t.Fatalf("prompts %+v, writes %q", m.Prompts, m.Writes)
	}
	round := discussRound(t, m.Prompts[0].Text)
	if !slices.Contains(out, "discuss of thread:1700000000.1 in api, "+verbNew) {
		t.Errorf("output %q", out)
	}
	text := storedText(t, te, round)
	if strings.Contains(text, "pull request") || !strings.Contains(text, "turn end thread:1700000000.1 --turn") {
		t.Errorf("instructions:\n%s", text)
	}
	b := te.threadBook(t)
	if len(b.Turns) != 1 || b.Turns[0].ID != round || b.Turns[0].Guidance != "be brief" || b.Turns[0].SentTo != te.sessionOf(t, "fake", "term-ws-main") {
		t.Fatalf("turns %+v", b.Turns)
	}
	ses, ok, err := te.repo.Session(context.Background(), b.Turns[0].SentTo)
	if err != nil || !ok || ses.Unit != thread1 || ses.WorkspaceID != "ws-main" {
		t.Fatalf("session %+v %v %v", ses, ok, err)
	}
	if len(te.gh.calls) != 0 {
		t.Errorf("GitHub calls %q", te.gh.calls)
	}
	if got := strings.Join(m.Writes, "\n"); strings.Contains(got, "link") || strings.Contains(got, "create") {
		t.Errorf("writes:\n%s", got)
	}
}

func TestAThreadWithoutPlacementRunsInTheNeutralRepository(t *testing.T) {
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
		Sessions:      map[string]string{"ws-main": "term-recent"},
	}
	te := newTestEnv(t, m)
	te.Rec = storeRecorder{te.Store}
	withThreads(t, te)
	te.serve(t)
	ctx := context.Background()
	if _, err := client.New(te.Socket).Action(ctx, thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	neutral := te.Env.neutralDir()
	writes := strings.Join(m.Writes, "\n")
	if !strings.Contains(writes, "repo add "+neutral) || !strings.Contains(writes, "create thread-1700000000.1") {
		t.Fatalf("writes:\n%s", writes)
	}
	if len(m.Prompts) != 1 || m.Prompts[0].Handle == "term-recent" {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	m.Writes = nil
	if _, err := client.New(te.Socket).Action(ctx, thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatalf("again: %v\n%s", err, te.err)
	}
	if got := strings.Join(m.Writes, "\n"); strings.Contains(got, "create") || strings.Contains(got, "repo add") {
		t.Fatalf("a second round created again:\n%s", got)
	}
}

func TestAPullRequestOnlyActionIsRefusedOnAThread(t *testing.T) {
	te, _ := threadTurnEnv(t)
	c := client.New(te.Socket)
	if _, err := c.Action(context.Background(), thread1, "take_over", api.ActionRequest{}); err == nil || !strings.Contains(err.Error(), "pull requests only") {
		t.Errorf("take_over: %v", err)
	}
	if _, err := c.Action(context.Background(), thread1, "review", api.ActionRequest{}); err == nil || !strings.Contains(err.Error(), "serves github units") {
		t.Errorf("review: %v", err)
	}
}

func TestInstructionsAcceptAUnitArgument(t *testing.T) {
	te, m := threadTurnEnv(t)
	if _, err := client.New(te.Socket).Action(context.Background(), thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatal(err)
	}
	round := discussRound(t, m.Prompts[0].Text)
	inSession(te, "term-ws-main")
	te.out.Reset()
	if code := te.run("instructions", "discuss", "--turn", round, "thread:1700000000.1"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); out != storedText(t, te, round) || !strings.Contains(out, "Discuss thread 1700000000.1") {
		t.Fatalf("%s", out)
	}
}

func TestMCPDialogBlocksAThreadTurn(t *testing.T) {
	defer func(d time.Duration) { mcpWait = d }(mcpWait)
	mcpWait = 50 * time.Millisecond
	te, m := threadTurnEnv(t)
	te.cfg.AgentHome = t.TempDir()
	m.Screen = mcpScreen
	te.reg.withoutMCP("term-ws-main")
	ctx := context.Background()
	c := client.New(te.Socket)
	_, err := c.Action(ctx, thread1, "discuss", api.ActionRequest{})
	if err == nil || !strings.Contains(err.Error(), "which MCP servers") || !strings.Contains(err.Error(), "it stays open for you to decide") {
		t.Fatalf("err = %v", err)
	}
	if len(m.Prompts) != 0 || strings.Contains(strings.Join(m.Writes, "\n"), "close") {
		t.Fatalf("prompts %+v, writes %q", m.Prompts, m.Writes)
	}
	blocks, err := te.Store.FirstRoundBlocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := inventory.StartBlockOf(blocks[thread1])
	if !ok || b.Question != claude.MCPQuestion || b.Round != "discuss" {
		t.Fatalf("blocks %+v: %+v %v", blocks, b, ok)
	}

	m.Screen = ""
	te.reg = &testRegistry{Registry: te.repo, Handles: te.repo, noMCP: map[string]bool{}}
	te.Turns.Registry = te.reg
	delete(m.Sessions, "ws-main")
	if _, err := c.Action(ctx, thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatalf("the round after the answer: %v", err)
	}
	if blocks, err = te.Store.FirstRoundBlocks(ctx); err != nil || len(blocks) != 0 {
		t.Fatalf("blocks after a passing round %+v %v", blocks, err)
	}
}

func TestThreadTurnResumes(t *testing.T) {
	te, m := threadTurnEnv(t)
	ctx := context.Background()
	if _, err := client.New(te.Socket).Action(ctx, thread1, "discuss", api.ActionRequest{Guidance: "be brief"}); err != nil {
		t.Fatal(err)
	}
	round := discussRound(t, m.Prompts[0].Text)
	id := te.sessionOf(t, "fake", m.Prompts[0].Handle)
	if _, err := te.Turns.Start(ctx, id, round); err != nil {
		t.Fatal(err)
	}
	rec := record.Record{State: record.Asked, Summary: "asked which answer to post", Resume: "post the answer the user picks",
		Question: &record.Question{Kind: "free", Text: "Post the short answer?"}}
	if _, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: id, Turn: round, Unit: thread1, Via: "socket", Record: rec}); err != nil {
		t.Fatal(err)
	}
	delete(m.Sessions, "ws-main")
	te.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	got, err := te.Turns.Answer(ctx, turns.AnswerRequest{Unit: thread1, Text: "yes, the short one", Source: dturn.Source("cli")})
	if err != nil || got.Route != dturn.Resumed {
		t.Fatalf("answer %+v %v\n%s", got, err, te.err)
	}
	b := te.threadBook(t)
	if len(b.Turns) != 2 || b.Turns[1].ResumeOf != b.Turns[0].Seq || b.Turns[1].Guidance != "be brief" || b.Turns[1].SentTo != te.sessionOf(t, "fake", "term-ws-main") {
		t.Fatalf("turns %+v", b.Turns)
	}
	if len(m.Prompts) != 2 || !strings.Contains(storedText(t, te, b.Turns[1].ID), "yes, the short one") {
		t.Fatalf("prompts %+v", m.Prompts)
	}
	if got := strings.Count(strings.Join(m.Writes, "\n"), "session ws-main"); got != 2 {
		t.Errorf("sessions started %d, writes %q", got, m.Writes)
	}
	if len(te.gh.calls) != 0 {
		t.Errorf("GitHub calls %q", te.gh.calls)
	}
}

// numberedSessions is the fake manager whose sessions each get their own
// terminal, live in sessions.
type numberedSessions struct {
	*fake.Manager
	sessions *turnstest.Sessions
	terms    []turns.Terminal
}

func (m *numberedSessions) StartAgentSession(ctx context.Context, req workspace.StartRequest) (string, error) {
	if _, err := m.Manager.StartAgentSession(ctx, req); err != nil {
		return "", err
	}
	h := fmt.Sprintf("term-%d", len(m.terms)+1)
	m.terms = append(m.terms, turns.Terminal{Session: h, Key: h, LastOutput: testNow})
	m.sessions.Put(req.WorkspaceID, m.terms...)
	return h, nil
}

func TestThreadsSharingAWorkspaceKeepTheirOwnSessions(t *testing.T) {
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	sessions := turnstest.NewSessions()
	ws := &numberedSessions{Manager: m, sessions: sessions}
	te := newTestEnv(t, ws)
	te.Turns.Sessions = turns.NewRouter(te.reg, handleSessions{m.Name(), sessions})
	te.Rec = storeRecorder{te.Store}
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	withThreads(t, te)
	userProfile(t, te, map[string]string{"intents/discuss/intent.toml": strings.Replace(discussIntent, `strategy = "new"`, `strategy = "reuse_live"`, 1), "intents/discuss/instructions.md.tmpl": discussBody})
	te.serve(t)
	thread2 := unit.Unit{Provider: ingesttest.Provider, Key: "1700000000.2"}
	c := client.New(te.Socket)
	for _, u := range []unit.Unit{thread1, thread2, thread1, thread2} {
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
	for i, u := range []unit.Unit{thread1, thread2, thread1, thread2} {
		if !strings.Contains(m.Prompts[i].Text, " "+u.String()+"`") {
			t.Errorf("prompt %d is not about %s: %q", i, u, m.Prompts[i].Text)
		}
	}
}

func TestAThreadRefusesPullRequestOnlyIntents(t *testing.T) {
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	te := newTestEnv(t, m)
	te.Rec = storeRecorder{te.Store}
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	withThreads(t, te)
	unoffered := strings.Replace(discussIntent, "[action]\nsurfaces = [\"row\", \"cli\"]\n", "", 1)
	sent := strings.Replace(discussIntent, `provider = "thread"`, "provider = \"thread\"\neffects = [\"plan.cleared\"]", 1)
	userProfile(t, te, map[string]string{
		"intents/quiet/intent.toml": unoffered, "intents/quiet/instructions.md.tmpl": discussBody,
		"intents/sent/intent.toml": sent, "intents/sent/instructions.md.tmpl": discussBody,
	})
	te.serve(t)
	c := client.New(te.Socket)
	if _, err := c.Action(context.Background(), thread1, "quiet", api.ActionRequest{}); err == nil || !strings.Contains(err.Error(), "no quiet on thread:1700000000.1") {
		t.Errorf("an intent offered on no surface: %v", err)
	}
	if _, err := c.Action(context.Background(), thread1, "sent", api.ActionRequest{}); err == nil || !strings.Contains(err.Error(), "effects plan.cleared, which act on pull requests only") {
		t.Errorf("an intent with effects: %v", err)
	}
	if len(m.Prompts) != 0 || len(m.Writes) != 0 {
		t.Fatalf("prompts %+v, writes %q", m.Prompts, m.Writes)
	}
	evs, err := te.Store.UnitlessEvents(context.Background())
	if err != nil || len(evs) != 0 {
		t.Fatalf("events under no unit %+v %v", evs, err)
	}
	blocks, err := te.Store.FirstRoundBlocks(context.Background())
	if err != nil || len(blocks) != 0 {
		t.Fatalf("blocks %+v %v", blocks, err)
	}
}

func TestASharedWorkspaceWaitingOnAPromptGetsANewSession(t *testing.T) {
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	sessions := turnstest.NewSessions()
	ws := &numberedSessions{Manager: m, sessions: sessions}
	te := newTestEnv(t, ws)
	te.Turns.Sessions = turns.NewRouter(te.reg, handleSessions{m.Name(), sessions})
	te.Rec = storeRecorder{te.Store}
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	withThreads(t, te)
	userProfile(t, te, map[string]string{"intents/discuss/intent.toml": strings.Replace(discussIntent, `strategy = "new"`, `strategy = "reuse_live"`, 1), "intents/discuss/instructions.md.tmpl": discussBody})
	te.serve(t)
	c := client.New(te.Socket)
	if _, err := c.Action(context.Background(), thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatal(err)
	}
	m.Activity = map[string]wsdomain.Activity{"ws-main": {Busy: true, Waiting: true}}
	if _, err := c.Action(context.Background(), thread1, "discuss", api.ActionRequest{}); err != nil {
		t.Fatalf("%v\n%s", err, te.err)
	}
	var handles []string
	for _, p := range m.Prompts {
		handles = append(handles, p.Handle)
	}
	if !slices.Equal(handles, []string{"term-1", "term-2"}) {
		t.Fatalf("prompts went to %q", handles)
	}
}

func TestADisabledThreadPluginRunsNoTurn(t *testing.T) {
	m := &fake.Manager{
		RepoList:      []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}},
		WorkspaceList: []wsdomain.Workspace{{ID: "ws-main", RepoID: "r1", Path: "/src/api", DisplayName: "api", HostID: wsdomain.LocalHost, IsMain: true}},
	}
	te := newTestEnv(t, m)
	te.Rec = storeRecorder{te.Store}
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	off := false
	te.cfg.Plugins.Other = map[string]config.Plugin{string(ingesttest.Provider): {Enabled: &off}}
	withThreads(t, te)
	te.serve(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", api.UnitPath(thread1, "discuss"), strings.NewReader("{}"))
	req.Header.Set(api.Header, api.Version)
	daemon.New(withRows(daemon.Deps{Turns: te.Turns, Actions: envActions{te.Env, te.Store}, Intents: te.loadedIntents, Cfg: te.cfg, Now: te.Now, Log: t.Logf}, te.Store)).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "thread is disabled") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(m.Prompts) != 0 || len(m.Writes) != 0 {
		t.Fatalf("prompts %+v, writes %q", m.Prompts, m.Writes)
	}
}

func TestTheSessionPlacerNamesWhereASessionRuns(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	ctx := context.Background()
	if got, err := (sessionPlacer{e: te.Env, cfg: te.cfg}).SessionPlace(ctx, thread1); err != nil || got != "a neutral directory with no checkout" {
		t.Fatalf("unplaced: %q %v", got, err)
	}
	te.cfg.Placement = []config.PlacementRule{{Match: "thread:*", Repo: "acme/api", Checkout: "main"}}
	if got, err := (sessionPlacer{e: te.Env, cfg: te.cfg}).SessionPlace(ctx, thread1); err != nil || got != "the main checkout of acme/api" {
		t.Fatalf("placed: %q %v", got, err)
	}
}

func TestAnIntentCommandTakesAUnitOfItsProvider(t *testing.T) {
	te, m := threadTurnEnv(t)
	if code := te.run("discuss", "thread:1700000000.1", "be", "brief"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(m.Prompts) != 1 || !strings.Contains(te.out.String(), "discuss thread:1700000000.1: sent") {
		t.Fatalf("prompts %+v, out %s", m.Prompts, te.out)
	}
	if b := te.threadBook(t); len(b.Turns) != 1 || b.Turns[0].Guidance != "be brief" {
		t.Fatalf("turns %+v", b.Turns)
	}
	te.err.Reset()
	if code := te.run("discuss", "nowhere:1"); code == 0 || !strings.Contains(te.err.String(), "names no unit") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}
