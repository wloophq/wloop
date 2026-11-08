package slack

import "testing"

func TestConversationsMembersIsPacedAtItsTier(t *testing.T) {
	if got := defaultPerMinute["conversations.members"]; got != tier4 {
		t.Fatalf("conversations.members paced at %d a minute, want Slack's tier 4 floor %d", got, tier4)
	}
}
