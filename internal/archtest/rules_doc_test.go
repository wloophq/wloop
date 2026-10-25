package archtest

import (
	"fmt"
	"testing"

	"github.com/wloophq/wloop/internal/archtest/rulesdoc"
)

// ruleDocs flags a rule test whose doc comment does not state its rule.
func ruleDocs(rules []rulesdoc.Rule) []string {
	var out []string
	for _, r := range rules {
		if r.Rule == "" {
			out = append(out, fmt.Sprintf("%s (%s) has no doc comment of the form %q; a rule test states its rule, which the architecture rules page shows", r.Test, r.File, "// "+r.Test+" checks that <rule>."))
		}
	}
	return out
}

// TestEveryRuleTestStatesItsRule checks that every rule test, a test that
// does not call flagged, states its rule in a doc comment.
func TestEveryRuleTestStatesItsRule(t *testing.T) {
	t.Parallel()
	rules, err := rulesdoc.Read(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range ruleDocs(rules) {
		t.Error(b)
	}
}
