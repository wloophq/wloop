package turn

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/profiles"
)

// userPartials is a user profile layer holding files under partials/.
func userPartials(t *testing.T, files map[string]string) []profile.Layer {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, "partials", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return append(profile.BundledChain(), profile.Layer{Name: "user", Source: dir, Dir: dir})
}

func loadWith(t *testing.T, layers []profile.Layer) (*intentload.Loaded, intentload.Problems) {
	t.Helper()
	b, err := Base()
	if err != nil {
		t.Fatal(err)
	}
	return intentload.Load(layers, BundledFS(), b, CheckTemplate)
}

var firstDefine = regexp.MustCompile(`^\{\{-?\s*define "[^"]+"\s*-?\}\}`)

const myRecord = "{{define \"record\"}}\n### My record\n\nCall `turn_end`, or run `{{.Command}} turn end {{.Payload.Arg}} --turn <id>`, with `state` and `summary`.\n{{end}}"

func TestAUserRecordPartialReplacesItForEveryIntent(t *testing.T) {
	l, problems := loadWith(t, userPartials(t, map[string]string{"record.md.tmpl": myRecord}))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	recorded := map[Intent]bool{}
	for _, s := range Scenarios() {
		c, err := Complete(s.Context, l)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Render(c, l, "")
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		if strings.Contains(r.Text, "### Record the end of the turn") {
			t.Errorf("%s: the bundled record partial rendered", s.Name)
		}
		if strings.Contains(r.Text, "### My record") {
			recorded[c.Intent] = true
		}
	}
	for _, in := range []Intent{"review", "address", "nudge"} {
		if !recorded[in] {
			t.Errorf("no %s scenario rendered the user's record partial", in)
		}
	}
}

func TestEveryPartialResolvesThroughTheLayers(t *testing.T) {
	names, err := fs.Glob(profiles.Default, "default/partials/*.tmpl")
	if err != nil || len(names) == 0 {
		t.Fatalf("%v %v", names, err)
	}
	for _, name := range names {
		file := path.Base(name)
		raw, _ := fs.ReadFile(profiles.Default, name)
		mine := firstDefine.ReplaceAllString(string(raw), "${0}MINE-"+file)
		l, problems := loadWith(t, userPartials(t, map[string]string{file: mine}))
		if len(problems) > 0 {
			t.Fatalf("%s: %v", file, problems)
		}
		found := false
		for _, s := range Scenarios() {
			c, err := Complete(s.Context, l)
			if err != nil {
				t.Fatal(err)
			}
			c.Why = &Why{Workflow: "follow-up", Trigger: "event", Event: "pr.pushed", Matched: []WhyFact{{Name: "pr.draft", Value: "false"}}}
			if r, err := Render(c, l, ""); err == nil && strings.Contains(r.Text, "MINE-"+file) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no scenario rendered the user's %s", file)
		}
	}
}

func TestARecordPartialMissingTheProtocolLoadsTheBundledOne(t *testing.T) {
	layers := userPartials(t, map[string]string{"record.md.tmpl": "{{define \"record\"}}\n### My record\n\nTell the user you are done.\n{{end}}"})
	l, problems := loadWith(t, layers)
	if len(problems) == 0 || !strings.Contains(problems.Error(), filepath.Join(layers[1].Dir, "partials", "record.md.tmpl")) || !strings.Contains(problems.Error(), "turn_end") {
		t.Fatalf("problems %v", problems)
	}
	r, err := render(scenario(t, "review/resume/answered").Context)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Complete(scenario(t, "review/resume/answered").Context, l)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(c, l, "")
	if err != nil || got.Text != r.Text {
		t.Fatalf("the bundled record did not load back: %v\n%s", err, got.Text)
	}
}

func TestABrokenUserPartialLoadsTheBundledOne(t *testing.T) {
	for name, body := range map[string]string{
		"parse": "{{define \"history\"}}{{if}}{{end}}",
		"exec":  "{{define \"history\"}}{{.NoSuchField}}{{end}}",
	} {
		layers := userPartials(t, map[string]string{"history.md.tmpl": body})
		l, problems := loadWith(t, layers)
		if len(problems) == 0 || !strings.Contains(problems.Error(), filepath.Join(layers[1].Dir, "partials", "history.md.tmpl")) {
			t.Fatalf("%s: problems %v", name, problems)
		}
		if _, ok := l.Registry.Get("review"); !ok {
			t.Fatalf("%s: the bundled review did not load", name)
		}
	}
}

func TestAUserProfileAddsAPartialItsIntentsCall(t *testing.T) {
	layers := userPartials(t, map[string]string{"sign-off.md.tmpl": `{{define "sign-off"}}Signed, {{name}}.{{end}}`})
	l, problems := loadWith(t, layers)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if l.Templates["review"].Lookup("sign-off") == nil {
		t.Fatal("the user's partial is not in the review's set")
	}
}
