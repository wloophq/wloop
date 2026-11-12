package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/cli"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type headPublisher struct{ head string }

func (p headPublisher) Publish(context.Context, draft.Draft) (draft.Evidence, error) {
	return draft.Evidence{Ref: "1"}, nil
}

func (p headPublisher) Verify(context.Context, draft.Draft, time.Time) (draft.Evidence, bool, error) {
	return draft.Evidence{}, false, nil
}

func (p headPublisher) Live(context.Context, unit.Unit, string) (string, error) { return p.head, nil }

func TestServeForTestRunsATurnThroughTheComposedDaemon(t *testing.T) {
	t.Parallel()
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	d := cli.ServeForTest(t, cli.TestOptions{Publishers: map[unit.ProviderID]port.Effects{unit.GitHub: headPublisher{head: "c2"}}})
	s := d.Start(t, pr, "review")
	if b := d.Book(t, pr); len(b.Turns) != 1 || b.Turns[0].State != dturn.Started {
		t.Fatalf("turns %+v", b.Turns)
	}
	d.EndTurn(t, s, api.TurnEndRequest{Turn: s.Round, PR: "https://github.com/acme/api/pull/7",
		Record: json.RawMessage(`{"state":"settled","summary":"nothing new"}`)})
	if b := d.Book(t, pr); len(b.Turns) != 1 || b.Turns[0].State != dturn.Settled {
		t.Fatalf("turns %+v", b.Turns)
	}
	if err := d.Send(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
}
