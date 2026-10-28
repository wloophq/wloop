package builtin

import (
	"strings"

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
	{"slack.live_entry", "slack.live_task"},
	{"config.gtd.", "config.tasks."},
	{"gtd.", "tasks."},
	{"gtd", "tasks"},
	{"entry", "task"},
	{"entry-results-ended", "task-results-ended"},
	{"guidance.handed", "pr.guidance_handed"},
	{"config.github.", "config.plugins.github."},
}

// RenamedKeys are the configuration keys a release renamed or moved, each
// with the key that replaced it, the most specific first. An Old ending in
// "." renames every key it prefixes.
var RenamedKeys = []Rename{
	{"github.drafts.approval", "plugins.github.gates.approval"},
	{"github.drafts.approval.", "plugins.github.gates.approval."},
	{"slack.drafts.approval", "plugins.slack.gates.approval"},
	{"slack.drafts.approval.", "plugins.slack.gates.approval."},
	{"github", "plugins.github"},
	{"github.", "plugins.github."},
	{"slack", "plugins.slack"},
	{"slack.", "plugins.slack."},
}

// KeyRenamedTo is the configuration key that replaced old, "" when old was
// never renamed.
func KeyRenamedTo(old string) string { return renamedIn(RenamedKeys, old) }

// RenamedFiles are the profile files a release renamed or split, and where
// what each held is read now.
var RenamedFiles = map[string]string{
	"gtd.toml": "its verbs are read from tasks.toml, its views and review from views.toml",
}

// RenamedTo is the name that replaced old, "" when old was never renamed.
func RenamedTo(old string) string { return renamedIn(Renamed, old) }

func renamedIn(table []Rename, old string) string {
	for _, r := range table {
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
