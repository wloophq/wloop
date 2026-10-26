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
	SlotOnSent:    OnSentNames,
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
	// ApprovalSlot holds the draft's own facts a [draft_approval] rule reads
	// besides its unit's.
	ApprovalSlot FactSlot = "approval"
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
	// Untrusted says that a third party's text, read by the extraction, the
	// proposing turn itself or an agent's brief sets the fact's value, so
	// no [draft_approval] rule may read it.
	Untrusted bool
}

var inputTypes = map[string]expr.Type{"flag": expr.Bool, "text": expr.String, "list": expr.List}

// Env is the slot's facts plus, for the turn slot, the intent's declared inputs.
func Env(slot FactSlot, inputs []dintent.Input) expr.Env {
	env := expr.Env{}
	for _, f := range Facts {
		if slices.Contains(f.Slots, slot) {
			put(env, f)
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

// RoundProviders are the providers whose turn.start runs under the round
// caps; a workflow starting turns on any other must set limits.per_day.
var RoundProviders = []unit.ProviderID{unit.GitHub}

// ContextProviders are the providers whose units a context builtin makes up,
// which only an intent of that provider may set: synthetic is a made-up pull
// request.
var ContextProviders = map[string]unit.ProviderID{"synthetic": unit.GitHub}

// PayloadProviders are the template roots a provider's payload alone holds,
// which only an intent of that provider may read.
var PayloadProviders = map[string]unit.ProviderID{"PR": unit.GitHub}

// Slots name each provider's fact slot.
var Slots = map[unit.ProviderID]string{unit.GitHub: "pr", unit.Session: "session", unit.Workspace: "workspace",
	unit.Entry: "entry", dslack.Provider: "slack"}
