package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/internal/readmodel"
)

var stalledKinds = []string{"draft", "checks", "changes", "merge", "reply", "reviewer_back", "review", "no_reviewer", "conflicts"}

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
	rows[0]["round_gate"] = map[string]any{"deadline": iso(now.Add(2 * time.Hour))}
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

// draftGates are the gates of a task carrying d.
func draftGates(d api.Draft) []api.GateView { return []api.GateView{api.DraftGate(d)} }

// quietClose is a pending item of a workflow that waits for you to apply it on task t-06bec1c03a26fadf, derived
// from a thread, resolving its two open items as moot and posting nothing.
func quietClose() api.PlanItem {
	return api.PlanItem{ID: "fake-close/task/t-06bec1c03a26fadf/ab", Workflow: "fake-close", Summary: "Close a quiet task; posts nothing",
		Decision: "pending", Default: "wait", Reason: "waits for you", Unit: unit.Unit{Provider: "task", Key: "t-06bec1c03a26fadf"},
		Label: "task:t-06bec1c03a26fadf", Steps: []string{"items.resolve"},
		Does: []api.PlanStep{{Use: "items.resolve", Reach: "local", Says: "Resolve its open items as {resolution}",
			Args:  map[string]string{"resolution": "moot", "resolution_says": "resolved as moot"},
			Items: []string{"Ana asks for the retry numbers", "Bea asks when it ships"}}},
		Target: &api.PlanTarget{Task: "t-06bec1c03a26fadf", Title: "Answer Ana about the retries",
			Units: []api.PlanUnit{{Label: "#general", Link: "https://chat.example/c1"}, {Label: "#ops"}}},
		Why:     api.Why{Trigger: "sweep", Matched: []api.Fact{{Name: "task.items_open", Value: "2"}}},
		Actions: []api.ActionState{}}
}

// bundledCatalog is the catalog the bundled GitHub and Slack plugins serve together, their facets merged as
// the daemon merges them.
func bundledCatalog(t *testing.T) json.RawMessage {
	t.Helper()
	c := readmodel.BuildCatalog(bundledPresenter{Catalog: psource.BundledCatalog(string(unit.GitHub))}, bundledPresenter{Catalog: psource.BundledCatalog("slack")})
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(brandtest.Expand(string(raw)))
}

func tagRows() []map[string]any {
	rows := lensRows()
	for i, tags := range [][]string{{"change:fix", "lang:go", "urgent"}, {"lang:go", "lang:sql"}, nil, {"change:refac", "domain:billing"}, {"lang:python"}} {
		if tags == nil {
			continue
		}
		b, _ := rows[i]["brief"].(map[string]any)
		if b == nil {
			b = map[string]any{}
			rows[i]["brief"] = b
		}
		b["tags"] = tags
	}
	return rows
}

var tasklensNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// servedViews is GET /api/views as the daemon serves the bundled profile: its views, its task facets and what
// the sort menu calls a view's own order.
func servedViews(t *testing.T) api.ViewsCatalog {
	t.Helper()
	def, problems := intentload.BundledViews(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	tasks, problems := intentload.BundledTasks(os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	cat := api.ViewsCatalog{Facets: catalog.TaskFacets(def.Facets, tasks, []string{"calls", "computer"}), DefaultSort: def.DefaultSort, Lens: catalog.LensWords(def.Lens)}
	for _, v := range def.Views {
		cat.Views = append(cat.Views, api.TaskViewSpec{ID: v.ID, Label: v.Label, Summary: v.Summary, Empty: v.Empty, When: v.WhenText, Sort: v.SortText, Over: v.Over, Group: v.Group})
	}
	return cat
}

// slackFacets are the Slack plugin's facets the GitHub plugin does not declare too.
func slackFacets(t *testing.T) []api.FacetSpec {
	t.Helper()
	var github struct {
		Facets []api.FacetSpec `json:"facets"`
	}
	if err := json.Unmarshal([]byte(servedCatalog(t)), &github); err != nil {
		t.Fatal(err)
	}
	var out []api.FacetSpec
	for _, f := range psource.BundledCatalog("slack").Facets() {
		if !slices.ContainsFunc(github.Facets, func(g api.FacetSpec) bool { return g.ID == f.ID }) {
			out = append(out, f)
		}
	}
	return out
}

// taskSpecs are the specs the daemon counts a task query with: the plugins' facets, then the profile's, by id.
func taskSpecs(t *testing.T, views api.ViewsCatalog) map[string]api.FacetSpec {
	t.Helper()
	var github struct {
		Facets []api.FacetSpec `json:"facets"`
	}
	if err := json.Unmarshal([]byte(servedCatalog(t)), &github); err != nil {
		t.Fatal(err)
	}
	specs := map[string]api.FacetSpec{}
	for _, f := range slices.Concat(github.Facets, slackFacets(t), views.Facets) {
		specs[f.ID] = f
	}
	return specs
}

// mixedTasks are Needs me's tasks: two Slack conversations and two pull requests, with the facets the daemon
// serves on each.
func mixedTasks() []api.TaskView {
	return []api.TaskView{
		task("s-1", "next", func(e *api.TaskView) {
			e.Title, e.Category, e.Units, e.UnitLabels = "Answer the deploy question", "ready", []unit.Unit{slackUnit}, []string{"#general thread"}
			e.Facets = map[string][]string{"source": {"slack"}, "where": {"general"}, "conversation": {"thread"}, "waits-on": {"me"}, "turn": {"me"}, "state": {"next"}, "category": {"ready"}}
		}),
		task("s-2", "next", func(e *api.TaskView) {
			e.Title, e.Category = "Close the lunch poll", "ready"
			e.Facets = map[string][]string{"source": {"slack"}, "where": {"random"}, "waits-on": {"me"}, "turn": {"me"}, "state": {"next"}, "category": {"ready"}}
		}),
		task("g-1", "doing", func(e *api.TaskView) {
			e.Title, e.Category, e.Units = "Fix the flaky upload test", "active", []unit.Unit{prUnit}
			e.Facets = map[string][]string{"source": {"github"}, "where": {"acme/api"}, "ci": {"failure"}, "kind": {"checks"}, "wait": {"me"}, "waits-on": {"me"}, "turn": {"me"},
				"state": {"doing"}, "category": {"active"}, "tag": {"lang:go"}}
		}),
		task("g-2", "doing", func(e *api.TaskView) {
			e.Title, e.Category, e.Units = "Review the general settings page", "active", []unit.Unit{prUnit2}
			e.Facets = map[string][]string{"source": {"github"}, "where": {"acme/web"}, "ci": {"success"}, "kind": {"review"}, "wait": {"reviewer"}, "waits-on": {"bia"}, "turn": {"others"},
				"state": {"doing"}, "category": {"active"}}
		}),
	}
}

// mineRows are lensRows listed by My PRs.
func mineRows() []map[string]any {
	rows := wireRows("me", tasklensNow, lensRows()...)
	for _, r := range rows {
		r["views"] = []string{"mine"}
	}
	return rows
}

// viewsCatalog is GET /api/views with the core views the daemon serves.
func viewsCatalog() api.ViewsCatalog {
	views := []api.TaskViewSpec{{ID: "focus", Label: "Focus"}, {ID: "inbox", Label: "Inbox"}, {ID: "outbox", Label: "Outbox"}, {ID: "fits-now", Label: "Fits now"},
		{ID: "waiting", Label: "Waiting"}, {ID: "projects", Label: "Projects"}, {ID: "someday", Label: "Someday"}, {ID: "logbook", Label: "Logbook"}}
	for i := range views {
		views[i].Rail = true
	}
	return api.ViewsCatalog{
		Views:    views,
		Areas:    []string{"OSS", "Hiring"},
		Contexts: []string{"@decide", "@reply"},
		Verbs: []api.VerbSpec{{Verb: "approve", Handler: "approve", Label: "Approve"}, {Verb: "edit", Handler: "edit", Label: "Edit"}, {Verb: "dismiss", Handler: "drop", Label: "Discard"},
			{Verb: "retry", Handler: "retry", Label: "Retry"}},
	}
}

func task(id, state string, mod func(*api.TaskView)) api.TaskView {
	e := api.TaskView{ID: id, State: state, Title: "Task " + id, Units: []unit.Unit{}, Verbs: []api.VerbState{},
		Created: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Updated: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	if mod != nil {
		mod(&e)
	}
	return e
}

// servedCatalog is GET /api/catalog as the daemon serves the GitHub
// provider's lists.
func servedCatalog(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../readmodel/prrow/testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// wireRows turns test rows written with every field at the top into rows as
// the daemon serves them: the pull request's fields in the
// payload, the situation in the summary, and the facets the GitHub provider
// derives for viewer at now.
func wireRows(viewer string, now time.Time, rows ...map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = wireRow(viewer, now, r)
	}
	return out
}

func wireRow(viewer string, now time.Time, old map[string]any) map[string]any {
	r := map[string]any{}
	payload := map[string]any{}
	for k, v := range old {
		switch {
		case k == "ref":
			r["ref"] = v
			ref := v.(map[string]any)
			label := str(ref["owner"]) + "/" + str(ref["repo"]) + "#" + fmt.Sprint(ref["number"])
			if old["label"] == nil {
				r["label"] = label
			}
			if old["unit"] == nil {
				r["unit"] = map[string]any{"provider": "github", "key": label}
			}
		case slices.Contains(payloadKeys, k):
			payload[k] = v
		case k == "waiting_on" || k == "since" || k == "stalled" || k == "reason":
		default:
			r[k] = v
		}
	}
	if old["unit"] != nil {
		r["unit"] = old["unit"]
	}
	if len(payload) > 0 {
		payload["viewer"] = viewer
		r["payload"] = payload
	}
	kind := str(old["kind"])
	r["summary"] = map[string]any{"waits_on": str(old["waiting_on"]), "since": cmpOr(str(old["since"]), "0001-01-01T00:00:00Z"), "stalled": old["stalled"] == true,
		"closed": kind == "done", "reason": str(old["reason"]), "line": str(old["reason"])}
	r["facets"] = facetsOf(viewer, now, old)
	return r
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

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

var prUnit = unit.Unit{Provider: "github", Key: "acme/api#8"}

var slackUnit = unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000000.000100"}

type bundledPresenter struct {
	bundledProvider
	psource.Catalog
}

var prUnit2 = unit.Unit{Provider: unit.GitHub, Key: "acme/web#9"}

var payloadKeys = []string{"author", "repo", "head", "approved", "reasons", "workspace", "override", "brief", "intake", "facts"}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// facetsOf mirrors the GitHub provider's facets for a test row.
func facetsOf(viewer string, now time.Time, r map[string]any) map[string][]string {
	author, kind := str(r["author"]), str(r["kind"])
	views := strs(r["views"])
	brief, ws, o, facts := obj(r["brief"]), obj(r["workspace"]), obj(r["override"]), obj(r["facts"])
	mine := viewer != "" && strings.EqualFold(author, viewer)
	resolve := func(w string) string {
		if w != "author" {
			return w
		}
		if mine {
			return "me"
		}
		return author
	}
	var who []string
	cats := map[string]bool{}
	for _, w := range strings.Split(str(r["waiting_on"]), ", ") {
		if w == "" || w == "nobody" {
			continue
		}
		who = append(who, resolve(w))
		switch {
		case resolve(w) == "me":
			cats["me"] = true
		case w == "author":
			cats["author"] = true
		default:
			cats["reviewer"] = true
		}
	}
	var wait []string
	if kind == "loop_working" || kind == "loop_waiting" {
		wait = []string{"loop"}
	} else {
		for _, c := range []string{"me", "loop", "reviewer", "author", "nobody"} {
			if cats[c] {
				wait = append(wait, c)
			}
		}
		if len(wait) == 0 {
			wait = []string{"nobody"}
		}
	}
	f := map[string][]string{"kind": {kind}, "wait": wait, "waits-on": who}
	if repo := str(r["repo"]); repo != "" {
		f["where"] = []string{repo}
	}
	if author != "" {
		f["author"] = []string{author}
		if mine {
			f["author"] = []string{"me", author}
		}
	}
	f["type"] = []string{cmpOr(str(brief["kind"]), "none")}
	f["risk"] = []string{oneOf(str(brief["risk"]), "low", "medium", "high")}
	f["size"] = []string{oneOf(str(brief["size"]), "xs", "s", "m", "l", "xl")}
	f["status"] = []string{cmpOr(str(ws["status"]), "none")}
	f["tag"] = strs(brief["tags"])
	mute := obj(r["mute"])
	mutedUntil, _ := time.Parse(time.RFC3339, str(mute["until"]))
	flags := map[string]bool{"stalled": slices.Contains(views, "stalled"), "needs-me": slices.Contains(views, "needs-me"), "in-loop": slices.Contains(views, "loop"),
		"approved": r["approved"] == true, "paused": o["hold"] == true, "muted": mute["until_state_change"] == true || mutedUntil.After(now),
		"adopted": o["adopted"] == true, "tracked": o["tracked"] == true}
	f["is"] = []string{}
	for _, k := range []string{"stalled", "needs-me", "in-loop", "approved", "paused", "muted", "adopted", "tracked"} {
		if flags[k] {
			f["is"] = append(f["is"], k)
		}
	}
	checks := obj(facts["checks"])
	f["ci"] = append([]string{cmpOr(strings.ToLower(str(checks["state"])), "none")}, strs(checks["failing"])...)
	if c := str(facts["created_at"]); isSetTime(c) {
		f["opened"] = []string{c}
	}
	f["scope"] = strs(r["reasons"])
	if s := str(r["since"]); isSetTime(s) {
		f["age"] = []string{s}
	}
	var due []string
	for _, n := range anys(facts["next"]) {
		if at := str(obj(n)["at"]); isSetTime(at) {
			due = append(due, at)
		}
	}
	if p := obj(r["round_gate"]); isSetTime(str(p["deadline"])) {
		due = append(due, str(p["deadline"]))
	}
	if len(due) > 0 {
		f["due"] = due
	}
	lane := "author"
	switch {
	case slices.Contains(views, "needs-me"):
		lane = "me"
	case slices.Contains(views, "loop"):
		lane = "loop"
	case kind == "merge":
		lane = "merge"
	case kind == "review" || kind == "reviewer_back":
		lane = "reviewer"
	case slices.Contains(who, "me"):
		lane = "me"
	}
	f["lane"] = []string{lane}
	return f
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

type bundledProvider = readmodel.Provider

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func strs(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, str(e))
		}
		return out
	}
	return nil
}

func isSetTime(s string) bool { return s != "" && !strings.HasPrefix(s, "0001-01-01") }

func anys(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
		}
		return out
	}
	return nil
}

func oneOf(v string, allowed ...string) string {
	if slices.Contains(allowed, v) {
		return v
	}
	return "none"
}
