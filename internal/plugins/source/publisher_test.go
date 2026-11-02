package source_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/codec"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// posting is a plugin whose Publish fails as the draft's text says, and
// whose Proposals are its proposals.
type posting struct {
	*sourcetest.Stub
	started   chan struct{}
	proposals []src.Draft
}

func (p posting) Validate(context.Context, src.Draft) error        { return nil }
func (p posting) Where(context.Context, src.Draft) (string, error) { return "", nil }
func (p posting) Place(context.Context, src.Draft) (string, error) { return "", nil }
func (p posting) Verify(context.Context, src.Draft, time.Time) (src.Evidence, bool, error) {
	return src.Evidence{}, false, nil
}

func (p posting) Publish(ctx context.Context, d src.Draft) (src.Evidence, error) {
	switch d.Text {
	case "posted":
		return src.Evidence{}, fmt.Errorf("%w on t1: HTTP 502", src.ErrMayHavePosted)
	case "refused":
		return src.Evidence{}, errors.New("commenting on t1: HTTP 422")
	}
	close(p.started)
	<-ctx.Done()
	return src.Evidence{}, ctx.Err()
}

func (p posting) Plan(context.Context, plugin.Unit, src.RoundState) (src.Plan, error) {
	return src.Plan{}, src.ErrUnsupported
}

func (p posting) Payload(context.Context, src.PayloadRequest) (src.PayloadResult, error) {
	return src.PayloadResult{}, src.ErrUnsupported
}

func (p posting) ApplyEffect(context.Context, src.EffectRequest) error { return src.ErrUnsupported }

func (p posting) Proposals(context.Context, time.Time) ([]src.Draft, error) { return p.proposals, nil }

func (p posting) Placement(context.Context, plugin.Unit, string) (src.Placements, error) {
	return src.Placements{}, src.ErrUnsupported
}

const commentingManifest = `default_delivery = "comment"
[[source.deliveries]]
name = "comment"
posts = true
[[source.deliveries]]
name = "reaction"
[[source.draft_kinds]]
name = "react"
delivery = "reaction"
`

// postingKind runs p as plugin acme, which posts comments, and answers its
// publisher.
func postingKind(t *testing.T, p posting) (psource.Publisher, psource.Units) {
	t.Helper()
	k := kindOver(t, &change.Log{})
	start(t, k, "acme", commentingManifest, p)
	m, err := manifest.Parse([]byte("id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{name}\"]}\n" + commentingManifest))
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	c, err := codec.Of("acme", s.Codec)
	if err != nil {
		t.Fatal(err)
	}
	units := psource.Units{Codecs: unit.NewRegistry(c)}
	return psource.Publisher{ID: "acme", Source: s, Units: units, Kind: k}, units
}

func TestAPublishThatMayHavePostedAsksAConfirmBeforeARetry(t *testing.T) {
	t.Parallel()
	p := posting{Stub: sourcetest.NewStub(), started: make(chan struct{})}
	pub, _ := postingKind(t, p)
	own := unit.Unit{Provider: "acme", Key: "t1"}
	ctx := context.Background()
	for text, may := range map[string]bool{"posted": true, "refused": false} {
		_, err := pub.Publish(ctx, draft.Draft{Unit: own, Text: text})
		if err == nil || strings.HasPrefix(err.Error(), "may have been posted") != may {
			t.Fatalf("%s: %v", text, err)
		}
	}
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := pub.Publish(hctx, draft.Draft{Unit: own, Text: "hang"})
		done <- err
	}()
	<-p.started
	cancel()
	if err := <-done; err == nil || !strings.HasPrefix(err.Error(), "may have been posted") {
		t.Fatalf("a Publish the plugin never answered: %v", err)
	}
}

func TestAPluginsProposalsAreItsOwnRulesWithNoDeliveryOfTheirOwn(t *testing.T) {
	t.Parallel()
	own := plugin.Unit{Provider: "acme", Key: "t1"}
	rule := src.DraftOrigin{Kind: "auto", Rule: "acme-nudge", Key: "k"}
	p := posting{Stub: sourcetest.NewStub(), started: make(chan struct{}), proposals: []src.Draft{
		{Unit: own, Purpose: "nudge", Delivery: "reaction", Text: "any news?", Origin: rule},
		{Unit: plugin.Unit{Provider: "slack", Key: "C1/1.2"}, Purpose: "nudge", Text: "elsewhere", Origin: rule},
		{Unit: own, Purpose: "nudge", Text: "as a session", Origin: src.DraftOrigin{Kind: "session"}},
		{Unit: own, Purpose: "nudge", Text: "no rule", Origin: src.DraftOrigin{Kind: "auto"}},
		{Unit: own, Purpose: "nudge", Text: "bad task", Task: "not a task", Origin: rule},
	}}
	pub, units := postingKind(t, p)
	got, err := psource.Proposals{ID: "acme", Units: units, Kind: pub.Kind}.Proposals(context.Background(), time.Time{})
	if err == nil || len(got) != 1 {
		t.Fatalf("proposals %+v, err %v: want the one draft of the plugin's own rule, and the rest refused", got, err)
	}
	for _, want := range []string{"slack", "auto origin", "invalid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusals %q name no %q", err, want)
		}
	}
	prop := got[0]
	if prop.Delivery != "" || prop.Purpose != "nudge" || prop.Origin != (draft.Origin{Kind: draft.OriginAuto, Rule: "acme-nudge", Key: "k"}) {
		t.Fatalf("proposal %+v", prop)
	}

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	gs := outboxtest.NewGates(func() time.Time { return now })
	svc := gs.Bind(&outbox.Service{Store: db.Drafts(), Clock: pinned(now), Effects: func(unit.ProviderID) (port.Effects, error) { return pub, nil }})
	d, err := svc.Propose(context.Background(), prop)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != draft.Held || d.Delivery != draft.CommentDelivery {
		t.Fatalf("proposed %+v, want a held comment", d)
	}
}

type pinned time.Time

func (c pinned) Now() time.Time { return time.Time(c) }
