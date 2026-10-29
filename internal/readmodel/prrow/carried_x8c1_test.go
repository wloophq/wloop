package prrow

import "testing"

func TestNudgeTargetsAndRefusalLiveOnlyInTheGitHubPlugin(t *testing.T) {
	t.Skip("carried from X8c1 review, retargeted to W4a by X8c3: NudgeTargets and NudgeRefusal stay on the host for the action facts pr.nudge.who, pr.nudge.why and pr.nudge.refusal " +
		"and the row's nudge offer, which read the row under the host's run and mute overlays (offers.go, adapter/facts/github/facts.go), while the plugin's github-nudge rule and nudge.prepare hold the same logic in plugins/github/nudge; " +
		"TestGitHubNudgeDraftsMatchTheNudgeOffer keeps them equal until W4a's nudgeable reads the plugin's stalled signal and the facts read the plugin's report")
}
