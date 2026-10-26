package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/facet"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/intent/expr"
)

var (
	both = []FactSlot{ActionSlot, TurnSlot}
	act  = []FactSlot{ActionSlot}
	trn  = []FactSlot{TurnSlot}
)

func fact(name string, t expr.Type, slots []FactSlot, doc string) Fact {
	return Fact{Name: name, Type: t, Slots: slots, Doc: doc}
}

// prFact is a fact of github's pull request, which only github intents read.
func prFact(name string, t expr.Type, slots []FactSlot, doc string) Fact {
	f := fact(name, t, slots, doc)
	f.Provider = unit.GitHub
	return f
}

// dim flags f as a profile dimension.
func dim(f Fact) Fact {
	f.Dim = true
	return f
}

// enum declares the values f takes, by provider.
func enum(f Fact, values map[unit.ProviderID][]string) Fact {
	f.Enum = values
	return f
}

// anyProvider keys an enum that holds for every provider.
const anyProvider unit.ProviderID = ""

// Associations are GitHub's author associations.
var Associations = []string{"OWNER", "MEMBER", "COLLABORATOR", "CONTRIBUTOR", "FIRST_TIMER", "FIRST_TIME_CONTRIBUTOR", "MANNEQUIN", "NONE"}

// wf is a workflow fact of slot, named <slot>.<name>.
func wf(slot, name string, t expr.Type, doc string) Fact {
	return Fact{Name: slot + "." + name, Type: t, Slots: []FactSlot{WorkflowSlot}, Slot: slot, Doc: doc}
}

// providerWF is a workflow fact of slot that the plugin of p computes.
func providerWF(p unit.ProviderID, slot, name string, t expr.Type, doc string) Fact {
	f := wf(slot, name, t, doc)
	f.Provider = p
	return f
}

// BriefSubjects are the workflow slots whose units may have a brief, which
// read the brief slot's facts.
var BriefSubjects = []string{"pr", "slack", "entry"}

// briefWF is a fact of the unit's brief, read by every slot of BriefSubjects.
func briefWF(name string, t expr.Type, doc string) Fact {
	f := wf("brief", name, t, doc)
	f.Subjects = BriefSubjects
	return f
}

// Facts are the fields predicates read, by slot; inputs.<name> are added per
// intent by Env. A workflow fact may share a name with an intent fact.
var Facts = []Fact{
	prFact("pr.mine", expr.Bool, both, "The pull request's author is the viewer."),
	dim(prFact("pr.author", expr.String, both, "The pull request's author login.")),
	prFact("pr.state", expr.String, both, "The pull request's state on GitHub."),
	prFact("pr.slug", expr.String, both, "owner/repo#number."),
	dim(prFact("pr.owner", expr.String, both, "The repository owner.")),
	dim(prFact("pr.repo", expr.String, both, "The repository name.")),
	dim(prFact("pr.base", expr.String, both, "The base branch.")),
	dim(enum(prFact("pr.association", expr.String, trn, "The author's association with the repository, from the Basics read."), map[unit.ProviderID][]string{anyProvider: Associations})),
	prFact("pr.cross_repository", expr.Bool, both, "The head branch lives in a fork."),
	prFact("pr.visibility", expr.String, trn, "The repository's visibility: public, private or internal, from the Basics read."),
	prFact("pr.draft", expr.Bool, act, "The pull request is a draft, from the inventory row."),
	prFact("pr.review_requested_me", expr.Bool, act, "A review is requested from the viewer, from the inventory row."),
	dim(prFact("pr.requested_teams", expr.List, trn, "The teams a review of the pull request was requested through, as <org>-<slug>.")),
	dim(prFact("pr.labels", expr.List, trn, "The pull request's labels, from the Basics read.")),
	dim(prFact("pr.address.reviewers", expr.List, trn, "The reviewers an address round answers; empty on any other turn.")),
	prFact("pr.address.checks", expr.List, trn, "The failing checks an address round fixes; empty on any other turn."),
	prFact("pr.address.conflict", expr.Bool, trn, "The head conflicts with the base and the address round resolves it; false on any other turn."),
	fact("row.kind", expr.String, act, "The row's situation kind."),
	fact("row.open", expr.Bool, act, "The pull request is open: kind is not done, or, untracked, its GitHub state is open."),
	fact("row.tracked", expr.Bool, act, "The pull request is in the inventory."),
	fact("row.adopted", expr.Bool, act, "The pull request was taken over into the loop."),
	fact("row.held", expr.Bool, act, "Rounds on the pull request are held."),
	fact("row.muted", expr.Bool, act, "The pull request is muted, by the server clock."),
	fact("row.question", expr.Bool, act, "A turn waits on the user's answer."),
	fact("row.pending", expr.Bool, act, "A round is pending."),
	fact("row.intake", expr.Bool, act, "An intake is open on the pull request."),
	fact("row.author_workspace", expr.Bool, act, "A linked workspace holds the author role."),
	fact("row.yours", expr.Bool, act, "The row's unit is the viewer's to act on: any unit but a pull request, or a pull request that is the viewer's, taken over or held by an author workspace."),
	fact("row.review_workspace", expr.Bool, act, "A linked workspace holds the review role."),
	fact("row.nudge.who", expr.List, act, "Whom a nudge would address, from nudge.targets."),
	fact("row.nudge.why", expr.String, act, "Why the pull request waits on them, from nudge.targets."),
	fact("row.nudge.refusal", expr.String, act, "The text nudge.prepare would refuse with now; empty when allowed."),
	fact("turn.trigger", expr.String, trn, "by-hand or automated."),
	fact("turn.automated", expr.Bool, trn, "The sweep started the turn."),
	fact("turn.owned", expr.Bool, trn, "The viewer owns the pull request's loop."),
	fact("turn.needs_take_over", expr.Bool, trn, "owner_only, not owned, and no input with before = \"take_over\" set."),
	fact("turn.resume", expr.Bool, trn, "The turn resumes an earlier one."),
	fact("turn.plain", expr.Bool, trn, "The turn is a dry run (profile try)."),
	fact("turn.planned", expr.String, trn, "The round the request planned."),
	dim(enum(fact("turn.round", expr.String, trn, "first or follow-up; only intents with dims = [\"round\"] may read it."), map[unit.ProviderID][]string{anyProvider: {"first", "follow-up"}})),
	fact("last_review.exists", expr.Bool, trn, "The viewer reviewed the pull request before."),
	fact("last_review.verdict", expr.String, trn, "The viewer's newest review state, any state."),
	fact("last_review.decision", expr.String, trn, "The viewer's newest non-comment verdict."),
	fact("config.address_reviews", expr.Bool, both, "The address_reviews setting for the pull request."),
	fact("config.address_checks", expr.Bool, both, "The address_checks setting for the pull request."),
	fact("config.followups_without_workspace", expr.Bool, both, "The followups_without_workspace setting for the pull request."),
	fact("config.address_without_workspace", expr.Bool, both, "The address_without_workspace setting for the pull request."),
	wf("pr", "state", expr.String, "The pull request's state on GitHub: open, closed or merged."),
	wf("pr", "merged_at", expr.Time, "When the pull request merged; zero when it did not."),
	wf("pr", "closed_at", expr.Time, "When the pull request closed; zero when open."),
	wf("pr", "finished_for", expr.Duration, "Working time since it ended, max(merged_at, closed_at), the rule retention uses; zero while open."),
	wf("pr", "draft", expr.Bool, "The pull request is a draft."),
	wf("pr", "head", expr.String, "The head commit's oid."),
	wf("pr", "conflicting_for", expr.Duration, "How long the pull request has conflicted with its base, from when the inventory first saw it; zero when it does not."),
	wf("pr", "mine", expr.Bool, "The pull request's author is the viewer."),
	wf("pr", "checks", expr.String, "The checks' rollup on the head."),
	wf("pr", "stalled", expr.Bool, "The checks stalled on the head."),
	wf("pr", "age", expr.Duration, "Time since the pull request opened."),
	wf("pr", "held", expr.Bool, "Rounds on the pull request are held."),
	wf("pr", "adopted", expr.Bool, "The pull request was taken over into the loop."),
	wf("pr", "muted", expr.Bool, "The pull request is muted."),
	wf("pr", "has_workspace", expr.Bool, "A linked workspace holds the pull request."),
	wf("pr", "stale", expr.Bool, "The stored detail is older than sweep.slow + 2 x sweep.fast."),
	wf("pr", "open_turn", expr.String, "The state of the book's open turn; empty when none."),
	wf("pr", "open_turn_seq", expr.Int, "The sequence of the book's open turn; 0 when none."),
	wf("pr", "orphan_answer_id", expr.Int, "The id of an answer whose turn ended; 0 when none."),
	wf("pr", "review.action", expr.String, "What the review rules decide: empty, follow-up or complete."),
	wf("pr", "review.reason", expr.String, "Why the review rules decide so."),
	wf("pr", "review.observed", expr.Bool, "The review rules observe without acting."),
	wf("pr", "first.due", expr.Bool, "A first round is due."),
	wf("pr", "first.reason", expr.String, "Why a first round is due or not."),
	wf("pr", "address.due", expr.Bool, "An address round is due."),
	wf("pr", "address.reason", expr.String, "Why an address round is due or not."),
	wf("pr", "address.reviewers", expr.List, "The reviewers whose reviews the address round would answer."),
	wf("pr", "address.checks", expr.List, "The failing checks the address round would fix."),
	wf("pr", "address.conflict", expr.Bool, "The head has conflicted with the base past the conflict window, and the address round would resolve it."),
	wf("pr", "address.observed", expr.Bool, "The address rules observe without acting."),
	wf("pr", "last_interaction_at", expr.Time, "The newest comment, review, push or review request, by anyone."),
	wf("pr", "silent_days", expr.Int, "Calendar days since last_interaction_at."),
	wf("pr", "waits_on_user", expr.Bool, "The row waits on the user, per situation.Derive."),
	wf("pr", "nudge_refusal", expr.String, "The text nudge.prepare would refuse with today; empty when a nudge may go."),
	wf("pr", "fingerprint", expr.String, "What the sweep records as handled until something new happens: recorded heads, review and check fingerprints, skips."),
	wf("pr", "repo", expr.String, "owner/repo."),
	wf("pr", "owner", expr.String, "The repository owner."),
	wf("pr", "author", expr.String, "The pull request's author login; empty when not stored."),
	wf("pr", "visibility", expr.String, "The repository's visibility: public, private or internal; empty until the pull request is read in full."),
	wf("session", "state", expr.String, "active (it has a live process), dormant (no process; its manager can wake it) or closed."),
	wf("session", "manager", expr.String, "The workspace manager that owns the session, such as orca or claude-desktop."),
	wf("session", "managed", expr.Bool, "The session was started by the loop (sessions.managed)."),
	wf("session", "listed", expr.Bool, "The session's manager lists its process as usable."),
	wf("session", "orphaned", expr.Bool, "The session's manager lists its process as not usable."),
	wf("session", "agent_state", expr.String, "The agent's state as the manager reports it."),
	wf("session", "user_active", expr.Bool, "The user acts in the session now."),
	wf("session", "user_typed_after_turn", expr.Bool, "The user typed in the session after the loop's last turn."),
	wf("session", "prompt_knowable", expr.Bool, "Whether the user typed can be known, from the hook or the transcript."),
	wf("session", "prompted_while_idle", expr.Bool, "A manual session got a prompt during its idle period."),
	wf("session", "open_turn", expr.String, "The state of the session's open turn; empty when none."),
	wf("session", "newest_state", expr.String, "The state of the session's newest turn."),
	wf("session", "newest_age", expr.Duration, "Time since the newest turn was sent or started."),
	wf("session", "newest_intent", expr.String, "The intent of the newest turn."),
	wf("session", "closes_when_done", expr.Bool, "The newest turn's intent closes its session when done."),
	wf("session", "idle_since", expr.Time, "When the current idle period started."),
	wf("session", "idle_for", expr.Duration, "Time since idle_since."),
	wf("session", "pr_state", expr.String, "The state of the book's pull request."),
	wf("session", "duplicate", expr.Bool, "Another listed session resumes the same agent session; the focused one, one the user prompted, and else the oldest are never duplicates."),
	wf("session", "worktree_gone", expr.Bool, "The session's worktree path no longer exists."),
	wf("session", "closed_by_manager", expr.Bool, "The dormant session's manager keeps its own record of it, and the user closed it there, such as archiving it in Claude Desktop."),
	wf("session", "transcript_gone", expr.Bool, "The agent's transcript of the session, as its hooks reported it, no longer exists."),
	wf("session", "process_gone", expr.Bool, "The active session's process was missed by two complete censuses of its manager."),
	wf("session", "fingerprint", expr.String, "The session id, its state, the start of the idle period and the newest turn's sequence."),
	wf("session", "repo", expr.String, "owner/repo of the book's pull request; empty when none."),
	wf("workspace", "role", expr.String, "author (a taken-over workspace included), review or none."),
	wf("workspace", "main", expr.Bool, "The workspace is the repository's main checkout."),
	wf("workspace", "pr", expr.String, "The linked pull request, owner/repo#number; empty when none."),
	wf("workspace", "pr_state", expr.String, "The linked pull request's state."),
	wf("workspace", "pr_finished_for", expr.Duration, "The linked pull request's pr.finished_for."),
	wf("workspace", "path_exists", expr.Bool, "The worktree path exists."),
	wf("workspace", "temporary", expr.Bool, "The worktree lives under a temporary path."),
	wf("workspace", "duplicate", expr.Bool, "Neither a main checkout nor the workspace the manager links to its pull request, while another non-main workspace is."),
	wf("workspace", "dirty", expr.Int, "Modified tracked files."),
	wf("workspace", "untracked", expr.Int, "Untracked files outside cleanup.disposable."),
	wf("workspace", "unpushed", expr.Int, "Commits on no remote."),
	wf("workspace", "newest_turn_state", expr.String, "The state of the newest turn of the linked pull request's book; empty when none."),
	wf("workspace", "open_turn", expr.Bool, "The linked pull request's book has an open turn."),
	wf("workspace", "pendency", expr.List, "What holds work: modified files, untracked files, commits on no remote, turn asked unanswered, turn failed, turn unrecorded, turn open."),
	wf("workspace", "sessions", expr.Int, "Agent sessions in the workspace that are active, or dormant and active within cleanup.idle."),
	wf("workspace", "manager", expr.String, "The workspace manager that owns the worktree."),
	wf("workspace", "last_activity", expr.Time, "The newest activity in the workspace."),
	wf("workspace", "quiet_for", expr.Duration, "Time since last_activity."),
	wf("workspace", "fingerprint", expr.String, "The worktree path and its head."),
	wf("workspace", "repo", expr.String, "owner/repo; empty when unknown."),
	wf("entry", "id", expr.String, "The entry's id."),
	wf("entry", "kind", expr.String, "The entry's kind, such as action, waiting, close, message, capture or project."),
	wf("entry", "state", expr.String, "The entry's state: inbox, next, waiting, scheduled, someday, reference, done or trash."),
	wf("entry", "waiting_on", expr.List, "The person keys the entry waits on, such as slack:U0123."),
	wf("entry", "resurface_at", expr.Time, "When the entry resurfaces; zero when it has no date."),
	wf("entry", "waiting_days", expr.Int, "Working days the entry has waited, in the configured location, as the GTD rules count them."),
	wf("entry", "since_nudge", expr.Duration, "Time since the entry's last nudge; very large when it was never nudged."),
	wf("entry", "nudge_due_at", expr.Time, "When a stalled nudge is due: the resurface date when set, else gtd.stale_waiting_days working days after the entry started waiting, then proposals.renudge_after after the last nudge; zero when the entry is not stalled."),
	wf("entry", "muted", expr.Bool, "The entry is muted."),
	wf("entry", "unit", expr.String, "The entry's unit as provider:key; empty for an entry with none."),
	wf("entry", "unit_provider", expr.String, "The provider of the entry's unit; empty for an entry with none."),
	wf("entry", "unit_started_by_me", expr.Bool, "The viewer started the entry's unit, from the facts GTD receives."),
	wf("entry", "unit_closure_posted", expr.Bool, "A closure was posted on the entry's unit, from the facts GTD receives."),
	wf("entry", "items_open", expr.Int, "Open items of the entry whose unit is the entry's unit."),
	wf("entry", "items_resolved", expr.Int, "Resolved items of the entry whose unit is the entry's unit."),
	wf("entry", "items_held", expr.Int, "Items of the entry's unit the waited people hold: open, assigned to or waited on from them."),
	wf("entry", "settled_at", expr.Time, "The newest update of a settled draft the workflow proposed on the entry, withdrawn ones included; zero when none."),
	wf("entry", "fingerprint", expr.String, "The entry's id, state and update time."),
	providerWF(dslack.Provider, "slack", "channel", expr.String, "The conversation's channel id."),
	providerWF(dslack.Provider, "slack", "conversation", expr.String, "The conversation's kind: channel, dm or group."),
	providerWF(dslack.Provider, "slack", "started_by_me", expr.Bool, "The viewer started the thread."),
	providerWF(dslack.Provider, "slack", "closure_posted", expr.Bool, "A closure was posted on the unit, by the extraction or a sent draft's unit.closed."),
	providerWF(dslack.Provider, "slack", "live_entry", expr.Bool, "An entry of the unit is open: neither done nor in the trash."),
	providerWF(dslack.Provider, "slack", "items_open", expr.Int, "Open items whose unit is this unit."),
	providerWF(dslack.Provider, "slack", "items_unowned", expr.Int, "Open items of the unit with no assignee, nobody waited on and no basis."),
	providerWF(dslack.Provider, "slack", "items_unowned_age_days", expr.Int, "Working days, in the configured location, since the oldest unowned item appeared."),
	providerWF(dslack.Provider, "slack", "settled_at", expr.Time, "The newest update of a settled draft the workflow proposed on the unit, withdrawn ones included; zero when none."),
	providerWF(dslack.Provider, "slack", "fingerprint", expr.String, "The unit, its newest message and the update time of its items."),
	briefWF("goal", expr.String, "The brief's goal; empty when the unit has no brief."),
	dim(enum(briefWF("risk", expr.String, "The brief's risk: low, medium or high."), map[unit.ProviderID][]string{anyProvider: facet.Risks})),
	dim(enum(briefWF("kind", expr.String, "The brief's kind, from the provider's taxonomy."), map[unit.ProviderID][]string{unit.GitHub: facet.Kinds})),
	dim(enum(briefWF("size", expr.String, "The brief's size; GitHub briefs only."), map[unit.ProviderID][]string{anyProvider: facet.Sizes})),
	briefWF("tags", expr.List, "The brief's normalized key:value tags."),
	briefWF("missing", expr.List, "The brief intent's extra fields the unit's brief lacks: new or changed since it was written."),
	wf("draft", "person_waited", expr.Bool, "The draft's person is still waited on by its entry; true for a draft of no person."),
	wf("draft", "items_held", expr.Int, "Items of the draft still open and held by its person, or unowned for a draft of no person."),
	wf("draft", "items_owned", expr.Int, "Items of the draft that gained an owner since it was proposed."),
	wf("draft", "subject_open", expr.Bool, "The draft's subject still exists: its entry is neither done nor in the trash, or its unit is stored."),
	wf("trigger", "kind", expr.String, "sweep, event or schedule."),
	wf("trigger", "event.kind", expr.String, "The event's kind; empty unless kind is event."),
	wf("trigger", "event.key", expr.String, "The event's key."),
	wf("trigger", "event.data", expr.String, "The event's data."),
	wf("trigger", "fired_at", expr.Time, "When the schedule fired; zero unless kind is schedule."),
	wf("config", "cleanup.idle", expr.Duration, "The cleanup.idle setting."),
	wf("config", "cleanup.asked_idle", expr.Duration, "The cleanup.asked_idle setting."),
	wf("config", "cleanup.workspace_grace", expr.Duration, "The cleanup.workspace_grace setting, in working time."),
	wf("config", "cleanup.manual_idle", expr.Duration, "The cleanup.manual_idle setting."),
	wf("config", "cleanup.stale_workspace", expr.Duration, "The cleanup.stale_workspace setting."),
	wf("config", "nudge.silent_days", expr.Int, "The nudge.silent_days setting, in calendar days."),
	wf("config", "gtd.stale_waiting_days", expr.Int, "The gtd.stale_waiting_days setting, in working days."),
	wf("config", "proposals.renudge_after", expr.Duration, "The proposals.renudge_after setting."),
	wf("config", "turns.automated_per_day", expr.Int, "The turns.automated_per_day setting: automated turns on units other than pull requests, all workflows together."),
}

// WorkflowSlots are the slots of workflow facts: each provider's, then the
// brief's, the draft's, the trigger's and the config's.
var WorkflowSlots = []string{"pr", "session", "workspace", "entry", "slack", "brief", "draft", "trigger", "config"}

// DraftSlot is the slot of the facts a workflow's [withdraw] when reads of
// each draft it proposed.
const DraftSlot = "draft"

// WorkflowEnv is the env of a workflow's when: the facts of slot, the
// brief's when slot is one of BriefSubjects, trigger.* and config.*.
func WorkflowEnv(slot string) expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if slices.Contains(f.Slots, WorkflowSlot) && (f.Slot == slot || slices.Contains(f.Subjects, slot) || f.Slot == "trigger" || f.Slot == "config") {
			env[f.Name] = f.Type
		}
	}
	return env
}

// WithdrawEnv is the env of a workflow's [withdraw] when: WorkflowEnv(slot)
// and the draft slot's facts.
func WithdrawEnv(slot string) expr.Env {
	env := WorkflowEnv(slot)
	for _, f := range Facts {
		if f.Slot == DraftSlot {
			env[f.Name] = f.Type
		}
	}
	return env
}

// ExtraFacts are the brief.extra.<name> facts the brief intent's extra
// schema declares, typed from it: a string, a string with an enum, an
// integer, a boolean or an array of strings with an enum. A predicate's env
// is extended with them at load.
func ExtraFacts(extra *jsonschema.Schema) expr.Env {
	if extra == nil {
		return expr.Env{}
	}
	return expr.Env{}
}

// PREnv is the pull request's own facts of the turn slot, the ones a turn
// reads before it is built.
func PREnv() expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if f.Provider == unit.GitHub && slices.Contains(f.Slots, TurnSlot) {
			env[f.Name] = f.Type
		}
	}
	return env
}

// WorkflowEnvAll is WorkflowEnv over every slot, for a predicate whose
// workflow is not known yet.
func WorkflowEnvAll() expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if slices.Contains(f.Slots, WorkflowSlot) {
			env[f.Name] = f.Type
		}
	}
	return env
}

// FactType is the type of the workflow fact name.
func FactType(name string) (expr.Type, bool) {
	for _, f := range Facts {
		if f.Name == name && slices.Contains(f.Slots, WorkflowSlot) {
			return f.Type, true
		}
	}
	return 0, false
}

// FactProvider is the provider whose units an intent fact describes; "" for
// a fact of every provider, or a name that is no intent fact.
func FactProvider(name string) unit.ProviderID {
	for _, f := range Facts {
		if f.Name == name && !slices.Contains(f.Slots, WorkflowSlot) {
			return f.Provider
		}
	}
	return ""
}
