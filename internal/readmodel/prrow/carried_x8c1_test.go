package prrow

import "testing"

func TestNudgeTargetsAndRefusalLiveOnlyInTheGitHubPlugin(t *testing.T) {
	t.Skip("carried from X8c1 review (X8c3): NudgeTargets and NudgeRefusal stay on the host for the action facts pr.nudge.who, pr.nudge.why and pr.nudge.refusal " +
		"(offers.go, adapter/facts/github/facts.go:656) while the plugin's github-nudge rule and nudge.prepare hold the same logic in plugins/github/nudge; " +
		"TestGitHubNudgeDraftsMatchTheNudgeOffer keeps them equal until the facts read the plugin's and these two leave internal/")
}
