package config

import (
	"slices"
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

func TestDocUnsetRemovesTheKeysLinesAndKeepsTheRest(t *testing.T) {
	raw := "# acme's loop\nagent = 'claude' # the agent\n\n# switches\n[workflows.acme-sync]\nenabled = false\nobserve = [\n  'a',\n]\n# after\n"
	for _, c := range []struct {
		path []string
		want string
	}{
		{[]string{"agent"}, "# acme's loop\n\n# switches\n[workflows.acme-sync]\nenabled = false\nobserve = [\n  'a',\n]\n# after\n"},
		{[]string{"workflows", "acme-sync", "observe"}, "# acme's loop\nagent = 'claude' # the agent\n\n# switches\n[workflows.acme-sync]\nenabled = false\n# after\n"},
	} {
		d, err := ParseDoc([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Unset(c.path); err != nil || string(d.Bytes()) != c.want {
			t.Errorf("%v: %v, got:\n%s\nwant:\n%s", c.path, err, d.Bytes(), c.want)
		}
	}
}

func TestDocUnsetOfAKeyTheFileLacksChangesNothing(t *testing.T) {
	raw := "[workflows.acme-sync]\nenabled = false\n"
	d, err := ParseDoc([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Unset([]string{"workflows", "acme-sync", "observe"}); err != nil || string(d.Bytes()) != raw {
		t.Fatalf("%v:\n%s", err, d.Bytes())
	}
}

func TestDocUnsetRefusesATableAnInlineTableAndAnArrayOfTables(t *testing.T) {
	for _, c := range []struct {
		raw  string
		path []string
	}{
		{"[workflows.acme-sync]\nenabled = false\n", []string{"workflows", "acme-sync"}},
		{"views = {orca = {columns = {needs-me = 'x'}}}\n", []string{"views", "orca", "columns", "needs-me"}},
		{"[[projects]]\nmatch = 'acme/*'\n", []string{"projects", "match"}},
	} {
		d, err := ParseDoc([]byte(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Unset(c.path); err == nil || string(d.Bytes()) != c.raw {
			t.Errorf("%v: %v\n%s", c.path, err, d.Bytes())
		}
	}
}

func TestDocSetRefusesAKeyInAnArrayOfTables(t *testing.T) {
	raw := "[[projects]]\nmatch = 'acme/*'\n\n[[projects]]\nmatch = 'beta/*'\n"
	if _, err := set(t, raw, []string{"projects", "match"}, "gamma/*"); err == nil || !strings.Contains(err.Error(), "projects") {
		t.Fatalf("err %v", err)
	}
}

func TestDocPutWritesAnEmptyValue(t *testing.T) {
	d, err := ParseDoc([]byte("[plugins.github]\nenabled = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Put([]string{"plugins", "github", "automated_reviewers"}, []string{}); err != nil {
		t.Fatal(err)
	}
	if want := "[plugins.github]\nenabled = true\nautomated_reviewers = []\n"; string(d.Bytes()) != want {
		t.Fatalf("got:\n%s", d.Bytes())
	}
}

func TestParseKey(t *testing.T) {
	for in, want := range map[string][]string{
		"agent":                         {"agent"},
		"workflows.acme-sync.enabled":   {"workflows", "acme-sync", "enabled"},
		`workflows."acme.sync".enabled`: {"workflows", "acme.sync", "enabled"},
	} {
		if got, err := ParseKey(in); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "a..b", "a = 1", "a.", "a\nb"} {
		if got, err := ParseKey(in); err == nil {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestParseValue(t *testing.T) {
	for in, want := range map[string]any{
		"true":                 true,
		"47811":                int64(47811),
		"0.5":                  0.5,
		`"5"`:                  "5",
		"5m":                   "5m",
		"2026-01-02":           "2026-01-02",
		"2026-01-02T10:00:00":  "2026-01-02T10:00:00",
		"2026-01-02T10:00:00Z": "2026-01-02T10:00:00Z",
		"12:30:00":             "12:30:00",
		"1\nmcp.port = 2":      "1\nmcp.port = 2",
	} {
		if got := ParseValue(in); got != want {
			t.Errorf("%q: %#v, want %#v", in, got, want)
		}
	}
	if got, ok := ParseValue(`["a", "b"]`).([]any); !ok || len(got) != 2 || got[0] != "a" {
		t.Errorf("list: %#v", got)
	}
}
