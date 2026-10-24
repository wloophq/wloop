package tag

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	for _, c := range []struct {
		key, value, want string
		ok               bool
	}{
		{"lang", "Python", "python", true},
		{"lang", "C++", "cpp", true},
		{"lang", "C#", "csharp", true},
		{"lang", "golang", "go", true},
		{"domain", "Petição Inicial", "peticao-inicial", true},
		{"framework", "Node.js", "nodejs", true},
		{"tool", "k8s", "kubernetes", true},
		{"tool", "GitHub_Actions", "github-actions", true},
		{"lang", "oauth2", "oauth2", true},
		{"change", "refactoring", "refac", true},
		{"change", "Bug", "fix", true},
		{"area", "data model", "data-model", true},
		{"area", "documentation", "docs", true},
		{"domain", "js", "js", true},
		{"domain", "Golang", "golang", true},
		{"change", "banana", "", false},
		{"lang", "3d", "", false},
		{"domain", strings.Repeat("a", 40), "", false},
		{"lang", "", "", false},
		{"lang", "!!", "", false},
		{"color", "red", "", false},
	} {
		got, ok := Normalize(c.key, c.value)
		if got != c.want || ok != c.ok {
			t.Errorf("Normalize(%q, %q) = %q, %v; want %q, %v", c.key, c.value, got, ok, c.want, c.ok)
		}
	}
}

func TestParseTakesOnlyANormalizedTag(t *testing.T) {
	if k, v, ok := Parse("lang:go"); !ok || k != "lang" || v != "go" {
		t.Fatal(k, v, ok)
	}
	for _, s := range []string{"lang:Go", "lang", "color:red", "change:banana", "lang:golang"} {
		if _, _, ok := Parse(s); ok {
			t.Errorf("parsed %q", s)
		}
	}
}

func TestFromObjectLimits(t *testing.T) {
	raw := map[string][]string{
		"lang":   {"Go", "golang", "python", "rust", "ruby"},
		"change": {"feat", "banana"},
		"color":  {"red"},
	}
	for i := range 20 {
		raw["domain"] = append(raw["domain"], fmt.Sprintf("d%d", i))
	}
	for _, k := range []string{"area", "concern", "audience", "framework", "tool"} {
		raw[k] = nil
	}
	tags, dropped := FromObject(raw)
	want := []string{"lang:go", "lang:python", "lang:rust", "change:feature", "domain:d0", "domain:d1", "domain:d2"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags %v", tags)
	}
	wantDropped := []string{"lang:ruby", "change:banana"}
	for i := 3; i < 20; i++ {
		wantDropped = append(wantDropped, fmt.Sprintf("domain:d%d", i))
	}
	wantDropped = append(wantDropped, "color:red")
	if !reflect.DeepEqual(dropped, wantDropped) {
		t.Fatalf("dropped %v", dropped)
	}
}

func TestFromObjectStopsAtMax(t *testing.T) {
	raw := map[string][]string{}
	for _, k := range Keys {
		if k.Open() {
			raw[k.Name] = []string{"a1", "a2", "a3"}
		} else {
			raw[k.Name] = k.Closed[:min(3, len(k.Closed))]
		}
	}
	tags, dropped := FromObject(raw)
	if len(tags) != Max || len(dropped) != 9*3-Max {
		t.Fatalf("%d tags, %d dropped: %v", len(tags), len(dropped), dropped)
	}
}

func TestVocabularyListsOpenKeysInOrder(t *testing.T) {
	got := Vocabulary(map[string][]Count{
		"domain": {{"billing", 4}},
		"lang":   {{"go", 12}, {"python", 3}},
		"change": {{"fix", 9}},
	})
	if want := "- lang: go (12), python (3)\n- domain: billing (4)"; got != want {
		t.Fatalf("%q", got)
	}
	if Vocabulary(nil) != "(none yet)" {
		t.Fatal(Vocabulary(nil))
	}
}

func TestWithSeedsAddsTheUnusedSeeds(t *testing.T) {
	got := WithSeeds(map[string][]Count{"lang": {{Value: "go", Count: 4}}, "domain": {{Value: "billing", Count: 1}}}, 3)
	want := map[string][]Count{
		"lang":      {{Value: "go", Count: 4}, {Value: "python"}, {Value: "typescript"}},
		"framework": {{Value: "django"}, {Value: "flask"}, {Value: "fastapi"}},
		"tool":      {{Value: "docker"}, {Value: "kubernetes"}, {Value: "terraform"}},
		"domain":    {{Value: "billing", Count: 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if v := Vocabulary(got); !strings.HasPrefix(v, "- lang: go (4), python, typescript\n") {
		t.Fatalf("%q", v)
	}
}

func TestOpenKeys(t *testing.T) {
	if got := OpenKeys(); !reflect.DeepEqual(got, []string{"lang", "framework", "tool", "domain"}) {
		t.Fatal(got)
	}
}
