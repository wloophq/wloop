package daemon

import "testing"

func TestOmniboxResolveRoutePastedAddress(t *testing.T) {
	t.Skip("B9 POST /api/omnibox/resolve resolves a pasted address to its unit")
}

func TestOmniboxResolveRouteUnclaimedAddress(t *testing.T) {
	t.Skip("B9 an address no plugin claims falls to search and creates nothing")
}

func TestAskOverCap(t *testing.T) {
	t.Skip("B11 POST /api/omnibox/ask over the cap is 429 and asks nothing")
}

func TestAskAgentFails(t *testing.T) { t.Skip("B11 a failed agent call is 502") }

func TestAskStoresNothing(t *testing.T) { t.Skip("B11 an ask stores no turn, gate or draft") }
