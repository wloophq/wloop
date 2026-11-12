package archtest

import "testing"

func TestArchNoManifestNamesInCoreFlagsAPlantedSlackDraft(t *testing.T) {
	t.Skip("T1")
	flagged(t, "a planted tool name", nil, 1, `"slack_draft"`)
}
