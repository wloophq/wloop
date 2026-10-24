package workspace_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// listings are the fake manager's meter ops for each part.
var listings = map[wsdomain.Part]string{wsdomain.PartRepos: "Repos", wsdomain.PartWorkspaces: "Workspaces",
	wsdomain.PartPanes: "Panes", wsdomain.PartTerminals: "AllTerminals"}

func TestCachedWriteInvalidates(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		write func(m workspace.Manager) error
		want  wsdomain.Part
	}{
		{"AddRepo", func(m workspace.Manager) error { _, err := m.AddRepo(ctx, "/p/x"); return err }, wsdomain.PartRepos},
		{"Create", func(m workspace.Manager) error {
			_, _, err := m.Create(ctx, workspace.CreateRequest{RepoID: "r1", Name: "n"})
			return err
		}, wsdomain.PartMutable},
		{"Remove", func(m workspace.Manager) error { return m.Remove(ctx, "a", false) }, wsdomain.PartMutable},
		{"Unregister", func(m workspace.Manager) error { return m.Unregister(ctx, "a") }, wsdomain.PartMutable},
		{"SetStatus", func(m workspace.Manager) error { return m.(workspace.Board).SetStatus(ctx, "a", "done") }, wsdomain.PartWorkspaces},
		{"MarkUnread", func(m workspace.Manager) error { return m.(workspace.Board).MarkUnread(ctx, "a") }, wsdomain.PartWorkspaces},
		{"LinkUnit", func(m workspace.Manager) error {
			return m.(workspace.Linker).LinkUnit(ctx, "a", unit.Unit{Provider: unit.GitHub, Key: "o/r#7"})
		}, wsdomain.PartWorkspaces},
		{"Rename", func(m workspace.Manager) error { return m.(workspace.Renamer).Rename(ctx, "a", "x") }, wsdomain.PartWorkspaces},
		{"StartAgentSession", func(m workspace.Manager) error {
			_, err := m.StartAgentSession(ctx, workspace.StartRequest{WorkspaceID: "a", Command: "claude", Title: "t"})
			return err
		}, wsdomain.PartPanes | wsdomain.PartTerminals},
		{"Wake", func(m workspace.Manager) error {
			_, err := m.(workspace.Waker).Wake(ctx, workspace.WakeRequest{WorkspaceID: "a", Session: "s1", Command: "claude --resume x"})
			return err
		}, wsdomain.PartPanes | wsdomain.PartTerminals},
		{"CloseTerminal", func(m workspace.Manager) error { return m.(workspace.TerminalCloser).CloseTerminal(ctx, "term-a") }, wsdomain.PartPanes | wsdomain.PartTerminals},
		{"SendPrompt", func(m workspace.Manager) error { _, err := m.SendPrompt(ctx, "term-a", "go", false); return err }, wsdomain.PartPanes},
		{"Focus", func(m workspace.Manager) error { return m.Focus(ctx, "a") }, wsdomain.PartPanes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mctx, sc := metertest.Start(t, ctx)
			fm := &fake.Manager{
				RepoList:      []wsdomain.Repo{{ID: "r1"}},
				WorkspaceList: []wsdomain.Workspace{{ID: "a"}},
				Sessions:      map[string]string{"a": "term-a"},
			}
			c := workspace.NewCached(fm, fm, 2*time.Minute)
			if _, err := c.Cache.Read(mctx, wsdomain.PartAll); err != nil {
				t.Fatal(err)
			}
			if err := tc.write(c); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Cache.Read(mctx, wsdomain.PartAll); err != nil {
				t.Fatal(err)
			}
			for p, op := range listings {
				want := int64(1)
				if tc.want&p != 0 {
					want = 2
				}
				if n := metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: op}); n != want {
					t.Errorf("%s loads = %d, want %d", op, n, want)
				}
			}
		})
	}
}

// identified is a manager that names the claude kind's terminals "claude-code"; any call
// that reaches it panics.
type identified struct {
	workspace.Manager
}

func (identified) AgentIdentity(kind string) string {
	if kind == "claude" {
		return "claude-code"
	}
	return kind
}

func TestCachedLiveAgentSessionFromTerminals(t *testing.T) {
	o, sc := newObserver(t)
	o.terminals = []wsdomain.Terminal{
		{Handle: "old", WorkspaceID: "a", Agent: "claude-code", LastOutput: time.Unix(10, 0), Usable: true},
		{Handle: "new", WorkspaceID: "a", Agent: "claude-code", LastOutput: time.Unix(20, 0), Usable: true},
		{Handle: "orphan", WorkspaceID: "a", Agent: "claude-code", LastOutput: time.Unix(30, 0)},
		{Handle: "shell", WorkspaceID: "a", LastOutput: time.Unix(40, 0), Usable: true},
		{Handle: "codex", WorkspaceID: "a", Agent: "codex", LastOutput: time.Unix(35, 0), Usable: true},
		{Handle: "elsewhere", WorkspaceID: "b", Agent: "claude-code", LastOutput: time.Unix(50, 0), Usable: true},
	}
	c := workspace.NewCached(identified{}, o, 2*time.Minute)
	ctx := o.ctx
	for _, tc := range []struct{ agent, want string }{{"claude", "new"}, {"", "codex"}, {"codex", "codex"}} {
		s, ok, err := c.LiveAgentSession(ctx, "a", tc.agent)
		if err != nil || !ok || s.Handle != tc.want {
			t.Errorf("LiveAgentSession(a, %q) = %+v %v %v, want %s", tc.agent, s, ok, err, tc.want)
		}
	}
	if _, ok, err := c.LiveAgentSession(ctx, "c", "claude"); ok || err != nil {
		t.Errorf("a workspace without terminals has a session: %v %v", ok, err)
	}
	metertest.Within(t, sc, metertest.Budget{{Res: meter.Workspace}: 1})
}

func TestInvalidateNoopOnPlainManager(t *testing.T) {
	plain := &fake.Manager{}
	workspace.Invalidate(plain, wsdomain.PartAll)
	if workspace.Direct(plain) != workspace.Manager(plain) {
		t.Fatal("a plain manager is not its own direct manager")
	}
	c := workspace.NewCached(plain, plain, time.Minute)
	if c.Snapshot() != c.Cache || workspace.Direct(c) != workspace.Manager(plain) {
		t.Fatal("Snapshot or Direct does not return what Cached holds")
	}
}

func TestCapabilityFollowsTheManagerUnderTheCache(t *testing.T) {
	c := workspace.NewCached(&fake.Manager{}, &fake.Manager{}, time.Minute)
	if _, ok := workspace.Capability[workspace.Board](c); !ok {
		t.Fatal("a cached fake has no board")
	}
	bare := workspace.NewCached(bareManager{&fake.Manager{}}, &fake.Manager{}, time.Minute)
	if _, ok := workspace.Capability[workspace.Board](bare); ok {
		t.Fatal("a cached manager with no board has one")
	}
}

// bareManager is a manager with no optional feature.
type bareManager struct{ m *fake.Manager }

func (b bareManager) Name() string                                       { return b.m.Name() }
func (b bareManager) Hosts(agent string) bool                            { return b.m.Hosts(agent) }
func (b bareManager) Reachable(ctx context.Context) error                { return b.m.Reachable(ctx) }
func (b bareManager) Repos(ctx context.Context) ([]wsdomain.Repo, error) { return b.m.Repos(ctx) }
func (b bareManager) AddRepo(ctx context.Context, p string) (wsdomain.Repo, error) {
	return b.m.AddRepo(ctx, p)
}
func (b bareManager) List(ctx context.Context) ([]wsdomain.Workspace, error) { return b.m.List(ctx) }
func (b bareManager) Create(ctx context.Context, r workspace.CreateRequest) (wsdomain.Workspace, string, error) {
	return b.m.Create(ctx, r)
}
func (b bareManager) AgentActivity(ctx context.Context) (map[string]wsdomain.Activity, error) {
	return b.m.AgentActivity(ctx)
}
func (b bareManager) LiveAgentSession(ctx context.Context, id, agent string) (workspace.Session, bool, error) {
	return b.m.LiveAgentSession(ctx, id, agent)
}
func (b bareManager) StartAgentSession(ctx context.Context, r workspace.StartRequest) (string, error) {
	return b.m.StartAgentSession(ctx, r)
}
func (b bareManager) SendPrompt(ctx context.Context, h, p string, q bool) (bool, error) {
	return b.m.SendPrompt(ctx, h, p, q)
}
func (b bareManager) Focus(ctx context.Context, id string) error { return b.m.Focus(ctx, id) }
func (b bareManager) Remove(ctx context.Context, id string, force bool) error {
	return b.m.Remove(ctx, id, force)
}
func (b bareManager) Unregister(ctx context.Context, id string) error { return b.m.Unregister(ctx, id) }

func TestCachedOverridesEveryWrite(t *testing.T) {
	cachedReads := []string{"Name", "Hosts", "ReadScreen", "TrustsAtLaunch", "Honors", "RewritePrompt", "Reachable", "Repos", "List", "AgentActivity",
		"LiveAgentSession", "Snapshot", "Direct", "Closed", "ConnectsMCPOnFirstPrompt"}
	manager := reflect.TypeFor[*workspace.Cached]()
	for i := range manager.NumMethod() {
		name := manager.Method(i).Name
		if slices.Contains(cachedReads, name) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			mctx, sc := metertest.Start(t, context.Background())
			fm := &fake.Manager{WorkspaceList: []wsdomain.Workspace{{ID: "a"}}}
			c := workspace.NewCached(fm, fm, 2*time.Minute)
			if _, err := c.Cache.Read(mctx, wsdomain.PartAll); err != nil {
				t.Fatal(err)
			}
			method := reflect.ValueOf(c).MethodByName(name)
			args := make([]reflect.Value, method.Type().NumIn())
			for j := range args {
				args[j] = reflect.Zero(method.Type().In(j))
			}
			args[0] = reflect.ValueOf(context.Background())
			method.Call(args)
			if _, err := c.Cache.Read(mctx, wsdomain.PartAll); err != nil {
				t.Fatal(err)
			}
			var n int64
			for _, op := range listings {
				n += metertest.Count(sc, metertest.Limit{Res: meter.Workspace, Op: op})
			}
			if n == 4 {
				t.Errorf("%s invalidates nothing: a write reaches the manager without Cached declaring it", name)
			}
		})
	}
}
