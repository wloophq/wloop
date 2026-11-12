package api

import (
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
	files, _ := filepath.Glob("../web/static/*.js")
	shown := false
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), ".draft.id") || strings.Contains(string(b), ".draft.rev") {
			shown = true
		}
	}
	if !shown {
		t.Error("no web client file renders an option's or a confirm's pinned draft (draft.id / draft.rev)")
	}
}
