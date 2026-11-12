package e2e

import "testing"

func TestDrafts(t *testing.T) {
	for _, s := range []string{"S1", "S2", "S3", "S4", "S5", "S6", "S7", "S8"} {
		t.Run(s, func(t *testing.T) {
			t.Skip("T11")
		})
	}
}
