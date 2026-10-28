package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/task"
)

// recordsGrammar is the keys of records.toml.
var recordsGrammar = []Key{
	{Path: "records", In: SurfaceRecords, Kind: KindMap, Doc: "The record kinds a run, a person or a plugin writes on a task, by kind."},
	{Path: "records.*", In: SurfaceRecords, Kind: KindTable, Doc: "A record kind, by its name, such as brief, report or decision."},
	{Path: "records.*.schema", In: SurfaceRecords, Kind: KindFile, Required: true, Doc: "The JSON schema a record of the kind follows, a file under records/. Required."},
	{Path: "records.*.writers", In: SurfaceRecords, Kind: KindList, Elem: KindEnum, Values: writerValues, Doc: "Who may write a record of the kind; a record an agent or a plugin wrote is untrusted."},
	{Path: "records.*.projects", In: SurfaceRecords, Kind: KindMap, Doc: "The record's fields that set a property of its task, by field."},
	{Path: "records.*.projects.*", In: SurfaceRecords, Kind: KindText, Doc: "The property, of tasks.toml, the field sets."},
}

func writerValues() []Value {
	doc := map[task.Writer]string{
		task.WriterPerson:   "A person, through a client.",
		task.WriterWorkflow: "A profile workflow's step.",
		task.WriterAgent:    "An agent's turn; untrusted.",
		task.WriterPlugin:   "A source plugin; untrusted.",
	}
	out := make([]Value, len(task.Writers))
	for i, w := range task.Writers {
		out[i] = Value{Name: string(w), Doc: doc[w]}
	}
	return out
}

func actorValues() []Value {
	out := make([]Value, len(task.Actors))
	for i, a := range task.Actors {
		out[i] = Value{Name: string(a)}
	}
	return out
}

// GateModes are the modes of a [gates.approval] table: the draft modes, and
// run for a plan gate's run.
func GateModes() []Value {
	out := DraftModes()
	for _, v := range RunModes() {
		if !slices.ContainsFunc(out, func(o Value) bool { return o.Name == v.Name }) {
			out = append(out, v)
		}
	}
	return out
}

// gateApprovalRows are the [gates.approval] keys of a manifest of surface.
func gateApprovalRows(surface Surface) []Key {
	return []Key{
		{Path: "gates", In: surface, Kind: KindTable, Doc: "The gates its runs and drafts open."},
		{Path: "gates.approval", In: surface, Kind: KindTable, Doc: "The approval of its gates; wait when absent."},
		{Path: "gates.approval.mode", In: surface, Kind: KindEnum, Values: GateModes, Doc: "The mode when no rule holds."},
		{Path: "gates.approval.window", In: surface, Kind: KindDur, Doc: "With mode delay only: how long a person has to stop the gate's consequence."},
		{Path: "gates.approval.rule[]", In: surface, Kind: KindTable, Doc: "A rule; the first whose when holds gives the mode."},
		{Path: "gates.approval.rule[].when", In: surface, Kind: KindPred, Doc: "When the rule applies to a gate."},
		{Path: "gates.approval.rule[].mode", In: surface, Kind: KindEnum, Values: GateModes, Doc: "The rule's mode."},
		{Path: "gates.approval.rule[].window", In: surface, Kind: KindDur, Doc: "With mode delay only: how long a person has to stop the gate's consequence."},
	}
}
