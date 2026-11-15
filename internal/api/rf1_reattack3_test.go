package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// What the person answers a question through: the view must carry the confirm's pin, and the web client
// must render each option's and the confirm's pinned draft, or the person approves a pin they never saw.
func TestReattackThePersonSeesThePin(t *testing.T) {
	if _, ok := reflect.TypeFor[QuestionView]().FieldByName("Draft"); !ok {
		t.Error("QuestionView has no confirm pin: a yes approves a draft the view never names")
	}
	shown := false
	_ = filepath.WalkDir("../web/ui/src", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".test.ts") || !(strings.HasSuffix(p, ".svelte") || strings.HasSuffix(p, ".ts")) {
			return err
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), ".draft.id") || strings.Contains(string(b), ".draft.rev") {
			shown = true
		}
		return nil
	})
	if !shown {
		t.Error("no web client file renders an option's or a confirm's pinned draft (draft.id / draft.rev)")
	}
}
