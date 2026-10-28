package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

type unitBriefs map[unit.Unit]string

func (m unitBriefs) UnitBrief(_ context.Context, u unit.Unit) (inventory.UnitBrief, bool, error) {
	b, ok := m[u]
	return inventory.UnitBrief{Run: 1, Brief: b}, ok, nil
}

func TestEveryBriefedSubjectReadsItsUnitsBrief(t *testing.T) {
	ctx := context.Background()
	prU := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	c, err := brief.Compose(brief.Schema, []byte(`{"type":"object","properties":{"approach":{"type":"string","enum":["patch","rewrite"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	b := briefedFacts{store: unitBriefs{
		prU:    `{"goal":"Batch the export","kind":"feature","size":"m","risk":"high","tags":["area:jobs"]}`,
		thread: `{"goal":"The export is down","kind":"incident","risk":"high","tags":[],"extra":{"approach":"patch"},"extra_versions":{"approach":"` + c.FieldVersions()["approach"] + `"}}`,
	}, intent: func(unit.ProviderID) brief.Intent { return brief.Intent{Schema: c} }}
	for name, cs := range map[string]struct {
		u     unit.Unit
		f     workflow.Facts
		kind  string
		extra any
	}{
		"a pull request":         {prU, workflow.Facts{"pr.state": "OPEN"}, "feature", ""},
		"a Slack thread":         {thread, workflow.Facts{}, "incident", "patch"},
		"an entry of the thread": {unit.Unit{Provider: unit.Task, Key: "e1"}, workflow.Facts{"entry.unit": "slack:" + thread.Key}, "incident", "patch"},
		"an entry without unit":  {unit.Unit{Provider: unit.Task, Key: "e2"}, workflow.Facts{"entry.unit": ""}, "", ""},
	} {
		got, err := b.add(ctx, cs.u, cs.f)
		if err != nil {
			t.Fatal(err)
		}
		if got["brief.kind"] != cs.kind || got["brief.extra.approach"] != cs.extra {
			t.Errorf("%s: %v", name, got)
		}
		for k := range cs.f {
			if _, ok := got[k]; !ok {
				t.Errorf("%s: lost %s", name, k)
			}
		}
	}
	if got, _ := b.add(ctx, prU, workflow.Facts{}); !slices.Equal(got["brief.missing"].([]string), []string{"approach"}) {
		t.Errorf("a pull request brief without the field: %v", got["brief.missing"])
	}
	session := unit.Unit{Provider: unit.Session, Key: "s1"}
	if got, _ := b.add(ctx, session, workflow.Facts{"session.idle": true}); len(got) != 1 {
		t.Errorf("a session got brief facts: %v", got)
	}
}

func TestABriefedSubjectReadsItsProvidersBriefIntent(t *testing.T) {
	ctx := context.Background()
	compose := func(extra string) brief.Composed {
		c, err := brief.Compose(brief.Schema, []byte(extra))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	byProvider := map[unit.ProviderID]brief.Composed{
		unit.GitHub: compose(`{"type":"object","properties":{"approach":{"type":"string"}}}`),
		"slack":     compose(`{"type":"object","properties":{"urgency":{"type":"string"}}}`),
	}
	prU := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	thread := unit.Unit{Provider: "slack", Key: "T000AAA/C000AAA/1700000000.000100"}
	b := briefedFacts{store: unitBriefs{prU: `{"goal":"g","kind":"feature"}`, thread: `{"goal":"g","kind":"request"}`},
		intent: func(p unit.ProviderID) brief.Intent { return brief.Intent{Schema: byProvider[p]} }}
	for name, cs := range map[string]struct {
		u          unit.Unit
		f          workflow.Facts
		has, hasnt string
	}{
		"a pull request":         {prU, workflow.Facts{}, "approach", "urgency"},
		"a Slack thread":         {thread, workflow.Facts{}, "urgency", "approach"},
		"an entry of the thread": {unit.Unit{Provider: unit.Task, Key: "e1"}, workflow.Facts{"entry.unit": "slack:" + thread.Key}, "urgency", "approach"},
	} {
		got, err := b.add(ctx, cs.u, cs.f)
		if err != nil {
			t.Fatal(err)
		}
		_, has := got["brief.extra."+cs.has]
		_, hasnt := got["brief.extra."+cs.hasnt]
		if !has || hasnt || !slices.Equal(got["brief.missing"].([]string), []string{cs.has}) {
			t.Errorf("%s: %v", name, got)
		}
	}
}
