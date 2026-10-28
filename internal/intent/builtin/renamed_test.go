package builtin

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// isOld says whether name is a name the rename table renames.
func isOld(name string) bool {
	for _, r := range Renamed {
		if r.Old == name || strings.HasSuffix(r.Old, ".") && strings.HasPrefix(name, r.Old) {
			return true
		}
	}
	return false
}

func TestNoRenamedNameIsLive(t *testing.T) {
	t.Parallel()
	for _, f := range Facts {
		if isOld(f.Name) {
			t.Errorf("fact %s was renamed %s and is a fact again", f.Name, RenamedTo(f.Name))
		}
	}
	for _, s := range Steps {
		if isOld(s.Name) {
			t.Errorf("step %s was renamed and is a step again", s.Name)
		}
	}
	for _, e := range Effects {
		if isOld(e.Name) {
			t.Errorf("effect %s was renamed and is an effect again", e.Name)
		}
	}
	for _, k := range Grammar {
		if k.In == SurfaceConfig && isOld(k.Path) {
			t.Errorf("config key %s was renamed and is a key again", k.Path)
		}
	}
	for _, s := range WorkflowSlots {
		if isOld(s) {
			t.Errorf("slot %s was renamed and is a slot again", s)
		}
	}
	for p := range Slots {
		if p != unit.Task && isOld(string(p)) {
			t.Errorf("provider %s was renamed and is a provider again", p)
		}
	}
}

func TestARenamedFactNamesALiveOne(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, f := range Facts {
		names[f.Name] = true
	}
	for _, old := range []string{"entry.state", "entry.units_ended", "entry.title", "slack.live_entry", "config.gtd.stale_waiting_days"} {
		if n := RenamedTo(old); !names[n] {
			t.Errorf("%s renames to %q, no fact", old, n)
		}
	}
	if RenamedTo("task.state") != "" || RenamedTo("entryway") != "" {
		t.Error("a live name, or one the table does not hold, is renamed")
	}
}
