package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/setup"
	"github.com/wloophq/wloop/internal/core/turns"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
	"github.com/wloophq/wloop/plugins/slack/slacktest"
)

// initGH answers gh by the start of its arguments; anything else fails.
type initGH struct {
	answers map[string]string
	fail    map[string]error
	calls   []string
}

func (g *initGH) Run(_ context.Context, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	g.calls = append(g.calls, call)
	for prefix, err := range g.fail {
		if strings.HasPrefix(call, prefix) {
			return nil, err
		}
	}
	for prefix, out := range g.answers {
		if strings.HasPrefix(call, prefix) {
			return []byte(out), nil
		}
	}
	return nil, fmt.Errorf("initGH: unexpected gh %s", call)
}

type initTestEnv struct {
	*Env
	out, err *bytes.Buffer
	gh       *initGH
	ws       *fake.Manager
	kc       *fakeKeychain
	// slackSt plays the browser for the Slack plugin's login.
	slackSt *slackTestEnv
	// onPath are the binaries the login shell finds.
	onPath []string
	// src holds most of the repositories the fake manager knows.
	src string
}

func newInitTestEnv(t *testing.T) *initTestEnv {
	t.Helper()
	old := goos
	goos = "linux"
	t.Cleanup(func() { goos = old })
	src := filepath.Join(t.TempDir(), "src")
	te := &initTestEnv{
		src: src,
		out: &bytes.Buffer{}, err: &bytes.Buffer{},
		gh: &initGH{answers: map[string]string{
			"auth status":    "",
			"search prs":     "acme/api\nacme/web\nacme/api\nAcme/API\n",
			"api user/teams": "acme/backend\nacme/platform\n",
		}, fail: map[string]error{}},
		ws: &fake.Manager{RepoList: []wsdomain.Repo{
			{ID: "1", Path: filepath.Join(src, "api"), RemoteKey: "github.com/acme/api"},
			{ID: "2", Path: filepath.Join(src, "web"), RemoteKey: "github.com/acme/web"},
			{ID: "3", Path: filepath.Join(t.TempDir(), "tool"), RemoteKey: "github.com/me/tool"},
		}},
		kc:     newFakeKeychain(),
		onPath: []string{"git", "claude"},
	}
	cfgPath := filepath.Join(t.TempDir(), "config", "config.toml")
	te.Env = (&Env{
		Out: te.out, Err: te.err, In: strings.NewReader(""),
		forge: ghForge{te.gh}, WS: workspace.NewSet(te.ws),
		Sh: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == shellPath() && len(args) == 2 && args[0] == "-lc" {
				var out []string
				for _, b := range te.onPath {
					out = append(out, "/opt/bin/"+b)
				}
				return []byte(strings.Join(out, "\n")), nil
			}
			return nil, errors.New("no shell in tests")
		},
		LoadConfig: func() (config.Config, error) { return config.LoadFile(cfgPath, Defaults()) },
		ConfigPath: cfgPath,
		StateDir:   t.TempDir(),
		Home:       t.TempDir(),
		Now:        func() time.Time { return testNow },
		Getenv:     func(string) string { return "" },
		Agents:     map[string]turns.Agent{"claude": claude.Agent{}},
		Homes:      map[string]setup.HomeKind{"claude": fakeKind{}},
		slack: slackEnv{
			Interactive: func() bool { return false },
			ReadSecret:  func() ([]byte, error) { return nil, errors.New("no secret in tests") },
		},
	}).owning()
	te.slackSt = &slackTestEnv{kc: te.kc}
	withSlackPlugin(t, te.Env, te.slackSt.pluginEnv(t, ""))
	te.Env.intents = newEnvIntents(te.Env)
	return te
}

func (te *initTestEnv) run(args ...string) int {
	return Run(context.Background(), te.Env, append([]string{"init"}, args...))
}

func (te *initTestEnv) config(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadFile(te.ConfigPath, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func (te *initTestEnv) writeConfig(t *testing.T, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(te.ConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(te.ConfigPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitDefaults(t *testing.T) {
	t.Parallel()
	cur := Defaults()
	cur.ProjectsDir, cur.Profile = "/p", "/prof"
	cur.GitHub.Scope.Repos = []string{"acme/api"}
	for _, c := range []struct {
		name    string
		facts   initFacts
		flags   []string
		want    initAnswers
		missing []string
		err     string
	}{
		{name: "first run asks for the directory and the profile",
			facts:   initFacts{cur: Defaults(), agents: []string{"claude"}, installed: []string{"claude"}, dirSuggestion: "/src"},
			want:    initAnswers{Agent: "claude"},
			missing: []string{"profile", "projects_dir"}},
		{name: "flags answer everything",
			facts: initFacts{cur: Defaults(), agents: []string{"claude"}, installed: []string{"claude"}},
			flags: []string{"--projects-dir", "/x", "--profile-default", "--scope-repo", "a/b,c/d", "--scope-repo", "a/b", "--scope-team", "acme/x"},
			want:  initAnswers{ProjectsDir: "/x", Agent: "claude", Repos: []string{"a/b", "c/d"}, Teams: []string{"acme/x"}}},
		{name: "a rerun defaults to the current values",
			facts: initFacts{cur: cur, exists: true, agents: []string{"claude"}, installed: []string{"claude"}},
			want:  initAnswers{ProjectsDir: "/p", Agent: "claude", Profile: "/prof", Repos: []string{"acme/api"}}},
		{name: "scope flags replace the scope",
			facts: initFacts{cur: cur, exists: true, agents: []string{"claude"}, installed: []string{"claude"}},
			flags: []string{"--scope-person", "octo"},
			want:  initAnswers{ProjectsDir: "/p", Agent: "claude", Profile: "/prof", People: []string{"octo"}}},
		{name: "several agents installed and none chosen",
			facts:   initFacts{cur: Defaults(), agents: []string{"claude", "codex"}, installed: []string{"claude", "codex"}},
			flags:   []string{"--projects-dir", "/x", "--profile", "/prof"},
			want:    initAnswers{ProjectsDir: "/x", Profile: "/prof"},
			missing: []string{"agent"}},
		{name: "an unsupported agent",
			facts: initFacts{cur: Defaults(), agents: []string{"claude"}, installed: []string{"claude"}},
			flags: []string{"--agent", "vim"},
			err:   "not supported"},
		{name: "an agent not installed",
			facts: initFacts{cur: Defaults(), agents: []string{"claude", "codex"}, installed: []string{"claude"}},
			flags: []string{"--agent", "codex"},
			err:   "not installed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fl, err := parseInitFlags(&Env{Err: &bytes.Buffer{}}, c.flags)
			if err != nil {
				t.Fatal(err)
			}
			got, missing, err := initDefaults(c.facts, fl, "/home")
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("answers %+v, want %+v", got, c.want)
			}
			keys := slices.Sorted(func(yield func(string) bool) {
				for k := range missing {
					if !yield(k) {
						return
					}
				}
			})
			if !slices.Equal(keys, c.missing) {
				t.Errorf("missing %v, want %v", keys, c.missing)
			}
		})
	}
}

func TestInitFlagsExcludeEachOther(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--profile", "/x", "--profile-default"}, {"--skip-slack", "--slack-client-id", "1.2"}, {"extra"}} {
		if _, err := parseInitFlags(&Env{Err: &bytes.Buffer{}}, args); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestMergeInitConfigKeepsTheKeysItDoesNotManage(t *testing.T) {
	t.Parallel()
	raw := `# acme's loop
agent = "claude" # the default

[sweep]
fast = "5m" # a shared rate limit

[plugins.github.scope]
# people I follow
watch = ["boss"]
repos = [
  "old/repo", # moved
]

[plugins.slack]
post_allowlist = ["C1"]
`
	a := initAnswers{ProjectsDir: "/src", Agent: "claude", Repos: []string{"acme/api"}, SlackClientID: "1.2"}
	got, err := mergeInitConfig([]byte(raw), a)
	if err != nil {
		t.Fatal(err)
	}
	want := `# acme's loop
agent = "claude" # the default
projects_dir = '/src'

[sweep]
fast = "5m" # a shared rate limit

[plugins.github.scope]
# people I follow
watch = ["boss"]
repos = ['acme/api']

[plugins.slack]
post_allowlist = ["C1"]
client_id = '1.2'
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	again, err := mergeInitConfig(got, a)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("a second merge changed the config (%v):\n%s", err, again)
	}
}

func TestMergeInitConfigWritesAFreshFile(t *testing.T) {
	t.Parallel()
	got, err := mergeInitConfig(nil, initAnswers{ProjectsDir: "/src", Profile: "/profiles/mine", Agent: "claude",
		Repos: []string{"acme/api"}, Teams: []string{"acme/core"}, People: []string{"ana"}, SlackClientID: "1.2"})
	if err != nil {
		t.Fatal(err)
	}
	want := `projects_dir = '/src'
profile = '/profiles/mine'
agent = 'claude'

[plugins.github.scope]
repos = ['acme/api']
teams = ['acme/core']
people = ['ana']

[plugins.slack]
client_id = '1.2'
`
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	var doc map[string]any
	if err := toml.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if ps := builtin.Check(builtin.SurfaceConfig, doc); len(ps) > 0 {
		t.Errorf("init writes keys the grammar refuses: %v\n%s", ps, got)
	}
}

func TestMergeInitConfigWritesWhereTheFileHasTheTable(t *testing.T) {
	t.Parallel()
	a := initAnswers{Agent: "claude", Repos: []string{"acme/api"}, SlackClientID: "1.2"}
	for name, c := range map[string]struct{ raw, want string }{
		"a parent table": {
			"[plugins.github]\nowners = [\"acme\"]\n\n[[projects]]\nmatch = \"acme/*\"\n",
			"agent = 'claude'\n\n[plugins.github]\nowners = [\"acme\"]\nscope.repos = ['acme/api']\n\n[[projects]]\nmatch = \"acme/*\"\n\n[plugins.slack]\nclient_id = '1.2'\n",
		},
		"dotted keys at the top": {
			"agent = \"claude\"\nplugins.github.scope.watch = [\"boss\"]\nplugins.slack.client_id = \"old\"",
			"agent = \"claude\"\nplugins.github.scope.watch = [\"boss\"]\nplugins.slack.client_id = '1.2'\nplugins.github.scope.repos = ['acme/api']",
		},
		"a header with no keys": {
			"agent = \"claude\"\n\n[plugins.github.scope] # mine\n",
			"agent = \"claude\"\n\n[plugins.github.scope] # mine\nrepos = ['acme/api']\n\n[plugins.slack]\nclient_id = '1.2'\n",
		},
	} {
		got, err := mergeInitConfig([]byte(c.raw), a)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %v, got:\n%s\nwant:\n%s", name, err, got, c.want)
			continue
		}
		if err := toml.Unmarshal(got, new(map[string]any)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMergeInitConfigRefusesToEditAnInlineTable(t *testing.T) {
	t.Parallel()
	raw := "plugins.github = {owners = [\"acme\"]}\n"
	if _, err := mergeInitConfig([]byte(raw), initAnswers{Repos: []string{"acme/api"}}); err == nil || !strings.Contains(err.Error(), "plugins.github.scope.repos") {
		t.Fatalf("err %v", err)
	}
	if got, err := mergeInitConfig([]byte(raw), initAnswers{}); err != nil || string(got) != raw {
		t.Fatalf("an empty answer touched the inline table (%v):\n%s", err, got)
	}
	followed := "[plugins.github]\nscope = { repos = [\"acme/api\"] }\n"
	if _, err := mergeInitConfig([]byte(followed), initAnswers{}); err == nil || !strings.Contains(err.Error(), "plugins.github.scope.repos") {
		t.Fatalf("clearing a list in an inline table: %v", err)
	}
}

func TestMergeInitConfigKeepsAPathSpelledWithTilde(t *testing.T) {
	t.Parallel()
	raw := "projects_dir = \"~/src\"\nprofile = \"mine\"\n"
	got, err := mergeInitConfig([]byte(raw), initAnswers{ProjectsDir: config.ExpandHome("~/src"), Profile: config.ResolveProfile("mine"), Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if want := raw + "agent = 'claude'\n"; string(got) != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestMergeInitConfigAddsNoEmptyKeys(t *testing.T) {
	t.Parallel()
	got, err := mergeInitConfig(nil, initAnswers{ProjectsDir: "/src", Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "projects_dir = '/src'\nagent = 'claude'\n"; string(got) != want {
		t.Fatalf("got:\n%s", got)
	}
	if _, err := mergeInitConfig([]byte("agent = "), initAnswers{}); err == nil {
		t.Fatal("a config that does not parse merged")
	}
}

func TestCommonParent(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		paths []string
		want  string
	}{
		{nil, ""},
		{[]string{"/src/a", "/src/b", "/other/c"}, "/src"},
		{[]string{"/b/x", "/a/y"}, "/a"},
	} {
		var repos []wsdomain.Repo
		for _, p := range c.paths {
			repos = append(repos, wsdomain.Repo{Path: p})
		}
		if got := commonParent(repos); got != c.want {
			t.Errorf("%v: got %q, want %q", c.paths, got, c.want)
		}
	}
}

func TestScopeSuggestionsReadGH(t *testing.T) {
	te := newInitTestEnv(t)
	repos, err := te.reviewedRepos(context.Background())
	if err != nil || !slices.Equal(repos, []string{"acme/api", "acme/web"}) {
		t.Fatalf("repos %v (%v)", repos, err)
	}
	if !slices.ContainsFunc(te.gh.calls, func(c string) bool {
		return strings.Contains(c, "--updated=>="+testNow.Add(-reviewedWindow).UTC().Format("2006-01-02"))
	}) {
		t.Errorf("calls %v", te.gh.calls)
	}
	teams, err := te.userTeams(context.Background())
	if err != nil || !slices.Equal(teams, []string{"acme/backend", "acme/platform"}) {
		t.Fatalf("teams %v (%v)", teams, err)
	}
}

func TestInitOffATerminalNamesTheMissingFlags(t *testing.T) {
	te := newInitTestEnv(t)
	if code := te.run(); code != 1 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, want := range []string{"--projects-dir <dir> (" + te.src + " holds most", "--profile <dir> or --profile-default"} {
		if !strings.Contains(te.err.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, te.err)
		}
	}
	if _, err := os.Stat(te.ConfigPath); err == nil {
		t.Error("wrote a config")
	}
}

func TestInitStopsOnAMissingPrerequisite(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(te *initTestEnv)
		fix   string
	}{
		{"gh", func(te *initTestEnv) { te.gh.fail["auth status"] = errors.New("not logged in") }, "gh auth login"},
		{"agent", func(te *initTestEnv) { te.onPath = []string{"git"} }, claude.Agent{}.InstallCommand()},
		{"git", func(te *initTestEnv) { te.onPath = []string{"claude"} }, "install git"},
		{"manager", func(te *initTestEnv) { te.ws.Unreachable = errors.New("orca is not running") }, "open the"},
	} {
		t.Run(c.name, func(t *testing.T) {
			te := newInitTestEnv(t)
			c.setup(te)
			if code := te.run("--projects-dir", "/x", "--profile-default"); code != 1 {
				t.Fatalf("exit %d", code)
			}
			if !strings.Contains(te.out.String(), "fix with: "+c.fix) || !strings.Contains(te.err.String(), "prerequisite(s) missing") {
				t.Fatalf("out:\n%s\nerr:\n%s", te.out, te.err)
			}
			if _, err := os.Stat(te.ConfigPath); err == nil {
				t.Error("wrote a config")
			}
		})
	}
}

func TestInitFromFlagsWritesTheConfigAndTheProfile(t *testing.T) {
	te := newInitTestEnv(t)
	projects := filepath.Join(te.Home, "src")
	prof := filepath.Join(te.Home, "profile")
	if code := te.run("--projects-dir", projects, "--scope-repo", "acme/api", "--scope-team", "acme/backend", "--profile", prof, "--skip-slack", "--no-install"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	cfg := te.config(t)
	if cfg.ProjectsDir != projects || cfg.Agent != "claude" || cfg.Profile != prof ||
		!slices.Equal(cfg.GitHub.Scope.Repos, []string{"acme/api"}) || !slices.Equal(cfg.GitHub.Scope.Teams, []string{"acme/backend"}) {
		t.Fatalf("config %+v", cfg)
	}
	if fi, err := os.Stat(projects); err != nil || !fi.IsDir() {
		t.Errorf("projects_dir not created: %v", err)
	}
	for _, intent := range bundledBlockIntents() {
		if _, err := os.Stat(filepath.Join(prof, "intent="+intent+".md"+exampleSuffix)); err != nil {
			t.Errorf("example for %s: %v", intent, err)
		}
	}
	out := te.out.String()
	for _, want := range []string{"+++ b/config.toml", "Skipped; run '" + brand.Command + " install'", "Next: '" + brand.Command + " review"} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
}

func TestInitRerunChangesNothing(t *testing.T) {
	te := newInitTestEnv(t)
	args := []string{"--projects-dir", filepath.Join(te.Home, "src"), "--profile-default", "--scope-repo", "acme/api", "--no-install"}
	if code := te.run(args...); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	first, _ := os.ReadFile(te.ConfigPath)
	te.out.Reset()
	if code := te.run("--no-install"); code != 0 {
		t.Fatalf("rerun exit %d: %s", code, te.err)
	}
	if second, _ := os.ReadFile(te.ConfigPath); !bytes.Equal(first, second) {
		t.Fatalf("the rerun changed the config:\n%s\n%s", first, second)
	}
	if !strings.Contains(te.out.String(), "is up to date") {
		t.Errorf("out:\n%s", te.out)
	}
	if baks, _ := filepath.Glob(te.ConfigPath + ".loop-*.bak"); len(baks) > 0 {
		t.Errorf("backups %v", baks)
	}
}

func TestInitOverwritesOnlyWithYes(t *testing.T) {
	te := newInitTestEnv(t)
	raw := "projects_dir = \"/old\"\n\n[sweep]\nfast = \"5m\"\n"
	te.writeConfig(t, raw)
	args := []string{"--projects-dir", filepath.Join(te.Home, "src"), "--profile-default", "--no-install"}
	if code := te.run(args...); code != 1 || !strings.Contains(te.err.String(), "pass --yes") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if now, _ := os.ReadFile(te.ConfigPath); string(now) != raw {
		t.Fatalf("changed without --yes:\n%s", now)
	}
	if !strings.Contains(te.out.String(), `-projects_dir = "/old"`) {
		t.Errorf("no diff:\n%s", te.out)
	}
	if code := te.run(append(args, "--yes")...); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if cfg := te.config(t); cfg.ProjectsDir != filepath.Join(te.Home, "src") || time.Duration(cfg.Sweep.Fast) != 5*time.Minute {
		t.Fatalf("config %+v", cfg)
	}
	if baks, _ := filepath.Glob(te.ConfigPath + ".loop-*.bak"); len(baks) != 1 {
		t.Errorf("backups %v", baks)
	}
}

func TestInitInstallsWithYes(t *testing.T) {
	te := newInitTestEnv(t)
	if code := te.run("--projects-dir", filepath.Join(te.Home, "src"), "--profile-default", "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if _, err := os.Stat(skillPathIn(filepath.Join(te.Home, ".agents"), brand.Name+"-review")); err != nil {
		t.Fatalf("install did not run: %v\n%s", err, te.out)
	}
	te.out.Reset()
	if code := te.run("--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "'"+brand.Command+" install': nothing to do") {
		t.Errorf("out:\n%s", te.out)
	}
}

// answers feeds the prompts one line each.
func answers(lines ...string) *strings.Reader {
	return strings.NewReader(strings.Join(lines, "\n") + "\n")
}

func TestInitAtATerminal(t *testing.T) {
	te := newInitTestEnv(t)
	srv := slacktest.New(t)
	srv.Handle("oauth.v2.access", func(url.Values) any {
		return map[string]any{
			"authed_user": map[string]any{"id": "U000", "access_token": "xoxe.xoxp-1-fakeaccess", "refresh_token": "xoxe-1-fakerefresh", "expires_in": 43200, "scope": "chat:write"},
			"team":        map[string]any{"id": "T000"},
		}
	})
	srv.Handle("auth.test", func(url.Values) any {
		return map[string]any{"team_id": "T000", "team": "Acme", "user_id": "U000", "url": "https://acme.example/"}
	})
	te.slackSt.open = func(u string) error { browse(t, u); return nil }
	withSlackPlugin(t, te.Env, te.slackSt.pluginEnv(t, srv.URL))
	te.slack.Interactive = func() bool { return true }
	te.slack.ReadSecret = func() ([]byte, error) { return []byte("xapp-1-fakeapp"), nil }
	te.In = answers(
		"",             // projects_dir: the suggestion
		"",             // repositories: the reviewed ones
		"acme/backend", // teams
		"-",            // people
		"personal",     // profile
		"",             // profile directory
		"y",            // set Slack up
		"123.456",      // client id
		"",             // write the config
		"n",            // install
	)
	if code := te.run(); code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, te.err, te.out)
	}
	cfg := te.config(t)
	if cfg.ProjectsDir != te.src || !slices.Equal(cfg.GitHub.Scope.Repos, []string{"acme/api", "acme/web"}) ||
		!slices.Equal(cfg.GitHub.Scope.Teams, []string{"acme/backend"}) || len(cfg.GitHub.Scope.People) != 0 ||
		cfg.Profile != filepath.Join(filepath.Dir(te.ConfigPath), "profiles", "personal") || cfg.Slack.ClientID != "123.456" {
		t.Fatalf("config %+v", cfg)
	}
	if len(te.kc.items) != 2 {
		t.Fatalf("slack keychain items %d, want the app token and the login\n%s", len(te.kc.items), te.out)
	}
	assertNoSlackToken(t, te.out.String()+te.err.String())
	for _, want := range []string{"Agent: claude, the only one installed", "you reviewed in the last 30 days: acme/api, acme/web", "socket_mode_enabled: true", "Logged in to Slack as U000"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, te.out)
		}
	}

	te.out.Reset()
	te.In = answers("", "", "", "", "", "", "", "n")
	if code := te.run(); code != 0 {
		t.Fatalf("rerun exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "is up to date") || !strings.Contains(te.out.String(), "Slack: logged in") || !strings.Contains(te.out.String(), "user: U000") {
		t.Errorf("rerun out:\n%s", te.out)
	}
}

func TestSlackManifestIsTheDocs(t *testing.T) {
	t.Parallel()
	var docs []string
	err := filepath.WalkDir(filepath.Join("..", "..", "docs"), func(p string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() && (d.Name() == "node_modules" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if err == nil && d.Name() == "slack-app-manifest.yaml" {
			docs = append(docs, p)
		}
		return err
	})
	if err != nil || len(docs) != 1 {
		t.Fatalf("manifests in the documentation: %v (%v)", docs, err)
	}
	raw, err := os.ReadFile(docs[0])
	if err != nil {
		t.Fatal(err)
	}
	want := string(raw)
	if !strings.Contains(want, "{{name}}") && brand.Name != brandtest.DefaultName {
		t.Skip("the documentation spells the default brand")
	}
	want = strings.ReplaceAll(want, "{{name}}", brand.Name)
	if got := renderSlackManifest(); got != want {
		t.Fatalf("the manifest differs from %s:\n%s", docs[0], got)
	}
}

func TestProfileExamplesAreInert(t *testing.T) {
	te := newInitTestEnv(t)
	dir := filepath.Join(t.TempDir(), "p")
	wrote, err := writeProfileExamples(dir)
	if err != nil || len(wrote) != len(bundledBlockIntents()) || len(wrote) == 0 {
		t.Fatalf("wrote %v (%v)", wrote, err)
	}
	if err := te.checkProfile(dir); err != nil {
		t.Fatal(err)
	}
	tree, err := profile.Load([]profile.Layer{{Name: "user", Dir: dir}}, te.loadedIntents().Registry, nil)
	if err != nil || len(tree.Blocks) != 0 {
		t.Fatalf("blocks %d (%v)", len(tree.Blocks), err)
	}
	for _, f := range wrote {
		raw, _ := os.ReadFile(f)
		active := filepath.Join(dir, strings.TrimSuffix(filepath.Base(f), exampleSuffix))
		if err := os.WriteFile(active, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := te.checkProfile(dir); err != nil {
		t.Fatalf("a renamed example does not load: %v", err)
	}
}
