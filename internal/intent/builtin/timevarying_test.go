package builtin

import "testing"

func TestTimeVaryingFactsAreMarkedInTheTable(t *testing.T) {
	for _, name := range []string{"pr.silent_days", "task.check_on", "slack.items_unowned_age_days"} {
		if !TimeVarying(name) {
			t.Errorf("%s changes as time passes, the table does not say so", name)
		}
	}
	for _, name := range []string{"pr.fingerprint", "slack.fingerprint", "pr.silent", "no.such_fact"} {
		if TimeVarying(name) {
			t.Errorf("%s is marked as changing with time", name)
		}
	}
}
