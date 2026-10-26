package intent

import (
	"strings"
	"testing"
)

func TestRepeatAfterIsADurationOrADurationOfTheConfig(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"repeat_after", "repeat_after_sent"} {
		for v, problem := range map[string]string{
			"config.drafts.repeat_after":    "",
			"2d":                            "",
			"config.gtd.stale_waiting_days": "is not a duration of the config slot",
			"config.nope":                   "is not a duration of the config slot",
			"soon":                          "is not a duration",
			"0s":                            "longer than zero",
		} {
			root := t.TempDir()
			writeWorkflow(t, root, "asks", `summary = "Ask who owns"
provider = "slack"
when = 'slack.items_unowned > 0'

[[step]]
use = "draft.propose"
purpose = "ask_owner"
`+arg+` = "`+v+`"
`)
			_, problems := Load(userLayer(root), nil, testBase(), nil)
			got := ""
			if problems != nil {
				got = problems.Error()
			}
			if problem == "" && got != "" || problem != "" && !strings.Contains(got, "step[0]."+arg+": ") || !strings.Contains(got, problem) {
				t.Errorf("%s %q: %q", arg, v, got)
			}
		}
	}
}

func TestOnlyRepeatAfterSentRepeatsNever(t *testing.T) {
	t.Parallel()
	for arg, ok := range map[string]bool{"repeat_after_sent": true, "repeat_after": false} {
		root := t.TempDir()
		writeWorkflow(t, root, "asks", `summary = "Ask who owns"
provider = "slack"
when = 'slack.items_unowned > 0'

[[step]]
use = "draft.propose"
purpose = "ask_owner"
`+arg+` = "forever"
`)
		if _, problems := Load(userLayer(root), nil, testBase(), nil); (problems == nil) != ok {
			t.Errorf("%s = forever: %v", arg, problems)
		}
	}
}
