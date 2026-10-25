// Package builtin is the Go vocabulary manifests name: builtins by slot,
// reserved names and keys, and the facts predicates read. The implementations
// live in internal/cli and internal/daemon and register at wiring.
package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/gtd"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
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
	{StepTurnStart, providers(unit.GitHub), "", []string{"intent", "round"}, nil,
		"Starts a turn: `intent`, and `round` (`first` or `follow-up`) for a review. Caps, the brief, busy and held checks apply as for any automatic round."},
	{"classify", nil, workflow.ReachLocal, []string{"prompt", "schema"}, []workflow.TriggerKind{workflow.TriggerEvent},
		"A headless classification whose output becomes facts. Event triggers only, cached by fingerprint, under `classify.daily_cap`."},
	{"notify.self", nil, workflow.ReachPrivate, []string{"text"}, nil,
		"A private notification."},
}

func providers(ps ...unit.ProviderID) []unit.ProviderID { return ps }

// Slots name each provider's fact slot.
var Slots = map[unit.ProviderID]string{unit.GitHub: "pr", unit.Session: "session", unit.Workspace: "workspace"}
