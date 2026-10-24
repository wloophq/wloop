package readmodel_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/turn"
)

type intentSettings struct{ base readmodel.Base }

func (s intentSettings) Settings(context.Context) (readmodel.Base, error) { return s.base, nil }

func TestRowsOfAnyProviderOfferTheIntentsServingIt(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	open, closed := unit.Unit{Provider: "thread", Key: "C1/1"}, unit.Unit{Provider: "thread", Key: "C1/2"}
	p := &summaries{sums: map[unit.Unit]readmodel.Summary{
		open:   {WaitsOn: "me", Since: at, OpenItems: 1},
		closed: {WaitsOn: "nobody", Since: at, Closed: true},
	}}
	base := readmodel.Base{Intents: l, SpecsVersion: catalog.VersionOf(l)}
	m := readmodel.New(nil, nil, nil, nil, intentSettings{base}, p)
	m.UseClock(func() time.Time { return at })
	ctx := context.Background()
	page, err := m.Rows(ctx, api.ViewAll, api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	byUnit := map[unit.Unit]api.Row{}
	for _, r := range page.Rows {
		byUnit[r.Unit] = r
	}
	act := func(r api.Row) (api.ActionState, bool) {
		i := slices.IndexFunc(r.Actions, func(st api.ActionState) bool { return st.ID == "act" })
		if i < 0 {
			return api.ActionState{}, false
		}
		return r.Actions[i], true
	}
	if st, ok := act(byUnit[open]); !ok || !st.Enabled || byUnit[open].SpecsVersion != base.SpecsVersion {
		t.Fatalf("open row: act %+v (%v), actions %+v, specs %q", st, ok, byUnit[open].Actions, byUnit[open].SpecsVersion)
	}
	if slices.ContainsFunc(byUnit[open].Actions, func(st api.ActionState) bool { return st.ID == "review" || st.ID == "open" }) {
		t.Fatalf("a thread row offers a pull request's action: %+v", byUnit[open].Actions)
	}
	asked, ok, err := m.Row(ctx, open, catalog.Asked{Name: "act"})
	if err != nil || !ok {
		t.Fatalf("row asked: %v %v", ok, err)
	}
	if st, found := act(asked); !found || !st.Enabled || asked.Unit != open {
		t.Fatalf("row asked for act: %+v", asked)
	}
}
