package gate

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestByUser(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		g    Gate
		want bool
	}{
		{"a person", Gate{By: task.ActorPerson}, true},
		{"an agent through a session the user drives", Gate{By: task.ActorAgent, BySession: "s-user"}, true},
		{"an agent through no session", Gate{By: task.ActorAgent}, false},
		{"a tool that posted at once", Gate{By: task.ActorAgent, BySession: "s-user", Rule: "slack_send_message"}, false},
		{"a rule", Gate{By: task.ActorWorkflow, Rule: "gates.approval rule 0"}, false},
		{"a window", Gate{By: task.ActorWorkflow, Rule: "window"}, false},
	} {
		if got := c.g.ByUser(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
