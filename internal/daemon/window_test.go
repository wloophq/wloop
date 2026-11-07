package daemon

import "testing"

func TestWindowRequestReachesOnlyTheWindowClient(t *testing.T) {
	t.Skip("A2 a window request reaches only the window client")
}

func TestWindowRequestIsNotDeliveredWithoutAWindowClient(t *testing.T) {
	t.Skip("A2 without a window client the request is not delivered")
}

func TestWindowRequestRefusesAnUnknownAction(t *testing.T) {
	t.Skip("A2 an unknown window action is refused")
}

func TestWindowRequestLeavesTheRowPositionAlone(t *testing.T) {
	t.Skip("A2 a window request leaves the hub position alone")
}
