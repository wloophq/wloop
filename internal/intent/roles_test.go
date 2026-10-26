package intent

import (
	"strings"
	"testing"
)

func TestARoleImpliesItsRound(t *testing.T) {
	for _, c := range []struct{ role, kind, reconcile, prepare string }{
		{"pr.review", "review", "my_review", ""},
		{"pr.address", "address", "", ""},
		{"pr.nudge", "", "", "nudge.prepare"},
		{"headless", "", "", ""},
	} {
		root := t.TempDir()
		writeIntent(t, root, "implement", strings.Replace(minimal, `workspace = "review"`, "workspace = \"given\"\nrole = \""+c.role+"\"", 1), "{{.Headline}}")
		l, problems := Load(userLayer(root), nil, testBase(), nil)
		if len(problems) > 0 {
			t.Fatalf("%s: %s", c.role, problems.Error())
		}
		d, _ := l.Registry.Get("implement")
		if d.RoundKind != c.kind || d.Reconcile != c.reconcile || d.Prepare != c.prepare {
			t.Errorf("%s: round kind %q reconcile %q prepare %q", c.role, d.RoundKind, d.Reconcile, d.Prepare)
		}
	}
}

func TestTheKeysARoleImpliesAreUnknown(t *testing.T) {
	for _, key := range []string{`round_kind = "review"`, `cap = "rounds"`, `reason = "address_round"`, `reconcile = "my_review"`,
		`prepare = "nudge.prepare"`, `context = "synthetic"`, `launch = "probe"`, `policy = "probe"`, `drives_row = true`, `board = "review"`,
		`on_sent = ["plan.cleared"]`, `refuse_when = "pr.nudge.refusal"`, `reason_fact = "pr.review.reason"`, `records = "true"`, `modes = ["session"]`,
		`key = "Q"`, `draft_effects = ["unit.closed"]`} {
		root := t.TempDir()
		writeIntent(t, root, "implement", strings.Replace(minimal, `workspace = "review"`, "workspace = \"given\"\n"+key, 1), "{{.Headline}}")
		_, problems := Load(userLayer(root), nil, testBase(), nil)
		name := key[:strings.Index(key, " ")]
		if !strings.Contains(problems.Error(), ": "+name+": unknown key") {
			t.Errorf("%s: %s", key, problems.Error())
		}
	}
}

func TestDrivesRowMovesWithTheRole(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "my-review", strings.Replace(minimal, `workspace = "review"`, "workspace = \"review\"\nrole = \"pr.review\"", 1), "{{.Headline}}")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	mine, _ := l.Registry.Get("my-review")
	bundled, _ := l.Registry.Get("review")
	if !mine.DrivesRow || mine.Reconcile != "my_review" || bundled.DrivesRow {
		t.Fatalf("mine drives %t (reconcile %q), the bundled review drives %t", mine.DrivesRow, mine.Reconcile, bundled.DrivesRow)
	}
}

func TestTwoDriversInOneWorkspaceFail(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "my-review", strings.Replace(minimal, `workspace = "review"`, "workspace = \"author\"\nrole = \"pr.review\"", 1), "{{.Headline}}")
	_, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if !strings.Contains(problems.Error(), "my-review/intent.toml:3:13: workspace: address drives the rows of author workspaces already") {
		t.Fatal(problems.Error())
	}
}

func TestAPullRequestRoleNeedsGitHubUnits(t *testing.T) {
	root := t.TempDir()
	m := strings.ReplaceAll(strings.Replace(minimal, `workspace = "review"`, "workspace = \"given\"\nprovider = \"slack\"\nrole = \"pr.nudge\"", 1), "{{.pr.slug}}", "{{.unit.ref}}")
	writeIntent(t, root, "implement", m, "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if !strings.Contains(problems.Error(), "role: pr.nudge runs on github units; a slack intent cannot hold it") {
		t.Fatal(problems.Error())
	}
}
