package intent

import (
	"slices"
	"strings"
	"testing"
)

func TestAProfileDeclaresAVerbGoDoesNotList(t *testing.T) {
	t.Parallel()
	user := `
[verbs.escalate]
label   = "Escalate"
summary = "Move it to Next and tell your lead."
from    = ["inbox", "waiting"]
to      = "next"
by      = { person = "true", workflow = "true" }
`
	d, problems := loadTasksWith(t, defaultTasks(t), user)
	if problems != "" {
		t.Fatal(problems)
	}
	if n := len(d.VerbOrder); n == 0 || d.VerbOrder[n-1] != "escalate" || d.VerbOrder[0] != "next" {
		t.Fatalf("verb order %v, want the bundled verbs, then the user's", d.VerbOrder)
	}
	if from, to, ok := d.Move("escalate"); !ok || to != "next" || !slices.Equal(from, []string{"inbox", "waiting"}) {
		t.Fatalf("escalate moves %v to %q (%v)", from, to, ok)
	}
	if !slices.Contains(d.VerbKinds(), "verb_escalate") {
		t.Fatalf("verb kinds %v lack verb_escalate", d.VerbKinds())
	}

	for _, c := range []struct{ user, want string }{
		{"[verbs.\"Escalate now\"]\nlabel = \"E\"\nsummary = \"S\"\n", "is no verb name"},
		{"[verbs.ping]\nlabel = \"Ping\"\nsummary = \"Wait on someone\"\nhandler = \"waiting\"\nby = { person = \"true\" }\n", "verbs.ping.input: is required"},
		{"[verbs.ping]\nlabel = \"Ping\"\n", "verbs.ping.summary: is required"},
	} {
		if _, problems := loadTasksWith(t, defaultTasks(t), c.user); !strings.Contains(problems, c.want) {
			t.Errorf("%q: problems %q, want %q", c.user, problems, c.want)
		}
	}
}
