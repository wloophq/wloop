package archtest

import (
	"errors"
	"testing"
)

func TestRTrustFlagsAPlantedRuleThatLoads(t *testing.T) {
	t.Parallel()
	plants := []trustPlant{
		{object: "loads", fact: "brief.kind"},
		{object: "refused elsewhere", fact: "pr.first.due", err: errors.New("unknown field pr.other")},
		{object: "refused", fact: "workspace.dirty", err: errors.New("reads workspace.dirty, which a rule that runs may not")},
	}
	flagged(t, "a rule that loads or is refused for another reason", ruleTrustedFacts(plants), 2, "(R-trust)")
}
