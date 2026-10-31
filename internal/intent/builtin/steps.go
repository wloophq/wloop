package builtin

import (
	"github.com/wloophq/wloop/internal/domain/draft"
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

// The steps whose arguments the grammar names.
const (
	StepTurnDismiss  = "turn.dismiss"
	StepAnswerRefuse = "answer.refuse"
	StepClassify     = "classify"
	StepNotifySelf   = "notify.self"
	StepTaskVerb     = "task.verb"
	StepGateOpen     = "gate.open"
	StepTaskSet      = "task.set"
)

// The values of draft.propose's arguments.
var (
	DraftDeliveries = []string{string(draft.Message), string(draft.Reaction)}
	// DraftEmojiDeliveries are the deliveries that post an emoji, which
	// need the reaction argument, and no text.
	DraftEmojiDeliveries = []string{"reaction"}
	DraftItems           = []string{"owed", "resolved", "unowned", "unowned_aged", "all"}
	DraftPer             = []string{"unit", "person"}
	DraftTargets         = []string{"unit", "item"}
	// DraftEnded is what a draft withdrawn as its task ended does to draft.propose while that task stays ended:
	// hold the workflow's proposals of no task or of that task, or hold them only for repeat_after.
	DraftEnded = []string{"hold", "repeat"}
	// RepeatForever is repeat_after_sent's value that never proposes again
	// after a draft was sent.
	RepeatForever = "forever"
)

// TurnRunners are the values of turn.start's runner.
var TurnRunners = []string{"session", "headless"}

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
		"Marks dormant an active session whose process its manager's census no longer lists: the session stays open, and the next turn wakes it."},
	{StepTurnStart, nil, "", nil,
		"Starts a turn of `intent` on the unit, which the intent must serve; `stage` (`first` or `follow-up`) for a review; `runner` (`session` or `headless`), by default the intent's first runner. On a pull request, caps, the brief, busy and held checks apply as for any automatic round; on any other unit, the workflow's `limits.per_day` and `turns.per_day`."},
	{StepDraftPropose, nil, workflow.ReachLocal, nil,
		"Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Items`, `.Person` and `.Facts`, the subject's facts by name, such as `{{index .Facts \"task.promised\"}}`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`unit` or `person`), `target` (`unit` or `item`), `effects` (the effects sending it applies), `repeat_after` (the least time after a settled draft of the same person, a duration or a duration of the config such as `config.drafts.repeat_after`) `repeat_after_sent` (the same after a sent one, or `forever`) and `ended` (`hold`, the default, holds proposals of no task or of the same task while a draft's task a verb ended stays ended; `repeat` holds them for `repeat_after`). Holds while a draft of the workflow, unit and person is open or failed; a run that leaves a person under a delay that ends with time stays pending until it ends."},
	{StepClassify, nil, workflow.ReachLocal, []workflow.TriggerKind{workflow.TriggerEvent},
		"A headless classification whose output becomes facts: `prompt` (a file in the workflow's directory, by default `prompt.md`) and `schema` (the JSON schema its answer follows, by default `schema.json`). Event triggers only, cached by fingerprint, under `classify.per_day`."},
	{StepNotifySelf, nil, workflow.ReachPrivate, nil,
		"A private notification: `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Workflow`, `.Unit` and `.Facts`, the unit's facts by name, such as `{{index .Facts \"pr.slug\"}}`, none once the unit is gone)."},
	{StepTaskVerb, providers(unit.Task), workflow.ReachLocal, nil,
		"Runs a verb of tasks.toml on the task as the workflow: `verb`, one whose `by.workflow` lets a workflow run it, with the arguments its handler takes (`until` and `on_activity` for schedule, `on` and `resurface` for waiting, `set` for context and area, `by` for promise). The plan blocks it while the task's state or the verb's `by` refuses it, and a verb that closes a task derived from a unit's items, which closes when they resolve. It records the `verb_<verb>` event with the workflow as its actor and the chain it runs in, so another workflow may react to it within `trigger.depth`."},
	{StepGateOpen, nil, workflow.ReachLocal, nil,
		"Opens the gate `name` of gates.toml on the unit, a decision that waits for you with the intents gates.toml gives it."},
	{StepTaskSet, providers(unit.Task), workflow.ReachLocal, nil,
		"Sets the task's property `prop` of tasks.toml to `value`, written by the workflow; a property the workflow may not write is refused."},
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
