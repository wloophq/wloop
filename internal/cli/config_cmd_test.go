package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestConfigGet(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Views["orca"].Columns["needs-me"] = "waiting-me"
	for key, want := range map[string]string{
		"views.orca.columns.needs-me":        "waiting-me\n",
		"views.orca.columns.reviewed":        "reviewed\n",
		"plugins.github.rate_limit_fraction": "0.5\n",
		"turns.per_unit_per_day":             "5\n",
		"brief.enabled":                      "false\n",
		"plugins.github.automated_reviewers": "[]\n",
		"sweep.fast":                         "2m\n",
		"sweep.slow":                         "1h\n",
		"plugins.github.stalled.review":      "2d\n",
		"cleanup.idle":                       "2h\n",
	} {
		te.out.Reset()
		if code := te.run("config", "get", key); code != 0 || te.out.String() != want {
			t.Errorf("%s: exit %d, %q, want %q (%s)", key, code, te.out, want, te.err)
		}
	}
	for _, args := range [][]string{{"config", "get", "nope"}, {"config", "get", "views.orca.columns.needs-me.x"}, {"config", "set", "a", "b"}, {"config"}} {
		if code := te.run(args...); code != 1 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if !strings.Contains(te.err.String(), `no config key "nope"`) || !strings.Contains(te.err.String(), "usage: "+brand.Command+" config get <key>") {
		t.Fatalf("stderr: %s", te.err)
	}
}

func TestConfigGetViewColumn(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	file := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, file, `views = {orca = {columns = {needs-me = "x"}}}`)
	te.LoadConfig = func() (config.Config, error) { return config.LoadFile(file, Defaults()) }
	if code := te.run("config", "get", "views.orca.columns.needs-me"); code != 0 || te.out.String() != "x\n" {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

const acmeConfig = "# acme's loop\nagent = 'claude' # the agent\n\n# how often\n[sweep]\nfast = '2m'\n"

func readFile(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestConfigSetWritesTheValueKeepingTheFileAndABackup(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args  []string
		want  string
		check func(config.Config) bool
	}{
		{[]string{"brief.enabled", "true"}, acmeConfig + "\n[brief]\nenabled = true\n", func(c config.Config) bool { return c.Brief.Enabled }},
		{[]string{"sweep.fast", "5m"}, strings.Replace(acmeConfig, "'2m'", "'5m'", 1), func(c config.Config) bool { return c.Sweep.Fast == config.Days(5*time.Minute) }},
		{[]string{"mcp.port", "47811"}, acmeConfig + "\n[mcp]\nport = 47811\n", func(c config.Config) bool { return c.MCP.Port == 47811 }},
		{[]string{"plugins.github.automated_reviewers", `["acme-bot", "beta-bot"]`}, acmeConfig + "\n[plugins.github]\nautomated_reviewers = ['acme-bot', 'beta-bot']\n",
			func(c config.Config) bool {
				return slices.Equal(c.GitHub.AutomatedReviewers, []string{"acme-bot", "beta-bot"})
			}},
		{[]string{"plugins.github.automated_reviewers", "[]"}, acmeConfig + "\n[plugins.github]\nautomated_reviewers = []\n",
			func(c config.Config) bool {
				return c.GitHub.AutomatedReviewers != nil && len(c.GitHub.AutomatedReviewers) == 0
			}},
		{[]string{"plugins.slack.client_id", `"1234.5678"`}, acmeConfig + "\n[plugins.slack]\nclient_id = '1234.5678'\n", func(c config.Config) bool { return c.Slack.ClientID == "1234.5678" }},
		{[]string{"plugins.slack.client_id", "12\nmcp.port = 2"}, acmeConfig + "\n[plugins.slack]\nclient_id = \"12\\nmcp.port = 2\"\n", func(c config.Config) bool { return c.Slack.ClientID == "12\nmcp.port = 2" }},
		{[]string{"workflows.acme-sync.enabled", "false"}, acmeConfig + "\n[workflows.acme-sync]\nenabled = false\n",
			func(c config.Config) bool {
				w, ok := c.Workflows["acme-sync"]
				return ok && w.Enabled != nil && !*w.Enabled
			}},
	} {
		t.Run(strings.Join(c.args, "="), func(t *testing.T) {
			te := newTestEnv(t, &fake.Manager{})
			writeFile(t, te.ConfigPath, acmeConfig)
			if code := te.run(append([]string{"config", "set"}, c.args...)...); code != 0 {
				t.Fatalf("exit %d: %s%s", code, te.out, te.err)
			}
			if got := readFile(t, te.ConfigPath); got != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			cfg, err := config.LoadFile(te.ConfigPath, Defaults())
			if err != nil || !c.check(cfg) {
				t.Fatalf("loaded %v", err)
			}
			bak := backups(t, te.ConfigPath)
			if want := te.ConfigPath + ".loop-20261009-150000.bak"; len(bak) != 1 || bak[0] != want || readFile(t, bak[0]) != acmeConfig {
				t.Fatalf("backups %q, want %s", bak, want)
			}
			if out := te.out.String(); !strings.Contains(out, bak[0]) || !strings.Contains(out, "Restart the daemon") {
				t.Fatalf("out: %s", out)
			}
		})
	}
}

func TestConfigSetRefusesAnUnknownKeyAndAResultThatDoesNotLoad(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"sweep.fsat", "5m"}, `did you mean "fast"`},
		{[]string{"nope", "1"}, "nope"},
		{[]string{"brief.enabled", "yes"}, "brief.enabled"},
		{[]string{"sweep.fast", "0s"}, "sweep.fast: a duration must be longer than zero"},
		{[]string{"sweep", "1"}, "sweep"},
		{[]string{"views", "{orca = {}}"}, "views"},
		{[]string{"sweep..fast", "1"}, "invalid key"},
	} {
		t.Run(strings.Join(c.args, "="), func(t *testing.T) {
			te := newTestEnv(t, &fake.Manager{})
			writeFile(t, te.ConfigPath, acmeConfig)
			if code := te.run(append([]string{"config", "set"}, c.args...)...); code != 1 || !strings.Contains(te.err.String(), c.want) {
				t.Fatalf("exit %d: %s%s", code, te.out, te.err)
			}
			if got := readFile(t, te.ConfigPath); got != acmeConfig || len(backups(t, te.ConfigPath)) != 0 {
				t.Fatalf("the file changed:\n%s", got)
			}
		})
	}
}

func TestConfigSetCreatesTheFileAndSaysAProfileAppliesLive(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("config", "set", "profile", "acme"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if got := readFile(t, te.ConfigPath); got != "profile = 'acme'\n" || len(backups(t, te.ConfigPath)) != 0 {
		t.Fatalf("got:\n%s", got)
	}
	if out := te.out.String(); !strings.Contains(out, "applies it live") || strings.Contains(out, "Restart") || strings.Contains(out, "previous") {
		t.Fatalf("out: %s", out)
	}
}

func TestConfigSetOfTheSameValueWritesNothing(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	writeFile(t, te.ConfigPath, acmeConfig)
	if code := te.run("config", "set", "sweep.fast", "2m"); code != 0 || !strings.Contains(te.out.String(), "already") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if got := readFile(t, te.ConfigPath); got != acmeConfig || len(backups(t, te.ConfigPath)) != 0 {
		t.Fatalf("got:\n%s", got)
	}
}

func TestConfigUnset(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	writeFile(t, te.ConfigPath, acmeConfig)
	if code := te.run("config", "unset", "sweep.fast"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if got, want := readFile(t, te.ConfigPath), "# acme's loop\nagent = 'claude' # the agent\n\n# how often\n[sweep]\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
	if bak := backups(t, te.ConfigPath); len(bak) != 1 || readFile(t, bak[0]) != acmeConfig || !strings.Contains(te.out.String(), bak[0]) {
		t.Fatalf("backups %q, out %s", bak, te.out)
	}
	te.out.Reset()
	if code := te.run("config", "unset", "sweep.fast"); code != 0 || !strings.Contains(te.out.String(), "not set") || len(backups(t, te.ConfigPath)) != 1 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if code := te.run("config", "unset", "sweep"); code != 1 || !strings.Contains(te.err.String(), "is a table") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestConfigUnsetFixesAFileThatDoesNotLoadWhereSetRefuses(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	broken := acmeConfig + "fsat = '1m'\nslwo = '1h'\n"
	writeFile(t, te.ConfigPath, broken)
	if code := te.run("config", "set", "sweep.slow", "2h"); code != 1 || readFile(t, te.ConfigPath) != broken {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("config", "unset", "sweep.fsat"); code != 0 || !strings.Contains(te.out.String(), "slwo") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if code := te.run("config", "unset", "sweep.slwo"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	if _, err := config.LoadFile(te.ConfigPath, Defaults()); err != nil {
		t.Fatal(err)
	}
}
