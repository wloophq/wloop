package intent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/profile"
)

// TestPreviewsResolveThroughTheProfileLayers reads a kind's preview from its plugin's profile layer, never the
// plugin's directory, and lets the user's layer of that plugin replace it; a template that does not parse is a
// problem and is left out.
func TestPreviewsResolveThroughTheProfileLayers(t *testing.T) {
	bundled := profile.BundledChain()
	got, problems := loadPreviews(bundled, readerOf(nil))
	want, err := os.ReadFile("../../plugins/slack/profile/drafts/edit/preview.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 || got["slack"]["edit"] != string(want) {
		t.Fatalf("slack edit preview %q, problems %v, want the layer's %q", got["slack"]["edit"], problems, want)
	}
	user := t.TempDir()
	write := func(kind, body string) {
		p := filepath.Join(user, "drafts", kind, "preview.md.tmpl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("edit", "Rewrite {{.Target}}")
	write("delete", "Remove {{.Target")
	got, problems = loadPreviews(append(bundled, profile.Layer{Name: "user:slack", Source: user, Dir: user}), readerOf(nil))
	if got["slack"]["edit"] != "Rewrite {{.Target}}" {
		t.Errorf("edit preview %q, want the user's layer's", got["slack"]["edit"])
	}
	if len(problems) != 1 || got["slack"]["delete"] == "Remove {{.Target" {
		t.Errorf("problems %v, delete %q: want the broken template refused and the lower one kept", problems, got["slack"]["delete"])
	}
}
