package builtin

import (
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// StepDef is a workflow step the engine can run: the providers whose units it
// acts on, its reach, the triggers it runs under and what it does. Its
// arguments are its rows of the grammar (StepArgs).
type StepDef struct {
	Name      string
	Providers []unit.ProviderID      // empty means any
	Reach     workflow.Reach         // "" for turn.start: the intent decides
	Trigger   []workflow.TriggerKind // empty means any
	Doc       string
}

// StepTurnStart is the step that starts an agent turn: its reach is its intent's.
const StepTurnStart = "turn.start"

// StepDraftPropose is the step that proposes a draft in the Outbox.
const StepDraftPropose = "draft.propose"

// DraftTargetPerson is draft.propose's target that posts each per-person draft in the direct conversation
// between the user and the person it is to.
const DraftTargetPerson = "person"

// The steps whose arguments the grammar names.
const (
	StepTurnDismiss  = "turn.dismiss"
	StepAnswerRefuse = "answer.refuse"
	StepClassify     = "classify"
	StepNotifySelf   = "notify.self"
	StepTaskVerb     = "task.verb"
	StepGateOpen     = "gate.open"
	StepTaskSet      = "task.set"
	StepItemsResolve = "items.resolve"
)

// The values of draft.propose's arguments.
var (
	// DraftDeliveries are the deliveries the merged plugins declare, which Install sets.
	DraftDeliveries []string
	// DraftEmojiDeliveries are the deliveries the merged plugins mark emoji, which need the reaction argument,
	// and no text; Install sets them.
	DraftEmojiDeliveries []string
	DraftItems           = []string{"owed", "resolved", "unowned", "unowned_aged", "all"}
	DraftPer             = []string{"unit", "person"}
	DraftTargets         = []string{"unit", "item", DraftTargetPerson}
	// DraftTo are whom of the people a draft would address it addresses: all of them, those who can read the
	// conversation it posts on, or those who cannot.
	DraftTo = []string{"all", "members", "non_members"}
	// DraftEnded is what a draft withdrawn as its task ended does to draft.propose while that task stays ended:
	// hold the workflow's proposals of no task or of that task, or hold them only for repeat_after.
	DraftEnded = []string{"hold", "repeat"}
	// RepeatForever is repeat_after_sent's value that never proposes again
	// after a draft was sent.
	RepeatForever = "forever"
)

// TurnRunners are the values of turn.start's runner.
var TurnRunners = []string{"session", "headless"}

// TurnDue are the values of turn.start's due, its default first: plan sends a round only when the unit's plan
// holds it due; when sends one whenever the workflow's when matches.
var TurnDue = []string{"plan", TurnDueWhen}

// TurnDueWhen is the due of a turn.start whose workflow's when alone decides that its round is due.
const TurnDueWhen = "when"

// TurnOpenQuestion are the values of turn.start's open_question, its default first.
var TurnOpenQuestion = []string{"hold", QuestionReplaceIfMoved}

// QuestionReplaceIfMoved is the open_question of a turn.start whose round
// replaces a question the unit moved past.
const QuestionReplaceIfMoved = "replace_if_moved"

// Steps is the step vocabulary of workflow manifests.
var Steps = stepsWith(nil)

// PluginSteps are the steps that run only on the units of the plugins whose
// manifest lists them in steps; with none, a workflow cannot name them.
var PluginSteps = []string{StepAnswerRefuse}

var coreSteps = []StepDef{
	{"session.close", providers(unit.Session), workflow.ReachLocal, nil,
		"Closes the agent session."},
	{"workspace.remove", providers(unit.Workspace), workflow.ReachLocal, nil,
		"Removes the workspace, after its guards. Never with `force`: a manifest that sets it fails to load."},
	{"workspace.unregister", providers(unit.Workspace), workflow.ReachLocal, nil,
		"Unregisters a workspace whose path is gone."},
	{StepTurnDismiss, providers(unit.Session), workflow.ReachLocal, nil,
		"Dismisses the open turn, recording the reason."},
	{StepAnswerRefuse, nil, workflow.ReachLocal, nil,
		"Refuses a queued answer whose route is gone."},
	{"session.mark_dormant", providers(unit.Session), workflow.ReachLocal, nil,
		"Marks dormant an active session whose process its manager's census no longer lists, or has listed orphaned with no agent for a fast job (`session.process_orphaned`) when no turn is sent or started in it, its agent neither works nor waits for permission, its workspace is not focused and you did not prompt it since the loop's last turn: the session stays open, and the next turn, or a census that shows its process in a window again, wakes it. It records the state and ends no process."},
	{"session.close_orphan", providers(unit.Session), workflow.ReachLocal, nil,
		"Side effect: ends the process of an active session that the census proved a durable orphan (`session.orphan_durable`), a terminal no window of the app shows with no agent in it. It runs under `session.close`'s guards (no turn sent or started, the agent neither working nor waiting for permission, the workspace not focused, no prompt of yours since the loop's last turn) and records nothing on the session: the censuses that then miss the process mark it dormant. Refused for good on a session gone, closed or dormant."},
	{StepTurnStart, nil, "", nil,
		"Starts a turn of `intent` on the unit, which the intent must serve; `stage` (`first` or `follow-up`) for a review; `runner` (`session` or `headless`), by default the intent's first runner. On a pull request, caps, the brief, busy and held checks apply as for any automatic round; on any other unit, the workflow's `limits.per_day` and `turns.per_day`."},
	{StepDraftPropose, nil, workflow.ReachLocal, nil,
		"Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Items`, `.Person` and `.Facts`, the subject's facts by name, such as `{{index .Facts \"task.promised\"}}`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`unit` or `person`), `target` (`unit` or `item`), `effects` (the effects sending it applies), `repeat_after` (the least time after a settled draft of the same person, a duration or a duration of the config such as `config.drafts.repeat_after`) `repeat_after_sent` (the same after a sent one, or `forever`) and `ended` (`hold`, the default, holds proposals of no task or of the same task while a draft's task a verb ended stays ended; `repeat` holds them for `repeat_after`). Holds while a draft of the workflow, unit and person is open or failed; a run that leaves a person under a delay that ends with time stays pending until it ends."},
	{StepClassify, nil, workflow.ReachLocal, []workflow.TriggerKind{workflow.TriggerEvent},
		"A headless classification whose output becomes facts, `classify.<workflow>.<field>` with each - of the workflow's name an _, typed by the schema's top-level properties: `prompt` (a file in the workflow's directory, by default `prompt.md`) and `schema` (the JSON schema its answer follows, by default `schema.json`). With `with`, a list fact of task ids, it reads those tasks too and its prompt is a template over them. Event triggers only, cached by fingerprint, prompt and schema, under `classify.per_day`. Each run that reads its output records a `classified` event on the unit, whose data is the workflow's name."},
	{StepNotifySelf, nil, workflow.ReachPrivate, nil,
		"A private notification: `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Workflow`, `.Unit` and `.Facts`, the unit's facts by name, such as `{{index .Facts \"pr.slug\"}}`, none once the unit is gone)."},
	{StepTaskVerb, providers(unit.Task), workflow.ReachLocal, nil,
		"Runs a verb of tasks.toml on the task as the workflow: `verb`, one whose `by.workflow` lets a workflow run it, with the arguments its handler takes (`until` and `on_activity` for schedule, `on` and `resurface` for waiting, `set` for context and area, `by` for promise, `task` for merge, split, relate and unrelate). The plan blocks it while the task's state or the verb's `by` refuses it, and a verb that closes a task derived from a unit's items, which closes when they resolve. A verb whose `by.workflow` is `gate` opens the verb's gate instead, under the workflow's `[gates.approval]`: released by a rule, the verb runs as the workflow; approved by you, as you; a pair you took apart (`task.undone`) waits for you. It records the `verb_<verb>` event with the workflow as its actor and the chain it runs in, so another workflow may react to it within `trigger.depth`."},
	{StepGateOpen, nil, workflow.ReachLocal, nil,
		"Opens the gate `name` of gates.toml on the unit, a decision that waits for you with the intents gates.toml gives it."},
	{StepItemsResolve, providers(unit.Task), workflow.ReachLocal, nil,
		"Resolves every open item the task holds, as the workflow, with `resolution` and the workflow as its evidence, so a task derived from its units' items closes; an item you confirmed becomes a proposal, a gate on the task. Posts nothing."},
	{StepTaskSet, providers(unit.Task), workflow.ReachLocal, nil,
		"Sets the task's property `prop` of tasks.toml to `value`, written by the workflow; a property the workflow may not write is refused."},
}

// StepSays are the catalog keys of what each core step does to a plan item's unit, as strings.toml words them for
// a person; a {name} is a step argument as the clients read it.
var StepSays = map[string]string{
	"session.close":        "Close the agent session",
	"workspace.remove":     "Remove the workspace",
	"workspace.unregister": "Forget the workspace whose folder is gone",
	StepTurnDismiss:        "Dismiss the agent's open turn",
	StepAnswerRefuse:       "Refuse a queued answer whose route is gone",
	"session.mark_dormant": "Mark the agent session dormant",
	StepTurnStart:          "Start an agent turn: {intent}",
	StepDraftPropose:       "Draft a message you approve before it posts",
	StepClassify:           "Classify it with an agent call",
	StepNotifySelf:         "Notify you",
	StepTaskVerb:           "Run {verb} on the task",
	StepGateOpen:           "Ask you to decide",
	StepItemsResolve:       "Resolve its open items as {resolution}",
	StepTaskSet:            "Set {prop} to {value}",
}

// StepSaysWithTask is the catalog key of what a step that names another task does, by step.
var StepSaysWithTask = map[string]string{StepTaskVerb: "Run {verb} on the task, with “{task}”"}

// The refusals of items.resolve, which a plan item shows as its reason.
const (
	RefusalNoOpenItem = "the task holds no open item"
	RefusalNotDerived = "the task is not derived from items: its items do not close it"
)

// StepRefusals are the refusals of the core steps a plan item shows as its reason, each a key of strings.toml and
// of api.PlanReasons, which the clients word it by.
var StepRefusals = []string{RefusalNoOpenItem, RefusalNotDerived}

// StepDrafts are the steps whose message posts once you approve its draft: a plan item running one may post.
var StepDrafts = []string{StepDraftPropose}

// ResolutionSays are the catalog keys of how an item resolves, by resolution, as items.resolve's resolution reads.
var ResolutionSays = map[items.Resolution]string{
	items.Done: "resolved as done", items.Answered: "resolved as answered", items.Fixed: "resolved as fixed",
	items.Delegated: "resolved as delegated", items.Declined: "resolved as declined", items.Dismissed: "resolved as dismissed",
	items.Superseded: "resolved as superseded", items.Moot: "resolved as moot",
}

// StepArg is the grammar row of step use's argument k.
func StepArg(use, k string) (Key, bool) {
	for _, r := range stepGrammar {
		if r.Step == use && r.Path == k {
			return r, true
		}
	}
	return Key{}, false
}

// StepFiles are the rows of step use's arguments that name a file beside
// the manifest, which the loader reads. draft.propose reads its text as it
// renders, since an emoji draft has none.
func StepFiles(use string) []Key {
	var out []Key
	for _, r := range stepGrammar {
		if r.Step == use && r.Kind == KindFile && use != StepDraftPropose {
			out = append(out, r)
		}
	}
	return out
}

func providers(ps ...unit.ProviderID) []unit.ProviderID { return ps }
