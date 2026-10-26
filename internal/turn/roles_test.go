package turn

import (
	"io/fs"
	"testing"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// bundledFile is the bundled intent file name of intent.
func bundledFile(t *testing.T, intent, name string) string {
	t.Helper()
	b, err := fs.ReadFile(BundledFS(), "intents/"+intent+"/"+name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAUserIntentTakingARoleTakesTheBundledHoldersKeys(t *testing.T) {
	l, problems := userIntent(t, "my-review", bundledFile(t, "review", "intent.toml"), bundledFile(t, "review", "instructions.md.tmpl"))
	if len(problems) > 0 {
		t.Fatalf("%s", problems.Error())
	}
	mine, ok := l.Registry.Get("my-review")
	if !ok || mine.Key != "V" || !mine.DrivesRow || mine.Reconcile != "my_review" {
		t.Fatalf("my-review: %+v", mine)
	}
	bundled, ok := l.Registry.Get(dintent.Review)
	if !ok || bundled.Key != "" || bundled.DrivesRow {
		t.Fatalf("the bundled review keeps loading without the role's keys: key %q drives_row %v", bundled.Key, bundled.DrivesRow)
	}
	if d, ok := l.Registry.ByRole(builtin.RoleReview, "github"); !ok || d.Name != "my-review" {
		t.Fatalf("by role: %v", d.Name)
	}
	var drivers []dintent.Name
	for _, d := range l.Registry.All() {
		if d.DrivesRow && d.Workspace == "review" {
			drivers = append(drivers, d.Name)
		}
	}
	if len(drivers) != 1 || drivers[0] != "my-review" {
		t.Fatalf("the review rows are driven by %v", drivers)
	}
}

func TestABrokenRoleHolderLeavesTheBundledOneItsKeys(t *testing.T) {
	l, _ := userIntent(t, "my-review", bundledFile(t, "review", "intent.toml"), "{{.Nope")
	if _, ok := l.Broken["my-review"]; !ok {
		t.Fatal("my-review should not load")
	}
	if d, _ := l.Registry.Get(dintent.Review); d.Key != "V" || !d.DrivesRow {
		t.Fatalf("the bundled review keeps its keys: key %q drives_row %v", d.Key, d.DrivesRow)
	}
}
