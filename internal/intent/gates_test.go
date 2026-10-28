package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/profile"
)

func loadGatesWith(t *testing.T, user string) (GatesDef, string) {
	t.Helper()
	b, err := os.ReadFile("../../profiles/default/" + GatesFile)
	if err != nil {
		t.Fatal(err)
	}
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, GatesFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	d, problems := loadGates(layers, fstest.MapFS{GatesFile: {Data: b}})
	return d, problems.Error()
}

func TestGatesTomlDeclaresKindsAndIntents(t *testing.T) {
	t.Parallel()
	d, problems := loadGatesWith(t, "")
	if problems != "" {
		t.Fatal(problems)
	}
	for _, k := range []gate.Kind{gate.KindDraft, gate.KindPlan, gate.KindQuestion, gate.KindPost, gate.KindProposal, gate.KindTrust, gate.KindStep} {
		if g, ok := d.FirstOf(k); !ok || g.Kind != k || g.Label == "" || len(g.Intents) == 0 {
			t.Errorf("first gate of %s: %+v %v", k, g, ok)
		}
	}
	draft, _ := d.Gate("draft")
	if len(draft.Intents) != 2 || draft.Intents[0].Name != "approve" || draft.Intents[0].Status != gate.Approved ||
		draft.Intents[1].Name != "discard" || draft.Intents[1].Status != gate.Rejected {
		t.Errorf("draft intents %+v", draft.Intents)
	}
	q, _ := d.Gate("question")
	if !q.Intents[0].Answer || q.Intents[1].Answer {
		t.Errorf("question intents %+v", q.Intents)
	}

	d, problems = loadGatesWith(t, `[gates.draft]
label = "Send it?"

[gates.draft.intents.later]
label = "Later"
status = "dismissed"

[gates.ship]
kind = "step"
label = "Ship it?"

[gates.ship.intents.go]
label = "Go"
status = "approved"
`)
	if problems != "" {
		t.Fatal(problems)
	}
	draft, _ = d.Gate("draft")
	if draft.Label != "Send it?" || len(draft.Intents) != 3 || draft.Intents[2].Name != "later" || draft.Kind != gate.KindDraft {
		t.Errorf("draft over a user layer %+v", draft)
	}
	if first, _ := d.FirstOf(gate.KindStep); first.Name != "review-diff" {
		t.Errorf("a user's gate comes after the bundled ones of its kind: %s", first.Name)
	}
	if _, ok := d.Gate("ship"); !ok {
		t.Error("the user's gate is not declared")
	}

	for user, want := range map[string]string{
		"[gates.wish]\nkind = \"wish\"\n":                       "gates.wish.kind",
		"[gates.wish]\nlabel = \"Wish\"\n":                      "gates.wish.kind: is required",
		"[gates.draft.intents.maybe]\nlabel = \"Maybe\"\n":      "gates.draft.intents.maybe.status: is required",
		"[gates.draft.intents.maybe]\nstatus = \"open\"\n":      "gates.draft.intents.maybe.status",
		"[gates.wish]\nkind = \"step\"\n[gates.wish.intents]\n": "gates.wish.intents: is required",
	} {
		_, problems := loadGatesWith(t, user)
		if !strings.Contains(problems, want) {
			t.Errorf("%q: problems %q, want %q", user, problems, want)
		}
	}
}

func TestAVerbsGateNamesADeclaredGate(t *testing.T) {
	t.Parallel()
	gates, problems := loadGatesWith(t, "")
	if problems != "" {
		t.Fatal(problems)
	}
	bundled := fstest.MapFS{TasksFile: {Data: []byte(defaultTasks(t))}}
	for gateName, want := range map[string]bool{"proposal": true, "nowhere": false} {
		dir := t.TempDir()
		user := "[verbs.done]\ngate = \"" + gateName + "\"\n[verbs.done.by]\nagent = \"gate\"\n"
		if err := os.WriteFile(filepath.Join(dir, TasksFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		_, problems := loadTasks([]profile.Layer{{Name: "user", Dir: dir}}, bundled, &gates)
		if got := !strings.Contains(problems.Error(), "verbs.done.gate: names no gate"); got != want {
			t.Errorf("gate %s: problems %q", gateName, problems.Error())
		}
	}
}
