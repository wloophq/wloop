package profile

import (
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	Review  Kind = "review"
	Address Kind = "address"
	Nudge   Kind = "nudge"
)

// testIntents is the bundled intents as far as blocks see them.
var testIntents = dintent.NewRegistry([]dintent.Def{
	{Name: "review", Profile: "review", Dims: []string{"round"}, Provider: unit.GitHub},
	{Name: "address", Profile: "address", Dims: []string{"reviewer"}, Provider: unit.GitHub},
	{Name: "nudge", Profile: "nudge", Provider: unit.GitHub},
	{Name: "probe", Provider: unit.GitHub},
})
