package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func set(t *testing.T, raw string, path []string, v any) (string, error) {
	t.Helper()
	d, err := ParseDoc([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Set(path, v)
	if err != nil && string(d.Bytes()) != raw {
		t.Errorf("a refused Set changed the document:\n%s", d.Bytes())
	}
	return string(d.Bytes()), err
}

func TestDocRefusesToChangeAValueInAnInlineTable(t *testing.T) {
	raw := "[github]\nscope = { repos = [\"a/b\"] }\n"
	for _, v := range [][]string{{}, {"acme/api"}} {
		if _, err := set(t, raw, []string{"github", "scope", "repos"}, v); err == nil || !strings.Contains(err.Error(), "github.scope.repos") {
			t.Errorf("%q: %v", v, err)
		}
	}
	for _, c := range []struct {
		path []string
		v    any
	}{{[]string{"github", "scope", "repos"}, []string{"a/b"}}, {[]string{"github", "scope", "teams"}, []string{}}} {
		if got, err := set(t, raw, c.path, c.v); err != nil || got != raw {
			t.Errorf("%v: %v\n%s", c.path, err, got)
		}
	}
}

func TestDocWritesANewHeaderWhereADottedKeyWouldRedefineATable(t *testing.T) {
	raw := "[github.scope.x]\na = 1\n\n[github]\nenabled = true\n"
	got, err := set(t, raw, []string{"github", "scope", "repos"}, []string{"acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		GitHub struct{ Scope struct{ Repos []string } }
	}
	if err := toml.Unmarshal([]byte(got), &doc); err != nil || len(doc.GitHub.Scope.Repos) != 1 {
		t.Fatalf("%v, %+v:\n%s", err, doc, got)
	}
}

func TestDocRefusesAnEditThatLeavesTheFileInvalid(t *testing.T) {
	raw := "[[github.scope]]\nrepos = []\n"
	if _, err := set(t, raw, []string{"github", "scope", "teams"}, []string{"acme/core"}); err == nil || !strings.Contains(err.Error(), "github.scope.teams") {
		t.Fatalf("err %v", err)
	}
}

func TestDocAddsARootKeyBelowTheFilesOpeningComment(t *testing.T) {
	raw := "# acme's loop\n# notes\n\n# the plugin\n[github]\nenabled = true\n"
	got, err := set(t, raw, []string{"agent"}, "claude")
	if want := "# acme's loop\n# notes\n\nagent = 'claude'\n\n# the plugin\n[github]\nenabled = true\n"; err != nil || got != want {
		t.Fatalf("%v, got:\n%s\nwant:\n%s", err, got, want)
	}
	got, err = set(t, "# only a comment\n", []string{"agent"}, "claude")
	if want := "# only a comment\nagent = 'claude'\n"; err != nil || got != want {
		t.Fatalf("%v, got:\n%s", err, got)
	}
}

func TestDocWritesAnyTOMLValueAndRefusesATable(t *testing.T) {
	got, err := set(t, "", []string{"github", "enabled"}, false)
	if want := "[github]\nenabled = false\n"; err != nil || got != want {
		t.Fatalf("%v, got:\n%s", err, got)
	}
	if _, err := set(t, "", []string{"github"}, map[string]any{"enabled": false}); err == nil {
		t.Fatal("a table was written as a value")
	}
}

func TestDocParsesOncePerEdit(t *testing.T) {
	d, err := ParseDoc([]byte("agent = 'claude'\n\n[github.scope]\nrepos = ['acme/api']\n"))
	if err != nil {
		t.Fatal(err)
	}
	before := d.parses
	_ = d.Get("agent")
	for _, s := range []struct {
		path []string
		v    any
	}{{[]string{"agent"}, "claude"}, {[]string{"github", "scope", "repos"}, []string{"acme/api"}}, {[]string{"github", "scope", "teams"}, []string{}}} {
		if err := d.Set(s.path, s.v); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Set([]string{"slack", "client_id"}, "1.2"); err != nil {
		t.Fatal(err)
	}
	if n := d.parses - before; n != 1 {
		t.Errorf("%d parses for one edit and four reads", n)
	}
}
