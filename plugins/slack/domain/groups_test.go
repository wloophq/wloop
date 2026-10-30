package slack

import "testing"

func TestGroupNamesComeFromMentions(t *testing.T) {
	h := map[string]string{}
	GroupHandles(h, "ping <!subteam^S0000000001|@team-a> and <!subteam^S0000000002>", "<!subteam^S0000000003|team-c>")
	for id, want := range map[string]string{"S0000000001": "@team-a", "S0000000002": UnknownGroup, "S0000000003": "@team-c", "S0000000009": UnknownGroup} {
		if got := GroupName(id, h); got != want {
			t.Errorf("GroupName(%s) = %q, want %q", id, got, want)
		}
	}
}
