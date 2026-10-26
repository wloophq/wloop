package builtin

import (
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// workflowGrammar is the workflow manifest's keys.
var workflowGrammar = slices.Concat([]Key{
	{Path: "summary", In: SurfaceWorkflow, Kind: KindText, Required: true, Doc: "One line, what the workflow does; its plan items' description. Required."},
	{Path: "provider", In: SurfaceWorkflow, Kind: KindEnum, Values: providerValues, Required: true, Doc: "The provider of the units it matches. Required."},
	{Path: "when", In: SurfaceWorkflow, Kind: KindPred, Required: true, Doc: "The match condition, over the unit's facts, the trigger's and config.*. Required."},
	{Path: "order", In: SurfaceWorkflow, Kind: KindInt, Default: "100", Doc: "Evaluation order, lowest first."},
	{Path: "key", In: SurfaceWorkflow, Kind: KindList, Elem: KindFact, Doc: "The facts forming the idempotency key, by default <slot>.fingerprint: a key that ran or was dismissed is not planned again."},
	{Path: "marks", In: SurfaceWorkflow, Kind: KindMap, Doc: "Mark name to fact, stored when a run is done or dismissed, which handled() reads."},
	{Path: "marks.*", In: SurfaceWorkflow, Kind: KindFact, Doc: "The fact whose value the mark stores."},
	{Path: "observe", In: SurfaceWorkflow, Kind: KindBool, Default: "false", Doc: "Plans items pending and never runs them by itself; a person applies an item by hand."},
	{Path: "trigger", In: SurfaceWorkflow, Kind: KindTable, Doc: "What plans the workflow: on or schedule; absent, every sweep."},
	{Path: "trigger.on", In: SurfaceWorkflow, Kind: KindList, Elem: KindEnum, Values: named(inventory.EventKinds...), Doc: "The source event kinds the workflow reacts to."},
	{Path: "trigger.schedule", In: SurfaceWorkflow, Kind: KindText, Doc: "A cron schedule, in local time."},
	{Path: "step[]", In: SurfaceWorkflow, Kind: KindTable, Required: true, Doc: "A step the run takes, in order. Required: at least one."},
	{Path: "step[].use", In: SurfaceWorkflow, Kind: KindEnum, Values: stepValues, Required: true, Doc: "The step."},
	{Path: "step[].*", In: SurfaceWorkflow, Kind: KindAny, Doc: "The step's arguments."},
	{Path: "limits", In: SurfaceWorkflow, Kind: KindTable, Doc: "How many runs the workflow may take."},
	{Path: "limits.per_run", In: SurfaceWorkflow, Kind: KindInt, Default: "0", Doc: "Runs per evaluation; 0 is no limit."},
	{Path: "limits.per_day", In: SurfaceWorkflow, Kind: KindInt, Default: "0", Doc: "Runs per local day; 0 is no limit."},
	{Path: "limits.per_unit_per_day", In: SurfaceWorkflow, Kind: KindInt, Default: "0", Doc: "Runs on one unit per local day; 0 is no limit."},
	{Path: "limits.cooldown", In: SurfaceWorkflow, Kind: KindDur, Doc: "The least time between runs on one unit."},
	{Path: "drafts", In: SurfaceWorkflow, Kind: KindTable, Doc: "The drafts the workflow's steps and turns propose."},
	{Path: "drafts.withdraw", In: SurfaceWorkflow, Kind: KindTable, Doc: "When an open draft the workflow proposed is withdrawn."},
	{Path: "drafts.withdraw.when", In: SurfaceWorkflow, Kind: KindPred, Doc: "When an open draft the workflow proposed is withdrawn, over the unit's facts and the draft's."},
	{Path: "drafts.withdraw.reason", In: SurfaceWorkflow, Kind: KindEnum, Values: withdrawValues, Default: draft.WithdrawMoot, Doc: "The reason the withdrawn draft stores."},
},
	approvalRows("approval", RunModes, string(workflow.ApprovalRun), "The run approval: how a matched item runs.", "the item runs"),
	approvalRows("drafts.approval", DraftModes, string(draft.Wait), "The approval of the drafts the workflow's steps and turns propose.", "the draft is sent"),
)

// approvalRows are an approval table's keys at at: one shape for runs and
// drafts, a mode set per object.
func approvalRows(at string, modes func() []Value, def, doc, what string) []Key {
	return []Key{
		{Path: at, In: SurfaceWorkflow, Kind: KindTable, Doc: doc},
		{Path: at + ".mode", In: SurfaceWorkflow, Kind: KindEnum, Values: modes, Default: def, Doc: "The mode when no rule matches."},
		{Path: at + ".window", In: SurfaceWorkflow, Kind: KindDur, Doc: "How long a person has before " + what + "; only with mode delay, which needs it."},
		{Path: at + ".rule[]", In: SurfaceWorkflow, Kind: KindTable, Doc: "A rule tried in order; the first whose when holds gives the mode."},
		{Path: at + ".rule[].when", In: SurfaceWorkflow, Kind: KindPred, Doc: "When the rule applies."},
		{Path: at + ".rule[].mode", In: SurfaceWorkflow, Kind: KindEnum, Values: modes, Doc: "The mode when the rule applies."},
		{Path: at + ".rule[].window", In: SurfaceWorkflow, Kind: KindDur, Doc: "The rule's window; only with mode delay, which needs it."},
	}
}

// RunModes are the run approval's modes, from workflow.ApprovalModes.
func RunModes() []Value {
	doc := map[workflow.ApprovalMode]string{
		workflow.ApprovalRun:   "Runs at once.",
		workflow.ApprovalDelay: "Runs when the window ends unless a person stops it.",
		workflow.ApprovalWait:  "Waits for a person; a matched wait rule also holds an item a guard refuses.",
	}
	out := make([]Value, len(workflow.ApprovalModes))
	for i, m := range workflow.ApprovalModes {
		out[i] = Value{Name: string(m), Doc: doc[m], Releases: m.Releases()}
	}
	return out
}

// DraftModes are the draft approval's modes, from draft.ApprovalModes.
func DraftModes() []Value {
	doc := map[draft.ApprovalMode]string{
		draft.Send:   "Posts at once.",
		draft.Delay:  "Posts when the window ends unless a person stops it.",
		draft.Wait:   "Waits for a person.",
		draft.DryRun: "Stores the draft and never sends it; nobody can release it.",
	}
	out := make([]Value, len(draft.ApprovalModes))
	for i, m := range draft.ApprovalModes {
		out[i] = Value{Name: string(m), Doc: doc[m], Releases: m.Releases()}
	}
	return out
}

func withdrawValues() []Value {
	return []Value{
		{Name: draft.WithdrawAssigned, Doc: "Its items got an owner."},
		{Name: draft.WithdrawMoot, Doc: "It no longer applies."},
	}
}

func providerValues() []Value {
	var out []Value
	for _, p := range slices.Sorted(maps.Keys(Slots)) {
		out = append(out, Value{Name: string(p), Doc: "Facts under " + Slots[p] + ".*."})
	}
	return out
}

func stepValues() []Value {
	out := make([]Value, len(Steps))
	for i, s := range Steps {
		out[i] = Value{Name: s.Name, Doc: s.Doc}
	}
	return out
}
