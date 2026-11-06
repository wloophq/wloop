package builtin

import "github.com/wloophq/wloop/internal/domain/items"

// stepGrammar is the workflow steps' arguments, each row's Step its step.
var stepGrammar = []Key{
	required(stepKey(StepTurnStart, "intent", KindText, "", "The intent whose turn starts; it must serve the unit. Required.")),
	stepEnum(StepTurnStart, "stage", Stages, "", "The pass over the unit: its first, or one after."),
	stepEnum(StepTurnStart, "runner", TurnRunners, "", "How the turn runs, one of the intent's runners; by default its first."),
	stepEnum(StepTurnStart, "open_question", TurnOpenQuestion, TurnOpenQuestion[0], "What an open question of the intent's newest turn does to a round of the unit's plan: hold waits for its answer; replace_if_moved runs a round planned on a head other than the one the question was asked on, which replaces the asked turn, supersedes its gate and carries the question in the new turn's history. An answer on its way holds the round either way."),

	required(stepKey(StepDraftPropose, "purpose", KindText, "", "What the draft is for, such as nudge or close. Required.")),
	{Path: "delivery", In: SurfaceStep, Step: StepDraftPropose, Kind: KindEnum, Values: func() []Value { return names(DraftDeliveries) }, Default: DraftDeliveries[0], Doc: "How the draft posts: the core's message or reaction, or a delivery a plugin declares."},
	stepKey(StepDraftPropose, "text", KindFile, "text.md.tmpl", "The template of the draft's text, beside the manifest."),
	stepKey(StepDraftPropose, "reaction", KindText, "", "The emoji name, with delivery reaction."),
	stepEnum(StepDraftPropose, "items", DraftItems, "all", "Which of the unit's items the draft lists."),
	stepEnum(StepDraftPropose, "per", DraftPer, DraftPer[0], "One draft per unit, or one per person it waits on."),
	stepEnum(StepDraftPropose, "target", DraftTargets, DraftTargets[0], "Where the draft posts: on the unit, or on each item; an intent's help.target is free text."),
	{Path: "effects", In: SurfaceStep, Step: StepDraftPropose, Kind: KindList, Elem: KindEnum, Values: effectValues(AppliesDraft), Doc: "What sending the draft applies."},
	stepKey(StepDraftPropose, "repeat_after", KindText, "", "The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after."),
	stepKey(StepDraftPropose, "repeat_after_sent", KindText, "", "The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever."),
	stepEnum(StepDraftPropose, "ended", DraftEnded, DraftEnded[0], "What a draft withdrawn as its task ended does while that task stays ended: hold the workflow's proposals of no task or of that task, or hold them for repeat_after like any settled draft."),

	stepKey(StepTurnDismiss, "reason", KindText, "", "The reason the dismissed turn records."),
	stepKey(StepAnswerRefuse, "reason", KindText, "", "The reason the refused answer records."),

	stepKey(StepClassify, "prompt", KindFile, "prompt.md", "The classification's prompt, beside the manifest."),
	stepKey(StepClassify, "schema", KindFile, "schema.json", "The JSON schema of its answer, beside the manifest."),
	stepKey(StepClassify, "with", KindText, "", "A list fact of task ids, such as task.candidates, whose tasks the prompt reads beside the unit: the prompt is then a template over .Facts, the unit's facts, .Task, the unit's own task when it is one, and .Tasks, the listed tasks in order, each with .ID, .Title, .Notes and .Items, a few of its items' summaries. Every untrusted value is inside this call's markers, .Begin and .End, which carry the code .Nonce. The output is cached by what the prompt reads of the unit's task and the listed tasks (ids, titles, notes, item summaries), so a write that changes none of them, such as opening a task, keeps it; only a reader that names it reads it. An empty list runs nothing."),

	stepKey(StepNotifySelf, "text", KindFile, "text.md.tmpl", "The template of the notification's text, beside the manifest: `.Workflow`, `.Unit` and `.Facts`."),

	required(stepKey(StepTaskVerb, "verb", KindText, "", "The verb of tasks.toml run on the task, one whose by.workflow lets a workflow run it. Required.")),
	stepKey(StepTaskVerb, "until", KindText, "", "With a verb whose handler is schedule, the date the task returns: RFC 3339 or YYYY-MM-DD."),
	stepKey(StepTaskVerb, "on_activity", KindText, "", "With a verb whose handler is schedule, `true` returns the task on new activity on its unit."),
	stepKey(StepTaskVerb, "on", KindText, "", "With a verb whose handler is waiting, the people the task waits on, comma-separated."),
	stepKey(StepTaskVerb, "resurface", KindText, "", "With a verb whose handler is waiting, the date the task resurfaces: RFC 3339 or YYYY-MM-DD."),
	stepKey(StepTaskVerb, "set", KindText, "", "With a verb whose handler is context, the contexts, comma-separated; with area, the area."),
	stepKey(StepTaskVerb, "by", KindText, "", "With a verb whose handler is promise, the time the task is promised by: HH:MM, YYYY-MM-DD or RFC 3339; empty clears it."),
	stepKey(StepTaskVerb, "task", KindText, "", "With a verb whose handler is merge, split, relate or unrelate, the fact that names the other task: a string fact holding its id, or a list fact whose first element does, such as task.link_target_tasks, or a classify output, such as classify.<workflow>.same_as, which no fact table declares before it runs; the step is refused while it names none."),

	required(stepKey(StepGateOpen, "name", KindText, "", "The gate, of gates.toml, the step opens. Required.")),

	stepEnum(StepItemsResolve, "resolution", itemResolutions(), "done", "How the items resolved."),

	required(stepKey(StepTaskSet, "prop", KindText, "", "The property, of tasks.toml, the step sets. Required.")),
	required(stepKey(StepTaskSet, "value", KindText, "", "The value, written as its type reads it: an enum's value, a date as YYYY-MM-DD or as a duration after now such as +2d, a duration, a list comma-separated. Required.")),
}

func required(k Key) Key {
	k.Required = true
	return k
}

func stepKey(step, path string, k Kind, def, doc string) Key {
	return Key{Path: path, In: SurfaceStep, Step: step, Kind: k, Default: def, Doc: doc}
}

func stepEnum(step, path string, values []string, def, doc string) Key {
	k := stepKey(step, path, KindEnum, def, doc)
	k.Values = func() []Value { return names(values) }
	return k
}

func itemResolutions() []string {
	var out []string
	for _, r := range items.Resolutions() {
		out = append(out, string(r))
	}
	return out
}

func names(values []string) []Value {
	out := make([]Value, len(values))
	for i, v := range values {
		out[i] = Value{Name: v}
	}
	return out
}
