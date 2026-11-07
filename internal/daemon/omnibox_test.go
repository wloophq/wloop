package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var pastedPR = unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}

// resolveRig is a tasks rig whose plugins claim one address, pastedPR's, and holding a task of pastedPR.
func resolveRig(t *testing.T) *tasksRig {
	t.Helper()
	r := newTasksRig(t, func(d *Deps) {
		d.Resolve = func(_ context.Context, address string) (unit.Unit, bool, error) {
			return pastedPR, address == "https://forge.example/acme/api/pull/8", nil
		}
	})
	r.put(t, task.Task{ID: "t-0000000000000008", State: "next", Category: task.CategoryReady, Title: "review the retries", Units: []unit.Unit{pastedPR}})
	return r
}

func resolve(t *testing.T, r *tasksRig, text string) api.OmniboxResolved {
	t.Helper()
	body, _ := json.Marshal(api.OmniboxText{Text: text})
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("POST", api.OmniboxResolvePath, strings.NewReader(string(body))))
	if rec.Code != 200 {
		t.Fatalf("%q: %d %s", text, rec.Code, rec.Body)
	}
	var out api.OmniboxResolved
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func storedTasks(t *testing.T, r *tasksRig) int {
	t.Helper()
	es, err := r.db.Tasks().Tasks(context.Background(), task.Query{All: true})
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

func TestOmniboxResolveRoutePastedAddress(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	got := resolve(t, r, "https://forge.example/acme/api/pull/8")
	if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != pastedPR || len(got.Unit.Tasks) != 1 || got.Unit.Tasks[0].ID != "t-0000000000000008" {
		t.Fatalf("%+v", got)
	}
}

func TestOmniboxResolveRouteLoneTaskID(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	r.put(t, task.Task{ID: "t-00000000000000c1", State: "next", Category: task.CategoryReady, Title: "call Ada"})
	got := resolve(t, r, "t-00000000000000c1")
	if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != task.UnitOf("t-00000000000000c1") || len(got.Unit.Tasks) != 1 {
		t.Fatalf("%+v", got)
	}
	if got := resolve(t, r, "t-0000000000000008"); got.Kind != "unit" || got.Unit.Unit != pastedPR {
		t.Fatalf("a task of a unit: %+v", got)
	}
	if got := resolve(t, r, "t-ffff"); got.Kind != "search" || got.Why != "no_unit_or_task" {
		t.Fatalf("an unknown id: %+v", got)
	}
}

func TestOmniboxResolveRouteUnclaimedAddress(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	before := storedTasks(t, r)
	got := resolve(t, r, "https://elsewhere.example/x/y/1")
	if got.Kind != "search" || got.Why != "unclaimed_address" || got.Params["address"] != "https://elsewhere.example/x/y/1" || got.Unit != nil || got.Preview != nil {
		t.Fatalf("%+v", got)
	}
	if after := storedTasks(t, r); after != before {
		t.Fatalf("resolving created tasks: %d to %d", before, after)
	}
}

func TestOmniboxResolveRouteVerb(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	for text, want := range map[string]api.OmniboxOperation{
		"revisar acme/api#8":   {Op: "action", Unit: &pastedPR, Action: "review"},
		"feche t-000000000000": {Op: "verb", Task: "t-0000000000000008", Verb: "done"},
		"anote ligar para Ada": {Op: "capture", Title: "ligar para Ada"},
	} {
		got := resolve(t, r, text)
		if got.Kind != "verb" || got.Preview == nil || len(got.Preview.Operations) != 1 {
			t.Fatalf("%q: %+v", text, got)
		}
		op := got.Preview.Operations[0]
		if op.Op != want.Op || op.Action != want.Action || op.Task != want.Task || op.Verb != want.Verb || op.Title != want.Title ||
			(want.Unit != nil && (op.Unit == nil || *op.Unit != *want.Unit)) {
			t.Errorf("%q: %+v, want %+v", text, op, want)
		}
	}
}

func TestResolveWhyCodesAreTheAPIs(t *testing.T) {
	for core, wire := range map[string]string{
		omnibox.WhyUnclaimed: api.OmniboxWhyUnclaimed, omnibox.WhyNoRef: api.OmniboxWhyNoRef,
		omnibox.WhyAmbiguous: api.OmniboxWhyAmbiguous, omnibox.WhyNothingNamed: api.OmniboxWhyNothingNamed,
	} {
		if core != wire {
			t.Errorf("the core resolves %q and the API words %q", core, wire)
		}
	}
}

func TestTheAskSnapshotHoldsTheVocabulary(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	snap, _, err := r.s.omniboxSnapshot(context.Background(), r.g)
	if err != nil {
		t.Fatal(err)
	}
	verbs, _ := snap.Vocabulary.([]omnibox.Verb)
	if !slices.ContainsFunc(verbs, func(v omnibox.Verb) bool { return v.Action == "review" }) {
		t.Fatalf("vocabulary %+v", snap.Vocabulary)
	}
}

func TestOmniboxResolveEmptyText(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("POST", api.OmniboxResolvePath, strings.NewReader(`{"text":"  "}`)))
	if rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestOmniboxResolveReadsAForgeAddressThroughTheCodecs(t *testing.T) {
	t.Parallel()
	declines := func(d *Deps) {
		d.Resolve = func(context.Context, string) (unit.Unit, bool, error) { return unit.Unit{}, false, nil }
	}
	for name, edit := range map[string]func(*Deps){"no resolver": nil, "a resolver that declines": declines} {
		r := newTasksRig(t, edit)
		r.put(t, task.Task{ID: "t-0000000000000008", State: "next", Category: task.CategoryReady, Title: "review the retries", Units: []unit.Unit{pastedPR}})
		got := resolve(t, r, "https://github.com/acme/api/pull/8")
		if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != pastedPR || len(got.Unit.Tasks) != 1 {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
