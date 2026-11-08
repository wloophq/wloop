package api

import (
	"slices"
	"testing"
)

func TestMergeKeepsEveryWindowRequestInOrder(t *testing.T) {
	open := WindowRequest{Action: WindowOpen, Route: "task=t-1"}
	hide := WindowRequest{Action: WindowHidePanel}
	c := Change{Windows: []WindowRequest{open}}
	c.Merge(Change{Type: "changed", Epoch: "e", Rev: 4})
	c.Merge(Change{Windows: []WindowRequest{hide}})
	if !slices.Equal(c.Windows, []WindowRequest{open, hide}) || c.Rev != 4 {
		t.Fatalf("merged %+v rev %d, want open then hide-panel at rev 4", c.Windows, c.Rev)
	}
}
