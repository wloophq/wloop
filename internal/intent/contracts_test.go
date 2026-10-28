package intent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const slackNudge = `summary = "Case"
provider = "task"
when = 'task.state == "waiting" && now >= task.nudge_due_at'
key = ["task.settled_at", "task.waiting_on"]

[[step]]
use = "draft.propose"
purpose = "nudge"
items = "owed"
per = "person"
effects = ["task.nudged"]
repeat_after = "1d"

[drafts.withdraw]
when = '!draft.person_waited'
`

func TestAWorkflowOnTasksProposesAndWithdrawsDrafts(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "my-nudge", slackNudge)
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) > 0 || len(l.Workflows) != 1 {
		t.Fatalf("%s", problems.Error())
	}
	w := l.Workflows[0]
	if w.Provider != "task" || w.Slot != "task" || w.Withdraw == nil || !slices.Equal(w.Steps[0].Args["effects"].([]string), []string{"task.nudged"}) {
		t.Fatalf("%+v", w)
	}
	if reads := strings.Join(w.Withdraw.Reads(), ","); !strings.Contains(reads, "draft.person_waited") {
		t.Fatalf("withdraw reads %s", reads)
	}
}

func TestDraftProposeRefusesBadArguments(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{`purpose = "nudge"`, `purpose = ""`, "step[0].purpose: is required"},
		{`purpose = "nudge"`, "purpose = \"nudge\"\ndelivery = \"fax\"", `step[0].delivery: "fax" is not one of`},
		{`items = "owed"`, `items = "mine"`, `step[0].items: "mine" is not one of`},
		{`per = "person"`, `per = "team"`, `step[0].per: "team" is not one of`},
		{`effects = ["task.nudged"]`, `effects = ["task.nudged", "task.closed"]`, `step[0].effects[1]: "task.closed" is not one of`},
		{`repeat_after = "1d"`, `repeat_after = "soon"`, `step[0].repeat_after: "soon" is not a duration`},
		{"when = '!draft.person_waited'", "when = '!draft.nope'", "unknown field draft.nope"},
	} {
		root := t.TempDir()
		writeWorkflow(t, root, "my-nudge", strings.Replace(slackNudge, c.from, c.to, 1))
		_, problems := Load(userLayer(root), nil, testBase(), nil)
		if !strings.Contains(problems.Error(), c.want) {
			t.Errorf("%s: %s, want %q", c.to, problems.Error(), c.want)
		}
	}
}

func TestAReactionDraftNeedsItsEmoji(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "my-nudge", strings.Replace(slackNudge, `purpose = "nudge"`, "purpose = \"close\"\ndelivery = \"reaction\"", 1))
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if !strings.Contains(problems.Error(), "step[0].reaction: is required with delivery") {
		t.Fatal(problems.Error())
	}
}

const slackTurn = `summary = "Case"
provider = "slack"
when = 'brief.kind == "request"'

[[step]]
use = "turn.start"
intent = "act"
runner = "headless"
`

func TestATurnStartOutsidePullRequestsNeedsADailyLimit(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "implement-requests", slackTurn)
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if !strings.Contains(problems.Error(), "limits.per_day: is required") {
		t.Fatalf("%s", problems.Error())
	}
	root = t.TempDir()
	writeWorkflow(t, root, "implement-requests", slackTurn+"\n[limits]\nper_day = 3\n")
	if _, problems := Load(userLayer(root), nil, testBase(), nil); strings.Contains(problems.Error(), "limits.per_day") || strings.Contains(problems.Error(), "brief.kind") {
		t.Fatalf("%s", problems.Error())
	}
	root = t.TempDir()
	writeWorkflow(t, root, "implement-requests", strings.Replace(slackTurn, `runner = "headless"`, `runner = "batch"`, 1)+"\n[limits]\nper_day = 3\n")
	if _, problems := Load(userLayer(root), nil, testBase(), nil); !strings.Contains(problems.Error(), `step[0].runner: "batch" is not one of`) {
		t.Fatalf("%s", problems.Error())
	}
}

func TestTheManifestKeysThatReplaceIntentNameSwitchesDecode(t *testing.T) {
	root := t.TempDir()
	m := strings.ReplaceAll(strings.Replace(minimal, `workspace = "review"`, `workspace = { repo_input = "repo" }
provider = ["github", "slack"]
role = "headless"
why = "{{.pr.address.reason}}"
drafts.allowed_effects = ["task.nudged", "unit.closed"]
brief = { need = ["solution"] }`, 1), "{{.pr.slug}}", "{{.unit.ref}}") + `
[inputs.repo]
type = "text"
flag = "--repo"
help = "owner/name"

[[refuse]]
when = 'pr.mute.refusal != ""'
why = "muted"
`
	writeIntent(t, root, "implement", m, "{{.Headline}}")
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) > 0 {
		t.Fatalf("%s", problems.Error())
	}
	d, ok := l.Registry.Get("implement")
	if !ok || d.WorkspaceRepo == nil || d.WorkspaceRepo.RepoInput != "repo" || d.Provider != "github" || len(d.Providers) != 2 ||
		d.Role != "headless" || len(d.AllowedEffects) != 2 || d.Why.Src != "{{.pr.address.reason}}" ||
		len(d.Refuse) != 1 || d.Refuse[0].When.Src != `pr.mute.refusal != ""` || d.Brief.Need[0] != "solution" {
		t.Fatalf("%+v", d)
	}
	if got, ok := l.Registry.ByRole("headless", "slack"); !ok || got.Name != "implement" {
		t.Fatalf("by role: %v %v", got.Name, ok)
	}
}

func TestBadManifestKeysAreRefused(t *testing.T) {
	for edit, want := range map[string]string{
		`workspace = { repo = "nope" }`:                                           `workspace.repo: "nope" is not a repository`,
		`workspace = { repo_input = "repo" }`:                                     `workspace.repo_input: "repo" is no input of the intent`,
		`workspace = { repo = "a/b", repo_input = "x" }`:                          "exactly one of repo or repo_input",
		"workspace = \"review\"\nrole = \"boss\"":                                 `role: "boss" is not one of`,
		"workspace = \"review\"\ndrafts.allowed_effects = [\"unit.opened\"]":      `drafts.allowed_effects[0]: "unit.opened" is not one of`,
		"workspace = \"review\"\neffects = [\"unit.closed\"]":                     `effects[0]: "unit.closed" is not one of`,
		"workspace = \"review\"\nwhy = \"{{.pr.nope}}\"":                          `why: `,
		"workspace = \"review\"\nrefuse = [{ when = 'pr.nope', why = \"held\" }]": "refuse[0].when: ",
		"workspace = \"review\"\nrefuse = [{ when = 'pr.held' }]":                 "refuse[0].why: is required",
		"workspace = \"review\"\nprompt = \"prompts/x.md\"":                       "prompt: is read only with builds",
	} {
		root := t.TempDir()
		writeIntent(t, root, "implement", strings.Replace(minimal, `workspace = "review"`, edit, 1), "{{.Headline}}")
		_, problems := Load(userLayer(root), nil, testBase(), nil)
		if !strings.Contains(problems.Error(), want) {
			t.Errorf("%s: %s, want %q", edit, problems.Error(), want)
		}
	}
}

func TestTwoIntentsOfOneLayerCannotHoldARole(t *testing.T) {
	root := t.TempDir()
	m := strings.Replace(minimal, `workspace = "review"`, "workspace = \"review\"\nrole = \"pr.review\"", 1)
	writeIntent(t, root, "my-review", m, "{{.Headline}}")
	writeIntent(t, root, "their-review", strings.Replace(m, `title = "ask"`, `title = "their"`, 1), "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if !strings.Contains(problems.Error(), "holds the role pr.review on github units already") {
		t.Fatalf("%s", problems.Error())
	}
}

func TestABuilderIntentNeedsNoTurnKeys(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "unit-brief", `label = "Brief"
summary = "One brief per unit"
builds = "brief"
provider = ["github", "slack"]
prompt = "prompts/brief.md"
schema = "schema.json"

[headline]
resumed = "x"
[[headline.new]]
line = "x"
`, "{{.Headline}}")
	if err := os.WriteFile(filepath.Join(root, "intents", "unit-brief", "schema.json"), []byte(`{"type":"object","properties":{"solution":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) > 0 {
		t.Fatalf("%s", problems.Error())
	}
	if d, ok := l.Registry.Builder("brief", "slack"); !ok || d.Prompt != "prompts/brief.md" || d.Schema != "schema.json" {
		t.Fatalf("%+v %v", d, ok)
	}
}
