package intent

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"text/template"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/profile"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func testBase() *template.Template {
	return template.Must(template.New("base").Parse(`{{define "review-judgment"}}judgment{{end}}`))
}

func bundledFS() fs.FS { return os.DirFS("testdata/bundled") }

// testDefaults is the bundled profile's defaults.toml.
const testDefaults = "provider = \"github\"\n"

// userLayer is the layers of a user profile in dir; dir holds the bundled
// defaults unless it has its own.
func userLayer(dir string) []profile.Layer {
	if _, err := os.Stat(filepath.Join(dir, DefaultsFile)); errors.Is(err, fs.ErrNotExist) {
		_ = os.WriteFile(filepath.Join(dir, DefaultsFile), []byte(testDefaults), 0o644)
	}
	return []profile.Layer{{Name: "bundled", Source: "embedded"}, {Name: "user", Source: dir, Dir: dir}}
}

const minimal = `label = "Ask author"
summary = "Ask the author one question"
workspace = "review"
session = { strategy = "new", title = "ask" }

[headline]
resumed = "ask round on {{.pr.slug}}"
[[headline.new]]
line = "Ask the author of {{.pr.slug}} one question."

[record]
states = ["done"]
`

func writeIntent(t testing.TB, root, name, manifest, body string) {
	t.Helper()
	dir := filepath.Join(root, "intents", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "intent.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instructions.md.tmpl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProblemsErrorSortsOneLineEach(t *testing.T) {
	p := Problems{
		{File: "b/intent.toml", Line: 1, Col: 1, Field: "label", Msg: "required"},
		{File: "a/intent.toml", Line: 9, Col: 3, Field: "action.enabled", Msg: "1: unknown field"},
		{File: "a/intent.toml", Line: 2, Col: 1, Msg: "syntax error"},
	}
	want := "a/intent.toml:2:1: syntax error\n" +
		"a/intent.toml:9:3: action.enabled: 1: unknown field\n" +
		"b/intent.toml:1:1: label: required"
	if got := p.Error(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if p[0].File != "b/intent.toml" {
		t.Error("Error() reordered the problems in place")
	}
}

func TestProblemsAreSortedAndOnePerLine(t *testing.T) {
	_, problems := Load(userLayer("testdata/bad/predicate-typo"), bundledFS(), testBase(), nil)
	p := append(problems, Problem{File: "testdata/bad/a/intents/x", Msg: "x is reserved"})
	lines := strings.Split(p.Error(), "\n")
	if len(lines) != len(p) {
		t.Fatalf("%d lines for %d problems:\n%s", len(lines), len(p), p.Error())
	}
	if !slices.IsSorted(lines) || lines[0] != "testdata/bad/a/intents/x: x is reserved" {
		t.Fatalf("not sorted, or a problem without a line kept a position:\n%s", p.Error())
	}
}

func TestBadManifestsGolden(t *testing.T) {
	cases, err := os.ReadDir("testdata/bad")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata/bad", c.Name())
			l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
			if len(problems) == 0 {
				t.Fatal("no problems")
			}
			for _, name := range []dintent.Name{"review", "address", "nudge", "probe"} {
				if d, ok := l.Registry.Get(name); !ok || !d.Bundled {
					t.Errorf("the bundled intent %s did not load", name)
				}
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
				if !strings.HasPrefix(line, dir+"/intents/") {
					t.Errorf("not placed in the case's files: %s", line)
				}
			}
		})
	}
}

func TestDefaultsAreTheSpecsTable(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}\n")
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	got, ok := l.Registry.Get("ask-author")
	if !ok {
		t.Fatal("not loaded")
	}
	file := filepath.Join(root, "intents", "ask-author", "intent.toml")
	want := dintent.Def{
		Name:       "ask-author",
		Label:      "Ask author",
		Summary:    "Ask the author one question",
		UserFacing: true,
		Workspace:  "review",
		Provider:   dunit.GitHub,
		Session:    dintent.Session{Strategy: "new", Title: "ask"},
		Context:    "pr",
		Policy:     "round",
		StartLine:  "starting the ask author",
		Answerable: true,
		Board:      "review",
		Reports:    true,
		Brief:      dintent.Brief{Render: true, Wait: true},
		Records:    dintent.Pred{Src: "true", At: dintent.Pos{File: file}},
		Headline: dintent.Headline{
			Resumed: dintent.Text{Src: "ask round on {{.pr.slug}}", At: dintent.Pos{File: file, Line: 7, Col: 11}},
			New:     []dintent.Case{{Text: dintent.Text{Src: "Ask the author of {{.pr.slug}} one question.", At: dintent.Pos{File: file, Line: 9, Col: 8}}}},
		},
		Guidance: dintent.Guidance{Label: "Guidance"},
		Record:   dintent.RecordRules{States: []string{"done"}},
		Resume:   dintent.Resume{Changed: DefaultChanged},
		Source:   dintent.Source{Layer: "user", Dir: filepath.Join(root, "intents", "ask-author")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%#v\nwant\n%#v", got, want)
	}
	ps := l.Programs["ask-author"]
	if ps.Records == nil || len(ps.Headline) != 1 || ps.Headline[0] != nil || ps.Action.When != nil {
		t.Errorf("%+v", ps)
	}
}

func TestDefaultsOfAnActionAndAnInput(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal+"\n[action]\n\n[inputs.topic]\ntype = \"text\"\nflag = \"--topic\"\nhelp = \"what\"\n\n[[also_profile]]\nprofile = \"review\"\nwhen = \"true\"\ndefine = \"review-judgment\"\n", "{{.Headline}}\n")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	a := d.Action
	if a == nil || a.Group != "start" || !slices.Equal(a.Surfaces, []string{"row", "detail", "tui", "cli", "explore"}) ||
		a.When.Src != "row.open" || len(a.Refuse) != 0 || a.Primary.Src != "false" || a.Confirm.Src != "" || a.Note.Src != "" {
		t.Errorf("action %+v", a)
	}
	in := d.Inputs[0]
	if in.From != "user" || !slices.Equal(in.Surfaces, []string{"cli", "instructions"}) || in.At.Line != 16 {
		t.Errorf("input %+v", in)
	}
	if d.AlsoProfiles[0].Prefix != "review guide: " {
		t.Errorf("also %+v", d.AlsoProfiles[0])
	}
}

func TestGoodManifestLoads(t *testing.T) {
	l, problems := Load(userLayer("testdata/good/ask-author"), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, ok := l.Registry.Get("ask-author")
	if !ok || d.Bundled || d.Key != "Q" || d.StartLine != "asking the author" || d.Inputs[0].Name != "topic" || !d.Inputs[0].Persist {
		t.Fatalf("%+v", d)
	}
	ps := l.Programs["ask-author"]
	if ps.Action.When == nil || ps.Action.Primary == nil || ps.Action.ActionTexts["note"] == nil {
		t.Fatalf("%+v", ps)
	}
	var b strings.Builder
	if err := ps.Action.ActionTexts["note"].Execute(&b, ActionFactMap(func(n string) any {
		if n == "pr.author" {
			return "octo"
		}
		return nil
	})); err != nil || b.String() != "Ask octo one question" {
		t.Fatal(b.String(), err)
	}
	if l.Templates["ask-author"].Lookup(HeadlineName(0)) == nil || l.Templates["ask-author"].Lookup("review-judgment") == nil {
		t.Fatal("headline or base defines missing from the template set")
	}
	for _, name := range []dintent.Name{"review", "address", "nudge", "probe"} {
		if d, ok := l.Registry.Get(name); !ok || !d.Bundled || d.Source.Layer != "bundled" {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func greetBundle() fstest.MapFS {
	return fstest.MapFS{
		DefaultsFile:                         {Data: []byte(testDefaults)},
		"intents/greet/intent.toml":          {Data: []byte(minimal)},
		"intents/greet/instructions.md.tmpl": {Data: []byte("{{.Headline}}")},
	}
}

func writeFile(t testing.TB, file, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUserOverridesABundledTemplateAndInheritsTheManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "intents", "greet", "instructions.md.tmpl"), "mine")
	l, problems := Load(userLayer(root), greetBundle(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, ok := l.Registry.Get("greet")
	if !ok || !d.Bundled || d.Summary != "Ask the author one question" {
		t.Fatalf("%+v", d)
	}
	if want := (dintent.Source{Layer: "user", Dir: filepath.Join(root, "intents", "greet"), Overrides: []string{"instructions.md.tmpl"}}); !reflect.DeepEqual(d.Source, want) {
		t.Errorf("source %+v, want %+v", d.Source, want)
	}
	var b strings.Builder
	if err := l.Templates["greet"].Execute(&b, nil); err != nil || b.String() != "mine" {
		t.Fatalf("%q %v", b.String(), err)
	}
}

func TestUserOverridesABundledManifestWhole(t *testing.T) {
	root := t.TempDir()
	manifest := "answerable = true\n" + strings.Replace(minimal, "one question", "two questions", 1)
	writeFile(t, filepath.Join(root, "intents", "greet", "intent.toml"), manifest)
	l, problems := Load(userLayer(root), greetBundle(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("greet")
	if d.Summary != "Ask the author two questions" || !slices.Equal(d.Source.Overrides, []string{"intent.toml"}) {
		t.Fatalf("%+v", d)
	}
}

func TestABrokenOverrideNamesTheUserFileAndKeepsTheBundledIntent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "intents", "greet", "intent.toml")
	writeFile(t, file, minimal+"bogus = 1\n")
	l, problems := Load(userLayer(root), greetBundle(), testBase(), nil)
	if len(problems) == 0 || problems[0].File != file {
		t.Fatalf("problems %q", problems.Error())
	}
	d, ok := l.Registry.Get("greet")
	if !ok || !d.Bundled || len(d.Source.Overrides) > 0 {
		t.Fatalf("the bundled intent did not stay: %+v", d)
	}
	if _, broken := l.Broken["greet"]; broken {
		t.Error("a bundled intent is never broken")
	}
}

func TestAnEmptyOverrideDirectoryChangesNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "intents", "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), greetBundle(), testBase(), nil)
	if d, _ := l.Registry.Get("greet"); len(problems) > 0 || d.Source.Layer != "bundled" || len(d.Source.Overrides) > 0 {
		t.Fatalf("%q %+v", problems.Error(), d)
	}
}

func TestLoadKeepsCleanUserIntentsAndReportsTheRest(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	writeIntent(t, root, "ask-twice", strings.Replace(minimal, `workspace = "review"`, `workspace = "theirs"`, 1), "{{.Headline}}")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if _, ok := l.Registry.Get("ask-author"); !ok {
		t.Error("the clean intent did not load")
	}
	if _, ok := l.Registry.Get("ask-twice"); ok {
		t.Error("the broken intent loaded")
	}
	if len(problems) != 1 || len(l.Broken["ask-twice"]) != 1 || l.Broken["ask-twice"][0].Field != "workspace" {
		t.Fatalf("%v %+v", problems, l.Broken)
	}
	if _, ok := l.Registry.Get("review"); !ok {
		t.Error("the bundled intents did not load")
	}
}

func TestReservedNamesApplyToUserLayersOnly(t *testing.T) {
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}}
	for _, n := range []string{"review", "address", "nudge", "probe", "pause"} {
		bundled["intents/"+n+"/intent.toml"] = &fstest.MapFile{Data: []byte(minimal)}
		bundled["intents/"+n+"/instructions.md.tmpl"] = &fstest.MapFile{Data: []byte("{{.Headline}}")}
	}
	l, problems := Load(nil, bundled, testBase(), nil)
	if len(problems) > 0 || len(l.Registry.All()) != 5 {
		t.Fatalf("%v %d", problems, len(l.Registry.All()))
	}
	root := t.TempDir()
	writeIntent(t, root, "pause", minimal, "{{.Headline}}")
	l, problems = Load(userLayer(root), nil, testBase(), nil)
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "pause is reserved") || problems[0].Line != 0 || l.Broken["pause"] != nil {
		t.Fatalf("%v", problems)
	}
}

func TestLowercaseIntentKeyIsDroppedWithAProblem(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", strings.Replace(minimal, "workspace =", "shortcut = \"a\"\nworkspace =", 1), "{{.Headline}}")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) != 1 || problems[0].Field != "shortcut" || problems[0].Line != 3 ||
		problems[0].Msg != "a is the core key of approve on web; the intent loads without a shortcut (palette only)" {
		t.Fatalf("%v", problems)
	}
	d, ok := l.Registry.Get("ask-author")
	if !ok || d.Key != "" || l.Broken["ask-author"] != nil {
		t.Fatalf("the intent did not load without its key: %+v %v", d, l.Broken)
	}
}

func TestAnIntentKeyIsCanonical(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", strings.Replace(minimal, "workspace =", "shortcut = \"shift+k\"\nworkspace =", 1), "{{.Headline}}")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if d, _ := l.Registry.Get("ask-author"); d.Key != "K" {
		t.Fatalf("%q", d.Key)
	}
}

func TestAVerbIsOneThatReachesAUnitAction(t *testing.T) {
	withVerb := func(verb string) string {
		return strings.Replace(minimal, "workspace =", "verb = \""+verb+"\"\nworkspace =", 1) + "\n[action]\nsurfaces = [\"row\"]\n"
	}
	root := t.TempDir()
	for _, v := range []string{"approve", "delegate", "dismiss"} {
		writeIntent(t, root, "ok-"+v, withVerb(v), "{{.Headline}}")
	}
	for _, v := range []string{"done", "next", "waiting", "run"} {
		writeIntent(t, root, "bad-"+v, withVerb(v), "{{.Headline}}")
	}
	l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
	for _, v := range []string{"approve", "delegate", "dismiss"} {
		if d, ok := l.Registry.Get(dintent.Name("ok-" + v)); !ok || d.Verb != v {
			t.Errorf("%s: %+v %v", v, d, l.Broken[dintent.Name("ok-"+v)])
		}
	}
	for _, v := range []string{"done", "next", "waiting", "run"} {
		p := l.Broken[dintent.Name("bad-"+v)]
		if len(p) != 1 || p[0].Field != "verb" || !strings.Contains(p[0].Msg, fmt.Sprintf("%q is not one of", v)) {
			t.Errorf("%s: %v", v, p)
		}
	}
}

func TestAVerbNeedsAnAction(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", strings.Replace(minimal, "workspace =", "verb = \"delegate\"\nworkspace =", 1), "{{.Headline}}")
	l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
	if p := l.Broken["ask-author"]; len(p) != 1 || p[0].Field != "verb" || p[0].Msg != "verb needs an [action]" {
		t.Fatalf("%v", p)
	}
}

func TestASkillGoesWithAUserFacingIntent(t *testing.T) {
	root := t.TempDir()
	const target = "\n[help]\ntarget = \"a pull request URL\"\n"
	writeIntent(t, root, "ask-author", "skill = true\n"+minimal+target, "{{.Headline}}")
	writeIntent(t, root, "ask-quietly", "skill = true\nuser_facing = false\n"+minimal+target, "{{.Headline}}")
	writeIntent(t, root, "ask-blindly", "skill = true\n"+minimal, "{{.Headline}}")
	l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
	if d, ok := l.Registry.Get("ask-author"); !ok || !d.Skill || d.Help.Target != "a pull request URL" {
		t.Fatalf("ask-author: %+v %v", d, l.Broken["ask-author"])
	}
	if p := l.Broken["ask-quietly"]; len(p) != 1 || p[0].Field != "skill" || p[0].Msg != "applies to user-facing intents only" {
		t.Fatalf("%v", p)
	}
	if p := l.Broken["ask-blindly"]; len(p) != 1 || p[0].Field != "help.target" {
		t.Fatalf("%v", p)
	}
}

func TestTheBundledLayerIsReadOnce(t *testing.T) {
	bundled, err := filepath.Abs("testdata/bundled")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	writeIntent(t, ".", "ghost", minimal, "{{.Headline}}")
	l, problems := Load([]profile.Layer{{Name: "bundled", Source: "embedded"}}, os.DirFS(bundled), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if _, ok := l.Registry.Get("ghost"); ok || len(l.Registry.All()) != 4 {
		t.Fatalf("a layer with no Dir was read: %d intents", len(l.Registry.All()))
	}
}

func TestTheLastHeadlineCaseHasNoWhenAtAll(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", strings.Replace(minimal, "[[headline.new]]\n", "[[headline.new]]\nwhen = \"\"\n", 1), "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) != 1 || problems[0].Field != "headline.new[0].when" {
		t.Fatalf("%v", problems)
	}
}

func TestAnUnquotedPredicateIsPlacedWithItsIndex(t *testing.T) {
	root := t.TempDir()
	m := strings.Replace(minimal, "[[headline.new]]\n", "[[headline.new]]\nwhen = !pr.mine\ntext = \"a\"\n[[headline.new]]\n", 1)
	m = strings.Replace(m, "[[headline.new]]\ntext = \"Ask", "[[headline.new]]\nwhen = !turn.owned\ntext = \"Ask", 1)
	writeIntent(t, root, "ask-author", m, "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) != 1 || problems[0].Field != "headline.new[0].when" {
		t.Fatalf("%v", problems)
	}
}

func TestAPrepareInputDeclaresNoFlag(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal+"\n[inputs.who]\ntype = \"list\"\nfrom = \"prepare\"\nflag = \"--no-wait\"\nhelp = \"who\"\n", "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) != 2 || problems[0].Msg != "applies to inputs from the user" || problems[1].Field != "inputs.who.help" {
		t.Fatalf("%v", problems)
	}
}

func TestAPredicateProblemCarriesItsCaret(t *testing.T) {
	_, problems := Load(userLayer("testdata/bad/predicate-typo"), bundledFS(), testBase(), nil)
	if len(problems) != 1 || problems[0].Snippet != "row.open && pr.mien\n            ^" {
		t.Fatalf("%q", problems)
	}
	if strings.Contains(problems.Error(), "^") {
		t.Fatal("the snippet leaked into Error")
	}
}

func TestCacheLoadsOnceUnderConcurrentGets(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	var mu sync.Mutex
	loads := 0
	c := NewCache(func() []profile.Layer { return userLayer(root) }, nil, testBase(), func(dintent.Def, *template.Template) []Problem {
		mu.Lock()
		loads++
		mu.Unlock()
		return nil
	}, CacheOptions{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); c.Get() }()
	}
	wg.Wait()
	if loads != 1 {
		t.Fatalf("%d loads", loads)
	}
}

func TestCacheReloadsOnANewerMtime(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	calls := 0
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), func(dintent.Def, *template.Template) []Problem {
		calls++
		return nil
	}, CacheOptions{})
	first := c.Get()
	if c.Get() != first {
		t.Fatal("reloaded without a change")
	}
	toml := filepath.Join(root, "intents", "ask-author", "intent.toml")
	if err := os.WriteFile(toml, []byte(strings.Replace(minimal, "Ask author", "Ask the author", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(toml, later, later); err != nil {
		t.Fatal(err)
	}
	second := c.Get()
	if second == first {
		t.Fatal("did not reload on a newer mtime")
	}
	if d, _ := second.Registry.Get("ask-author"); d.Label != "Ask the author" {
		t.Fatalf("%+v", d)
	}
	if err := os.WriteFile(filepath.Join(root, "intent=review.md"), []byte("Be kind.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c.Get() == second {
		t.Fatal("did not reload on a new block")
	}
	if calls != 15 {
		t.Fatalf("check ran %d times, want 5 intents x 3 loads", calls)
	}
}

func TestCacheOnReloadFiresOnAChangeNotTheFirstLoad(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	reloads := 0
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), func(dintent.Def, *template.Template) []Problem { return nil },
		CacheOptions{OnReload: func() { reloads++ }})
	c.Get()
	c.Get()
	if reloads != 0 {
		t.Fatalf("%d reloads before any change", reloads)
	}
	toml := filepath.Join(root, "intents", "ask-author", "intent.toml")
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(toml, later, later); err != nil {
		t.Fatal(err)
	}
	c.Get()
	c.Get()
	if reloads != 1 {
		t.Fatalf("%d reloads, want 1", reloads)
	}
}

func TestUndeclaredInputInTemplateIsCaughtStatically(t *testing.T) {
	root := t.TempDir()
	body := "{{.Headline}}\n\n{{if .Inputs.topic}}x{{end}}\n{{.Inputs.nope}}\n{{define \"extra\"}}{{$.Inputs.other}}{{end}}"
	writeIntent(t, root, "ask-author", minimal+"\n[inputs.topic]\ntype = \"text\"\nflag = \"--topic\"\nhelp = \"what\"\n", body)
	checked := false
	_, problems := Load(userLayer(root), nil, testBase(), func(dintent.Def, *template.Template) []Problem {
		checked = true
		return nil
	})
	tmpl := filepath.Join(root, "intents", "ask-author", "instructions.md.tmpl")
	want := tmpl + ":4:3: inputs.nope is not declared\n" + tmpl + ":5:21: inputs.other is not declared"
	if problems.Error() != want {
		t.Fatalf("got\n%s\nwant\n%s", problems.Error(), want)
	}
	if checked {
		t.Error("the template check ran on a manifest that already failed")
	}
}

func TestTemplateCheckProblemsBreakTheIntent(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	l, problems := Load(userLayer(root), nil, testBase(), func(d dintent.Def, tm *template.Template) []Problem {
		if tm == nil || tm.Lookup(HeadlineName(-1)) == nil {
			t.Error("check got no template set")
		}
		return []Problem{{File: "x", Line: 1, Col: 1, Msg: "fails"}}
	})
	if len(problems) != 1 || l.Broken["ask-author"] == nil {
		t.Fatal(problems)
	}
}

func TestATypeMismatchInsideAnInlineTableKeepsTheOtherChecks(t *testing.T) {
	root := t.TempDir()
	m := strings.Replace(minimal, `session = { strategy = "new", title = "ask" }`, `session = { strategy = 1, title = "ASK" }`, 1)
	writeIntent(t, root, "ask-author", m, "{{.Headline}}")
	_, problems := Load(userLayer(root), nil, testBase(), nil)
	if e := problems.Error(); len(problems) != 2 || !strings.Contains(e, ":4:24: session.strategy: must be a string, not integer") || !strings.Contains(e, ":4:35: session.title: ") {
		t.Fatalf("%v", problems)
	}
}

func TestABrokenIntentsRulesCarryItsFirstProblem(t *testing.T) {
	l, _ := Load(userLayer("testdata/bad/predicate-typo"), bundledFS(), testBase(), nil)
	_, err := l.Rules("ask-author")
	want := `the intent "ask-author" does not load: testdata/bad/predicate-typo/intents/ask-author/intent.toml:15:8: action.when: 13: unknown field pr.mien; did you mean "pr.mine"?`
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
	if _, err := l.Rules("review"); err != nil {
		t.Fatal(err)
	}
}

func TestABrokenIntentClaimsNoKeyFlagOrRowDriver(t *testing.T) {
	claims := strings.Replace(minimal, `workspace = "review"`, `workspace = "review"
role = "pr.review"
shortcut = "Q"`, 1) + `
[inputs.topic]
type = "text"
flag = "--topic"
help = "what to ask about"
`
	root := t.TempDir()
	broken := strings.Replace(claims, `summary = `, `why = "{{.pr.mien}}"
summary = `, 1)
	writeIntent(t, root, "aa-broken", strings.Replace(broken, "[inputs.topic]", "[inputs.subject]", 1), "{{.Headline}}")
	writeIntent(t, root, "bb-clean", claims, "{{.Headline}}")
	l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
	if _, ok := l.Registry.Get("bb-clean"); !ok {
		t.Fatalf("the clean intent yielded to the broken one: %v", l.Broken["bb-clean"])
	}
	if len(l.Broken["aa-broken"]) == 0 {
		t.Fatal("the broken intent loaded")
	}
}

func TestOneIntentCannotGiveAFlagToTwoInputs(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal+`
[inputs.topic]
type = "text"
flag = "--topic"
help = "what to ask about"
[inputs.subject]
type = "text"
flag = "--topic"
help = "what to ask about"
`, "{{.Headline}}")
	l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
	if p := l.Broken["ask-author"]; len(p) != 1 || !strings.Contains(p[0].Msg, "--topic means") {
		t.Fatalf("%v", p)
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestCacheWalksOncePerRecheck(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	clock := &fakeClock{now: time.Unix(1000, 0)}
	layerCalls := 0
	c := NewCache(func() []profile.Layer { layerCalls++; return userLayer(root) }, bundledFS(), testBase(), nil,
		CacheOptions{Recheck: 2 * time.Second, Now: clock.Now})
	first := c.Get()
	for range 99 {
		clock.now = clock.now.Add(10 * time.Millisecond)
		if c.Get() != first {
			t.Fatal("reloaded inside the window")
		}
	}
	if layerCalls != 1 || c.walks != 1 {
		t.Fatalf("%d layers() calls, %d walks", layerCalls, c.walks)
	}
	clock.now = clock.now.Add(2 * time.Second)
	c.Get()
	if layerCalls != 2 || c.walks != 2 {
		t.Fatalf("%d layers() calls, %d walks after the window", layerCalls, c.walks)
	}
}

func TestCacheSeesAnEditAfterRecheck(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	clock := &fakeClock{now: time.Unix(1000, 0)}
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), nil,
		CacheOptions{Recheck: 2 * time.Second, Now: clock.Now})
	first := c.Get()
	toml := filepath.Join(root, "intents", "ask-author", "intent.toml")
	if err := os.WriteFile(toml, []byte(strings.Replace(minimal, "Ask author", "Ask the author", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(toml, later, later); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	if c.Get() != first {
		t.Fatal("saw the edit inside the window")
	}
	clock.now = clock.now.Add(time.Second)
	second := c.Get()
	if second == first {
		t.Fatal("did not see the edit after the window")
	}
	if d, _ := second.Registry.Get("ask-author"); d.Label != "Ask the author" {
		t.Fatalf("%+v", d)
	}
}

func BenchmarkCacheGetWarm(b *testing.B) {
	root := b.TempDir()
	writeIntent(b, root, "ask-author", minimal, "{{.Headline}}")
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), nil,
		CacheOptions{Recheck: time.Hour})
	c.Get()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.Get()
	}
}

func TestALoadedReadsItsTreeOnce(t *testing.T) {
	loads := 0
	c := NewCache(func() []profile.Layer { return userLayer(t.TempDir()) }, bundledFS(), testBase(), nil, CacheOptions{
		LoadTree: func([]profile.Layer, *dintent.Registry, []dintent.Name) (profile.Tree, error) {
			loads++
			return profile.Tree{}, nil
		},
	})
	l := c.Get()
	l.Tree()
	l.Tree()
	if loads != 1 {
		t.Fatalf("%d tree loads", loads)
	}
}

func writeBlock(t *testing.T, root, text string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, "intent=review.md")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func reviewText(t *testing.T, l *Loaded) string {
	t.Helper()
	tree, err := l.Tree()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, p := range tree.Compose(profile.Kind("review"), profile.Facts{}).Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func TestABlockEditReachesTheTreeAfterRecheck(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	start := time.Now()
	writeBlock(t, root, "Old.\n", start)
	clock := &fakeClock{now: start}
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), nil,
		CacheOptions{Recheck: 2 * time.Second, Now: clock.Now})
	if got := reviewText(t, c.Get()); !strings.Contains(got, "Old.") {
		t.Fatalf("%q", got)
	}
	writeBlock(t, root, "New text.\n", start.Add(time.Minute))
	clock.now = clock.now.Add(time.Second)
	if got := reviewText(t, c.Get()); !strings.Contains(got, "Old.") {
		t.Fatalf("saw the edit inside the window: %q", got)
	}
	clock.now = clock.now.Add(2 * time.Second)
	if got := reviewText(t, c.Get()); !strings.Contains(got, "New text.") || strings.Contains(got, "Old.") {
		t.Fatalf("%q", got)
	}
}

func TestABrokenBlockErrorEndsWithTheStampChange(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	start := time.Now()
	writeBlock(t, root, "\n+++\noverride = true\n+++\nX", start)
	clock := &fakeClock{now: start}
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), nil,
		CacheOptions{Recheck: 2 * time.Second, Now: clock.Now})
	if _, err := c.Get().Tree(); err == nil {
		t.Fatal("a broken block loaded")
	}
	writeBlock(t, root, "Fixed.\n", start.Add(time.Minute))
	clock.now = clock.now.Add(3 * time.Second)
	if got := reviewText(t, c.Get()); !strings.Contains(got, "Fixed.") {
		t.Fatalf("%q", got)
	}
}

func TestLoadedTreeIsSafeUnderConcurrentReads(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	writeBlock(t, root, "Be kind.\n", time.Now())
	loads := 0
	c := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), nil, CacheOptions{
		LoadTree: func(chain []profile.Layer, r *dintent.Registry, b []dintent.Name) (profile.Tree, error) {
			loads++
			return profile.Load(chain, r, b)
		},
	})
	l := c.Get()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tree, err := l.Tree()
			if err != nil {
				t.Error(err)
				return
			}
			tree.Compose(profile.Kind("review"), profile.Facts{})
		}()
	}
	wg.Wait()
	if loads != 1 {
		t.Fatalf("%d tree loads", loads)
	}
}

func TestABlockEditUnderASymlinkedProfileReachesTheTree(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	start := time.Now()
	writeBlock(t, root, "Old.\n", start)
	link := filepath.Join(t.TempDir(), "profile")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: start}
	c := NewCache(func() []profile.Layer { return userLayer(link) }, bundledFS(), testBase(), nil,
		CacheOptions{Recheck: 2 * time.Second, Now: clock.Now})
	if got := reviewText(t, c.Get()); !strings.Contains(got, "Old.") {
		t.Fatalf("%q", got)
	}
	writeBlock(t, root, "New text.\n", start.Add(time.Minute))
	clock.now = clock.now.Add(3 * time.Second)
	if got := reviewText(t, c.Get()); !strings.Contains(got, "New text.") {
		t.Fatalf("%q", got)
	}
}

func TestTheTreeIsReadWithItsRegistry(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}")
	l := NewCache(func() []profile.Layer { return userLayer(root) }, bundledFS(), testBase(), nil, CacheOptions{}).Get()
	writeIntent(t, root, "ask-later", "profile = \"ask-later\"\n"+strings.Replace(minimal, "Ask author", "Ask later", 1), "{{.Headline}}")
	if err := os.WriteFile(filepath.Join(root, "intent=ask-later.md"), []byte("Later.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Tree(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderDefaultsToGitHub(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", minimal, "{{.Headline}}\n")
	thread := strings.ReplaceAll(minimal, "{{.pr.slug}}", "{{.unit.ref}}")
	writeIntent(t, root, "ask-thread", strings.Replace(thread, `workspace = "review"`, "workspace = \"review\"\nprovider = \"slack\"", 1), "{{.Headline}}\n")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for name, want := range map[dintent.Name]dunit.ProviderID{"ask-author": dunit.GitHub, "ask-thread": "slack", "review": dunit.GitHub} {
		if d, _ := l.Registry.Get(name); d.Provider != want {
			t.Errorf("%s: provider %q, want %q", name, d.Provider, want)
		}
	}
	if got := l.Registry.ForProvider("slack"); len(got) != 1 || got[0].Name != "ask-thread" {
		t.Errorf("ForProvider(slack) = %v", got)
	}
}

func TestThreadIntentCannotReadPRFacts(t *testing.T) {
	_, problems := Load(userLayer("testdata/bad/provider-pr-fact"), bundledFS(), testBase(), nil)
	got := problems.Error()
	for _, want := range []string{"record.when: pr.author is a github fact; a slack intent cannot read it", ".PR.Author reads the github pull request; a slack intent cannot read it"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestASessionNameRendersOverItsFacts(t *testing.T) {
	dir := t.TempDir()
	m := strings.Replace(minimal, `session = { strategy = "new", title = "ask" }`, `session = { strategy = "new", title = "ask", name = '{{.session.title}} on {{.unit.label}}' }`, 1)
	writeIntent(t, dir, "ask", m, "Ask.")
	l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if got, err := l.SessionName("ask", map[string]any{"unit.label": "acme/api#7"}); err != nil || got != "ask on acme/api#7" {
		t.Fatalf("SessionName = %q, %v", got, err)
	}
}

func TestASessionNameWithoutTemplateIsTheTitle(t *testing.T) {
	dir := t.TempDir()
	writeIntent(t, dir, "ask", minimal, "Ask.")
	l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if got, err := l.SessionName("ask", map[string]any{"unit.label": "acme/api#7"}); err != nil || got != "ask" {
		t.Fatalf("SessionName = %q, %v", got, err)
	}
}

func TestASessionNameReadingAnUnknownFactFailsToLoad(t *testing.T) {
	dir := t.TempDir()
	m := strings.Replace(minimal, `session = { strategy = "new", title = "ask" }`, `session = { strategy = "new", title = "ask", name = '{{.pr.slug}}' }`, 1)
	writeIntent(t, dir, "ask", m, "Ask.")
	_, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if e := problems.Error(); !strings.Contains(e, "session.name") {
		t.Fatalf("problems = %s", e)
	}
}

func TestAUserDefaultsSessionNameNamesEveryIntentWithoutOne(t *testing.T) {
	dir := t.TempDir()
	writeIntent(t, dir, "ask", minimal, "Ask.")
	if err := os.WriteFile(filepath.Join(dir, DefaultsFile), []byte(testDefaults+"session_name = '{{.unit.label}} {{.session.title}}'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if got, err := l.SessionName("ask", map[string]any{"unit.label": "acme/api#7"}); err != nil || got != "acme/api#7 ask" {
		t.Fatalf("SessionName = %q, %v", got, err)
	}
}

func TestABrokenDefaultsSessionNameIsReportedInDefaults(t *testing.T) {
	dir := t.TempDir()
	writeIntent(t, dir, "ask", minimal, "Ask.")
	file := filepath.Join(dir, DefaultsFile)
	if err := os.WriteFile(file, []byte(testDefaults+"session_name = '{{.nothing.here}}'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if len(problems) != 1 || problems[0].File != file || !strings.Contains(problems.Error(), "session_name") {
		t.Fatalf("problems = %s", problems.Error())
	}
	if got, err := l.SessionName("ask", nil); err != nil || got != "ask" {
		t.Fatalf("SessionName = %q, %v", got, err)
	}
}

const startLike = `label = "Start"
summary = "Start new work from a brief"
provider = "entry"
workspace = { repo_input = "repo", repo_optional = true, name = '{{slug .task.title}}-{{.task.short_id}}' }
session = { strategy = "new", title = "start", name = '{{or .workspace.name .task.title}}' }

[target]
missing = "capture"
title = '{{firstLine .guidance}}'

[inputs.repo]
type = "text"
flag = "--repo"
help = "owner/name"

[headline]
resumed = "start"
[[headline.new]]
line = "Start."

[record]
states = ["done"]
`

func TestAStartIntentNamesItsWorkspaceSessionAndTask(t *testing.T) {
	dir := t.TempDir()
	writeIntent(t, dir, "begin", startLike, "Begin.")
	l, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("begin")
	if w := d.WorkspaceRepo; w == nil || w.RepoInput != "repo" || !w.Optional || w.Name.Src == "" {
		t.Fatalf("workspace = %+v", d.WorkspaceRepo)
	}
	if d.Target.Missing != dintent.TargetCapture {
		t.Fatalf("target = %+v", d.Target)
	}
	facts := map[string]any{"task.title": "Add retries to the Webhook sender!", "task.short_id": "3f9a2c"}
	ws, err := l.WorkspaceName("begin", facts)
	if err != nil || ws != "add-retries-to-the-webhook-sender-3f9a2c" {
		t.Fatalf("WorkspaceName = %q, %v", ws, err)
	}
	facts["workspace.name"] = ws
	if got, err := l.SessionName("begin", facts); err != nil || got != ws {
		t.Fatalf("SessionName = %q, %v", got, err)
	}
	facts["workspace.name"] = ""
	if got, err := l.SessionName("begin", facts); err != nil || got != "Add retries to the Webhook sender!" {
		t.Fatalf("SessionName without a workspace = %q, %v", got, err)
	}
	if got, err := l.TargetTitle("begin", "  Add retries\nto the sender, with backoff"); err != nil || got != "Add retries" {
		t.Fatalf("TargetTitle = %q, %v", got, err)
	}
}

func TestStartKeysAreChecked(t *testing.T) {
	for name, c := range map[string]struct{ from, to, want string }{
		"repo_optional needs repo_input": {`workspace = { repo_input = "repo", repo_optional = true,`, `workspace = { repo = "acme/api", repo_optional = true,`, "workspace.repo_optional"},
		"target.missing names capture":   {`missing = "capture"`, `missing = "skip"`, "target.missing"},
		"workspace.name reads its facts": {`{{slug .task.title}}`, `{{.pr.slug}}`, "workspace.name"},
		"capture needs a task intent":    {`provider = "entry"`, `provider = "github"`, "target.missing"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeIntent(t, dir, "begin", strings.Replace(startLike, c.from, c.to, 1), "Begin.")
			_, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
			if e := problems.Error(); !strings.Contains(e, c.want) {
				t.Fatalf("problems = %s", e)
			}
		})
	}
}
