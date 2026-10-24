package slack

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	coreplacement "github.com/wloophq/wloop/internal/core/placement"
	"github.com/wloophq/wloop/internal/domain/placement"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type overrides map[unit.Unit]placement.Placement

func (o overrides) Override(_ context.Context, u unit.Unit) (placement.Placement, bool, error) {
	p, ok := o[u]
	return p, ok, nil
}

func (o overrides) SetOverride(_ context.Context, u unit.Unit, p placement.Placement) error {
	o[u] = p
	return nil
}

func (f *fixture) hint(t *testing.T, u unit.Unit, hints map[string]sqlite.ExtractionHint) {
	t.Helper()
	if err := f.db.Extraction().Save(context.Background(), sqlite.ExtractionSave{Hints: map[unit.Unit]map[string]sqlite.ExtractionHint{u: hints}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaceHintOrder(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seedReadThread(t)
	u := dslack.ThreadUnit(team, readConv, readParent)
	f.hint(t, u, map[string]sqlite.ExtractionHint{"e1": {Mode: "session", Context: "github:acme/web*", Why: "the web app"}})
	byID := config.PlacementRule{Match: "slack:" + u.Key, Repo: "acme/byid", Checkout: "main"}
	hinted := config.PlacementRule{Match: "github:acme/web*", Repo: "acme/web", Role: "work"}
	channel := config.PlacementRule{Match: "slack:#gen*", Repo: "acme/channel", Role: "work"}
	over := overrides{u: {Repo: "acme/override", Role: "author"}}

	place := func(contexts ...config.PlacementRule) (placement.Placement, error) {
		cfg := f.cfg
		cfg.Placement = contexts
		pl := f.p.places(cfg)
		s := &coreplacement.Service{Map: cfg.PlacementMap, Overrides: over,
			Hints: map[unit.ProviderID]placement.Hints{dslack.Provider: pl}, Defaults: map[unit.ProviderID]placement.Defaults{dslack.Provider: pl}}
		return s.Place(context.Background(), u, "")
	}
	for _, step := range []struct {
		name     string
		contexts []config.PlacementRule
		drop     func()
		repo     string
		source   string
		shared   bool
	}{
		{"override", []config.PlacementRule{hinted, byID, channel}, func() { delete(over, u) }, "acme/override", placement.SourceOverride, false},
		{"context by id", []config.PlacementRule{hinted, byID, channel}, nil, "acme/byid", placement.SourceContext, true},
		{"extraction hint", []config.PlacementRule{hinted, channel}, nil, "acme/web", placement.SourceSuggested, false},
		{"channel rule", []config.PlacementRule{channel}, nil, "acme/channel", placement.SourceProvider, false},
	} {
		got, err := place(step.contexts...)
		if err != nil || got.Repo != step.repo || got.Source != step.source || got.Shared != step.shared {
			t.Fatalf("%s: %+v %v", step.name, got, err)
		}
		if step.drop != nil {
			step.drop()
		}
	}
	if got, err := place(); !errors.Is(err, coreplacement.ErrUnplaced) {
		t.Fatalf("no rule: %+v %v", got, err)
	}
}

func TestPlaceHintByItem(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seedReadThread(t)
	u := dslack.ThreadUnit(team, readConv, readParent)
	f.hint(t, u, map[string]sqlite.ExtractionHint{
		"e1": {Mode: "session", Context: "slack:#api"}, "e2": {Mode: "session", Context: "slack:#web"}, "e3": {Mode: "none"}})
	cfg := f.cfg
	cfg.Placement = []config.PlacementRule{{Match: "slack:#api", Repo: "acme/api"}, {Match: "slack:#web", Repo: "acme/web"}}
	pl := f.p.places(cfg)
	ctx := context.Background()
	for item, repo := range map[string]string{"e1": "acme/api", "e2": "acme/web"} {
		if got, ok, err := pl.Suggested(ctx, u, item); err != nil || !ok || got.Repo != repo {
			t.Errorf("%s: %+v %t %v", item, got, ok, err)
		}
	}
	for _, item := range []string{"", "e3", "e9"} {
		if got, ok, err := pl.Suggested(ctx, u, item); err != nil || ok {
			t.Errorf("%q: %+v %t %v", item, got, ok, err)
		}
	}
	cfg.Placement = cfg.Placement[1:]
	if got, ok, err := f.p.places(cfg).Suggested(ctx, u, "e1"); err != nil || ok {
		t.Errorf("a context the config dropped: %+v %t %v", got, ok, err)
	}
}

func TestPlaceDefaultSkipsDirectMessages(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	const dm = "D0000000001"
	f.seed(t, dslack.Facts{Conversations: []dslack.Conversation{{ID: dm, Kind: dslack.KindIM, DMUser: other}}})
	cfg := f.cfg
	cfg.Placement = []config.PlacementRule{{Match: "slack:#*", Repo: "acme/any"}}
	pl := f.p.places(cfg)
	ctx := context.Background()
	for _, u := range []unit.Unit{
		dslack.Key{Team: team, Conversation: dm, Segment: "s1"}.Unit(),
		dslack.ThreadUnit(team, "C0000000009", readParent),
	} {
		if got, ok, err := pl.Default(ctx, u, ""); err != nil || ok {
			t.Errorf("%s: %+v %t %v", u, got, ok, err)
		}
	}
}

func TestPlaceOverridesStoredInTheInventory(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seedReadThread(t)
	u := dslack.ThreadUnit(team, readConv, readParent)
	st := inventory.New(f.db)
	if err := (inventory.Placements{Store: st}).SetOverride(context.Background(), u, placement.Placement{Repo: "acme/pinned"}); err != nil {
		t.Fatal(err)
	}
	cfg := f.cfg
	cfg.Placement = []config.PlacementRule{{Match: "slack:#*", Repo: "acme/any"}}
	pl := f.p.places(cfg)
	s := &coreplacement.Service{Map: cfg.PlacementMap, Overrides: inventory.Placements{Store: st},
		Hints: map[unit.ProviderID]placement.Hints{dslack.Provider: pl}, Defaults: map[unit.ProviderID]placement.Defaults{dslack.Provider: pl}}
	if got, err := s.Place(context.Background(), u, ""); err != nil || got.Repo != "acme/pinned" {
		t.Fatalf("%+v %v", got, err)
	}
}
