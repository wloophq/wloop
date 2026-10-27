---
title: Profile language
description: Every reserved word of the profile files and the configuration, one line each.
---

Every key a profile file or `config.json` may set, with its kind, its values and its default. A key or a value not listed here fails to load, with a close listed one as a suggestion when one exists. `<name>` is a name the author chooses, and `[]` marks an array of tables (`[[step]]`). Predicates and templates read the [facts](/reference/facts/); the manifests are detailed in the [intent manifest](/reference/intent-manifest/), [workflow manifest](/reference/workflow-manifest/) and [configuration](/reference/configuration/) pages.

## Intent manifest (`intents/<name>/intent.toml`)

### Identity and help

- `label` (string): The name people read. Required.
- `summary` (string): One line on what it does: its action's tooltip, its help line, its profile listing. Required.
- `shortcut` (string): One uppercase letter for its action, not a built-in action's.
- `verb` (one of `approve`, `delegate`, `dismiss`): The GTD verb its action fulfils.
- `user_facing` (bool, default `true`): Offered as a command, a skill and an action.
- `skill` (bool, default `false`): Install writes it a turn skill; needs help.target.
- `help` (table): How its command presents it.
- `help.target` (string): Free text saying what the command's argument names, such as a pull request URL.
- `help.summary` (string): The main help's line; the intent's summary when absent.
- `help.group` (string): The title of the main help's group that lists the command, free text; action.group is the action's.
- `help.examples` (list of strings): Command lines without the command's name.
- `help.docs` (string): A page path of the documentation site.

### Binding

- `role` (one of `pr.review`, `pr.address`, `pr.nudge`, `probe`, `headless`): What binds it to a plugin's mechanism. One intent per role and provider.
  - `pr.review`: The review round on someone else's pull request, in its review workspace: first or follow-up, reconciled with the review you published; it drives its workspace's row.
  - `pr.address`: The address round on a pull request you develop, in its author workspace: its reviews, failing checks and conflicts; its instructions say why it is due; it drives its workspace's row.
  - `pr.nudge`: The nudge on a pull request: nudge.prepare fills its inputs from who the pull request waits on, or refuses it.
  - `probe`: The probe of an agent home: a synthetic pull request in the workspace the request names, under the probe's policy.
  - `headless`: The intent a headless turn on a unit runs when its request names none.
- `provider` (one of `entry`, `github`, `session`, `slack`, `workspace`, `any`, or a list of them): The units its turns serve: a provider id, a list of them, or any; defaults.toml provider when absent.
  - `entry`: Facts under entry.*.
  - `github`: Facts under pr.*.
  - `session`: Facts under session.*.
  - `slack`: Facts under slack.*.
  - `workspace`: Facts under workspace.*.
  - `any`: Every provider's units.
- `builds` (one of `brief`): A builder, never a turn.
- `prompt` (file): A builder's prompt file, through the profile layers.
- `schema` (file): A builder's extra fields' schema file, beside the manifest.

### Where it runs

- `workspace` (one of `review`, `author`, `linked_else_main`, `given`, or a table): Where its turn runs, and the board it moves: a workspace builtin, or a table naming the repository.
  - `review`: The review workspace, created at the head.
  - `author`: The workspace that develops the unit.
  - `linked_else_main`: The workspace linked to the unit, else the repository's main checkout.
  - `given`: The workspace the request names.
- `workspace.repo` (string): The repository the turn runs in, owner/name.
- `workspace.repo_input` (string): The input that holds the repository the turn runs in.
- `runners` (list of `session`, `headless`): How its turns may run; ["session"] when absent.
- `session` (table): The agent session its turns run in.
- `session.strategy` (one of `reuse_live`, `new`): Whether a turn reuses the unit's live session or starts a new one.
- `session.title` (string): The session's title: 1 to 12 lowercase letters.
- `session.close_when_done` (bool, default `false`): The session closes when its turn is done.

### How a turn runs

- `intake` (bool, default `false`): Runs through numbered intake.
- `owner_only` (bool, default `false`): The unit must be yours or taken over.
- `join` (table): When a request joins an open turn.
- `join.differs_on` (list of strings): Persisted inputs whose change starts a new turn.
- `answerable` (bool, default `true`): The turn may stop on a question.
- `reports` (bool, default `true`): The turn reports.

### Inputs

- `inputs` (table by name): The turn's inputs, by name.
- `inputs.<name>` (table): An input.
- `inputs.<name>.type` (one of `bool`, `text`, `list`): The input's type.
- `inputs.<name>.flag` (string): The CLI flag that sets it; required from the user.
- `inputs.<name>.help` (string): What it does; required from the user.
- `inputs.<name>.from` (one of `user`, `prepare`, default `user`): Who sets it.
- `inputs.<name>.persist` (bool, default `false`): Stored with the turn, restored on resume.
- `inputs.<name>.hidden` (bool, default `false`): Left out of help.
- `inputs.<name>.by_hand_only` (bool, default `false`): An automated turn never sets it.
- `inputs.<name>.before` (one of `take_over`): What runs before the turn when it is set.
  - `take_over`: Takes the unit over into the loop before the turn.
- `inputs.<name>.max` (integer, default `0`): A text input's length cap in runes; 0 for none.
- `inputs.<name>.surfaces` (list of `cli`, `instructions`, `try`): Where it is offered; cli and instructions when absent.

### When a turn is due or refused

- `why` (template): Why the turn is due, over the unit's workflow facts.
- `refuse[]` (table): A refusal of turn.start; the first whose when holds refuses with its why.
- `refuse[].when` (predicate): When it refuses, over the unit's workflow facts.
- `refuse[].why` (template): What the refusal says, over the unit's workflow facts.

### Its action on a row

- `action` (table): How the intent's action is offered on a row; its description is the intent's summary.
- `action.group` (one of `start`, `turn`, `control`, `workspace`, `brief`, `intake`, default `start`): The group the action is listed under.
- `action.surfaces` (list of `row`, `detail`, `tui`, `menubar`, `notification`, `cli`, `explore`): Where the action is offered: row, detail, tui, cli and explore when absent.
- `action.when` (predicate, default `row.open`): When the action is offered on a row.
- `action.primary` (table): When the action is the row's primary one.
- `action.primary.when` (predicate, default `false`): When the action is the row's primary one.
- `action.refuse[]` (table): A case that offers the action disabled; the first that holds says why.
- `action.refuse[].when` (predicate): When the action is offered disabled.
- `action.refuse[].why` (template): Why the action is disabled, over the action facts.
- `action.note` (template): What the action would do on this row, over the action facts.
- `action.warning` (template): Shown under the action, over the action facts; empty shows nothing.
- `action.confirm` (template): The dialog that confirms the action, over the action facts; empty asks nothing.

### What the prompt opens with

- `headline` (table): The line that opens the turn's prompt.
- `headline.resumed` (template): What a resumed turn continues, over the turn's facts.
- `headline.new[]` (table): A headline case; the last has no when.
- `headline.new[].when` (predicate): When the headline case applies.
- `headline.new[].line` (template): The headline, over the turn's facts.
- `start_line` (string): The intake's first line.
- `resume` (table): What a resumed turn reads.
- `resume.changed` (string): The text when the unit changed since the question.

### Guidance and brief

- `guidance` (table): The guidance a person gives the turn.
- `guidance.label` (string, default `Guidance`): The guidance's label.
- `guidance.help` (string): What the guidance is for.
- `guidance.max` (integer, default `0`): The guidance's length cap in runes; 0 for none.
- `brief` (table): How the turn reads the unit's brief.
- `brief.render` (bool, default `true`): The instructions carry the brief.
- `brief.wait` (bool, default `true`): The turn waits for the brief.
- `brief.need` (list of strings): The brief intent's extra fields the turn needs.

### Judgment

- `profile` (string): The intent whose judgment blocks the turn reads, or none; the configuration's profile is the profile directory.
- `dims` (list of `stage`, `reviewer`): The dimensions valid under its profile.
- `also_profile[]` (table): Other judgment the turn reads.
- `also_profile[].profile` (string): The intent with judgment blocks.
- `also_profile[].when` (predicate): When the turn reads the other judgment.
- `also_profile[].dims` (table): Dimensions it pins, such as { stage = "first" }.
- `also_profile[].dims.stage` (one of `first`, `follow-up`): The pinned stage.
- `also_profile[].dims.reviewer` (string): The pinned reviewer login.
- `also_profile[].define` (string): The template define that renders it.
- `also_profile[].prefix` (string): What its sources are listed under; "&lt;profile&gt; guide: " when absent.

### What it records

- `record` (table): The turn's record.
- `record.when` (predicate, default `true`): When the turn records its outcome.
- `record.states` (list of `done`, `settled`, `asked`, `failed`): The states a record may take. Required.
- `record.artifacts` (list of `review-draft`, `triage`): The artifacts a record may carry.
- `record.outcome` (list of `verdict`, `url`): The outcome fields a record may carry.
- `record.outcome_doc` (string): What the outcome holds, for the instructions.
- `record.history_artifact` (string): The artifact the unit's history shows.
- `record.require[]` (table): What a record in a state must carry.
- `record.require[].state` (string): The state.
- `record.require[].artifact` (string): The artifact it must carry.
- `record.require[].fields` (list of strings): The outcome fields it must carry.

### Effects and drafts

- `effects` (list of `plan.cleared`, `guidance.handed`, `pr.round_sent`, `pr.reviewed`, `pr.addressed`, `pr.nudged`): What sending the turn applies.
  - `plan.cleared`: Drops the unit's pending plan items.
  - `guidance.handed`: Hands the guidance queued on the pull request to the turn, and queues it back when the turn is not sent.
  - `pr.round_sent`: Records the round-sent event on the pull request.
  - `pr.reviewed`: Writes the plugin mark reviewed: the reviews the round covered, once its review is submitted.
  - `pr.addressed`: Writes the plugin marks addressed.*: the reviews, checks and conflict the address round covered.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, or as the GitHub publisher posts its draft.
- `drafts` (table): The drafts its turns' agents propose.
- `drafts.allowed_effects` (list of `entry.nudged`, `unit.closed`, `pr.nudged`): The effects its agents may declare on their drafts.
  - `entry.nudged`: Stamps the last nudge of the draft's entry, or of every live waiting entry of its unit when it names none.
  - `unit.closed`: Records the closure of the draft's unit, which ends its close entry and unwatches it after its warm period; through the unit's plugin.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, or as the GitHub publisher posts its draft.
- `drafts.approval` (an [approval table](#approval-tables) of draft modes): The approval of its drafts; wait when absent.

## Built-in actions (`actions.toml`)

- `<name>` (table): A built-in action, by its id.
- `<name>.label` (string): The name people read. Required.
- `<name>.summary` (string): One line, what pressing it does: its tooltip and help line. Required.
- `<name>.note` (template): What the action would do on this row, over the action facts.
- `<name>.warning` (template): Shown under the action, over the action facts; empty shows nothing.
- `<name>.confirm` (template): The dialog that confirms the action, over the action facts; empty asks nothing.
- `<name>.why` (template): Why the action is disabled, over the action facts.
- `<name>.input` (table): The text the action asks for.
- `<name>.input.label` (string): The input's name.
- `<name>.input.help` (string): What to write, shown in the empty input.

## GTD verbs and views (`gtd.toml`)

- `verbs` (table): The verbs an entry offers, by verb.
- `verbs.<name>` (table): A verb, by its name.
- `verbs.<name>.label` (string): The name people read on its button. Required.
- `verbs.<name>.summary` (string): One line, what pressing it does: its tooltip. Required.
- `views` (table): The lists of entries, by view.
- `views.<name>` (table): A view, by its name.
- `views.<name>.label` (string): The name people read in the list of views. Required.

## Workflow manifest (`workflows/<name>/workflow.toml`)

### What it matches

- `summary` (string): One line, what the workflow does; its plan items' description. Required.
- `provider` (one of `entry`, `github`, `session`, `slack`, `workspace`): The provider of the units it matches. Required.
  - `entry`: Facts under entry.*.
  - `github`: Facts under pr.*.
  - `session`: Facts under session.*.
  - `slack`: Facts under slack.*.
  - `workspace`: Facts under workspace.*.
- `when` (predicate): The match condition, over the unit's facts, the trigger's and config.*. Required.
- `order` (integer, default `100`): Evaluation order, lowest first.
- `key` (list of facts): The facts forming the idempotency key, by default &lt;slot&gt;.fingerprint: a key that ran or was dismissed is not planned again. A workflow with trigger.on may add trigger.event.id, so each event plans its own run.
- `marks` (table by name): Mark name to fact, stored when a run is done or dismissed, which handled() reads.
- `marks.<name>` (fact): The fact whose value the mark stores.

### When it is planned

- `trigger` (table): What plans the workflow: on or schedule; absent, every sweep.
- `trigger.on` (list of `opened`, `push`, `review_requested`, `review_request_removed`, `review`, `comment`, `check`, `ready`, `draft`, `merged`, `closed`, `round_sent`, `round_held`, `round_pending`, `round_skipped`, `guidance`, `round_blocked`, `round_unblocked`, `muted`, `unmuted`, `held`, `released`, `adopted`, `tracked`, `untracked`, `nudged`, `removed`, `status`, `would_review`, `would_address`, `review_ended`, `verb_next`, `verb_waiting`, `verb_schedule`, `verb_someday`, `verb_reference`, `verb_focus`, `verb_project`, `verb_approve`, `verb_delegate`, `verb_done`, `verb_dismiss`, `verb_context`, `verb_area`, `verb_answer`, `verb_reviewed`, `verb_sequential`, `verb_reply`): The source event kinds the workflow reacts to; verb_&lt;verb&gt;, a verb a person ran on an entry, with provider entry.
- `trigger.schedule` (string): A cron schedule, in local time.
- `observe` (bool, default `false`): Plans items pending and never runs them by itself; a person applies an item by hand.
- `limits` (table): How many runs the workflow may take.
- `limits.per_run` (integer, default `0`): Runs per evaluation; 0 is no limit.
- `limits.per_day` (integer, default `0`): Runs per local day; 0 is no limit.
- `limits.per_unit_per_day` (integer, default `0`): Runs on one unit per local day; 0 is no limit.
- `limits.cooldown` (duration): The least time between runs on one unit.

### Steps

- `step[]` (table): A step the run takes, in order. Required: at least one.
- `step[].use` (one of `session.close`, `workspace.remove`, `workspace.unregister`, `turn.dismiss`, `answer.refuse`, `session.mark_dormant`, `turn.start`, `draft.propose`, `classify`, `notify.self`): The step.
  - `session.close`: Closes the agent session.
  - `workspace.remove`: Removes the workspace, after its guards. Never with `force`: a manifest that sets it fails to load.
  - `workspace.unregister`: Unregisters a workspace whose path is gone.
  - `turn.dismiss`: Dismisses the open turn, recording the reason.
  - `answer.refuse`: Refuses a queued answer whose route is gone.
  - `session.mark_dormant`: Marks dormant an active session whose process its manager's census no longer lists: the session stays open, and the next turn wakes it.
  - `turn.start`: Starts a turn of `intent` on the unit, which the intent must serve; `stage` (`first` or `follow-up`) for a review; `runner` (`session` or `headless`), by default the intent's first runner. On a pull request, caps, the brief, busy and held checks apply as for any automatic round; on any other unit, the workflow's `limits.per_day` and `turns.per_day`.
  - `draft.propose`: Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`unit` or `person`), `target` (`unit` or `item`), `effects` (the effects sending it applies), `repeat_after` (the least time after a settled draft of the same person, a duration or a duration of the config such as `config.drafts.repeat_after`) and `repeat_after_sent` (the same after a sent one, or `forever`). Holds while a draft of the workflow, unit and person is open or failed; a run that leaves a person under a delay that ends with time stays pending until it ends.
  - `classify`: A headless classification whose output becomes facts: `prompt` (a file in the workflow's directory, by default `prompt.md`) and `schema` (the JSON schema its answer follows, by default `schema.json`). Event triggers only, cached by fingerprint, under `classify.per_day`.
  - `notify.self`: A private notification: `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Workflow`, `.Unit` and `.Facts`, the unit's facts by name, such as `{{index .Facts "pr.slug"}}`, none once the unit is gone).
- `step[].<name>` (string or list): The step's arguments.

### Run approval

- `approval` (an [approval table](#approval-tables) of run modes, default mode `run`): The run approval: how a matched item runs.

### Drafts

- `drafts` (table): The drafts the workflow's steps and turns propose.
- `drafts.withdraw` (table): When an open draft the workflow proposed is withdrawn.
- `drafts.withdraw.when` (predicate): When an open draft the workflow proposed is withdrawn, over the unit's facts and the draft's.
- `drafts.withdraw.reason` (one of `assigned`, `moot`, default `moot`): The reason the withdrawn draft stores.
  - `assigned`: Its items got an owner.
  - `moot`: It no longer applies.
- `drafts.approval` (an [approval table](#approval-tables) of draft modes, default mode `wait`): The approval of the drafts the workflow's steps and turns propose.

## Steps and their arguments

- `session.close`: no arguments.
- `workspace.remove`: no arguments.
- `workspace.unregister`: no arguments.
- `turn.dismiss`:
  - `reason` (string): The reason the dismissed turn records.
- `answer.refuse`:
  - `reason` (string): The reason the refused answer records.
- `session.mark_dormant`: no arguments.
- `turn.start`:
  - `intent` (string): The intent whose turn starts; it must serve the unit. Required.
  - `stage` (one of `first`, `follow-up`): The pass over the unit: its first, or one after.
  - `runner` (one of `session`, `headless`): How the turn runs, one of the intent's runners; by default its first.
- `draft.propose`:
  - `purpose` (string): What the draft is for, such as nudge or close. Required.
  - `delivery` (one of `message`, `reaction`, `comment`, default `message`): How the draft posts.
  - `text` (file, default `text.md.tmpl`): The template of the draft's text, beside the manifest.
  - `reaction` (string): The emoji name, with delivery reaction.
  - `items` (one of `owed`, `resolved`, `unowned`, `unowned_aged`, `all`, default `all`): Which of the unit's items the draft lists.
  - `per` (one of `unit`, `person`, default `unit`): One draft per unit, or one per person it waits on.
  - `target` (one of `unit`, `item`, default `unit`): Where the draft posts: on the unit, or on each item; an intent's help.target is free text.
  - `effects` (list of `entry.nudged`, `unit.closed`, `pr.nudged`): What sending the draft applies.
  - `entry.nudged`: Stamps the last nudge of the draft's entry, or of every live waiting entry of its unit when it names none.
  - `unit.closed`: Records the closure of the draft's unit, which ends its close entry and unwatches it after its warm period; through the unit's plugin.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, or as the GitHub publisher posts its draft.
  - `repeat_after` (string): The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after.
  - `repeat_after_sent` (string): The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever.
- `classify`:
  - `prompt` (file, default `prompt.md`): The classification's prompt, beside the manifest.
  - `schema` (file, default `schema.json`): The JSON schema of its answer, beside the manifest.
- `notify.self`:
  - `text` (file, default `text.md.tmpl`): The template of the notification's text, beside the manifest: `.Workflow`, `.Unit` and `.Facts`.

## Defaults (`defaults.toml`)

- `provider` (one of `entry`, `github`, `session`, `slack`, `workspace`, `any`): The provider of an intent whose manifest names none.
  - `entry`: Facts under entry.*.
  - `github`: Facts under pr.*.
  - `session`: Facts under session.*.
  - `slack`: Facts under slack.*.
  - `workspace`: Facts under workspace.*.
  - `any`: Every provider's units.
- `refusal` (string): What a [[refuse]] case whose when holds says when its why renders empty.

## Block headings (`labels.toml`)

- `other` (string): The heading of a dimension dims does not name: {Dim} and {value}.
- `tag` (string): The heading of a tag condition: {key} and {value}.
- `dims` (table by name): A dimension's heading, by dimension: {value}.
- `dims.<name>` (string): The dimension's heading.
- `values` (table by name): The heading of one condition, by "&lt;dim&gt;=&lt;value&gt;".
- `values.<name>` (string): The condition's heading.

## MCP tool descriptions (`tools.toml`)

- `<name>` (table): An MCP tool, by its name.
  - `turn_end`: Records how the session's turn ended.
  - `inbox_post`: Posts a question or a note to the user's Inbox during a turn.
  - `slack_send_message`: Posts to Slack as the user, now.
  - `slack_read`: Reads a Slack thread or conversation.
  - `slack_search`: Searches the Slack messages the user can read.
  - `slack_draft`: Proposes a Slack message or reaction for the user's approval.
  - `slack_react`: Adds or removes a reaction on a Slack message as the user, now.
  - `slack_edit`: Replaces the text of one of the user's Slack messages, now.
- `<name>.description` (template): What the tool does and when to call it, over the tool data. Required.
- `<name>.params` (table by name): A parameter's description, by its path: a nested one as "record.state", an array's element as "items[]".
- `<name>.params.<name>` (template): The parameter's description, over the tool data.
- `<name>.answers` (table by name): What the tool answers the agent, by answer name; the reference lists each tool's answers and the fields they read.
- `<name>.answers.<name>` (template): The answer, over the tool data and its fields.

Each tool's answers, under `[<tool>.answers]`, and the fields each reads besides `Name`, `Command` and `PostsPerTurn`:

- `turn_end`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `problem_extra_arguments` (`Keys`): A problem: arguments besides turn and record.
  - `problem_turn_type`: A problem: turn is not a string.
  - `problem_turn_required`: A problem: turn is missing or empty.
  - `problem_record_required`: A problem: record is missing.
  - `problem_record` (`Error`): A problem: record does not decode.
  - `refused_record` (`Reason`): The turn refused the record.
  - `store_failed` (`Error`): The record could not be stored.
  - `recorded` (`State`, `Unit`, `Note`, `Dropped`, `Ledger`): The record is stored: its state and unit, a note and what was dropped when any, and the ledger's outcome, "" when none.
  - `ledger_applied` (`Count`: number): A part of the ledger's outcome: ops applied.
  - `ledger_unchanged` (`Count`: number): A part of the ledger's outcome: ops that changed nothing.
  - `ledger_proposed` (`Count`: number): A part of the ledger's outcome: ops proposed to the user.
  - `ledger_refused` (`Count`: number, `Why`): A part of the ledger's outcome: ops refused, and why.
  - `ledger_refusal` (`Index`: number, `Reason`): Why one op was refused.
- `inbox_post`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `text_required`: The text is missing.
  - `capped` (`Error`): The turn has too many messages waiting.
  - `not_in_turn` (`Error`): The session has no started turn.
  - `post_failed` (`Error`): The message could not be posted.
  - `posted` (`ID`: number): The message is in the Inbox.
- `slack_send_message`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_target_required`: A problem: target is missing.
  - `problem_text_required`: A problem: text is missing or empty.
  - `broadcast`: The text notifies a whole channel or group.
  - `posted` (`Conversation`, `Parent`, `Permalink`, `Channel`, `TS`, `Text`): The message is posted: where, its permalink, "" when Slack gave none, and its text as posted.
- `slack_read`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_target_required`: A problem: target is missing.
  - `problem_limit` (`Max`: number): A problem: limit is out of range.
- `slack_search`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_query_required`: A problem: query is missing or empty.
  - `problem_after`: A problem: after is not a date.
  - `problem_before`: A problem: before is not a date.
  - `problem_before_after`: A problem: before is not after after.
  - `problem_limit` (`Max`: number): A problem: limit is out of range.
- `slack_draft`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_kind` (`Kinds`): A problem: kind is none of the kinds.
  - `problem_reaction_required`: A problem: a react draft has no reaction.
  - `problem_text_required`: A problem: text is missing or empty.
  - `proposed` (`ID`: number, `Unit`, `Where`): The draft waits for the user.
  - `approved` (`ID`: number, `Unit`, `Where`): The draft's policy approved it.
  - `dry_run` (`ID`: number, `Unit`, `Where`): The draft's policy only stores it.
- `slack_react`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_permalink`: A problem: permalink is not a message permalink.
  - `problem_emoji_required`: A problem: emoji is missing or empty.
  - `reacted` (`Emoji`, `Conversation`, `Permalink`): The reaction is added.
  - `removed` (`Emoji`, `Conversation`, `Permalink`): The reaction is removed.
  - `already` (`Emoji`, `Conversation`, `Permalink`): The message already had the user's reaction.
  - `absent` (`Emoji`, `Conversation`, `Permalink`): The message had no such reaction of the user.
- `slack_edit`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_permalink`: A problem: permalink is not a message permalink.
  - `problem_text_required`: A problem: text is missing or empty.
  - `broadcast`: The text notifies a whole channel or group.
  - `edited` (`Conversation`, `Permalink`, `Text`, `Replaced`): The message is edited: where, its new text as posted and the text it replaced.

## Judgment block front matter (`+++` in `<dim>=<value>/.../intent=<name>.md`)

- `override` (bool, default `false`): Drops the blocks before it whose path is an ancestor of its own.
- `title` (string): Replaces the block's heading.
- `include` (list of files): Up to eight .md or .txt files of the profile the block inlines, relative to it.
- `paths` (list of strings): The block applies only when a changed file matches one of these globs.
- `delegates` (table by name): Per agent, skills the finding step runs besides the procedure's own work.
- `delegates.<name>` (list of strings): The agent's skills.

## Configuration (`config.json`)

### Agent and workspaces

- `profile` (string): The user profile layered over the bundled one: a name under the profiles directory, or a path; an intent's profile names an intent.
- `agent` (string): The agent the sessions run.
- `agent_home` (string): The agent's own configuration directory; empty is the agent's default.
- `agent_models` (table by name): Per agent, a model name for each model tier.
- `agent_models.<name>` (table by name): One agent's models by tier.
- `agent_models.<name>.<name>` (string): The agent's model name for the tier.
- `projects_dir` (string): Where review clones a repository the workspace manager does not know yet.
- `workspace_managers` (list of strings): The enabled workspace managers, in the order a new worktree picks them.
- `intents` (table by name): Each intent's settings, by intent name.
- `intents.<name>` (table): One intent's settings.
- `intents.<name>.project_settings` (table): Where the intent's sessions load the checkout's own agent settings.
- `intents.<name>.project_settings.when` (predicate): Where the intent's sessions load the checkout's settings; elsewhere they start without them.

### Projects

- `projects[]` (table): Settings for the pull requests of matching repositories; the first match wins.
- `projects[].match` (string): An owner/repo pattern, such as acme/* or acme/api.
- `projects[].projects_dir` (string): The project's projects_dir.
- `projects[].agent` (string): The agent the project's sessions run.
- `projects[].agent_home` (string): The project's agent_home.
- `projects[].workspace_managers` (list of strings): The managers a new worktree of the project picks from, in order.
- `projects[].github` (table): The GitHub settings the project overrides.
- `projects[].github.trusted_authors` (list of strings): The project's github.trusted_authors.
- `projects[].github.followups_without_workspace` (bool): The project's github.followups_without_workspace.
- `projects[].github.address_without_workspace` (bool): The project's github.address_without_workspace.

### Placement

- `placement[]` (table): The rules that place units; the first match wins.
- `placement[].match` (string): The units the rule places.
- `placement[].repo` (string): The repository their work checks out.
- `placement[].checkout` (string): The branch their work checks out.
- `placement[].role` (one of `review`, `author`, `work`): The role of their workspace.
- `placement[].shared` (bool): The units share one workspace.

### GitHub

- `github` (table): The GitHub plugin's settings.
- `github.enabled` (bool): Switches the GitHub plugin; left out, it is on.
- `github.review_prefix` (string, default `REVIEW `): The prefix of a review workspace's name.
- `github.automated_reviewers` (list of strings): Logins of review bots whose reviews count as automated.
- `github.owners` (list of strings): The GitHub owners automated follow-ups run on; empty is every owner the workspace manager knows.
- `github.trusted_authors` (list of strings): Logins whose pull requests pass the author association check of automated rounds.
- `github.followups_without_workspace` (bool, default `false`): A follow-up review recreates the review workspace the user deleted; off, the sweep only logs it.
- `github.address_without_workspace` (bool, default `false`): An address round creates its author workspace when it has none; off, the sweep only records it.
- `github.rate_limit_fraction` (number, default `0.5`): The share of the GitHub rate limit the sweeps may spend, a number between 0 and 1.
- `github.drafts` (table): The drafts the plugin's own rules propose.
- `github.drafts.approval` (table): Their approval; left out, each waits for you.
- `github.drafts.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`): The approval mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
- `github.drafts.approval.window` (duration): How long a delay waits before the draft goes; only with mode delay.

### GitHub address rounds

- `github.address_reviews` (bool, default `true`): Someone's review of a pull request you develop starts an address round.
- `github.address_skip_reviewers` (list of strings): Logins whose reviews never start an address round.
- `github.address_checks` (bool, default `true`): Failing checks on the head of a pull request you develop start an address round.
- `github.ignore_checks` (list of strings): Checks whose failure never starts an address round.
- `github.address_conflicts` (bool, default `true`): A pull request you develop that conflicts with its base past the conflict window starts an address round.

### GitHub scope, stalls and nudges

- `github.scope` (table): Whose pull requests the GitHub plugin follows.
- `github.scope.people` (list of strings): Logins whose pull requests it follows.
- `github.scope.teams` (list of strings): Teams whose pull requests it follows.
- `github.scope.repos` (list of strings): Repositories whose pull requests it follows.
- `github.scope.watch` (list of strings): Repositories it watches.
- `github.stalled` (table): When a pull request counts as stalled.
- `github.stalled.review` (duration, default `2d`): Waiting on a review for this long.
- `github.stalled.reply` (duration, default `1d`): Waiting on a reply for this long.
- `github.stalled.merge` (duration, default `1d`): Approved and unmerged for this long.
- `github.nudge` (table): The nudges on pull requests.
- `github.nudge.silent_days` (integer, default `30`): Calendar days of silence on a pull request before a nudge is due; workflows read it as config.github.nudge.silent_days.

### Slack

- `slack` (table): The Slack plugin's settings; no secret lives here.
- `slack.enabled` (bool): Switches the Slack plugin; left out, it is on.
- `slack.client_id` (string): The Slack app's client id; empty leaves Slack unconfigured.
- `slack.redirect_port` (integer): The login callback port registered in the Slack app; 0 is 53682.
- `slack.keychain_service` (string): The name of the Keychain items; empty is the brand's.
- `slack.post_allowlist` (list of strings): When not empty, the only channel ids, or user ids naming the DM with that user, a post may go to.
- `slack.request_log` (bool, default `false`): Logs one line per Slack call: method and HTTP status.
- `slack.turns` (table): What a turn on a Slack unit reads.
- `slack.turns.max_chars` (integer): The characters of the unit's messages rendered into the instructions; 0 is 30000.
- `slack.drafts` (table): The drafts the plugin's own rules propose.
- `slack.drafts.approval` (table): Their approval; left out, each waits for you.
- `slack.drafts.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`): The approval mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
- `slack.drafts.approval.window` (duration): How long a delay waits before the draft goes; only with mode delay.

### Slack ingest

- `slack.ingest` (table): What the Slack plugin admits and how it listens.
- `slack.ingest.terms` (list of strings): A message that holds one of them as a word, in any case, is admitted.
- `slack.ingest.allow_apps` (list of strings): Bot ids (B...) or app ids (A...) whose messages are admitted.
- `slack.ingest.deny_apps` (list of strings): Bot ids (B...) or app ids (A...) whose messages are not.
- `slack.ingest.socket` (bool): Listens on a socket; left out, it does.
- `slack.ingest.since` (string): The first start's backfill date, YYYY-MM-DD; empty is 7 days back.

### Slack extraction

- `slack.extraction` (table): The extraction job; a zero value keeps its default.
- `slack.extraction.enabled` (bool): Switches the extraction job; left out, it is on.
- `slack.extraction.tier` (one of `fast`, `balanced`, `frontier`): Its model tier; left out, fast.
- `slack.extraction.language` (string): The language the extraction writes in; empty is English.
- `slack.extraction.per_call_usd` (number): What one extraction call may cost, in USD; 0 is 0.10.
- `slack.extraction.per_day_usd` (number): What extraction may cost in a day, in USD; 0 is 3.
- `slack.extraction.per_day` (integer): Extraction calls per day; 0 is 500.
- `slack.extraction.quiet` (duration): How long a unit stays unchanged before it runs; empty is 90s.
- `slack.extraction.max_wait` (duration): The longest delay of a unit's oldest change; empty is 10m.
- `slack.extraction.window_chars` (integer): The characters of conversation one call reads; 0 is 40000.
- `slack.extraction.max_failures` (integer): Failed calls in a row after which the job gives up on a unit until its next change; 0 is 3.
- `slack.extraction.context_messages` (integer): The read-only messages shown before a DM's window; 0 is 10, -1 turns them off.
- `slack.extraction.context_chars` (integer): The characters of those messages; 0 is 4000, -1 turns them off.
- `slack.extraction.escalate_over_chars` (integer): A unit whose rendered conversation is longer goes to the frontier tier; 0 or -1 is off.

### Workflow overrides

- `workflows` (table by name): Overrides of each workflow's manifest, by workflow name.
- `workflows.<name>` (table): One workflow's overrides.
- `workflows.<name>.enabled` (bool): Switches the workflow; left out, it is on.
- `workflows.<name>.observe` (bool): Plans the workflow's items pending, for you to run; left out, the manifest's.
- `workflows.<name>.only_when` (predicate): A condition ANDed with the workflow's when.
- `workflows.<name>.approval` (an [approval table](#approval-tables) of run modes): The approval of the workflow's runs, over the manifest's field by field.

### Workflow draft approval

- `workflows.<name>.drafts` (table): The drafts the workflow proposes.
- `workflows.<name>.drafts.approval` (an [approval table](#approval-tables) of draft modes): The approval of the workflow's drafts, over the manifest's field by field.

### Turns and headless calls

- `turns` (table): The automated turn budgets.
- `turns.per_day` (integer, default `20`): Automated turns per day, all workflows together, on units whose plugin keeps no per-unit count (every provider but GitHub today).
- `turns.per_unit_per_day` (integer, default `5`): Automated turns on one unit per local day, on units whose plugin keeps a per-unit count (GitHub today); every automated turn spends one, whatever its intent declares, and a turn by hand none.
- `headless` (table): The headless turns' caps; a cost of zero leaves it uncapped.
- `headless.tier` (one of `fast`, `balanced`, `frontier`, default `balanced`): The model tier of their calls.
- `headless.per_call_usd` (number, default `0.5`): What one call may cost, in USD.
- `headless.per_day_usd` (number, default `5`): What they may cost in a day, in USD.
- `brief` (table): The brief: one headless call that previews each unit.
- `brief.enabled` (bool, default `false`): Briefs each unit.
- `brief.tier` (one of `fast`, `balanced`, `frontier`, default `fast`): The brief's model tier.
- `brief.max_input_bytes` (integer, default `60000`): The most bytes of input a brief reads, between 1000 and 200000.
- `classify` (table): The classify step.
- `classify.per_day` (integer, default `50`): Classify calls per day.

### Drafts and notifications

- `drafts` (table): The settings every proposer's drafts share.
- `drafts.repeat_after` (duration, default `1d`): The default repeat_after a config.drafts.repeat_after reference reads, and the plugin rules' delay before they propose a settled draft's key again.
- `notifications` (table): The notifications.
- `notifications.digest_at` (string, default `09:00`): The local HH:MM of the daily digest; empty for none.

### Units and GTD

- `items` (table): The items.
- `items.heat` (table): How long a unit stays warm.
- `items.heat.warm_for` (duration): How long a unit stays warm after its last item resolved; left out, the heat reader's default.
- `gtd` (table): The GTD entries' settings.
- `gtd.stale_waiting_days` (integer, default `2`): Working days after which a waiting entry goes stale.
- `gtd.areas` (list of strings): The areas an entry is offered.
- `gtd.contexts` (list of strings): The contexts an entry is offered.

### Cleanup

- `cleanup` (table): The thresholds of the session and workspace cleanup workflows.
- `cleanup.idle` (duration, default `2h`): How long a finished session stays idle before it closes.
- `cleanup.asked_idle` (duration, default `4h`): How long a session that asked you stays idle before it closes.
- `cleanup.workspace_grace` (duration, default `1d`): How long, in working time, a workspace stays after its unit finished.
- `cleanup.manual_idle` (duration, default `1d`): How long a session you started stays idle before it closes.
- `cleanup.stale_workspace` (duration, default `14d`): How long an untouched workspace stays.
- `cleanup.disposable` (list of strings): Ignored files and directories a tool recreates, so removing a workspace that holds them loses nothing.

### Dossiers

- `dossiers` (table): The dossiers' lifecycle and quotas; a value of zero or less keeps its default.
- `dossiers.archive_after` (duration, default `30d`): How long after its unit or project resolved a dossier is archived.
- `dossiers.purge_after` (duration, default `400d`): How long after the archive it is purged.
- `dossiers.soft_cap_mb` (integer, default `50`): The size per dossier doctor reports.
- `dossiers.total_cap_mb` (integer, default `2048`): The size of dossiers and archives doctor reports.
- `dossiers.max_file_mb` (integer, default `5`): The largest file a dossier keeps; a larger one is linked.
- `dossiers.compact_at_kb` (integer, default `256`): The log size that triggers compaction.

### Daemon

- `sweep` (table): How often the sweeps run.
- `sweep.fast` (duration, default `2m`): The fast sweep's interval.
- `sweep.slow` (duration, default `1h`): The slow sweep's interval.
- `http` (table): The daemon's web cockpit.
- `http.port` (integer, default `0`): The cockpit's port; 0 is none.
- `mcp` (table): The daemon's MCP endpoint, which agent homes register.
- `mcp.port` (integer, default `47800`): The MCP endpoint's port.
- `views` (table by name): Each view sink's own settings, by sink.
- `views.<name>` (table): One sink's settings.
- `views.<name>.columns` (table by name): The sink's column for each status: working, working-fallback, needs-me, reviewed, ready, in-progress, in-review, completed.
- `views.<name>.columns.<name>` (string): The column of the status.

## Approval tables

An approval table that links here has these keys; a run approval takes the run modes, a draft approval the draft modes.

- `mode` (string): The mode when no rule matches.
- `window` (duration): How long a person has before the item runs or the draft is sent; only with mode delay, which needs it.
- `rule[]` (table): A rule tried in order; the first whose when holds gives the mode.
- `rule[].when` (predicate): When the rule applies.
- `rule[].mode` (string): The mode when the rule applies.
- `rule[].window` (duration): The rule's window; only with mode delay, which needs it.

The run modes:

- `run`: Runs at once.
- `delay`: Runs when the window ends unless a person stops it.
- `wait`: Waits for a person; a matched wait rule also holds an item a guard refuses.

The draft modes:

- `send`: Posts at once.
- `delay`: Posts when the window ends unless a person stops it.
- `wait`: Waits for a person.
- `dry_run`: Stores the draft and never sends it; nobody can release it.

This page is generated from the grammar table by `make docs-ref`.
