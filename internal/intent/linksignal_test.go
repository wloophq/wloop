package intent

import (
	"strings"
	"testing"
)

func TestAnUndeclaredLinkSignalIsAProblem(t *testing.T) {
	_, problems := loadListsWith(t, string(defaultLists()), "[views.quiet]\nlabel = \"Quiet\"\nwhen = \"task.link_signal.stalled && task.link_signal.in_loop\"\n")
	if problems != "" {
		t.Fatalf("the GitHub plugin declares stalled and in_loop: %s", problems)
	}
	_, problems = loadListsWith(t, string(defaultLists()), "[views.quiet]\nlabel = \"Quiet\"\nwhen = \"task.link_signal.snoring\"\n")
	if !strings.Contains(problems, "views.quiet.when:") || !strings.Contains(problems, "task.link_signal.snoring") {
		t.Fatalf("a signal no plugin declares loads: %q", problems)
	}
}
