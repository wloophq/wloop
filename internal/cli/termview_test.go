package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

var sgrNames = map[string]string{"1": "bold", "2": "dim", "31": "red", "32": "green", "33": "yellow", "36": "cyan", "22": "/", "39": "/"}

var styleEscapes = regexp.MustCompile(`\x1b\[([0-9]+)m|\x1b\]8;;([^\x1b]*)\x1b\\`)

// bracketed spells the styles of s as [green]✓[/], and a link as
// [link url]text[/], so a golden file reads as text.
func bracketed(s string) string {
	return styleEscapes.ReplaceAllStringFunc(s, func(m string) string {
		sub := styleEscapes.FindStringSubmatch(m)
		switch {
		case strings.HasPrefix(m, "\x1b]") && sub[2] == "":
			return "[/]"
		case strings.HasPrefix(m, "\x1b]"):
			return "[link " + sub[2] + "]"
		case sgrNames[sub[1]] == "/":
			return "[/]"
		}
		return "[" + sgrNames[sub[1]] + "]"
	})
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		brandtest.CanUpdate(t)
		if err := os.WriteFile(path, []byte(brandtest.Collapse(got)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != brandtest.Expand(string(want)) {
		t.Errorf("%s differs (go test -run %s -update rewrites it):\n%s", path, t.Name(), brandtest.Collapse(got))
	}
}

func doctorFixture() []checkSection {
	return dedupe([]checkSection{
		{"GitHub and Orca", []check{{markOK, "gh authenticated", "", ""}, {markOK, "orca reachable", "", ""}}},
		{"Config", []check{
			{markOK, "config", "/home/u/.config/" + brand.StateDir() + "/config.json", ""},
			{markInfo, "effective config", `agent=claude github.review_prefix="REVIEW " views.orca.columns.working=reviewing views.orca.columns.ready=ready-to-merge github.automated_reviewers=[lintbot-ai] brief=true`, ""},
			{markInfo, "turn budgets", "turns.per_unit_per_day=5 (github) turns.per_day=20 (entry, session, slack, workspace)", ""},
		}},
		{homesSection, []check{
			{markOK, "agent home /home/u/.claude: MCP server " + brand.Name, "", ""},
			{markOK, "agent home /home/u/.claude: hook Stop", "", ""},
			{markMissing, "agent home /home/u/.claude: hook SessionStart", "absent", brand.Command + " install --home /home/u/.claude"},
			{markInfo, "agent home /home/u/.claude: Orca status hook", "PreToolUse, Stop run Orca's status hook, which moves the workspace card on every prompt and competes with " + brand.Name + "'s projection of the turn", ""},
			{markOK, "skills in /home/u/.claude", "", ""},
			{markInfo, "agent home /home/u/.codex", "absent; " + brand.Name + " installs nothing there", ""},
			{markOK, "skills in /home/u/.agents", "", ""},
		}},
		{"Profile and intents", []check{{markMissing, "intent triage", "does not load:\n            when: unknown field pr.nope\n            body: empty", "fix the manifest; '" + brand.Command + " profile check' lists every problem"}}},
		{"Daemon", []check{
			{markOK, "store", "/home/u/.local/state/" + brand.StateDir() + "/store.db, not /home/user2/x nor x/home/u/y/abc/def/ghi", ""},
			{markOK, "LaunchAgent", "/home/u/Library/LaunchAgents/com.example.daemon.plist, runs serve, sweeps every 2m", ""},
			{markMissing, "menu bar LaunchAgent loaded", "loaded, last exit code 75", brand.Command + " app install"},
			{markMissing, "daemon warning", "fast: search author:me failed", ""},
			{markOK, "gh authenticated", "a duplicate the report drops", ""},
		}},
		{"Store and board", nil},
	})
}

func TestDoctorReadsAsSectionsOnATerminal(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	e := &Env{Out: &out, Home: "/home/u"}
	e.writeDoctor(termfmt.Term{TTY: true, Color: true, Width: 100}, doctorFixture())
	golden(t, "doctor_tty.golden", bracketed(out.String()))
}

func TestDoctorPipedKeepsItsLines(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	writeDoctorPlain(&out, doctorFixture())
	want := `  [ok     ] gh authenticated
  [ok     ] orca reachable
  [ok     ] config: /home/u/.config/` + brand.StateDir() + `/config.json
  [info   ] effective config: agent=claude github.review_prefix="REVIEW " views.orca.columns.working=reviewing views.orca.columns.ready=ready-to-merge github.automated_reviewers=[lintbot-ai] brief=true
  [info   ] turn budgets: turns.per_unit_per_day=5 (github) turns.per_day=20 (entry, session, slack, workspace)
  [ok     ] agent home /home/u/.claude: MCP server ` + brand.Name + `
  [ok     ] agent home /home/u/.claude: hook Stop
  [missing] agent home /home/u/.claude: hook SessionStart: absent
            fix with: ` + brand.Command + ` install --home /home/u/.claude
  [info   ] agent home /home/u/.claude: Orca status hook: PreToolUse, Stop run Orca's status hook, which moves the workspace card on every prompt and competes with ` + brand.Name + `'s projection of the turn
  [ok     ] skills in /home/u/.claude
  [info   ] agent home /home/u/.codex: absent; ` + brand.Name + ` installs nothing there
  [ok     ] skills in /home/u/.agents
  [missing] intent triage: does not load:
            when: unknown field pr.nope
            body: empty
            fix with: fix the manifest; '` + brand.Command + ` profile check' lists every problem
  [ok     ] store: /home/u/.local/state/` + brand.StateDir() + `/store.db, not /home/user2/x nor x/home/u/y/abc/def/ghi
  [ok     ] LaunchAgent: /home/u/Library/LaunchAgents/com.example.daemon.plist, runs serve, sweeps every 2m
  [missing] menu bar LaunchAgent loaded: loaded, last exit code 75
            fix with: ` + brand.Command + ` app install
  [missing] daemon warning: fast: search author:me failed

`
	if got := out.String(); got != want {
		t.Errorf("piped doctor changed:\n%s", got)
	}
	if n := pending(doctorFixture()); n != 4 {
		t.Errorf("%d pending", n)
	}
}

func TestDoctorStylesOnlyWhenForced(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.run("doctor")
	if strings.Contains(te.out.String(), "\x1b") || !strings.Contains(te.out.String(), "  [ok     ] config: ") {
		t.Fatalf("piped doctor:\n%s", te.out)
	}
	plain := te.out.String()
	te.out.Reset()
	te.Getenv = func(k string) string { return map[string]string{"CLICOLOR_FORCE": "1"}[k] }
	te.run("doctor")
	if out := te.out.String(); out != plain {
		t.Fatalf("CLICOLOR_FORCE changed the piped doctor:\n%s", out)
	}
	te.out.Reset()
	te.Getenv = func(k string) string { return map[string]string{"GH_FORCE_TTY": "100"}[k] }
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "\x1b[1mConfig\x1b[22m") || strings.Contains(out, "[ok     ]") {
		t.Fatalf("GH_FORCE_TTY doctor:\n%s", out)
	}
	te.out.Reset()
	te.Getenv = func(string) string { return "" }
	te.run("doctor", "--color", "always")
	if out := te.out.String(); !strings.Contains(out, "\x1b[1mConfig\x1b[22m") {
		t.Fatalf("--color always doctor:\n%s", out)
	}
	te.out.Reset()
	te.run("doctor", "--color", "never")
	if out := te.out.String(); strings.Contains(out, "\x1b") || !strings.Contains(out, "[ok     ]") {
		t.Fatalf("--color never piped:\n%s", out)
	}
}
