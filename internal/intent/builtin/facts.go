package builtin

import (
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/facet"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/intent/expr"
)

var (
	both = []FactSlot{ActionSlot, TurnSlot}
	act  = []FactSlot{ActionSlot}
	trn  = []FactSlot{TurnSlot}
	nam  = []FactSlot{NameSlot}
)

func categoryNames() []string {
	out := make([]string, len(task.Categories))
	for i, c := range task.Categories {
		out[i] = string(c)
	}
	return out
}

func fact(name string, t expr.Type, slots []FactSlot, doc string) Fact {
	return Fact{Name: name, Type: t, Slots: slots, Doc: doc}
}

// taskName is a name fact of a task, which only task intents read.
func taskName(name, doc string) Fact {
	f := fact(name, expr.String, nam, doc)
	f.Provider = unit.Task
	return f
}

// dim flags f as a profile dimension.
func dim(f Fact) Fact {
	f.Dim = true
	return f
}

// untrusted flags f as a fact no rule of a draft gate, nor a rule that
// releases a gate, reads.
func untrusted(f Fact) Fact {
	f.Untrusted = true
	return f
}

// timeVarying marks f as changing with time alone.
func timeVarying(f Fact) Fact {
	f.TimeVarying = true
	return f
}

// enum declares the values f takes, by provider.
func enum(f Fact, values map[unit.ProviderID][]string) Fact {
	f.Enum = values
	return f
}

// anyProvider keys an enum that holds for every provider.
const anyProvider unit.ProviderID = ""

// enumOf is an enum of values that holds for every provider.
func enumOf(values ...string) map[unit.ProviderID][]string {
	return map[unit.ProviderID][]string{anyProvider: values}
}

// PRStates are a pull request's states as GitHub spells them.
var PRStates = []string{"OPEN", "CLOSED", "MERGED"}

// TurnStates are the states a turn is in, and the empty value of none.
var TurnStates = []string{"", string(dturn.Sent), string(dturn.Started), string(dturn.Asked), string(dturn.Done), string(dturn.Settled),
	string(dturn.Failed), string(dturn.Unrecorded), string(dturn.Dismissed), string(dturn.Replaced), string(dturn.Withdrawn)}

// wf is a workflow fact of slot, named <slot>.<name>.
func wf(slot, name string, t expr.Type, doc string) Fact {
	return Fact{Name: slot + "." + name, Type: t, Slots: []FactSlot{WorkflowSlot}, Slot: slot, Doc: doc}
}

// BriefSubjects are the workflow slots whose units may have a brief, which
// read the brief slot's facts: the task's, and those of the plugins whose
// manifest says brief_subject.
var BriefSubjects = []string{"task"}

// BriefSlot is the slot of the facts of a unit's brief.
const BriefSlot = "brief"

// briefWF is a fact of the unit's brief, read by every slot of BriefSubjects;
// an agent writes the brief, so it is untrusted.
func briefWF(name string, t expr.Type, doc string) Fact {
	f := wf(BriefSlot, name, t, doc)
	f.Subjects = BriefSubjects
	f.Untrusted = true
	return f
}

// Facts are the fields predicates read, by slot; inputs.<name> are added per
// intent by Env. A workflow fact may share a name with an intent fact.
var Facts = slices.Concat(ActionFacts, otherFacts, proposalFacts)

// otherFacts are the turn slot's own facts, the config facts of the intent
// slots, and every workflow and approval fact.
var otherFacts = []Fact{
	fact("turn.automated", expr.Bool, trn, "The sweep started the turn."),
	fact("turn.owned", expr.Bool, trn, "The viewer owns the pull request's loop."),
	fact("turn.needs_take_over", expr.Bool, trn, "owner_only, not owned, and no input with before = \"take_over\" set."),
	fact("turn.resume", expr.Bool, trn, "The turn resumes an earlier one."),
	fact("turn.try", expr.Bool, trn, "The turn is a dry run (profile try)."),
	enum(fact("turn.planned", expr.String, trn, "The round the request planned: first, follow-up, or empty for none."), enumOf("", "first", "follow-up")),
	dim(enum(fact("turn.stage", expr.String, trn, "first or follow-up; only intents with dims = [\"stage\"] may read it."), enumOf(Stages...))),
	fact("unit.ref", expr.String, trn, "The unit as the instructions name it, such as owner/repo#12."),
	fact("unit.label", expr.String, nam, "The unit as people read it: a pull request as owner/repo#12, any other unit as provider:key."),
	fact("session.title", expr.String, nam, "The manifest's session.title."),
	fact("workspace.name", expr.String, nam, "The name of the worktree created for the unit; empty when the turn creates none or before it is named."),
	taskName("task.title", "The task's title."),
	taskName("task.short_id", "The task's id, short: its first six hex digits."),
	fact("session.started", expr.Time, nam, "When the session starts; a template formats it with Go's layout, as {{.session.started.Format \"02/01 15:04\"}}. Zero when unset; test with `> timestamp(0)`."),
	fact("config.plugins.github.address_reviews", expr.Bool, both, "The plugins.github.address_reviews setting for the pull request."),
	fact("config.plugins.github.address_checks", expr.Bool, both, "The plugins.github.address_checks setting for the pull request."),
	fact("config.plugins.github.followups_without_workspace", expr.Bool, both, "The plugins.github.followups_without_workspace setting for the pull request."),
	fact("config.plugins.github.address_without_workspace", expr.Bool, both, "The plugins.github.address_without_workspace setting for the pull request."),
	enum(wf("session", "state", expr.String, "active (it has a live process), dormant (no process; its manager can wake it) or closed."), enumOf("active", "dormant", "closed")),
	wf("session", "manager", expr.String, "The workspace manager that owns the session, such as orca or claude-desktop."),
	wf("session", "managed", expr.Bool, "The session was started by the loop (sessions.managed)."),
	wf("session", "listed", expr.Bool, "The session's manager lists its process as usable."),
	wf("session", "orphaned", expr.Bool, "The session's manager lists its process as not usable."),
	untrusted(wf("session", "agent_state", expr.String, "The agent's state as the manager reports it.")),
	wf("session", "user_active", expr.Bool, "The user acts in the session now."),
	wf("session", "user_typed_after_turn", expr.Bool, "The user typed in the session after the loop's last turn."),
	wf("session", "prompt_knowable", expr.Bool, "Whether the user typed can be known, from the hook or the transcript."),
	untrusted(wf("session", "prompted_while_idle", expr.Bool, "A manual session got a prompt during its idle period.")),
	untrusted(enum(wf("session", "open_turn", expr.String, "The state of the session's open turn; empty when none."), enumOf(TurnStates...))),
	untrusted(enum(wf("session", "newest_state", expr.String, "The state of the session's newest turn."), enumOf(TurnStates...))),
	wf("session", "newest_age", expr.Duration, "Time since the newest turn was sent or started."),
	wf("session", "newest_intent", expr.String, "The intent of the newest turn."),
	wf("session", "closes_when_done", expr.Bool, "The newest turn's intent closes its session when done."),
	untrusted(wf("session", "idle_since", expr.Time, "When the current idle period started. Zero when unset; test with `> timestamp(0)`.")),
	untrusted(wf("session", "idle_for", expr.Duration, "Time since idle_since.")),
	wf("session", "unread_gates", expr.Int, "Gates of the session's turns the user answered, whose notice reached the session and whose answer its agent has not read with task_read."),
	wf("session", "unread_since", expr.Time, "When the notice of the oldest unread answer of unread_gates reached the session; zero when none. Test with `> timestamp(0)`."),
	wf("session", "unread_for", expr.Duration, "Time since unread_since; 0 when none."),
	enum(wf("session", "pr_state", expr.String, "The state of the book's pull request: OPEN, CLOSED or MERGED; empty when none."), enumOf(append([]string{""}, PRStates...)...)),
	wf("session", "duplicate", expr.Bool, "Another listed session resumes the same agent session; the focused one, one the user prompted, and else the oldest are never duplicates."),
	wf("session", "worktree_gone", expr.Bool, "The session's worktree path no longer exists."),
	wf("session", "closed_by_manager", expr.Bool, "The dormant session's manager keeps its own record of it, and the user closed it there, such as archiving it in Claude Desktop."),
	wf("session", "transcript_gone", expr.Bool, "The agent's transcript of the session, as its hooks reported it, no longer exists."),
	wf("session", "process_gone", expr.Bool, "The active session's process was missed by two complete censuses of its manager."),
	untrusted(wf("session", "fingerprint", expr.String, "The session id, its state, the start of the idle period and the newest turn's sequence.")),
	wf("session", "repo", expr.String, "owner/repo of the book's pull request; empty when none."),
	enum(wf("workspace", "role", expr.String, "author (a taken-over workspace included), review or none."), enumOf("author", "review", "none")),
	wf("workspace", "main", expr.Bool, "The workspace is the repository's main checkout."),
	wf("workspace", "pr", expr.String, "The linked pull request, owner/repo#number; empty when none."),
	enum(wf("workspace", "pr_state", expr.String, "The linked pull request's state: OPEN, CLOSED or MERGED; empty when none."), enumOf(append([]string{""}, PRStates...)...)),
	wf("workspace", "pr_finished_for", expr.Duration, "The linked pull request's pr.finished_for."),
	wf("workspace", "path_exists", expr.Bool, "The worktree path exists."),
	wf("workspace", "temporary", expr.Bool, "The worktree lives under a temporary path."),
	wf("workspace", "duplicate", expr.Bool, "Neither a main checkout nor the workspace the manager links to its pull request, while another non-main workspace is."),
	untrusted(wf("workspace", "dirty", expr.Int, "Modified tracked files.")),
	untrusted(wf("workspace", "untracked", expr.Int, "Untracked files outside cleanup.disposable.")),
	untrusted(wf("workspace", "unpushed", expr.Int, "Commits on no remote.")),
	untrusted(enum(wf("workspace", "newest_turn_state", expr.String, "The state of the newest turn of the linked pull request's book; empty when none."), enumOf(TurnStates...))),
	untrusted(wf("workspace", "open_turn", expr.Bool, "The linked pull request's book has an open turn.")),
	untrusted(wf("workspace", "pendency", expr.List, "What holds work: modified files, untracked files, commits on no remote, turn asked unanswered, turn failed, turn unrecorded, turn open.")),
	wf("workspace", "sessions", expr.Int, "Agent sessions in the workspace that are active, or dormant and active within cleanup.idle."),
	wf("workspace", "manager", expr.String, "The workspace manager that owns the worktree."),
	untrusted(wf("workspace", "last_activity", expr.Time, "The newest activity in the workspace. Zero when unset; test with `> timestamp(0)`.")),
	untrusted(wf("workspace", "quiet_for", expr.Duration, "Time since last_activity.")),
	untrusted(wf("workspace", "fingerprint", expr.String, "The worktree path and its head.")),
	wf("workspace", "repo", expr.String, "owner/repo; empty when unknown."),
	wf("task", "id", expr.String, "The task's id."),
	untrusted(wf("task", "state", expr.String, "The task's state, one of the states tasks.toml declares.")),
	wf("task", "unit", expr.String, "The task's first unit as provider:key: the unit it was captured or derived on, else its first attachment; empty for a task with none."),
	enum(wf("task", "unit_provider", expr.String, "The provider of task.unit; empty for a task with none."), enumOf(append([]string{""}, providerIDs()...)...)),
	wf("task", "units", expr.List, "The task's units as provider:key, in order: the unit it was captured or derived on, then those attached to it in attach order, then any only its items name. The task itself is not one of them."),
	wf("task", "unit_providers", expr.List, "The providers of task.units, each once, in order."),
	timeVarying(wf("task", "link_ended", expr.Bool, "The task has a unit, and every one of task.units has ended as its plugin says (pr.ended, slack.ended); a unit of a plugin that says nothing never ends.")),
	wf("task", "turns", expr.Int, "Turns run on the task itself, such as a start; turns on its units are not counted."),
	wf("task", "captured_by_user", expr.Bool, "The user captured the task: origin user and kind capture. False for a task derived from a unit's items, such as one from a Slack thread, or opened by another service."),
	untrusted(wf("task", "link_started_by_me", expr.Bool, "The viewer started task.unit, from the facts the tasks receive.")),
	untrusted(wf("task", "link_closure_posted", expr.Bool, "A closure was posted on task.unit, from the facts the tasks receive.")),
	untrusted(wf("task", "items_open", expr.Int, "Open items of the task's units.")),
	untrusted(wf("task", "items_resolved", expr.Int, "Resolved items of the task's units.")),
	wf("task", "settled_at", expr.Time, "The newest update of a settled draft the workflow proposed on the task, withdrawn ones included; zero when none. Test with `> timestamp(0)`."),
	untrusted(wf("task", "fingerprint", expr.String, "The task's id, state and update time.")),
	untrusted(enum(wf("task", "category", expr.String, "The category of the task's state: inbox, ready, active, waiting, deferred, done or dropped."), enumOf(categoryNames()...))),
	untrusted(wf("task", "owed_by", expr.String, "Who owes the task's next step, as tasks.toml's owed_by says: me, a person's key, or empty when nobody does.")),
	timeVarying(untrusted(wf("task", "owed_since", expr.Time, "Since when task.owed_by has owed the next step; zero when nobody does. Test with `> timestamp(0)`."))),
	timeVarying(wf("task", "since_movement", expr.Duration, "Time since the task, or a unit linked to it, last moved: a state change, a source event, a run.")),
	timeVarying(untrusted(wf("task", "check_on", expr.Time, "When to check on the task: its check_on property, else, when the items it holds wait on someone else and not on you, tasks.stale_waiting_days working days after the oldest of them; zero when neither. Test with `> timestamp(0)`."))),
	wf("task", "gates_open", expr.Int, "The task's open gates."),
	wf("task", "runs_live", expr.Int, "The task's runs whose session is live."),
	untrusted(wf("task", "wake_until", expr.Time, "When the task's wake wakes it; zero when it has none, or one that waits on events or a condition only. Test with `> timestamp(0)`.")),
	untrusted(wf("task", "wake_when", expr.String, "The condition the task's wake waits on; empty when none.")),
	wf("task", "repo", expr.String, "owner/repo of the task's work: its placement, else its pull request's; empty when unknown."),
	untrusted(wf("task", "sequential_next", expr.Bool, "The task is the first open child of a sequential parent.")),
	timeVarying(untrusted(wf("task", "review_due", expr.Bool, "The task is a parent whose review is due."))),
	untrusted(wf("task", "children_ready", expr.Bool, "The task has a child in the ready category.")),
	timeVarying(untrusted(wf("task", "ask_age", expr.Duration, "The age of the task's oldest unanswered ask; zero when none is."))),
	wf("task", "promised", expr.Time, "The date the user promised the task by, its promised property; zero when unset. Test with `> timestamp(0)`."),
	timeVarying(untrusted(wf("task", "extraction_lag", expr.Duration, "How long the oldest message of a unit linked to the task has waited to be extracted; zero when none waits."))),
	untrusted(wf("task", "link_owes", expr.List, "Who the open items the task holds wait on, each once: me for the user, else a person's key such as slack:U0123; a unit the task holds with none of its items, all the unit's open items.")),
	wf("gate", "kind", expr.String, "The gate's kind: draft, plan, question, post, proposal, trust or step."),
	wf("gate", "name", expr.String, "The gate's name in gates.toml; empty for a gate of no profile name."),
	wf("gate", "status", expr.String, "The gate's status: open, approved, rejected, answered, dismissed or superseded."),
	wf("gate", "delivery", expr.String, "A draft gate's delivery state, such as sending or failed; empty for any other gate."),
	briefWF("goal", expr.String, "The brief's goal; empty when the unit has no brief."),
	dim(enum(briefWF("risk", expr.String, "The brief's risk: low, medium or high."), map[unit.ProviderID][]string{anyProvider: facet.Risks})),
	dim(enum(briefWF("kind", expr.String, "The brief's kind, from the provider's taxonomy."), map[unit.ProviderID][]string{unit.GitHub: facet.Kinds})),
	dim(enum(briefWF("size", expr.String, "The brief's size; GitHub briefs only."), map[unit.ProviderID][]string{anyProvider: facet.Sizes})),
	briefWF("tags", expr.List, "The brief's normalized key:value tags."),
	briefWF("missing", expr.List, "The brief intent's extra fields the unit's brief lacks: new or changed since it was written."),
	wf("draft", "person_waited", expr.Bool, "The draft's person is still waited on by its task; true for a draft of no person."),
	wf("draft", "items_owed", expr.Int, "Items of the draft still open and held by its person, or unowned for a draft of no person."),
	wf("draft", "items_assigned", expr.Int, "Items of the draft that have an owner, someone waited on, or are no longer open."),
	wf("draft", "unit_open", expr.Bool, "The draft's unit still exists: its task is neither done nor in the trash, or its unit is stored."),
	enum(approvalFact("delivery", expr.String, "How the unit's plugin posts the draft: message, reaction or comment. A plugin that posts every text as a comment makes each draft that is not a reaction a comment, whatever its proposer asked for."), enumOf(DraftDeliveries...)),
	approvalFact("proposer", expr.String, "What proposed the draft: workflow:<name> for a workflow's draft.propose, intent:<name> for a turn's or a session's, rule:<name> for a plugin rule's."),
	approvalFact("effects", expr.List, "The effects the draft applies once sent when the loop named them, a workflow's draft.propose or a plugin rule; empty for a turn's or a session's draft, whose agent names them. A workflow's rules and the configuration's read it; an intent's do not."),
	approvalFact("purpose", expr.String, "The draft's purpose when the loop named it, a workflow's draft.propose or a plugin rule; empty for a turn's or a session's draft, whose agent names it. A workflow's rules and the configuration's read it; an intent's do not."),
	enum(wf("trigger", "kind", expr.String, "sweep, event or schedule."), enumOf("sweep", "event", "schedule")),
	wf("trigger", "event.kind", expr.String, "The event's kind; empty unless kind is event."),
	wf("trigger", "event.key", expr.String, "The event's key."),
	wf("trigger", "event.id", expr.Int, "The event's id, unique and growing; 0 unless kind is event."),
	untrusted(wf("trigger", "event.data", expr.String, "The event's data: a person's or an agent's text, such as an intake's note, an answer or the mentions of a sent nudge.")),
	wf("trigger", "fired_at", expr.Time, "When the schedule fired; zero unless kind is schedule. Test with `> timestamp(0)`."),
	wf("config", "cleanup.idle", expr.Duration, "The cleanup.idle setting."),
	wf("config", "cleanup.asked_idle", expr.Duration, "The cleanup.asked_idle setting."),
	wf("config", "cleanup.workspace_grace", expr.Duration, "The cleanup.workspace_grace setting, in working time."),
	wf("config", "cleanup.manual_idle", expr.Duration, "The cleanup.manual_idle setting."),
	wf("config", "cleanup.stale_workspace", expr.Duration, "The cleanup.stale_workspace setting."),
	wf("config", "plugins.github.nudge.silent_days", expr.Int, "The plugins.github.nudge.silent_days setting, in calendar days."),
	wf("config", "tasks.stale_waiting_days", expr.Int, "The tasks.stale_waiting_days setting, in working days."),
	wf("config", "drafts.repeat_after", expr.Duration, "The drafts.repeat_after setting."),
	wf("config", "turns.per_day", expr.Int, "The turns.per_day setting: automated turns on units whose plugin keeps no per-unit count, all workflows together."),
}

// endedName is the name, under a provider's slot, of the fact its plugin
// says a unit has ended with.
const endedName = "ended"

// EndedFact is the workflow fact provider p's plugin says one of its units
// has ended with; false for a provider whose plugin says nothing.
func EndedFact(p unit.ProviderID) (string, bool) {
	slot, ok := Slots[p]
	if !ok {
		return "", false
	}
	name := slot + "." + endedName
	if _, ok := FactType(name); !ok {
		return "", false
	}
	return name, true
}

// put declares f in env with its closed values, when they hold for every
// provider.
func put(env expr.Env, f Fact) {
	env[f.Name] = f.Type
	if vs, ok := f.Enum[anyProvider]; ok {
		env.SetEnum(f.Name, vs)
	}
}

// Narrow closes, in env, each fact whose values are declared per provider
// to the values of providers: their union when each of them declares a
// set, and any value when one does not or providers is empty.
func Narrow(env expr.Env, providers []unit.ProviderID) {
	for _, f := range Facts {
		if _, in := env[f.Name]; !in || f.Enum == nil {
			continue
		}
		if _, all := f.Enum[anyProvider]; all {
			continue
		}
		env.ClearEnum(f.Name)
		var union []string
		closed := len(providers) > 0
		for _, p := range providers {
			vs, ok := f.Enum[p]
			closed = closed && ok
			union = append(union, vs...)
		}
		if closed {
			env.SetEnum(f.Name, union)
		}
	}
}

// approvalFact is a fact of the draft a draft gate's rule reads,
// named draft.<name>.
func approvalFact(name string, t expr.Type, doc string) Fact {
	return Fact{Name: DraftSlot + "." + name, Type: t, Slots: []FactSlot{ApprovalSlot}, Doc: doc}
}

// DraftPurpose and DraftEffects are the approval facts an intent's rules
// may not read: a turn's agent names its drafts' purpose and effects.
const (
	DraftPurpose = "draft.purpose"
	DraftEffects = "draft.effects"
)

// AutoOnly are the approval facts only a workflow's rules and the
// configuration's read.
var AutoOnly = []string{DraftPurpose, DraftEffects}

// DraftDelivery is the approval fact a rule must pin to a delivery to
// release a draft of it whose delivery posts.
const DraftDelivery = "draft.delivery"

// ApprovalEnv is the env of a draft gate's rule's when: the approval
// slot's facts, without AutoOnly unless auto, and the facts of each of
// slots, never a brief's, which an agent writes.
func ApprovalEnv(auto bool, slots ...string) expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		switch {
		case slices.Contains(f.Slots, ApprovalSlot) && (auto || !slices.Contains(AutoOnly, f.Name)):
			put(env, f)
		case slices.Contains(f.Slots, WorkflowSlot) && f.Slot != "" && slices.Contains(slots, f.Slot):
			put(env, f)
		}
	}
	return env
}

// providerIDs are the providers of Slots, sorted.
func providerIDs() []string {
	out := make([]string, 0, len(Slots))
	for p := range Slots {
		out = append(out, string(p))
	}
	slices.Sort(out)
	return out
}

// UnitSlots are the workflow slots of every provider's units.
func UnitSlots() []string {
	out := make([]string, 0, len(Slots))
	for _, s := range Slots {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// WorkflowSlots are the slots of workflow facts: each provider's, then the
// brief's, the draft's, the gate's, the trigger's and the config's.
var WorkflowSlots = slices.Concat(coreUnitSlots, nonUnitSlots)

// coreUnitSlots are the slots of the core providers' units; Merge adds each
// plugin's after them.
var coreUnitSlots = []string{"session", "workspace", "task"}

// nonUnitSlots are the workflow slots of no provider.
var nonUnitSlots = []string{BriefSlot, DraftSlot, GateSlot, "trigger", "config"}

// DraftSlot is the slot of the facts a workflow's [withdraw] when reads of
// each draft it proposed.
const DraftSlot = "draft"

// WorkflowEnv is the env of a workflow's when: the facts of slot, the
// brief's when slot is one of BriefSubjects, trigger.* and config.*.
func WorkflowEnv(slot string) expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if slices.Contains(f.Slots, WorkflowSlot) && (f.Slot == slot || slices.Contains(f.Subjects, slot) || f.Slot == "trigger" || f.Slot == "config") {
			put(env, f)
		}
	}
	Narrow(env, providersOfSlot(slot))
	return env
}

// providersOfSlot are the providers whose units' facts are under slot.
func providersOfSlot(slot string) []unit.ProviderID {
	var out []unit.ProviderID
	for p, s := range Slots {
		if s == slot {
			out = append(out, p)
		}
	}
	return out
}

// WithdrawEnv is the env of a workflow's [withdraw] when: WorkflowEnv(slot)
// and the draft slot's facts.
func WithdrawEnv(slot string) expr.Env {
	env := WorkflowEnv(slot)
	for _, f := range Facts {
		if f.Slot == DraftSlot {
			put(env, f)
		}
	}
	return env
}

// ExtraFacts are the brief.extra.<name> facts the brief intent's extra
// schema declares, typed from its properties: a string (with or without an
// enum), an integer, a boolean or an array of strings, the types the loader
// lets a builder's schema hold. A predicate's env is extended with them at
// load, so naming an undeclared extra fails there.
func ExtraFacts(props []jsonschema.Property) expr.Env {
	env := expr.Env{}
	for _, p := range props {
		t, ok := map[string]expr.Type{"string": expr.String, "integer": expr.Int, "boolean": expr.Bool, "array": expr.List}[p.Type]
		if ok {
			env[ExtraPrefix+p.Name] = t
			if p.Type == "string" && len(p.Enum) > 0 {
				env.SetEnum(ExtraPrefix+p.Name, p.Enum)
			}
		}
	}
	return env
}

// ClassifyPrefix names a classify step's output as a fact.
const ClassifyPrefix = "classify."

// IsUntrusted says whether a rule may not trust the fact name: an agent or a
// third party sets its value. A brief's extra fields and a classification's
// output are untrusted whatever their name.
func IsUntrusted(name string) bool {
	if strings.HasPrefix(name, ExtraPrefix) || strings.HasPrefix(name, ClassifyPrefix) {
		return true
	}
	for _, f := range Facts {
		if f.Name == name && f.Untrusted {
			return true
		}
	}
	return false
}

// ExtraPrefix names a brief's extra field as a fact.
const ExtraPrefix = "brief.extra."

// PropPrefix names a task property of tasks.toml as a fact, typed from its
// declaration.
const PropPrefix = "task.prop."

// PropByPrefix names who last wrote a task property as a fact,
// task.prop_by.<name>: person, workflow, agent or plugin, empty while unset.
const PropByPrefix = "task.prop_by."

// PropFactType is the type of the fact task.prop.<name> of a property of
// type t: an enum, an actor and a string read as a string (an actor as its
// key), a list type as a list. An int or a duration of 0 reads as unset.
func PropFactType(t task.PropType) expr.Type {
	switch t {
	case task.PropInt:
		return expr.Int
	case task.PropDate:
		return expr.Time
	case task.PropDuration:
		return expr.Duration
	case task.PropStrings, task.PropActors:
		return expr.List
	}
	return expr.String
}

// TaskSlot is the slot of a task's facts, which the task.prop.* facts of
// the profile's tasks.toml extend.
var TaskSlot = Slots[unit.Task]

// LinkSignalPrefix names a signal the plugins report per link, such as
// stalled, aggregated over the task's links as a bool fact.
const LinkSignalPrefix = "task.link_signal."

// GateSlot is the slot of a gate's facts, which a view over gates reads.
const GateSlot = "gate"

// TurnEnv is every provider's own facts of the turn slot, the ones a turn
// reads before it is built.
func TurnEnv() expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if f.Provider != "" && slices.Contains(f.Slots, TurnSlot) {
			put(env, f)
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
			put(env, f)
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

// TimeVarying says whether the workflow fact name changes as time passes
// with nothing else changing; false for a name that is no workflow fact.
func TimeVarying(name string) bool {
	for _, f := range Facts {
		if f.Name == name && slices.Contains(f.Slots, WorkflowSlot) {
			return f.TimeVarying
		}
	}
	return false
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

// OfferFacts are the facts a verb's offer reads: a task's own, which a
// list of tasks reads without a store.
var OfferFacts = []string{"task.id", OfferKind, "task.state", "task.unit", "task.unit_provider"}

// OfferKind is the task's kind property as an offer reads it, typed a
// string whatever tasks.toml declares, so an offer compiles before the
// properties load.
const OfferKind = PropPrefix + "kind"

// OfferEnv is the env of a verb's offer in tasks.toml.
func OfferEnv() expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if slices.Contains(OfferFacts, f.Name) {
			put(env, f)
		}
	}
	env[OfferKind] = expr.String
	env.SetUntrusted(OfferKind)
	Narrow(env, providersOfSlot("task"))
	return env
}
