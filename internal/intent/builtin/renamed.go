package builtin

import (
	"strings"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/intent/expr"
)

// Rename is a name the profile language no longer has and the name that
// replaced it. An Old ending in "." renames every name it prefixes.
type Rename struct {
	Old, New string
}

// Renamed are the names a release renamed. A user profile, manifest or
// config.toml that still writes an old one fails to load, its problem
// naming the new one: no old name is accepted beside its new one.
var Renamed = []Rename{
	{"entry.units_ended", "task.link_ended"},
	{"entry.unit_started_by_me", "task.link_started_by_me"},
	{"entry.unit_closure_posted", "task.link_closure_posted"},
	{"entry.", "task."},
	{string(dslack.Provider) + ".live_entry", string(dslack.Provider) + ".live_task"},
	{"config.gtd.", "config.tasks."},
	{"gtd.", "tasks."},
	{"gtd", "tasks"},
	{"entry", "task"},
	{"entry-results-ended", "task-results-ended"},
}

// RenamedFiles are the profile files a release renamed or split, and where
// what each held is read now.
var RenamedFiles = map[string]string{
	"gtd.toml": "its verbs are read from tasks.toml, its views and review from views.toml",
}

// RenamedTo is the name that replaced old, "" when old was never renamed.
func RenamedTo(old string) string {
	for _, r := range Renamed {
		if r.Old == old {
			return r.New
		}
		if strings.HasSuffix(r.Old, ".") && strings.HasPrefix(old, r.Old) {
			return r.New + strings.TrimPrefix(old, r.Old)
		}
	}
	return ""
}

func init() { expr.Renamed = RenamedTo }
