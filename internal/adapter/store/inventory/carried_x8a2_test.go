package inventory

import "testing"

func TestTheHostKeepsNoCopyOfGitHubsRows(t *testing.T) {
	t.Skip("carried from X8a2 review: SaveReported (with ghsource.Reported) copies each reported pull request into the host's pull_requests, pr_details and overrides.tracked, " +
		"and UpsertPR, SaveSnapshots, SetReasons, SetTracked, SetWorkspace and ReplaceWorkspaces still write them for review, remove and test seeding; " +
		"rows, cards, items and source facts read the snapshots since X8b; the round sends, the presenter and the commands still read them, X8c3 deletes them with the inventory's PR parts")
}
