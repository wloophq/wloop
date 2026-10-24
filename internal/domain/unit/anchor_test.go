package unit

import (
	"strings"
	"testing"
)

func TestAnchorValid(t *testing.T) {
	u := Unit{Provider: GitHub, Key: "o/r#1"}
	if err := (Anchor{Unit: u, Ref: "review:1"}).Valid(); err != nil {
		t.Errorf("valid anchor refused: %v", err)
	}
	for name, a := range map[string]Anchor{
		"blank ref": {Unit: u, Ref: "  "},
		"two lines": {Unit: u, Ref: "a\nb"},
		"too long":  {Unit: u, Ref: strings.Repeat("x", 301)},
		"no unit":   {Ref: "review:1"},
	} {
		if err := a.Valid(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
