package prrow

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel"
)

var extraIntents = dintent.NewRegistry([]dintent.Def{
	{Name: "review", Profile: "review"},
	{Name: "brief", Builds: "brief", Provider: unit.GitHub, Extra: []jsonschema.Property{
		{Name: "solution", Type: "string"},
		{Name: "approach", Type: "string", Enum: []string{"incremental", "rewrite"}},
		{Name: "areas", Type: "array", Enum: []string{"api", "ui"}},
	}},
})

func TestAnExtraEnumIsAFacet(t *testing.T) {
	loaded := &intentload.Loaded{Registry: extraIntents}
	p := New(Source{RowDeps: readmodel.RowDeps{Settings: func() readmodel.RowSettings { return readmodel.RowSettings{Loaded: loaded} }}})
	specs := p.Facets()
	if len(specs) != len(FacetSpecs())+2 {
		t.Fatalf("%d facets", len(specs))
	}
	approach, areas := specs[len(specs)-2], specs[len(specs)-1]
	if approach.ID != "extra.approach" || !slices.Equal(approach.Rank, []string{"incremental", "rewrite"}) || !approach.Group || approach.Provider != string(unit.GitHub) ||
		areas.ID != "extra.areas" || areas.Group {
		t.Fatalf("%+v %+v", approach, areas)
	}
	d := Deriver{Loaded: loaded}
	got := d.facetValues(api.Row{}, Payload{Brief: &api.BriefSummary{Extra: map[string]any{
		"approach": "rewrite", "areas": []any{"ui", "elsewhere"}, "solution": "split the cache"}}}, nil)
	if !slices.Equal(got["extra.approach"], []string{"rewrite"}) || !slices.Equal(got["extra.areas"], []string{"ui"}) || got["extra.solution"] != nil {
		t.Fatalf("%v", got)
	}
	if got := d.facetValues(api.Row{}, Payload{}, nil); got["extra.approach"] != nil {
		t.Fatalf("without a brief: %v", got)
	}
}
