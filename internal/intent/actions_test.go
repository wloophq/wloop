package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func defaultProfile() []byte {
	b, err := os.ReadFile("../../profiles/default/actions.toml")
	if err != nil {
		panic(err)
	}
	return b
}

func TestEveryActionHasASummary(t *testing.T) {
	actions, problems := loadActions(nil, os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, id := range builtin.BuiltinActionIDs {
		a := actions[id]
		if a.Label == "" || a.Summary == "" {
			t.Errorf("%s: label %q, summary %q", id, a.Label, a.Summary)
		}
	}
}

func actionsProblems(t *testing.T, bundled, user string) string {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ActionsFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	_, problems := loadActions(layers, fstest.MapFS{ActionsFile: {Data: []byte(bundled)}})
	return problems.Error()
}

func TestABuiltinActionWithoutATableFailsAtLoad(t *testing.T) {
	src := strings.Replace(string(defaultProfile()), "[plan_dismiss]", "[plan_dismis]", 1)
	got := actionsProblems(t, src, "")
	for _, want := range []string{"plan_dismis: names no built-in action; did you mean \"plan_dismiss\"?", "plan_dismiss: is missing"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestAUserTableNamingNoActionFails(t *testing.T) {
	got := actionsProblems(t, string(defaultProfile()), "[pasue]\nsummary = \"x\"\n")
	if !strings.Contains(got, "pasue: names no built-in action; did you mean \"pause\"?") {
		t.Errorf("%s", got)
	}
}

func TestADraftActionOfAUserActionsTomlNamesItsGateIntent(t *testing.T) {
	got := actionsProblems(t, string(defaultProfile()), "[\"draft.approve\"]\nlabel = \"Ship it\"\n[\"draft.discard\"]\nlabel = \"Drop\"\n")
	for _, want := range []string{`draft.approve: names no built-in action: it is gates.draft.intents.approve of gates.toml now`,
		`draft.discard: names no built-in action: it is gates.draft.intents.discard of gates.toml now`} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestARowGateActionOfAUserActionsTomlNamesItsGateIntent(t *testing.T) {
	got := actionsProblems(t, string(defaultProfile()), "[run_now]\nlabel = \"Go\"\n[trust_run]\nlabel = \"Trust\"\n[answer]\nlabel = \"Reply\"\n[continue]\nlabel = \"Go on\"\n")
	for _, want := range []string{`run_now: names no built-in action: it is gates.plan.intents.run_now of gates.toml now`,
		`trust_run: names no built-in action: it is gates.trust.intents.trust_run of gates.toml now`,
		`answer: names no built-in action: it is gates.question.intents.answer of gates.toml now`,
		`continue: names no built-in action: it is gates.question.intents.continue of gates.toml now`} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}

func TestABuiltinActionWithoutLabelOrSummaryFails(t *testing.T) {
	got := actionsProblems(t, "", "")
	if !strings.Contains(got, "take_over: is missing") {
		t.Errorf("%s", got)
	}
	got = actionsProblems(t, string(defaultProfile()), "[pause]\nlabel = \"\"\n")
	if !strings.Contains(got, "pause.label: is required") {
		t.Errorf("%s", got)
	}
	got = actionsProblems(t, string(defaultProfile()), "[pause]\nsummary = \"\"\n")
	if !strings.Contains(got, "pause.summary: is required") {
		t.Errorf("%s", got)
	}
}

func TestAUserActionTableLaysItsKeysOverTheBundledOnes(t *testing.T) {
	dir := t.TempDir()
	user := "[mute]\nsummary = \"Quiet for a while\"\nnote = \"{{.pr.nope}}\"\n\n[mute.input]\nhelp = \"3d\"\n"
	if err := os.WriteFile(filepath.Join(dir, ActionsFile), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	got, problems := loadActions([]profile.Layer{{Name: "user", Dir: dir}}, fstest.MapFS{ActionsFile: {Data: defaultProfile()}})
	if len(problems) != 1 || !strings.Contains(problems.Error(), "mute.note") {
		t.Fatalf("%v", problems)
	}
	m := got["mute"]
	if m.Label != "Mute" || m.Summary != "Quiet for a while" || m.Input.Label != "Mute for" || m.Input.Help != "3d" {
		t.Errorf("%+v", m)
	}
}

func TestActionsFileKeysFollowTheGrammar(t *testing.T) {
	bundled := fstest.MapFS{ActionsFile: {Data: []byte("[skip]\nlabel = \"Skip\"\nsummari = \"Skips\"\n")}}
	_, problems := loadActions(nil, bundled)
	if !strings.Contains(problems.Error(), "skip.summari: unknown key; did you mean \"summary\"?") {
		t.Errorf("%s", problems.Error())
	}
}

func TestAnEmptyWhyFailsAtLoad(t *testing.T) {
	got := actionsProblems(t, string(defaultProfile()), "[take_over]\nwhy = \"\"\n")
	if !strings.Contains(got, "take_over.why: may not be empty") {
		t.Errorf("%s", got)
	}
}
