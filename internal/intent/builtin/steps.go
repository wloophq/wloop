package builtin

import (
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// StepDef is a workflow step the engine can run: the providers whose units it
// acts on, its reach, the arguments a manifest may set, the triggers it runs
// under and what it does.
type StepDef struct {
	Name      string
	Providers []unit.ProviderID // empty means any
	Reach     workflow.Reach    // "" for turn.start: the intent decides
	Args      []string
	Trigger   []workflow.TriggerKind // empty means any
	Doc       string
}

// StepTurnStart is the step that starts an agent turn: its reach is its intent's.
const StepTurnStart = "turn.start"

// StepDraftPropose is the step that proposes a draft in the Outbox.
const StepDraftPropose = "draft.propose"

// The values of draft.propose's arguments.
var (
	DraftDeliveries = []string{"message", "reaction", "comment"}
	// DraftEmojiDeliveries are the deliveries that post an emoji, which
	// need the reaction argument, and no text.
	DraftEmojiDeliveries = []string{"reaction"}
	DraftItems           = []string{"owed", "resolved", "unowned", "unowned_aged", "all"}
	DraftPer             = []string{"subject", "person"}
	DraftTargets         = []string{"unit", "item"}
	// HoldForever is hold_after_sent's value that never proposes again
	// after a draft was sent.
	HoldForever = "forever"
)

// TurnModes are the values of turn.start's mode.
var TurnModes = []string{"session", "headless"}

// Steps is the step vocabulary of workflow manifests.
var Steps = []StepDef{
	{"session.close", providers(unit.Session), workflow.ReachLocal, nil, nil,
		"Closes the agent session."},
	{"workspace.remove", providers(unit.Workspace), workflow.ReachLocal, nil, nil,
		"Removes the workspace, after its guards. Never with `force`: a manifest that sets it fails to load."},
	{"workspace.unregister", providers(unit.Workspace), workflow.ReachLocal, nil, nil,
		"Unregisters a workspace whose path is gone."},
	{"turn.dismiss", providers(unit.Session, unit.GitHub), workflow.ReachLocal, []string{"reason"}, nil,
		"Dismisses the open turn, recording the reason."},
	{"answer.refuse", providers(unit.GitHub), workflow.ReachLocal, []string{"reason"}, nil,
		"Refuses a queued answer whose route is gone."},
	{"session.mark_dormant", providers(unit.Session), workflow.ReachLocal, nil, nil,
		"Marks dormant an active session whose process its manager's census no longer lists: the session stays open, and the next turn wakes it."},
	{StepTurnStart, nil, "", []string{"intent", "round", "mode"}, nil,
		"Starts a turn of `intent` on the subject, which the intent must serve; `round` (`first` or `follow-up`) for a review; `mode` (`session` or `headless`), by default the intent's first mode. On a pull request, caps, the brief, busy and held checks apply as for any automatic round; on any other unit, the workflow's `limits.per_day` and `turns.automated_per_day`."},
	{StepDraftPropose, nil, workflow.ReachLocal, []string{"purpose", "delivery", "text", "reaction", "items", "per", "target", "on_posted", "hold", "hold_after_sent"}, nil,
		"Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`subject` or `person`), `target` (`unit` or `item`), `on_posted` (effects, comma-separated), `hold` (a duration after a draft settled, or a duration of the config such as `config.drafts.repeat_after`) and `hold_after_sent` (likewise, or `forever`). Holds while a draft of the workflow, subject and person is open or failed; a run that leaves a person under a hold that ends with time stays pending until it ends."},
	{"classify", nil, workflow.ReachLocal, []string{"prompt", "schema"}, []workflow.TriggerKind{workflow.TriggerEvent},
		"A headless classification whose output becomes facts. Event triggers only, cached by fingerprint, under `classify.daily_cap`."},
	{"notify.self", nil, workflow.ReachPrivate, []string{"text"}, nil,
		"A private notification."},
}

func providers(ps ...unit.ProviderID) []unit.ProviderID { return ps }
