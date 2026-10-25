package archtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/archtest/rulesdoc"
)

func TestRuleDocsFlagsAnUnstatedRule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := `package archtest

// TestStated checks that a rule is stated.
func TestStated(t *testing.T) {}

func TestBare(t *testing.T) {}

// TestMisworded fails when a rule breaks.
func TestMisworded(t *testing.T) {}

func TestBareFlagsAPlantedViolation(t *testing.T) { flagged(t, "", nil, 0, "") }
`
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, err := rulesdoc.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	flagged(t, "planted", ruleDocs(rules), 2, "a rule test states its rule")
}
