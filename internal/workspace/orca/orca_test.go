package orca

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

type canned map[string]string

func (c canned) Run(_ context.Context, args ...string) ([]byte, error) {
	return []byte(c[strings.Join(args[:2], " ")]), nil
}

// Call answers a runtime method with the envelope its CLI command gets.
func (c canned) Call(_ context.Context, method string, _ any) ([]byte, error) {
	return []byte(c[strings.Replace(method, ".", " ", 1)]), nil
}

func (c canned) manager() *Manager { return &Manager{R: c, RPC: c} }

func TestLinkedPRShapes(t *testing.T) {
	for raw, want := range map[string]int{`7`: 7, `{"number":8,"state":"open"}`: 8, `null`: 0} {
		if got := linkedPR(json.RawMessage(raw)); got != want {
			t.Errorf("linkedPR(%s) = %d, want %d", raw, got, want)
		}
	}
}

func TestListNormalizesWorktrees(t *testing.T) {
	m := canned{"worktree list": `{"ok":true,"result":{"worktrees":[
		{"id":"a","repoId":"r","path":"/p","displayName":"x","branch":"refs/heads/me/x","workspaceStatus":"in-review","linkedPR":3,"pushTarget":{"branchName":"x"},"comment":"triage asks: keep both columns?"},
		{"id":"b","repoId":"r","path":"/q","displayName":"y","branch":"refs/heads/y","hostId":"ssh:box","linkedPR":null,"isMainWorktree":true}
	]}}`}.manager()
	got, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Branch != "me/x" || got[0].HeadBranch() != "x" || got[0].LinkedPR != 3 || got[0].HostID != "local" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].Comment != "triage asks: keep both columns?" {
		t.Errorf("comment = %q", got[0].Comment)
	}
	if got[1].HostID != "ssh:box" || !got[1].IsMain || got[1].HeadBranch() != "y" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestReposReadTheDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	head := filepath.Join(dir, ".git", "refs", "remotes", "origin", "HEAD")
	if err := os.MkdirAll(filepath.Dir(head), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(head, []byte("ref: refs/remotes/origin/develop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(dir)
	m := canned{"repo list": `{"ok":true,"result":{"repos":[
		{"id":"r","path":` + string(raw) + `,"executionHostId":"local","gitRemoteIdentity":{"canonicalKey":"github.com/acme/api"}},
		{"id":"s","path":` + string(raw) + `,"executionHostId":"ssh:box"},
		{"id":"c","path":` + string(raw) + `,"connectionId":"conn-1"}
	]}}`}.manager()
	got, err := m.Repos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].DefaultBranch != "develop" || got[0].RemoteKey != "github.com/acme/api" {
		t.Fatalf("repos = %+v", got)
	}
	if got[1].DefaultBranch != "" || got[2].DefaultBranch != "" {
		t.Fatalf("read a remote host's path locally: %+v", got[1:])
	}
}

func TestSetStatusFailureIsUnknownStatus(t *testing.T) {
	m := canned{"worktree set": `{"ok":false,"error":{"code":"invalid_argument","message":"unknown status"}}`}.manager()
	err := m.SetStatus(context.Background(), "a", "ready-to-merge")
	if !errors.Is(err, workspace.ErrUnknownStatus) {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentActivity(t *testing.T) {
	m := canned{"worktree ps": `{"ok":true,"result":{"worktrees":[
		{"worktreeId":"a","isActive":false,"agents":[{"state":"done"},{"state":"waiting"}]},
		{"worktreeId":"b","isActive":true,"agents":[{"state":"done"}]}
	]}}`}.manager()
	got, err := m.AgentActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got["a"].Busy || got["a"].UserActive || got["b"].Busy || !got["b"].UserActive {
		t.Fatalf("activity = %+v", got)
	}
}

func TestOrcaStatesMapToTheConstants(t *testing.T) {
	m := canned{"worktree ps": `{"ok":true,"result":{"worktrees":[
		{"worktreeId":"w","isActive":false,"agents":[{"state":"working"}]},
		{"worktreeId":"q","isActive":false,"agents":[{"state":"waiting"}]},
		{"worktreeId":"d","isActive":false,"agents":[{"state":"done"}]}
	]}}`}.manager()
	got, err := m.AgentActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]struct {
		state         turn.AgentState
		busy, waiting bool
	}{
		"w": {turn.AgentWorking, true, false},
		"q": {turn.AgentWaiting, true, true},
		"d": {turn.AgentDone, false, false},
	} {
		a := got[id]
		if !slices.Equal(a.States, []turn.AgentState{want.state}) || a.Busy != want.busy || a.Waiting != want.waiting {
			t.Errorf("%s: activity %+v", id, a)
		}
	}
}

func TestUnknownOrcaStatePassesThroughVerbatim(t *testing.T) {
	m := canned{"worktree ps": `{"ok":true,"result":{"worktrees":[
		{"worktreeId":"a","isActive":false,"agents":[{"paneKey":"tab-1:leaf-1","state":"idle"}]}
	]}}`}.manager()
	act, err := m.AgentActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a := act["a"]; len(a.States) != 1 || a.States[0].String() != "idle" || a.Busy || a.Waiting {
		t.Fatalf("activity %+v", a)
	}
	agents, err := m.Agents(context.Background())
	if err != nil || len(agents) != 1 || agents[0].State.String() != "idle" {
		t.Fatalf("agents %+v, %v", agents, err)
	}
}

func TestLiveAgentSessionPicksTheMostRecent(t *testing.T) {
	m := canned{"terminal list": `{"ok":true,"result":{"terminals":[
		{"handle":"old","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":1},
		{"handle":"new","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":2},
		{"handle":"gone","agentIdentity":"claude","connected":true,"writable":true,"orphaned":true,"lastOutputAt":3},
		{"handle":"shell","agentIdentity":"","connected":true,"writable":true,"lastOutputAt":4}
	]}}`}.manager()
	h, ok, err := m.LiveAgentSession(context.Background(), "a", "claude")
	if err != nil || !ok || h.Handle != "new" || h.LastOutput.UnixMilli() != 2 {
		t.Fatalf("got %s %v %v", h, ok, err)
	}
}

func TestLiveAgentSessionMapsTheKindToOrcasIdentity(t *testing.T) {
	list := canned{"terminal list": `{"ok":true,"result":{"terminals":[
		{"handle":"gem","agentIdentity":"gemini-cli","connected":true,"writable":true,"lastOutputAt":1},
		{"handle":"cl","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":2}
	]}}`}
	m := &Manager{R: list, RPC: list, Identities: map[string]string{"gem": "gemini-cli"}}
	if h, ok, err := m.LiveAgentSession(context.Background(), "a", "gem"); err != nil || !ok || h.Handle != "gem" {
		t.Fatalf("gem: %s %v %v", h, ok, err)
	}
	if h, ok, err := list.manager().LiveAgentSession(context.Background(), "a", "claude"); err != nil || !ok || h.Handle != "cl" {
		t.Fatalf("claude: %s %v %v", h, ok, err)
	}
}

func TestTrustsAtLaunchMatchesOrcasClaudeProfileHomes(t *testing.T) {
	m := &Manager{}
	for home, want := range map[string]bool{
		"/Users/me/Library/Application Support/orca/claude-profiles/p0/home": true,
		"/Users/me/Library/Application Support/orca/claude-profiles/p0":      false,
		"/Users/me/.claude":           false,
		"/x/claude-profiles/abc/home": false,
	} {
		if got := m.TrustsAtLaunch("claude", home); got != want {
			t.Errorf("TrustsAtLaunch(%s) = %v", home, got)
		}
	}
}

func TestTrustsAtLaunchIsPerKind(t *testing.T) {
	m := &Manager{}
	claudeHome := "/Users/me/Library/Application Support/orca/claude-profiles/p1/home"
	codexHome := "/Users/me/Library/Application Support/orca/codex-profiles/p1/home"
	if !m.TrustsAtLaunch("claude", claudeHome) || m.TrustsAtLaunch("codex", claudeHome) || !m.TrustsAtLaunch("codex", codexHome) || m.TrustsAtLaunch("claude", codexHome) {
		t.Fatal("TrustsAtLaunch matched a profile home of another kind")
	}
}

func TestProfileHomesFollowTheKind(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Orca's default user data directory is under Library on macOS only")
	}
	t.Setenv("ORCA_USER_DATA_PATH", "")
	user := t.TempDir()
	orca := filepath.Join(user, "Library", "Application Support", "orca")
	for _, d := range []string{"claude-profiles/b/home", "claude-profiles/a/home", "claude-profiles/c", "codex-profiles/x/home"} {
		if err := os.MkdirAll(filepath.Join(orca, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{}
	if got, want := m.ProfileHomes(user, "claude"), []string{filepath.Join(orca, "claude-profiles/a/home"), filepath.Join(orca, "claude-profiles/b/home")}; !slices.Equal(got, want) {
		t.Errorf("claude: %q, want %q", got, want)
	}
	if got, want := m.ProfileHomes(user, "codex"), []string{filepath.Join(orca, "codex-profiles/x/home")}; !slices.Equal(got, want) {
		t.Errorf("codex: %q, want %q", got, want)
	}
	if got := m.ProfileHomes(user, "echo"); len(got) != 0 {
		t.Errorf("echo: %q", got)
	}
	named := &Manager{Identities: map[string]string{"gem": "codex"}}
	home := filepath.Join(orca, "codex-profiles/x/home")
	if got := named.ProfileHomes(user, "gem"); !slices.Equal(got, []string{home}) || !named.TrustsAtLaunch("gem", home) || named.TrustsAtLaunch("codex", filepath.Join(orca, "gem-profiles/x/home")) {
		t.Errorf("a kind Orca names otherwise: %q", got)
	}
}

func TestProfileHomesHonorOrcaUserDataPath(t *testing.T) {
	user := t.TempDir()
	if err := os.MkdirAll(filepath.Join(user, "Library", "Application Support", "orca", "claude-profiles", "real", "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	t.Setenv("ORCA_USER_DATA_PATH", filepath.Join(other, "absent"))
	m := &Manager{}
	if got := m.ProfileHomes(user, "claude"); len(got) != 0 {
		t.Errorf("an absent ORCA_USER_DATA_PATH still lists %q", got)
	}
	home := filepath.Join(other, "claude-profiles", "p", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORCA_USER_DATA_PATH", other)
	if got := m.ProfileHomes(user, "claude"); !slices.Equal(got, []string{home}) {
		t.Errorf("ProfileHomes = %q, want %q", got, home)
	}
}

func TestReadScreenReadsTheRenderedScreen(t *testing.T) {
	r := &recorder{canned: canned{"terminal read": `{"ok":true,"result":{"terminal":{"handle":"term_x","status":"running","source":"screen","tail":["Allow external CLAUDE.md file imports?","> 1. Yes"],"nextCursor":null}}}`}}
	got, err := (&Manager{R: r, RPC: r}).ReadScreen(context.Background(), "term_x")
	if err != nil || got != "Allow external CLAUDE.md file imports?\n> 1. Yes" {
		t.Fatalf("ReadScreen = %q, %v", got, err)
	}
	if len(r.calls) != 1 || r.calls[0] != `rpc terminal.read {"screen":true,"terminal":"term_x"}` {
		t.Errorf("calls = %q", r.calls)
	}
}

func TestStartAgentSessionNamesWhatTheTerminalWaitsOn(t *testing.T) {
	created := `{"ok":true,"result":{"terminal":{"handle":"term_x"}}}`
	m := canned{"terminal create": created,
		"terminal wait": `{"ok":true,"result":{"wait":{"handle":"term_x","condition":"tui-idle","satisfied":false,"status":"running","exitCode":null,"blockedReason":"agent-trust-workspace"}}}`}.manager()
	h, err := m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: "a", Command: "claude", Title: "review"})
	if err == nil || !strings.Contains(err.Error(), "agent-trust-workspace") || !strings.Contains(err.Error(), "term_x") {
		t.Fatalf("StartAgentSession = %q, %v", h, err)
	}
	m = canned{"terminal create": created,
		"terminal wait": `{"ok":true,"result":{"wait":{"handle":"term_x","condition":"tui-idle","satisfied":true,"status":"running","exitCode":null}}}`}.manager()
	if h, err := m.StartAgentSession(context.Background(), workspace.StartRequest{WorkspaceID: "a", Command: "claude", Title: "review"}); err != nil || h != "term_x" {
		t.Fatalf("StartAgentSession = %q, %v", h, err)
	}
}

func TestWaitIdleReadsWhetherThePromptCameBack(t *testing.T) {
	for want, satisfied := range map[bool]string{true: "true", false: "false"} {
		r := &recorder{canned: canned{"terminal wait": `{"ok":true,"result":{"wait":{"handle":"term_x","condition":"tui-idle","satisfied":` + satisfied + `,"status":"running","exitCode":null}}}`}}
		got, err := (&Manager{R: r, RPC: r}).WaitIdle(context.Background(), "term_x", 15*time.Second)
		if err != nil || got != want {
			t.Fatalf("WaitIdle = %v, %v; want %v", got, err, want)
		}
		if len(r.calls) != 1 || r.calls[0] != `rpc terminal.wait {"for":"tui-idle","terminal":"term_x","timeoutMs":15000}` {
			t.Errorf("calls = %q", r.calls)
		}
	}
}

func TestSendPromptObservesTurnStart(t *testing.T) {
	cases := map[string]struct {
		receipt string
		queued  bool
		want    bool
	}{
		"turn started":        {`{"ok":true,"result":{"send":{"handle":"term_x","accepted":true,"bytesWritten":1443,"prompt":{"requestId":"df1e3f3f","stages":["input_accepted","turn_started"],"provider":"claude","observation":"supported","generation":15}}}}`, false, true},
		"input accepted only": {`{"ok":true,"result":{"send":{"handle":"term_x","accepted":true,"bytesWritten":1443,"prompt":{"requestId":"df1e3f3f","stages":["input_accepted"],"provider":"claude","observation":"supported","generation":15}}}}`, false, false},
		"queued mid-turn":     {`{"ok":true,"result":{"send":{"handle":"term_x","accepted":true,"bytesWritten":1443,"prompt":{"requestId":"df1e3f3f","stages":["input_accepted"],"provider":"claude","observation":"supported","generation":15}}}}`, true, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := canned{"terminal send": c.receipt}.manager()
			got, err := m.SendPrompt(context.Background(), "term_x", "hi", c.queued)
			if err != nil || got != c.want {
				t.Fatalf("SendPrompt = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

type fakeRPC struct {
	calls   []string
	params  map[string]map[string]any
	replies map[string]string
	fail    error
}

func (f *fakeRPC) Call(_ context.Context, method string, params any) ([]byte, error) {
	f.calls = append(f.calls, method)
	if f.fail != nil {
		return nil, f.fail
	}
	if f.params == nil {
		f.params = map[string]map[string]any{}
	}
	f.params[method], _ = params.(map[string]any)
	return []byte(f.replies[method]), nil
}

func TestCreateFromPRNamesTheBranchAfterTheHead(t *testing.T) {
	rpc := &fakeRPC{replies: map[string]string{
		"worktree.resolvePrBase": `{"ok":true,"result":{"baseBranch":"abc","compareBaseRef":"refs/remotes/origin/main","branchNameOverride":"feat/x","pushTarget":{"remoteName":"origin","branchName":"feat/x"}}}`,
		"worktree.create":        `{"ok":true,"result":{"worktree":{"id":"r1::/wt/REVIEW-feat-x","path":"/wt/REVIEW-feat-x","displayName":"REVIEW feat/x","branch":"refs/heads/feat/x"},"agentTerminalHandle":"term-1"}}`,
	}}
	m := &Manager{R: canned{}, RPC: rpc}
	ws, handle, err := m.Create(context.Background(), workspace.CreateRequest{
		RepoID: "r1", Name: "REVIEW feat/x", Unit: prUnit(7), Agent: "claude", Prompt: "/skill url",
		HeadBranch: "feat/x", BaseBranchName: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Branch != "feat/x" || handle != "term-1" {
		t.Fatalf("ws = %+v, handle = %q", ws, handle)
	}
	p := rpc.params["worktree.create"]
	if p["branchNameOverride"] != "feat/x" || p["displayName"] != "REVIEW feat/x" || p["linkedPR"] != 7 || p["startupAgent"] != "claude" || p["noParent"] != true {
		t.Fatalf("create params = %v", p)
	}
	if p["pushTarget"] == nil {
		t.Fatalf("push target dropped: %v", p)
	}
}

func TestCreateMarksARenamedBranch(t *testing.T) {
	for branch, want := range map[string]bool{"feat/x": false, "feat/x-2": true, "feat/x-12": true, "feat/x-y": false, "feat/xy": false} {
		rpc := &fakeRPC{replies: map[string]string{
			"worktree.resolvePrBase": `{"ok":true,"result":{"baseBranch":"abc","branchNameOverride":"feat/x"}}`,
			"worktree.create":        `{"ok":true,"result":{"worktree":{"id":"r1::/wt/x","path":"/wt/x","displayName":"REVIEW feat/x","branch":"refs/heads/` + branch + `"}}}`,
		}}
		ws, _, err := (&Manager{R: canned{}, RPC: rpc}).Create(context.Background(), workspace.CreateRequest{RepoID: "r1", Name: "REVIEW feat/x", Unit: prUnit(7), HeadBranch: "feat/x"})
		if err != nil || ws.BranchRenamed != want {
			t.Errorf("branch %s: renamed %v, err %v", branch, ws.BranchRenamed, err)
		}
	}
}

func TestCreateFallsBackToTheCLIWhenRPCFails(t *testing.T) {
	m := &Manager{
		R:   canned{"worktree create": `{"ok":true,"result":{"worktree":{"id":"r1::/wt/x","path":"/wt/x","displayName":"REVIEW feat/x"}}}`},
		RPC: &fakeRPC{fail: errors.New("orca app layout not found")},
	}
	ws, _, err := m.Create(context.Background(), workspace.CreateRequest{RepoID: "r1", Name: "REVIEW feat/x", Unit: prUnit(7), HeadBranch: "feat/x"})
	if err != nil || ws.Path != "/wt/x" {
		t.Fatalf("ws = %+v, err = %v", ws, err)
	}
}

type recorder struct {
	canned
	calls []string
}

func (r *recorder) Run(ctx context.Context, args ...string) ([]byte, error) {
	argv := strings.Join(args, " ")
	r.calls = append(r.calls, argv)
	return r.canned.Run(ctx, args...)
}

// Call records "rpc <method> <params as JSON>".
func (r *recorder) Call(ctx context.Context, method string, params any) ([]byte, error) {
	raw, _ := json.Marshal(params)
	r.calls = append(r.calls, "rpc "+method+" "+string(raw))
	return r.canned.Call(ctx, method, params)
}

const listWT = `rpc terminal.list {"includeVisualLayouts":false,"worktree":"id:r1::/wt/x"}`

func TestFocusSwitchesToTheLiveAgentTerminal(t *testing.T) {
	r := &recorder{canned: canned{
		"terminal list": `{"ok":true,"result":{"terminals":[
			{"handle":"shell","agentIdentity":"","connected":true,"writable":true,"lastOutputAt":9},
			{"handle":"agent","agentIdentity":"codex","connected":true,"writable":true,"lastOutputAt":2}
		]}}`,
		"terminal switch": `{"ok":true,"result":{}}`,
	}}
	if err := (&Manager{R: r, RPC: r}).Focus(context.Background(), "r1::/wt/x"); err != nil {
		t.Fatal(err)
	}
	want := []string{listWT, "terminal switch --terminal agent --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestFocusSwitchesToAnyTerminalWithoutAnAgent(t *testing.T) {
	r := &recorder{canned: canned{
		"terminal list": `{"ok":true,"result":{"terminals":[
			{"handle":"gone","agentIdentity":"","connected":false,"writable":true,"lastOutputAt":9},
			{"handle":"shell","agentIdentity":"","connected":true,"writable":true,"lastOutputAt":2}
		]}}`,
		"terminal switch": `{"ok":true,"result":{}}`,
	}}
	if err := (&Manager{R: r, RPC: r}).Focus(context.Background(), "r1::/wt/x"); err != nil {
		t.Fatal(err)
	}
	want := []string{listWT, "terminal switch --terminal shell --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestFocusOpensTheReadmeWithoutATerminal(t *testing.T) {
	r := &recorder{canned: canned{
		"terminal list": `{"ok":true,"result":{"terminals":[
			{"handle":"orphan","agentIdentity":"","connected":true,"writable":true,"orphaned":true,"lastOutputAt":9}
		]}}`,
		"file open": `{"ok":true,"result":{}}`,
	}}
	if err := (&Manager{R: r, RPC: r}).Focus(context.Background(), "r1::/wt/x"); err != nil {
		t.Fatal(err)
	}
	want := []string{listWT, "file open --path README.md --worktree id:r1::/wt/x --focus --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestFocusFailsWithoutATerminalOrAReadme(t *testing.T) {
	r := &recorder{canned: canned{
		"terminal list": `{"ok":true,"result":{"terminals":[]}}`,
		"file open":     `{"ok":false,"error":{"code":"runtime_error","message":"ENOENT: no such file or directory, open '/wt/x/README.md'"}}`,
	}}
	err := (&Manager{R: r, RPC: r}).Focus(context.Background(), "r1::/wt/x")
	if err == nil || !strings.Contains(err.Error(), "ENOENT") {
		t.Fatalf("err = %v", err)
	}
	want := []string{listWT, "file open --path README.md --worktree id:r1::/wt/x --focus --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestFocusKeepsTheFirstErrorWhenTheReadmeExists(t *testing.T) {
	r := &recorder{canned: canned{
		"terminal list": `{"ok":true,"result":{"terminals":[]}}`,
		"file open":     `{"ok":false,"error":{"code":"selector_not_found","message":"selector_not_found"}}`,
	}}
	err := (&Manager{R: r, RPC: r}).Focus(context.Background(), "r1::/wt/x")
	if err == nil || !strings.Contains(err.Error(), "README.md") || !strings.Contains(err.Error(), "selector_not_found") {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestRemoveDeletesTheWorktreeWithoutForce(t *testing.T) {
	r := &recorder{canned: canned{"worktree rm": `{"ok":true,"result":{}}`}}
	m := &Manager{R: r, RPC: r}
	if err := m.Remove(context.Background(), "r1::/wt/x", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(context.Background(), "r1::/wt/x", true); err != nil {
		t.Fatal(err)
	}
	want := []string{"worktree rm --worktree id:r1::/wt/x --json", "worktree rm --worktree id:r1::/wt/x --force --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls %q", r.calls)
	}
}

func TestUnregisterRemovesTheRegistrationWithoutForce(t *testing.T) {
	r := &recorder{canned: canned{"worktree rm": `{"ok":true,"result":{}}`}}
	m := &Manager{R: r, RPC: r}
	if err := m.Unregister(context.Background(), "r1::/wt/x"); err != nil {
		t.Fatal(err)
	}
	want := []string{"worktree rm --worktree id:r1::/wt/x --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls %q", r.calls)
	}
}

func TestRemoveFailureCarriesOrcasMessage(t *testing.T) {
	msg := "Failed to physically stop every PTY for worktree: r1::/wt/x \u2014 the terminal sweep failed: connect ENOENT /Users/x/Library/Application Support/orca/daemon/daemon-v37.sock. Retry with force delete (--force) to remove it anyway."
	m := canned{"worktree rm": `{"ok":false,"error":{"code":"runtime_error","message":"` + msg + `"}}`}.manager()
	err := m.Remove(context.Background(), "r1::/wt/x", false)
	var detailed interface{ Detail() string }
	if !errors.As(err, &detailed) || !strings.Contains(detailed.Detail(), msg) {
		t.Fatalf("err = %v", err)
	}
}

func TestTerminalsKeepOnlyLiveOnes(t *testing.T) {
	m := canned{"terminal list": `{"ok":true,"result":{"terminals":[
		{"handle":"term_1","tabId":"tab-1","title":"` + brand.Command + ` review","agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":1760745600000},
		{"handle":"term_2","tabId":"tab-2","title":"zsh","connected":true,"writable":true},
		{"handle":"gone","tabId":"tab-3","agentIdentity":"claude","connected":true,"writable":true,"orphaned":true,"lastOutputAt":3}
	]}}`}.manager()
	got, err := m.Terminals(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Terminal{Handle: "term_1", TabID: "tab-1", Title: brand.Command + " review", Agent: "claude", LastOutput: time.UnixMilli(1760745600000)}) {
		t.Fatalf("terminals = %+v", got)
	}
	if !got[1].LastOutput.IsZero() {
		t.Fatalf("a terminal that never printed has last output %v", got[1].LastOutput)
	}
}

func TestAgentsCarryThePaneAndTheWorkspace(t *testing.T) {
	m := canned{"worktree ps": `{"ok":true,"result":{"worktrees":[
		{"worktreeId":"a","isActive":false,"agents":[{"paneKey":"tab-1:leaf-1","state":"done","lastAssistantMessage":"Done.","stateStartedAt":1760745600000}]},
		{"worktreeId":"b","isActive":true,"agents":[{"paneKey":"tab-2:leaf-1","state":"waiting"}]},
		{"worktreeId":"c","isActive":false,"agents":[]}
	]}}`}.manager()
	got, err := m.Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Agent{
		{WorkspaceID: "a", Pane: "tab-1:leaf-1", State: turn.AgentDone, StateSince: time.UnixMilli(1760745600000), LastMessage: "Done."},
		{WorkspaceID: "b", Pane: "tab-2:leaf-1", State: turn.AgentWaiting, UserActive: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("agents = %+v", got)
	}
}

func TestSetCardOneCall(t *testing.T) {
	for _, c := range []struct {
		status, comment string
		unread          bool
		want            string
	}{
		{"needs-me", "asks: keep both?", true, `rpc worktree.set {"comment":"asks: keep both?","isUnread":true,"noParent":false,"workspaceStatus":"needs-me","worktree":"id:a"}`},
		{"", "commented", false, `rpc worktree.set {"comment":"commented","noParent":false,"worktree":"id:a"}`},
		{"reviewed", "", false, `rpc worktree.set {"noParent":false,"workspaceStatus":"reviewed","worktree":"id:a"}`},
	} {
		r := &recorder{canned: canned{"worktree set": `{"ok":true,"result":{}}`}}
		if err := (&Manager{R: r, RPC: r}).SetCard(context.Background(), "a", c.status, c.comment, c.unread); err != nil {
			t.Fatal(err)
		}
		if len(r.calls) != 1 || r.calls[0] != c.want {
			t.Errorf("calls = %q, want %q", r.calls, c.want)
		}
	}
	r := &recorder{}
	if err := (&Manager{R: r, RPC: r}).SetCard(context.Background(), "a", "", "", false); err != nil || len(r.calls) != 0 {
		t.Fatalf("an empty card write ran %q: %v", r.calls, err)
	}
}

func TestSetCardFailureIsUnknownStatus(t *testing.T) {
	m := canned{"worktree set": `{"ok":false,"error":{"code":"invalid_argument","message":"unknown status"}}`}.manager()
	if err := m.SetCard(context.Background(), "a", "reviewing", "c", false); !errors.Is(err, workspace.ErrUnknownStatus) {
		t.Fatalf("err = %v", err)
	}
	if err := m.SetCard(context.Background(), "a", "", "c", false); err == nil || errors.Is(err, workspace.ErrUnknownStatus) {
		t.Fatalf("a comment-only failure: %v", err)
	}
	other := canned{"worktree set": `{"ok":false,"error":{"code":"bad_request","message":"no column reviewing"}}`}.manager()
	if err := other.SetCard(context.Background(), "a", "reviewing", "", true); !errors.Is(err, workspace.ErrUnknownStatus) {
		t.Fatalf("another code for a status write: %v", err)
	}
	gone := canned{"worktree set": `{"ok":false,"error":{"code":"not_found","message":"no such worktree"}}`}.manager()
	if err := gone.SetCard(context.Background(), "a", "reviewing", "c", false); err == nil || errors.Is(err, workspace.ErrUnknownStatus) {
		t.Fatalf("a missing worktree: %v", err)
	}
}

const createdWT = `{"ok":true,"result":{"worktree":{"id":"r1::/wt/fix-login","path":"/wt/fix-login","displayName":"fix-login"}}}`

func TestCreateFromTheDefaultBaseOmitsTheBaseBranch(t *testing.T) {
	r := &recorder{canned: canned{"worktree create": createdWT}}
	if _, _, err := (&Manager{R: r}).Create(context.Background(), workspace.CreateRequest{RepoID: "r1", Name: "fix-login", BaseRef: "origin/pr/3", DefaultBase: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"worktree create --repo id:r1 --name fix-login --no-parent --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestCreateUnderAListedParent(t *testing.T) {
	r := &recorder{canned: canned{
		"worktree list":   `{"ok":true,"result":{"worktrees":[{"id":"r0::/wt/lead","repoId":"r0","path":"/wt/lead"}]}}`,
		"worktree create": createdWT,
	}}
	ws, _, err := (&Manager{R: r}).Create(context.Background(), workspace.CreateRequest{RepoID: "r1", Name: "fix-login", Parent: "/wt/lead/", DefaultBase: true})
	if err != nil || ws.ParentDropped {
		t.Fatalf("ws = %+v, err = %v", ws, err)
	}
	want := []string{"worktree list --limit 1000 --json", "worktree create --repo id:r1 --name fix-login --parent-worktree path:/wt/lead --json"}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q", r.calls)
	}
}

func TestCreateDropsAParentOrcaDoesNotList(t *testing.T) {
	r := &recorder{canned: canned{
		"worktree list":   `{"ok":true,"result":{"worktrees":[{"id":"r0::/wt/lead","repoId":"r0","path":"/wt/lead"}]}}`,
		"worktree create": createdWT,
	}}
	ws, _, err := (&Manager{R: r}).Create(context.Background(), workspace.CreateRequest{RepoID: "r1", Name: "fix-login", Parent: "/elsewhere/ana"})
	if err != nil || !ws.ParentDropped {
		t.Fatalf("ws = %+v, err = %v", ws, err)
	}
	if got := r.calls[len(r.calls)-1]; got != "worktree create --repo id:r1 --name fix-login --no-parent --json" {
		t.Fatalf("create = %q", got)
	}
}

func TestCreateFromPRDropsTheParent(t *testing.T) {
	rpc := &fakeRPC{replies: map[string]string{
		"worktree.resolvePrBase": `{"ok":true,"result":{"baseBranch":"abc","branchNameOverride":"feat/x"}}`,
		"worktree.create":        `{"ok":true,"result":{"worktree":{"id":"r1::/wt/x","path":"/wt/x","branch":"refs/heads/feat/x"}}}`,
	}}
	ws, _, err := (&Manager{R: canned{}, RPC: rpc}).Create(context.Background(), workspace.CreateRequest{RepoID: "r1", Name: "x", Unit: prUnit(7), HeadBranch: "feat/x", Parent: "/wt/lead"})
	if err != nil || !ws.ParentDropped || rpc.params["worktree.create"]["noParent"] != true {
		t.Fatalf("ws = %+v, err = %v, params = %v", ws, err, rpc.params["worktree.create"])
	}
}
