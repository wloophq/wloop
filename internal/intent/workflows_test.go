package intent

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/profiles"
)

const quietSession = `subject = "session"
when = 'session.managed && session.idle_for > config.cleanup.idle'

[trigger]
every = "sweep"

[[step]]
use = "session.close"
`

func writeWorkflow(t testing.TB, root, name, manifest string) {
	t.Helper()
	dir := filepath.Join(root, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBundledWorkflowsLoad(t *testing.T) {
	sub, err := fs.Sub(profiles.Default, "default")
	if err != nil {
		t.Fatal(err)
	}
	loaded, broken, _, problems, warnings := loadWorkflows(nil, sub, nil, nil)
	if len(problems) > 0 || len(broken) > 0 || len(warnings) > 0 {
		t.Fatalf("%v\n%v", problems.Error(), warnings.Error())
	}
	starts := 0
	for _, w := range loaded {
		for _, s := range w.Steps {
			if _, forced := s.Args["force"]; forced {
				t.Errorf("%s: step %s sets force", w.Name, s.Use)
			}
			if s.Use == builtin.StepTurnStart {
				starts++
				if w.Approval.Window != time.Minute {
					t.Errorf("%s: a turn.start workflow waits %s, want 1m", w.Name, w.Approval.Window)
				}
			}
		}
	}
	if starts == 0 {
		t.Fatal("no bundled turn.start workflow")
	}
}

func TestGoodWorkflowsLoad(t *testing.T) {
	cases, err := os.ReadDir("testdata/workflows/good")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata/workflows/good", c.Name())
			l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
			if len(problems) > 0 || len(l.Workflows) != 1 {
				t.Fatalf("%d workflows; %s", len(l.Workflows), problems.Error())
			}
		})
	}
}

func TestAWorkflowManifestBecomesTheDomainWorkflow(t *testing.T) {
	l, problems := Load(userLayer("testdata/workflows/good/close-quiet"), nil, testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	w := l.Workflows[0]
	if w.Name != "close-quiet" || w.Provider != dunit.Session || w.Slot != "session" || !w.Observe || w.Order != 100 {
		t.Errorf("%+v", w)
	}
	if w.Trigger.Kind != workflow.TriggerSweep || w.Limits != (workflow.Limits{PerRun: 5, PerDay: 50, Cooldown: time.Hour}) {
		t.Errorf("%+v %+v", w.Trigger, w.Limits)
	}
	if len(w.Steps) != 1 || w.Steps[0].Use != "session.close" || w.Steps[0].Reach != workflow.ReachLocal || w.Steps[0].Args != nil {
		t.Errorf("%+v", w.Steps)
	}
	if w.Approval.Default != workflow.OutcomeRun || w.When == nil || !strings.HasSuffix(w.Source, "close-quiet/workflow.toml") {
		t.Errorf("%+v", w)
	}
	if want := []string{"config.cleanup.idle", "session.agent_state", "session.idle_for", "session.managed", "session.open_turn", "session.user_active"}; !slices.Equal(w.When.Reads(), want) {
		t.Errorf("reads %v", w.When.Reads())
	}
}

func TestATurnStartWorkflowWithoutAWindowRunsAtOnce(t *testing.T) {
	l, problems := Load(userLayer("testdata/workflows/good/review-follow-up"), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	w := l.Workflows[0]
	if w.Approval.Window != 0 || w.Order != 10 || !slices.Equal(w.Key, []string{"pr.fingerprint"}) {
		t.Errorf("%+v", w)
	}
	if got := w.Steps[0].Args; got["intent"] != "review" || got["round"] != "follow-up" || w.Steps[0].Reach != "" {
		t.Errorf("%v", got)
	}
}

func TestAnEventAndAScheduleWorkflowLoad(t *testing.T) {
	l, _ := Load(userLayer("testdata/workflows/good/after-merge"), nil, testBase(), nil)
	if w := l.Workflows[0]; w.Trigger.Kind != workflow.TriggerEvent || !slices.Equal(w.Trigger.On, []string{"merged", "closed"}) || len(w.Steps) != 2 {
		t.Errorf("%+v", w)
	}
	l, _ = Load(userLayer("testdata/workflows/good/morning-sweep"), nil, testBase(), nil)
	w := l.Workflows[0]
	if w.Trigger.Kind != workflow.TriggerSchedule || w.Trigger.Schedule == nil || w.Approval.Window != 24*time.Hour || w.Approval.Default != workflow.OutcomeWait || w.Approval.WaitWhen == nil {
		t.Errorf("%+v", w)
	}
}

func TestBadWorkflowsGolden(t *testing.T) {
	cases, err := os.ReadDir("testdata/workflows/bad")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata/workflows/bad", c.Name())
			l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
			if len(problems) == 0 || len(l.Workflows) != 0 || len(l.BrokenWorkflows) != 1 {
				t.Fatalf("%d workflows, %d broken: %s", len(l.Workflows), len(l.BrokenWorkflows), problems.Error())
			}
			golden := filepath.Join(dir, c.Name()+".golden")
			got := problems.Error() + "\n"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
			for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
				if !strings.HasPrefix(line, dir+"/workflows/") {
					t.Errorf("not placed in the case's files: %s", line)
				}
			}
		})
	}
}

func TestAnInvalidUserWorkflowDisablesOnlyItself(t *testing.T) {
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}, "workflows/close-quiet/workflow.toml": {Data: []byte(quietSession)}}
	root := t.TempDir()
	writeWorkflow(t, root, "mine-ok", quietSession)
	writeWorkflow(t, root, "mine-typo", strings.Replace(quietSession, "session.managed", "session.manged", 1))
	writeWorkflow(t, root, "mine-step", strings.Replace(quietSession, "session.close", "session.explode", 1))
	l, problems := Load(userLayer(root), bundled, testBase(), nil)
	var names []string
	for _, w := range l.Workflows {
		names = append(names, w.Name)
	}
	if !slices.Equal(names, []string{"close-quiet", "mine-ok"}) {
		t.Errorf("loaded %v", names)
	}
	if got := slices.Sorted(mapKeys(l.BrokenWorkflows)); !slices.Equal(got, []string{"mine-step", "mine-typo"}) {
		t.Errorf("broken %v", got)
	}
	if !strings.Contains(problems.Error(), "unknown field session.manged; did you mean session.managed?") {
		t.Error(problems.Error())
	}
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func TestAUserWorkflowOverridesABundledManifest(t *testing.T) {
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}, "workflows/close-quiet/workflow.toml": {Data: []byte(quietSession)}}
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", strings.Replace(quietSession, "config.cleanup.idle", "duration(\"2h\")", 1))
	l, problems := Load(userLayer(root), bundled, testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	file := filepath.Join(root, "workflows", "close-quiet", "workflow.toml")
	if len(l.Workflows) != 1 || l.Workflows[0].Source != file || !slices.Equal(l.Workflows[0].Overrides, []string{"workflow.toml"}) {
		t.Fatalf("%+v", l.Workflows)
	}
}

func TestABrokenWorkflowOverrideKeepsTheBundledOne(t *testing.T) {
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}, "workflows/close-quiet/workflow.toml": {Data: []byte(quietSession)}}
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession+"bogus = 1\n")
	l, problems := Load(userLayer(root), bundled, testBase(), nil)
	file := filepath.Join(root, "workflows", "close-quiet", "workflow.toml")
	if len(problems) == 0 || problems[0].File != file {
		t.Fatalf("problems %q", problems.Error())
	}
	if len(l.Workflows) != 1 || l.Workflows[0].Source != "workflows/close-quiet/workflow.toml" || len(l.Workflows[0].Overrides) > 0 {
		t.Fatalf("%+v", l.Workflows)
	}
	if _, ok := l.BrokenWorkflows["close-quiet"]; ok {
		t.Error("a bundled workflow is never broken")
	}
}

func TestTwoLayersCannotDefineTheSameWorkflow(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeWorkflow(t, a, "shared", quietSession)
	writeWorkflow(t, b, "shared", quietSession)
	layers := append(userLayer(a), userLayer(b)[1])
	layers[2].Name = "second"
	l, problems := Load(layers, nil, testBase(), nil)
	if len(l.Workflows) != 1 || !strings.Contains(problems.Error(), "shared is defined already in "+filepath.Join(a, "workflows", "shared")) {
		t.Errorf("%d: %s", len(l.Workflows), problems.Error())
	}
}

func TestPlainFilesInWorkflowsAreIgnored(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "mine-ok", quietSession)
	if err := os.WriteFile(filepath.Join(root, "workflows", "README.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) > 0 || len(l.Workflows) != 1 {
		t.Fatal(problems.Error())
	}
}

func TestAWorkflowDirectoryWithoutItsManifestIsAProblem(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "empty-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(l.BrokenWorkflows["empty-one"]) != 1 || !strings.Contains(problems.Error(), "missing: every workflow directory holds workflow.toml") {
		t.Error(problems.Error())
	}
}

func TestNoManifestForces(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "forced", strings.Replace(quietSession, `use = "session.close"`, "use = \"session.close\"\nforce = \"true\"", 1))
	writeWorkflow(t, root, "forced-removal", `subject = "workspace"
when = "workspace.main"

[trigger]
every = "sweep"

[[step]]
use = "workspace.remove"
force = "true"
`)
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(l.Workflows) != 0 || strings.Count(problems.Error(), "force is never available to an automatic run") != 2 {
		t.Error(problems.Error())
	}
}

func TestACachedLoadReloadsWhenAWorkflowChanges(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "mine-ok", quietSession)
	now := time.Now()
	c := NewCache(func() []profile.Layer { return userLayer(root) }, nil, testBase(), nil, CacheOptions{Now: func() time.Time { return now }})
	if n := len(c.Get().Workflows); n != 1 {
		t.Fatal(n)
	}
	writeWorkflow(t, root, "mine-two", quietSession)
	if n := len(c.Get().Workflows); n != 2 {
		t.Fatalf("not reloaded: %d", n)
	}
}

func TestWorkflowSchemaMatchesTheDecodeStruct(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(WorkflowSchema, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	typ := reflect.TypeFor[wManifest]()
	seen := map[string]bool{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		key := f.Tag.Get("toml")
		seen[key] = true
		sub, ok := props[key].(map[string]any)
		if !ok {
			t.Errorf("%s: in the decode struct, not in the schema", key)
			continue
		}
		if f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct {
			compareSchema(t, key+".", f.Type.Elem(), sub)
		}
	}
	for key := range props {
		if !seen[key] {
			t.Errorf("%s: in the schema, not in the decode struct", key)
		}
	}
}

func TestATurnStartNamesAnIntentThatLoads(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	writeIntent(t, root, "ask-twice", strings.Replace(minimal, `workspace = "review"`, `workspace = "nowhere"`, 1), "{{.Headline}}")
	starts := func(intent string) string {
		return `subject = "github"
when = "true"

[trigger]
every = "sweep"

[[step]]
use = "turn.start"
intent = "` + intent + `"
`
	}
	writeWorkflow(t, root, "ask-it", starts("ask-author"))
	writeWorkflow(t, root, "ask-broken", starts("ask-twice"))
	l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(l.Workflows) != 1 || l.Workflows[0].Name != "ask-it" {
		t.Errorf("loaded %v; want ask-it", l.Workflows)
	}
	got := l.BrokenWorkflows["ask-broken"]
	want := `step[0].intent: the intent "ask-twice" does not load`
	if len(got) != 1 || !strings.Contains(got.Error(), want) {
		t.Errorf("ask-broken problems %v; want one with %q", got, want)
	}
}

func TestAWorkflowsMarksNameTheFactsTheyRecord(t *testing.T) {
	l, problems := Load(userLayer("testdata/workflows/good/conflict-once"), bundledFS(), testBase(), nil)
	if len(problems) > 0 || len(l.Workflows) != 1 {
		t.Fatalf("%d workflows; %v", len(l.Workflows), problems)
	}
	if got := l.Workflows[0].Marks; !maps.Equal(got, map[string]string{"head": "pr.head"}) {
		t.Fatalf("marks %v", got)
	}
}
