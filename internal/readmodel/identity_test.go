package readmodel_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	psource "github.com/wloophq/wloop/internal/plugins/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/pkg/plugin/source/sourcetest"
)

// presenting is a source that says who the viewer is.
type presenting struct {
	*sourcetest.Stub
	id src.Identity
}

func (p presenting) Action(context.Context, src.ActionRequest) (json.RawMessage, error) {
	return nil, nil
}
func (p presenting) View(context.Context, src.ViewRequest) (json.RawMessage, error) { return nil, nil }
func (p presenting) Identity(context.Context) (src.Identity, error)                 { return p.id, nil }

// teamRows derives each unit's row from the viewer and the teams its provider's base holds.
type teamRows struct{ units []unit.Unit }

func (p teamRows) Provider() unit.ProviderID                   { return "acme" }
func (p teamRows) Listed(context.Context) ([]unit.Unit, error) { return p.units, nil }
func (p teamRows) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	out := map[unit.Unit]any{}
	for _, u := range units {
		out[u] = u
	}
	return out, nil
}

func (p teamRows) Derive(b readmodel.Base, _ any, _ time.Time) (api.Row, error) {
	return api.Row{Facets: map[string][]string{"viewer": {b.Viewers["acme"]}, "teams": b.Teams["acme"]}}, nil
}

type identified struct {
	id func(ctx context.Context) (src.Identity, error)
}

func (s identified) Settings(ctx context.Context) (readmodel.Base, error) {
	id, err := s.id(ctx)
	if err != nil {
		return readmodel.Base{}, err
	}
	var b readmodel.Base
	b.Identify("acme", id)
	return b, nil
}

func TestTeamsComeFromIdentity(t *testing.T) {
	broker := &psource.Broker{ID: "acme", Config: func() config.Config { return config.Config{} }}
	client := sourcetest.Launch(t, "acme", presenting{Stub: sourcetest.NewStub(),
		id: src.Identity{Viewer: src.Person{Key: "acme:ana", Name: "Ana"}, Teams: []string{"acme/core", "acme/web"}}}, broker.Host())
	one := unit.Unit{Provider: "acme", Key: "one"}
	m := readmodel.New(nil, nil, nil, nil, identified{client.Identity}, teamRows{units: []unit.Unit{one}})
	page, err := m.Rows(context.Background(), api.ViewAll, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("rows %+v", page.Rows)
	}
	f := page.Rows[0].Facets
	if !slices.Equal(f["teams"], []string{"acme/core", "acme/web"}) || strings.Join(f["viewer"], ",") != "ana" {
		t.Errorf("facets %v, want the teams and the login the plugin's Identity says", f)
	}

	var b readmodel.Base
	b.Identify("acme", src.Identity{Viewer: src.Person{Key: "group:acme/core"}})
	if b.Viewers["acme"] != "" || b.Teams["acme"] != nil {
		t.Errorf("a viewer of another key's shape names no login: %+v", b)
	}
}
