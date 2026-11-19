package inventory

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
)

func TestAStartBlockKeepsWhenItRetries(t *testing.T) {
	at := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	for name, b := range map[string]StartBlock{
		"a retry":            {Folder: "/w/app", Round: "first", RetryAt: at.Add(2 * time.Hour)},
		"a retry with skips": {Folder: "/w/app", Round: "first", RetryAt: at.Add(2 * time.Hour), Skip: map[string]string{"reviewed": "c1"}},
		"a question":         {Question: "trust", Root: "/w/app", Round: "first"},
	} {
		got, ok := StartBlockOf(port.Event{Kind: dinv.KindRoundBlocked, Key: b.Key(at)})
		if !ok || got.Question != b.Question || got.Folder != b.Folder || !got.RetryAt.Equal(b.RetryAt) || len(b.Skip) > 0 && got.Skip["reviewed"] != b.Skip["reviewed"] {
			t.Errorf("%s: read back %+v, %v; want %+v", name, got, ok, b)
		}
	}
}

func TestAStartBlockKeepsTheRoundAPersonAskedFor(t *testing.T) {
	at := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	asked := &AskedRound{Intent: "review", Guidance: "look at the migration\nand the tests", Inputs: map[string]any{"focus": "db"}}
	for name, b := range map[string]StartBlock{
		"a question":            {Question: "trust", Root: "/w/app", Homes: []string{"/h/a", "/h/b"}, Round: "review", Asked: asked},
		"a retry":               {Folder: "/w/app", Round: "review", RetryAt: at.Add(time.Hour), Asked: asked},
		"a question with skips": {Question: "trust", Root: "/w/app", Round: "review", Skip: map[string]string{"reviewed": "c1"}, Asked: asked},
	} {
		got, ok := StartBlockOf(port.Event{Kind: dinv.KindRoundBlocked, Key: b.Key(at)})
		if !ok || got.Asked == nil || got.Asked.Intent != "review" || got.Asked.Guidance != asked.Guidance || got.Asked.Inputs["focus"] != "db" ||
			!got.RetryAt.Equal(b.RetryAt) || got.Root != b.Root || len(got.Homes) != len(b.Homes) || got.Skip["reviewed"] != b.Skip["reviewed"] {
			t.Errorf("%s: read back %+v, %v; want %+v", name, got, ok, b)
		}
	}
}
