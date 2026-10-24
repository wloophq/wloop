// Package workspace holds what a workspace manager reports, as values, and names its worktree as a unit.
package workspace

import "github.com/wloophq/wloop/internal/domain/unit"

// Codec is the unit codec of provider "workspace".
type Codec struct{}

func (Codec) Provider() unit.ProviderID { return unit.Workspace }

func (Codec) Valid(key string) error { return unit.OpaqueCodec{ID: unit.Workspace}.Valid(key) }

// UnitOf is the unit of the worktree at path: a workspace's unit key is its
// worktree's path, whichever manager owns it.
func UnitOf(path string) unit.Unit {
	if path == "" {
		return unit.Unit{}
	}
	return unit.Unit{Provider: unit.Workspace, Key: path}
}
