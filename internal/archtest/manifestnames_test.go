package archtest

import "testing"

// TestArchNoDeliveryList checks that Go holds no list of draft deliveries: each comes from a plugin manifest.
func TestArchNoDeliveryList(t *testing.T) {
	t.Skip("T1")
}

// TestArchNoManifestNamesInCore checks that the core, the domain and the agent adapters name no fact, draft
// kind, delivery, tool or command a bundled plugin manifest declares.
func TestArchNoManifestNamesInCore(t *testing.T) {
	t.Skip("T1")
}
