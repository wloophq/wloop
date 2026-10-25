package web

import (
	"fmt"
	"io"
	"math"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workday"
)

var dashFuncs = append(slices.Clone(lensFuncs), "toggledQualifier", "qualifierToken", "tagKeys", "stamp",
	"dashPalette", "dashSeries", "dashTheme", "dashMix", "dashDim", "dashItem", "dashTally", "dashRanked", "dashBase", "dashCountAxis", "dashBars",
	"dashKpis", "dashWaitOption", "dashPeopleOption", "dashAuthorsOption", "dashReposOption", "dashKindOption", "dashTagsOption",
	"dashShapeOption", "dashWorkingMs", "dashDays", "dashQuantile", "dashRound", "dashAgingOption", "dashNextOption", "dashOpenedOption",
	"dashCostOption", "dashCompact", "dashHas", "dashBlockersOption", "dashLift", "dashLiftOption", "dashAgeBoxOption", "dashClickQuery", "dashEsc")

var testPal = map[string]string{"me": "#000001", "author": "#000002", "reviewer": "#000003", "loop": "#000004", "nobody": "#000005",
	"ink": "#000006", "muted": "#000007", "rule": "#000008", "panel": "#ffffff", "bg": "#000009", "sel": "#00000a", "late": "#00000b", "err": "#00000c", "mine": "#00000d"}

func dashPrelude(t *testing.T) string {
	src := cockpitJS(t)
	var b strings.Builder
	b.WriteString(lensPrelude(t))
	for _, m := range regexp.MustCompile(`(?m)^const DASH_\w+ = .*;$`).FindAllString(src, -1) {
		b.WriteString(m)
		b.WriteString("\n")
	}
	for _, c := range []string{"DASH_KPIS", "DASH_CAUSES"} {
		b.WriteString(jsBlock(t, src, c))
	}
	return b.String()
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// dashRows are a backlog of six open pull requests and one closed, with
// facts on some of them.
func dashRows(now time.Time) []map[string]any {
	day := 24 * time.Hour
	row := func(n int, repo, author, kind, waiting string, waited time.Duration, stalled bool, views []string, brief map[string]any) map[string]any {
		owner, name, _ := strings.Cut(repo, "/")
		r := map[string]any{"ref": map[string]any{"owner": owner, "repo": name, "number": n}, "repo": repo, "author": author, "kind": kind,
			"waiting_on": waiting, "since": iso(now.Add(-waited)), "stalled": stalled, "views": views, "title": fmt.Sprint("t", n)}
		if brief != nil {
			r["brief"] = brief
		}
		return r
	}
	tags := func(size, risk, kind string, tags ...string) map[string]any {
		return map[string]any{"size": size, "risk": risk, "kind": kind, "tags": tags}
	}
	rows := []map[string]any{
		row(1, "acme/api", "ana", "review", "me", 2*day, true, []string{"needs-me", "stalled"}, tags("s", "low", "fix", "area:auth", "lang:go")),
		row(2, "acme/api", "me", "checks", "author", 5*day, true, []string{"mine", "stalled"}, tags("l", "high", "feature", "area:auth", "lang:go")),
		row(3, "acme/web", "bia", "review", "me", 10*time.Hour, false, []string{"needs-me"}, tags("s", "low", "fix", "area:ui", "lang:ts")),
		row(4, "jus/op", "ana", "merge", "author", 1*day, false, []string{"approved"}, tags("m", "medium", "chore", "area:auth", "lang:go")),
		row(5, "jus/op", "bia", "loop_working", "me", 3*time.Hour, false, []string{"loop"}, tags("xs", "low", "fix", "area:ui", "lang:go")),
		row(6, "acme/web", "caio", "conflicts", "author", 8*day, true, []string{"stalled"}, tags("xl", "high", "feature", "area:billing", "lang:ts")),
		row(7, "acme/api", "ana", "done", "nobody", 30*day, false, []string{"done"}, nil),
	}
	rows[3]["approved"] = true
	rows[0]["pending"] = map[string]any{"deadline": iso(now.Add(2 * time.Hour))}
	rows[1]["facts"] = map[string]any{"created_at": iso(now.Add(-10 * day)), "changed_files": 12,
		"checks": map[string]any{"state": "FAILURE", "failing": []string{"lint", "unit"}},
		"turns":  map[string]any{"count": 3, "open": 1, "tokens": 9000, "cost_usd": 1.25},
		"next":   []any{map[string]any{"at": iso(now.Add(30 * time.Hour)), "what": "unmute"}}}
	rows[5]["facts"] = map[string]any{"created_at": iso(now.Add(-40 * day)), "changed_files": 3,
		"checks": map[string]any{"state": "FAILURE", "failing": []string{"lint"}},
		"turns":  map[string]any{"count": 1, "cost_usd": 0.5},
		"next":   []any{map[string]any{"at": iso(now.Add(5 * time.Hour)), "what": "brief_retry"}}}
	rows[2]["facts"] = map[string]any{"created_at": iso(now.Add(-12 * time.Hour)), "checks": map[string]any{"state": "SUCCESS"}}
	return rows
}

// dashRun builds each named option over the backlog of dashRows under the
// query q and returns it as JSON-decoded data.
func dashRun(t *testing.T, q string, rows []map[string]any, now time.Time, main string, out any) {
	t.Helper()
	nodeRun(t, dashPrelude(t), dashFuncs, `const now = `+fmt.Sprint(now.UnixMilli())+`;
const pal = `+mustJS(t, testPal)+`;
const all = `+mustJS(t, wireRows("me", now, rows...))+`;
const lens = { view: "dash", q: input, ts: terms(input), quals: qualifiers(input), filters: filtersOf(qualifiers(input)), tk: "", done: false };
const rows = all.filter((r) => inView(r, "dash", false) && matches(r, lens.ts));
const bars = (o) => o ? Object.fromEntries(o.series[0].data.map((d) => [d.name, d.value])) : null;
`+main, q, out)
}

func TestTheBacklogLeavesDoneOutUnlessAsked(t *testing.T) {
	var got []int
	nodeRun(t, dashPrelude(t), dashFuncs, `const rows = `+mustJS(t, wireRows("me", time.Now(), dashRows(time.Now())...))+`;
process.stdout.write(JSON.stringify([rows.filter((r) => inView(r, "dash", false)).length, rows.filter((r) => inView(r, "dash", true)).length]));`, nil, &got)
	if !slices.Equal(got, []int{6, 7}) {
		t.Fatalf("%v", got)
	}
}

func TestTheBacklogHoldsOnlyPullRequests(t *testing.T) {
	var got []int
	nodeRun(t, dashPrelude(t), dashFuncs, `const rows = `+mustJS(t, wireRows("me", time.Now(), dashRows(time.Now())...))+`;
rows.push({ unit: { provider: "thread", key: "C1/171.1" }, title: "Deploy window?", views: ["threads"], summary: { closed: false } });
process.stdout.write(JSON.stringify([rows.filter((r) => inView(r, "dash", false)).length, rows.filter((r) => inView(r, "dash", true)).length,
  rows.filter((r) => inView(r, "all", false)).length]));`, nil, &got)
	if !slices.Equal(got, []int{6, 7, 8}) {
		t.Fatalf("backlog, backlog with done, all: %v", got)
	}
}

func TestEachPanelCountsIgnoringItsOwnField(t *testing.T) {
	now := time.Now()
	var got struct {
		Wait, Kind, Repo map[string]int
		Authors          map[string]int
	}
	dashRun(t, "wait:me repo:acme/api", dashRows(now), now, `
const repos = dashReposOption(rows, lens, pal).series[0].data.flatMap((o) => o.children.map((c) => [o.name + "/" + c.name, c.value]));
process.stdout.write(JSON.stringify({ Wait: bars(dashWaitOption(rows, lens, pal)), Kind: bars(dashKindOption(rows, lens, pal)),
  Repo: Object.fromEntries(repos), Authors: bars(dashAuthorsOption(rows, lens, pal)) }));`, &got)
	if want := map[string]int{"Waiting on me": 2}; !mapsEqual(got.Wait, want) {
		t.Errorf("wait %v, want %v: the wait panel ignores wait:me but keeps repo:acme/api", got.Wait, want)
	}
	if want := map[string]int{"review": 1, "checks failing": 1}; !mapsEqual(got.Kind, want) {
		t.Errorf("kind %v, want %v", got.Kind, want)
	}
	if want := map[string]int{"acme/api": 2, "acme/web": 1}; !mapsEqual(got.Repo, want) {
		t.Errorf("repo %v, want %v: the repo panel ignores repo: but keeps wait:me", got.Repo, want)
	}
	if want := map[string]int{"ana": 1, "me": 1}; !mapsEqual(got.Authors, want) {
		t.Errorf("authors %v, want %v", got.Authors, want)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestASelectedValueStaysBrightAndTheRestDim(t *testing.T) {
	now := time.Now()
	var got map[string]float64
	dashRun(t, "wait:me", dashRows(now), now, `
process.stdout.write(JSON.stringify(Object.fromEntries(dashWaitOption(rows, lens, pal).series[0].data.map((d) => [d.q[0][1], d.itemStyle.opacity]))));`, &got)
	if got["me"] != 1 || got["author"] >= 1 || got["loop"] >= 1 {
		t.Fatalf("%v", got)
	}
}

func TestTheKpisAgreeWithTheFlagsTheyFilter(t *testing.T) {
	now := time.Now()
	var got []struct {
		ID   string `json:"id"`
		N    int    `json:"n"`
		On   bool   `json:"on"`
		Flag string `json:"flag"`
	}
	rows := dashRows(now)
	rows[4]["stalled"] = true
	dashRun(t, "is:stalled", rows, now, `process.stdout.write(JSON.stringify(dashKpis(rows, lens)));`, &got)
	want := map[string]int{"pending": 3, "needs-me": 2, "stalled": 3, "in-loop": 1, "approved": 1, "due": 2}
	for _, k := range got {
		if k.N != want[k.ID] {
			t.Errorf("%s: %d, want %d", k.ID, k.N, want[k.ID])
		}
		if k.On != (k.ID == "stalled") {
			t.Errorf("%s: on %v", k.ID, k.On)
		}
	}
}

func TestTagConcentrationNestsValuesUnderTheirKey(t *testing.T) {
	now := time.Now()
	var got struct {
		Tree map[string]map[string]int
		Lang map[string]int
		Key  [][]string
	}
	dashRun(t, "tag:lang:go", dashRows(now), now, `
const tree = dashTagsOption(rows, lens, pal, "").series[0].data;
process.stdout.write(JSON.stringify({
  Tree: Object.fromEntries(tree.map((k) => [k.name, Object.fromEntries(k.children.map((c) => [c.name, c.value]))])),
  Lang: bars(dashTagsOption(rows, lens, pal, "lang")),
  Key: tree.find((k) => k.name === "area").q,
}));`, &got)
	if want := map[string]int{"auth": 3, "ui": 2, "billing": 1}; !mapsEqual(got.Tree["area"], want) {
		t.Errorf("area %v, want %v: the tag panel ignores its own tag: filter", got.Tree["area"], want)
	}
	if want := map[string]int{"go": 4, "ts": 2}; !mapsEqual(got.Lang, want) {
		t.Errorf("lang %v, want %v", got.Lang, want)
	}
	if len(got.Key) != 1 || got.Key[0][0] != "tag" || got.Key[0][1] != "area" {
		t.Errorf("a key node filters by the key: %v", got.Key)
	}
}

func TestTagLiftComparesTheStalledWithTheBacklog(t *testing.T) {
	now := time.Now()
	var got struct {
		Lift []struct {
			Tag     string `json:"tag"`
			N       int    `json:"n"`
			Stalled int    `json:"stalled"`
			Lift    int    `json:"lift"`
		}
		Q [][]string
	}
	dashRun(t, "is:stalled", dashRows(now), now, `
process.stdout.write(JSON.stringify({ Lift: dashLift(rows, lens), Q: dashLiftOption(rows, lens, pal).series[0].data[0].q }));`, &got)
	want := map[string][3]int{"area:auth": {3, 2, 33}, "lang:go": {4, 2, 0}}
	if len(got.Lift) != len(want) {
		t.Fatalf("only tags on %d or more pull requests: %+v", 3, got.Lift)
	}
	for _, l := range got.Lift {
		w := want[l.Tag]
		if l.N != w[0] || l.Stalled != w[1] || l.Lift != w[2] {
			t.Errorf("%+v, want n %d, stalled %d, lift %d%%", l, w[0], w[1], w[2])
		}
	}
	if got.Lift[0].Tag != "area:auth" {
		t.Errorf("most over-represented first: %+v", got.Lift)
	}
	if !slices.EqualFunc(got.Q, [][]string{{"tag", "area:auth"}, {"is", "stalled"}}, slices.Equal) {
		t.Errorf("a lift bar filters by the tag among the stalled: %v", got.Q)
	}
}

func TestTheKpisCountWhatTheirTabsShow(t *testing.T) {
	now := time.Now()
	rows := dashRows(now)
	rows[4]["stalled"] = true
	var got map[string]int
	dashRun(t, "", rows, now, `
const tabs = { "needs-me": "needs-me", stalled: "stalled", "in-loop": "loop", approved: "approved" };
const out = {};
for (const k of dashKpis(rows, lens)) if (tabs[k.id]) out[k.id] = k.n - all.filter((r) => inView(r, tabs[k.id])).length;
process.stdout.write(JSON.stringify(out));`, &got)
	if len(got) != 4 {
		t.Fatalf("%v", got)
	}
	for id, diff := range got {
		if diff != 0 {
			t.Errorf("KPI %s differs from its tab by %d: an out-of-scope stalled row is not stalled", id, diff)
		}
	}
}

func TestWorkingMsLeavesWeekendsOut(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	spans := [][2]time.Time{
		{at("2026-10-14T09:00:00Z"), at("2026-10-14T17:30:00Z")},
		{at("2026-10-16T12:00:00Z"), at("2026-10-19T12:00:00Z")},
		{at("2026-10-17T03:00:00Z"), at("2026-10-18T22:00:00Z")},
		{at("2026-10-09T18:00:00Z"), at("2026-10-21T06:00:00Z")},
		{at("2026-10-20T00:00:00-03:00"), at("2026-10-19T00:00:00Z")},
	}
	var in [][2]int64
	for _, s := range spans {
		in = append(in, [2]int64{s[0].UnixMilli(), s[1].UnixMilli()})
	}
	var got []int64
	nodeRun(t, "", []string{"dashWorkingMs"}, `process.stdout.write(JSON.stringify(input.map(([a, b]) => dashWorkingMs(a, b))));`, in, &got)
	want := []int64{(8*time.Hour + 30*time.Minute).Milliseconds(), 24 * time.Hour.Milliseconds(), 0, (7*24 + 12) * time.Hour.Milliseconds(), 0}
	for i, s := range spans {
		if w := workday.Between(s[0], s[1]).Milliseconds(); w != want[i] {
			t.Fatalf("case %d: Go says %d, the test wants %d", i, w, want[i])
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}

func TestAgingPlotsEachPullRequestByStageAndWait(t *testing.T) {
	now := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
	var got struct {
		Kinds  []string
		Points []struct {
			Value []float64 `json:"value"`
			PR    string    `json:"pr"`
		}
		Lines map[string]float64
	}
	dashRun(t, "", dashRows(now), now, `
const o = dashAgingOption(rows, lens, pal, now, 48);
process.stdout.write(JSON.stringify({ Kinds: o.xAxis[0].data, Points: o.series[0].data,
  Lines: Object.fromEntries(o.series[0].markLine.data.map((l) => [l.name, l.yAxis])) }));`, &got)
	if want := []string{"agent working", "checks failing", "merge conflicts", "review", "can merge"}; !slices.Equal(got.Kinds, want) {
		t.Errorf("stages %v, want %v in the flow's order", got.Kinds, want)
	}
	days := map[string]float64{}
	for _, r := range dashRows(now) {
		ref := r["ref"].(map[string]any)
		since, _ := time.Parse(time.RFC3339, r["since"].(string))
		days[fmt.Sprint("github:", ref["owner"], "/", ref["repo"], "#", ref["number"])] = max(1.0/24, workday.Between(since, now).Hours()/24)
	}
	delete(days, "github:acme/api#7")
	if len(got.Points) != len(days) {
		t.Fatalf("%d points, want %d", len(got.Points), len(days))
	}
	for _, p := range got.Points {
		if math.Abs(p.Value[1]-days[p.PR]) > 0.02 {
			t.Errorf("%s waits %v days, want %v", p.PR, p.Value[1], days[p.PR])
		}
	}
	if days["github:acme/api#1"] != 0.625 || days["github:acme/web#6"] != 5.625 {
		t.Fatalf("a Monday afternoon should leave the weekend out: %v", days)
	}
	if got.Lines["stalled"] != 2 || math.Abs(got.Lines["p50"]-0.625) > 0.02 || got.Lines["p85"] <= got.Lines["p50"] {
		t.Errorf("lines %v", got.Lines)
	}
}

func TestPanelsOnFactsStayQuietWithoutThem(t *testing.T) {
	now := time.Now()
	rows := dashRows(now)
	for _, r := range rows {
		delete(r, "facts")
		delete(r, "pending")
	}
	var got map[string]any
	dashRun(t, "", rows, now, `
process.stdout.write(JSON.stringify({ next: dashNextOption(rows, lens, pal, now, null), opened: dashOpenedOption(rows, lens, pal),
  cost: dashCostOption(rows, lens, pal), checks: dashBlockersOption(rows, lens, pal).series[1].data.length }));`, &got)
	if got["next"] != nil || got["opened"] != nil || got["cost"] != nil || got["checks"] != float64(0) {
		t.Fatalf("%v", got)
	}
}

func TestPanelsOnFactsReadTheContract(t *testing.T) {
	now := time.Now()
	var got struct {
		Next    [][]any
		Opened  map[string]int
		Load    map[string]int
		Spend   map[string]float64
		Causes  map[string]int
		Failing map[string]int
		CI      []int
	}
	dashRun(t, "", dashRows(now), now, `
const next = dashNextOption(rows, lens, pal, now, { brief: { paused_until: new Date(now + 3600000).toISOString() } });
const cost = dashCostOption(rows, lens, pal);
const blockers = dashBlockersOption(rows, lens, pal);
const pick = (s) => Object.fromEntries(s.data.map((d) => [d.name, d.value]));
const ci = (q) => { const l = { ...lens, filters: filtersOf(qualifiers(q)) }; return rows.filter((r) => passesFilters(r, l.filters)).map((r) => r.ref.number); };
process.stdout.write(JSON.stringify({
  Next: next.series[0].data.map((d) => [d.name, next.yAxis.data[d.value[1]]]),
  Opened: bars(dashOpenedOption(rows, lens, pal)),
  Load: pick(cost.series[0]), Spend: pick(cost.series[1]),
  Causes: pick(blockers.series[0]), Failing: pick(blockers.series[1]),
  CI: [...ci("ci:failure"), 0, ...ci("ci:lint"), 0, ...ci("opened:older")],
}));`, &got)
	wantNext := map[string]string{"acme/api#1": "Round sends", "acme/api#2": "Mute ends", "acme/web#6": "Brief retry", "brief quota": "Brief quota resets"}
	if len(got.Next) != len(wantNext) {
		t.Errorf("next %v", got.Next)
	}
	for _, e := range got.Next {
		if wantNext[e[0].(string)] != e[1] {
			t.Errorf("next %v", e)
		}
	}
	if want := map[string]int{"0-1 days": 1, "1-2 weeks": 1, "over 4 weeks": 1}; !mapsEqual(got.Opened, want) {
		t.Errorf("opened %v, want %v", got.Opened, want)
	}
	if want := map[string]int{"Waiting on me": 12, "Waiting on the author": 3}; !mapsEqual(got.Load, want) {
		t.Errorf("load %v, want %v", got.Load, want)
	}
	if got.Spend["acme/api#2"] != 1.25 || got.Spend["acme/web#6"] != 0.5 || len(got.Spend) != 2 {
		t.Errorf("spend %v", got.Spend)
	}
	if want := map[string]int{"CI failing": 2, "Merge conflicts": 1}; !mapsEqual(got.Causes, want) {
		t.Errorf("causes %v, want %v", got.Causes, want)
	}
	if want := map[string]int{"lint": 2, "unit": 1}; !mapsEqual(got.Failing, want) {
		t.Errorf("failing %v, want %v", got.Failing, want)
	}
	if want := []int{2, 6, 0, 2, 6, 0, 6}; !slices.Equal(got.CI, want) {
		t.Errorf("ci: and opened: filter %v, want %v", got.CI, want)
	}
}

func TestShapeAndAgeBoxesFilterBySizeAndRisk(t *testing.T) {
	now := time.Now()
	var got struct {
		Cells [][]any
		Boxes map[string][]float64
		BoxQ  [][][]string
	}
	dashRun(t, "size:s", dashRows(now), now, `
const shape = dashShapeOption(rows, lens, pal);
const box = dashAgeBoxOption(rows, lens, pal, now);
process.stdout.write(JSON.stringify({
  Cells: shape.series[0].data.map((d) => [d.q, d.value[2], d.itemStyle.opacity]),
  Boxes: Object.fromEntries(box.series[0].data.map((d) => [d.name, d.value])),
  BoxQ: box.series[0].data.map((d) => d.q),
}));`, &got)
	cells := map[string]float64{}
	for _, c := range got.Cells {
		q := c[0].([]any)
		key := fmt.Sprint(q[0].([]any)[1], "/", q[1].([]any)[1])
		cells[key] = c[1].(float64)
		if (key == "s/low") != (c[2].(float64) == 1) {
			t.Errorf("cell %s opacity %v", key, c[2])
		}
	}
	if cells["s/low"] != 2 || cells["l/high"] != 1 || cells["xl/high"] != 1 || len(cells) != 5 {
		t.Errorf("the heatmap ignores size: and risk: filters %v", cells)
	}
	if b := got.Boxes["size S"]; len(b) != 5 || b[0] > b[2] || b[2] > b[4] {
		t.Errorf("box %v", got.Boxes)
	}
	if _, ok := got.Boxes["high risk"]; ok {
		t.Errorf("risk boxes keep size:s, no high-risk S: %v", got.Boxes)
	}
}

func TestAClickWritesTheQuery(t *testing.T) {
	cases := []struct {
		q     string
		pairs [][]string
		neg   bool
		want  string
	}{
		{"", [][]string{{"wait", "me"}}, false, "wait:me"},
		{"wait:me", [][]string{{"wait", "me"}}, false, ""},
		{"wait:me", [][]string{{"wait", "author"}}, false, "wait:me wait:author"},
		{"wait:me", [][]string{{"wait", "me"}}, true, "-wait:me"},
		{"fix", [][]string{{"waits-on", "team:acme/core"}}, false, "fix waits-on:team:acme/core"},
		{"", [][]string{{"size", "s"}, {"risk", "low"}}, false, "size:s risk:low"},
		{"size:s", [][]string{{"size", "s"}, {"risk", "low"}}, false, "size:s risk:low"},
		{"size:s risk:low repo:x", [][]string{{"size", "s"}, {"risk", "low"}}, false, "repo:x"},
		{"", [][]string{{"tag", "area:auth"}, {"is", "stalled"}}, false, "tag:area:auth is:stalled"},
		{"", [][]string{{"ci", "unit tests"}}, false, `ci:"unit tests"`},
		{"", [][]string{{"opened", "2w"}}, true, "-opened:2w"},
	}
	var got []string
	nodeRun(t, dashPrelude(t), dashFuncs, `process.stdout.write(JSON.stringify(input.map((c) => dashClickQuery(c.q, c.pairs, c.neg))));`,
		func() []map[string]any {
			var in []map[string]any
			for _, c := range cases {
				in = append(in, map[string]any{"q": c.q, "pairs": c.pairs, "neg": c.neg})
			}
			return in
		}(), &got)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("click %v on %q (neg %v): %q, want %q", c.pairs, c.q, c.neg, got[i], c.want)
		}
	}
}

func TestEveryClickableDatumNamesAFilterField(t *testing.T) {
	now := time.Now()
	var got [][]string
	dashRun(t, "", dashRows(now), now, `
const opts = [dashWaitOption, dashPeopleOption, dashAuthorsOption, dashReposOption, dashKindOption, dashShapeOption,
  dashOpenedOption, dashCostOption, dashBlockersOption, dashLiftOption, dashAgeBoxOption].map((f) => f(rows, lens, pal, now))
  .concat([dashTagsOption(rows, lens, pal, ""), dashTagsOption(rows, lens, pal, "area")]);
const qs = [];
const walk = (d) => { if (d.q) qs.push(...d.q.map((p) => p.join(":"))); for (const c of d.children || []) walk(c); };
for (const o of opts) for (const s of o.series) for (const d of s.data) walk(d);
process.stdout.write(JSON.stringify(qs.map((p) => { const x = qualifierOf(p.includes(" ") ? p.replace(/:(.*)$/, ':"$1"') : p); return [p, x ? x.field : ""]; })));`, &got)
	if len(got) < 20 {
		t.Fatalf("only %d clickable data", len(got))
	}
	for _, g := range got {
		if g[1] == "" || !strings.HasPrefix(g[0], g[1]+":") {
			t.Errorf("%s does not read back as a filter", g[0])
		}
	}
}

func TestTheChartThemeComesFromTheCSSTokens(t *testing.T) {
	light, dark := themeTokens(t)
	for name, tokens := range map[string]map[string]string{"light": light, "dark": dark} {
		var got struct {
			Pal   map[string]string
			Color []string
		}
		nodeRun(t, "", []string{"dashPalette", "dashSeries", "dashTheme"}, `const css = `+mustJS(t, tokens)+`;
const pal = dashPalette((n) => " " + (css[n.slice(2)] || "") + " ");
process.stdout.write(JSON.stringify({ Pal: pal, Color: dashTheme(pal).color }));`, nil, &got)
		for k, v := range got.Pal {
			if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(v) {
				t.Errorf("%s: %s is %q, not a token", name, k, v)
			}
		}
		if got.Pal["me"] != tokens["me"] || got.Pal["nobody"] != tokens["none"] || len(got.Color) < 5 {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestCockpitHasTheBacklogDashboard(t *testing.T) {
	h := Handler("")
	get := func(path string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
	index := get("/")
	for _, want := range []string{`id="dash"`, `id="list-box"`, `/static/vendor/echarts.min.js`, `/static/dash.js`} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if strings.Index(index, "echarts.min.js") > strings.Index(index, "dash.js") || strings.Index(index, "dash.js") > strings.Index(index, "app.js") {
		t.Error("scripts load out of order")
	}
	lib := get("/static/vendor/echarts.min.js")
	if !strings.Contains(lib[:2000], "Licensed to the Apache Software Foundation") || !strings.Contains(lib, `"5.6.0"`) {
		t.Error("vendor/echarts.min.js is not Apache ECharts 5.6.0 with its license header")
	}
	if !strings.Contains(get("/static/dash.js"), `renderer: "svg"`) {
		t.Error("dash.js does not draw SVG")
	}
}

func TestSpendFallsBackToTokensWithoutACost(t *testing.T) {
	now := time.Now()
	rows := dashRows(now)
	rows[1]["facts"].(map[string]any)["turns"] = map[string]any{"count": 3, "tokens": 90000, "cost_usd": 0}
	rows[5]["facts"].(map[string]any)["turns"] = map[string]any{"count": 1, "tokens": 4000}
	var got struct {
		Title string
		Spend map[string]float64
	}
	dashRun(t, "", rows, now, `
const cost = dashCostOption(rows, lens, pal);
process.stdout.write(JSON.stringify({ Title: cost.title[1].text, Spend: Object.fromEntries(cost.series[1].data.map((d) => [d.name, d.value])) }));`, &got)
	if got.Title != "Tokens per pull request\nno US$ cost recorded" {
		t.Errorf("title %q", got.Title)
	}
	if got.Spend["acme/api#2"] != 90000 || got.Spend["acme/web#6"] != 4000 || len(got.Spend) != 2 {
		t.Errorf("spend %v", got.Spend)
	}
}
