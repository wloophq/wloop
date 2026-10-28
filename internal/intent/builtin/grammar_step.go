package builtin

// stepGrammar is the workflow steps' arguments, each row's Step its step.
var stepGrammar = []Key{
	required(stepKey(StepTurnStart, "intent", KindText, "", "The intent whose turn starts; it must serve the unit. Required.")),
	stepEnum(StepTurnStart, "stage", Stages, "", "The pass over the unit: its first, or one after."),
	stepEnum(StepTurnStart, "runner", TurnRunners, "", "How the turn runs, one of the intent's runners; by default its first."),

	required(stepKey(StepDraftPropose, "purpose", KindText, "", "What the draft is for, such as nudge or close. Required.")),
	stepEnum(StepDraftPropose, "delivery", DraftDeliveries, DraftDeliveries[0], "How the draft posts."),
	stepKey(StepDraftPropose, "text", KindFile, "text.md.tmpl", "The template of the draft's text, beside the manifest."),
	stepKey(StepDraftPropose, "reaction", KindText, "", "The emoji name, with delivery reaction."),
	stepEnum(StepDraftPropose, "items", DraftItems, "all", "Which of the unit's items the draft lists."),
	stepEnum(StepDraftPropose, "per", DraftPer, DraftPer[0], "One draft per unit, or one per person it waits on."),
	stepEnum(StepDraftPropose, "target", DraftTargets, DraftTargets[0], "Where the draft posts: on the unit, or on each item; an intent's help.target is free text."),
	{Path: "effects", In: SurfaceStep, Step: StepDraftPropose, Kind: KindList, Elem: KindEnum, Values: effectValues(AppliesDraft), Doc: "What sending the draft applies."},
	stepKey(StepDraftPropose, "repeat_after", KindText, "", "The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after."),
	stepKey(StepDraftPropose, "repeat_after_sent", KindText, "", "The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever."),

	stepKey(StepTurnDismiss, "reason", KindText, "", "The reason the dismissed turn records."),
	stepKey(StepAnswerRefuse, "reason", KindText, "", "The reason the refused answer records."),

	stepKey(StepClassify, "prompt", KindFile, "prompt.md", "The classification's prompt, beside the manifest."),
	stepKey(StepClassify, "schema", KindFile, "schema.json", "The JSON schema of its answer, beside the manifest."),

	stepKey(StepNotifySelf, "text", KindFile, "text.md.tmpl", "The template of the notification's text, beside the manifest: `.Workflow`, `.Unit` and `.Facts`."),

	required(stepEnum(StepTaskVerb, "verb", TaskStepVerbs, "", "The verb run on the task. Required.")),
	stepKey(StepTaskVerb, "until", KindText, "", "With schedule, the date the task returns: RFC 3339 or YYYY-MM-DD."),
	stepKey(StepTaskVerb, "on_activity", KindText, "", "With schedule, `true` returns the task on new activity on its unit."),
	stepKey(StepTaskVerb, "on", KindText, "", "With waiting, the people the task waits on, comma-separated."),
	stepKey(StepTaskVerb, "resurface", KindText, "", "With waiting, the date the task resurfaces: RFC 3339 or YYYY-MM-DD."),
	stepKey(StepTaskVerb, "set", KindText, "", "With context, the contexts, comma-separated; with area, the area."),

	required(stepKey(StepGateOpen, "name", KindText, "", "The gate, of gates.toml, the step opens. Required.")),

	required(stepKey(StepTaskSet, "prop", KindText, "", "The property, of tasks.toml, the step sets. Required.")),
	required(stepKey(StepTaskSet, "value", KindText, "", "The value, written as its type reads it: an enum's value, a date as YYYY-MM-DD, a duration, a list comma-separated. Required.")),
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

func names(values []string) []Value {
	out := make([]Value, len(values))
	for i, v := range values {
		out[i] = Value{Name: v}
	}
	return out
}
