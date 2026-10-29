package readmodel_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/source/fakesource"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
)

// planGates are the automatic rounds open plan gates hold, by unit.
type planGates map[unit.Unit]overlay.Pending

func (p planGates) Plans(context.Context) (map[unit.Unit]overlay.Pending, error) { return p, nil }

func TestPendingRoundsAreReadFromPlanGatesAlone(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	th := fakesource.Thread{Key: "C1/171.1", Channel: "general", Title: "deploy?", Kind: fakesource.KindWaiting, WaitsOn: "ana", Reason: "ana owes a reply", Since: at}
	row := func(plans readmodel.Plans) (api.Row, string) {
		t.Helper()
		m := readmodel.New(change.NewLog(change.DefaultWindow, nil), nil, plans, nil, nil, fakesource.New(th).Rows())
		page, err := m.Rows(context.Background(), api.Since{})
		if err != nil || len(page.Rows) != 1 {
			t.Fatalf("rows %+v %v", page.Rows, err)
		}
		raw, err := json.Marshal(page.Rows[0])
		if err != nil {
			t.Fatal(err)
		}
		return page.Rows[0], string(raw)
	}
	gated, raw := row(planGates{th.Unit(): {Intent: "follow-up", Reason: "ana replied", Since: at, Deadline: at.Add(time.Minute)}})
	if gated.Kind != overlay.KindRoundPending || gated.Summary.WaitsOn != overlay.WaitsOnMe || gated.Summary.Reason != "follow-up waiting for your answer: ana replied" {
		t.Fatalf("the row of a unit whose plan gate holds a round: %+v", gated)
	}
	if strings.Contains(raw, `"pending"`) || strings.Contains(raw, at.Add(time.Minute).Format(time.RFC3339)) {
		t.Fatalf("the row carries the round's window, which its plan gate serves: %s", raw)
	}
	for name, plans := range map[string]readmodel.Plans{"no plan gate": planGates{}, "no plans": nil} {
		if r, _ := row(plans); r.Kind != fakesource.KindWaiting || r.Summary.WaitsOn != "ana" {
			t.Errorf("%s: the row %+v, want the thread's own situation", name, r)
		}
	}
}
