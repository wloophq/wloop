---
title: Profile language
description: Every reserved word of the profile files and the configuration, one line each.
---

Every key a profile file or `config.toml` may set, with its kind, its values and its default. A key or a value not listed here fails to load, with a close listed one as a suggestion when one exists. `<name>` is a name the author chooses, and `[]` marks an array of tables (`[[step]]`). Predicates and templates read the [facts](/reference/facts/); the manifests are detailed in the [intent manifest](/reference/intent-manifest/), [workflow manifest](/reference/workflow-manifest/) and [configuration](/reference/configuration/) pages.

## Intent manifest (`intents/<name>/intent.toml`)

### Identity and help

- `label` (string): The name people read. Required.
- `summary` (string): One line on what it does: its action's tooltip, its help line, its profile listing. Required.
- `shortcut` (string): One uppercase letter for its action, not a built-in action's.
- `verb` (one of `approve`, `delegate`, `dismiss`): The verb its action fulfils.
- `user_facing` (bool, default `true`): Offered as a command, a skill and an action.
- `skill` (bool, default `false`): Install writes it a turn skill; needs help.target.
- `help` (table): How its command presents it.
- `help.target` (string): Free text saying what the command's argument names, such as a pull request URL.
- `help.summary` (string): The main help's line; the intent's summary when absent.
- `help.group` (string): The title of the main help's group that lists the command, free text; action.group is the action's.
- `help.examples` (list of strings): Command lines without the command's name.
- `help.docs` (string): A page path of the documentation site.

### Binding

- `role` (one of `headless`, `pr.review`, `pr.address`, `pr.nudge`, `probe`): What binds it to a plugin's mechanism. One intent per role and provider.
  - `headless`: The intent a headless turn on a unit runs when its request names none.
  - `pr.review`: The review round on someone else's pull request, in its review workspace: first or follow-up, reconciled with the review you published; it drives its workspace's row.
  - `pr.address`: The address round on a pull request you develop, in its author workspace: its reviews, failing checks and conflicts; its instructions say why it is due; it drives its workspace's row.
  - `pr.nudge`: The nudge on a pull request: nudge.prepare fills its inputs from who the pull request waits on, or refuses it.
  - `probe`: The probe of an agent home: a synthetic pull request in the workspace the request names, under the probe's policy.
- `provider` (one of `github`, `session`, `slack`, `task`, `workspace`, `any`, or a list of them): The units its turns serve: a provider id, a list of them, or any; defaults.toml provider when absent.
  - `github`: Facts under pr.*.
  - `session`: Facts under session.*.
  - `slack`: Facts under slack.*.
  - `task`: Facts under task.*.
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
- `workspace.repo_optional` (bool, default `false`): With repo_input: a turn whose input names no repository runs in the neutral repository, with no checkout.
- `workspace.name` (template): The name of the worktree created for the turn's unit, over the name facts, with slug; the loop's own when absent.
- `runners` (list of `session`, `headless`): How its turns may run; ["session"] when absent.
- `session` (table): The agent session its turns run in.
- `session.strategy` (one of `reuse_live`, `new`): Whether a turn reuses the unit's live session or starts a new one.
- `session.title` (string): The session's title: 1 to 12 lowercase letters.
- `session.name` (template): What the workspace manager and the agent show the session as, over the name facts; defaults.toml session_name when absent, else the title. A name that session.title leads and the start time ends, as dd/mm hh:mm, lets the loop recognize its session when it lost track of it.
- `session.close_when_done` (bool, default `false`): The session closes when its turn is done.
- `target` (table): What a run with no unit does.
- `target.missing` (one of `capture`): capture: a run with no unit captures its guidance as a new task, the turn's unit; refused when absent.
- `target.title` (template): The captured task's title, over .guidance, with firstLine.

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
- `record.attach_url` (bool, default `false`): On a turn whose unit is a task, a done record's outcome url, when it names a unit such as a pull request, attaches that unit to the task; needs url in record.outcome.
- `record.outcome_doc` (string): What the outcome holds, for the instructions.
- `record.history_artifact` (string): The artifact the unit's history shows.
- `record.require[]` (table): What a record in a state must carry; record.requires is the record kind a run owes.
- `record.require[].state` (string): The state.
- `record.require[].artifact` (string): The artifact it must carry.
- `record.require[].fields` (list of strings): The outcome fields it must carry.
- `record.requires` (string): The record kind, of records.toml, a run of the intent owes; a run that ends without it opens the gate missing names. Left out, a run owes no record. Not record.require, what a record in a state carries.
- `record.missing` (string): The gate, of gates.toml, a run that ends without the record requires names opens.

### Gate approval

- `gates` (table): The gates its runs and drafts open.
- `gates.approval` (table): The approval of its gates.
- `gates.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The mode when no rule holds.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `gates.approval.window` (duration): With mode delay only: how long a person has to stop the gate's consequence.
- `gates.approval.rule[]` (table): A rule; the first whose when holds gives the mode.
- `gates.approval.rule[].when` (predicate): When the rule applies to a gate.
- `gates.approval.rule[].mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The rule's mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `gates.approval.rule[].window` (duration): With mode delay only: how long a person has to stop the gate's consequence.

### Effects and drafts

- `effects` (list of `plan.cleared`, `turn.guidance_handed`, `turn.round_sent`, `pr.reviewed`, `pr.addressed`, `pr.nudged`): What sending the turn applies.
  - `plan.cleared`: Drops the unit's pending plan items.
  - `turn.guidance_handed`: Hands the guidance queued on the unit to the turn, and queues it back when the turn is not sent.
  - `turn.round_sent`: Records the round-sent event on the unit when a person sends the turn.
  - `pr.reviewed`: Writes the plugin mark reviewed: the reviews the round covered, once its review is submitted.
  - `pr.addressed`: Writes the plugin marks addressed.*: the reviews, checks and conflict the address round covered.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft.
- `drafts` (table): The drafts its turns' agents propose.
- `drafts.allowed_effects` (list of `task.nudged`, `unit.closed`, `pr.nudged`): The effects its agents may declare on their drafts.
  - `task.nudged`: Stamps the last nudge of the draft's task, or of every live waiting task of its unit when it names none.
  - `unit.closed`: Records the closure of the draft's unit, which ends its close task and unwatches it after its warm period; through the unit's plugin.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft.
- `drafts.attach_posts` (bool, default `false`): On a turn whose unit is a task, the unit of each post its agent makes, sent at once or once approved, is attached to the task; reactions and edits are not posts.
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

## Tasks: properties, states and verbs (`tasks.toml`)

- `verbs` (table): The verbs a task offers, by verb.
- `verbs.<name>` (table): A verb, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `verbs.<name>.label` (string): The name people read on its button. Required.
- `verbs.<name>.summary` (string): One line, what pressing it does: its tooltip. Required.
- `verbs.<name>.offer` (predicate, default `true`): When a task offers the verb, over the task's own facts (task.id, task.kind, task.state, task.muted, task.unit, task.unit_provider); where it holds not, the verb is neither listed nor run.
- `verbs.<name>.input` (string): The question a verb that asks for a value puts first (waiting, schedule, project, context, area, delegate, reply); required for those.
- `verbs.<name>.from` (list of strings): The states the verb moves a task from, each a state of states; every state when absent.
- `verbs.<name>.to` (string): The state the verb moves a task to, a state of states; absent, the verb moves none.
- `verbs.<name>.by` (table by name): Who may run the verb, by actor (person, workflow, agent, plugin); an actor left out may not.
- `verbs.<name>.by.<name>` (string): A predicate over the task's facts that lets the actor run the verb where it holds, or "gate", which opens the gate the verb's gate names, and a person's approval runs the verb as theirs; true lets the actor run it anywhere.
- `verbs.<name>.gate` (string): The gate, of gates.toml, a by.&lt;actor&gt; of "gate" opens; required when one does.
- `verbs.<name>.handler` (one of `focus`, `schedule`, `waiting`, `approve`, `delegate`, `answer`, `edit`, `retry`, `reply`, `project`, `context`, `area`, `seen`, `reviewed`, `sequential`): What the verb runs besides its move, a handler of the loop's; none when absent.
  - `focus`: Stars the task in Focus, or takes the star off.
  - `schedule`: Hides the task until a date or until its unit moves, then brings it back where it was.
  - `waiting`: Records who the task waits on and when it resurfaces.
  - `approve`: Resolves the task's open gate as approved, or runs its unit's approve action.
  - `delegate`: Starts a turn on the task, headless or in a session.
  - `answer`: Answers the task's open question gate with the text it asks for.
  - `edit`: Edits the text of the task's draft.
  - `retry`: Sends again the task's approved draft whose delivery failed.
  - `reply`: Posts a reply or a reaction on the task's unit; the composer's verb.
  - `project`: Files the task under a parent, creating it from a title.
  - `context`: Sets the task's contexts.
  - `area`: Sets the task's area.
  - `seen`: Marks the task seen; clients send it when it opens.
  - `reviewed`: Marks a parent task reviewed.
  - `sequential`: Switches a parent task's children between sequential and parallel.
- `verbs.<name>.send` (one of `now`, `draft`, default `draft`): With reply: now sends the reply at once, draft proposes it in the Outbox.
- `props` (table by name): The task properties, by name, in the order the file declares them, a user layer's new ones after the bundled; each is the fact task.prop.&lt;name&gt;, typed from its type.
- `props.<name>` (table): A property, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `props.<name>.type` (one of `string`, `string[]`, `int`, `enum`, `date`, `duration`, `actor`, `actor[]`): The property's type; required for a new property. An int or a duration of 0 means unset: a value of 0 is refused.
- `props.<name>.values` (list of strings): An enum's values, in order; required with type enum.
- `props.<name>.writers` (list of `person`, `workflow`, `agent`, `plugin`): Who may write the property; one an agent or a plugin may write is untrusted, so no approval rule reads it.
  - `person`: A person, through a client.
  - `workflow`: A profile workflow's step.
  - `agent`: An agent's turn; untrusted.
  - `plugin`: A source plugin; untrusted.
- `props.<name>.doc` (string): What the property holds, for the facts' reference and the agents.
- `states` (table by name): The states a task takes, by name, in the order the file declares them, a user layer's new ones after the bundled; clients list them in this order, and a task proposed in a category takes the first state of it.
- `states.<name>` (table): A state, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `states.<name>.category` (one of `inbox`, `ready`, `active`, `waiting`, `deferred`, `done`, `dropped`): The state's category, which is all Go reads of it; required for a new state.
- `states.<name>.label` (string): The name people read.
- `owed_by` (table): Who owes a task's next step.
- `owed_by.expr` (string): An expression over the task's facts whose value, me or a person's key, is task.owed_by.
- `chat` (table): The chat on a task.
- `chat.intent` (string): The intent of the turn a message starts on a task with no live session.
- `areas` (list of strings): The areas a task may be in, the values of the area property.
- `contexts` (list of strings): The contexts the menu offers; a task may hold others.
- `stale_waiting_days` (integer): Working days a task waits before it is stale.

## Views and the review (`views.toml`)

- `views` (table): The lists of tasks, by view.
- `views.<name>` (table): A view, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `views.<name>.label` (string): The name people read in the list of views. Required.
- `views.<name>.over` (one of `tasks`, `gates`, default `tasks`): What the view lists: tasks, or gates.
- `views.<name>.when` (predicate): Which tasks or gates the view lists, over their facts.
- `views.<name>.group` (string): The fact the view groups by.
- `views.<name>.sort` (string): The facts the view sorts by, comma-separated; a leading - sorts one descending.
- `views.<name>.key` (string): The key that jumps to the view: `g`, a space and a key that no core key uses, such as `g i`. Its help is the view's label.
- `views.<name>.hide_disabled` (bool, default `false`): The view hides the tasks of a disabled plugin.
- `badge` (table): The menu bar's badge.
- `badge.views` (list of strings): The views whose counts the badge adds up.
- `review` (table): The review's steps, by step.
- `review.<name>` (table): A step of the review: inbox, outbox, waiting, projects, someday or focus. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `review.<name>.label` (string): What the step asks of you, as the review lists it. Required.

## Gates (`gates.toml`)

- `gates` (table by name): The gates a profile names, by name, in the order the file declares them, a user layer's new ones after the bundled.
- `gates.<name>` (table): A gate, by its name: the one a gate.open step, an intent's record.missing or a verb's gate opens; a gate the loop opens by kind takes the first of its kind. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `gates.<name>.kind` (one of `draft`, `plan`, `question`, `post`, `proposal`, `trust`, `step`): What the gate decides, which names the consequence of resolving it; required for a new gate.
  - `draft`: A draft to send: resolving it publishes the draft.
  - `plan`: A run the plan holds: resolving it releases the plan item.
  - `question`: An agent's question: resolving it delivers the answer to its session.
  - `post`: An agent's post: resolving it delivers the reply to its session.
  - `proposal`: Proposed task or item operations: resolving it applies them.
  - `trust`: A repository's first run: resolving it unblocks the run.
  - `step`: A workflow's gate.open: resolving it is an event workflows react to.
- `gates.<name>.label` (string): What the gate asks of you, as clients list it.
- `gates.<name>.intents` (table by name): What you may resolve the gate with, by intent, in the order clients offer them.
- `gates.<name>.intents.<name>` (table): An intent, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `gates.<name>.intents.<name>.label` (string): The name people read on its button.
- `gates.<name>.intents.<name>.status` (one of `approved`, `rejected`, `answered`, `dismissed`, `superseded`): The status the intent resolves the gate to; required for a new intent.
- `gates.<name>.intents.<name>.answer` (bool, default `false`): The intent takes text, such as a question's answer.

## Record kinds (`records.toml`)

- `records` (table by name): The record kinds a run, a person or a plugin writes on a task, by kind.
- `records.<name>` (table): A record kind, by its name, such as brief, report or decision.
- `records.<name>.schema` (file): The JSON schema a record of the kind follows, a file under records/. Required.
- `records.<name>.writers` (list of `person`, `workflow`, `agent`, `plugin`): Who may write a record of the kind; a record an agent or a plugin wrote is untrusted.
  - `person`: A person, through a client.
  - `workflow`: A profile workflow's step.
  - `agent`: An agent's turn; untrusted.
  - `plugin`: A source plugin; untrusted.
- `records.<name>.projects` (table by name): The record's fields that set a property of its task, by field.
- `records.<name>.projects.<name>` (string): The property, of tasks.toml, the field sets.

## Key help (`keys.toml`)

- `help` (table): The help line of each core key, by context (list, global, question, review, plan), then action (`capture`, `done`); a numbered one as `answer:{n}`, `{n}` its number.
- `help.<name>` (table): The keys of one context, by action.
- `help.<name>.<name>` (string): What the key does, as the help and the palette list it. Required.

## Workflow manifest (`workflows/<name>/workflow.toml`)

### What it matches

- `summary` (string): One line, what the workflow does; its plan items' description. Required.
- `provider` (one of `github`, `session`, `slack`, `task`, `workspace`): The provider of the units it matches. Required.
  - `github`: Facts under pr.*.
  - `session`: Facts under session.*.
  - `slack`: Facts under slack.*.
  - `task`: Facts under task.*.
  - `workspace`: Facts under workspace.*.
- `when` (predicate): The match condition, over the unit's facts, the trigger's and config.*. Required.
- `order` (integer, default `100`): Evaluation order, lowest first.
- `key` (list of facts): The facts forming the idempotency key, by default &lt;slot&gt;.fingerprint: a key that ran or was dismissed is not planned again. A workflow with trigger.on may add trigger.event.id, so each event plans its own run.
- `marks` (table by name): Mark name to fact, stored when a run is done or dismissed, which handled() reads.
- `marks.<name>` (fact): The fact whose value the mark stores.

### When it is planned

- `trigger` (table): What plans the workflow: on or schedule; absent, every sweep.
- `trigger.on` (list of strings): The event kinds the workflow reacts to: the loop's, the plugins', task.woke (a task's wake matched) and verb_&lt;verb&gt;, a verb of tasks.toml run on a task by anyone, a workflow included; the last two with provider task.
- `trigger.schedule` (string): A cron schedule, in local time.
- `trigger.actors` (list of `person`, `workflow`, `agent`, `plugin`): With on, the actors whose verb events plan it (person, workflow, agent, plugin); every actor when absent.
- `trigger.depth` (integer, default `3`): With on, the longest chain of workflows triggering each other the workflow may close: an event a chain of that many workflows caused plans it no more, and a workflow already in an event's chain never runs on it. 1 to 5.
- `observe` (bool, default `false`): Plans items pending and never runs them by itself; a person applies an item by hand.
- `limits` (table): How many runs the workflow may take.
- `limits.per_run` (integer, default `0`): Runs per evaluation; 0 is no limit.
- `limits.per_day` (integer, default `0`): Runs per local day; 0 is no limit.
- `limits.per_unit_per_day` (integer, default `0`): Runs on one unit per local day; 0 is no limit.
- `limits.cooldown` (duration): The least time between runs on one unit.

### Steps

- `step[]` (table): A step the run takes, in order. Required: at least one.
- `step[].use` (one of `session.close`, `workspace.remove`, `workspace.unregister`, `turn.dismiss`, `answer.refuse`, `session.mark_dormant`, `turn.start`, `draft.propose`, `classify`, `notify.self`, `task.verb`, `gate.open`, `task.set`): The step.
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
  - `task.verb`: Runs a verb on the task: `verb` (`next`, `waiting`, `schedule`, `someday`, `reference`, `focus`, `context`, `area`, `done` or `dismiss`), with `until` and `on_activity` for schedule, `on` and `resurface` for waiting, and `set` for context and area. The plan blocks it while the task's state refuses the verb, and `done` and `dismiss` on a task derived from a unit's items, which closes when they resolve. It records no `verb_&lt;verb&gt;` event, which only a person's verb records, so a workflow never triggers another through it.
  - `gate.open`: Opens the gate `name` of gates.toml on the unit, a decision that waits for you with the intents gates.toml gives it.
  - `task.set`: Sets the task's property `prop` of tasks.toml to `value`, written by the workflow; a property the workflow may not write is refused.
- `step[].<name>` (string or list): The step's arguments.

### Run approval

- `approval` (an [approval table](#approval-tables) of run modes, default mode `run`): The run approval: how a matched item runs.

### Gate approval

- `gates` (table): The gates its runs and drafts open.
- `gates.approval` (table): The approval of its gates.
- `gates.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The mode when no rule holds.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `gates.approval.window` (duration): With mode delay only: how long a person has to stop the gate's consequence.
- `gates.approval.rule[]` (table): A rule; the first whose when holds gives the mode.
- `gates.approval.rule[].when` (predicate): When the rule applies to a gate.
- `gates.approval.rule[].mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The rule's mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `gates.approval.rule[].window` (duration): With mode delay only: how long a person has to stop the gate's consequence.

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
  - `delivery` (one of `message`, `reaction`, `comment`, default `message`): How the draft posts: the core's message or reaction, or a delivery a plugin declares.
  - `text` (file, default `text.md.tmpl`): The template of the draft's text, beside the manifest.
  - `reaction` (string): The emoji name, with delivery reaction.
  - `items` (one of `owed`, `resolved`, `unowned`, `unowned_aged`, `all`, default `all`): Which of the unit's items the draft lists.
  - `per` (one of `unit`, `person`, default `unit`): One draft per unit, or one per person it waits on.
  - `target` (one of `unit`, `item`, default `unit`): Where the draft posts: on the unit, or on each item; an intent's help.target is free text.
  - `effects` (list of `task.nudged`, `unit.closed`, `pr.nudged`): What sending the draft applies.
  - `task.nudged`: Stamps the last nudge of the draft's task, or of every live waiting task of its unit when it names none.
  - `unit.closed`: Records the closure of the draft's unit, which ends its close task and unwatches it after its warm period; through the unit's plugin.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft.
  - `repeat_after` (string): The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after.
  - `repeat_after_sent` (string): The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever.
- `classify`:
  - `prompt` (file, default `prompt.md`): The classification's prompt, beside the manifest.
  - `schema` (file, default `schema.json`): The JSON schema of its answer, beside the manifest.
- `notify.self`:
  - `text` (file, default `text.md.tmpl`): The template of the notification's text, beside the manifest: `.Workflow`, `.Unit` and `.Facts`.
- `task.verb`:
  - `verb` (string): The verb of tasks.toml run on the task, one whose by.workflow lets a workflow run it. Required.
  - `until` (string): With a verb whose handler is schedule, the date the task returns: RFC 3339 or YYYY-MM-DD.
  - `on_activity` (string): With a verb whose handler is schedule, `true` returns the task on new activity on its unit.
  - `on` (string): With a verb whose handler is waiting, the people the task waits on, comma-separated.
  - `resurface` (string): With a verb whose handler is waiting, the date the task resurfaces: RFC 3339 or YYYY-MM-DD.
  - `set` (string): With a verb whose handler is context, the contexts, comma-separated; with area, the area.
- `gate.open`:
  - `name` (string): The gate, of gates.toml, the step opens. Required.
- `task.set`:
  - `prop` (string): The property, of tasks.toml, the step sets. Required.
  - `value` (string): The value, written as its type reads it: an enum's value, a date as YYYY-MM-DD, a duration, a list comma-separated. Required.

## Defaults (`defaults.toml`)

- `provider` (one of `github`, `session`, `slack`, `task`, `workspace`, `any`): The provider of an intent whose manifest names none.
  - `github`: Facts under pr.*.
  - `session`: Facts under session.*.
  - `slack`: Facts under slack.*.
  - `task`: Facts under task.*.
  - `workspace`: Facts under workspace.*.
  - `any`: Every provider's units.
- `session_name` (template): The session.name of an intent whose manifest has none.
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
  - `inbox_read`: Reads the user's answers to the session's Inbox messages, and marks them read.
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
- `inbox_read`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `no_turn` (`Error`): The session has no turn.
  - `no_message` (`ID`: number): The message is not one of the session's.
  - `read_failed` (`Error`): The Inbox could not be read.
  - `nothing` (`ID`: number): No answer to read: of message ID, or of any message when ID is 0.
  - `read` (`Conversations`): The answers read: the conversations, one after another.
  - `conversation` (`ID`: number, `Kind`, `Text`, `Options`, `Answers`): A part of read: a message, its kind (question or agent_post), its text and options, and its answers.
  - `turn_answers` (`Answers`): A part of read: answers to the session's turns stored before each answer named its message.
  - `option` (`ID`, `Label`): A part of conversation: one option of the message.
  - `answer` (`Option`, `Label`, `Choice`, `Text`, `At`): A part of conversation: one answer, with its option and the option's label, yes or no, its text and when it was given.
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
  - `problem_message`: A problem: message is not a message ref or permalink.
  - `problem_emoji_required`: A problem: emoji is missing or empty.
  - `reacted` (`Emoji`, `Conversation`, `Message`, `Permalink`): The reaction is added; Permalink is "" when Slack gave none.
  - `removed` (`Emoji`, `Conversation`, `Message`, `Permalink`): The reaction is removed; Permalink is "" when Slack gave none.
  - `already` (`Emoji`, `Conversation`, `Message`, `Permalink`): The message already had the user's reaction; Permalink is "" when Slack gave none.
  - `absent` (`Emoji`, `Conversation`, `Message`, `Permalink`): The message had no such reaction of the user; Permalink is "" when Slack gave none.
- `slack_edit`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_message`: A problem: message is not a message ref or permalink.
  - `problem_text_required`: A problem: text is missing or empty.
  - `broadcast`: The text notifies a whole channel or group.
  - `edited` (`Conversation`, `Message`, `Permalink`, `Text`, `Replaced`): The message is edited: where, its ref and its permalink, "" when Slack gave none, its new text as posted and the text it replaced.

## What the loop tells a session (`notices.toml`)

- `<name>` (template): A notice, by its name: a template over its fields, `Name` and `Command`.
  - `answered`: Typed into an idle session, or the reason that blocks its stop while it works: the user answered an Inbox message, Message, 0 for an answer stored before answers named their message, of the unit named as the loop's commands read it.
  - `record_owed`: The reason that blocks the stop of a session whose turn has no record: the unit, the turn's id and the directory a record file goes in.

The fields each notice reads besides `Name` and `Command`:

  - `answered` (`Message`: number, `Unit`)
  - `record_owed` (`Unit`, `Turn`, `ScratchDir`)

## Judgment block front matter (`+++` in `<dim>=<value>/.../intent=<name>.md`)

- `override` (bool, default `false`): Drops the blocks before it whose path is an ancestor of its own.
- `title` (string): Replaces the block's heading.
- `include` (list of files): Up to eight .md or .txt files of the profile the block inlines, relative to it.
- `paths` (list of strings): The block applies only when a changed file matches one of these globs.
- `delegates` (table by name): Per agent, skills the finding step runs besides the procedure's own work.
- `delegates.<name>` (list of strings): The agent's skills.

## Configuration (`config.toml`)

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

- `projects[]` (table): Settings for the pull requests and the work of matching repositories; the first match wins.
- `projects[].match` (string): An owner/repo pattern, such as acme/* or acme/api.
- `projects[].projects_dir` (string): The project's projects_dir.
- `projects[].agent` (string): The agent the project's sessions run.
- `projects[].agent_home` (string): The project's agent_home.
- `projects[].workspace_managers` (list of strings): The managers a new worktree of the project picks from, in order.
- `projects[].trust_clones` (bool): Trusts a repository of the project in the agent home at the moment the loop clones it, so its settings and hooks run as you; the match must name its owner, such as acme/*.
- `projects[].github` (table): The GitHub settings the project overrides.
- `projects[].github.trusted_authors` (list of strings): The project's plugins.github.trusted_authors.
- `projects[].github.followups_without_workspace` (bool): The project's plugins.github.followups_without_workspace.
- `projects[].github.address_without_workspace` (bool): The project's plugins.github.address_without_workspace.

### Placement

- `placement[]` (table): The rules that place units; the first match wins.
- `placement[].match` (string): The units the rule places.
- `placement[].repo` (string): The repository their work checks out.
- `placement[].checkout` (string): The branch their work checks out.
- `placement[].role` (one of `review`, `author`, `work`): The role of their workspace.
- `placement[].shared` (bool): The units share one workspace.

### Plugins

- `plugins` (table): Each plugin's table, by id: its switch, its trust and its settings.
- `plugins.<name>` (table): A plugin's table; the keys besides enabled and trust are the plugin's own, which only it reads.
- `plugins.<name>.enabled` (bool): Switches the plugin; left out, it is on.
- `plugins.<name>.trust` (one of `path`): path runs the plugin's binary whatever its checksum; left out, a bundled plugin runs only with the checksum its install recorded, and any other plugin does not run. Only you write it.
- `plugins.<name>.<name>` (string or list): A key of the plugin's own.

### GitHub

- `plugins.github` (table): The GitHub plugin's settings.
- `plugins.github.enabled` (bool): Switches the GitHub plugin; left out, it is on.
- `plugins.github.trust` (one of `path`): path runs the GitHub plugin's binary whatever its checksum; left out, it runs only with the checksum its install recorded.
- `plugins.github.review_prefix` (string, default `REVIEW `): The prefix of a review workspace's name.
- `plugins.github.automated_reviewers` (list of strings): Logins of review bots whose reviews count as automated.
- `plugins.github.owners` (list of strings): The GitHub owners automated follow-ups run on; empty is every owner the workspace manager knows.
- `plugins.github.trusted_authors` (list of strings): Logins whose pull requests pass the author association check of automated rounds.
- `plugins.github.followups_without_workspace` (bool, default `false`): A follow-up review recreates the review workspace the user deleted; off, the sweep only logs it.
- `plugins.github.address_without_workspace` (bool, default `false`): An address round creates its author workspace when it has none; off, the sweep only records it.
- `plugins.github.rate_limit_fraction` (number, default `0.5`): The share of the GitHub rate limit the sweeps may spend, a number between 0 and 1.
- `plugins.github.gates` (table): The gates the plugin's own rules open.
- `plugins.github.gates.approval` (table): Their approval; left out, each waits for you.
- `plugins.github.gates.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The approval mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `plugins.github.gates.approval.window` (duration): How long a delay waits before the gate's consequence; only with mode delay.

### GitHub address rounds

- `plugins.github.address_reviews` (bool, default `true`): Someone's review of a pull request you develop starts an address round.
- `plugins.github.address_skip_reviewers` (list of strings): Logins whose reviews never start an address round.
- `plugins.github.address_checks` (bool, default `true`): Failing checks on the head of a pull request you develop start an address round.
- `plugins.github.ignore_checks` (list of strings): Checks whose failure never starts an address round.
- `plugins.github.address_conflicts` (bool, default `true`): A pull request you develop that conflicts with its base past the conflict window starts an address round.

### GitHub scope, stalls and nudges

- `plugins.github.scope` (table): Whose pull requests the GitHub plugin follows.
- `plugins.github.scope.people` (list of strings): Logins whose pull requests it follows.
- `plugins.github.scope.teams` (list of strings): Teams whose pull requests it follows.
- `plugins.github.scope.repos` (list of strings): Repositories whose pull requests it follows.
- `plugins.github.scope.watch` (list of strings): Repositories it watches.
- `plugins.github.stalled` (table): When a pull request counts as stalled.
- `plugins.github.stalled.review` (duration, default `2d`): Waiting on a review for this long.
- `plugins.github.stalled.reply` (duration, default `1d`): Waiting on a reply for this long.
- `plugins.github.stalled.merge` (duration, default `1d`): Approved and unmerged for this long.
- `plugins.github.nudge` (table): The nudges on pull requests.
- `plugins.github.nudge.silent_days` (integer, default `30`): Calendar days of silence on a pull request before a nudge is due; workflows read it as config.plugins.github.nudge.silent_days.

### Slack

- `plugins.slack` (table): The Slack plugin's settings; no secret lives here.
- `plugins.slack.enabled` (bool): Switches the Slack plugin; left out, it is on.
- `plugins.slack.trust` (one of `path`): path runs the Slack plugin's binary whatever its checksum; left out, it runs only with the checksum its install recorded.
- `plugins.slack.client_id` (string): The Slack app's client id; empty leaves Slack unconfigured.
- `plugins.slack.redirect_port` (integer): The login callback port registered in the Slack app; 0 is 53682.
- `plugins.slack.keychain_service` (string): The name of the Keychain items; empty is the brand's.
- `plugins.slack.post_allowlist` (list of strings): When not empty, the only channel ids, or user ids naming the DM with that user, a post may go to.
- `plugins.slack.request_log` (bool, default `false`): Logs one line per Slack call: method and HTTP status.
- `plugins.slack.ended_after` (duration, default `7d`): How long a Slack unit stays quiet, its newest stored message that old, before the plugin says it has ended (slack.ended).
- `plugins.slack.turns` (table): What a turn on a Slack unit reads.
- `plugins.slack.turns.max_chars` (integer): The characters of the unit's messages rendered into the instructions; 0 is 30000.
- `plugins.slack.gates` (table): The gates the plugin's own rules open.
- `plugins.slack.gates.approval` (table): Their approval; left out, each waits for you.
- `plugins.slack.gates.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The approval mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `plugins.slack.gates.approval.window` (duration): How long a delay waits before the gate's consequence; only with mode delay.

### Slack ingest

- `plugins.slack.ingest` (table): What the Slack plugin admits and how it listens.
- `plugins.slack.ingest.terms` (list of strings): A message that holds one of them as a word, in any case, is admitted.
- `plugins.slack.ingest.allow_apps` (list of strings): Bot ids (B...) or app ids (A...) whose messages are admitted.
- `plugins.slack.ingest.deny_apps` (list of strings): Bot ids (B...) or app ids (A...) whose messages are not.
- `plugins.slack.ingest.socket` (bool): Listens on a socket; left out, it does.
- `plugins.slack.ingest.since` (string): The first start's backfill date, YYYY-MM-DD; empty is 7 days back.

### Slack extraction

- `plugins.slack.extraction` (table): The extraction job; a zero value keeps its default.
- `plugins.slack.extraction.enabled` (bool): Switches the extraction job; left out, it is on.
- `plugins.slack.extraction.tier` (one of `fast`, `balanced`, `frontier`): Its model tier; left out, fast.
- `plugins.slack.extraction.language` (string): The language the extraction writes in; empty is English.
- `plugins.slack.extraction.per_call_usd` (number): What one extraction call may cost, in USD; 0 is 0.10.
- `plugins.slack.extraction.per_day_usd` (number): What extraction may cost in a day, in USD; 0 is 3.
- `plugins.slack.extraction.per_day` (integer): Extraction calls per day; 0 is 500.
- `plugins.slack.extraction.quiet` (duration): How long a unit stays unchanged before it runs; empty is 90s.
- `plugins.slack.extraction.max_wait` (duration): The longest delay of a unit's oldest change; empty is 10m.
- `plugins.slack.extraction.window_chars` (integer): The characters of conversation one call reads; 0 is 40000.
- `plugins.slack.extraction.max_failures` (integer): Failed calls in a row after which the job gives up on a unit until its next change; 0 is 3.
- `plugins.slack.extraction.context_messages` (integer): The read-only messages shown before a DM's window; 0 is 10, -1 turns them off.
- `plugins.slack.extraction.context_chars` (integer): The characters of those messages; 0 is 4000, -1 turns them off.
- `plugins.slack.extraction.escalate_over_chars` (integer): A unit whose rendered conversation is longer goes to the frontier tier; 0 or -1 is off.

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

### Workflow gate approval

- `workflows.<name>.gates` (table): The gates the workflow opens.
- `workflows.<name>.gates.approval` (table): The approval of the workflow's gates, over the manifest's field by field.
- `workflows.<name>.gates.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The mode when no rule matches; left out, the manifest's.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `workflows.<name>.gates.approval.window` (duration): How long a delay waits before it goes; only with mode delay.
- `workflows.<name>.gates.approval.rule[]` (table): Rules tried in order before the mode, the first match wins; set, they replace the manifest's, and [] clears them.
- `workflows.<name>.gates.approval.rule[].when` (predicate): When the rule applies.
- `workflows.<name>.gates.approval.rule[].mode` (one of `send`, `delay`, `wait`, `dry_run`, `run`): The rule's mode.
  - `send`: Posts at once.
  - `delay`: Posts when the window ends unless a person stops it.
  - `wait`: Waits for a person.
  - `dry_run`: Stores the draft and never sends it; nobody can release it.
  - `run`: Runs at once.
- `workflows.<name>.gates.approval.rule[].window` (duration): The rule's delay; only with mode delay.

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

### Units and tasks

- `items` (table): The items.
- `items.heat` (table): How long a unit stays warm.
- `items.heat.warm_for` (duration): How long a unit stays warm after its last item resolved; left out, the heat reader's default.
- `tasks` (table): The tasks' settings.
- `tasks.stale_waiting_days` (integer, default `2`): Working days after which a waiting task goes stale.
- `tasks.areas` (list of strings): The areas a task is offered.
- `tasks.contexts` (list of strings): The contexts a task is offered.

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
- `dossiers.archive_after` (duration, default `30d`): How long after its task or unit resolved a dossier is archived.
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
