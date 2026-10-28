package builtin

import (
	"strings"
	"testing"
)

// TestNoCoreToolIsAPlugins checks that the core's tools and answers name
// no plugin's tool: a plugin's come from its manifest through Merge.
func TestNoCoreToolIsAPlugins(t *testing.T) {
	for _, v := range coreTools {
		if strings.HasPrefix(v.Name, "slack_") {
			t.Errorf("core tool %s", v.Name)
		}
	}
	for _, a := range coreToolAnswers {
		if strings.HasPrefix(a.Tool, "slack_") {
			t.Errorf("core answer %s.%s", a.Tool, a.Name)
		}
	}
}
