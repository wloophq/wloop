package rulesdoc

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func plant(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const planted = `package archtest

import "testing"

type allowed struct{ file, literal, reason string }

type edge struct{ from, to string }

var allowlist = []allowed{
	{"internal/b.go", "x", "b | needs x"},
	{"internal/a.go", "y", "a needs y"},
}

var exempt = map[string]string{"internal/z": "z spawns", "internal/m": "m spawns"}

var debt = []edge{{"internal/p", "internal/q"}}

var names = []string{"not", "an", "allowlist"}

func flagged(t *testing.T, what string, got []string, n int, want string) {}

func scan() []string { return check(allowlist) }

func check(a []allowed) []string { _ = names; return nil }

// TestZebra checks that zebras stay striped.
func TestZebra(t *testing.T) {
	_ = scan()
	_ = exempt
}

// TestAardvark checks that aardvarks
// dig only where allowed.
func TestAardvark(t *testing.T) { _ = debt }

func TestUndocumented(t *testing.T) {}

// TestMisworded fails when something moves.
func TestMisworded(t *testing.T) {}

func TestZebraFlagsAPlantedHorse(t *testing.T) {
	flagged(t, "horse", nil, 1, "")
}

func helper(t *testing.T) {}
`

func TestReadFindsRuleTestsAndTheirAllowlists(t *testing.T) {
	t.Parallel()
	dir := plant(t, map[string]string{"rules_test.go": planted, "notes.go": "package archtest\n\nfunc TestIgnored() {}\n"})
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Rule{
		{Test: "TestAardvark", File: "rules_test.go", Rule: "Aardvarks dig only where allowed.", Allowlists: []Allowlist{
			{Name: "debt", Entries: []Entry{{Item: []string{"internal/p", "internal/q"}}}},
		}},
		{Test: "TestMisworded", File: "rules_test.go"},
		{Test: "TestUndocumented", File: "rules_test.go"},
		{Test: "TestZebra", File: "rules_test.go", Rule: "Zebras stay striped.", Allowlists: []Allowlist{
			{Name: "allowlist", Entries: []Entry{
				{Item: []string{"internal/a.go", "y"}, Reason: "a needs y"},
				{Item: []string{"internal/b.go", "x"}, Reason: "b | needs x"},
			}},
			{Name: "exempt", Entries: []Entry{
				{Item: []string{"internal/m"}, Reason: "m spawns"},
				{Item: []string{"internal/z"}, Reason: "z spawns"},
			}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestPageIsStableAndEscapesCells(t *testing.T) {
	t.Parallel()
	dir := plant(t, map[string]string{"rules_test.go": planted})
	rules, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	page := Page(rules)
	if again := Page(rules); again != page {
		t.Fatal("two renders differ")
	}
	for _, want := range []string{
		"| `TestAardvark` | Aardvarks dig only where allowed. |\n",
		"| `TestZebra` | Zebras stay striped. |\n",
		"### `TestZebra`\n",
		"| `internal/b.go` `x` | b \\| needs x |\n",
		"| `debt` entry |\n|---|\n| `internal/p` `internal/q` |\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Index(page, "`TestAardvark`") > strings.Index(page, "`TestZebra`") {
		t.Error("rules are not sorted by test name")
	}
	if strings.Contains(page, "TestZebraFlagsAPlantedHorse") {
		t.Error("a self-test is listed as a rule")
	}
}
