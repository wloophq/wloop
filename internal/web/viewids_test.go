package web

import (
	"os"
	"path/filepath"
	"testing"

	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// TestNoPluginViewSharesALensViewsID checks the bundled plugins' views against the bundled profile's: the
// cockpit's rail keeps one view per id, so a plugin view of a lens view's id would hide one of them.
func TestNoPluginViewSharesALensViewsID(t *testing.T) {
	views, problems := intentload.BundledViews(os.DirFS("../../profiles/default"))
	if len(problems) > 0 || len(views.Views) == 0 {
		t.Fatalf("the bundled views: %d, problems %v", len(views.Views), problems)
	}
	dirs, err := filepath.Glob("../../plugins/*/plugin.toml")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no bundled plugin: %v", err)
	}
	for _, path := range dirs {
		m, err := manifest.Load(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		src, err := m.Source()
		if err != nil {
			t.Fatal(err)
		}
		if src.UI == nil {
			continue
		}
		for _, v := range src.UI.Views {
			if _, ok := views.View(v.ID); ok {
				t.Errorf("%s: view %s shares the id of a lens view", m.ID, v.ID)
			}
		}
	}
}
