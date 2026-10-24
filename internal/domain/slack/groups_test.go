package slack

import "testing"

func TestGroupNamesComeFromMentions(t *testing.T) {
	h := map[string]string{}
	GroupHandles(h, "ping <!subteam^S0001|@team-a> and <!subteam^S0002>", "<!subteam^S0003|team-c>")
	for id, want := range map[string]string{"S0001": "@team-a", "S0002": UnknownGroup, "S0003": "@team-c", "S0009": UnknownGroup} {
		if got := GroupName(id, h); got != want {
			t.Errorf("GroupName(%s) = %q, want %q", id, got, want)
		}
	}
}
