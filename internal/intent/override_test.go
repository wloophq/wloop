package intent

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/profile"
)

func bundledFile(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata/bundled", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func composed(t *testing.T, tree profile.Tree, kind string) string {
	t.Helper()
	var b strings.Builder
	for _, p := range tree.Compose(profile.Kind(kind), profile.Facts{}).Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func TestTheBlocksOfAProfileOnlyARevertedOverrideDeclaredAreSkipped(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "intents", "probe", "intent.toml")
	writeFile(t, file, "profile = \"probe\"\n"+bundledFile(t, "intents/probe/intent.toml"))
	writeFile(t, filepath.Join(root, "intents", "probe", "instructions.md.tmpl"), "{{")
	writeFile(t, filepath.Join(root, "intent=probe.md"), "Probe gently.\n")
	writeFile(t, filepath.Join(root, "intent=review.md"), "Mine.\n")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) == 0 || !strings.HasPrefix(problems.Sorted()[0].File, filepath.Join(root, "intents", "probe")) {
		t.Fatalf("problems %q", problems.Error())
	}
	tree, err := l.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if got := composed(t, tree, "review"); !strings.Contains(got, "Mine.") {
		t.Errorf("the review blocks were skipped too: %q", got)
	}
}

func TestTheBlocksOfDimsOnlyARevertedOverrideDeclaredAreSkipped(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "intents", "address", "intent.toml"), "dims = [\"round\"]\n"+bundledFile(t, "intents/address/intent.toml"))
	writeFile(t, filepath.Join(root, "intents", "address", "instructions.md.tmpl"), "{{")
	writeFile(t, filepath.Join(root, "round=first", "intent=address.md"), "First round.\n")
	writeFile(t, filepath.Join(root, "intent=review.md"), "Mine.\n")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) == 0 {
		t.Fatal("no problems")
	}
	tree, err := l.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if got := composed(t, tree, "review"); !strings.Contains(got, "Mine.") {
		t.Errorf("the review blocks were skipped too: %q", got)
	}
}

func TestABundledIntentBrokenByAClashBlamesOnlyTheOverrideHoldingTheClaim(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "intents", "address", "intent.toml")
	writeFile(t, file, strings.Replace(bundledFile(t, "intents/address/intent.toml"), `shortcut = "F"`, `shortcut = "V"`, 1))
	writeFile(t, filepath.Join(root, "intents", "nudge", "instructions.md.tmpl"), "{{.Headline}}\nMy nudge.\n")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) == 0 {
		t.Fatal("no problems")
	}
	for _, p := range problems {
		if p.File != file {
			t.Errorf("a problem off the clashing override: %s", Problems{p}.Error())
		}
	}
	if d, _ := l.Registry.Get("nudge"); !slices.Equal(d.Source.Overrides, []string{"instructions.md.tmpl"}) {
		t.Errorf("the nudge override was reverted: %+v", d.Source)
	}
	if d, _ := l.Registry.Get("review"); d.Key != "V" {
		t.Errorf("review lost its key: %q", d.Key)
	}
	if d, _ := l.Registry.Get("address"); d.Key != "F" || len(d.Source.Overrides) > 0 {
		t.Errorf("address is not the bundled one: %+v", d)
	}
}

func TestAManifestOverrideBreakingTheBundledTemplateNamesTheUserManifest(t *testing.T) {
	root := t.TempDir()
	m := bundledFile(t, "intents/address/intent.toml")
	m = strings.Replace(m, "[inputs.self_review]\ntype = \"bool\"\nflag = \"--self-review\"\nhelp = \"reviews the change first\"\npersist = true\nby_hand_only = true\n", "", 1)
	m = strings.Replace(m, `when    = "inputs.self_review && !turn.needs_take_over"`, `when    = "!turn.needs_take_over"`, 1)
	m = strings.Replace(m, "[join]\ndiffers_on = [\"self_review\"]\n", "", 1)
	file := filepath.Join(root, "intents", "address", "intent.toml")
	writeFile(t, file, m)
	_, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) == 0 {
		t.Fatal("no problems")
	}
	for _, p := range problems {
		if p.File != file {
			t.Errorf("not on the user's manifest: %s", Problems{p}.Error())
		}
	}
	if !strings.Contains(problems.Error(), "intents/address/instructions.md.tmpl") {
		t.Errorf("the bundled template is not named: %s", problems.Error())
	}
}

func TestAnUnreadableOverrideIsAProblemOnTheUserFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	root := t.TempDir()
	file := filepath.Join(root, "intents", "greet", "intent.toml")
	writeFile(t, file, minimal)
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), greetBundle(), testBase(), nil)
	if len(problems) != 1 || problems[0].File != file || strings.Contains(problems[0].Msg, "missing") {
		t.Fatalf("problems %q", problems.Error())
	}
	if d, _ := l.Registry.Get("greet"); len(d.Source.Overrides) > 0 {
		t.Errorf("shown as overridden: %+v", d.Source)
	}
}

func TestADirectoryNamedAsTheManifestIsAProblem(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "intents", "greet", "intent.toml", "notes"), "x")
	_, problems := Load(userLayer(root), greetBundle(), testBase(), nil)
	if len(problems) != 1 || problems[0].File != filepath.Join(root, "intents", "greet", "intent.toml") {
		t.Fatalf("problems %q", problems.Error())
	}
}

func TestAnUnreadableWorkflowOverrideIsAProblemOnTheUserFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}, "workflows/close-quiet/workflow.toml": {Data: []byte(quietSession)}}
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession)
	file := filepath.Join(root, "workflows", "close-quiet", "workflow.toml")
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), bundled, testBase(), nil)
	if len(problems) != 1 || problems[0].File != file || strings.Contains(problems[0].Msg, "missing") {
		t.Fatalf("problems %q", problems.Error())
	}
	if _, ok := l.RevertedWorkflows["close-quiet"]; !ok || len(l.Workflows) != 1 || len(l.Workflows[0].Overrides) > 0 {
		t.Fatalf("%+v %v", l.Workflows, l.RevertedWorkflows)
	}
}

func TestARevertedOverrideIsListed(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "intents", "greet", "intent.toml")
	writeFile(t, file, minimal+"bogus = 1\n")
	l, _ := Load(userLayer(root), greetBundle(), testBase(), nil)
	if p := l.Reverted["greet"]; len(p) == 0 || p[0].File != file {
		t.Fatalf("reverted %v", l.Reverted)
	}
}

func TestAnOverrideClashingWithAUserIntentYields(t *testing.T) {
	root := t.TempDir()
	writeIntent(t, root, "ask-author", strings.Replace(minimal, "workspace =", "shortcut = \"K\"\nworkspace =", 1), "{{.Headline}}")
	file := filepath.Join(root, "intents", "review", "intent.toml")
	writeFile(t, file, strings.Replace(bundledFile(t, "intents/review/intent.toml"), `shortcut = "V"`, `shortcut = "K"`, 1))
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) == 0 {
		t.Fatal("no problems")
	}
	for _, p := range problems {
		if p.File != file {
			t.Errorf("a problem off the override: %s", Problems{p}.Error())
		}
	}
	if d, _ := l.Registry.Get("ask-author"); d.Key == "" {
		t.Error("the user intent lost its key")
	}
	if d, _ := l.Registry.Get("review"); d.Key != "V" || len(d.Source.Overrides) > 0 {
		t.Errorf("review is not the bundled one: %+v", d)
	}
}

func TestDotfilesDoNotOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "intents", "probe", ".DS_Store"), "x")
	writeFile(t, filepath.Join(root, "intents", "address", ".DS_Store"), "x")
	writeFile(t, filepath.Join(root, "intents", "address", "instructions.md.tmpl"), "{{.Headline}}\n")
	writeFile(t, filepath.Join(root, "workflows", "close-quiet", ".DS_Store"), "x")
	bundled := fstest.MapFS{DefaultsFile: {Data: []byte(testDefaults)}, "workflows/close-quiet/workflow.toml": {Data: []byte(quietSession)}}
	if err := fs.WalkDir(bundledFS(), ".", func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			bundled[p] = &fstest.MapFile{Data: []byte(bundledFile(t, p))}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	l, problems := Load(userLayer(root), bundled, testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if d, _ := l.Registry.Get("probe"); d.Source.Layer != "bundled" {
		t.Errorf("probe: %+v", d.Source)
	}
	if d, _ := l.Registry.Get(dintent.Name("address")); !slices.Equal(d.Source.Overrides, []string{"instructions.md.tmpl"}) {
		t.Errorf("address: %+v", d.Source)
	}
	if len(l.Workflows) != 1 || len(l.Workflows[0].Overrides) > 0 || l.Workflows[0].Source != "workflows/close-quiet/workflow.toml" {
		t.Errorf("%+v", l.Workflows)
	}
}
