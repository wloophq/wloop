package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

func TestEveryNoticeHasABundledText(t *testing.T) {
	notices, problems := loadNotices(nil, os.DirFS("../../profiles/default"))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	for _, n := range builtin.Notices {
		if notices[n.Name] == "" {
			t.Errorf("%s has no text", n.Name)
		}
	}
}

func TestAUserNoticeReplacesTheBundledOneUnlessItFails(t *testing.T) {
	bundled := "answered = \"answered {{.Gate}}\"\nrecord_owed = \"owed {{.Turn}}\"\n"
	dir := t.TempDir()
	user := "answered = \"look at {{.Gate}} on {{.Unit}}\"\nrecord_owed = \"{{.Nope}}\"\nanswred = \"x\"\n"
	if err := os.WriteFile(filepath.Join(dir, NoticesFile), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	notices, problems := loadNotices([]profile.Layer{{Name: "user", Dir: dir}}, fstest.MapFS{NoticesFile: {Data: []byte(bundled)}})
	if notices["answered"] != "look at {{.Gate}} on {{.Unit}}" || notices["record_owed"] != "owed {{.Turn}}" {
		t.Fatalf("notices %+v", notices)
	}
	got := problems.Error()
	for _, want := range []string{"record_owed", "Nope", "answred", `did you mean "answered"`} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %q lack %q", got, want)
		}
	}
}
