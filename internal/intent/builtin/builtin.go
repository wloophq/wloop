// Package builtin is the Go vocabulary manifests name: builtins by slot,
// reserved names and keys, and the facts predicates read. The implementations
// live in internal/cli and internal/daemon and register at wiring.
package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/gtd"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/expr"
)

type Slot string

const (
	SlotWorkspace Slot = "workspace"
	SlotLaunch    Slot = "launch"
	SlotPrepare   Slot = "prepare"
	SlotBefore    Slot = "before"
	SlotOnSent    Slot = "on_sent"
	SlotPolicy    Slot = "policy"
	SlotContext   Slot = "context"
	SlotReconcile Slot = "reconcile"
)

// LaunchProbe is the launch of a probe: a synthetic pull request in the
// workspace the request names.
const LaunchProbe = "probe"

var Names = map[Slot][]string{
	SlotWorkspace: {"review", "author", "pr_else_main", "given"},
	SlotLaunch:    {"", LaunchProbe},
	SlotPrepare:   {"", "nudge.prepare"},
	SlotBefore:    {"take_over"},
	SlotOnSent:    {"plan.drop_pending", "event.round_sent", "event.nudged", "record_round", "keeps_guidance", "mark_reviewed", "mark_addressed"},
	SlotPolicy:    {"round", "probe"},
	SlotContext:   {"pr", "synthetic"},
	SlotReconcile: {"", "my_review"},
}

// ReservedNames are names a user intent may not take: CLI commands, built-in action ids, profile words, dimensions.
// The loader checks them against user layers only.
var ReservedNames = []string{
	"app", "brief", "config", "doctor", "eval-instructions", "explore", "help", "hook", "init", "install",
	"instructions", "intake", "mute", "open", "outbox", "pause", "plan", "profile", "remove", "resume",
	"serve", "slack", "status", "store", "take-over", "timings", "track", "trust", "tui", "turn", "uninstall", "unmute", "untrack",
	"version", "workflows",

	"answer", "brief_refresh", "continue", "dismiss", "guide", "intake_cancel", "intake_without", "remove_force", "skip",
	"plan_apply", "plan_dismiss", "run_now", "take_over", "trust_run", "unblock",

	"eval", "intents", "settings", "tags", "try",

	"association", "base", "kind", "label", "organization", "repo", "reviewer", "risk", "round", "size", "team", "user",
}

// ActionKeys are the keys of the built-in actions, by id; internal/keys
// holds the rule they follow.
var ActionKeys = map[string]string{
	"open":      "O",
	"take_over": "A",
	"skip":      "S",
	"pause":     "H",
	"resume":    "H",
	"run_now":   "R",
	"mute":      "M",
	"unmute":    "M",
	"answer":    "W",
	"continue":  "C",
	"dismiss":   "X",
	"guide":     "G",
	"trust_run": "T",
	"unblock":   "D",
}

// IntentVerbs are the gtd verbs a manifest's verb may name: those an entry
// bridges to its unit's served action.
var IntentVerbs = []string{string(gtd.VerbApprove), string(gtd.VerbDelegate), string(gtd.VerbDismiss)}

type FactSlot string

const (
	ActionSlot   FactSlot = "action"
	TurnSlot     FactSlot = "turn"
	WorkflowSlot FactSlot = "workflow"
)

type Fact struct {
	Name  string
	Type  expr.Type
	Slots []FactSlot
	Doc   string
	Slot  string // a workflow fact's slot: one of WorkflowSlots
	// Provider is the provider whose intents alone may read the fact; "" for every provider.
	Provider unit.ProviderID
	// Subjects are the workflow slots besides Slot whose when reads the
	// fact: the brief slot's facts are read on every unit that has a brief.
	Subjects []string
	// Dim makes the fact a profile dimension: a block path condition under
	// the intents of its provider.
	Dim bool
	// Enum are the fact's values by provider, "" standing for every
	// provider; a provider absent takes any value.
	Enum map[unit.ProviderID][]string
}

var inputTypes = map[string]expr.Type{"flag": expr.Bool, "text": expr.String, "list": expr.List}

// Env is the slot's facts plus, for the turn slot, the intent's declared inputs.
func Env(slot FactSlot, inputs []dintent.Input) expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if slices.Contains(f.Slots, slot) {
			env[f.Name] = f.Type
		}
	}
	if slot == TurnSlot {
		for _, in := range inputs {
			if t, ok := inputTypes[in.Type]; ok {
				env["inputs."+in.Name] = t
			}
		}
	}
	return env
}

// RecordStates are the states a manifest's record.states may list.
var RecordStates = []string{string(record.Done), string(record.Settled), string(record.Asked), string(record.Failed)}

// IntentDims are the profile dimensions a manifest's dims may list.
var IntentDims = []string{dintent.DimRound, dintent.DimReviewer}

// ReservedFlags are flags the CLI owns; no input may declare them.
var ReservedFlags = []string{"--no-wait", "--no-brief", "--turn", "--help"}

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

// RoundProviders are the providers whose turn.start runs under the round
// caps; a workflow starting turns on any other must set limits.per_day.
var RoundProviders = []unit.ProviderID{unit.GitHub}

// The values of draft.propose's arguments.
var (
	DraftDeliveries = []string{"message", "reaction", "comment"}
	DraftItems      = []string{"owed", "resolved", "unowned", "unowned_aged", "all"}
	DraftPer        = []string{"subject", "person"}
	DraftTargets    = []string{"unit", "item"}
	// HoldForever is hold_after_sent's value that never proposes again
	// after a draft was sent.
	HoldForever = "forever"
)

// Roles are what Go resolves an intent by, a manifest's role.
var Roles = []string{"round.review", "round.address", "nudge", "probe", "headless.default"}

// BuildsBrief is what the brief intent builds.
const BuildsBrief = "brief"

// Builds are what a builder intent may build.
var Builds = []string{BuildsBrief}

// The values of a manifest's round_kind, cap and reason.
var (
	RoundKinds  = []string{"review", "address"}
	Caps        = []string{"rounds"}
	TurnReasons = []string{"address_round"}
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
		"Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`subject` or `person`), `target` (`unit` or `item`), `on_posted` (effects, comma-separated), `hold` (a duration after a draft settled) and `hold_after_sent` (a duration, or `forever`). Holds while a draft of the workflow, subject and person is open or failed."},
	{"classify", nil, workflow.ReachLocal, []string{"prompt", "schema"}, []workflow.TriggerKind{workflow.TriggerEvent},
		"A headless classification whose output becomes facts. Event triggers only, cached by fingerprint, under `classify.daily_cap`."},
	{"notify.self", nil, workflow.ReachPrivate, []string{"text"}, nil,
		"A private notification."},
}

func providers(ps ...unit.ProviderID) []unit.ProviderID { return ps }

// Slots name each provider's fact slot.
var Slots = map[unit.ProviderID]string{unit.GitHub: "pr", unit.Session: "session", unit.Workspace: "workspace",
	unit.Entry: "entry", dslack.Provider: "slack"}

// Effect is an effect a sent draft applies: the name a draft's effects, a
// draft.propose's on_posted and an intent's draft_effects list.
type Effect struct {
	Name string
	// Provider is the provider whose drafts alone may declare it; "" any.
	Provider unit.ProviderID
	Doc      string
}

// Effects is the effect vocabulary of drafts.
var Effects = []Effect{
	{"entry.nudged", "", "Stamps the last nudge of the draft's entry, or of every live waiting entry of its unit when it names none."},
	{"unit.closed", "", "Records the closure of the draft's unit, which ends its close entry and unwatches it after its warm period; through the unit's plugin."},
	{"pr.nudged", unit.GitHub, "Records the nudged event on the pull request, which the nudge refusal reads; applied by the GitHub publisher as it posts."},
}

// EffectNames are the names of Effects.
func EffectNames() []string {
	out := make([]string, len(Effects))
	for i, e := range Effects {
		out[i] = e.Name
	}
	return out
}
