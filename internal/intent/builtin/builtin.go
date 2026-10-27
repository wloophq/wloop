// Package builtin is the Go vocabulary manifests name: builtins by slot,
// reserved names and keys, and the facts predicates read. The implementations
// live in internal/cli and internal/daemon and register at wiring.
package builtin

import (
	"maps"
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
	SlotBefore    Slot = "before"
)

// WorkspaceLinkedElseMain runs a turn in the workspace linked to its unit,
// else in the repository's main checkout.
const WorkspaceLinkedElseMain = "linked_else_main"

// The workspaces of a round on someone else's pull request and on one you
// develop.
const (
	WorkspaceReview = "review"
	WorkspaceAuthor = "author"
)

// Names are the builtins a manifest names, by the key that names them.
var Names = map[Slot][]string{
	SlotWorkspace: {WorkspaceReview, WorkspaceAuthor, WorkspaceLinkedElseMain, "given"},
	SlotBefore:    {"take_over"},
}

// nameDocs say what each builtin of Names is.
var nameDocs = map[string]string{
	"review":                "The review workspace, created at the head.",
	"author":                "The workspace that develops the unit.",
	WorkspaceLinkedElseMain: "The workspace linked to the unit, else the repository's main checkout.",
	"given":                 "The workspace the request names.",
	"take_over":             "Takes the unit over into the loop before the turn.",
}

// NameValues are the builtins of slot, with what each is.
func NameValues(slot Slot) []Value {
	out := make([]Value, len(Names[slot]))
	for i, n := range Names[slot] {
		out[i] = Value{Name: n, Doc: nameDocs[n]}
	}
	return out
}

// Boards are the workspaces whose board an intent's turns move: a turn in
// the review or the author workspace moves that board, any other none.
var Boards = []string{"review", "author"}

// BoardOf is the board a turn in workspace moves, "none" for none.
func BoardOf(workspace string) string {
	if slices.Contains(Boards, workspace) {
		return workspace
	}
	return "none"
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

	"association", "base", "kind", "label", "organization", "repo", "reviewer", "risk", "size", "stage", "team", "user",
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
	// ApprovalSlot holds the draft's own facts a [drafts.approval] rule
	// reads besides its unit's.
	ApprovalSlot FactSlot = "approval"
	// NameSlot holds the facts a manifest's session.name reads.
	NameSlot FactSlot = "name"
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
	// no [drafts.approval] rule may read it.
	Untrusted bool
	// TimeVarying says that the fact changes as time passes with nothing
	// else changing, so no workflow key may hold it.
	TimeVarying bool
}

// InputTypes are the types an input declares, with the type its fact has.
var InputTypes = map[string]expr.Type{"bool": expr.Bool, "text": expr.String, "list": expr.List}

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
			if t, ok := InputTypes[in.Type]; ok {
				env["inputs."+in.Name] = t
			}
		}
	}
	return env
}

// RecordStates are the states a manifest's record.states may list.
var RecordStates = []string{string(record.Done), string(record.Settled), string(record.Asked), string(record.Failed)}

// IntentDims are the profile dimensions a manifest's dims may list.
var IntentDims = []string{dintent.DimStage, dintent.DimReviewer}

// Stages are the passes over a unit: the first, and each one after.
var Stages = []string{"first", "follow-up"}

// ReservedFlags are flags the CLI owns; no input may declare them.
var ReservedFlags = []string{"--no-wait", "--no-brief", "--turn", "--help", "--manager", "--parent"}

// RoundProviders are the providers whose plugin keeps a per-unit count of
// turns: every automated turn.start on their units spends
// turns.per_unit_per_day; a workflow starting turns on any other must set
// limits.per_day.
var RoundProviders = []unit.ProviderID{unit.GitHub}

// PayloadProviders are the template roots a provider's payload alone holds,
// which only an intent of that provider may read.
var PayloadProviders = map[string]unit.ProviderID{"PR": unit.GitHub}

// Slots name each provider's fact slot.
var Slots = map[unit.ProviderID]string{unit.GitHub: "pr", unit.Session: "session", unit.Workspace: "workspace",
	unit.Entry: "entry", dslack.Provider: "slack"}

// Providers are the providers an intent may serve: those of Slots, and a
// plugin whose units have no workflow facts adds its own.
var Providers = slices.Sorted(maps.Keys(Slots))
