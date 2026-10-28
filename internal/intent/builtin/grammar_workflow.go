package builtin

import (
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
)

// workflowGrammar is the workflow manifest's keys.
var workflowGrammar = slices.Concat([]Key{
	{Path: "summary", In: SurfaceWorkflow, Kind: KindText, Required: true, Doc: "One line, what the workflow does; its plan items' description. Required."},
	{Path: "provider", In: SurfaceWorkflow, Kind: KindEnum, Values: providerValues, Required: true, Doc: "The provider of the units it matches. Required."},
	{Path: "when", In: SurfaceWorkflow, Kind: KindPred, Required: true, Doc: "The match condition, over the unit's facts, the trigger's and config.*. Required."},
	{Path: "order", In: SurfaceWorkflow, Kind: KindInt, Default: "100", Doc: "Evaluation order, lowest first."},
	{Path: "key", In: SurfaceWorkflow, Kind: KindList, Elem: KindFact, Doc: "The facts forming the idempotency key, by default <slot>.fingerprint: a key that ran or was dismissed is not planned again. A workflow with trigger.on may add trigger.event.id, so each event plans its own run."},
	{Path: "marks", In: SurfaceWorkflow, Kind: KindMap, Doc: "Mark name to fact, stored when a run is done or dismissed, which handled() reads."},
	{Path: "marks.*", In: SurfaceWorkflow, Kind: KindFact, Doc: "The fact whose value the mark stores."},
	{Path: "observe", In: SurfaceWorkflow, Kind: KindBool, Default: "false", Doc: "Plans items pending and never runs them by itself; a person applies an item by hand."},
	{Path: "trigger", In: SurfaceWorkflow, Kind: KindTable, Doc: "What plans the workflow: on or schedule; absent, every sweep."},
	{Path: "trigger.on", In: SurfaceWorkflow, Kind: KindList, Elem: KindText, Doc: "The event kinds the workflow reacts to: the loop's, the plugins', task.woke (a task's wake matched) and verb_<verb>, a verb of tasks.toml run on a task by anyone, a workflow included; the last two with provider task."},
	{Path: "trigger.schedule", In: SurfaceWorkflow, Kind: KindText, Doc: "A cron schedule, in local time."},
	{Path: "trigger.actors", In: SurfaceWorkflow, Kind: KindList, Elem: KindEnum, Values: actorValues, Doc: "With on, the actors whose verb events plan it (person, workflow, agent, plugin); every actor when absent."},
	{Path: "trigger.depth", In: SurfaceWorkflow, Kind: KindInt, Default: "3", Doc: "With on, the longest chain of workflows triggering each other the workflow may close: an event a chain of that many workflows caused plans it no more, and a workflow already in an event's chain never runs on it. 1 to 5."},
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
})

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
