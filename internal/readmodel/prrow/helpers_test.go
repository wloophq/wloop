package prrow

import (
	"testing"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/turn"
)

func bundled(t *testing.T) *intentload.Loaded {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func actionsOf(a *api.Attention) []string {
	var out []string
	for _, x := range a.Actions {
		s := x.ID + " " + x.Title
		if x.TextInput {
			s += " [text]"
		}
		out = append(out, s)
	}
	return out
}
