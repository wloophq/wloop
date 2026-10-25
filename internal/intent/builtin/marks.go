package builtin

import "github.com/wloophq/wloop/internal/domain/round"

// Mark is a mark a plugin writes that a workflow may read with handled().
type Mark struct {
	Name, Doc string
}

// Marks are the plugins' marks a workflow may read. reviewed is not one: its
// value is a fingerprint no fact has.
var Marks = []Mark{
	{round.AddressedReviews, "The newest review an address round triaged on a pull request you develop, a time: handled(\"addressed.reviews\", <time>)."},
	{round.AddressedChecks, "The head whose failing checks an address round covered: handled(\"addressed.checks\", pr.head)."},
	{round.AddressedConflict, "The head whose conflict with the base an address round covered: handled(\"addressed.conflict\", pr.head)."},
}
