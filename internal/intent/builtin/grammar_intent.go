package builtin

import (
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
)

// named are values without a doc of their own.
func named(names ...string) func() []Value {
	return func() []Value {
		out := make([]Value, len(names))
		for i, n := range names {
			out[i] = Value{Name: n}
		}
		return out
	}
}

// intentProviderValues are the providers an intent's provider names, and
// any, for every provider.
func intentProviderValues() []Value {
	var out []Value
	for _, p := range Providers {
		v := Value{Name: string(p)}
		if slot, ok := Slots[p]; ok {
			v.Doc = "Facts under " + slot + ".*."
		}
		out = append(out, v)
	}
	return append(out, Value{Name: string(dintent.AnyProvider), Doc: "Every provider's units."})
}

func roleNames() []Value {
	out := make([]Value, len(Roles))
	for i, r := range Roles {
		out[i] = Value{Name: r.Name, Doc: r.Doc}
	}
	return out
}

func effectValues(a Applies) func() []Value {
	return func() []Value {
		var out []Value
		for _, e := range Effects {
			if e.Applies&a != 0 {
				out = append(out, Value{Name: e.Name, Doc: e.Doc})
			}
		}
		return out
	}
}

// intentGrammar is the intent manifest's keys.
var intentGrammar = []Key{
	{Path: "label", In: SurfaceIntent, Kind: KindText, Required: true, Doc: "The name people read. Required."},
	{Path: "summary", In: SurfaceIntent, Kind: KindText, Required: true, Doc: "One line on what it does: its action's tooltip, its help line, its profile listing. Required."},
	{Path: "shortcut", In: SurfaceIntent, Kind: KindText, Doc: "One uppercase letter for its action, not a built-in action's."},
	{Path: "verb", In: SurfaceIntent, Kind: KindEnum, Values: named(IntentVerbs...), Doc: "The verb its action fulfils."},
	{Path: "user_facing", In: SurfaceIntent, Kind: KindBool, Default: "true", Doc: "Offered as a command, a skill and an action."},
	{Path: "skill", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "Install writes it a turn skill; needs help.target."},
	{Path: "role", In: SurfaceIntent, Kind: KindEnum, Values: roleNames, Doc: "What binds it to a plugin's mechanism. One intent per role and provider."},
	{Path: "provider", In: SurfaceIntent, Kind: KindAny, Values: intentProviderValues, Doc: "The units its turns serve: a provider id, a list of them, or any; defaults.toml provider when absent."},
	{Path: "workspace", In: SurfaceIntent, Kind: KindAny, Values: func() []Value { return NameValues(SlotWorkspace) }, Doc: "Where its turn runs, and the board it moves: a workspace builtin, or a table naming the repository."},
	{Path: "workspace.repo", In: SurfaceIntent, Kind: KindText, Doc: "The repository the turn runs in, owner/name."},
	{Path: "workspace.repo_input", In: SurfaceIntent, Kind: KindText, Doc: "The input that holds the repository the turn runs in."},
	{Path: "workspace.repo_optional", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "With repo_input: a turn whose input names no repository runs in the neutral repository, with no checkout."},
	{Path: "workspace.name", In: SurfaceIntent, Kind: KindTemplate, Doc: "The name of the worktree created for the turn's unit, over the name facts, with slug; the loop's own when absent."},
	{Path: "runners", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: named(TurnRunners...), Doc: "How its turns may run; [\"session\"] when absent."},
	{Path: "session", In: SurfaceIntent, Kind: KindTable, Doc: "The agent session its turns run in."},
	{Path: "target", In: SurfaceIntent, Kind: KindTable, Doc: "What a run with no unit does."},
	{Path: "target.missing", In: SurfaceIntent, Kind: KindEnum, Values: named(dintent.TargetCapture), Doc: "capture: a run with no unit captures its guidance as a new task, the turn's unit; refused when absent."},
	{Path: "target.title", In: SurfaceIntent, Kind: KindTemplate, Doc: "The captured task's title, over .guidance, with firstLine."},
	{Path: "session.strategy", In: SurfaceIntent, Kind: KindEnum, Values: named("reuse_live", "new"), Doc: "Whether a turn reuses the unit's live session or starts a new one."},
	{Path: "session.title", In: SurfaceIntent, Kind: KindText, Doc: "The session's title: 1 to 12 lowercase letters."},
	{Path: "session.name", In: SurfaceIntent, Kind: KindTemplate, Doc: "What the workspace manager and the agent show the session as, over the name facts; defaults.toml session_name when absent, else the title. A name that session.title leads and the start time ends, as dd/mm hh:mm, lets the loop recognize its session when it lost track of it."},
	{Path: "session.close_when_done", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "The session closes when its turn is done."},
	{Path: "intake", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "Runs through numbered intake."},
	{Path: "start_line", In: SurfaceIntent, Kind: KindText, Doc: "The intake's first line."},
	{Path: "owner_only", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "The unit must be yours or taken over."},
	{Path: "answerable", In: SurfaceIntent, Kind: KindBool, Default: "true", Doc: "The turn may stop on a question."},
	{Path: "reports", In: SurfaceIntent, Kind: KindBool, Default: "true", Doc: "The turn reports."},
	{Path: "brief", In: SurfaceIntent, Kind: KindTable, Doc: "How the turn reads the unit's brief."},
	{Path: "brief.render", In: SurfaceIntent, Kind: KindBool, Default: "true", Doc: "The instructions carry the brief."},
	{Path: "brief.wait", In: SurfaceIntent, Kind: KindBool, Default: "true", Doc: "The turn waits for the brief."},
	{Path: "brief.need", In: SurfaceIntent, Kind: KindList, Elem: KindText, Doc: "The brief intent's extra fields the turn needs."},
	{Path: "profile", In: SurfaceIntent, Kind: KindText, Doc: "The intent whose judgment blocks the turn reads, or none; the configuration's profile is the profile directory."},
	{Path: "dims", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: named(IntentDims...), Doc: "The dimensions valid under its profile."},
	{Path: "also_profile[]", In: SurfaceIntent, Kind: KindTable, Doc: "Other judgment the turn reads."},
	{Path: "also_profile[].profile", In: SurfaceIntent, Kind: KindText, Doc: "The intent with judgment blocks."},
	{Path: "also_profile[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the turn reads the other judgment."},
	{Path: "also_profile[].dims", In: SurfaceIntent, Kind: KindTable, Doc: "Dimensions it pins, such as { stage = \"first\" }."},
	{Path: "also_profile[].dims.stage", In: SurfaceIntent, Kind: KindEnum, Values: named(Stages...), Doc: "The pinned stage."},
	{Path: "also_profile[].dims.reviewer", In: SurfaceIntent, Kind: KindText, Doc: "The pinned reviewer login."},
	{Path: "also_profile[].define", In: SurfaceIntent, Kind: KindText, Doc: "The template define that renders it."},
	{Path: "also_profile[].prefix", In: SurfaceIntent, Kind: KindText, Doc: "What its sources are listed under; \"<profile> guide: \" when absent."},
	{Path: "effects", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: effectValues(AppliesTurn), Doc: "What sending the turn applies."},
	{Path: "why", In: SurfaceIntent, Kind: KindTemplate, Doc: "Why the turn is due, over the unit's workflow facts."},
	{Path: "refuse[]", In: SurfaceIntent, Kind: KindTable, Doc: "A refusal of turn.start; the first whose when holds refuses with its why."},
	{Path: "refuse[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When it refuses, over the unit's workflow facts."},
	{Path: "refuse[].why", In: SurfaceIntent, Kind: KindTemplate, Doc: "What the refusal says, over the unit's workflow facts."},
	{Path: "drafts", In: SurfaceIntent, Kind: KindTable, Doc: "The drafts its turns' agents propose."},
	{Path: "drafts.allowed_effects", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: effectValues(AppliesDraft), Doc: "The effects its agents may declare on their drafts."},
	{Path: "drafts.attach_posts", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "On a turn whose unit is a task, the unit of each post its agent makes, sent at once or once approved, is attached to the task; reactions and edits are not posts."},
	{Path: "drafts.approval", In: SurfaceIntent, Kind: KindTable, Doc: "The approval of its drafts; wait when absent."},
	{Path: "drafts.approval.mode", In: SurfaceIntent, Kind: KindEnum, Values: DraftModes, Doc: "The approval when no rule holds."},
	{Path: "drafts.approval.window", In: SurfaceIntent, Kind: KindDur, Doc: "With mode delay only: how long a person has to stop the draft."},
	{Path: "drafts.approval.rule[]", In: SurfaceIntent, Kind: KindTable, Doc: "A rule; the first whose when holds gives the mode."},
	{Path: "drafts.approval.rule[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the approval rule applies to a draft."},
	{Path: "drafts.approval.rule[].mode", In: SurfaceIntent, Kind: KindEnum, Values: DraftModes, Doc: "The rule's approval."},
	{Path: "drafts.approval.rule[].window", In: SurfaceIntent, Kind: KindDur, Doc: "With mode delay only: how long a person has to stop the draft."},
	{Path: "guidance", In: SurfaceIntent, Kind: KindTable, Doc: "The guidance a person gives the turn."},
	{Path: "guidance.label", In: SurfaceIntent, Kind: KindText, Default: "Guidance", Doc: "The guidance's label."},
	{Path: "guidance.help", In: SurfaceIntent, Kind: KindText, Doc: "What the guidance is for."},
	{Path: "guidance.max", In: SurfaceIntent, Kind: KindInt, Default: "0", Doc: "The guidance's length cap in runes; 0 for none."},
	{Path: "headline", In: SurfaceIntent, Kind: KindTable, Doc: "The line that opens the turn's prompt."},
	{Path: "headline.resumed", In: SurfaceIntent, Kind: KindTemplate, Doc: "What a resumed turn continues, over the turn's facts."},
	{Path: "headline.new[]", In: SurfaceIntent, Kind: KindTable, Doc: "A headline case; the last has no when."},
	{Path: "headline.new[].when", In: SurfaceIntent, Kind: KindPred, Doc: "When the headline case applies."},
	{Path: "headline.new[].line", In: SurfaceIntent, Kind: KindTemplate, Doc: "The headline, over the turn's facts."},
	{Path: "inputs", In: SurfaceIntent, Kind: KindMap, Doc: "The turn's inputs, by name."},
	{Path: "inputs.*", In: SurfaceIntent, Kind: KindTable, Doc: "An input."},
	{Path: "inputs.*.type", In: SurfaceIntent, Kind: KindEnum, Values: named("bool", "text", "list"), Doc: "The input's type."},
	{Path: "inputs.*.flag", In: SurfaceIntent, Kind: KindText, Doc: "The CLI flag that sets it; required from the user."},
	{Path: "inputs.*.help", In: SurfaceIntent, Kind: KindText, Doc: "What it does; required from the user."},
	{Path: "inputs.*.from", In: SurfaceIntent, Kind: KindEnum, Values: named("user", "prepare"), Default: "user", Doc: "Who sets it."},
	{Path: "inputs.*.persist", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "Stored with the turn, restored on resume."},
	{Path: "inputs.*.hidden", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "Left out of help."},
	{Path: "inputs.*.by_hand_only", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "An automated turn never sets it."},
	{Path: "inputs.*.before", In: SurfaceIntent, Kind: KindEnum, Values: func() []Value { return NameValues(SlotBefore) }, Doc: "What runs before the turn when it is set."},
	{Path: "inputs.*.max", In: SurfaceIntent, Kind: KindInt, Default: "0", Doc: "A text input's length cap in runes; 0 for none."},
	{Path: "inputs.*.surfaces", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: named("cli", "instructions", "try"), Doc: "Where it is offered; cli and instructions when absent."},
	{Path: "help", In: SurfaceIntent, Kind: KindTable, Doc: "How its command presents it."},
	{Path: "help.target", In: SurfaceIntent, Kind: KindText, Doc: "Free text saying what the command's argument names, such as a pull request URL."},
	{Path: "help.summary", In: SurfaceIntent, Kind: KindText, Doc: "The main help's line; the intent's summary when absent."},
	{Path: "help.group", In: SurfaceIntent, Kind: KindText, Doc: "The title of the main help's group that lists the command, free text; action.group is the action's."},
	{Path: "help.examples", In: SurfaceIntent, Kind: KindList, Elem: KindText, Doc: "Command lines without the command's name."},
	{Path: "help.docs", In: SurfaceIntent, Kind: KindText, Doc: "A page path of the documentation site."},
	{Path: "join", In: SurfaceIntent, Kind: KindTable, Doc: "When a request joins an open turn."},
	{Path: "join.differs_on", In: SurfaceIntent, Kind: KindList, Elem: KindText, Doc: "Persisted inputs whose change starts a new turn."},
	{Path: "resume", In: SurfaceIntent, Kind: KindTable, Doc: "What a resumed turn reads."},
	{Path: "resume.changed", In: SurfaceIntent, Kind: KindText, Doc: "The text when the unit changed since the question."},
	{Path: "record", In: SurfaceIntent, Kind: KindTable, Doc: "The turn's record."},
	{Path: "record.when", In: SurfaceIntent, Kind: KindPred, Default: "true", Doc: "When the turn records its outcome."},
	{Path: "record.states", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: named(RecordStates...), Required: true, Doc: "The states a record may take. Required."},
	{Path: "record.artifacts", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: named(record.ArtifactKinds...), Doc: "The artifacts a record may carry."},
	{Path: "record.outcome", In: SurfaceIntent, Kind: KindList, Elem: KindEnum, Values: named(record.OutcomeFields...), Doc: "The outcome fields a record may carry."},
	{Path: "record.attach_url", In: SurfaceIntent, Kind: KindBool, Default: "false", Doc: "On a turn whose unit is a task, a done record's outcome url, when it names a unit such as a pull request, attaches that unit to the task; needs url in record.outcome."},
	{Path: "record.outcome_doc", In: SurfaceIntent, Kind: KindText, Doc: "What the outcome holds, for the instructions."},
	{Path: "record.history_artifact", In: SurfaceIntent, Kind: KindText, Doc: "The artifact the unit's history shows."},
	{Path: "record.require[]", In: SurfaceIntent, Kind: KindTable, Doc: "What a record in a state must carry; record.requires is the record kind a run owes."},
	{Path: "record.require[].state", In: SurfaceIntent, Kind: KindText, Doc: "The state."},
	{Path: "record.require[].artifact", In: SurfaceIntent, Kind: KindText, Doc: "The artifact it must carry."},
	{Path: "record.require[].fields", In: SurfaceIntent, Kind: KindList, Elem: KindText, Doc: "The outcome fields it must carry."},
	{Path: "record.requires", In: SurfaceIntent, Kind: KindText, Doc: "The record kind, of records.toml, a run of the intent owes; a run that ends without it opens the gate missing names. Left out, a run owes no record. Not record.require, what a record in a state carries."},
	{Path: "record.missing", In: SurfaceIntent, Kind: KindText, Doc: "The gate, of gates.toml, a run that ends without the record requires names opens."},
	{Path: "builds", In: SurfaceIntent, Kind: KindEnum, Values: named(Builds...), Doc: "A builder, never a turn."},
	{Path: "prompt", In: SurfaceIntent, Kind: KindFile, Doc: "A builder's prompt file, through the profile layers."},
	{Path: "schema", In: SurfaceIntent, Kind: KindFile, Doc: "A builder's extra fields' schema file, beside the manifest."},
}
