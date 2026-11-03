package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestUnitBriefsReadEachUnitsBriefInOneStatement(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	a, b, none := unit.Unit{Provider: "slack", Key: "T000/C001/1.1"}, unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, unit.Unit{Provider: "slack", Key: "T000/C001/9.9"}
	for i, u := range []unit.Unit{a, b} {
		if err := s.db.PutUnitBrief(ctx, u, "v1", `{"goal":"g`+string(rune('1'+i))+`"}`, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.UnitBriefs(ctx, []unit.Unit{a, b, none})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[none]; ok || len(got) != 2 {
		t.Fatalf("briefs %+v, want a's and b's", got)
	}
	for _, u := range []unit.Unit{a, b} {
		if one, ok, err := s.UnitBrief(ctx, u); err != nil || !ok || one != got[u] {
			t.Errorf("%s: UnitBriefs %+v, UnitBrief %+v %v %v", u, got[u], one, ok, err)
		}
	}
	if empty, err := s.UnitBriefs(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no units: %+v %v", empty, err)
	}
}
