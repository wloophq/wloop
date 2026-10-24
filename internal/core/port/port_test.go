package port_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestPROfOfANonPREvent(t *testing.T) {
	ref := pr.Ref{Owner: "o", Repo: "r", Number: 3}
	if got, ok := port.PROf(port.Event{Unit: ref.Unit()}); !ok || got != ref {
		t.Errorf("PROf(github) = %v %v", got, ok)
	}
	for _, u := range []unit.Unit{{}, {Provider: unit.Session, Key: "t1"}, {Provider: unit.GitHub, Key: "garbage"}} {
		if got, ok := port.PROf(port.Event{Unit: u}); ok || got != (pr.Ref{}) {
			t.Errorf("PROf(%v) = %v %v, want none", u, got, ok)
		}
	}
}
