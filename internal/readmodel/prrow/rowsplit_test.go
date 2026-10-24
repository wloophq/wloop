package prrow

import (
	"bytes"
	"encoding/json"
	"flag"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/fakethread"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/situation"
)

var update = flag.Bool("update", false, "rewrite testdata/rows.json and testdata/catalog.json")

// deriveCases derives every fixture row as Provider.Derive does.
func deriveCases(t *testing.T, at time.Time) []api.Row {
	t.Helper()
	cases := rowCases()
	d := fixtureDeriver(t, at, cases)
	out := make([]api.Row, len(cases))
	for i, c := range cases {
		r, p := d.Parts(c.pr, c.events, c.override)
		p.Brief, p.Intake = c.brief, c.intake
		if c.activity != nil {
			d.AddActivity(&p, *c.activity)
		}
		out[i] = d.Seal(r, p)
	}
	return out
}

func payloadOf(t *testing.T, r api.Row) Payload {
	t.Helper()
	p, ok := PayloadOf(r)
	if !ok {
		t.Fatalf("%s has no GitHub payload", r.Label)
	}
	return p
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGitHubRowKeepsPRFieldsInThePayload(t *testing.T) {
	moved := []string{"author", "repo", "head", "workspace", "brief", "facts", "override", "intake", "reasons", "approved", "waiting_on", "since", "stalled", "reason"}
	for _, r := range deriveCases(t, fixtureNow) {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(mustJSON(t, r), &top); err != nil {
			t.Fatal(err)
		}
		for _, k := range moved {
			if _, ok := top[k]; ok {
				t.Errorf("%s: top-level %q", r.Label, k)
			}
		}
		payloadOf(t, r)
	}
}

func TestPayloadOfRefusesAnotherProvider(t *testing.T) {
	gh := deriveCases(t, fixtureNow)[0]
	thread := fakethread.Row(fakethread.Thread{Key: "C1/1.1", Channel: "general", Kind: fakethread.KindWaiting, WaitsOn: "me"})
	thread.Payload = gh.Payload
	if _, ok := PayloadOf(thread); ok {
		t.Error("a thread row read as GitHub's")
	}
	gh.Payload = nil
	if _, ok := PayloadOf(gh); ok {
		t.Error("a row with no payload read as GitHub's")
	}
	if NeedsMe(thread) || InScope(thread) {
		t.Error("a thread row passes the GitHub predicates")
	}
}

func TestSummaryLineIsTheMenuBarLabel(t *testing.T) {
	menu := map[situation.Kind]string{situation.KindRoundPending: "round soon", situation.KindHeldByCap: "held (cap)", situation.KindBlocked: "round blocked",
		situation.KindLoopWaiting: "agent asks", situation.KindLoopStopped: "agent stopped", situation.KindLoopWorking: "agent working"}
	for _, k := range situation.Order {
		r := api.Row{Kind: api.Kind(k), Summary: api.Summary{Reason: "the reason"}}
		want, ok := menu[k]
		if !ok {
			want = "the reason"
		}
		if got := summaryLine(r, Payload{}); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
		for _, c := range []struct {
			intake api.Intake
			want   string
		}{
			{api.Intake{State: api.IntakeBriefing}, "briefing…"},
			{api.Intake{State: api.IntakeAccepted, Attempt: 1}, "briefing… (attempt 1)"},
			{api.Intake{State: api.IntakeBriefing, Attempt: 2, LastError: "boom"}, "briefing… (attempt 2; boom)"},
			{api.Intake{State: api.IntakeStarting, Attempt: 2, LastError: "boom"}, cmpOr(menu[k], "the reason")},
		} {
			if got := summaryLine(r, Payload{Intake: &c.intake}); got != c.want {
				t.Errorf("%s with intake %+v: %q, want %q", k, c.intake, got, c.want)
			}
		}
	}
	for _, r := range deriveCases(t, fixtureNow) {
		if r.Summary.Line == "" {
			t.Errorf("%s has no line", r.Label)
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// kindOrder and kindLabel are the order and the labels of the GitHub situations.
var (
	kindOrder = []string{"round_pending", "loop_waiting", "loop_stopped", "blocked", "to_address", "held", "loop_working", "changes", "checks", "conflicts",
		"reply", "reviewer_back", "review", "no_reviewer", "merge", "draft", "muted", "done"}
	kindLabel = map[string]string{"done": "closed", "muted": "muted", "held": "held (cap)", "blocked": "round blocked", "to_address": "to address",
		"round_pending": "round soon", "loop_working": "agent working", "loop_waiting": "agent asks", "loop_stopped": "agent stopped", "draft": "draft",
		"checks": "checks failing", "changes": "changes asked", "merge": "can merge", "conflicts": "merge conflicts", "reply": "reply due",
		"reviewer_back": "reviewer due", "review": "review", "no_reviewer": "no reviewer"}
)

func TestSituationsServeTheirLabels(t *testing.T) {
	c := readmodel.BuildCatalog(New(Source{}))
	if len(c.Situations) != len(kindOrder) {
		t.Fatalf("%d situations, want %d", len(c.Situations), len(kindOrder))
	}
	for i, s := range c.Situations {
		if s.Provider != "github" || string(s.Kind) != kindOrder[i] || s.Label != kindLabel[kindOrder[i]] || s.Rank != i {
			t.Errorf("situation %d: %+v", i, s)
		}
	}
}

func TestCatalogServesTheGitHubViews(t *testing.T) {
	want := []struct{ id, label string }{{"needs-me", "Needs me"}, {"stalled", "Stalled"}, {"mine", "My PRs"}, {"loop", "In loop"}, {"plan", "Plan"},
		{"approved", "Approved"}, {"dash", "Backlog"}, {"done", "Done"}, {"all", "All"}, {"explore", "Explore"}, {"recent", "Recent"}}
	c := readmodel.BuildCatalog(New(Source{}))
	if len(c.Views) != len(want) {
		t.Fatalf("%d views: %+v", len(c.Views), c.Views)
	}
	custom := []string{"plan", "dash", "explore", "recent"}
	for i, v := range c.Views {
		if v.ID != want[i].id || v.Label != want[i].label || v.Provider != "github" || v.Order != i || v.Rows == slices.Contains(custom, v.ID) ||
			!slices.Contains(v.Surfaces, "web") {
			t.Errorf("view %d: %+v", i, v)
		}
	}
	var tui []string
	for _, v := range c.ViewsOn("tui") {
		tui = append(tui, v.ID)
	}
	if !slices.Equal(tui, []string{"needs-me", "mine", "loop", "plan"}) {
		t.Errorf("terminal views %v", tui)
	}
	if c.Version == "" || c.Version != readmodel.BuildCatalog(New(Source{})).Version {
		t.Errorf("version %q is not stable", c.Version)
	}
}

// goldenFacet is a field's values as the cockpit computes them, with its
// group key; age and due hold the time and the due flag instead.
type goldenFacet struct {
	Values  []string `json:"values"`
	Key     *string  `json:"key"`
	SinceMS *float64 `json:"since_ms"`
	Due     *bool    `json:"due"`
}

func TestFacetValuesMatchTheCockpit(t *testing.T) {
	raw := readTestdata(t, "testdata/facets.golden.json")
	var golden []struct {
		Label  string                 `json:"label"`
		Facets map[string]goldenFacet `json:"facets"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	specs := map[string]api.FacetSpec{}
	for _, f := range FacetSpecs() {
		specs[f.ID] = f
	}
	rows := deriveCases(t, fixtureNow)
	for i, g := range golden {
		r := rows[i]
		if r.Label != g.Label {
			t.Fatalf("row %d is %s, golden %s", i, r.Label, g.Label)
		}
		for id, want := range g.Facets {
			got := r.Facets[id]
			switch id {
			case FacetOpened:
				bucket := "none"
				if len(got) > 0 {
					bucket = api.BucketOf(specs[id].Buckets, fixtureNow, mustTime(t, got[0]))
				}
				if !slices.Equal([]string{bucket}, want.Values) {
					t.Errorf("%s %s: %s bucketed %q, want %v", r.Label, id, got, bucket, want.Values)
				}
				continue
			case FacetAge:
				if len(got) != 1 || float64(mustTime(t, got[0]).UnixMilli()) != *want.SinceMS {
					t.Errorf("%s age: %v, want %v ms", r.Label, got, *want.SinceMS)
				}
				continue
			case FacetDue:
				due := slices.ContainsFunc(got, func(v string) bool { return api.BucketOf(specs[id].Buckets, fixtureNow, mustTime(t, v)) == "due" })
				if due != *want.Due {
					t.Errorf("%s due: %v from %v, want %v", r.Label, due, got, *want.Due)
				}
				continue
			case FacetIs:
				want.Values = slices.DeleteFunc(slices.Clone(want.Values), func(v string) bool { return v == "due" })
			}
			if !slices.Equal(got, want.Values) && !(len(got) == 0 && len(want.Values) == 0) {
				t.Errorf("%s %s: %v, want %v", r.Label, id, got, want.Values)
			}
			if want.Key != nil {
				key := "none"
				if len(got) > 0 {
					key = got[0]
				}
				if key != *want.Key {
					t.Errorf("%s %s: key %q, want %q", r.Label, id, key, *want.Key)
				}
			}
		}
	}
	if *update {
		writeJSON(t, "testdata/rows.json", rows)
		writeJSON(t, "testdata/catalog.json", readmodel.BuildCatalog(New(Source{})))
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	brandtest.CanUpdate(t)
	if err := os.WriteFile(path, []byte(brandtest.Collapse(string(raw))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRowsGoldenIsCurrent(t *testing.T) {
	for path, v := range map[string]any{"testdata/rows.json": deriveCases(t, fixtureNow), "testdata/catalog.json": readmodel.BuildCatalog(New(Source{}))} {
		want, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got := readTestdata(t, path)
		if !bytes.Equal(bytes.TrimSpace(got), want) {
			t.Errorf("%s is stale; run go test -run TestFacetValuesMatchTheCockpit -update", path)
		}
	}
}

func mustTime(t *testing.T, v string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestFacetsDoNotDependOnTheClock(t *testing.T) {
	now, later := deriveCases(t, fixtureNow), deriveCases(t, fixtureNow.Add(24*time.Hour))
	compared := 0
	for i, a := range now {
		b := later[i]
		pa, pb := payloadOf(t, a), payloadOf(t, b)
		if a.Kind != b.Kind || a.Summary.Stalled != b.Summary.Stalled || (pa.Mute == nil) != (pb.Mute == nil) || !slices.Equal(a.Views, b.Views) {
			continue
		}
		compared++
		if !maps.EqualFunc(a.Facets, b.Facets, slices.Equal) {
			t.Errorf("%s: facets moved with the clock:\n%v\n%v", a.Label, a.Facets, b.Facets)
		}
	}
	if compared < len(now)/2 {
		t.Fatalf("only %d of %d rows kept their situation a day later", compared, len(now))
	}
}

func TestProviderFacetsStayWithTheirRows(t *testing.T) {
	c := readmodel.BuildCatalog(New(Source{}), fakethread.New())
	i := slices.IndexFunc(c.Facets, func(f api.FacetSpec) bool { return f.ID == fakethread.FacetChannel })
	if i < 0 || c.Facets[i].Provider != "thread" {
		t.Fatalf("channel facet: %+v", c.Facets)
	}
	for _, r := range deriveCases(t, fixtureNow) {
		if _, ok := r.Facets[fakethread.FacetChannel]; ok {
			t.Errorf("%s carries the thread facet", r.Label)
		}
		for id := range r.Facets {
			if j := slices.IndexFunc(c.Facets, func(f api.FacetSpec) bool { return f.ID == id }); j < 0 || c.Facets[j].Provider != "github" {
				t.Errorf("%s: facet %s is not GitHub's", r.Label, id)
			}
		}
	}
	thread := fakethread.Row(fakethread.Thread{Key: "C1/1.1", Channel: "general", Kind: fakethread.KindWaiting, WaitsOn: "me"})
	if !slices.Equal(slices.Collect(maps.Keys(thread.Facets)), []string{fakethread.FacetChannel}) {
		t.Errorf("thread facets %v", thread.Facets)
	}
}

func TestMutedRowKeepsItsUnmutedSituation(t *testing.T) {
	for _, c := range rowCases() {
		if !strings.HasPrefix(c.name, "muted") {
			continue
		}
		d := fixtureDeriver(t, fixtureNow, []rowCase{c})
		r := d.Row(c.pr, c.events, c.override)
		p := payloadOf(t, r)
		if situation.Kind(r.Kind) != situation.KindMuted || p.Mute == nil || p.UnmutedKind != string(situation.KindReview) || p.UnmutedWaitingOn != "me" {
			t.Errorf("%s: kind %s, payload %+v", c.name, r.Kind, p)
		}
		if !NeedsMe(r) || slices.Contains(r.Views, api.ViewNeedsMe) {
			t.Errorf("%s: NeedsMe %v, views %v", c.name, NeedsMe(r), r.Views)
		}
		if p.Mute.UntilStateChange != c.override.MuteUntilStateChange || !p.Mute.Until.Equal(c.override.MutedUntil) {
			t.Errorf("%s: mute %+v", c.name, p.Mute)
		}
	}
	plain := deriveCases(t, fixtureNow)[0]
	if p := payloadOf(t, plain); p.Mute != nil || p.UnmutedKind != "" || p.UnmutedWaitingOn != "" {
		t.Errorf("an unmuted row: %+v", p)
	}
}

func TestNeedsMeAndInScopeMatchTheDeriver(t *testing.T) {
	for _, r := range deriveCases(t, fixtureNow) {
		p := payloadOf(t, r)
		if in := slices.Contains(r.Views, api.ViewNeedsMe); in != (NeedsMe(r) && p.Mute == nil) {
			t.Errorf("%s: needs-me %v, NeedsMe %v, muted %v", r.Label, in, NeedsMe(r), p.Mute != nil)
		}
		if want := len(p.Reasons) > 0 || p.Workspace.IsReview || p.Override.Adopted || p.Override.Tracked; InScope(r) != want {
			t.Errorf("%s: InScope %v, want %v", r.Label, InScope(r), want)
		}
	}
}

func TestLastHumanSkipsViewerAndBots(t *testing.T) {
	c := rowCases()[0]
	d := fixtureDeriver(t, fixtureNow, nil)
	d.Ignore = []string{"copilot"}
	ev := func(kind, actor string, at time.Time) dinv.Event {
		return dinv.Event{PR: c.pr.Ref, Kind: kind, Key: actor + at.String(), Actor: actor, At: at}
	}
	only := []dinv.Event{ev(dinv.KindComment, fixtureViewer, ago(time.Hour)), ev(dinv.KindReview, "renovate[bot]", ago(time.Minute)),
		ev(dinv.KindComment, "Copilot", ago(time.Second)), ev(dinv.KindMuted, "ana", ago(time.Second))}
	if p := payloadOf(t, d.Row(c.pr, only, c.override)); p.LastHuman != nil {
		t.Errorf("viewer and bots only: %+v", p.LastHuman)
	}
	evs := append(only, ev(dinv.KindComment, "bia", ago(2*time.Hour)), ev(dinv.KindReview, "carol", ago(30*time.Minute)))
	r := d.Row(c.pr, evs, c.override)
	p := payloadOf(t, r)
	if h := p.LastHuman; h == nil || h.Who != "carol" || h.What != dinv.KindReview || !h.At.Equal(ago(30*time.Minute)) || h.Link != r.Link || h.Link == "" {
		t.Errorf("last human %+v", h)
	}
}

// readTestdata reads a golden file with the active brand in place of its
// tokens.
func readTestdata(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run go test -run TestFacetValuesMatchTheCockpit -update", err)
	}
	return []byte(brandtest.Expand(string(raw)))
}
