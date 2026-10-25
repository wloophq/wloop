package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestConfigGet(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Views["orca"].Columns["needs-me"] = "waiting-me"
	for key, want := range map[string]string{
		"views.orca.columns.needs-me": "waiting-me\n",
		"views.orca.columns.reviewed": "reviewed\n",
		"sweep.rate_limit_fraction":   "0.5\n",
		"rounds.per_pr_per_day":       "5\n",
		"brief.enabled":               "false\n",
		"automated_reviewers":         "[]\n",
		"sweep.fast":                  "2m\n",
		"sweep.slow":                  "1h\n",
		"stalled.review":              "2d\n",
		"cleanup.idle":                "2h\n",
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
	file := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, file, `{"views":{"orca":{"columns":{"needs-me":"x"}}}}`)
	te.LoadConfig = func() (config.Config, error) { return config.LoadFile(file) }
	if code := te.run("config", "get", "views.orca.columns.needs-me"); code != 0 || te.out.String() != "x\n" {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}
