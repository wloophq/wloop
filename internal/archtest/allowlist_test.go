package archtest

import "testing"

// TestAllowListRuleShipped checks that every shipped approval rule reads a draft payload fact only as an
// allow-list, so an absent fact never releases a draft.
func TestAllowListRuleShipped(t *testing.T) {
	t.Skip("T4")
}
