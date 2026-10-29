package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
)

func appJS(t *testing.T) string {
	t.Helper()
	raw, err := fs.ReadFile(files, "static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(render(raw))
}

// cockpitJS is app.js and dash.js, the scripts whose functions tests run.
func cockpitJS(t *testing.T) string {
	t.Helper()
	raw, err := fs.ReadFile(files, "static/dash.js")
	if err != nil {
		t.Fatal(err)
	}
	return appJS(t) + "\n" + string(render(raw))
}

// jsFunction is the source of a top-level function in app.js.
func jsFunction(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "\nfunction "+name+"(")
	if start < 0 {
		start = strings.Index(src, "\nasync function "+name+"(")
	}
	if start < 0 {
		t.Fatalf("app.js has no function %s", name)
	}
	end := strings.Index(src[start:], "\n}\n")
	return src[start : start+end+3]
}

var stalledKinds = []string{"draft", "checks", "changes", "merge", "reply", "reviewer_back", "review", "no_reviewer", "conflicts"}

// nodeRun runs app.js's named functions with prelude, then main, which
// writes JSON to stdout; in is its stdin.
func nodeRun(t *testing.T, prelude string, funcs []string, main string, in, out any) {
	t.Helper()
	src := cockpitJS(t)
	var script strings.Builder
	script.WriteString(prelude)
	for _, f := range funcs {
		script.WriteString(jsFunction(t, src, f))
	}
	script.WriteString("\n")
	script.WriteString(main)
	nodeScript(t, script.String(), in, out)
}

// jsConst is a one-line top-level const declaration in app.js.
func jsConst(t *testing.T, src, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^const ` + name + ` = .*;$`).FindString(src)
	if m == "" {
		t.Fatalf("app.js has no one-line const %s", name)
	}
	return m + "\n"
}

// keyJS is app.js's naming of rows and pull requests: keys, labels and unit
// paths.
func keyJS(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	for _, c := range []string{"refLabel", "unitKey", "keyUnit", "unitPath", "unitOf", "keyOf", "labelOf", "labelTail"} {
		b.WriteString(jsConst(t, src, c))
	}
	return b.String()
}

// jsBlock is a multi-line top-level const array or object in app.js.
func jsBlock(t *testing.T, src, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\nconst ` + name + ` = [\[{].*?\n[\]}];\n`).FindString(src)
	if m == "" {
		t.Fatalf("app.js has no const block %s", name)
	}
	return m
}

// fieldsPrelude builds the lens's fields from the served catalog.
func fieldsPrelude(t *testing.T) string {
	return catalogPrelude(t)
}

func filterPrelude(t *testing.T) string {
	src := appJS(t)
	var b strings.Builder
	b.WriteString("const folded = new WeakMap();\n")
	b.WriteString(`function h(tag, attrs, ...c) { return { [tag]: c.join("") }; }` + "\n")
	for _, c := range []string{"UNIT_KEY", "PR_URL"} {
		b.WriteString(jsConst(t, src, c))
	}
	b.WriteString(keyJS(t))
	b.WriteString(fieldsPrelude(t))
	return b.String()
}

var queryFuncs = []string{"fold", "tokens", "unquote", "terms", "fieldById", "normValue", "qualifierOf", "qualifiers"}

var filterFuncs = append(slices.Clone(queryFuncs), "haystack", "matches", "foldMap", "marked", "rowByURL")

func TestFilterFoldsAndMarks(t *testing.T) {
	cases := [][2]string{
		{"Exportação de dados", "exportacao"},
		{"Expo\u0301rt now", "export"},
		{"Expo\u0301rt now", "expo"},
		{"batch export", "batch atch exp"},
		{"batch", "bat ch"},
		{"🚀 launch", "launch"},
		{"🚀 launch", "🚀"},
		{"<b>bold</b> fix", "fix"},
		{"nothing here", "zzz"},
	}
	var got []any
	nodeRun(t, filterPrelude(t), filterFuncs,
		`process.stdout.write(JSON.stringify(input.map(([text, q]) => marked(text, terms(q)))));`, cases, &got)
	want := []any{
		[]any{map[string]any{"mark": "Exportação"}, " de dados"},
		[]any{map[string]any{"mark": "Expo\u0301rt"}, " now"},
		[]any{map[string]any{"mark": "Expo\u0301"}, "rt now"},
		[]any{map[string]any{"mark": "batch"}, " ", map[string]any{"mark": "exp"}, "ort"},
		[]any{map[string]any{"mark": "batch"}},
		[]any{"🚀 ", map[string]any{"mark": "launch"}},
		[]any{map[string]any{"mark": "🚀"}, " launch"},
		[]any{"<b>bold</b> ", map[string]any{"mark": "fix"}},
		"nothing here",
	}
	for i := range cases {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("marked(%q, %q) = %#v, want %#v", cases[i][0], cases[i][1], got[i], want[i])
		}
	}
}

func TestFilterMatchesEveryTermAndAPastedLinkFindsItsRow(t *testing.T) {
	var got struct {
		Terms   []string `json:"terms"`
		Matches []bool   `json:"matches"`
		Keys    []string `json:"keys"`
	}
	pull := wireRow("me", time.Now(), map[string]any{"title": "Batch export", "label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "repo": "acme/api", "author": "ana", "head": "ana/batch-x", "waiting_on": "bob"})
	pull["link"] = "https://github.com/acme/api/pull/7"
	issue := map[string]any{"unit": map[string]any{"provider": "linear", "key": "PAY-88"}, "label": "PAY-88", "title": "Retry budget", "link": "https://linear.app/acme/issue/PAY-88"}
	in := map[string]any{
		"rows": []any{pull, issue},
		"qs":   []string{"batch ana", "BATCH zoe", "api#7", "", "batch-x", "exportacao"},
		"urls": []string{"https://github.com/acme/api/pull/7/files", " https://github.com/acme/api/pull/7#discussion_r1 ",
			"https://github.com.evil.com/acme/api/pull/7", "https://evil.com/github.com/acme/api/pull/7", "http://github.com/acme/api/pull/7",
			"https://github.com/acme/../pull/7", "https://github.com/acme/api/pull/70", "acme/api#7",
			"https://linear.app/acme/issue/PAY-88/retry-budget", "https://linear.app/acme/issue/PAY-8"},
	}
	nodeRun(t, filterPrelude(t)+"const state = { rows: input.rows };\n", filterFuncs, `process.stdout.write(JSON.stringify({
  terms: terms("  Exportação  ANA "),
  matches: input.qs.map((q) => matches(input.rows[0], terms(q))),
  keys: input.urls.map((u) => { const r = rowByURL(u); return r ? keyOf(r) : ""; }),
}));`, in, &got)
	if !slices.Equal(got.Terms, []string{"exportacao", "ana"}) {
		t.Errorf("terms %q", got.Terms)
	}
	if !slices.Equal(got.Matches, []bool{true, false, true, true, true, false}) {
		t.Errorf("matches %v", got.Matches)
	}
	if want := []string{"github:acme/api#7", "github:acme/api#7", "", "", "", "", "", "", "linear:PAY-88", ""}; !slices.Equal(got.Keys, want) {
		t.Errorf("keys %q, want %q", got.Keys, want)
	}
}

func TestEmptyNotesNameTheFilter(t *testing.T) {
	prelude := filterPrelude(t) + `let activityLoopOnly = false;
const state = { loaded: true, rows: [], activityLoaded: true, activity: [{ event: { kind: "push" } }] };
`
	var got [][]any
	nodeRun(t, prelude, append(slices.Clone(filterFuncs), "rowByKey", "emptyNote", "activityEmptyNote"), `process.stdout.write(JSON.stringify([
  activityEmptyNote("zzz"),
  activityEmptyNote("https://github.com/acme/web/pull/3"),
  (activityLoopOnly = true, activityEmptyNote("")),
  (activityLoopOnly = false, state.activity = [], activityEmptyNote("zzz")),
  emptyNote("stalled", "zzz"),
  emptyNote("stalled", "waits-on:bob zzz"),
  emptyNote("stalled", ""),
]));`, nil, &got)
	want := [][]any{
		{`Nothing in Recent matches "zzz".`},
		{"acme/web#3 is not in the inventory. ", map[string]any{"button": "Explore it"}, " ", map[string]any{"a": "Open it on GitHub"}},
		{"No loop events in the recent activity."},
		{"Nothing happened in the inventory yet."},
		{`Nothing in Stalled matches "zzz".`},
		{`Nothing in Stalled matches "waits-on:bob zzz".`, " ", map[string]any{"button": "Clear filters"}},
		{"Nothing in Stalled right now."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestExploreChipsFollowTheText(t *testing.T) {
	var got []any
	nodeRun(t, "", []string{"tokens", "hasTerm", "toggledQuery"}, `process.stdout.write(JSON.stringify([
  toggledQuery("org:acme", "draft:false"),
  toggledQuery("org:acme  draft:false label:x", "draft:false"),
  toggledQuery("", "-reviewed-by:@me"),
  hasTerm("org:acme draft:false", "draft:false"),
  hasTerm("org:acme draft:falsey", "draft:false"),
]));`, nil, &got)
	want := []any{"org:acme draft:false", "org:acme label:x", "-reviewed-by:@me", true, false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExploreSortChipsFollowTheText(t *testing.T) {
	var got []any
	nodeRun(t, catalogPrelude(t), []string{"querySort", "withSort"}, `process.stdout.write(JSON.stringify([
  withSort("org:acme", "created-asc"),
  withSort("org:acme  sort:created-asc label:x", "updated-desc"),
  withSort("sort:updated-desc @zoe sort:created-asc", "updated-asc"),
  withSort("", "updated-asc"),
  querySort("org:acme"),
  querySort("org:acme sort:created-asc"),
  querySort("sort:created-asc org:acme sort:updated-desc"),
  querySort("label:resort:x"),
]));`, nil, &got)
	want := []any{"org:acme sort:created-asc", "org:acme label:x sort:updated-desc", "@zoe sort:updated-asc", "sort:updated-asc",
		"updated-asc", "created-asc", "updated-desc", "updated-asc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTheActivityWindowChipFollowsTheText(t *testing.T) {
	var got []any
	nodeRun(t, "", []string{"windowDate", "hasWindow", "withWindow"}, `const today = new Date(2026, 9, 15, 23, 30);
process.stdout.write(JSON.stringify([
  withWindow("org:acme", today, 30),
  withWindow("org:acme updated:>=2026-01-01 label:x", today, 30),
  withWindow("org:acme  updated:>=2026-01-01 sort:created-asc", today, 0),
  withWindow("", new Date(2026, 2, 10), 30),
  withWindow("updated:>2026-01-01", today, 30),
  hasWindow("org:acme updated:>=2026-09-15"),
  hasWindow("org:acme updated:>2026-09-15"),
  hasWindow("org:acme"),
]));`, nil, &got)
	want := []any{"org:acme updated:>=2026-09-15", "org:acme label:x updated:>=2026-09-15", "org:acme sort:created-asc", "updated:>=2026-02-08",
		"updated:>2026-01-01 updated:>=2026-09-15", true, false, false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestCandidatesSayHowLongTheyWait(t *testing.T) {
	var got []any
	nodeRun(t, "", []string{"isSet", "sm", "waited", "candidateWait"}, `const now = Date.parse("2026-10-15T12:00:00Z");
const ago = (h) => new Date(now - h * 3600000).toISOString();
process.stdout.write(JSON.stringify([
  candidateWait({ updated_at: ago(0.5) }, null, 48, now),
  candidateWait({ updated_at: ago(5) }, null, 48, now),
  candidateWait({ updated_at: ago(47) }, null, 48, now),
  candidateWait({ updated_at: ago(49) }, null, 48, now),
  candidateWait({ updated_at: ago(80) }, null, 72, now),
  candidateWait({ updated_at: ago(30) }, null, 0, now),
  candidateWait({ updated_at: ago(80) }, { summary: { since: ago(3), stalled: false } }, 48, now),
  candidateWait({ updated_at: ago(1) }, { summary: { since: ago(30), stalled: true } }, 48, now),
  candidateWait({ updated_at: ago(60) }, { summary: { since: "0001-01-01T00:00:00Z" } }, 48, now),
  candidateWait({ updated_at: "0001-01-01T00:00:00Z" }, null, 48, now),
]));`, nil, &got)
	w := func(text string, late bool) map[string]any { return map[string]any{"text": text, "late": late} }
	want := []any{w("waiting 30m", false), w("waiting 5h", false), w("waiting 47h", false), w("waiting 2d", true), w("waiting 3d", true),
		w("waiting 30h", false), w("waiting 3h", false), w("waiting 30h", true), w("waiting 2d", true), nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestTheFilterFindsKindsByTheirLabel(t *testing.T) {
	var got []bool
	nodeRun(t, filterPrelude(t), filterFuncs, `process.stdout.write(JSON.stringify(input.map((q) => matches({ title: "t", label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, kind: "conflicts" }, terms(q)))));`,
		[]string{"merge conflicts", "conflicts", "can merge"}, &got)
	if !slices.Equal(got, []bool{true, true, false}) {
		t.Errorf("got %v", got)
	}
}

func TestRowsShowTheBriefSizeRiskAndQuestions(t *testing.T) {
	prelude := `function h(tag, attrs, ...c) { return { tag, attrs, children: c.flat().filter((x) => x !== null && x !== undefined && x !== false) }; }` + "\n" +
		jsConst(t, appJS(t), "SIZES") + jsConst(t, appJS(t), "RISKS") + jsConst(t, appJS(t), "TAG_CHIPS")
	var got []any
	nodeRun(t, prelude, []string{"tagChips", "briefTags"}, `process.stdout.write(JSON.stringify(input.map((b) => briefTags(b))));`, []any{
		map[string]any{"size": "m", "risk": "high", "goal": "g", "open_questions": 2},
		map[string]any{"size": "xs", "risk": "low", "open_questions": 1},
		map[string]any{"size": "huge", "risk": "x\" onmouseover=\"y", "open_questions": 0},
		nil,
		map[string]any{"size": "s", "tags": []string{"area:api", "change:fix", "domain:billing", "lang:go", "lang:sql"}},
		map[string]any{"tags": []string{"lang:go"}},
	}, &got)
	pill := func(class, text string, title ...string) map[string]any {
		attrs := map[string]any{"class": class}
		if len(title) > 0 {
			attrs["title"] = title[0]
		}
		return map[string]any{"tag": "span", "attrs": attrs, "children": []any{text}}
	}
	want := []any{
		map[string]any{"tag": "span", "attrs": map[string]any{"class": "btags"}, "children": []any{
			pill("pill", "M", "size m"), pill("pill risk-high", "risk high"), pill("pill qs", "? 2", "2 open questions in the brief")}},
		map[string]any{"tag": "span", "attrs": map[string]any{"class": "btags"}, "children": []any{
			pill("pill", "XS", "size xs"), pill("pill risk-low", "risk low"), pill("pill qs", "? 1", "1 open question in the brief")}},
		nil,
		nil,
		map[string]any{"tag": "span", "attrs": map[string]any{"class": "btags"}, "children": []any{
			pill("pill", "S", "size s"), pill("pill tag", "area:api"), pill("pill tag", "change:fix"), pill("pill tag", "domain:billing"),
			pill("pill tag", "+2", "lang:go, lang:sql")}},
		map[string]any{"tag": "span", "attrs": map[string]any{"class": "btags"}, "children": []any{pill("pill tag", "lang:go")}},
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("briefTags %d = %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

func TestTheFilterFindsTheBriefGoal(t *testing.T) {
	var got []bool
	row := wireRow("me", time.Now(), map[string]any{"title": "Batch export", "label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "brief": map[string]any{"goal": "Retry the webhook delivery"}})
	nodeRun(t, filterPrelude(t), filterFuncs,
		`process.stdout.write(JSON.stringify(["webhook", "batch delivery", "zebra"].map((q) => matches(input, terms(q)))));`, row, &got)
	if !slices.Equal(got, []bool{true, true, false}) {
		t.Fatalf("%v", got)
	}
}

func TestAReviewRequestIsReadyForSlack(t *testing.T) {
	in := []map[string]any{
		{"row": map[string]any{"label": "acme/widgets#4877", "unit": map[string]any{"provider": "github", "key": "acme/widgets#4877"}, "link": "https://github.com/acme/widgets/pull/4877", "title": "perf(queue): refill <each> free slot"},
			"brief": map[string]any{"brief": map[string]any{"goal": "Give each free slot the next message.", "context": "The loop asked for the whole batch.", "risk_why": "Affects every pod & the dispatch job."}}},
		{"row": map[string]any{"label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "link": "javascript:alert(1)", "title": "fix: x"}},
	}
	var got []map[string]string
	nodeRun(t, keyJS(t), []string{"safeURL", "escapeHTML", "reviewRequest"},
		`process.stdout.write(JSON.stringify(input.map((x) => reviewRequest(x.row, x.brief))));`, in, &got)
	wantText := "review, pls? `perf(queue): refill <each> free slot`\nhttps://github.com/acme/widgets/pull/4877\n\n*Goal:*\nGive each free slot the next message.\n\n*Context:*\nThe loop asked for the whole batch.\n\n*Risk:*\nAffects every pod & the dispatch job."
	if got[0]["text"] != wantText {
		t.Errorf("text:\n%s\nwant:\n%s", got[0]["text"], wantText)
	}
	wantHTML := `review, pls? <code>perf(queue): refill &lt;each&gt; free slot</code><br><a href="https://github.com/acme/widgets/pull/4877">acme/widgets#4877</a><br><br><b>Goal:</b><br>Give each free slot the next message.<br><br><b>Context:</b><br>The loop asked for the whole batch.<br><br><b>Risk:</b><br>Affects every pod &amp; the dispatch job.`
	if got[0]["html"] != wantHTML {
		t.Errorf("html:\n%s\nwant:\n%s", got[0]["html"], wantHTML)
	}
	if got[1]["text"] != "review, pls? `fix: x`\nacme/api#7" || strings.Contains(got[1]["html"], "javascript") || !strings.Contains(got[1]["html"], "acme/api#7") {
		t.Errorf("without a brief or a safe URL: %q / %q", got[1]["text"], got[1]["html"])
	}
}

func TestExplorePresetsAskForMyTeamsInOneClick(t *testing.T) {
	var got []map[string]string
	nodeRun(t, catalogPrelude(t), nil, `process.stdout.write(JSON.stringify(exploreSearch().presets));`, nil, &got)
	want := []map[string]string{
		{"query": "user-review-requested:@me", "label": "Asked of me"},
		{"query": "review-requested:@me -user-review-requested:@me", "label": "Asked of my teams"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("presets %v, want %v", got, want)
	}
	src := appJS(t)
	for _, want := range []string{"(exploreSearch().presets || []).map((p) =>", "onclick: () => explore(p.query)"} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
}

func TestAnExploreActionBringsThePullRequestInAndSaysHowItWent(t *testing.T) {
	prelude := keyJS(t) + `
const state = { rows: [], actionError: "", explore: { status: {} } };
const calls = [];
const rowByKey = (k) => state.rows.find((r) => keyOf(r) === k);
const render = () => {};
async function load() { if (calls.includes("track")) state.rows = [{ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" }, reasons: ["tracked"] }]; }
async function act(row, name) { calls.push(name); if (name === "review" && input.failReview) { state.actionError = "review acme/api#7 failed: boom"; return false; } return true; }
`
	var got []map[string]any
	nodeRun(t, prelude, []string{"actOnCandidate"},
		`(async () => { const c = { label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" } }; const ok = await actOnCandidate(c, "review", {}); process.stdout.write(JSON.stringify([{ ok, calls, status: state.explore.status["github:acme/api#7"] }])); })();`,
		map[string]any{"failReview": false}, &got)
	if got[0]["ok"] != true || fmt.Sprint(got[0]["calls"]) != "[track review]" {
		t.Fatalf("%v", got[0])
	}
	st := got[0]["status"].(map[string]any)
	if st["text"] != "review started; it is in "+brand.Name+" now" || st["done"] != true {
		t.Errorf("status %v", st)
	}
}

func TestACopiedListIsYAMLAnAgentCanRead(t *testing.T) {
	in := map[string]any{
		"meta": map[string]any{"view": "needs-me", "filter": `widgets "x"`, "at": "2026-10-15T13:00:00Z"},
		"rows": wireRows("carol", time.Now(), map[string]any{
			"label": "acme/widgets#4870", "unit": map[string]any{"provider": "github", "key": "acme/widgets#4870"}, "link": "https://github.com/acme/widgets/pull/4870",
			"title": "perf: one \"conn\"\nper batch", "author": "carol", "kind": "conflicts", "waiting_on": "author", "since": "2026-10-13T13:00:00Z",
			"stalled": true, "reason": "merge conflicts with develop", "head": "fix/x", "reasons": []string{"author"},
			"workspace": map[string]any{"display_name": "fix-x", "status": "in-progress"},
			"brief":     map[string]any{"size": "m", "risk": "high", "goal": "Run batches on one connection.", "open_questions": 2},
			"override":  map[string]any{"hold": true},
		}),
	}
	var got string
	prelude := keyJS(t) + catalogPrelude(t) + `const state = { health: { viewer: "carol" } };`
	nodeRun(t, prelude, []string{"isSet", "resolveWho", "waitingLabel", "isMuted", "yamlString", "listYAML"},
		`process.stdout.write(JSON.stringify(listYAML(input.meta, input.rows)));`, in, &got)
	for _, want := range []string{
		`view: "needs-me"`, `filter: "widgets \"x\""`, `copied_at: "2026-10-15T13:00:00Z"`, "count: 1", "pull_requests:",
		`  - ref: "acme/widgets#4870"`, `    url: "https://github.com/acme/widgets/pull/4870"`,
		`    title: "perf: one \"conn\"\nper batch"`, `    situation: "merge conflicts"`, `    waiting_on: "me"`,
		`    since: "2026-10-13T13:00:00Z"`, "    stalled: true", `    reason: "merge conflicts with develop"`,
		`    head: "fix/x"`, `    workspace: "fix-x (in-progress)"`, `    in_scope_as: ["author"]`,
		`    brief: {size: "m", risk: "high", open_questions: 2, goal: "Run batches on one connection."}`, `    flags: ["paused"]`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCopiedExploreCandidatesSayWhatTheCardsSay(t *testing.T) {
	in := map[string]any{
		"meta": map[string]any{"query": "widgets", "read": `read "widgets" as repo:acme/widgets`, "at": "2026-10-15T13:00:00Z"},
		"cands": []map[string]any{{
			"label": "acme/widgets#5068", "unit": map[string]any{"provider": "github", "key": "acme/widgets#5068"}, "url": "https://github.com/acme/widgets/pull/5068", "title": "fix(cron): flags", "author": "ana",
			"additions": 6, "deletions": 0, "changed_files": 2, "review_decision": "APPROVED", "checks": "FAILURE",
			"review_requests": []string{"bia", "team:acme/web"}, "draft": false, "updated_at": "2026-10-14T13:00:00Z", "created_at": "2026-10-10T13:00:00Z",
			"brief": map[string]any{"brief": map[string]any{"size": "s", "risk": "low", "goal": "Add flags.", "open_questions": []string{"why?"}}},
		}},
	}
	var got string
	nodeRun(t, keyJS(t)+catalogPrelude(t)+`const state = { rows: [] };
const rowByKey = () => undefined;`,
		[]string{"safeURL", "candidateURL", "yamlString", "candidatesYAML"},
		`process.stdout.write(JSON.stringify(candidatesYAML(input.meta, input.cands)));`, in, &got)
	for _, want := range []string{
		`query: "widgets"`, `read_as: "read \"widgets\" as repo:acme/widgets"`, "count: 1", "pull_requests:",
		`  - ref: "acme/widgets#5068"`, `    url: "https://github.com/acme/widgets/pull/5068"`, `    author: "ana"`,
		`    size: "+6 -0, 2 files"`, `    review: "approved"`, `    checks: "failure"`, `    asks: ["bia", "acme/web"]`,
		`    updated_at: "2026-10-14T13:00:00Z"`, `    in_loop: false`, `    brief: {size: "s", risk: "low", open_questions: 1, goal: "Add flags."}`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestAQueryHoldsFreeTermsAndQualifiers(t *testing.T) {
	var got struct {
		Tokens [][]string         `json:"tokens"`
		Terms  [][]string         `json:"terms"`
		Quals  [][]map[string]any `json:"quals"`
	}
	qs := []string{
		`repo:acme/widgets wait:me fix`,
		`status:"needs me" -risk:high "two words"`,
		`fix: https://github.com/acme/api/pull/7 is:pr author:@me AUTHOR:Ana`,
		`Repo:Widgets -waits-on:team:acme/search unknown:x`,
	}
	nodeRun(t, filterPrelude(t), queryFuncs, `process.stdout.write(JSON.stringify({
  tokens: input.map(tokens),
  terms: input.map(terms),
  quals: input.map((q) => qualifiers(q).map(({ field, value, raw, neg }) => ({ field, value, raw, neg }))),
}));`, qs, &got)
	wantTerms := [][]string{{"fix"}, {"two words"}, {"fix:", "https://github.com/acme/api/pull/7"}, {"unknown:x"}}
	if !reflect.DeepEqual(got.Terms, wantTerms) {
		t.Errorf("terms %q, want %q", got.Terms, wantTerms)
	}
	q := func(field, value, raw string, neg bool) map[string]any {
		return map[string]any{"field": field, "value": value, "raw": raw, "neg": neg}
	}
	wantQuals := [][]map[string]any{
		{q("repo", "acme/widgets", "acme/widgets", false), q("wait", "me", "me", false)},
		{q("status", "needs me", "needs me", false), q("risk", "high", "high", true)},
		{q("is", "pr", "pr", false), q("author", "me", "@me", false), q("author", "ana", "Ana", false)},
		{q("repo", "widgets", "Widgets", false), q("waits-on", "team:acme/search", "team:acme/search", true)},
	}
	if !reflect.DeepEqual(got.Quals, wantQuals) {
		t.Errorf("qualifiers\n%v\nwant\n%v", got.Quals, wantQuals)
	}
	if !slices.Equal(got.Tokens[1], []string{`status:"needs me"`, "-risk:high", `"two words"`}) {
		t.Errorf("tokens %q", got.Tokens[1])
	}
}

func TestAFacetTogglesItsQualifier(t *testing.T) {
	var got []string
	nodeRun(t, filterPrelude(t), append(slices.Clone(queryFuncs), "qualifierToken", "toggledQualifier", "withoutQualifiers"), `process.stdout.write(JSON.stringify([
  toggledQualifier("fix", "repo", "acme/widgets", false),
  toggledQualifier("fix repo:acme/widgets", "repo", "Acme/Widgets", false),
  toggledQualifier("repo:a", "repo", "b", false),
  toggledQualifier("repo:a", "repo", "a", true),
  toggledQualifier("-repo:a", "repo", "a", false),
  toggledQualifier("author:@me", "author", "me", false),
  toggledQualifier("", "status", "needs me", false),
  toggledQualifier('status:"needs me"', "status", "needs me", false),
  withoutQualifiers("repo:a fix -repo:b repo:c wait:me", "repo", false),
]));`, nil, &got)
	want := []string{"fix repo:acme/widgets", "fix", "repo:a repo:b", "-repo:a", "repo:a", "", `status:"needs me"`, "", "fix -repo:b wait:me"}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// lensFuncs are what pipeline runs, with what the fields call.
var lensFuncs = append(slices.Clone(queryFuncs), "haystack", "matches", "inView", "isSet", "isMuted", "isMine", "resolveWho", "waitingWho", "waitingLabel",
	"waitCat", "waitClass", "isStalled", "rowFlags", "upcoming", "sinceTime", "rowValues", "rowKey", "filtersOf", "passesFilters", "passes", "parseSort",
	"tagKeyField", "groupField", "effectiveGroup", "cmp", "byDefault", "keyRank", "keyOrder", "compareRows", "groupLabel", "groupRows", "pipeline")

func lensPrelude(t *testing.T) string {
	src := appJS(t)
	return filterPrelude(t) + jsConst(t, src, "SIZES") + jsConst(t, src, "RISKS") + jsConst(t, src, "rowValuesCache") + `const state = { health: { viewer: "me" } };` + "\n"
}

func TestWaitCatIsTheStripAndAgreesWithTheLabel(t *testing.T) {
	var cases []map[string]any
	kinds := append([]string{"loop_working", "loop_waiting", "done"}, stalledKinds...)
	for _, kind := range kinds {
		for _, waiting := range []string{"me", "author", "alice", "team:o/t", "author, alice", "nobody", ""} {
			for _, author := range []string{"me", "ana"} {
				cases = append(cases, wireRow("me", time.Now(), map[string]any{"label": "o/r#1", "unit": map[string]any{"provider": "github", "key": "o/r#1"}, "kind": kind, "waiting_on": waiting, "author": author}))
			}
		}
	}
	cases = append(cases, map[string]any{"unit": map[string]any{"provider": "thread", "key": "c1"}, "kind": "waiting", "summary": map[string]any{"waits_on": "ana"}})
	var got []struct{ Cat, Class, Label string }
	nodeRun(t, lensPrelude(t), lensFuncs, `process.stdout.write(JSON.stringify(input.map((r) => ({ Cat: waitCat(r), Class: waitClass(r), Label: waitingLabel(r) }))));`, cases, &got)
	for i, c := range cases {
		g := got[i]
		wait, _ := c["facets"].(map[string][]string)
		want := "nobody"
		if len(wait["wait"]) > 0 {
			want = wait["wait"][0]
		}
		loop := c["kind"] == "loop_working" || c["kind"] == "loop_waiting"
		says := slices.Contains(strings.Split(g.Label, ", "), "me")
		switch {
		case g.Cat != want || g.Class != "w-"+g.Cat:
			t.Errorf("%v: cat %s class %s, want %s", c, g.Cat, g.Class, want)
		case !loop && (g.Cat == "me") != says:
			t.Errorf("%v: %s while the label says %q", c, g.Cat, g.Label)
		}
	}
	if got[len(got)-1].Label != "ana" {
		t.Errorf("thread row label %q", got[len(got)-1].Label)
	}
}

func lensRows() []map[string]any {
	row := func(n int, repo, author, kind, waiting, since string, stalled bool, brief map[string]any, ws string) map[string]any {
		r := map[string]any{"ref": map[string]any{"owner": strings.Split(repo, "/")[0], "repo": strings.Split(repo, "/")[1], "number": n},
			"repo": repo, "author": author, "kind": kind, "waiting_on": waiting, "since": since, "stalled": stalled, "views": []string{"needs-me"}, "title": fmt.Sprint("t", n)}
		if stalled {
			r["views"] = []string{"needs-me", "stalled"}
		}
		if brief != nil {
			r["brief"] = brief
		}
		if ws != "" {
			r["workspace"] = map[string]any{"status": ws}
		}
		return r
	}
	return []map[string]any{
		row(1, "acme/api", "ana", "review", "bia", "2026-10-10T00:00:00Z", false, map[string]any{"risk": "low", "size": "s"}, "in-review"),
		row(2, "acme/web", "me", "changes", "author", "2026-10-09T00:00:00Z", true, map[string]any{"risk": "high", "size": "l"}, ""),
		row(3, "acme/widgets", "ana", "merge", "author", "2026-10-08T00:00:00Z", false, nil, "ready-to-merge"),
		row(4, "acme/widgets", "bia", "review", "me", "2026-10-12T00:00:00Z", true, map[string]any{"risk": "medium", "size": "m"}, ""),
		row(5, "acme/api", "bia", "loop_working", "me", "2026-10-11T00:00:00Z", false, map[string]any{"risk": "high", "size": "xs"}, "reviewing"),
	}
}

func TestPipelineFiltersSortsAndGroups(t *testing.T) {
	type res struct {
		Total  int `json:"total"`
		Shown  int `json:"shown"`
		Group  string
		Groups []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Rows  []int  `json:"rows"`
		} `json:"groups"`
	}
	lenses := []map[string]any{
		{"view": "needs-me", "q": ""},
		{"view": "needs-me", "q": "repo:widgets"},
		{"view": "needs-me", "q": "repo:widgets repo:acme/web"},
		{"view": "needs-me", "q": "repo:acme/api wait:loop"},
		{"view": "needs-me", "q": "author:@me"},
		{"view": "needs-me", "q": "-risk:high risk:none risk:low"},
		{"view": "needs-me", "q": "waits-on:me"},
		{"view": "needs-me", "q": "is:stalled"},
		{"view": "needs-me", "q": "", "sort": "risk"},
		{"view": "needs-me", "q": "", "sort": "risk-desc"},
		{"view": "needs-me", "q": "", "sort": "age-desc"},
		{"view": "needs-me", "q": "", "group": "wait"},
		{"view": "needs-me", "q": "", "group": "repo", "sort": "age"},
		{"view": "stalled", "q": "", "layout": "board"},
		{"view": "needs-me", "q": "", "layout": "board"},
		{"view": "needs-me", "q": "", "group": "size"},
	}
	var got []res
	nodeRun(t, lensPrelude(t), lensFuncs, `const rows = `+mustJS(t, wireRows("me", time.Now(), lensRows()...))+`;
process.stdout.write(JSON.stringify(input.map((l) => {
  const lens = { view: l.view, q: l.q, ts: terms(l.q), quals: qualifiers(l.q), filters: filtersOf(qualifiers(l.q)), group: l.group || "", sort: l.sort || "", layout: l.layout || "" };
  const r = pipeline(rows, lens);
  return { total: r.total, shown: r.shown, Group: r.group, groups: r.groups.map((g) => ({ key: g.key, label: g.label, rows: g.rows.map((x) => x.ref.number) })) };
})));`, lenses, &got)
	flat := func(r res) []int {
		var out []int
		for _, g := range r.Groups {
			out = append(out, g.Rows...)
		}
		return out
	}
	for i, want := range [][]int{
		{2, 4, 3, 1, 5},
		{4, 3},
		{2, 4, 3},
		{5},
		{2},
		{3, 1},
		{2, 4, 5},
		{2, 4},
		{2, 5, 4, 1, 3},
		{1, 4, 2, 5, 3},
		{4, 5, 1, 2, 3},
	} {
		if g := flat(got[i]); !slices.Equal(g, want) {
			t.Errorf("lens %v: rows %v, want %v", lenses[i], g, want)
		}
	}
	if got[0].Total != 5 || got[1].Shown != 2 || got[1].Total != 5 {
		t.Errorf("counts %+v %+v", got[0], got[1])
	}
	keys := func(r res) []string {
		var out []string
		for _, g := range r.Groups {
			out = append(out, g.Key+"="+fmt.Sprint(g.Rows))
		}
		return out
	}
	for i, want := range map[int][]string{
		11: {"me=[2 4]", "loop=[5]", "reviewer=[1]", "author=[3]"},
		12: {"acme/api=[1 5]", "acme/web=[2]", "acme/widgets=[3 4]"},
		13: {"me=[2 4]", "reviewer=[]", "author=[]", "merge=[]", "loop=[]"},
		14: {"me=[2 4]", "loop=[5]", "reviewer=[1]", "author=[3]"},
		15: {"xs=[5]", "s=[1]", "m=[4]", "l=[2]", "none=[3]"},
	} {
		if g := keys(got[i]); !slices.Equal(g, want) {
			t.Errorf("lens %v: groups %v, want %v", lenses[i], g, want)
		}
	}
	if got[15].Groups[4].Label != "no brief" || got[11].Groups[0].Label != "Waiting on me" {
		t.Errorf("labels %+v %+v", got[15].Groups[4], got[11].Groups[0])
	}
}

func mustJS(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestACopiedListSaysHowItWasGroupedAndSorted(t *testing.T) {
	var got string
	prelude := keyJS(t) + catalogPrelude(t) + `const state = { health: { viewer: "me" } };`
	nodeRun(t, prelude, []string{"isSet", "resolveWho", "waitingLabel", "isMuted", "yamlString", "listYAML"},
		`process.stdout.write(JSON.stringify(listYAML({ view: "needs-me", filter: "repo:widgets", group_by: "repo", sort: "risk descending", at: "x" }, [])));`, nil, &got)
	for _, want := range []string{`filter: "repo:widgets"`, `group_by: "repo"`, `sort: "risk descending"`, "count: 0"} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestTheOpenRowIsMarkedAndScrolledToOnce(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	src := appJS(t)
	for _, want := range []string{`key === pr ? "sel" : ""`, `"aria-selected": key === pr`, `"aria-current": key === pr`} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	css, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tbody tr.sel td:first-child, .card.sel", "box-shadow: inset var(--strip-card) 0 0 var(--me)", "--sel-unit: color-mix(in srgb, var(--me) 14%"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	script := `const state = {}; let scrolls = 0, row = null;
const document = { querySelector: () => row };` + jsFunction(t, src, "openSlot") + jsFunction(t, src, "revealOpenRow") + `
const log = [];
revealOpenRow("o/r/1"); log.push(scrolls);
row = { scrollIntoView: (o) => { scrolls++; log.push(o.block); } };
revealOpenRow("o/r/1"); revealOpenRow("o/r/1"); log.push(scrolls);
revealOpenRow(""); revealOpenRow("o/r/1"); log.push(scrolls);
process.stdout.write(JSON.stringify(log));`
	out, err := exec.Command(node, "-e", script).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if got, want := string(out), `[0,"nearest",1,"nearest",2]`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestQuestionBlockRenders(t *testing.T) {
	src := appJS(t)
	prelude := `function h(tag, attrs, ...c) { return { tag, attrs: attrs || {}, children: c.flat(Infinity).filter((x) => x !== null && x !== undefined && x !== false) }; }
const state = { busy: new Set(), guidance: { "github:acme/api#7": " check the CI first " }, health: { viewer: "me" }, specs: { byId: { answer: { id: "answer", input: { kind: "answer" } } } } };
const posts = [];
async function fetch(url, init) { posts.push({ url, body: JSON.parse(init.body) }); return { ok: true, status: 200, json: async () => ({ ok: true }) }; }
function render() {}
function setNotice() {}
function scheduleLoad() {}
const document = { getElementById: () => null };
` + keyJS(t) + jsConst(t, src, "ARTIFACT_LINES") + jsConst(t, src, "API_VERSION")
	draft := strings.Repeat("a finding\n", 45)
	ref := map[string]any{"owner": "acme", "repo": "api", "number": 7}
	in := map[string]any{
		"asked": map[string]any{
			"row": map[string]any{"ref": ref, "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "kind": "loop_waiting", "actions": []map[string]any{{"id": "answer", "enabled": true}, {"id": "continue", "enabled": true}, {"id": "dismiss", "enabled": true}}, "question": map[string]any{
				"record_id": 3, "state": "asked", "kind": "choice", "text": "Publish the review?\n  It has two findings.",
				"options": []map[string]any{
					{"id": "a", "label": "Merge as is", "consequence": "Approves without the findings"},
					{"id": "b", "label": "Rebase first", "consequence": "Asks the author to rebase"},
					{"id": "c", "label": "Ask the author", "consequence": "Comments and waits"},
				},
				"recommend": "b", "verdict": "APPROVE", "blocking": 0, "non_blocking": 2,
			}},
			"turn": map[string]any{"turn": map[string]any{"seq": 1}, "records": []map[string]any{
				{"id": 2, "record": map[string]any{"summary": "an older record"}},
				{"id": 3, "record": map[string]any{"summary": "Reviewed the batch export", "artifact": map[string]any{"kind": "review_draft", "body": draft}}},
			}, "answers": []any{}},
		},
		"unrecorded": map[string]any{
			"row":  map[string]any{"ref": ref, "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "kind": "loop_stopped", "actions": []map[string]any{{"id": "answer", "enabled": true}, {"id": "continue", "enabled": true}, {"id": "dismiss", "enabled": true}}, "question": map[string]any{"state": "unrecorded", "kind": "", "text": "", "blocking": 0, "non_blocking": 0, "last_message": "I ran out of context before recording."}},
			"turn": map[string]any{"turn": map[string]any{"seq": 1}, "records": []any{}, "answers": []any{}},
		},
	}
	type button struct {
		Label string `json:"label"`
		Title string `json:"title"`
		Class string `json:"cls"`
	}
	type view struct {
		Text    string   `json:"text"`
		Buttons []button `json:"buttons"`
		Details []struct {
			Open bool   `json:"open"`
			Text string `json:"text"`
		} `json:"details"`
	}
	var got struct {
		Asked   view   `json:"asked"`
		Stopped view   `json:"stopped"`
		Label   string `json:"label"`
		Plain   string `json:"plain"`
		Posts   []struct {
			URL  string         `json:"url"`
			Body map[string]any `json:"body"`
		} `json:"posts"`
	}
	nodeRun(t, prelude, []string{"apiError", "isSet", "questionRecord", "answerNote", "actionTitle", "turnButton", "answerBlock", "guidanceBox", "recommendedLabel", "optionButton", "questionChoices", "artifactBlock", "questionBlock", "guidanceLabel", "act"}, `(async () => {
  const walk = (n, out = []) => { if (n && typeof n === "object") { out.push(n); n.children.forEach((c) => walk(c, out)); } return out; };
  const text = (n) => (n && typeof n === "object" ? n.children.map(text).join("") : String(n));
  const view = (n) => { const all = walk(n); return {
    text: text(n),
    buttons: all.filter((x) => x.tag === "button").map((b) => ({ label: text(b), title: b.attrs.title || "", cls: b.attrs.class })),
    details: all.filter((x) => x.tag === "details").map((d) => ({ open: !!d.attrs.open, text: text(d) })),
  }; };
  const block = questionBlock(input.asked);
  const out = { asked: view(block), stopped: view(questionBlock(input.unrecorded)), label: guidanceLabel(input.asked.row), plain: guidanceLabel({ ref: input.asked.row.ref }) };
  await walk(block).find((b) => b.tag === "button" && text(b).startsWith("Rebase first")).attrs.onclick();
  process.stdout.write(JSON.stringify({ ...out, posts }));
})();`, in, &got)
	for _, want := range []string{"Agent asks", "Reviewed the batch export", "drafted APPROVE, 0 blocking, 2 non-blocking", "Publish the review?\n  It has two findings."} {
		if !strings.Contains(got.Asked.Text, want) {
			t.Errorf("the asked block lacks %q:\n%s", want, got.Asked.Text)
		}
	}
	if strings.Contains(got.Asked.Text, "an older record") {
		t.Error("the asked block shows a record other than the question's")
	}
	wantButtons := []button{
		{"Rebase first recommended", "Asks the author to rebase", "act primary"},
		{"Merge as is", "Approves without the findings", "act"},
		{"Ask the author", "Comments and waits", "act"},
	}
	if len(got.Asked.Buttons) != 6 || !reflect.DeepEqual(got.Asked.Buttons[:3], wantButtons) || got.Asked.Buttons[3].Label != "Send answer" ||
		got.Asked.Buttons[4].Label != "Continue" || got.Asked.Buttons[5].Label != "Dismiss" {
		t.Errorf("buttons %+v", got.Asked.Buttons)
	}
	if len(got.Asked.Details) != 1 || got.Asked.Details[0].Open || !strings.Contains(got.Asked.Details[0].Text, "45 lines") {
		t.Errorf("the draft is not collapsed: %+v", got.Asked.Details)
	}
	if !strings.Contains(got.Asked.Text, "Or answer in your own words") || !strings.Contains(got.Stopped.Text, "Answer in your own words") {
		t.Errorf("the free answer is not in the card:\n%s\n%s", got.Asked.Text, got.Stopped.Text)
	}
	if got.Label != "Or answer in your own words" || got.Plain != "Guidance for the next round (optional)" {
		t.Errorf("box labels %q, %q", got.Label, got.Plain)
	}
	wantBody := map[string]any{"guidance": "check the CI first", "option_id": "b", "source": "cockpit"}
	if len(got.Posts) != 1 || got.Posts[0].URL != "/api/units/github/acme%2Fapi%237/answer" || !reflect.DeepEqual(got.Posts[0].Body, wantBody) {
		t.Errorf("posts %+v", got.Posts)
	}
	if !strings.Contains(got.Stopped.Text, "Agent stopped") || !strings.Contains(got.Stopped.Text, "I ran out of context before recording.") {
		t.Errorf("the stopped block: %s", got.Stopped.Text)
	}
	if len(got.Stopped.Buttons) != 3 || got.Stopped.Buttons[0].Label != "Send answer" || got.Stopped.Buttons[1].Label != "Continue" || got.Stopped.Buttons[2].Label != "Dismiss" {
		t.Errorf("stopped buttons %+v", got.Stopped.Buttons)
	}
}

func TestQuestionCardSendsTheChoiceWithTheNoteOrTheTextAlone(t *testing.T) {
	src := appJS(t)
	prelude := `function h(tag, attrs, ...c) { return { tag, attrs: attrs || {}, children: c.flat(Infinity).filter((x) => x !== null && x !== undefined && x !== false) }; }
const state = { busy: new Set(), guidance: {}, health: { viewer: "me" }, specs: { byId: { answer: { id: "answer", input: { kind: "answer" } } } } };
const posts = [];
async function fetch(url, init) { posts.push(JSON.parse(init.body)); return { ok: true, status: 200, json: async () => ({ ok: true }) }; }
function render() {}
function setNotice() {}
function scheduleLoad() {}
const document = { getElementById: () => null };
` + keyJS(t) + jsConst(t, src, "ARTIFACT_LINES") + jsConst(t, src, "API_VERSION")
	in := map[string]any{
		"row":  map[string]any{"label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "kind": "loop_waiting", "actions": []map[string]any{{"id": "answer", "enabled": true}, {"id": "continue", "enabled": true}, {"id": "dismiss", "enabled": true}}, "question": map[string]any{"state": "asked", "kind": "confirm", "text": "Publish?", "recommend": "yes"}},
		"turn": map[string]any{"turn": map[string]any{"seq": 1}, "records": []any{}, "answers": []any{}},
	}
	var got struct {
		Idle  bool             `json:"idle"`
		Ready bool             `json:"ready"`
		Posts []map[string]any `json:"posts"`
	}
	nodeRun(t, prelude, []string{"apiError", "isSet", "questionRecord", "answerNote", "actionTitle", "turnButton", "answerBlock", "guidanceBox", "recommendedLabel", "optionButton", "questionChoices", "artifactBlock", "questionBlock", "guidanceLabel", "act"}, `(async () => {
  const walk = (n, out = []) => { if (n && typeof n === "object") { out.push(n); n.children.forEach((c) => walk(c, out)); } return out; };
  const text = (n) => (n && typeof n === "object" ? n.children.map(text).join("") : String(n));
  const click = async (label, note) => {
    state.guidance["github:acme/api#7"] = note;
    const b = walk(questionBlock(input)).find((x) => x.tag === "button" && text(x).startsWith(label));
    if (!b.attrs.disabled) await b.attrs.onclick();
    return b;
  };
  const idle = (await click("Send answer", " ")).attrs.disabled;
  await click("Yes", "");
  await click("No", "only the first finding");
  const ready = !(await click("Send answer", "look at the flaky test instead")).attrs.disabled;
  process.stdout.write(JSON.stringify({ idle, ready, posts }));
})();`, in, &got)
	if !got.Idle || !got.Ready {
		t.Errorf("Send answer: off while blank %v, on with text %v", got.Idle, got.Ready)
	}
	want := []map[string]any{
		{"yes": true, "source": "cockpit"},
		{"no": true, "guidance": "only the first finding", "source": "cockpit"},
		{"guidance": "look at the flaky test instead", "source": "cockpit"},
	}
	if !reflect.DeepEqual(got.Posts, want) {
		t.Errorf("posts %v, want %v", got.Posts, want)
	}
}

func TestAnswerSaidJoinsTheChoiceAndTheNote(t *testing.T) {
	in := []map[string]any{
		{"yes": true},
		{"yes": true, "text": "but squash"},
		{"option_id": "b", "text": "after CI"},
		{"no": true, "text": "drop the nit"},
		{"text": "look elsewhere"},
		{},
	}
	var got []string
	nodeRun(t, "", []string{"answerSaid"}, `process.stdout.write(JSON.stringify(input.map(answerSaid)));`, in, &got)
	want := []string{"yes", "yes. but squash", "option b. after CI", "no. drop the nit", "look elsewhere", "no"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestEveryLoopActionHasALabelAndIsALoopEvent(t *testing.T) {
	src := appJS(t)
	labels := src[strings.Index(src, "const EVENT_LABEL = {"):]
	labels = labels[:strings.Index(labels, "};")]
	loop := src[strings.Index(src, "const LOOP_EVENTS = "):]
	loop = loop[:strings.Index(loop, "\n")]
	for _, kind := range []string{"turn_sent", "turn_delivered", "turn_started", "turn_ended", "turn_resumed", "turn_withdrawn",
		"answered", "answer_routed", "question_dismissed", "late_record", "stop_blocked", "turn_stopped", "guide_trimmed", "dossier_restored", "dossier_granted", "dossier_call", "brief_waiting", "intake_accepted", "intake_briefing", "intake_starting", "intake_started", "intake_failed", "intake_canceled", "intake_guidance", "workflow_step_ran"} {
		if !strings.Contains(labels, kind+": ") {
			t.Errorf("%s has no label", kind)
		}
		if !strings.Contains(loop, `"`+kind+`"`) {
			t.Errorf("%s is not a loop event", kind)
		}
	}
}

func TestTimelineShowsAnAnswerOnceWhenTheTurnHasNoSentAt(t *testing.T) {
	d := map[string]any{
		"turn":   map[string]any{"turn": map[string]any{"seq": 1}, "answers": []any{map[string]any{"turn_seq": 1, "text": "go", "source": "tui", "at": "2026-10-18T12:00:00Z"}}},
		"events": []any{map[string]any{"kind": "answered", "at": "2026-10-18T12:00:00Z", "data": "go"}},
	}
	var got []map[string]any
	nodeRun(t, "", []string{"answerSaid", "timelineEvents"}, `process.stdout.write(JSON.stringify(timelineEvents(input)));`, d, &got)
	if len(got) != 1 || got[0]["data"] != "go (from tui)" {
		t.Fatalf("%+v", got)
	}
}

func TestTimelineSaysWhatTheAnswerChoseAndThatItIsQueued(t *testing.T) {
	answers := []any{
		map[string]any{"turn_seq": 1, "yes": true, "text": "but thank them", "source": "cockpit", "route": "typed", "at": "2026-10-18T12:01:00Z"},
		map[string]any{"turn_seq": 1, "no": true, "source": "notification", "route": "typed", "at": "2026-10-18T12:02:00Z"},
		map[string]any{"turn_seq": 1, "option_id": "publish", "text": "Publish (option publish)", "source": "cockpit", "route": "typed", "at": "2026-10-18T12:03:00Z"},
		map[string]any{"turn_seq": 1, "option_id": "publish", "text": "shorten it", "source": "cockpit", "route": "typed", "at": "2026-10-18T12:04:00Z"},
		map[string]any{"turn_seq": 1, "text": "wait for CI", "source": "cockpit", "route": "queued", "at": "2026-10-18T12:05:00Z"},
	}
	d := map[string]any{"turn": map[string]any{"turn": map[string]any{"seq": 1}, "answers": answers}, "events": []any{}}
	var got []map[string]any
	nodeRun(t, "", []string{"answerSaid", "timelineEvents"}, `process.stdout.write(JSON.stringify(timelineEvents(input)));`, d, &got)
	var said []string
	for _, e := range got {
		said = append(said, e["data"].(string))
	}
	want := []string{"wait for CI (from cockpit, queued until the agent stops)", "option publish. shorten it (from cockpit)",
		"Publish (option publish) (from cockpit)", "no (from notification)", "yes. but thank them (from cockpit)"}
	if !slices.Equal(said, want) {
		t.Fatalf("%q\nwant %q", said, want)
	}
}

func TestCockpitShowsTheBriefingIntake(t *testing.T) {
	prelude := `function h(tag, attrs, ...c) { return { tag, attrs, children: c.flat() }; }` + "\n" + `function stamp(t) { return t; }`
	in := []any{
		map[string]any{"payload": map[string]any{"intake": map[string]any{"seq": 42, "intent": "review", "state": "briefing", "attempt": 2, "last_error": "rate limited", "next": "14:05"}}},
		map[string]any{"payload": map[string]any{"intake": map[string]any{"seq": 43, "intent": "review", "state": "starting"}}},
		map[string]any{},
	}
	var got struct {
		Chips []any      `json:"chips"`
		Lines [][]string `json:"lines"`
		Notes []string   `json:"notes"`
	}
	nodeRun(t, prelude, []string{"isSet", "pl", "intakeWaiting", "intakeChip", "intakeLines", "acceptedNote"}, `process.stdout.write(JSON.stringify({
		chips: input.map((r) => intakeChip(r)),
		lines: [intakeLines(input[0].payload.intake)],
		notes: [acceptedNote("review", "acme/api#7", { seq: 42 }), acceptedNote("review", "acme/api#7", { seq: 42, joined: true, state: "briefing" }), acceptedNote("open", "acme/api#7", undefined)],
	}));`, in, &got)
	chip := map[string]any{"tag": "span", "attrs": map[string]any{"class": "flag briefing", "title": "intake 42 waits for the brief, attempt 2, last error: rate limited"}, "children": []any{"briefing"}}
	if !reflect.DeepEqual(got.Chips, []any{chip, nil, nil}) {
		t.Errorf("chips %#v", got.Chips)
	}
	if want := []string{"Intake 42 (review) waits for the brief, attempt 2", "Last error: rate limited", "Next attempt at 14:05"}; !slices.Equal(got.Lines[0], want) {
		t.Errorf("lines %q", got.Lines[0])
	}
	if want := []string{"review acme/api#7: accepted as intake 42", "review acme/api#7: joined intake 42", ""}; !slices.Equal(got.Notes, want) {
		t.Errorf("notes %q", got.Notes)
	}
}

func TestTheTurnDetailNamesTheTrimmedBlocks(t *testing.T) {
	prelude := `function h(tag, attrs, ...c) { return { tag, attrs, children: c.flat() }; }`
	in := []any{
		map[string]any{"turn": map[string]any{"turn": map[string]any{"id": "r2"}}, "events": []any{
			map[string]any{"kind": "guide_trimmed", "key": "r1", "data": "old"},
			map[string]any{"kind": "guide_trimmed", "key": "r2", "data": "user: tag=lang=go/intent=review.md"}}},
		map[string]any{"turn": map[string]any{"turn": map[string]any{"id": "r3"}}, "events": []any{}},
	}
	var got []any
	nodeRun(t, prelude, []string{"trimmedNote"}, `process.stdout.write(JSON.stringify(input.map((d) => trimmedNote(d))));`, in, &got)
	want := map[string]any{"tag": "div", "attrs": map[string]any{"class": "d-trimmed muted small"}, "children": []any{"Guide trimmed over the judgment limit: user: tag=lang=go/intent=review.md"}}
	if !reflect.DeepEqual(got, []any{want, nil}) {
		t.Fatalf("%#v", got)
	}
}

func TestAnOutOfDatePageIsToldToReload(t *testing.T) {
	var got []string
	nodeRun(t, "", []string{"apiError"}, `process.stdout.write(JSON.stringify([
		apiError(input, "HTTP 426", 426),
		apiError(input, "HTTP 503", 503),
	]));`, map[string]any{"error": "API version 3 is not this daemon's 4", "fix": "launchctl kickstart -k gui/501/x"}, &got)
	if !strings.Contains(got[0], "reload this page") || strings.Contains(got[0], "kickstart") {
		t.Errorf("426: %s", got[0])
	}
	if !strings.Contains(got[1], "run: launchctl kickstart") {
		t.Errorf("503: %s", got[1])
	}
}

type wireInput struct {
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Default     string `json:"default"`
	UntilState  bool   `json:"until_state"`
	Max         int    `json:"max"`
}

type wireSpec struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Summary  string     `json:"summary"`
	Group    string     `json:"group"`
	Surfaces []string   `json:"surfaces"`
	Input    *wireInput `json:"input"`
}

type wireState struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
	Note    string `json:"note"`
	Label   string `json:"label"`
	Primary bool   `json:"primary"`
	Confirm string `json:"confirm"`
	Basis   string `json:"basis"`
	Warning string `json:"warning"`
}

// wantTitle is an action button's tooltip: its summary, then its note when
// enabled or its reason when not.
func wantTitle(sp wireSpec, st wireState) string {
	second := st.Note
	if !st.Enabled {
		second = st.Reason
	}
	return strings.Join(slices.DeleteFunc([]string{sp.Summary, second}, func(x string) bool { return x == "" }), "\n")
}

type wireButton struct {
	Label    string `json:"label"`
	Disabled bool   `json:"disabled"`
	Title    string `json:"title"`
	Class    string `json:"class"`
}

// wireFixtures reads the goldens the daemon serves: GET /api/actions and the rows' actions.
func wireFixtures(t *testing.T) (specs map[string]any, rows map[string]map[string]any, byID map[string]wireSpec, states map[string][]wireState) {
	t.Helper()
	read := func(name string, v any) {
		raw, err := os.ReadFile(filepath.Join("..", "daemon", "testdata", "wire", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(brandtest.Expand(string(raw))), v); err != nil {
			t.Fatal(err)
		}
	}
	var typed struct {
		Specs []wireSpec `json:"specs"`
	}
	read("specs.json", &specs)
	read("specs.json", &typed)
	read("actions.json", &rows)
	var rowStates map[string]struct {
		Actions []wireState `json:"actions"`
	}
	read("actions.json", &rowStates)
	byID = map[string]wireSpec{}
	for _, s := range typed.Specs {
		byID[s.ID] = s
	}
	states = map[string][]wireState{}
	for name, r := range rowStates {
		states[name] = r.Actions
	}
	return
}

// cardGroups are the actions the question card and the briefing block render themselves.
var cardGroups = []string{"turn", "intake", "brief"}

func buttonPrelude(t *testing.T) string {
	return `function h(tag, attrs, ...c) { return { tag, attrs: attrs || {}, text: c.flat().filter((x) => x !== null && x !== undefined && x !== false).join("") }; }
const state = { busy: new Set(), guidance: {}, actionError: "", actionDetail: "", specs: { version: "", byId: {} } };
const render = () => {}, setNotice = () => {}, scheduleLoad = () => {};
const document = { getElementById: () => null };
` + keyJS(t) + jsConst(t, appJS(t), "API_VERSION") + jsConst(t, appJS(t), "FOCUS_PAIRS") + jsFunction(t, appJS(t), "focusSlot") + "\n" + jsConst(t, appJS(t), "CARD_GROUPS") + `
let confirmed = [], prompts = [], fetched = [], promptAnswer = null, confirmAnswer = false;
const confirm = (text) => { confirmed.push(text); return confirmAnswer; };
const prompt = (text, def) => { prompts.push([text, def]); return promptAnswer; };
const fetch = async (url, opts) => { fetched.push({ url, body: JSON.parse(opts.body) }); return { ok: true, status: 200, json: async () => ({ ok: true, output: [] }) }; };
const useSpecs = (s) => { state.specs = { version: s.version, byId: Object.fromEntries(s.specs.map((x) => [x.id, x])) }; };
`
}

var buttonFuncs = []string{"actionStates", "actionTitle", "actionButton", "actionButtons", "actionNotes", "act", "apiError", "acceptedNote"}

func TestActionButtonsRenderTheServedStates(t *testing.T) {
	specs, rows, byID, states := wireFixtures(t)
	var got map[string]struct {
		Row    []wireButton `json:"row"`
		Detail []wireButton `json:"detail"`
		Notes  []string     `json:"notes"`
		Clicks []string     `json:"confirms"`
	}
	nodeRun(t, buttonPrelude(t), append(buttonFuncs, "intakeWaiting"), `useSpecs(input.specs);
(async () => {
  const out = {};
  for (const [name, row] of Object.entries(input.rows)) {
    const view = (surface) => actionButtons(row, surface).map((b) => ({ label: b.text, disabled: !!b.attrs.disabled, title: b.attrs.title || "", class: b.attrs.class }));
    const seen = { row: view("row"), detail: view("detail"), notes: actionNotes(row, "detail").map((n) => n.text) };
    confirmed = [];
    for (const b of actionButtons(row, "detail")) if (!b.attrs.disabled) await b.attrs.onclick({ stopPropagation() {} });
    out[name] = { ...seen, confirms: confirmed, busy: state.busy.size };
  }
  process.stdout.write(JSON.stringify(out));
})();`, map[string]any{"specs": specs, "rows": rows}, &got)
	if len(got) != len(rows) {
		t.Fatalf("%d fixtures rendered, want %d", len(got), len(rows))
	}
	for name, sts := range states {
		for _, surface := range []string{"row", "detail"} {
			var want []wireButton
			var wantNotes, wantConfirms []string
			for _, st := range sts {
				sp, ok := byID[st.ID]
				if !ok || !slices.Contains(sp.Surfaces, surface) || (surface == "detail" && slices.Contains(cardGroups, sp.Group)) {
					continue
				}
				b := wireButton{Label: cmpOr(st.Label, sp.Label), Disabled: !st.Enabled, Title: wantTitle(sp, st), Class: "act"}
				if !st.Enabled {
					b.Class = "act off"
				} else if st.Primary {
					b.Class = "act primary"
				}
				want = append(want, b)
				if surface == "detail" && st.Enabled {
					if st.Warning != "" {
						wantNotes = append(wantNotes, st.Warning)
					}
					if st.Confirm != "" {
						wantConfirms = append(wantConfirms, st.Confirm)
					}
				}
			}
			gotButtons := got[name].Row
			if surface == "detail" {
				gotButtons = got[name].Detail
			}
			if !reflect.DeepEqual(gotButtons, want) {
				t.Errorf("%s %s buttons:\n got %+v\nwant %+v", name, surface, gotButtons, want)
			}
			if surface == "detail" {
				if !slices.Equal(got[name].Notes, wantNotes) {
					t.Errorf("%s notes %q, want %q", name, got[name].Notes, wantNotes)
				}
				if !slices.Equal(got[name].Clicks, wantConfirms) {
					t.Errorf("%s confirms %q, want %q", name, got[name].Clicks, wantConfirms)
				}
			}
		}
		for _, b := range append(got[name].Row, got[name].Detail...) {
			if b.Label == "Answer" || b.Label == "Continue" || b.Label == "Dismiss" {
				t.Errorf("%s: %s belongs to the question card", name, b.Label)
			}
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestADisabledActionLooksNotAllowed(t *testing.T) {
	specs, _, byID, _ := wireFixtures(t)
	row := map[string]any{"label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "actions": []wireState{
		{ID: "address", Enabled: false, Primary: true, Reason: "Not yours: take it over first"},
		{ID: "open", Enabled: true, Note: "Open the workspace"},
	}}
	var got []map[string]any
	nodeRun(t, buttonPrelude(t), buttonFuncs, `useSpecs(input.specs);
process.stdout.write(JSON.stringify(actionButtons(input.row, "row").map((b) => ({ disabled: !!b.attrs.disabled, title: b.attrs.title, class: b.attrs.class }))));`,
		map[string]any{"specs": specs, "row": row}, &got)
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got[0]["disabled"] != true || got[0]["title"] != wantTitle(byID["address"], wireState{Reason: "Not yours: take it over first"}) || got[0]["class"] != "act off" {
		t.Errorf("a disabled action renders %v", got[0])
	}
	if got[1]["disabled"] != false || got[1]["title"] != wantTitle(byID["open"], wireState{Enabled: true, Note: "Open the workspace"}) || got[1]["class"] != "act" {
		t.Errorf("an enabled action renders %v", got[1])
	}
	raw, err := fs.ReadFile(files, "static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\.act\.off[^{]*\{[^}]*cursor: not-allowed[^}]*border-style: dashed`).Match(raw) {
		t.Error("app.css gives a refused action no not-allowed cursor and dashed border")
	}
}

func TestPostCarriesBasisAndSource(t *testing.T) {
	specs, rows, _, _ := wireFixtures(t)
	type call struct {
		Fixture, ID, Guidance, Prompt string
		Confirm                       bool
	}
	type result struct {
		URL     string         `json:"url"`
		Body    map[string]any `json:"body"`
		Error   string         `json:"error"`
		Fetched int            `json:"fetched"`
	}
	long := strings.Repeat("x", 501)
	cases := []struct {
		call
		want result
	}{
		{call{Fixture: "pending", ID: "run_now", Guidance: "go on"}, result{URL: "/api/units/github/acme%2Fapi%237/run_now", Body: map[string]any{"source": "cockpit", "basis": "pending:2026-10-08T10:00:00Z", "guidance": "go on"}, Fetched: 1}},
		{call{Fixture: "held", ID: "run_now", Confirm: true}, result{URL: "/api/units/github/acme%2Fapi%237/run_now", Body: map[string]any{"source": "cockpit", "basis": "held:automatic round held: daily cap"}, Fetched: 1}},
		{call{Fixture: "pending", ID: "mute", Prompt: "36h"}, result{URL: "/api/units/github/acme%2Fapi%237/mute", Body: map[string]any{"source": "cockpit", "until": "36h"}, Fetched: 1}},
		{call{Fixture: "pending", ID: "mute", Prompt: "state"}, result{URL: "/api/units/github/acme%2Fapi%237/mute", Body: map[string]any{"source": "cockpit", "until_state": true}, Fetched: 1}},
		{call{Fixture: "pending", ID: "guide"}, result{Error: "guide acme/api#7: Guide is required"}},
		{call{Fixture: "mine_no_reviewer", ID: "nudge", Guidance: long, Confirm: true}, result{Error: "nudge acme/api#10: guidance is 501 characters, the limit is 500"}},
		{call{Fixture: "mine_no_reviewer", ID: "nudge", Guidance: "ask about CI", Confirm: true}, result{URL: "/api/units/github/acme%2Fapi%2310/nudge", Body: map[string]any{"source": "cockpit", "guidance": "ask about CI"}, Fetched: 1}},
		{call{Fixture: "forceable_refusal", ID: "remove_force", Confirm: true}, result{URL: "/api/units/github/acme%2Fapi%237/remove_force", Body: map[string]any{"source": "cockpit"}, Fetched: 1}},
	}
	var in []map[string]any
	for _, c := range cases {
		in = append(in, map[string]any{"row": rows[c.Fixture], "id": c.ID, "guidance": c.Guidance, "prompt": c.Prompt, "confirm": c.Confirm})
	}
	var got []result
	nodeRun(t, buttonPrelude(t), buttonFuncs, `useSpecs(input.specs);
(async () => {
  const out = [];
  for (const c of input.cases) {
    fetched = []; state.actionError = ""; state.guidance = {};
    promptAnswer = c.prompt || null; confirmAnswer = c.confirm;
    await act(c.row, c.id, { guidance: c.guidance });
    out.push({ url: fetched.length ? fetched[0].url : "", body: fetched.length ? fetched[0].body : null, error: state.actionError, fetched: fetched.length });
  }
  process.stdout.write(JSON.stringify(out));
})();`, map[string]any{"specs": specs, "cases": in}, &got)
	for i, c := range cases {
		g := got[i]
		if g.URL != c.want.URL || !reflect.DeepEqual(g.Body, c.want.Body) || g.Fetched != c.want.Fetched || (c.want.Error == "") != (g.Error == "") || !strings.Contains(g.Error, c.want.Error) {
			t.Errorf("%s %s: got %+v, want %+v", c.Fixture, c.ID, g, c.want)
		}
	}
}

func TestSpecsAreRefetchedWhenTheVersionChanges(t *testing.T) {
	var got struct {
		Fetches  int      `json:"fetches"`
		Versions []string `json:"versions"`
		Labels   []string `json:"labels"`
	}
	nodeRun(t, `const state = { specs: { version: "", byId: {} }, error: "" };
let fetches = 0, served = { version: "v1", specs: [{ id: "run_now", label: "Run now" }] };
const getJSON = async (url) => { if (url !== "/api/actions") throw new Error(url); fetches++; return served; };
`, []string{"specsStale", "loadSpecs", "refreshSpecs"}, `(async () => {
  const versions = [];
  await refreshSpecs([{ specs_version: "v1" }, { specs_version: "v1" }]); versions.push(state.specs.version);
  await refreshSpecs([{ specs_version: "v1" }]); versions.push(state.specs.version);
  served = { version: "v2", specs: [{ id: "run_now", label: "Go now" }] };
  await refreshSpecs([{ specs_version: "v2" }]); versions.push(state.specs.version);
  await refreshSpecs([]); 
  process.stdout.write(JSON.stringify({ fetches, versions, labels: [state.specs.byId.run_now.label] }));
})();`, nil, &got)
	if got.Fetches != 2 || !slices.Equal(got.Versions, []string{"v1", "v1", "v2"}) || !slices.Equal(got.Labels, []string{"Go now"}) {
		t.Errorf("%+v", got)
	}
}

func TestExploreCandidatesUseTheirActions(t *testing.T) {
	specs, rows, byID, states := wireFixtures(t)
	fixtures := []string{"explore_candidate_mine", "untracked_open_not_mine"}
	in := map[string]any{"specs": specs, "candidates": map[string]any{}}
	for _, f := range fixtures {
		in["candidates"].(map[string]any)[f] = rows[f]
	}
	var got map[string]struct {
		Buttons []wireButton `json:"buttons"`
		Ran     []string     `json:"ran"`
	}
	nodeRun(t, buttonPrelude(t), buttonFuncs, `useSpecs(input.specs);
(async () => {
  const out = {};
  for (const [name, c] of Object.entries(input.candidates)) {
    const ran = [];
    const bs = actionButtons(c, "explore", { scope: "explore", run: (id) => ran.push(id) });
    for (const b of bs) if (!b.attrs.disabled) await b.attrs.onclick({ stopPropagation() {} });
    out[name] = { buttons: bs.map((b) => ({ label: b.text, disabled: !!b.attrs.disabled, title: b.attrs.title || "", class: b.attrs.class })), ran };
  }
  process.stdout.write(JSON.stringify(out));
})();`, in, &got)
	for _, f := range fixtures {
		var want []wireButton
		var wantRan []string
		for _, st := range states[f] {
			sp := byID[st.ID]
			if !slices.Contains(sp.Surfaces, "explore") {
				continue
			}
			b := wireButton{Label: cmpOr(st.Label, sp.Label), Disabled: !st.Enabled, Title: wantTitle(sp, st), Class: "act"}
			if !st.Enabled {
				b.Class = "act off"
			} else {
				wantRan = append(wantRan, st.ID)
				if st.Primary {
					b.Class = "act primary"
				}
			}
			want = append(want, b)
		}
		if !reflect.DeepEqual(got[f].Buttons, want) || !slices.Equal(got[f].Ran, wantRan) {
			t.Errorf("%s:\n got %+v ran %v\nwant %+v ran %v", f, got[f].Buttons, got[f].Ran, want, wantRan)
		}
	}
}

func TestAnExploreActionTheRowRefusesTakesTheTrackBack(t *testing.T) {
	prelude := keyJS(t) + `
const state = { rows: [], actionError: "", explore: { status: {} } };
const calls = [];
const rowByKey = (k) => state.rows.find((r) => keyOf(r) === k);
const render = () => {};
async function load() { if (calls.includes("track") && !calls.includes("untrack")) state.rows = [{ label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" }, actions: [{ id: "nudge", enabled: false, reason: "it waits on the review loop" }] }]; else state.rows = []; }
async function act(row, name) { calls.push(name); return true; }
`
	var got []map[string]any
	nodeRun(t, prelude, []string{"actOnCandidate"},
		`(async () => { const c = { label: "acme/api#7", unit: { provider: "github", key: "acme/api#7" } }; const ok = await actOnCandidate(c, "nudge", {}); process.stdout.write(JSON.stringify([{ ok, calls, status: state.explore.status["github:acme/api#7"] }])); })();`,
		nil, &got)
	if got[0]["ok"] != false || fmt.Sprint(got[0]["calls"]) != "[track untrack]" {
		t.Fatalf("%v", got[0])
	}
	st := got[0]["status"].(map[string]any)
	if st["text"] != "nudge acme/api#7: it waits on the review loop; untracked it again" || st["error"] != true {
		t.Errorf("status %v", st)
	}
}

func TestAnEmptyBoardIsHidden(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler("").ServeHTTP(rec, httptest.NewRequest("GET", "/static/app.css", nil))
	if !strings.Contains(rec.Body.String(), ".board[hidden] { display: none; }") {
		t.Error("app.css lacks the hidden board rule")
	}
}

const treePrelude = `function h(tag, attrs, ...c) { const children = c.flat().filter((x) => x !== null && x !== undefined && x !== false); return { tag, attrs: attrs || {}, children, text: children.map((x) => (typeof x === "object" ? x.text : String(x))).join("") }; }
function stamp(t) { return t; }
const walk = (n, out = []) => { if (n && typeof n === "object") { out.push(n); (n.children || []).forEach((c) => walk(c, out)); } return out; };
const buttonsOf = (n) => walk(n).filter((x) => x.tag === "button").map((b) => ({ label: b.text, disabled: !!b.attrs.disabled, title: b.attrs.title || "", class: b.attrs.class, click: b.attrs.onclick }));
`

func TestTheQuestionCardFollowsTheServedStates(t *testing.T) {
	specs, rows, byID, states := wireFixtures(t)
	fixtures := []string{"asked_choice", "blocked"}
	in := map[string]any{"specs": specs, "rows": map[string]any{
		"asked_choice": rows["asked_choice"],
		"blocked":      rows["blocked"],
		"hidden":       map[string]any{"label": "acme/api#7", "unit": map[string]any{"provider": "github", "key": "acme/api#7"}, "actions": []wireState{{ID: "open", Enabled: true}}},
	}}
	var got map[string]map[string]*wireButton
	nodeRun(t, buttonPrelude(t), append(buttonFuncs, "answerNote", "turnButton"), `useSpecs(input.specs);
const out = {};
for (const [name, row] of Object.entries(input.rows)) {
  out[name] = {};
  for (const id of ["answer", "continue", "dismiss"]) {
    const b = turnButton(row, id, id, id, {}, { title: "card title" });
    out[name][id] = b && { label: b.text, disabled: !!b.attrs.disabled, title: b.attrs.title || "", class: b.attrs.class };
  }
}
process.stdout.write(JSON.stringify(out));`, in, &got)
	for _, f := range fixtures {
		for _, st := range states[f] {
			if st.ID != "answer" && st.ID != "continue" && st.ID != "dismiss" {
				continue
			}
			want := wireButton{Label: st.ID, Disabled: !st.Enabled, Title: wantTitle(byID[st.ID], st), Class: "act"}
			if st.Enabled && st.Note == "" {
				want.Title = "card title"
			}
			if !st.Enabled {
				want.Class = "act off"
			}
			if g := got[f][st.ID]; g == nil || *g != want {
				t.Errorf("%s %s: got %+v, want %+v", f, st.ID, g, want)
			}
		}
	}
	for id, b := range got["hidden"] {
		if b != nil {
			t.Errorf("%s is drawn though the row does not offer it: %+v", id, b)
		}
	}
	if got["blocked"]["continue"] != nil {
		t.Errorf("continue is drawn on a row with no question gate: %+v", got["blocked"]["continue"])
	}
}

func TestIntakeAndBriefButtonsFollowTheServedStates(t *testing.T) {
	specs, rows, byID, states := wireFixtures(t)
	withIntake := func(name, source string) map[string]any {
		r := map[string]any{}
		for k, v := range rows[name] {
			r[k] = v
		}
		payload := map[string]any{}
		if p, ok := r["payload"].(map[string]any); ok {
			for k, v := range p {
				payload[k] = v
			}
		}
		payload["intake"] = map[string]any{"seq": 1, "intent": "review", "state": "briefing", "source": source}
		r["payload"] = payload
		return r
	}
	in := map[string]any{"specs": specs, "rows": map[string]any{
		"intake_waiting_cli":     withIntake("intake_waiting_cli", "cli"),
		"intake_waiting_session": withIntake("intake_waiting_session", "session"),
		"brief_off":              rows["brief_off"],
		"tracked":                rows["tracked"],
	}}
	type result struct {
		Intake []wireButton `json:"intake"`
		Brief  []wireButton `json:"brief"`
		Posts  []string     `json:"posts"`
	}
	var got map[string]result
	nodeRun(t, buttonPrelude(t)+treePrelude, append(buttonFuncs, "isSet", "pl", "intakeWaiting", "intakeLines", "intakeBlock", "refreshBriefButton"), `useSpecs(input.specs);
(async () => {
  const out = {};
  for (const [name, row] of Object.entries(input.rows)) {
    fetched = [];
    const intake = buttonsOf(intakeBlock(row));
    const brief = buttonsOf({ tag: "div", children: [].concat(refreshBriefButton(row)) });
    for (const b of [...intake, ...brief]) if (!b.disabled) await b.click({ stopPropagation() {} });
    const strip = ({ click, ...b }) => b;
    out[name] = { intake: intake.map(strip), brief: brief.map(strip), posts: fetched.map((f) => f.url + " " + JSON.stringify(f.body)) };
  }
  process.stdout.write(JSON.stringify(out));
})();`, in, &got)
	for name, r := range got {
		var wantIntake, wantBrief []wireButton
		var wantPosts, briefPosts []string
		for _, st := range states[name] {
			if st.ID != "intake_without" && st.ID != "intake_cancel" && st.ID != "brief_refresh" {
				continue
			}
			b := wireButton{Label: cmpOr(st.Label, byID[st.ID].Label), Disabled: !st.Enabled, Title: wantTitle(byID[st.ID], st), Class: "act"}
			if !st.Enabled {
				b.Class = "act off"
			}
			if st.Enabled {
				body := `{"source":"cockpit"`
				if st.Basis != "" {
					body += `,"basis":"` + st.Basis + `"`
				}
				post := "/api/units/github/acme%2Fapi%237/" + st.ID + " " + body + "}"
				if st.ID == "brief_refresh" {
					briefPosts = append(briefPosts, post)
				} else {
					wantPosts = append(wantPosts, post)
				}
			}
			if st.ID == "brief_refresh" {
				wantBrief = append(wantBrief, b)
			} else if strings.HasPrefix(name, "intake_") {
				wantIntake = append(wantIntake, b)
			}
		}
		if len(wantIntake) == 0 && strings.HasPrefix(name, "intake_") {
			t.Fatalf("%s: no intake states", name)
		}
		wantPosts = append(wantPosts, briefPosts...)
		if !slices.Equal(r.Intake, wantIntake) || !slices.Equal(r.Brief, wantBrief) {
			t.Errorf("%s:\n got intake %+v brief %+v\nwant intake %+v brief %+v", name, r.Intake, r.Brief, wantIntake, wantBrief)
		}
		if !slices.Equal(r.Posts, wantPosts) {
			t.Errorf("%s posts:\n got %q\nwant %q", name, r.Posts, wantPosts)
		}
	}
	if b := got["intake_waiting_session"].Intake; len(b) != 2 || !b[0].Disabled || !strings.Contains(b[0].Title, "turn by hand") || b[1].Disabled {
		t.Errorf("the session intake offers %+v", b)
	}
}

func TestLoadRefreshesTheSpecsAndKeepsThemWhenTheFetchFails(t *testing.T) {
	var got struct {
		Fetches  []string `json:"fetches"`
		Versions []string `json:"versions"`
		Buttons  []string `json:"buttons"`
		Error    string   `json:"error"`
	}
	nodeRun(t, `const state = { specs: { version: "", byId: {} }, rows: [], detail: null, error: "", busy: new Set() };
`+keyJS(t)+noPlugins+noViews+noUpdate+`const CARD_GROUPS = new Set(["turn", "intake", "brief"]);
const focusSlot = (n) => n;
function h(tag, attrs, ...c) { return { tag, attrs: attrs || {}, text: c.flat().join("") }; }
const fetches = []; let listed = "v1"; let actionsFail = false; let detailVersion = "v1";
const row = (v) => ({ label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, specs_version: v, actions: [{ id: "open", enabled: true }] });
const getJSON = async (url) => {
  if (url === "/api/rows") return { rows: [row(listed)] };
  if (url === "/api/health") return {};
  if (url === "/api/actions") { fetches.push(url); if (actionsFail) throw new Error("boom"); return { version: listed, specs: [{ id: "open", label: "Open", surfaces: ["row"] }] }; }
  throw new Error(url);
};
const loadActivity = async () => {}, loadExploreRecent = async () => {}, loadPlan = async () => {}, render = () => {};
const loadDetail = async () => { state.detail = { row: row(detailVersion) }; };
`, []string{"specsStale", "loadSpecs", "refreshSpecs", "actionStates", "actionTitle", "actionButton", "actionButtons", "load"}, `(async () => {
  const versions = [];
  await load(); versions.push(state.specs.version);
  listed = "v2"; detailVersion = "v2"; actionsFail = true;
  await load(); versions.push(state.specs.version);
  process.stdout.write(JSON.stringify({ fetches, versions, buttons: actionButtons(state.rows[0], "row").map((b) => b.text), error: state.error }));
})();`, nil, &got)
	if len(got.Fetches) != 2 || !slices.Equal(got.Versions, []string{"v1", "v1"}) || !slices.Equal(got.Buttons, []string{"Open"}) || !strings.Contains(got.Error, "Could not load the actions: boom") {
		t.Errorf("%+v", got)
	}
}

func TestOnlyATextInputOrAnIntakeTakesTheGuidance(t *testing.T) {
	var got []struct {
		Name     string         `json:"name"`
		Body     map[string]any `json:"body"`
		Guidance string         `json:"guidance"`
	}
	nodeRun(t, buttonPrelude(t), buttonFuncs, `useSpecs({ version: "v1", specs: [
  { id: "mute", input: { kind: "duration", default: "2d" } },
  { id: "guide", input: { kind: "text" } },
  { id: "review", intake: true },
  { id: "hold" },
] });
const row = { label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, actions: [] };
promptAnswer = "2d";
(async () => {
  const out = [];
  for (const name of ["mute", "hold", "guide", "review"]) {
    state.guidance["github:a/b#1"] = "look at the retry";
    fetched = [];
    await act(row, name, { guidance: "look at the retry" });
    out.push({ name, body: fetched[0].body, guidance: state.guidance["github:a/b#1"] || "" });
  }
  process.stdout.write(JSON.stringify(out));
})();`, nil, &got)
	want := map[string]bool{"mute": false, "hold": false, "guide": true, "review": true}
	for _, g := range got {
		sent := g.Body["guidance"] == "look at the retry"
		if sent != want[g.Name] || (g.Guidance == "") != sent {
			t.Errorf("%s: body %v, guidance box %q", g.Name, g.Body, g.Guidance)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%d actions ran", len(got))
	}
}

func TestSpecsLoadWithAnEmptyInventory(t *testing.T) {
	var got struct {
		Fetches []string `json:"fetches"`
		Version string   `json:"version"`
	}
	nodeRun(t, noPlugins+noViews+noUpdate+`const state = { specs: { version: "", byId: {} }, rows: [], detail: null, error: "" };
const fetches = [];
const getJSON = async (url) => {
  if (url === "/api/rows") return { rows: [] };
  if (url === "/api/health") return {};
  if (url === "/api/actions") { fetches.push(url); return { version: "v1", specs: [{ id: "track", label: "Track", surfaces: ["explore"] }] }; }
  throw new Error(url);
};
const loadActivity = async () => {}, loadExploreRecent = async () => {}, loadPlan = async () => {}, loadDetail = async () => {}, render = () => {};
`, []string{"specsStale", "loadSpecs", "refreshSpecs", "load"}, `(async () => {
  await load();
  await load();
  process.stdout.write(JSON.stringify({ fetches, version: state.specs.version }));
})();`, nil, &got)
	if len(got.Fetches) != 1 || got.Version != "v1" {
		t.Errorf("%+v", got)
	}
}

func TestTheDetailShowsTheServedBriefRefreshWithoutABrief(t *testing.T) {
	var got []string
	treeH := `function h(tag, attrs, ...c) { const kids = c.flat().filter((x) => x !== null && x !== undefined && x !== false); return { tag, attrs: attrs || {}, kids, text: kids.map((x) => (typeof x === "object" ? x.text : String(x))).join("") }; }`
	prelude := treeH + "\n" + strings.SplitN(buttonPrelude(t), "\n", 2)[1]
	nodeRun(t, prelude, append(buttonFuncs, "refreshBriefButton", "briefBlock"), `useSpecs({ version: "v1", specs: [{ id: "brief_refresh", label: "Refresh brief", group: "brief", surfaces: ["detail"] }] });
const row = (st) => ({ label: "a/b#1", unit: { provider: "github", key: "a/b#1" }, actions: st ? [st] : [] });
const text = (el) => (el ? JSON.stringify(el) : "");
process.stdout.write(JSON.stringify([
  text(briefBlock(null, row({ id: "brief_refresh", reason: "the brief is off; set brief.enabled in the config" }))),
  text(briefBlock({ state: "pending" }, row({ id: "brief_refresh", enabled: true }))),
  text(briefBlock(null, row(null))),
]));`, nil, &got)
	if len(got) != 3 || !strings.Contains(got[0], "the brief is off") || !strings.Contains(got[1], "Refresh brief") || got[2] != "" {
		t.Errorf("%q", got)
	}
}

func TestCockpitPlanListsPendingFirstWithTheTimeLeft(t *testing.T) {
	type item struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
		Default  string `json:"default,omitempty"`
		Deadline string `json:"deadline,omitempty"`
	}
	var got struct {
		Groups []struct {
			Decision string   `json:"decision"`
			IDs      []string `json:"ids"`
		} `json:"groups"`
		When  map[string]string `json:"when"`
		Count int               `json:"count"`
	}
	in := []item{
		{ID: "removal", Decision: "blocked"},
		{ID: "manual", Decision: "pending", Default: "wait"},
		{ID: "later", Decision: "pending", Default: "run", Deadline: "2026-10-14T15:04:10Z"},
		{ID: "ran", Decision: "run"},
		{ID: "soon", Decision: "pending", Default: "run", Deadline: "2026-10-14T15:00:42Z"},
		{ID: "past", Decision: "pending", Default: "run", Deadline: "2026-10-14T14:59:00Z"},
	}
	src := appJS(t)
	prelude := jsConst(t, src, "PLAN_ORDER") + `const state = { plan: { items: [], loaded: true } };`
	nodeRun(t, prelude, []string{"planGroups", "planWhen", "planCount"}, `
state.plan.items = input;
const now = Date.parse("2026-10-14T15:00:00Z");
process.stdout.write(JSON.stringify({
  groups: planGroups(input).map((g) => ({ decision: g.decision, ids: g.items.map((it) => it.id) })),
  when: Object.fromEntries(input.map((it) => [it.id, planWhen(it, now)])),
  count: planCount(),
}));`, in, &got)
	if len(got.Groups) != 2 || got.Groups[0].Decision != "pending" || !slices.Equal(got.Groups[0].IDs, []string{"past", "soon", "later", "manual"}) ||
		got.Groups[1].Decision != "blocked" || !slices.Equal(got.Groups[1].IDs, []string{"removal"}) || got.Count != 5 {
		t.Errorf("groups %+v, count %d", got.Groups, got.Count)
	}
	for id, want := range map[string]string{"soon": "runs in 42 s", "later": "runs in 4m 10s", "past": "running", "manual": "waits for you", "removal": "blocked"} {
		if got.When[id] != want {
			t.Errorf("%s: %q, want %q", id, got.When[id], want)
		}
	}
}

func TestCockpitPlanActionsPostToThePlanOrActOnTheUnit(t *testing.T) {
	var got struct {
		Posts []json.RawMessage `json:"posts"`
		Acts  []string          `json:"acts"`
	}
	src := appJS(t)
	prelude := jsConst(t, src, "PLAN_ACTS") + jsConst(t, src, "itemLabel") + jsConst(t, src, "planName") + jsConst(t, src, "unitKey") + `
const API_VERSION = "1";
const state = { busy: new Set() };
const posts = [], acts = [];
const render = () => {}, setNotice = () => {}, loadPlan = async () => {}, apiError = (o, f) => o.error || f;
const prompt = () => "look at the retry";
const act = async (row, id) => { acts.push(id + " " + row.label); };
const fetch = async (url, opts) => { posts.push(JSON.parse(opts.body)); return { ok: true, json: async () => ({ items: [], errors: [] }) }; };
`
	nodeRun(t, prelude, []string{"actOnPlan"}, `
(async () => {
  const it = { id: "review-follow-up/github/acme%2Fapi%237/aa", workflow: "review-follow-up", unit: { provider: "github", key: "acme/api#7" }, label: "acme/api#7", actions: [] };
  await actOnPlan(it, "plan_apply");
  await actOnPlan(it, "plan_dismiss");
  await actOnPlan(it, "guide");
  await actOnPlan(it, "unblock");
  await actOnPlan({ ...it, unit: { provider: "session", key: "term_1" }, label: "session:term_1" }, "trust_run");
  await actOnPlan({ ...it, unit: { provider: "slack", key: "T1/C1/171.1" }, label: "#general", title: "Launch plan" }, "trust_run");
  process.stdout.write(JSON.stringify({ posts, acts }));
})();`, nil, &got)
	want := []string{
		`{"ids":["review-follow-up/github/acme%2Fapi%237/aa"],"action":"apply"}`,
		`{"ids":["review-follow-up/github/acme%2Fapi%237/aa"],"action":"dismiss"}`,
		`{"ids":["review-follow-up/github/acme%2Fapi%237/aa"],"action":"guide","guidance":"look at the retry"}`,
	}
	if len(got.Posts) != len(want) {
		t.Fatalf("posts %s", got.Posts)
	}
	for i, p := range got.Posts {
		if string(p) != want[i] {
			t.Errorf("post %d: %s, want %s", i, p, want[i])
		}
	}
	if !slices.Equal(got.Acts, []string{"unblock acme/api#7", "trust_run session:term_1", "trust_run #general · Launch plan"}) {
		t.Errorf("acts %q", got.Acts)
	}
}

func TestCockpitPlanItemNamesItsUnitByLabelAndTitle(t *testing.T) {
	var got struct {
		Unit  string `json:"unit"`
		Title string `json:"title"`
		Href  string `json:"href"`
	}
	src := appJS(t)
	prelude := treeJS + jsConst(t, src, "itemLabel") + jsConst(t, src, "unitKey") + `
const state = { busy: new Set(), planSpecs: {} };
const planWhen = () => "waits for you", actOnPlan = () => {}, actionTitle = () => "";
`
	nodeRun(t, prelude, []string{"planButtons", "planItem"}, `
const li = planItem({ id: "slack-follow-up/slack/T1%2FC1%2F171.1/ee", workflow: "slack-follow-up", decision: "pending", default: "wait",
  unit: { provider: "slack", key: "T1/C1/171.1" }, label: "#general", title: "Launch plan for the export", link: "https://chat.example/T1/C1/171.1", actions: [] });
const unit = byClass(li, "plan-unit")[0];
const link = all(unit, (x) => x.tag === "a")[0];
process.stdout.write(JSON.stringify({ unit: text(unit), title: text(byClass(li, "plan-title")[0]), href: link ? link.attrs.href : "" }));`, nil, &got)
	if got.Unit != "#general" || got.Title != "Launch plan for the export" || got.Href != "https://chat.example/T1/C1/171.1" {
		t.Fatalf("%+v", got)
	}
}

func TestAClosedSessionNamesItsSession(t *testing.T) {
	prelude := `function h(tag, attrs, ...c) { return [tag, c.flat(Infinity).filter((x) => x !== null && x !== undefined)]; }
function stamp(t) { return t; }
function age() { return "2h 0m"; }
`
	c := api.ClosedSession{Session: "term_1", At: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), Reason: "its pull request merged", Resume: "claude --resume b"}
	var got string
	nodeRun(t, prelude, []string{"closedItem"}, `process.stdout.write(JSON.stringify(JSON.stringify(closedItem(input))));`, c, &got)
	if !strings.Contains(got, `"term_1"`) || !strings.Contains(got, "claude --resume b") {
		t.Errorf("closed item %s", got)
	}
}

func TestActivityNamesAnEventByItsUnitsLabel(t *testing.T) {
	type in struct {
		Label string         `json:"label,omitempty"`
		Event map[string]any `json:"event"`
	}
	cases := []in{
		{"acme/api#7", map[string]any{"unit": map[string]any{"provider": "github", "key": "acme/api#7"}}},
		{"#general", map[string]any{"unit": map[string]any{"provider": "chat", "key": "C1/1.1"}}},
		{"", map[string]any{}},
	}
	var got [][2]any
	nodeRun(t, keyJS(t)+jsConst(t, appJS(t), "eventRef"), nil,
		`process.stdout.write(JSON.stringify(input.map((it) => [eventRef(it), activityRepos(it)])));`+jsFunction(t, appJS(t), "activityRepos"), cases, &got)
	want := `[["acme/api#7",["acme/api"]],["#general",[]],["",[]]]`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
}
