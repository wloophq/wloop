package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/omnibox"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
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

var unheldPR = unit.Unit{Provider: unit.GitHub, Key: "acme/api#99"}

func enabledActions(states []api.ActionState) []string {
	var out []string
	for _, st := range states {
		if st.Enabled {
			out = append(out, st.ID)
		}
	}
	return out
}

func specIDs(specs []api.ActionSpec) []string {
	out := make([]string, len(specs))
	for i, sp := range specs {
		out[i] = sp.ID
	}
	return out
}

func TestOmniboxResolveOffersAnUnheldUnitTheActionsOfItsRow(t *testing.T) {
	t.Parallel()
	r := resolveRig(t)
	before := storedTasks(t, r)
	got := resolve(t, r, "https://github.com/acme/api/pull/99")
	if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != unheldPR || len(got.Unit.Tasks) != 0 {
		t.Fatalf("%+v", got)
	}
	row, err := r.s.currentRow(context.Background(), unheldPR)
	if err != nil {
		t.Fatal(err)
	}
	want := enabledActions(row.Actions)
	offered := specIDs(got.Unit.Actions)
	if !slices.Equal(offered, want) {
		t.Fatalf("offered %v, the row its plugin derives offers %v", offered, want)
	}
	for _, id := range []string{"review", "track", "take_over"} {
		if !slices.Contains(offered, id) {
			t.Errorf("no %s among %v", id, offered)
		}
	}
	if got.Unit.Row == nil || got.Unit.Row.Unit != unheldPR || !slices.Equal(enabledActions(got.Unit.Row.Actions), want) || got.Unit.Permalink != row.Link {
		t.Errorf("the derived row %+v, permalink %q", got.Unit.Row, got.Unit.Permalink)
	}
	if got.Unit.Held || got.Unit.Why != "" {
		t.Errorf("held %v, why %q", got.Unit.Held, got.Unit.Why)
	}
	if held := resolve(t, r, "https://forge.example/acme/api/pull/8"); held.Unit.Row != nil || !held.Unit.Held {
		t.Errorf("a unit the loop holds: held %v, row %+v", held.Unit.Held, held.Unit.Row)
	}
	if after := storedTasks(t, r); after != before {
		t.Fatalf("resolving created tasks: %d to %d", before, after)
	}
}

// downOnGitHub reads no pull request: GitHub does not answer.
type downOnGitHub struct{ openOnGitHub }

func (downOnGitHub) CandidateByRef(context.Context, pr.Ref, string) (dgh.CandidatePage, error) {
	return dgh.CandidatePage{}, errors.New("GitHub does not answer")
}

func TestOmniboxResolveSaysWhyAUnitOffersNoAction(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		github ghSearch
		why    string
	}{
		"its source does not answer": {downOnGitHub{}, api.OmniboxUnitUnreadable},
		"its source has none":        {missingOnGitHub{}, api.OmniboxUnitNoRow},
	} {
		r := newTasksRig(t, func(d *Deps) { exploreOn(d, c.github) })
		got := resolve(t, r, "https://github.com/acme/api/pull/99")
		if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != unheldPR || len(got.Unit.Actions) != 0 || got.Unit.Row != nil || got.Unit.Held {
			t.Fatalf("%s: %+v", name, got.Unit)
		}
		if got.Unit.Why != c.why || got.Unit.Params["unit"] != "acme/api#99" {
			t.Errorf("%s: why %q %v", name, got.Unit.Why, got.Unit.Params)
		}
	}
}

// missingOnGitHub has no pull request.
type missingOnGitHub struct{ openOnGitHub }

func (missingOnGitHub) CandidateByRef(context.Context, pr.Ref, string) (dgh.CandidatePage, error) {
	return dgh.CandidatePage{}, nil
}

func TestOmniboxResolveReadsAKeyThroughTheUnitCodecs(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, func(d *Deps) {
		d.Resolve = func(context.Context, string) (unit.Unit, bool, error) { return unit.Unit{}, false, nil }
	})
	got := resolve(t, r, "acme/api#99")
	if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != unheldPR || got.Unit.Held || !slices.Contains(specIDs(got.Unit.Actions), "review") {
		t.Fatalf("a key of a unit the loop does not hold: %+v", got)
	}
	verb := resolve(t, r, "assumir acme/api#99")
	if verb.Kind != "verb" || verb.Preview == nil || len(verb.Preview.Operations) != 1 {
		t.Fatalf("%+v", verb)
	}
	op := verb.Preview.Operations[0]
	if op.Op != "action" || op.Action != "take_over" || op.Unit == nil || *op.Unit != unheldPR {
		t.Fatalf("%+v", op)
	}
	row, err := r.s.currentRow(context.Background(), unheldPR)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := offerOf(row, "take_over"); st.Confirm == "" || op.Confirm != st.Confirm {
		t.Errorf("the operation asks %q, its row's action %q", op.Confirm, st.Confirm)
	}
	if review := resolve(t, r, "revisar acme/api#99").Preview.Operations[0]; review.Confirm != "" {
		t.Errorf("an action that asks nothing carries %q", review.Confirm)
	}
}

func TestOmniboxResolveReadsAKeyInAnyCaseAsTheUnitTheLoopKnows(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	listed := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	if got := resolve(t, r, "Acme/API#7"); got.Kind != "unit" || got.Unit.Unit != listed || !got.Unit.Held {
		t.Fatalf("a row's key in another case: %+v", got.Unit)
	}
	r.put(t, task.Task{ID: "t-0000000000000007", State: "next", Category: task.CategoryReady, Title: "review the export", Units: []unit.Unit{listed}})
	for _, text := range []string{"Acme/API#7", "https://github.com/ACME/api/pull/7"} {
		if got := resolve(t, r, text); got.Kind != "unit" || got.Unit.Unit != listed || len(got.Unit.Tasks) != 1 {
			t.Fatalf("%q: %+v", text, got.Unit)
		}
	}
	if got := resolve(t, r, "done ACME/API#7"); got.Kind != "verb" || got.Preview == nil || got.Preview.Operations[0].Task != "t-0000000000000007" {
		t.Fatalf("a task verb on it: %+v", got)
	}
}

func TestOmniboxResolveFindsAShortLabelAmongTheRows(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	listed := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	for _, text := range []string{"api#7", "API#7", "acme/api#7"} {
		got := resolve(t, r, text)
		if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != listed || len(got.Unit.Tasks) != 0 || !slices.Contains(specIDs(got.Unit.Actions), "review") {
			t.Fatalf("%q, a row no task links: %+v", text, got)
		}
	}
	if got := resolve(t, r, "api#404"); got.Kind != "search" || got.Why != "no_unit_or_task" {
		t.Fatalf("a label of no row: %+v", got)
	}
	r.put(t, task.Task{ID: "t-0000000000000007", State: "next", Category: task.CategoryReady, Title: "review the export", Units: []unit.Unit{listed}})
	if got := resolve(t, r, "api#7"); got.Kind != "unit" || got.Unit.Unit != listed || len(got.Unit.Tasks) != 1 {
		t.Fatalf("a row a task links names one unit: %+v", got)
	}
}

func TestOmniboxResolveNeverGuessesAmongRowsOfOneShortLabel(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	other := pr.Ref{Owner: "globex", Repo: "api", Number: 7}
	inventorytest.Of(r.s.store()).UpsertPR(context.Background(), dinv.PR{Ref: other, Title: "Retry the export", Author: "ana", State: "OPEN", HeadOid: "h1", Reasons: []string{"review-requested"}, URL: other.URL()})
	got := resolve(t, r, "api#7")
	if got.Kind != "agent" || got.Why != "ambiguous_ref" || got.Params["ref"] != "api#7" || got.Unit != nil {
		t.Fatalf("%+v", got)
	}
	if got := resolve(t, r, "revisar api#7"); got.Kind != "agent" || got.Why != "ambiguous_ref" || got.Preview != nil {
		t.Fatalf("a verb on it: %+v", got)
	}
	if got := resolve(t, r, "globex/api#7"); got.Kind != "unit" || got.Unit.Unit != other.Unit() {
		t.Fatalf("the whole label: %+v", got)
	}
}

func TestOmniboxResolveNeverGuessesAShortLabelWhileTheRowsCannotBeRead(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, func(d *Deps) {
		d.Identity = func(context.Context) (src.Identity, error) { return src.Identity{}, src.ErrUnavailable }
	})
	r.put(t, task.Task{ID: "t-0000000000000008", State: "next", Category: task.CategoryReady, Title: "review the retries", Units: []unit.Unit{pastedPR}})
	if _, err := r.s.readModel().Labels(context.Background()); err == nil {
		t.Fatal("the rows read without a viewer: the rig no longer fails them")
	}
	for _, text := range []string{"https://github.com/acme/api/pull/8", "acme/api#8", "t-0000000000000008"} {
		got := resolve(t, r, text)
		if got.Kind != "unit" || got.Unit == nil || got.Unit.Unit != pastedPR || len(got.Unit.Tasks) != 1 || len(got.Unit.Actions) != 0 {
			t.Errorf("%q: %+v", text, got)
		}
		if got.Unit != nil && got.Unit.Why != api.OmniboxUnitUnreadable {
			t.Errorf("%q: why %q", text, got.Unit.Why)
		}
	}
	for _, text := range []string{"api#8", "revisar api#8"} {
		body, _ := json.Marshal(api.OmniboxText{Text: text})
		rec := httptest.NewRecorder()
		r.s.Handler().ServeHTTP(rec, apiRequest("POST", api.OmniboxResolvePath, strings.NewReader(string(body))))
		if rec.Code == 200 {
			t.Errorf("%q resolved among the tasks alone: %s", text, rec.Body)
		}
	}
}

func TestOmniboxResolveVerbCarriesTheActionsGuidanceAndItsInput(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	op := resolve(t, r, "revisar acme/api#99 focus on the migration").Preview.Operations[0]
	if op.Guidance != "focus on the migration" {
		t.Errorf("guidance %q", op.Guidance)
	}
	var spec api.ActionSpec
	for _, s := range r.s.servedSpecs() {
		if s.ID == "review" {
			spec = s
		}
	}
	if spec.Input == nil || op.Input == nil || *op.Input != *spec.Input {
		t.Errorf("the operation asks %+v, the action's spec %+v", op.Input, spec.Input)
	}
}

func TestOmniboxPreviewDropsGuidanceAnActionDoesNotTake(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	var plain string
	for _, s := range r.s.servedSpecs() {
		if s.Input == nil {
			plain = s.ID
			break
		}
	}
	if plain == "" {
		t.Fatal("every served action takes guidance")
	}
	ops := r.s.asking(context.Background(), []api.OmniboxOperation{{Op: "action", Unit: &unheldPR, Action: plain, Guidance: "stray words"}})
	if ops[0].Guidance != "" || ops[0].Input != nil {
		t.Errorf("%s: %+v", plain, ops[0])
	}
}
