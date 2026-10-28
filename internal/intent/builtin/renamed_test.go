package builtin

import (
	"strings"
	"testing"
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
		if isOld(string(p)) {
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
	for _, old := range []string{"entry.state", "entry.units_ended", "entry.unit_started_by_me", "entry.unit_closure_posted", "entry.title", "slack.live_entry", "config.gtd.stale_waiting_days"} {
		if n := RenamedTo(old); !names[n] {
			t.Errorf("%s renames to %q, no fact", old, n)
		}
	}
	for _, p := range []string{"kind", "last_nudge"} {
		names[PropPrefix+p] = true
	}
	for _, old := range v6Facts {
		if !names[old] && !names[RenamedTo(old)] {
			t.Errorf("the version 6 fact %s is gone: add its final name to Renamed", old)
		}
	}
	if RenamedTo("task.state") != "" || RenamedTo("entryway") != "" {
		t.Error("a live name, or one the table does not hold, is renamed")
	}
}

// v6Facts are the facts of version 6, frozen: each is live, or renamed in
// one hop to a live name.
var v6Facts = []string{
	"brief.goal", "brief.kind", "brief.missing", "brief.risk", "brief.size", "brief.tags", "config.cleanup.asked_idle",
	"config.cleanup.idle", "config.cleanup.manual_idle", "config.cleanup.stale_workspace",
	"config.cleanup.workspace_grace", "config.drafts.repeat_after", "config.github.address_checks",
	"config.github.address_reviews", "config.github.address_without_workspace",
	"config.github.followups_without_workspace", "config.github.nudge.silent_days", "config.gtd.stale_waiting_days",
	"config.turns.per_day", "draft.delivery", "draft.effects", "draft.items_assigned", "draft.items_owed",
	"draft.maybe_posted", "draft.person_waited", "draft.proposer", "draft.provider", "draft.purpose", "draft.refusal",
	"draft.state", "draft.unit_open", "entry.captured_by_user", "entry.fingerprint", "entry.id", "entry.items_open",
	"entry.items_owed", "entry.items_resolved", "entry.kind", "entry.muted", "entry.nudge_due_at", "entry.resurface_at",
	"entry.settled_at", "entry.short_id", "entry.since_nudge", "entry.state", "entry.title", "entry.turns",
	"entry.unit", "entry.unit_closure_posted", "entry.unit_provider", "entry.unit_providers",
	"entry.unit_started_by_me", "entry.units", "entry.units_ended", "entry.waiting_days", "entry.waiting_on",
	"pr.address.checks", "pr.address.conflict", "pr.address.due", "pr.address.observed", "pr.address.reason",
	"pr.address.reviewers", "pr.adopted", "pr.age", "pr.association", "pr.author", "pr.base", "pr.checks",
	"pr.closed_at", "pr.conflicting_for", "pr.cross_repository", "pr.draft", "pr.ended", "pr.fingerprint",
	"pr.finished_for", "pr.first.due", "pr.first.reason", "pr.has_workspace", "pr.head", "pr.held", "pr.labels",
	"pr.last_interaction_at", "pr.last_review.decision", "pr.last_review.exists", "pr.last_review.verdict",
	"pr.merged_at", "pr.mine", "pr.mute.refusal", "pr.muted", "pr.nudge.refusal", "pr.nudge.who", "pr.nudge.why",
	"pr.open_turn", "pr.open_turn_seq", "pr.orphan_answer_id", "pr.owner", "pr.repo", "pr.repo_name",
	"pr.requested_teams", "pr.review_requested_me", "pr.review.action", "pr.review.observed", "pr.review.reason",
	"pr.silent_days", "pr.slug", "pr.stale", "pr.stalled", "pr.state", "pr.visibility", "pr.waits_on_user",
	"row.author_workspace", "row.capped", "row.intake", "row.intake_seq", "row.kind", "row.open", "row.pending",
	"row.question", "row.recommended", "row.remove_refusal", "row.review_workspace", "row.tracked", "row.trust_folder",
	"row.trust_homes", "row.yours", "session.agent_state", "session.closed_by_manager", "session.closes_when_done",
	"session.duplicate", "session.fingerprint", "session.idle_for", "session.idle_since", "session.listed",
	"session.managed", "session.manager", "session.newest_age", "session.newest_intent", "session.newest_state",
	"session.open_turn", "session.orphaned", "session.pr_state", "session.process_gone", "session.prompt_knowable",
	"session.prompted_while_idle", "session.repo", "session.started", "session.state", "session.title",
	"session.transcript_gone", "session.unread_answers", "session.unread_for", "session.unread_since",
	"session.user_active", "session.user_typed_after_turn", "session.worktree_gone", "slack.channel",
	"slack.closure_posted", "slack.conversation", "slack.ended", "slack.fingerprint", "slack.items_open",
	"slack.items_unowned", "slack.items_unowned_age_days", "slack.live_entry", "slack.settled_at",
	"slack.started_by_me", "trigger.event.data", "trigger.event.id", "trigger.event.key", "trigger.event.kind",
	"trigger.fired_at", "trigger.kind", "turn.automated", "turn.needs_take_over", "turn.owned", "turn.planned",
	"turn.resume", "turn.stage", "turn.try", "unit.label", "unit.ref", "workspace.dirty", "workspace.duplicate",
	"workspace.fingerprint", "workspace.last_activity", "workspace.main", "workspace.manager", "workspace.name",
	"workspace.newest_turn_state", "workspace.open_turn", "workspace.path_exists", "workspace.pendency", "workspace.pr",
	"workspace.pr_finished_for", "workspace.pr_state", "workspace.quiet_for", "workspace.repo", "workspace.role",
	"workspace.sessions", "workspace.temporary", "workspace.unpushed", "workspace.untracked",
}
