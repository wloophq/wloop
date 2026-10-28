package intent

import (
	"strings"
	"testing"
	"testing/fstest"
)

func intentProblems(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	writeIntent(t, root, "ask-author", manifest, "{{.Headline}}\n")
	_, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	return problems.Error()
}

func workflowProblems(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, "mine", manifest)
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}}
	_, problems := Load(userLayer(root), bundled, testBase(), nil)
	return problems.Error()
}

func wantProblem(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("lacks %q:\n%s", want, got)
	}
}

func TestTheDecodersCheckValuesInTheTablesWords(t *testing.T) {
	t.Parallel()
	for edit, want := range map[[2]string]string{
		{`workspace = "review"`, "workspace = \"review\"\nrunners = [\"sesion\"]"}:                                                        `runners[0]: "sesion" is not one of "session" or "headless"; did you mean "session"?`,
		{`workspace = "review"`, `workspace = "linked_else_mian"`}:                                                                        `workspace: "linked_else_mian" is not one of "review", "author", "linked_else_main" or "given"; did you mean "linked_else_main"?`,
		{`workspace = "review"`, "workspace = \"review\"\nprovider = \"slak\""}:                                                           `provider: "slak" is not one of "entry", "github", "session", "slack", "workspace" or "any"; did you mean "slack"?`,
		{`workspace = "review"`, "workspace = \"review\"\nprovider = [\"github\", \"slak\"]"}:                                             `provider[1]: "slak" is not one of`,
		{"[record]", "[drafts.approval]\nmode = \"dry-run\"\n\n[record]"}:                                                                 `drafts.approval.mode: "dry-run" is not one of "send", "delay", "wait" or "dry_run"; did you mean "dry_run"?`,
		{"[record]", "[[also_profile]]\nprofile = \"review\"\ndefine = \"x\"\nwhen = \"true\"\ndims = { stag = \"first\" }\n\n[record]"}:  `also_profile[0].dims.stag: unknown key; did you mean "stage"?`,
		{"[record]", "[[also_profile]]\nprofile = \"review\"\ndefine = \"x\"\nwhen = \"true\"\ndims = { stage = \"frist\" }\n\n[record]"}: `also_profile[0].dims.stage: "frist" is not one of "first" or "follow-up"; did you mean "first"?`,
	} {
		wantProblem(t, intentProblems(t, strings.Replace(minimal, edit[0], edit[1], 1)), want)
	}
}

// A guess never turns an unknown approval mode into one that sends or runs
// with no person approving it.
func TestNoGuessReleasesADraftOrARun(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"record", "sned", "dalay"} {
		got := intentProblems(t, strings.Replace(minimal, "[record]", "[drafts.approval]\nmode = \""+mode+"\"\n\n[record]", 1))
		wantProblem(t, got, `drafts.approval.mode: "`+mode+`" is not one of "send", "delay", "wait" or "dry_run"`)
		if strings.Contains(got, "did you mean") {
			t.Errorf("%s: %s", mode, got)
		}
	}
	got := workflowProblems(t, quietSession+"\n[approval]\nmode = \"rnu\"\n")
	wantProblem(t, got, `approval.mode: "rnu" is not one of "run", "delay" or "wait"`)
	if strings.Contains(got, "did you mean") {
		t.Error(got)
	}
}

func TestWorkflowValuesAreTheTables(t *testing.T) {
	t.Parallel()
	for edit, want := range map[[2]string]string{
		{`provider = "session"`, `provider = "sesion"`}:                           `provider: "sesion" is not one of "entry", "github", "session", "slack" or "workspace"; did you mean "session"?`,
		{`use = "session.close"`, `use = "session.clsoe"`}:                        `step[0].use: "session.clsoe" is not one of`,
		{`use = "session.close"`, "use = \"session.close\"\nreasn = \"x\""}:       `step[0].reasn: unknown key`,
		{`when = 'session`, "[trigger]\non = [\"zzz\"]\n\nwhen = 'session"}:       `trigger.on[0]: "zzz" is not one of`,
		{`session.managed &&`, `session.managed && session.manager == "orca" &&`}: "",
	} {
		got := workflowProblems(t, strings.Replace(quietSession, edit[0], edit[1], 1))
		if want == "" {
			if got != "" {
				t.Errorf("%s: %s", edit[1], got)
			}
			continue
		}
		wantProblem(t, got, want)
	}
	wantProblem(t, workflowProblems(t, strings.Replace(quietSession, `use = "session.close"`, "use = \"session.clsoe\"", 1)), `did you mean "session.close"?`)
}

func TestProviderKeyedEnumsHoldForTheirProvider(t *testing.T) {
	t.Parallel()
	wantProblem(t, intentProblems(t, minimal+"\n[action]\nsurfaces = [\"row\"]\nwhen = 'row.kind == \"reviw_due\"'\n"), `row.kind is`)
	wantProblem(t, workflowProblems(t, strings.NewReplacer(`provider = "session"`, `provider = "github"`,
		`session.managed && session.idle_for > config.cleanup.idle`, `brief.kind == "zzz"`, `session.close`, `turn.dismiss`).Replace(quietSession)), `brief.kind is`)
	slack := strings.NewReplacer(`provider = "session"`, `provider = "slack"`,
		`session.managed && session.idle_for > config.cleanup.idle`, `brief.kind == "zzz"`, "[[step]]\nuse = \"session.close\"\n",
		"[[step]]\nuse = \"notify.self\"\n").Replace(quietSession)
	if got := workflowProblems(t, slack); strings.Contains(got, "brief.kind is") {
		t.Errorf("a slack brief's kind is open: %s", got)
	}
}

func TestVisibilityIsDeclaredLowercase(t *testing.T) {
	t.Parallel()
	wantProblem(t, workflowProblems(t, strings.NewReplacer(`provider = "session"`, `provider = "github"`,
		`session.managed && session.idle_for > config.cleanup.idle`, `pr.visibility == "PUBLIC"`, `session.close`, `turn.dismiss`).Replace(quietSession)),
		`did you mean "public"?`)
}

// One name and one spelling per pull request fact, whatever slot reads it.
func TestPRFactsAreSpelledAlikeInEverySlot(t *testing.T) {
	t.Parallel()
	manifest := strings.Replace(minimal, `workspace = "review"`, "workspace = \"review\"\nwhy = \"{{.pr.repo}} {{.pr.repo_name}}\"", 1) + `
[[refuse]]
when = 'pr.repo == "acme/api" && pr.repo_name == "api" && pr.state == "OPEN"'
why = "no"

[action]
surfaces = ["row"]
when = 'pr.repo == "acme/api" && pr.repo_name == "api" && pr.state == "OPEN"'
`
	if got := intentProblems(t, manifest); got != "" {
		t.Error(got)
	}
}

func TestAWorkflowNamesOnlyItsProvidersEffects(t *testing.T) {
	t.Parallel()
	task := `summary = "Nudge a waiting task"
provider = "entry"
when = 'task.waiting_days > 2'

[[step]]
use = "draft.propose"
purpose = "nudge"
effects = ["pr.nudged"]
`
	wantProblem(t, workflowProblems(t, task), "step[0].effects[0]: pr.nudged applies to github units only; a entry workflow cannot name it")
}
