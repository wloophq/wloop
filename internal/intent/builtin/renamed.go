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
	{"entry.waiting_on", "task.owed_by"},
	{"entry.items_owed", "task.owed_by"},
	{"entry.kind", "task.prop.kind"},
	{"entry.resurface_at", "task.check_on"},
	{"entry.waiting_days", "task.owed_since"},
	{"entry.since_nudge", "task.prop.last_nudge"},
	{"entry.nudge_due_at", "task.prop.last_nudge"},
	{"entry.", "task."},
	{"slack.live_entry", "slack.live_task"},
	{"config.gtd.", "config.tasks."},
	{"gtd.", "tasks."},
	{"gtd", "tasks"},
	{"entry", "task"},
	{"entry-results-ended", "task-results-ended"},
	{"guidance.handed", "turn.guidance_handed"},
	{"pr.guidance_handed", "turn.guidance_handed"},
	{"pr.round_sent", "turn.round_sent"},
	{"config.github.", "config.plugins.github."},
	{"draft.approve", "gates.draft.intents.approve"},
	{"draft.discard", "gates.draft.intents.discard"},
	{"run_now", "gates.plan.intents.run_now"},
	{"guide", "gates.plan.intents.guide"},
	{"skip", "gates.plan.intents.skip"},
	{"trust_run", "gates.trust.intents.trust_run"},
	{"unblock", "gates.trust.intents.unblock"},
	{"answer", "gates.question.intents.answer"},
	{"continue", "gates.question.intents.continue"},
	{"dismiss", "gates.question.intents.dismiss"},
	{"session.unread_answers", "session.unread_gates"},
	{"inbox_post", "task_post"},
	{"inbox_read", "task_read"},
}

// RenamedKeys are the configuration keys a release renamed or moved, each
// with the key that replaced it, the most specific first. An Old ending in
// "." renames every key it prefixes.
var RenamedKeys = []Rename{
	{"workflows.*.approval", "workflows.*.gates.approval"},
	{"workflows.*.approval.", "workflows.*.gates.approval."},
	{"workflows.*.drafts.approval", "workflows.*.gates.approval"},
	{"workflows.*.drafts.approval.", "workflows.*.gates.approval."},
	{"plugins.*.drafts.approval", "plugins.*.gates.approval"},
	{"plugins.*.drafts.approval.", "plugins.*.gates.approval."},
	{"github.drafts.approval", "plugins.github.gates.approval"},
	{"github.drafts.approval.", "plugins.github.gates.approval."},
	{"slack.drafts.approval", "plugins.slack.gates.approval"},
	{"slack.drafts.approval.", "plugins.slack.gates.approval."},
	{"github", "plugins.github"},
	{"github.", "plugins.github."},
	{"slack", "plugins.slack"},
	{"slack.", "plugins.slack."},
}

// ManifestRenamedKeys are the keys of an intent's or a workflow's manifest
// a release renamed, as RenamedKeys.
var ManifestRenamedKeys = []Rename{
	{"drafts.approval", "gates.approval"},
	{"drafts.approval.", "gates.approval."},
	{"approval", "gates.approval"},
	{"approval.", "gates.approval."},
}

// KeyRenamedTo is the configuration key that replaced old, "" when old was
// never renamed.
func KeyRenamedTo(old string) string { return renamedIn(RenamedKeys, old) }

// ManifestKeyRenamedTo is the manifest key that replaced old, "" when old
// was never renamed.
func ManifestKeyRenamedTo(old string) string { return renamedIn(ManifestRenamedKeys, old) }

// RenamedFiles are the profile files a release renamed or split, and where
// what each held is read now.
var RenamedFiles = map[string]string{
	"gtd.toml": "its verbs are read from tasks.toml, its views and review from views.toml",
}

// RenamedTo is the name that replaced old, "" when old was never renamed.
func RenamedTo(old string) string { return renamedIn(Renamed, old) }

func renamedIn(table []Rename, old string) string {
	for _, r := range table {
		if n, ok := renamedBy(r, old); ok {
			return n
		}
	}
	return ""
}

// renamedBy is old renamed by r: a * segment of r.Old matches any one
// segment, which the * of r.New takes.
func renamedBy(r Rename, old string) (string, bool) {
	pat, segs := strings.Split(strings.TrimSuffix(r.Old, "."), "."), strings.Split(old, ".")
	prefix := strings.HasSuffix(r.Old, ".")
	if len(segs) < len(pat) || !prefix && len(segs) != len(pat) || prefix && len(segs) == len(pat) {
		return "", false
	}
	var wild []string
	for i, p := range pat {
		switch {
		case p == "*":
			wild = append(wild, segs[i])
		case p != segs[i]:
			return "", false
		}
	}
	n := r.New
	for _, w := range wild {
		n = strings.Replace(n, "*", w, 1)
	}
	if prefix {
		n += strings.Join(segs[len(pat):], ".")
	}
	return n, true
}

func init() { expr.Renamed = RenamedTo }
