package source_test

import (
	"cmp"
	"context"
	"fmt"
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
	proposed []outbox.Proposal
	// policy is the state the approval policy leaves a proposed draft in.
	policy draft.State
}

func (d *gateDrafts) Propose(_ context.Context, p outbox.Proposal) (draft.Draft, error) {
	d.proposed = append(d.proposed, p)
	return draft.Draft{ID: draft.ID(len(d.proposed)), State: cmp.Or(d.policy, draft.Held), Rev: 3}, nil
}

func (d *gateDrafts) PublisherOf(dr draft.Draft) unit.ProviderID {
	if strings.HasPrefix(dr.Target, "#") {
		return "acme"
	}
	return dr.Unit.Provider
}

// gatesOver is the gates of plugin acme, whose tool write writes drafts,
// with session live on the turn of a reply intent on acme's unit t1 that
// allows the effect unit.closed, session bare live with no turn, and
// session gone closed.
func gatesOver(t *testing.T) (src.Gates, *gateDrafts) {
	t.Helper()
	return gatesPosting(t, nil)
}

// gatesPosting is gatesOver with po as the host's poster.
func gatesPosting(t *testing.T, po psource.Poster) (src.Gates, *gateDrafts) {
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
		Manifest: &manifest.Source{Tools: []manifest.Tool{{Name: "write", Writes: true}, {Name: "read_only"}}},
		Hosted: &psource.Hosted{Drafts: ds, Poster: po,
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
	if p.Origin.Kind != draft.OriginTurn || p.Origin.TurnSeq != 7 || p.Origin.Session != "live" || p.Origin.Rule != "" || p.Task != "task-1" || len(p.Items) != 1 || p.Items[0] != "i1" {
		t.Fatalf("proposed %+v, want the turn's origin and session, task and items", p)
	}
}

func TestASessionsGateCarriesTheDraftsWhyPayloadBasisAndHold(t *testing.T) {
	t.Parallel()
	g, ds := gatesOver(t)
	d := src.Draft{Unit: plugin.Unit{Provider: "acme", Key: "t1"}, Kind: "reply", Text: "hi", Why: "the loop never backs off",
		Payload: []byte(`{"event":"COMMENT"}`), Basis: "h1", Hold: true}
	if _, err := g.OpenGate(context.Background(), src.GateRequest{Draft: d, Session: "live"}); err != nil {
		t.Fatal(err)
	}
	if p := ds.proposed[0]; p.Why != d.Why || string(p.Payload) != string(d.Payload) || p.Basis != "h1" || !p.Hold {
		t.Fatalf("proposed %+v", p)
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
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			g, ds := gatesOver(t)
			err := c.call(g)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want a refusal naming %q", err, c.want)
			}
			if len(ds.proposed) != 0 {
				t.Fatalf("a refused call reached the outbox: %+v", ds.proposed)
			}
		})
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
	if p := ds.proposed[0]; p.Origin != (draft.Origin{Kind: draft.OriginSession, Session: "bare"}) || p.Unit.Key != "t2" || len(p.Items) != 0 || p.Task != "" ||
		ref.State != string(draft.Held) || ref.Rev != 3 {
		t.Fatalf("proposed %+v as %q at rev %d, want t2 with the session origin at the rev the outbox stored", p, ref.State, ref.Rev)
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

func TestAPluginsDraftCarriesNoDeliveryOfItsOwn(t *testing.T) {
	t.Parallel()
	g, ds := gatesOver(t)
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	for _, d := range []src.Draft{
		{Unit: own, Purpose: "nudge", Delivery: "reaction", Text: "any news?", Origin: src.DraftOrigin{Kind: "auto", Rule: "acme-nudge", Key: "k"}},
		{Unit: own, Kind: "reply", Delivery: "reaction", Text: "hi"},
	} {
		session := ""
		if d.Kind != "" {
			session = "live"
		}
		if _, err := g.OpenGate(context.Background(), src.GateRequest{Draft: d, Session: session}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range ds.proposed {
		if p.Delivery != "" {
			t.Errorf("proposed %+v with the plugin's delivery", p)
		}
	}
	if ds.proposed[0].Purpose != "nudge" || ds.proposed[1].Kind != draft.Kind("reply") {
		t.Fatalf("proposed %+v", ds.proposed)
	}
}

// poster posts every draft through a session it drives and refuses the others, as the daemon's does.
type poster struct {
	driven map[string]bool
	posted []draft.ID
}

func (p *poster) PostDraft(_ context.Context, session string, id draft.ID, rev int64) (draft.Draft, bool, error) {
	if !p.driven[session] {
		return draft.Draft{ID: id, Rev: rev, State: draft.Held}, false, nil
	}
	p.posted = append(p.posted, id)
	return draft.Draft{ID: id, Rev: rev, State: draft.Queued}, true, nil
}

func (p *poster) DraftLink(id draft.ID) string {
	return fmt.Sprintf("http://127.0.0.1:1/#task=draft%%3A%d", id)
}

// A write with Post goes through the host's poster, which posts it from a session the user drives and keeps it a
// draft from any other; either answer carries the draft's link. Without Post nothing is posted.
func TestAWriteWithPostGoesThroughTheHostsPoster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	for _, c := range []struct {
		post   bool
		driven bool
		want   draft.State
	}{{true, true, draft.Queued}, {true, false, draft.Held}, {false, true, draft.Held}} {
		po := &poster{driven: map[string]bool{"live": c.driven}}
		g, _ := gatesPosting(t, po)
		ref, err := g.OpenGate(ctx, src.GateRequest{Draft: src.Draft{Unit: own, Kind: "reply", Text: "hi"}, Session: "live", Post: c.post})
		if err != nil || ref.State != string(c.want) || ref.Link != "http://127.0.0.1:1/#task=draft%3A1" {
			t.Fatalf("post %v, driven %v: %+v %v, want %s with its link", c.post, c.driven, ref, err, c.want)
		}
		if posted := len(po.posted) == 1; posted != (c.post && c.driven) {
			t.Fatalf("post %v, driven %v: posted %v", c.post, c.driven, po.posted)
		}
	}
}

// A write from a session with no turn that names no unit is on the session's own unit, posted to its target.
func TestAWriteThatNamesNoUnitIsOnTheSessionsUnit(t *testing.T) {
	t.Parallel()
	g, ds := gatesOver(t)
	if _, err := g.OpenGate(context.Background(), src.GateRequest{Draft: src.Draft{Kind: "reply", Text: "hi", Target: "#general"}, Session: "bare"}); err != nil {
		t.Fatal(err)
	}
	if p := ds.proposed[0]; p.Unit != dturn.SessionUnit("bare") || p.Target != "#general" || p.Origin.Kind != draft.OriginSession {
		t.Fatalf("proposed %+v, want the session's unit and its target", p)
	}
	g, ds = gatesOver(t)
	if _, err := g.OpenGate(context.Background(), src.GateRequest{Draft: src.Draft{Kind: "reply", Text: "hi", Target: "#general"}, Session: "live"}); err == nil || len(ds.proposed) != 0 {
		t.Fatalf("a session with a turn naming no unit: %v, %+v", err, ds.proposed)
	}
}
