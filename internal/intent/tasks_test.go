package intent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func defaultTasks(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../profiles/default/" + TasksFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// loadTasksWith is tasks.toml loaded from bundled, then a user layer
// holding user when it is not empty, with no gates.toml to check against.
func loadTasksWith(t *testing.T, bundled, user string) (TasksDef, string) {
	t.Helper()
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, TasksFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	def, problems := loadTasks(layers, fstest.MapFS{TasksFile: {Data: []byte(bundled)}}, nil)
	return def, problems.Error()
}

func stateNames(d TasksDef) []string {
	out := make([]string, len(d.States))
	for i, s := range d.States {
		out[i] = s.Name
	}
	return out
}

func TestBundledTasksDeclareTheEightStatesWithTheirCategories(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "")
	if problems != "" {
		t.Fatal(problems)
	}
	want := []StateDef{
		{Name: "inbox", Category: task.CategoryInbox},
		{Name: "next", Category: task.CategoryReady},
		{Name: "waiting", Category: task.CategoryWaiting},
		{Name: "scheduled", Category: task.CategoryDeferred},
		{Name: "someday", Category: task.CategoryDeferred},
		{Name: "done", Category: task.CategoryDone},
		{Name: "reference", Category: task.CategoryDone},
		{Name: "trash", Category: task.CategoryDropped},
	}
	if len(d.States) != len(want) {
		t.Fatalf("states %+v", d.States)
	}
	for i, w := range want {
		got := d.States[i]
		if got.Name != w.Name || got.Category != w.Category || got.Label == "" {
			t.Errorf("state %d: %+v, want %s in %s with a label", i, got, w.Name, w.Category)
		}
	}
}

func TestAUserLayerAddsAPropertyAndOverridesAStateLabel(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), `[props.client]
type = "string"
writers = ["person"]
doc = "The client the work is for."

[states.next]
label = "Do next"
`)
	if problems != "" {
		t.Fatal(problems)
	}
	p, ok := d.Prop("client")
	if !ok || p.Type != task.PropString || !slices.Equal(p.Writers, []task.Writer{task.WriterPerson}) || p.Doc == "" {
		t.Fatalf("client %+v %v", p, ok)
	}
	if d.Props[len(d.Props)-1].Name != "client" {
		t.Errorf("a user's new property comes after the bundled ones: %+v", d.Props)
	}
	if _, ok := d.Prop("risk"); !ok {
		t.Error("the bundled risk property is gone")
	}
	i := slices.Index(stateNames(d), "next")
	if i != 1 || d.States[i].Label != "Do next" || d.States[i].Category != task.CategoryReady {
		t.Errorf("next %d %+v", i, d.States)
	}
	if c, ok := d.CategoryOf("next"); !ok || c != task.CategoryReady {
		t.Errorf("CategoryOf(next) = %s %v", c, ok)
	}
}

func TestAPropertyOfAnUnknownTypeIsAProblem(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "[props.mood]\ntype = \"feeling\"\n")
	if !strings.Contains(problems, "props.mood.type") || !strings.Contains(problems, "feeling") {
		t.Errorf("problems %q", problems)
	}
	if _, ok := d.Prop("mood"); ok {
		t.Error("a property of an unknown type is declared")
	}
	_, problems = loadTasksWith(t, defaultTasks(t), "[props.mood]\nwriters = [\"person\"]\n")
	if !strings.Contains(problems, "props.mood.type: is required") {
		t.Errorf("a new property without a type: %q", problems)
	}
}

func TestAStateOfAnUnknownCategoryIsAProblem(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "[states.blocked]\ncategory = \"stuck\"\n")
	if !strings.Contains(problems, "states.blocked.category") || !strings.Contains(problems, "stuck") {
		t.Errorf("problems %q", problems)
	}
	if _, ok := d.CategoryOf("blocked"); ok {
		t.Error("a state of an unknown category is declared")
	}
	_, problems = loadTasksWith(t, defaultTasks(t), "[states.blocked]\nlabel = \"Blocked\"\n")
	if !strings.Contains(problems, "states.blocked.category: is required") {
		t.Errorf("a new state without a category: %q", problems)
	}
}

// withoutTable is src with the TOML table header and its keys removed.
func withoutTable(t *testing.T, src, header string) string {
	t.Helper()
	i := strings.Index(src, header+"\n")
	if i < 0 {
		t.Fatalf("no %s", header)
	}
	end := strings.Index(src[i+len(header):], "\n[")
	if end < 0 {
		return src[:i]
	}
	return src[:i] + src[i+len(header)+end+1:]
}

func TestRemovingAStateABundledVerbNeedsIsAProblem(t *testing.T) {
	t.Parallel()
	bundled := defaultTasks(t)
	if _, problems := loadTasksWith(t, bundled, ""); problems != "" {
		t.Fatalf("with every state: %s", problems)
	}
	_, problems := loadTasksWith(t, withoutTable(t, bundled, "[states.trash]"), "")
	if !strings.Contains(problems, "verbs.dismiss.to: names no state trash") {
		t.Errorf("problems %q", problems)
	}
	_, problems = loadTasksWith(t, withoutTable(t, bundled, "[states.next]"), "")
	if !strings.Contains(problems, "verbs.dismiss.from[1]: names no state next") {
		t.Errorf("problems %q", problems)
	}
}

func TestFirstOfACategoryIsTheProfilesFirstState(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "[states.today]\ncategory = \"ready\"\n[states.doing]\ncategory = \"active\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	for c, want := range map[task.Category]string{
		task.CategoryInbox: "inbox", task.CategoryReady: "next", task.CategoryDeferred: "scheduled",
		task.CategoryDone: "done", task.CategoryDropped: "trash", task.CategoryActive: "doing",
	} {
		if got := d.FirstOf(c); got != want {
			t.Errorf("FirstOf(%s) = %q, want %q", c, got, want)
		}
	}
	bundled, _ := loadTasksWith(t, defaultTasks(t), "")
	if got := bundled.FirstOf(task.CategoryActive); got != "" {
		t.Errorf("FirstOf(active) with no active state = %q", got)
	}
}

func TestStatesKeepTheirDeclaredOrderAcrossLayers(t *testing.T) {
	t.Parallel()
	bundled := strings.Replace(defaultTasks(t), "[states.inbox]", "[states.zeta]\ncategory = \"ready\"\n\n[states.alpha]\ncategory = \"done\"\n\n[states.inbox]", 1)
	d, problems := loadTasksWith(t, bundled, "[states.yak]\ncategory = \"ready\"\n\n[states.alpha]\nlabel = \"Alpha\"\n\n[states.bee]\ncategory = \"waiting\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	want := []string{"zeta", "alpha", "inbox", "next", "waiting", "scheduled", "someday", "done", "reference", "trash", "yak", "bee"}
	if got := stateNames(d); !slices.Equal(got, want) {
		t.Errorf("states %v, want %v", got, want)
	}
	if d.FirstOf(task.CategoryReady) != "zeta" {
		t.Errorf("FirstOf(ready) = %q", d.FirstOf(task.CategoryReady))
	}
	props, _ := loadTasksWith(t, defaultTasks(t), "[props.zz]\ntype = \"int\"\n[props.aa]\ntype = \"int\"\n")
	names := make([]string, len(props.Props))
	for i, p := range props.Props {
		names[i] = p.Name
	}
	if !slices.Equal(names[len(names)-2:], []string{"zz", "aa"}) || names[0] != "priority" {
		t.Errorf("props %v", names)
	}
}

func TestAgentWritablePropIsUntrusted(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "")
	if problems != "" {
		t.Fatal(problems)
	}
	for prop, want := range map[string]bool{
		"risk": true, "effort": true, "priority": true, "due": true, "asker": true, "kind": true,
		"promised": false, "owner": false, "area": false, "focus": false, "last_nudge": false, "check_on": false,
		"undeclared": true,
	} {
		if got := d.Untrusted(prop); got != want {
			t.Errorf("Untrusted(%s) = %v, want %v", prop, got, want)
		}
	}
	env := d.Facts()
	if !env.Untrusted(builtin.PropPrefix+"risk") || env.Untrusted(builtin.PropPrefix+"promised") {
		t.Error("the facts do not carry the properties' trust")
	}
}

func TestAByKeyThatIsNoActorIsAProblem(t *testing.T) {
	t.Parallel()
	_, problems := loadTasksWith(t, defaultTasks(t), "[verbs.done.by]\nrobot = \"true\"\nperson = \"true\"\n")
	if !strings.Contains(problems, "verbs.done.by.robot: names no actor") {
		t.Errorf("problems %q", problems)
	}
	if strings.Contains(problems, "verbs.done.by.person") {
		t.Errorf("person is an actor: %q", problems)
	}
	_, problems = loadTasksWith(t, defaultTasks(t), "[verbs.done.by]\nagent = 'task.prop.risk == \"tiny\"'\n")
	if !strings.Contains(problems, "verbs.done.by.agent") {
		t.Errorf("a by predicate over an enum value the property lacks: %q", problems)
	}
	_, problems = loadTasksWith(t, defaultTasks(t), "[verbs.done.by]\nagent = 'task.prop.risk == \"low\"'\n")
	if problems != "" {
		t.Errorf("a by predicate over a declared property: %q", problems)
	}
}

func TestAZeroIntPropertyIsUnset(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "[props.count]\ntype = \"int\"\nwriters = [\"workflow\"]\n")
	if problems != "" {
		t.Fatal(problems)
	}
	for _, c := range []struct{ prop, text string }{{"count", "0"}, {"size", "0s"}, {"size", "0"}} {
		if _, err := d.Parse(c.prop, c.text, time.UTC); err == nil || !strings.Contains(err.Error(), "0 unsets") {
			t.Errorf("Parse(%s, %q) = %v", c.prop, c.text, err)
		}
	}
	if v, err := d.Parse("count", "3", time.UTC); err != nil || v.Int != 3 {
		t.Errorf("Parse(count, 3) = %+v %v", v, err)
	}
	if v, err := d.Parse("count", "", time.UTC); err != nil || !v.IsZero() {
		t.Errorf("empty text unsets: %+v %v", v, err)
	}
}

func TestParseReadsEachPropertyType(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "[props.reviewers]\ntype = \"actor[]\"\n")
	if problems != "" {
		t.Fatal(problems)
	}
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		prop, text string
		ok         func(task.Value) bool
	}{
		{"priority", "high", func(v task.Value) bool { return v.Text == "high" }},
		{"check_on", "2026-11-02", func(v task.Value) bool { return v.Time.Equal(day) && v.Text == "" }},
		{"check_on", "2026-11-02T09:30:00Z", func(v task.Value) bool { return v.Time.Equal(day.Add(9*time.Hour + 30*time.Minute)) }},
		{"size", "90m", func(v task.Value) bool { return v.Dur == 90*time.Minute }},
		{"size", "2d", func(v task.Value) bool { return v.Dur == 48*time.Hour }},
		{"contexts", "@home, @calls,", func(v task.Value) bool { return slices.Equal(v.List, []string{"@home", "@calls"}) }},
		{"owner", "slack:U0AAA", func(v task.Value) bool { return len(v.Actors) == 1 && v.Actors[0].Key == "slack:U0AAA" }},
		{"reviewers", "github:ana, github:bruno", func(v task.Value) bool { return len(v.Actors) == 2 && v.Actors[1].Key == "github:bruno" }},
	}
	for _, c := range cases {
		v, err := d.Parse(c.prop, c.text, time.UTC)
		if err != nil || !c.ok(v) {
			t.Errorf("Parse(%s, %q) = %+v %v", c.prop, c.text, v, err)
		}
	}
	for _, c := range []struct{ prop, text, msg string }{
		{"priority", "urgent", "low, normal, high"},
		{"check_on", "next week", "YYYY-MM-DD"},
		{"size", "a while", "duration"},
		{"owner", "ana", "provider:id"},
		{"nope", "x", "names no property"},
	} {
		if _, err := d.Parse(c.prop, c.text, time.UTC); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("Parse(%s, %q) = %v, want %q", c.prop, c.text, err, c.msg)
		}
	}
}

const focusOnTask = `summary = "Focus on a task"
provider = "task"
when = 'task.prop.priority == "high"'

[[step]]
use = "task.set"
prop = "focus"
value = "on"

[gates.approval]
mode = "wait"
[[gates.approval.rule]]
`

func TestAnApprovalRuleReadsATrustedPropertyNeverAnAgentWrittenOne(t *testing.T) {
	t.Parallel()
	bundled := fstest.MapFS{TasksFile: {Data: []byte(defaultTasks(t))}}
	for when, refused := range map[string]bool{
		`task.prop.promised > timestamp(0)`: false,
		`task.prop.owner == "slack:U0AAA"`:  false,
		`task.prop.risk == "high"`:          true,
		`task.prop.due > timestamp(0)`:      true,
	} {
		root := t.TempDir()
		writeWorkflow(t, root, "focus", focusOnTask+"when = '"+when+"'\nmode = \"run\"\n")
		l, problems := Load(userLayer(root), bundled, testBase(), nil)
		fact := strings.Fields(when)[0]
		got := strings.Contains(problems.Error(), "gates.approval.rule[0].when: reads "+fact+", which a rule that releases a gate")
		if got != refused {
			t.Errorf("%s: %s", when, problems.Error())
		}
		if !refused && (len(l.Workflows) != 1 || len(l.Workflows[0].Approval.Rules) != 1) {
			t.Errorf("%s: %s %+v", when, problems.Error(), l.Workflows)
		}
	}
	root := t.TempDir()
	writeWorkflow(t, root, "focus", strings.Replace(focusOnTask, `"high"'`, `"urgent"'`, 1)+"when = 'true'\nmode = \"wait\"\n")
	if _, problems := Load(userLayer(root), bundled, testBase(), nil); !strings.Contains(problems.Error(), "urgent") {
		t.Errorf("an enum property's facts take its values only: %s", problems.Error())
	}
}

func TestADateOnlyValueIsMidnightInTheGivenLocation(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "")
	if problems != "" {
		t.Fatal(problems)
	}
	brt, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	v, err := d.Parse("check_on", "2026-10-29", brt)
	if err != nil || !v.Time.Equal(time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("check_on %v %v", v.Time.UTC(), err)
	}
}

func TestAnEnumWithoutValuesIsAProblem(t *testing.T) {
	t.Parallel()
	d, problems := loadTasksWith(t, defaultTasks(t), "[props.mood]\ntype = \"enum\"\nwriters = [\"person\"]\n")
	if !strings.Contains(problems, "props.mood.values: is required with type enum") {
		t.Errorf("problems %q", problems)
	}
	if _, ok := d.Prop("mood"); ok {
		t.Error("an enum without values is declared")
	}
	if p, _ := d.Prop("area"); p.Type != task.PropString {
		t.Errorf("area %+v", p)
	}
}

func TestAnIntentRefuseReadsATaskProperty(t *testing.T) {
	t.Parallel()
	bundled := fstest.MapFS{TasksFile: {Data: []byte(defaultTasks(t))}}
	for kind, want := range map[string]string{"project": "", "galaxy": "galaxy"} {
		dir := t.TempDir()
		refuse := "\n[[refuse]]\nwhen = 'task.prop.kind == \"" + kind + "\"'\nwhy = \"A project starts its children.\"\n"
		writeIntent(t, dir, "begin", startLike+refuse, "Begin.")
		_, problems := Load(userLayer(dir), bundled, testBase(), nil)
		if got := problems.Error(); (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("kind %s: problems %q", kind, got)
		}
	}
}

func TestAByPredicateNamesOnlyFactsTheAuthorityReads(t *testing.T) {
	_, problems := loadTasksWith(t, defaultTasks(t), "[verbs.done]\nby = { agent = 'task.repo != \"acme/prod\"' }\n")
	if !strings.Contains(problems, "verbs.done.by.agent") || !strings.Contains(problems, "task.repo") {
		t.Errorf("a by naming a fact the authority does not read loads:\n%s", problems)
	}
	if _, problems := loadTasksWith(t, defaultTasks(t), "[verbs.done]\nby = { agent = 'task.prop.kind == \"capture\" && task.prop.risk == \"low\"' }\n"); problems != "" {
		t.Errorf("a by over the task's own facts and its properties: %s", problems)
	}
}
