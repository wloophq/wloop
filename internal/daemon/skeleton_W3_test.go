package daemon

import "testing"

func TestMutingAFollowedPRNoTaskHoldsMutesItsTask(t *testing.T) {
	t.Skip("W3: W2a review, muting a followed pull request no open task holds answers 404, and a task made after the mute is not muted, until each followed pull request has its one task")
}
