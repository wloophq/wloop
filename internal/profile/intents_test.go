package profile

import dintent "github.com/wloophq/wloop/internal/domain/intent"

const (
	Review  Kind = "review"
	Address Kind = "address"
	Nudge   Kind = "nudge"
)

// testIntents is the bundled intents as far as blocks see them.
var testIntents = dintent.NewRegistry([]dintent.Def{
	{Name: "review", Profile: "review", Dims: []string{"round"}},
	{Name: "address", Profile: "address", Dims: []string{"reviewer"}},
	{Name: "nudge", Profile: "nudge"},
	{Name: "probe"},
})
