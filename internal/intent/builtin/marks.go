package builtin

// Mark is a mark a plugin writes that a workflow may read with handled().
type Mark struct {
	Name, Doc string
	Provider  string
}

// Marks are the merged plugins' marks a workflow may read; the core writes
// none.
var Marks []Mark
