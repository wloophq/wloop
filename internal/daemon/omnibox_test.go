package daemon

import "testing"

func TestOmniboxResolveRoutePastedAddress(t *testing.T) {
	t.Skip("B9 POST /api/omnibox/resolve resolves a pasted address to its unit")
}

func TestOmniboxResolveRouteUnclaimedAddress(t *testing.T) {
	t.Skip("B9 an address no plugin claims falls to search and creates nothing")
}
