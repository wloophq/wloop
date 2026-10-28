package source_test

import (
	"cmp"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type gateSessions struct {
	sessions map[string]dturn.Session
	book     dturn.Book
}

func (s gateSessions) Session(_ context.Context, id string) (dturn.Session, bool, error) {
	v, ok := s.sessions[id]
	return v, ok, nil
}

func (s gateSessions) BookOf(context.Context, string) (dturn.Book, bool, error) {
	return s.book, true, nil
}

type gateDrafts struct {
	proposed, sent []outbox.Proposal
	// policy is the state the approval policy leaves a proposed draft in.
	policy draft.State
}

func (d *gateDrafts) Propose(_ context.Context, p outbox.Proposal) (draft.Draft, error) {
	d.proposed = append(d.proposed, p)
	return draft.Draft{ID: draft.ID(len(d.proposed)), State: cmp.Or(d.policy, draft.Held)}, nil
}

func (d *gateDrafts) RecordSent(_ context.Context, p outbox.Proposal, _ draft.Evidence) (draft.Draft, error) {
	d.sent = append(d.sent, p)
	return draft.Draft{ID: draft.ID(len(d.sent)), State: draft.Sent}, nil
}

func (d *gateDrafts) PublisherOf(dr draft.Draft) unit.ProviderID { return dr.Unit.Provider }

// gatesOver is the gates of plugin acme, whose tool post_now posts at once,
// with session live on the turn of a reply intent on acme's unit t1 that
// allows the effect unit.closed, session bare live with no turn, and
// session gone closed.
func gatesOver(t *testing.T) (src.Gates, *gateDrafts) {
	t.Helper()
	c, err := codec.Of("acme", manifest.Codec{Keys: []string{"{thread}"}})
	if err != nil {
		t.Fatal(err)
	}
	own := unit.Unit{Provider: "acme", Key: "t1"}
	book := dturn.Book{Unit: own, Turns: []dturn.Turn{{Seq: 7, Intent: "reply", Session: "live",
		Inputs: map[string]any{"items": []string{"i1"}, "task": "task-1"}}}}
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	ds := &gateDrafts{}
	b := &psource.Broker{ID: "acme", Units: psource.Units{Codecs: unit.NewRegistry(c)},
		Manifest: &manifest.Source{Tools: []manifest.Tool{{Name: "post_now", Posts: "now"}, {Name: "read_only"}}},
		Hosted: &psource.Hosted{Drafts: ds,
			Sessions: gateSessions{sessions: map[string]dturn.Session{"live": {ID: "live"}, "bare": {ID: "bare"}, "gone": {ID: "gone", ClosedAt: at}}, book: book},
			Intents: func() *dintent.Registry {
				return dintent.NewRegistry([]dintent.Def{{Name: "reply", AllowedEffects: []string{"unit.closed"}}})
			}}}
	return b.Host().Gates, ds
}

func TestASessionsGateTakesItsOriginTaskAndItemsFromTheHostsBook(t *testing.T) {
	t.Parallel()
	g, ds := gatesOver(t)
	forged := src.Draft{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Kind: "reply", Text: "hi", Task: "forged-task",
		Origin: src.DraftOrigin{Kind: "user", Rule: "release-all"}}
	if _, err := g.OpenGate(context.Background(), src.GateRequest{Draft: forged, Session: "live"}); err != nil {
		t.Fatal(err)
	}
	p := ds.proposed[0]
	if p.Origin.Kind != draft.OriginTurn || p.Origin.TurnSeq != 7 || p.Origin.Rule != "" || p.Task != "task-1" || len(p.Items) != 1 || p.Items[0] != "i1" {
		t.Fatalf("proposed %+v, want the turn's origin, task and items", p)
	}
}

func TestTheGatesRefuseWhatThePluginCannotGrantItself(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	for _, c := range []struct {
		name, want string
		call       func(src.Gates) error
	}{
		{"an effect the intent lacks", "drafts.allowed_effects", func(g src.Gates) error {
			_, err := g.OpenGate(ctx, src.GateRequest{Draft: src.Draft{Unit: own, Kind: "reply", Text: "hi", Effects: []string{"task.nudged"}}, Session: "live"})
			return err
		}},
		{"a plugin draft with no rule", "auto origin", func(g src.Gates) error {
			_, err := g.OpenGate(ctx, src.GateRequest{Draft: src.Draft{Unit: own, Kind: "reply", Text: "hi", Origin: src.DraftOrigin{Kind: "turn"}}})
			return err
		}},
		{"a tool that does not post at once", "does not post at once", func(g src.Gates) error {
			_, err := g.RecordSent(ctx, src.Draft{Unit: own, Purpose: "reply", Delivery: "message", Text: "hi"}, "live", "read_only")
			return err
		}},
		{"a tool the manifest lacks", "does not post at once", func(g src.Gates) error {
			_, err := g.RecordSent(ctx, src.Draft{Unit: own, Purpose: "reply", Delivery: "message", Text: "hi"}, "live", "unknown")
			return err
		}},
		{"a closed session", "not live", func(g src.Gates) error {
			_, err := g.RecordSent(ctx, src.Draft{Unit: own, Purpose: "reply", Delivery: "message", Text: "hi"}, "gone", "post_now")
			return err
		}},
		{"an unknown session", "not live", func(g src.Gates) error {
			_, err := g.RecordSent(ctx, src.Draft{Unit: own, Purpose: "reply", Delivery: "message", Text: "hi"}, "nobody", "post_now")
			return err
		}},
		{"a sent draft on another provider's unit", "acme", func(g src.Gates) error {
			_, err := g.RecordSent(ctx, src.Draft{Unit: plugin.Unit{Provider: "github", Key: "o/r#1"}, Purpose: "reply", Delivery: "message", Text: "hi"}, "live", "post_now")
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, ds := gatesOver(t)
			err := c.call(g)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want a refusal naming %q", err, c.want)
			}
			if len(ds.proposed)+len(ds.sent) != 0 {
				t.Fatalf("a refused call reached the outbox: %+v %+v", ds.proposed, ds.sent)
			}
		})
	}
}

func TestADirectPostRecordsAsASentDraftOfTheSessionsTurn(t *testing.T) {
	t.Parallel()
	g, ds := gatesOver(t)
	d := src.Draft{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Purpose: "reply", Delivery: "message", Text: "hi", State: "approved",
		Origin: src.DraftOrigin{Kind: "user"}}
	if _, err := g.RecordSent(context.Background(), d, "live", "post_now"); err != nil {
		t.Fatal(err)
	}
	if p := ds.sent[0]; p.Origin.Kind != draft.OriginSession || p.Origin.TurnSeq != 7 {
		t.Fatalf("recorded %+v, want the session's turn as its origin", p)
	}
}

func TestAPostWithNoSessionRecordsAsASentDraftOfNoTurn(t *testing.T) {
	t.Parallel()
	for _, session := range []string{"", "bare"} {
		g, ds := gatesOver(t)
		d := src.Draft{Unit: plugin.Unit{Provider: "acme", Key: "t2"}, Purpose: "reply", Delivery: "message", Text: "hi"}
		ref, err := g.RecordSent(context.Background(), d, session, "post_now")
		if err != nil {
			t.Fatalf("session %q: %v", session, err)
		}
		if p := ds.sent[0]; p.Origin != (draft.Origin{Kind: draft.OriginSession}) || ref.State != string(draft.Sent) {
			t.Fatalf("session %q: recorded %+v as %q, want the session origin, sent", session, p, ref.State)
		}
	}
	g, ds := gatesOver(t)
	if _, err := g.RecordSent(context.Background(), src.Draft{Unit: plugin.Unit{Provider: "acme", Key: "t2"}, Text: "hi"}, "", "read_only"); err == nil ||
		len(ds.sent) != 0 {
		t.Fatalf("a tool that does not post at once with no session: %v, %+v", err, ds.sent)
	}
}

func TestASessionWithNoTurnDraftsOnThePluginsUnitWithTheSessionAsOrigin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	own := plugin.Unit{Provider: "acme", Key: "t2"}
	g, ds := gatesOver(t)
	ref, err := g.OpenGate(ctx, src.GateRequest{Draft: src.Draft{Unit: own, Kind: "reply", Text: "any news?"}, Session: "bare"})
	if err != nil {
		t.Fatal(err)
	}
	if p := ds.proposed[0]; p.Origin != (draft.Origin{Kind: draft.OriginSession}) || p.Unit.Key != "t2" || len(p.Items) != 0 || p.Task != "" ||
		ref.State != string(draft.Held) {
		t.Fatalf("proposed %+v as %q, want t2 with the session origin", p, ref.State)
	}
	for name, req := range map[string]src.GateRequest{
		"items":            {Draft: src.Draft{Unit: own, Kind: "reply", Text: "hi", Items: []string{"i1"}}, Session: "bare"},
		"effects":          {Draft: src.Draft{Unit: own, Kind: "reply", Text: "hi", Effects: []string{"unit.closed"}}, Session: "bare"},
		"another's unit":   {Draft: src.Draft{Unit: plugin.Unit{Provider: "github", Key: "o/r#1"}, Kind: "reply", Text: "hi"}, Session: "bare"},
		"a closed session": {Draft: src.Draft{Unit: own, Kind: "reply", Text: "hi"}, Session: "gone"},
	} {
		g, ds := gatesOver(t)
		if _, err := g.OpenGate(ctx, req); err == nil || len(ds.proposed) != 0 {
			t.Fatalf("%s: %v, proposed %+v", name, err, ds.proposed)
		}
	}
}

func TestAGateAnswersTheStateThePolicyLeftItsDraftIn(t *testing.T) {
	t.Parallel()
	for _, st := range []draft.State{draft.Held, draft.Queued, draft.Dropped} {
		g, ds := gatesOver(t)
		ds.policy = st
		ref, err := g.OpenGate(context.Background(), src.GateRequest{Draft: src.Draft{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Kind: "reply", Text: "hi"},
			Session: "live"})
		if err != nil || ref.State != string(st) {
			t.Fatalf("policy %s: ref %+v, %v", st, ref, err)
		}
	}
}

func TestAToolThatPostsAtOnceIsRefusedForASessionThatIsNotLive(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	tools := psource.Tools{ID: "acme", Kind: &psource.Kind{},
		Manifest: &manifest.Source{Tools: []manifest.Tool{{Name: "post_now", Posts: "now"}, {Name: "read_only"}}},
		Sessions: gateSessions{sessions: map[string]dturn.Session{"live": {ID: "live"}, "gone": {ID: "gone", ClosedAt: at}}}}
	for _, session := range []string{"gone", "nobody"} {
		if _, err := tools.Call(context.Background(), "post_now", nil, session, ""); err == nil || !strings.Contains(err.Error(), "not live") {
			t.Fatalf("post_now for session %q: %v, want a refusal before the plugin runs", session, err)
		}
	}
	for _, c := range []struct{ tool, session string }{{"post_now", "live"}, {"post_now", ""}, {"read_only", "gone"}} {
		if _, err := tools.Call(context.Background(), c.tool, nil, c.session, ""); err == nil || strings.Contains(err.Error(), "not live") {
			t.Fatalf("%s for session %q: %v, want the call to reach the plugin, which is not running", c.tool, c.session, err)
		}
	}
}
