package inventory

import "testing"

func TestTheHostKeepsNoCopyOfGitHubsRows(t *testing.T) {
	t.Skip("carried from X8a2 review: SaveReported (with ghsource.Reported) copies each reported pull request into the host's pull_requests, pr_details and overrides.tracked, " +
		"and UpsertPR, SaveSnapshots, SetReasons, SetTracked, SetWorkspace and ReplaceWorkspaces still write them for review, remove and test seeding; " +
		"X8b reads the snapshots and deletes them with the inventory's PR parts")
}
