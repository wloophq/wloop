package intent

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func defaultLists() []byte {
	var out []byte
	for _, f := range []string{TasksFile, ViewsFile} {
		b, err := os.ReadFile("../../profiles/default/" + f)
		if err != nil {
			panic(err)
		}
		out = append(append(out, b...), '\n')
	}
	return out
}

// splitLists is src, the tables of tasks.toml and views.toml together,
// as the two files: [verbs.*], [states.*], [props.*], [owed_by], [chat] and
// [attribution] tables in tasks.toml, the rest in views.toml.
func splitLists(src string) map[string]string {
	out := map[string]string{}
	file := ViewsFile
	for _, line := range strings.SplitAfter(src, "\n") {
		if strings.HasPrefix(line, "empty =") || strings.HasPrefix(line, "default_sort =") {
			out[ViewsFile] += line
			continue
		}
		if strings.HasPrefix(line, "[") {
			file = ViewsFile
			if strings.HasPrefix(line, "[verbs") || strings.HasPrefix(line, "[states") || strings.HasPrefix(line, "[props") || strings.HasPrefix(line, "[owed_by") || strings.HasPrefix(line, "[chat]") ||
				strings.HasPrefix(line, "[attribution]") {
				file = TasksFile
			}
		}
		out[file] += line
	}
	return out
}

// lists are the views and the verbs' texts of tasks.toml.
type lists struct {
	ViewsDef
	Verbs map[string]VerbText
}

// label is view id's label; "" when the profile declares none.
func (l lists) label(id string) string {
	v, _ := l.View(id)
	return v.Label
}

// key is view id's key; "" when it has none.
func (l lists) key(id string) string {
	v, _ := l.View(id)
	return v.Key
}

// step is the label of the review step that walks view id; "" when none does.
func (l lists) step(id string) string {
	for _, st := range l.Review {
		if st.View == id {
			return st.Label
		}
	}
	return ""
}

func loadListsWith(t *testing.T, bundled, user string) (lists, string) {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		for file, src := range splitLists(user) {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	fsys := fstest.MapFS{}
	for file, src := range splitLists(bundled) {
		fsys[file] = &fstest.MapFile{Data: []byte(src)}
	}
	texts, tasks, problems := loadViews(layers, fsys, nil)
	return lists{texts, tasks.Verbs}, problems.Error()
}

func TestEveryVerbAndViewIsWorded(t *testing.T) {
	views, tasks, problems := loadViews(nil, os.DirFS("../../profiles/default"), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	texts := lists{views, tasks.Verbs}
	for _, v := range tasks.VerbOrder {
		w := texts.Verbs[v]
		if w.Label == "" || w.Summary == "" {
			t.Errorf("verb %s: label %q, summary %q", v, w.Label, w.Summary)
		}
		if slices.Contains(builtin.InputHandlers, w.Handler) && w.Input == "" {
			t.Errorf("verb %s asks for a value and says nothing", v)
		}
	}
	for _, v := range texts.Views {
		if v.Label == "" || v.Summary == "" || v.Empty == "" {
			t.Errorf("view %s: label %q, summary %q, empty %q", v.ID, v.Label, v.Summary, v.Empty)
		}
	}
	for _, st := range texts.Review {
		if st.Label == "" {
			t.Errorf("review step %s: no label", st.View)
		}
	}
}

func TestListTextsComeFromTheProfile(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.done]\nsummary = \"Close it\"\n\n[views.needs-me]\nlabel = \"Now\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	if got := texts.Verbs["done"]; got.Label != "Done" || got.Summary != "Close it" {
		t.Errorf("done: %+v", got)
	}
	if got := texts.label("needs-me"); got != "Now" {
		t.Errorf("needs-me view: %q", got)
	}
}

func TestAUserVerbAndViewNeedTheirLabels(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.dne]\nsummary = \"x\"\n\n[views.later]\nwhen = \"true\"\n")
	for _, want := range []string{
		"verbs.dne.label: is required",
		"views.later.label: is required",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["done"].Summary == "x" {
		t.Error("a new verb changed done")
	}
}

func TestAViewSaysWhatItListsAndWhatItShowsEmpty(t *testing.T) {
	bundled := "[views.inbox]\nlabel = \"Inbox\"\nsummary = \"What nobody clarified yet.\"\nwhen = \"task.category == 'inbox'\"\nempty = \"Inbox zero.\"\n\n" +
		"[views.waiting]\nlabel = \"Waiting\"\nsummary = \"What others owe.\"\nwhen = \" task.owed_by != 'me' \"\ngroup = \"task.owed_by\"\nsort = \"-task.since_movement\"\n"
	tasks, err := os.ReadFile("../../profiles/default/" + TasksFile)
	if err != nil {
		t.Fatal(err)
	}
	src := string(tasks) + "\nempty = \"Nothing in {{.label}} right now.\"\n\n" + bundled
	texts, problems := loadListsWith(t, src, "[views.waiting]\nlabel = \"Owed\"\n\n[views.later]\nlabel = \"Later\"\nsummary = \"Low priority.\"\nwhen = \"true\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	inbox, _ := texts.View("inbox")
	waiting, _ := texts.View("waiting")
	later, _ := texts.View("later")
	if inbox.Summary != "What nobody clarified yet." || inbox.Empty != "Inbox zero." {
		t.Errorf("inbox %+v", inbox)
	}
	if waiting.Summary != "What others owe." || waiting.Empty != "Nothing in Owed right now." || waiting.WhenText != "task.owed_by != 'me'" || waiting.SortText != "-task.since_movement" {
		t.Errorf("waiting %+v", waiting)
	}
	if later.Summary != "Low priority." || later.Empty != "Nothing in Later right now." {
		t.Errorf("later %+v", later)
	}
	texts, problems = loadListsWith(t, src, "empty = \"No {{.label}}.\"\n\n[views.later]\nlabel = \"Later\"\nwhen = \"true\"\n")
	if !strings.Contains(problems, "views.later.summary: is required") {
		t.Errorf("no summary problem in:\n%s", problems)
	}
	if _, ok := texts.View("later"); ok {
		t.Error("a view without its summary loaded")
	}
	if w, _ := texts.View("waiting"); w.Empty != "No Waiting." {
		t.Errorf("the user's empty: %q", w.Empty)
	}
	_, problems = loadListsWith(t, src, "empty = \"No {{.labl}}.\"\n")
	if !strings.Contains(problems, "empty:") {
		t.Errorf("no empty problem in:\n%s", problems)
	}
}

func TestAnEmptyListTextKeepsTheBundledOne(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.next]\nlabel = \"\"\n\n[views.inbox]\nlabel = \"\"\n")
	for _, want := range []string{"verbs.next.label: is required", "views.inbox.label: is required"} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if texts.Verbs["next"].Label != "Next" || texts.label("inbox") != "Inbox" {
		t.Errorf("bundled texts lost: %+v %q", texts.Verbs["next"], texts.label("inbox"))
	}
}

func TestAVerbThatAsksNeedsItsQuestion(t *testing.T) {
	src := strings.Replace(string(defaultLists()), "input   = \"Waiting on whom (optional)\"\n", "", 1)
	_, problems := loadListsWith(t, src, "")
	if !strings.Contains(problems, "verbs.waiting.input: is required: its handler waiting asks for a value first") {
		t.Errorf("%s", problems)
	}
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.waiting]\ninput = \"On whom?\"\n")
	if problems != "" || texts.Verbs["waiting"].Input != "On whom?" {
		t.Errorf("%q %s", texts.Verbs["waiting"].Input, problems)
	}
}

func TestTheReviewStepsAreWordedByTheProfile(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[review.waiting]\nlabel = \"Chase what waits\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	if texts.step("waiting") != "Chase what waits" || texts.step("inbox") != "Inbox to zero" {
		t.Errorf("review %v", texts.Review)
	}
}

func TestAReviewStepNamesAView(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[review.inbx]\nlabel = \"x\"\n\n[badge]\nviews = [\"inbox\", \"neds-me\"]\n")
	for _, want := range []string{
		"review.inbx: names no view of views.toml; did you mean \"inbox\"?",
		"badge.views[1]: names no view of views.toml; did you mean \"needs-me\"?",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
	if slices.ContainsFunc(texts.Review, func(st ReviewStepDef) bool { return st.View == "inbx" }) || !slices.Equal(texts.Badge, []string{"inbox"}) {
		t.Errorf("review %v, badge %v", texts.Review, texts.Badge)
	}
	var walked []string
	for _, st := range texts.Review {
		if _, ok := texts.View(st.View); !ok {
			t.Errorf("review step %s walks no view", st.View)
		}
		walked = append(walked, st.View)
	}
	if want := []string{"inbox", "outbox", "waiting", "review-projects", "someday", "focus"}; !slices.Equal(walked, want) {
		t.Errorf("the review walks %v, want %v", walked, want)
	}
}

func TestAUserViewLoads(t *testing.T) {
	user := "[views.later]\nlabel = \"Later\"\nsummary = \"s\"\nkey = \"g y\"\nwhen = \"task.prop.priority == 'low' && task.gates_open == 0\"\ngroup = \"task.prop.area\"\nsort = \"-task.prop.due, task.id\"\n\n" +
		"[views.asks]\nlabel = \"Asks\"\nsummary = \"s\"\nover = \"gates\"\nwhen = \"gate.kind == 'question'\"\n\n" +
		"[review.later]\nlabel = \"Anything to pull in?\"\n\n[badge]\nviews = [\"inbox\", \"later\"]\n"
	texts, problems := loadListsWith(t, string(defaultLists()), user)
	if problems != "" {
		t.Fatal(problems)
	}
	later, ok := texts.View("later")
	if !ok || later.Label != "Later" || later.Key != "g y" || later.Over != task.OverTasks || later.Group != "task.prop.area" || later.When == nil ||
		!slices.Equal(later.Sort, []SortKey{{Fact: "task.prop.due", Desc: true}, {Fact: "task.id"}}) {
		t.Fatalf("later %+v", later)
	}
	for f, want := range map[string]bool{"low": true, "high": false} {
		got, err := later.When.Match(workflow.Vars{Facts: workflow.Facts{"task.prop.priority": f, "task.gates_open": 0}})
		if err != nil || got != want {
			t.Errorf("priority %s: %v %v", f, got, err)
		}
	}
	if asks, ok := texts.View("asks"); !ok || asks.Over != task.OverGates || asks.When == nil {
		t.Errorf("asks %+v", asks)
	}
	if texts.step("later") == "" || !texts.Badged("later") || texts.Badged("needs-me") {
		t.Errorf("review %v, badge %v", texts.Review, texts.Badge)
	}
	_, problems = loadListsWith(t, string(defaultLists()), "[views.later]\nlabel = \"Later\"\nsummary = \"s\"\nwhen = \"gate.kind == 'draft'\"\ngroup = \"task.nope\"\n")
	for _, want := range []string{"views.later.when:", "views.later.group: task.nope names no fact of a view over tasks"} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
}

func TestViewsKeepTheirDeclaredOrderAcrossLayers(t *testing.T) {
	user := "[views.zeta]\nlabel = \"Zeta\"\nsummary = \"s\"\nwhen = \"true\"\n\n[views.inbox]\nlabel = \"In\"\n\n[views.alpha]\nlabel = \"Alpha\"\nsummary = \"s\"\nwhen = \"true\"\n\n" +
		"[review.zeta]\nlabel = \"z\"\n\n[review.alpha]\nlabel = \"a\"\n"
	texts, problems := loadListsWith(t, string(defaultLists()), user)
	if problems != "" {
		t.Fatal(problems)
	}
	bundled, _ := BundledViews(os.DirFS("../../profiles/default"))
	if want := append(bundled.IDs(), "zeta", "alpha"); !slices.Equal(texts.IDs(), want) {
		t.Errorf("views %v, want %v", texts.IDs(), want)
	}
	if want := []string{"inbox", "needs-me", "waiting", "chase", "fits-now", "blocked-by-me", "stalled", "in-loop", "approved", "snoozed", "focus", "projects", "someday", "logbook", "done", "outbox", "review-projects", "review"}; !slices.Equal(bundled.IDs(), want) {
		t.Errorf("bundled views %v, want %v", bundled.IDs(), want)
	}
	var steps []string
	for _, st := range texts.Review {
		steps = append(steps, st.View)
	}
	if want := []string{"inbox", "outbox", "waiting", "review-projects", "someday", "focus", "zeta", "alpha"}; !slices.Equal(steps, want) {
		t.Errorf("steps %v, want %v", steps, want)
	}
}

func TestAVerbIsOfferedWhereTheProfileSays(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.delegate]\noffer = 'task.prop.kind != \"capture\"'\n\n[verbs.done]\noffer = 'task.items_open == 0'\n")
	if !strings.Contains(problems, "verbs.done.offer:") {
		t.Errorf("an offer reading a fact it cannot see loads: %s", problems)
	}
	offer := texts.Verbs["delegate"].Offer
	if offer == nil {
		t.Fatal("delegate has no offer")
	}
	for kind, want := range map[string]bool{"capture": false, "thread": true} {
		got, err := offer.Match(workflow.Vars{Facts: workflow.Facts{"task.prop.kind": kind}})
		if err != nil || got != want {
			t.Errorf("%s: offered %v, %v", kind, got, err)
		}
	}
	if texts.Verbs["done"].Offer != nil || texts.Verbs["next"].Offer != nil {
		t.Error("a verb whose offer failed, or that has none, is offered everywhere")
	}
}

func TestSeenTakesNoOffer(t *testing.T) {
	texts, problems := loadListsWith(t, string(defaultLists()), "[verbs.seen]\noffer = 'task.prop.kind == \"capture\"'\n")
	if !strings.Contains(problems, "verbs.seen.offer: seen takes no offer") || texts.Verbs["seen"].Offer != nil {
		t.Errorf("%s", problems)
	}
}

func TestAGatedVerbNamesItsGate(t *testing.T) {
	_, problems := loadListsWith(t, string(defaultLists()), "[verbs.done.by]\nagent = \"gate\"\n")
	if !strings.Contains(problems, "verbs.done.gate: is required") {
		t.Errorf("%s", problems)
	}
	_, problems = loadListsWith(t, string(defaultLists()), "[verbs.done]\ngate = \"proposal\"\n[verbs.done.by]\nagent = \"gate\"\n")
	if strings.Contains(problems, "verbs.done.gate") {
		t.Errorf("%s", problems)
	}
}

func TestAViewsKeyComesFromTheProfile(t *testing.T) {
	got, problems := loadListsWith(t, string(defaultLists()), "[views.waiting]\nkey = \"g x\"\n\n[views.review-projects]\nkey = \"l\"\n\n[views.focus]\nkey = \"g p\"\n")
	if got.key("waiting") != "g x" || got.key("inbox") != "g i" || got.key("review-projects") != "" || got.key("logbook") != "" {
		t.Errorf("keys %+v", got.Views)
	}
	for _, want := range []string{
		"views.review-projects.key: l is not a chord",
		"views.focus.key: g p is the key of views.projects too",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("no %q in:\n%s", want, problems)
		}
	}
}

func loadFacets(t *testing.T, userViews string) (ViewsDef, Problems) {
	t.Helper()
	var layers []profile.Layer
	if userViews != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ViewsFile), []byte(userViews), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	bundled := os.DirFS("../../profiles/default")
	views, tasks, problems := loadViews(layers, bundled, nil)
	records, recordProblems := loadRecords(layers, bundled, &tasks)
	problems = append(problems, recordProblems...)
	return views, append(problems, views.checkFacets(tasks, records)...)
}

func facetOf(d ViewsDef, id string) (FacetDef, bool) {
	i := slices.IndexFunc(d.Facets, func(f FacetDef) bool { return f.ID == id })
	if i < 0 {
		return FacetDef{}, false
	}
	return d.Facets[i], true
}

func TestTaskFacetsLoadFromViews(t *testing.T) {
	views, problems := loadFacets(t, "")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	var ids []string
	for _, f := range views.Facets {
		ids = append(ids, f.ID)
	}
	want := []string{"source", "state", "category", "contexts", "waits-on", "asked-by", "placement", "moved", "gates", "signal", "prop.kind", "prop.area", "prop.risk", "prop.focus", "prop.priority", "type", "risk", "size", "tag"}
	if !slices.Equal(ids, want) {
		t.Fatalf("facets %v, want %v", ids, want)
	}
	if f, _ := facetOf(views, "waits-on"); f.Fact != "task.owed_by" || f.Merge != "rows-first" || f.Match != "team" || f.Units != "all" || !f.Menu {
		t.Errorf("waits-on: %+v", f)
	}
	if f, _ := facetOf(views, "tag"); f.Fact != "brief.tags" || f.Match != "prefix" {
		t.Errorf("tag: %+v", f)
	}
	moved, _ := facetOf(views, "moved")
	if moved.Sort != "time" || len(moved.Buckets) != 6 || moved.Buckets[0] != (FacetBucket{ID: "1d", Label: "0-1 days", Within: 24 * time.Hour}) || moved.Buckets[5].Within != 0 {
		t.Errorf("moved: %+v", moved)
	}
	if views.DefaultSort == "" {
		t.Error("no default_sort")
	}
}

func TestAFacetNamingNoFactIsAProblem(t *testing.T) {
	views, problems := loadFacets(t, "[facets.mine]\nfact = \"task.nope\"\nlabel = \"Mine\"\n\n[facets.owed]\nfact = \"task.owed_bi\"\nlabel = \"Owed\"\n\n[facets.when]\nfact = \"task.state\"\nlabel = \"When\"\nsort = \"time\"\n\n[facets.field]\nfact = \"brief.nope\"\nlabel = \"Field\"\n")
	got := problems.Error()
	for _, want := range []string{"facets.mine.fact", "task.nope", "facets.owed.fact: task.owed_bi", `did you mean "task.owed_by"?`, "facets.when.sort", "facets.field.fact"} {
		if !strings.Contains(got, want) {
			t.Errorf("problems lack %q:\n%s", want, got)
		}
	}
	for _, id := range []string{"mine", "owed", "when", "field"} {
		if _, ok := facetOf(views, id); ok {
			t.Errorf("facet %s with a problem is served", id)
		}
	}
}

func TestAUserLayerReplacesOnlyTheFacetKeysItSets(t *testing.T) {
	views, problems := loadFacets(t, "[facets.risk]\nlabel = \"Danger\"\n\n[facets.placement.values]\n\"acme/api\" = \"API\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	risk, _ := facetOf(views, "risk")
	if risk.Label != "Danger" || risk.Fact != "brief.risk" || risk.Sort != "rank" || !slices.Equal(risk.Rank, []string{"high", "medium", "low"}) {
		t.Errorf("risk: %+v", risk)
	}
	if p, _ := facetOf(views, "placement"); p.Values["acme/api"] != "API" {
		t.Errorf("placement: %+v", p)
	}
	_, problems = loadFacets(t, "[facets.fresh]\nlabel = \"Fresh\"\n")
	if !strings.Contains(problems.Error(), "facets.fresh.fact: is required") {
		t.Errorf("a new facet without a fact: %s", problems.Error())
	}
}

func TestAViewGroupNeedsAFacet(t *testing.T) {
	views, problems := loadFacets(t, "[views.waiting]\ngroup = \"task.since_movement\"\n")
	if got := problems.Error(); !strings.Contains(got, "views.waiting.group") || !strings.Contains(got, "no facet") {
		t.Fatalf("problems: %s", got)
	}
	if v, _ := views.View("waiting"); v.Group != "" {
		t.Errorf("group %q kept", v.Group)
	}
	views, problems = loadFacets(t, "")
	if v, _ := views.View("waiting"); len(problems) > 0 || v.Group != "task.owed_by" {
		t.Errorf("bundled group %q, problems %s", v.Group, problems.Error())
	}
}

func TestAViewGroupProblemPointsAtTheLayerThatSetIt(t *testing.T) {
	_, problems := loadFacets(t, "[views.waiting]\ngroup = \"task.since_movement\"\n")
	i := slices.IndexFunc(problems, func(p Problem) bool { return p.Field == "views.waiting.group" })
	if i < 0 || problems[i].Line != 2 || !strings.HasSuffix(problems[i].File, ViewsFile) || problems[i].File == ViewsFile {
		t.Fatalf("problems: %+v", problems)
	}
}

func TestABadFacetOverrideKeepsWhatItWouldReplace(t *testing.T) {
	views, problems := loadFacets(t, "[facets.risk]\nsort = \"bogus\"\n\n[facets.moved.buckets.1d]\nwithin = \"soon\"\n")
	for _, f := range []string{"facets.risk.sort", "facets.moved.buckets.1d.within"} {
		if !strings.Contains(problems.Error(), f) {
			t.Errorf("no problem for %s: %s", f, problems.Error())
		}
	}
	risk, _ := facetOf(views, "risk")
	moved, _ := facetOf(views, "moved")
	if risk.Sort != "rank" || len(risk.Rank) != 3 || len(moved.Buckets) != 6 || moved.Buckets[0].Within != 24*time.Hour {
		t.Errorf("risk %+v, moved %+v", risk, moved)
	}
}

func TestAFactNamedByAMetaKeyIsNoFact(t *testing.T) {
	_, problems := loadFacets(t, "[facets.meta]\nfact = \"\\u0000now\"\nlabel = \"Meta\"\n")
	if !strings.Contains(problems.Error(), "facets.meta.fact") {
		t.Errorf("problems: %s", problems.Error())
	}
}

func TestTheLensWordsNameOnlyTheirVariables(t *testing.T) {
	views, problems := loadFacets(t, "")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	w := views.Lens
	if w.DefaultGroup == "" || w.BoardGroup == "" || w.NoGroup == "" || w.Copied == "" || w.CopiedGrouped == "" {
		t.Fatalf("the bundled profile leaves lens words out: %+v", w)
	}
	_, problems = loadFacets(t, "[lens]\ncopied = \"Copied {{.count}} of {{.group}}\"\n")
	if len(problems) != 1 || problems[0].Field != "lens.copied" {
		t.Errorf("a copied word naming the group: %v", problems)
	}
}

func TestAFacetsShowNamesAPartOrAPartsValues(t *testing.T) {
	views, problems := loadFacets(t, "[facets.risk]\nshow = { flag = [\"high\"] }\nshow_values = \"first\"\n\n[facets.size]\nshow = \"aside\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if risk, _ := facetOf(views, "risk"); risk.Show.Part != "flag" || !slices.Equal(risk.Show.Only, []string{"high"}) || risk.Show.Values != "first" {
		t.Errorf("risk shows %+v", risk.Show)
	}
	if size, _ := facetOf(views, "size"); size.Show.Part != "aside" || size.Show.Only != nil {
		t.Errorf("size shows %+v", size.Show)
	}
	if moved, _ := facetOf(views, "moved"); moved.Show.Part != "age" {
		t.Errorf("the bundled moved shows %+v", moved.Show)
	}
	views, problems = loadFacets(t, "[facets.moved]\nshow = \"\"\n")
	if moved, _ := facetOf(views, "moved"); len(problems) > 0 || moved.Show.Part != "" {
		t.Errorf("show = \"\" leaves moved %+v, problems %v", moved.Show, problems)
	}
	views, problems = loadFacets(t, "[facets.risk]\nshow = { \"\" = [\"high\"] }\n")
	if risk, _ := facetOf(views, "risk"); risk.Show.Only != nil || !strings.Contains(problems.Error(), "names no part") {
		t.Errorf("a table of no part leaves risk %+v, problems %v", risk.Show, problems)
	}
	for _, bad := range []string{"show = \"corner\"", "show = { corner = [\"high\"] }", "show = { flag = \"high\" }", "show_values = \"some\""} {
		_, problems := loadFacets(t, "[facets.risk]\n"+bad+"\n")
		if !strings.Contains(problems.Error(), "facets.risk.show") {
			t.Errorf("%s: %v", bad, problems)
		}
	}
}

func TestTheRowWordsAreSetAndNameOnlyTheirVariables(t *testing.T) {
	views, problems := loadFacets(t, "")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if set := views.Lens.Row.Set(); len(set) != len(builtin.RowWords) {
		t.Fatalf("the bundled profile sets %d of %d row words: %v", len(set), len(builtin.RowWords), set)
	}
	_, problems = loadFacets(t, "[lens]\nage_days = \"{{.d}} days {{.m}}\"\n")
	if len(problems) != 1 || problems[0].Field != "lens.age_days" {
		t.Errorf("an age naming the minutes: %v", problems)
	}
}

func TestAPluginsUIShowsItsFacetsAndReplacesRowWords(t *testing.T) {
	plugin, user := t.TempDir(), t.TempDir()
	write := func(dir, body string) {
		if err := os.WriteFile(filepath.Join(dir, UIFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(plugin, "[lens]\ncol_aside = \"Workspace\"\n\n[facets.author]\nlabel = \"Author\"\nshow = \"meta\"\nshow_values = \"first\"\nlabel_prefix = \"by \"\nrow_class = { me = \"mine\" }\n")
	write(user, "[facets.author]\nshow = \"who\"\n")
	texts, problems := loadUI([]profile.Layer{{Name: profile.PluginLayer("acme"), Dir: plugin}, {Name: "user:acme", Dir: user}})
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	a := texts["acme"].Facets["author"]
	if a.Show.Part != "who" || a.Show.Values != "first" || a.LabelPrefix == nil || *a.LabelPrefix != "by " || a.RowClass["me"] != "mine" {
		t.Errorf("author: %+v", a)
	}
	if got := texts["acme"].Lens.Set(); !maps.Equal(got, map[string]string{"col_aside": "Workspace"}) {
		t.Errorf("row words %v", got)
	}
	write(user, "[facets.author]\nshow = \"\"\n")
	texts, problems = loadUI([]profile.Layer{{Name: profile.PluginLayer("acme"), Dir: plugin}, {Name: "user:acme", Dir: user}})
	if a := texts["acme"].Facets["author"]; len(problems) > 0 || a.Show.Part != "" || a.Show.Values != "first" {
		t.Errorf("show = \"\" over a placed facet: %+v, problems %v", a.Show, problems)
	}
	write(user, "[facets.author]\nshow = \"corner\"\n\n[lens]\ncol_aside = \"{{.label}}\"\n")
	_, problems = loadUI([]profile.Layer{{Name: "user:acme", Dir: user}})
	if got := problems.Error(); !strings.Contains(got, "facets.author.show") || !strings.Contains(got, "lens.col_aside") {
		t.Errorf("problems: %s", got)
	}
}
