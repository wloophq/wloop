//go:build !unix

package procgroup

import "testing"

func TestCancelStopsTheWholeGroup(t *testing.T) {
	t.Skip("process groups are a unix mechanism; elsewhere Command is exec.CommandContext with a wait delay")
}
