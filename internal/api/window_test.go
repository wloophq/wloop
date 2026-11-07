package api

import "testing"

func TestMergeKeepsTheLaterWindowRequest(t *testing.T) {
	c := Change{Window: &WindowRequest{Action: WindowOpen, Route: "task=t-1"}}
	c.Merge(Change{Type: "changed", Epoch: "e", Rev: 4})
	if c.Window == nil || c.Window.Route != "task=t-1" {
		t.Fatalf("a change without a window request dropped the pending one: %+v", c.Window)
	}
	c.Merge(Change{Window: &WindowRequest{Action: WindowOpen, Route: "task=t-2"}})
	if c.Window == nil || c.Window.Route != "task=t-2" || c.Rev != 4 {
		t.Fatalf("merged %+v rev %d, want route task=t-2 at rev 4", c.Window, c.Rev)
	}
}
