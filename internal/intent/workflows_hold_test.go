package intent

import (
	"strings"
	"testing"
)

func TestADraftHoldIsADurationOrADurationOfTheConfig(t *testing.T) {
	t.Parallel()
	for hold, problem := range map[string]string{
		"config.drafts.repeat_after":    "",
		"2d":                            "",
		"config.gtd.stale_waiting_days": "is not a duration of the config slot",
		"config.nope":                   "is not a duration of the config slot",
		"soon":                          "is not a duration",
	} {
		root := t.TempDir()
		writeWorkflow(t, root, "asks", `subject = "slack"
when = 'slack.items_unowned > 0'

[trigger]
every = "sweep"

[[step]]
use = "draft.propose"
purpose = "ask_owner"
hold = "`+hold+`"
`)
		_, problems := Load(userLayer(root), nil, testBase(), nil)
		got := ""
		if problems != nil {
			got = problems.Error()
		}
		if problem == "" && strings.Contains(got, "hold") || problem != "" && !strings.Contains(got, problem) {
			t.Errorf("hold %q: %q", hold, got)
		}
	}
}
