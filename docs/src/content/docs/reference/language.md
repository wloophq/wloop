---
title: Profile language
description: Every reserved word of the profile files and the configuration, one line each.
---

Every key a profile file or `config.toml` may set, with its kind, its values and its default. A key or a value not listed here fails to load, with a close listed one as a suggestion when one exists. `<name>` is a name the author chooses, and `[]` marks an array of tables (`[[step]]`). Predicates and templates read the [facts](/reference/facts/); the manifests are detailed in the [intent manifest](/reference/intent-manifest/), [workflow manifest](/reference/workflow-manifest/) and [configuration](/reference/configuration/) pages.

## Intent manifest (`intents/<name>/intent.toml`)

### Identity and help

- `label` (string, localized): The name people read. Required.
- `summary` (string, localized): One line on what it does: its action's tooltip, its help line, its profile listing. Required.
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

- `role` (one of `headless`, `attach`, `pr.review`, `pr.address`, `pr.nudge`, `probe`): What binds it to a plugin's mechanism. One intent per role and provider.
  - `headless`: The intent a headless turn on a unit runs when its request names none.
  - `attach`: The intent a session started outside the loop runs when it attaches to a task: started by hand on that session, which becomes a run of the task.
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
- `session.source_writes` (bool, default `true`): The session may run the commands the plugins' manifests declare as writes to their source, under [source.session] write_commands; false denies them, so the agent proposes drafts instead.
- `target` (table): What a run with no unit does.
- `target.missing` (one of `capture`): capture: a run with no unit captures its guidance as a new task, the turn's unit; refused when absent.
- `target.title` (template): The captured task's title, over .guidance, with firstLine or title.

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

- `why` (template, localized): Why the turn is due, over the unit's workflow facts.
- `refuse[]` (table): A refusal of turn.start; the first whose when holds refuses with its why.
- `refuse[].when` (predicate): When it refuses, over the unit's workflow facts.
- `refuse[].why` (template, localized): What the refusal says, over the unit's workflow facts.

### Its action on a row

- `action` (table): How the intent's action is offered on a row; its description is the intent's summary.
- `action.group` (one of `start`, `turn`, `control`, `workspace`, `brief`, `intake`, default `start`): The group the action is listed under.
- `action.surfaces` (list of `row`, `detail`, `task`, `tui`, `menubar`, `notification`, `cli`, `explore`): Where the action is offered: row, detail, tui, cli and explore when absent.
- `action.when` (predicate, default `row.open`): When the action is offered on a row.
- `action.primary` (table): When the action is the row's primary one.
- `action.primary.when` (predicate, default `false`): When the action is the row's primary one.
- `action.refuse[]` (table): A case that offers the action disabled; the first that holds says why.
- `action.refuse[].when` (predicate): When the action is offered disabled.
- `action.refuse[].why` (template, localized): Why the action is disabled, over the action facts.
- `action.note` (template, localized): What the action would do on this row, over the action facts.
- `action.warning` (template, localized): Shown under the action, over the action facts; empty shows nothing.
- `action.confirm` (template, localized): The dialog that confirms the action, over the action facts; empty asks nothing.

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
- `guidance.label` (string, localized, default `Guidance`): The guidance's label.
- `guidance.help` (string, localized): What the guidance is for.
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

- `record` (table): The turn's record. Left out, a turn of the intent owes no record and its session's stop is never blocked for one.
- `record.when` (predicate, default `true`): When the turn records its outcome.
- `record.states` (list of `done`, `settled`, `asked`, `failed`): The states a record may take. Required when the table sets when, artifacts, outcome, require or attach_url; a table that only sets requires and missing needs none.
- `record.artifacts` (list of `review-draft`, `triage`): The artifacts a record may carry.
- `record.outcome` (list of `verdict`, `url`, `draft`): The outcome fields a record may carry.
- `record.attach_url` (bool, default `false`): On a turn whose unit is a task, a done record's outcome url, when it names a unit such as a pull request, attaches that unit to the task; needs url in record.outcome.
- `record.concludes` (list of strings): The outcome verdicts that end the work a turn is about, such as an approval, which the board shows done; each is a verdict the intent's plugins' draft kinds declare.
- `record.outcome_doc` (string): What the outcome holds, for the instructions.
- `record.history_artifact` (string): The artifact the unit's history shows.
- `record.require[]` (table): What a record in a state must carry; record.requires is the record kind a run owes.
- `record.require[].state` (string): The state.
- `record.require[].artifact` (string): The artifact it must carry.
- `record.require[].fields` (list of strings): The outcome fields it must carry.
- `record.requires` (string): The record kind, of records.toml, a run of the intent owes; a run that ends without it opens the gate missing names. Left out, a run owes no record. Not record.require, what a record in a state carries.
- `record.missing` (string): The gate, of gates.toml, a run opens when it fails or ends without its record, which an answer resumes; left out, such a run opens none. Required with requires.

### Gate approval

- `gates` (table): The gates its runs and drafts open.
- `gates.approval` (an [approval table](#approval-tables) of gate modes): The approval of its gates: a rule applies to the kinds its when pins gate.kind to, else to draft gates alone when it reads a draft's facts, else to every kind; what no rule or mode here decides, gates.toml's [approval] does.

### Effects and drafts

- `effects` (list of `plan.cleared`, `turn.guidance_handed`, `turn.round_sent`, `pr.reviewed`, `pr.addressed`, `pr.nudged`): What sending the turn applies.
  - `plan.cleared`: Drops the unit's pending plan items.
  - `turn.guidance_handed`: Hands the guidance queued on the unit to the turn, and queues it back when the turn is not sent.
  - `turn.round_sent`: Records the round-sent event on the unit when a person sends the turn.
  - `pr.reviewed`: Writes the plugin mark reviewed: the reviews the round covered, once its review is submitted.
  - `pr.addressed`: Writes the plugin marks addressed.*: the reviews, checks and conflict the address round covered.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft.
- `drafts` (table): The drafts its turns' agents propose.
- `drafts.allowed_effects` (list of `task.nudged`, `unit.closed`, `pr.reviewed`, `pr.nudged`): The effects its agents may declare on their drafts.
  - `task.nudged`: Writes the last_nudge property of the draft's task and of every open task holding one of the items it lists. It says the people were nudged, so it needs the draft's post.
  - `unit.closed`: Records the closure of the draft's unit through its plugin, which unwatches it after its warm period, then ends the open tasks that hold the unit. It applies without the draft's post too.
  - `pr.reviewed`: Writes the plugin mark reviewed: the reviews the round covered, once its review is submitted.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft.
- `drafts.attach_posts` (bool, default `false`): On a turn whose unit is a task, the unit of each post its agent makes, sent at once or once approved, is attached to the task; reactions and edits are not posts.

## Built-in actions (`actions.toml`)

- `<name>` (table): A built-in action, by its id.
- `<name>.label` (string, localized): The name people read. Required.
- `<name>.summary` (string, localized): One line, what pressing it does: its tooltip and help line. Required.
- `<name>.note` (template, localized): What the action would do on this row, over the action facts.
- `<name>.warning` (template, localized): Shown under the action, over the action facts; empty shows nothing.
- `<name>.confirm` (template, localized): The dialog that confirms the action, over the action facts; empty asks nothing.
- `<name>.why` (template, localized): Why the action is disabled, over the action facts.
- `<name>.input` (table): The text the action asks for.
- `<name>.input.label` (string, localized): The input's name.
- `<name>.input.help` (string, localized): What to write, shown in the empty input.
- `<name>.copy` (table): What running the action gives the person to paste elsewhere, on an action whose only effect is its copy (copy_review_request): the client puts it on the clipboard, or shows it where it cannot. Its texts run over the copy facts of the unit the action runs on (`{{.copy.title}}`, `{{.copy.link}}`, `{{.copy.brief.goal}}`); `html` escapes a value and `replace` swaps a substring (`{{replace .copy.title "`" "'"}}`).
- `<name>.copy.text` (template, localized): The plain text copied, over the copy facts. Required.
- `<name>.copy.html` (template, localized): The same text formatted as HTML, which a rich paste target reads; empty copies the plain text alone.
- `<name>.copy.notice` (template, localized): What the client says once it copied, over the copy facts. Required.

## Tasks: properties, states and verbs (`tasks.toml`)

- `verbs` (table): The verbs a task offers, by verb.
- `verbs.<name>` (table): A verb, by its name: lowercase letters, digits and hyphens, starting with a letter. A user layer may set only some keys of an entry declared below it, and adds its own verbs, which record the event verb_&lt;name&gt;; a new entry sets the required ones.
- `verbs.<name>.label` (string, localized): The name people read on its button. Required.
- `verbs.<name>.summary` (string, localized): One line, what pressing it does: its tooltip. Required.
- `verbs.<name>.offer` (predicate, default `true`): When a task offers the verb, over the task's own facts (task.id, task.prop.kind, task.state, task.unit, task.unit_provider); where it holds not, the verb is neither listed nor run.
- `verbs.<name>.input` (string): The question a verb that asks for a value puts first; required for a verb whose handler asks for one (waiting, schedule, project, context, area, delegate, reply, promise, merge, split, relate, unrelate); a verb that takes another task puts it over the list of tasks the person picks from.
- `verbs.<name>.from` (list of strings): The states the verb moves a task from, each a state of states; every state when absent.
- `verbs.<name>.to` (string): The state the verb moves a task to, a state of states; absent, the verb moves none.
- `verbs.<name>.by` (table by name): Who may run the verb, by actor (person, workflow, agent, plugin); an actor left out may not.
- `verbs.<name>.by.<name>` (string): A predicate over the task's facts that lets the actor run the verb where it holds, or "gate", which opens the gate the verb's gate names, and a person's approval runs the verb as theirs; true lets the actor run it anywhere.
- `verbs.<name>.gate` (string): The gate, of gates.toml, a by.&lt;actor&gt; of "gate" opens; required when one does.
- `verbs.<name>.handler` (one of `finish`, `drop`, `focus`, `schedule`, `wake`, `waiting`, `approve`, `delegate`, `answer`, `edit`, `retry`, `reply`, `project`, `context`, `area`, `seen`, `promise`, `reviewed`, `sequential`, `merge`, `split`, `relate`, `unrelate`): What the verb runs besides its move, a handler of the loop's; none when absent.
  - `finish`: Moves the task to the verb's to; a project's dossier starts its lifecycle.
  - `drop`: Moves the task to the verb's to; a kind another service serves drops its subject: a draft is discarded, a message's gate refused.
  - `focus`: Stars the task, or takes the star off: toggles the property of role star.
  - `schedule`: Hides the task until a date or until its unit moves, then brings it back where it was.
  - `wake`: Brings a deferred task back where its wake returns it, the first ready state when it names none, and drops the wake.
  - `waiting`: Records who the task waits on and when it resurfaces.
  - `approve`: Resolves the task's open gate as approved, or runs its unit's approve action.
  - `delegate`: Starts a turn on the task, headless or in a session.
  - `answer`: Answers the task's open question gate with the text it asks for.
  - `edit`: Edits the text of the task's draft.
  - `retry`: Sends again the task's approved draft whose delivery failed.
  - `reply`: Posts a reply or a reaction on the task's unit; the composer's verb.
  - `project`: Files the task under a parent, creating it from a title.
  - `context`: Sets the task's contexts: the property of role contexts.
  - `area`: Sets the task's area: the property of role area.
  - `seen`: Marks the task seen; clients send it when it opens.
  - `promise`: Sets the property of role promise to the time its by names: HH:MM, today or tomorrow once that passed; YYYY-MM-DD, 18:00 that day; or an RFC 3339 time. An empty by clears it.
  - `reviewed`: Marks a parent task reviewed.
  - `sequential`: Switches a parent task's children between sequential and parallel.
  - `merge`: Folds the task and the other task its task argument names into one: the one the verb's survivor holds on alone stays, else the older; the other's items, units, gates, drafts and messages move to it, its id leads to it, and split gives it back.
  - `split`: Gives back a task merged into this one, as it was before the merge: its items, units, gates, drafts and messages; what reached this task after the merge stays.
  - `relate`: Links the task and the other task its task argument names as related, distinct work: nothing moves, and each lists the other in its property of role related.
  - `unrelate`: Takes the link between the task and a related one off both.
- `verbs.<name>.send` (one of `now`, `draft`, default `draft`): On a verb whose handler is reply: what a reply that names no send does. now sends it at once, as the person approving it; draft holds it on its gate for approval. The composer's Save as draft always holds it.
- `verbs.<name>.survivor` (predicate): On a verb whose handler is merge: a predicate over each of the two tasks' facts; the task it holds on alone is the one that stays and takes the other in, and the older stays when it holds on both or neither.
- `verbs.<name>.keep_drafts` (list of strings): The purposes of the held drafts a task keeps when the verb moves it from an open state to a done or dropped one; every other held draft of the task is withdrawn with the reason ended, and holds its workflow from proposing again on the unit while the task stays ended. Every held draft is withdrawn when absent.
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
- `props.<name>.role` (one of `area`, `contexts`, `star`, `deadline`, `promise`, `related`, `merged`): The mechanism of the loop that acts on the property; at most one property takes each role. Go names the role, never the property.
  - `area`: A string: the area filter of the lists and the counters reads it, the area handler sets it, and a new parent takes it from the task it starts with.
  - `contexts`: A string[]: the contexts filter reads it, the context handler sets it, and a task takes it from the item or the request that opens it.
  - `star`: An enum: the star handler toggles it between its first value, starred, and its second.
  - `deadline`: A date: a task takes it from its items' deadline, and a draft's task from its gate's window.
  - `promise`: A date: the promise handler sets it to the time you promise the task by, and task.promised reads it.
  - `related`: A string[] of task ids: the relate handler adds each of two tasks to the other's, unrelate takes it off both.
  - `merged`: A string[] of task ids, in merge order: the merge handler adds the task it folds in, split takes it off; a task that holds one settles item by item.
- `states` (table by name): The states a task takes, by name, in the order the file declares them, a user layer's new ones after the bundled; clients list them in this order, and a task proposed in a category takes the first state of it.
- `states.<name>` (table): A state, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `states.<name>.category` (one of `inbox`, `ready`, `active`, `waiting`, `deferred`, `done`, `dropped`): The state's category, which is all Go reads of it; required for a new state.
- `states.<name>.label` (string, localized): The name people read.
- `owed_by` (table): Who owes a task's next step.
- `owed_by.expr` (string): An expression over the task's facts whose value, me or a person's key, is task.owed_by.
- `chat` (table): The chat on a task.
- `chat.intent` (string): The intent of the turn a message starts on a task with no live session.
- `attribution` (table): How the task's page names who made a change.
- `attribution.via_session` (string): The name of a change an agent made for you through a session you drove; {session} is the session's id.
- `attribution.via_workflow` (string): The name of a change a workflow made, by a verb its by lets it run or a gate its approval released; {workflow} is the workflow's name.
- `detail` (table): What a task's detail panel offers.
- `detail.verbs` (list of strings): The verbs the detail shows as buttons, in order, each a verb of verbs; those the task does not offer are left out, and every other verb it offers waits behind a More button that opens the palette. A user layer's list replaces the one below it.
- `runs` (table): The words a task's runs read in its detail and its terminal page.
- `runs.states` (table by name): Where a run's turn stands, by state, as its run card says it. A state with no word shows as it is named.
- `runs.states.<name>` (string, localized): The state's word, such as "started"; the key is a turn state: sent, started, asked, done, settled, failed, unrecorded, dismissed, replaced or withdrawn.
- `runs.sessions` (table by name): Where a run's agent session stands, by state, as its run card says it.
- `runs.sessions.<name>` (string, localized): The session state's word, such as "dormant"; the key is a session state: active, dormant or closed.
- `areas` (list of strings): The areas a task may be in, the values of the area property.
- `contexts` (list of strings): The contexts the menu offers; a task may hold others.
- `stale_waiting_days` (integer): Working days a task waits before it is stale.

## Views and the review (`views.toml`)

- `empty` (template, localized): What a view without its own empty shows when it lists nothing, over the view's label as `{{.label}}`. Required in the bundled profile; a user layer may replace it.
- `default_sort` (string, localized): What clients call a view's own order in the sort menu, such as "View order". Required in the bundled profile; a user layer may replace it.
- `lens` (table): What every list's rows and toolbar say. Each is a template whose only actions are the `{{.name}}` its doc lists, which the clients fill in. Required in the bundled profile; a user layer may replace any.
- `lens.default_group` (template, localized): The group menu's entry for the field a view groups by on its own, over the field's label as `{{.label}}`.
- `lens.board_group` (template, localized): The group menu's entry for the field a board groups by when nothing is picked, over the field's label as `{{.label}}`.
- `lens.no_group` (string, localized): The group menu's entry that lists the view in one group.
- `lens.copied` (template, localized): What Copy list says it copied, over `{{.count}}` items and the `{{.view}}` they came from.
- `lens.copied_grouped` (template, localized): What Copy list says it copied from a grouped list, over `{{.count}}`, `{{.view}}` and the `{{.group}}` field's label.
- `lens.tag_key` (template, localized): The name of the field that groups by one key of the tag facet's `key:value` values, over the tag facet's `{{.label}}` and the `{{.key}}`.
- `lens.tag_key_empty` (template, localized): The group of the items with no tag of that key, over the `{{.key}}`.
- `lens.grouped` (template, localized): What the terminal says after a group pick, over the picked entry's `{{.label}}`.
- `lens.sorted` (template, localized): What the terminal says after a sort pick, over the picked entry's `{{.label}}`.
- `lens.descending` (template, localized): The terminal's sort entry for a field in descending order, over the field's `{{.label}}`.
- `lens.no_lens` (string, localized): What the terminal says when a group or sort pick is asked where no list is on screen.
- `facets` (table by name): The task facets, by id: the fields every client filters, groups and sorts tasks by, in the order the file declares them, a user layer's new ones after the bundled. A plugin's row facet of the same id must agree on group, sort, rank, match and buckets.
- `facets.<name>` (table): A facet, by its id. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `facets.<name>.fact` (fact): The fact the facet reads: a fact of a task (task.*, task.prop.*), or a field of a record kind, written &lt;kind&gt;.&lt;field&gt; (brief.risk). Required.
- `facets.<name>.label` (string, localized): The name people read. Required.
- `facets.<name>.group` (bool, default `false`): Whether a list can group by the facet.
- `facets.<name>.sort` (one of `rank`, `text`, `time`): How the facet's values order: by rank, as text, or as instants (time). Absent, as text.
- `facets.<name>.rank` (list of strings): The facet's values in their order, first first.
- `facets.<name>.values` (table by name): The text people read for a value, by value.
- `facets.<name>.values.<name>` (string, localized): The text of one value.
- `facets.<name>.label_prefix` (string, localized): Text before the label of each value the facet shows on a row, such as the `@` of a context, never in a group's head; a label that has it already keeps it, as a context configured as `@calls` does.
- `facets.<name>.show` (one of ``, `ref`, `meta`, `flag`, `who`, `kind`, `age`, `aside`, or a table): The part of a row the facet's values show in, each a button that filters by its value: a part's name, or a table of one part to the values it shows there, such as `{ flag = ["paused", "muted"] }`, or `""` to take a facet a lower layer places off the row. Absent or `""`, the facet is only for filters, groups and sorts. A value `none` and a time facet's item without a time never show.
  - ``: Nowhere: the facet is for the lens alone, as when a user layer takes a lower layer's placement off the row.
  - `ref`: Beside the title, before the unit's own label: where it lives.
  - `meta`: Under the title: who opened it, its contexts, its brief's type, size, risk and tags.
  - `flag`: Under the title, after the meta: a state worth a glance, such as paused.
  - `who`: Whom it waits on.
  - `kind`: Its situation or state, the pill under whom it waits on.
  - `age`: How long since the facet's instant, for a facet that sorts by time; only the first such facet shows.
  - `aside`: Beside the reason: its workspace and links.
- `facets.<name>.show_values` (one of `all`, `first`, default `all`): How many of an item's values the part shows: all, or the first alone.
  - `all`: Every value, in the item's order.
  - `first`: The first value alone.
- `facets.<name>.show_when` (table by name): Shows the facet only on a row that holds, in each facet it names, one of the listed values, such as `{ turn = ["me"] }`; each named facet must be declared. Two age facets with opposite show_when place the row's one date by whom it waits on.
- `facets.<name>.show_when.<name>` (list of strings): The values one facet must hold.
- `facets.<name>.show_max` (integer): How many of an item's values the part shows at most; the rest count into one piece in the `more` row word. Absent or 0, all.
- `facets.<name>.show_label` (template, localized): For a facet shown in the age part: the label of its date, a template over `{{.age}}`, the age in the row words, and `{{.at}}`, the time, which each client writes in its own locale, such as `since {{.at}}`. Absent, the age alone.
- `facets.<name>.empty` (string, localized): What a group of tasks with no value shows.
- `facets.<name>.match` (one of `exact`, `suffix`, `prefix`, `team`, default `exact`): How a filter's text matches a value: the whole value, its last segment, its first, or a person's team.
- `facets.<name>.menu` (bool, default `true`): Whether the facet is offered in the filter menu.
- `facets.<name>.strip` (bool, default `false`): Whether each row's strip is colored by the facet's first value, its rank the strip's legend.
- `facets.<name>.colors` (table by name): For the facet that colors the strip: the hue of each value's strip and dot, by value; a value it does not name takes nobody's.
- `facets.<name>.colors.<name>` (one of `me`, `author`, `reviewer`, `loop`, `nobody`): One value's hue.
  - `me`: Yours: the next step is the viewer's.
  - `author`: Someone else's, such as a pull request's author.
  - `reviewer`: Another person's, apart from the author, such as a reviewer.
  - `loop`: The loop's: an agent works on it or is due to.
  - `nobody`: Nobody's: a faint strip.
- `facets.<name>.buckets` (table by name): For a facet that sorts by time: the buckets its instants fall in, by id, in the order the file declares them; the first whose within covers the time since the instant wins, and one with no within takes the rest.
- `facets.<name>.buckets.<name>` (table): A bucket, by its id.
- `facets.<name>.buckets.<name>.within` (duration): How long ago an instant may be and still fall in the bucket.
- `facets.<name>.buckets.<name>.label` (string, localized): The text people read; the id when absent.
- `facets.<name>.merge` (one of `union`, `rows-first`, `own`, default `union`): How a task's value joins its units' rows' values: union adds it to them, rows-first uses it only when no row has a value, own keeps it alone.
- `facets.<name>.units` (one of `all`, `primary`, default `all`): Which of a task's units contribute rows' values: all of them, or the primary unit alone.
- `views` (table): The lists of tasks, by view.
- `views.<name>` (table): A view, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `views.<name>.label` (string, localized): The name people read in the list of views. Required.
- `views.<name>.summary` (string, localized): One line on what the view lists: its tooltip in the list of views, its subtitle, and its key's help line. Required.
- `views.<name>.empty` (string, localized): What the view shows when it lists nothing; the top-level empty when absent. A filter that matches nothing says so in its own words.
- `views.<name>.over` (one of `tasks`, `gates`, default `tasks`): What the view lists: tasks, or gates.
- `views.<name>.when` (predicate): Which tasks or gates the view lists, over their facts: a task's, its properties' (task.prop.*), or a gate's. A view with none lists nothing: a place in the clients' list of views that a client draws itself, such as the review.
- `views.<name>.group` (string): The fact the view groups by, such as task.owed_by.
- `views.<name>.sort` (string): The facts the view sorts by, comma-separated; a leading - sorts one descending. A view's tasks start oldest first, a parent's children in their sibling order.
- `views.<name>.key` (string): The key that jumps to the view: `g`, a space and a key that no core key uses, such as `g i`. Its help is the view's label, and its help line the view's summary.
- `views.<name>.hide_disabled` (bool, default `false`): The view hides the tasks whose units are all of a disabled plugin, and the gates on a disabled plugin's units.
- `views.<name>.rail` (bool, default `true`): The rail lists the view; false keeps it for the home, a queue or search.
- `views.<name>.remove_clean` (bool, default `false`): The view offers to remove at once the workspaces its tasks' links hold whose checkouts hold nothing that is not published, as each link's remove action does; the rest are kept, with why.
- `views.<name>.layout` (one of `list`, `board`, `outbox`, default `list`): How the cockpit draws the view: list, its rows; board, a lane per group, which a person may switch back to the list; outbox, the Outbox's drafts grouped by the workflow or purpose that proposed them, read from the Outbox's own route whatever the view lists.
- `badge` (table): The menu bar's badge.
- `badge.views` (list of strings): The views whose counts the badge adds up, each a view of views.
- `review` (table): The review's steps, in the order the review walks them.
- `review.<name>` (table): A step of the review, by the view it walks, which views declares. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones, and comes after the steps below it.
- `review.<name>.label` (string, localized): What the step asks of you, as the review lists it. Required.
- `lens.age_now` (template, localized): A row's age under a minute.
- `lens.age_minutes` (template, localized): A row's age under an hour, over its minutes as `{{.m}}`.
- `lens.age_hours` (template, localized): A row's age under a day, over its hours `{{.h}}` and the minutes past them `{{.m}}`.
- `lens.age_days` (template, localized): A row's age from a day, over its days `{{.d}}` and the hours past them `{{.h}}`.
- `lens.filter_label` (template, localized): What a facet button on a row says to a screen reader, over the facet's `{{.label}}` and the `{{.value}}`.
- `lens.filter_title` (template, localized): A facet button's tooltip, over the facet's `{{.label}}` and the `{{.value}}`.
- `lens.filter_title_group` (template, localized): A facet button's tooltip when its facet groups, over `{{.label}}`, `{{.value}}` and the `{{.group}}` field's label.
- `lens.filter_group` (template, localized): What a facet button calls whom a row waits on when the value is a group of people, such as a Slack user group, in place of the facet's label.
- `lens.list_count` (template, localized): A row view's summary, over its `{{.unit}}`, how many rows the filter `{{.shown}}` and how many the view holds, `{{.total}}`.
- `lens.list_empty` (template, localized): What a plugin view's list says when it holds no row and the plugin gives no words of its own.
- `lens.countdown_title` (template, localized): The tooltip of a pending round's countdown, over the `{{.time}}` it sends at.
- `lens.countdown_sending` (template, localized): A pending round's countdown once its deadline passed.
- `lens.countdown_seconds` (template, localized): A pending round's countdown under 100 seconds, over the seconds `{{.s}}`.
- `lens.countdown_minutes` (template, localized): A pending round's countdown from 100 seconds, over its minutes `{{.m}}` and the seconds past them, two digits, `{{.s}}`.
- `lens.tip_named` (template, localized): A row's tooltip's start when it names whom the row waits on, over the `{{.strip}}` value's label and `{{.who}}`.
- `lens.tip_kind` (template, localized): A row's tooltip with its situation, over the `{{.tip}}` so far and the `{{.kind}}` labels.
- `lens.tip_why` (template, localized): A row's tooltip with why it waits, over the `{{.tip}}` so far and the `{{.why}}`.
- `lens.legend_title` (template, localized): The tooltip of the legend of a view's strip, the colors of its strip facet's values.
- `lens.sort_by` (template, localized): The tooltip of the list toolbar's Sort chip, over the `{{.label}}` of the facet it sorts by.
- `lens.col_unit` (template, localized): What a row view whose rows mix kinds calls its unit, as `list_count`'s `{{.unit}}`.
- `lens.stale` (template, localized): The flag of a row whose plugin is not reporting.
- `lens.stale_title` (template, localized): The stale flag's tooltip.
- `lens.briefing` (template, localized): The flag of a row whose intake waits for its brief.
- `lens.briefing_title` (template, localized): The briefing flag's tooltip, over the intake's `{{.seq}}`.
- `lens.briefing_attempt` (template, localized): What the briefing flag's tooltip adds of a retried intake, over the attempt `{{.n}}`.
- `lens.briefing_error` (template, localized): What the briefing flag's tooltip adds of an intake's last error, over the `{{.error}}`.
- `lens.questions` (template, localized): The pill of a brief's open questions, over their number `{{.n}}`.
- `lens.questions_title` (template, localized): That pill's tooltip, over `{{.n}}`.
- `lens.starred` (template, localized): The tooltip of a starred task's star.
- `lens.until` (template, localized): The flag of a task asleep until a date, over the `{{.date}}`.
- `lens.due` (template, localized): The flag of a task's deadline, over the `{{.date}}`.
- `lens.unread` (template, localized): The flag of a task whose links hold messages the extraction has not read, over how long the oldest waited, `{{.lag}}`.
- `lens.unread_title` (template, localized): The unread flag's tooltip.
- `lens.review_due` (template, localized): The flag of a project past its review.
- `lens.no_next_action` (template, localized): The flag of a project with no open child.
- `lens.open_children` (template, localized): A project's open children, over their number `{{.n}}`.
- `lens.sequential` (template, localized): The flag of a project that takes its children one at a time.
- `lens.every` (template, localized): How often a project is reviewed, over the `{{.every}}`.
- `lens.reviewed` (template, localized): When a project was last reviewed, over the `{{.date}}`.
- `lens.more` (template, localized): The piece that counts the values a facet's `show_max` leaves off a row, over their number `{{.n}}`.
- `lens.col_who` (retired, not read): The head of the list column of whom a row waits on.
- `lens.col_age` (retired, not read): The head of the list column of a row's age.
- `lens.col_why` (retired, not read): The head of the list column of why a row waits.
- `lens.col_aside` (retired, not read): The head of the list column of a row's workspace and links.
- `lens.col_actions` (retired, not read): The head of the list column of a row's actions.
- `lens.changed` (retired, not read): The flag of a task that changed since you last opened it.
- `facets.<name>.show.ref` (list of strings): The values the facet shows in the ref part.
- `facets.<name>.show.meta` (list of strings): The values the facet shows in the meta part.
- `facets.<name>.show.flag` (list of strings): The values the facet shows in the flag part.
- `facets.<name>.show.who` (list of strings): The values the facet shows in the who part.
- `facets.<name>.show.kind` (list of strings): The values the facet shows in the kind part.
- `facets.<name>.show.age` (list of strings): The values the facet shows in the age part.
- `facets.<name>.show.aside` (list of strings): The values the facet shows in the aside part.

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
  - `step`: A workflow's gate.open: resolving it records the event gate_resolved on its task, which workflows react to.
- `gates.<name>.label` (string, localized): What the gate asks of you, as clients list it.
- `gates.<name>.text` (template, localized): The words of the gate's subject when the loop writes them, over the proposal facts: the first proposal gate words an item proposal of the ledger with it, and a verb's gate the proposal of its verb. A step gate's is over the workflow facts of its unit and of the units its task holds, such as `{{.pr.slug}}`, rendered as its gate.open opens it.
- `gates.<name>.card_says` (bool, default `false`): The gate's card lists what each of its intents does, its summary, under what it asks; false leaves the summaries to help and palettes.
- `gates.<name>.closes_with` (string): The intent, one that refuses the gate, the gate closes with when you end the task it is on. While the gate is open, nothing else ends the task: its items resolving leave it open, and a workflow's verb that would end it is refused, until you settle the gate. The task's unit ending (a pull request merged or closed) supersedes the gate as moot. Empty or left out, the gate neither holds its task nor closes with it.
- `gates.<name>.intents` (table by name): What you may resolve the gate with, by intent, in the order clients offer them.
- `gates.<name>.intents.<name>` (table): An intent, by its name. A user layer may set only some keys of an entry declared below it; a new entry sets the required ones.
- `gates.<name>.intents.<name>.label` (string, localized): The name people read on its button.
- `gates.<name>.intents.<name>.summary` (string, localized): What resolving the gate with the intent does, as help, palettes and the gate's card show it.
- `gates.<name>.intents.<name>.note` (template, localized): What the intent would do on a row that offers it, over the action facts.
- `gates.<name>.intents.<name>.confirm` (template, localized): The dialog that confirms the intent on a row, over the action facts; empty asks nothing.
- `gates.<name>.intents.<name>.status` (one of `approved`, `rejected`, `answered`, `dismissed`, `superseded`): The status the intent resolves the gate to; required for a new intent.
- `gates.<name>.intents.<name>.answer` (bool, default `false`): The intent takes text, such as a question's answer.
- `gates.<name>.ends_task` (table by name): What closing the gate does to a task the loop opened only to hold gates, by the status it closes with (superseded when its subject is withdrawn), once none of the task's gates is open; a status left out leaves the task open, and a user layer sets only the statuses it names.
- `gates.<name>.ends_task.<name>` (one of `done`, `dropped`): The category the task ends in.
  - `done`: Done: the task leaves the lists for the Logbook.
  - `dropped`: Dropped: the task goes to the trash.
- `effects` (table by name): What sending a draft does besides posting it, one sentence per effect, by effect name; a draft's card shows the sentences of the effects it carries. A user layer sets only the effects it names; an empty sentence takes a lower layer's off.
- `effects.<name>` (string, localized): The sentence, such as "The task is marked as nudged."; the key is an effect a draft may carry.
- `drafts` (table): The words a draft's card and the Outbox name drafts with. A user layer sets only the words it names; an empty word takes a lower layer's off.
- `drafts.by_hand` (string, localized): The title of the Outbox's group of the drafts no workflow proposed and no purpose names: those written by hand.
- `drafts.by_hand_about` (string, localized): One line under that title.
- `drafts.quoted` (string, localized): What a draft's card says of a draft posted in the direct conversation of the person it is to, which quotes items of another conversation: `{conversation}` names that conversation.
- `drafts.stale` (string, localized): What a draft's card says of a draft written against what its unit no longer is, such as an older head of a pull request; it is also why its approval is off.
- `drafts.hold` (string, localized): What a draft's card says of a held draft its proposer asked no approval rule to send.
- `drafts.authors` (table by name): Who wrote a draft's current content, by author, as its card says it; an author with no words shows nothing.
- `drafts.authors.<name>` (string, localized): The author's words, such as "Revised by an agent"; the key is person, session, rule or window.
- `drafts.purposes` (table by name): What a draft is for, by the purpose its proposer names (nudge, close, ...): its card's head, and the title of the Outbox's group of the drafts of that purpose no workflow proposed. A purpose with no word shows as it is named.
- `drafts.purposes.<name>` (string, localized): The purpose's word, such as "Nudge".
- `drafts.states` (table by name): Where a draft's delivery stands, by state, as its card says it.
- `drafts.states.<name>` (string, localized): The state's words, such as "failed to send"; the key is a draft state: held, queued, sending, sent, failed or dropped.
- `drafts.reasons` (table by name): Why a dropped draft was withdrawn, by reason, as the Outbox says it.
- `drafts.reasons.<name>` (string, localized): The reason's words; the key is a withdraw reason: assigned, moot, outside, joined, ended, skipped or superseded.
- `approval` (an [approval table](#approval-tables) of gate modes): How a gate is decided when no manifest or configuration says: the default of each kind. A user layer's mode replaces the mode and its window, its rules replace the rules.

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

- `help` (table): The help line of each core key, by context (list, global, question, review, plan, detail), then action (`capture`, `done`); a numbered one as `answer:{n}`, `{n}` its number.
- `help.<name>` (table): The keys of one context, by action.
- `help.<name>.<name>` (string, localized): What the key does, as the help and the palette list it. Required.

## Workflow manifest (`workflows/<name>/workflow.toml`)

### What it matches

- `summary` (string, localized): One line, what the workflow does; its plan items' description. Required.
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
- `enabled` (bool, default `true`): false ships the workflow off: it plans nothing until workflows.&lt;name&gt;.enabled = true in the config turns it on.
- `limits` (table): How many runs the workflow may take.
- `limits.per_run` (integer, default `0`): Runs per evaluation; 0 is no limit.
- `limits.per_day` (integer, default `0`): Runs per local day; 0 is no limit.
- `limits.per_unit_per_day` (integer, default `0`): Runs on one unit per local day; 0 is no limit.
- `limits.cooldown` (duration): The least time between runs on one unit.

### Steps

- `step[]` (table): A step the run takes, in order. Required: at least one.
- `step[].use` (one of `session.close`, `workspace.remove`, `workspace.unregister`, `turn.dismiss`, `answer.refuse`, `session.mark_dormant`, `session.close_orphan`, `turn.start`, `draft.propose`, `classify`, `notify.self`, `task.verb`, `gate.open`, `items.resolve`, `task.set`): The step.
  - `session.close`: Closes the agent session.
  - `workspace.remove`: Removes the workspace, after its guards. Never with `force`: a manifest that sets it fails to load.
  - `workspace.unregister`: Unregisters a workspace whose path is gone.
  - `turn.dismiss`: Dismisses the open turn, recording the reason.
  - `answer.refuse`: Refuses a queued answer whose route is gone.
  - `session.mark_dormant`: Marks dormant an active session whose process its manager's census no longer lists, or has listed orphaned with no agent for a fast job (`session.process_orphaned`) when no turn is sent or started in it, its agent neither works nor waits for permission, its workspace is not focused and you did not prompt it since the loop's last turn: the session stays open, and the next turn, or a census that shows its process in a window again, wakes it. It records the state and ends no process.
  - `session.close_orphan`: Side effect: ends the process of an active session that the census proved a durable orphan (`session.orphan_durable`), a terminal no window of the app shows with no agent in it. It runs under `session.close`'s guards (no turn sent or started, the agent neither working nor waiting for permission, the workspace not focused, no prompt of yours since the loop's last turn) and records nothing on the session: the censuses that then miss the process mark it dormant. Refused for good on a session gone, closed or dormant.
  - `turn.start`: Starts a turn of `intent` on the unit, which the intent must serve; `stage` (`first` or `follow-up`) for a review; `runner` (`session` or `headless`), by default the intent's first runner. On a pull request, caps, the brief, busy and held checks apply as for any automatic round; on any other unit, the workflow's `limits.per_day` and `turns.per_day`.
  - `draft.propose`: Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Items`, `.Person` and `.Facts`, the subject's facts by name, such as `{{index .Facts "task.promised"}}`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`unit` or `person`), `target` (`unit` or `item`), `effects` (the effects sending it applies), `repeat_after` (the least time after a settled draft of the same person, a duration or a duration of the config such as `config.drafts.repeat_after`) `repeat_after_sent` (the same after a sent one, or `forever`) and `ended` (`hold`, the default, holds proposals of no task or of the same task while a draft's task a verb ended stays ended; `repeat` holds them for `repeat_after`). Holds while a draft of the workflow, unit and person is open or failed; a run that leaves a person under a delay that ends with time stays pending until it ends.
  - `classify`: A headless classification whose output becomes facts, `classify.&lt;workflow&gt;.&lt;field&gt;` with each - of the workflow's name an _, typed by the schema's top-level properties: `prompt` (a file in the workflow's directory, by default `prompt.md`) and `schema` (the JSON schema its answer follows, by default `schema.json`). With `with`, a list fact of task ids, it reads those tasks too and its prompt is a template over them. Event triggers only, cached by fingerprint, prompt and schema, under `classify.per_day`. Each run that reads its output records a `classified` event on the unit, whose data is the workflow's name.
  - `notify.self`: A private notification: `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Workflow`, `.Unit` and `.Facts`, the unit's facts by name, such as `{{index .Facts "pr.slug"}}`, none once the unit is gone).
  - `task.verb`: Runs a verb of tasks.toml on the task as the workflow: `verb`, one whose `by.workflow` lets a workflow run it, with the arguments its handler takes (`until` and `on_activity` for schedule, `on` and `resurface` for waiting, `set` for context and area, `by` for promise, `task` for merge, split, relate and unrelate). The plan blocks it while the task's state or the verb's `by` refuses it, and a verb that closes a task derived from a unit's items, which closes when they resolve. A verb whose `by.workflow` is `gate` opens the verb's gate instead, under the workflow's `[gates.approval]`: released by a rule, the verb runs as the workflow; approved by you, as you; a pair you took apart (`task.undone`) waits for you. It records the `verb_&lt;verb&gt;` event with the workflow as its actor and the chain it runs in, so another workflow may react to it within `trigger.depth`.
  - `gate.open`: Opens the gate `name` of gates.toml on the unit, a decision that waits for you with the intents gates.toml gives it.
  - `items.resolve`: Resolves every open item the task holds, as the workflow, with `resolution` and the workflow as its evidence, so a task derived from its units' items closes; an item you confirmed becomes a proposal, a gate on the task. Posts nothing.
  - `task.set`: Sets the task's property `prop` of tasks.toml to `value`, written by the workflow; a property the workflow may not write is refused.
- `step[].<name>` (string or list): The step's arguments.

### Gate approval

- `ask` (string, localized): What the plan gate of a run asks, a text template over `.Unit` (the unit as people read it) and `.Facts` (the unit's facts by name, such as `{{index .Facts "session.name"}}`); its summary when absent.
- `gates.drafts_wait` (bool, default `false`): Every draft the workflow proposes waits for a person: no approval rule of its own or of the configuration releases it.
- `gates.withdraw` (table): When an open gate the workflow's gate.open opened on a unit no longer stands: the engine supersedes it at the start of a sweep and records gate_withdrawn on its task with the reason, so a step whose subject is gone never waits for you to dismiss it.
- `gates.withdraw.when` (predicate): When an open gate the workflow opened is withdrawn, over its unit's facts; it may read no untrusted fact, since a withdrawal closes a gate that waits on you. Required.
- `gates.withdraw.reason` (string, localized): Why the gate was withdrawn, as its task's timeline shows it.
- `gates` (table): The gates its runs and drafts open.
- `gates.approval` (an [approval table](#approval-tables) of gate modes): The approval of its gates: a rule applies to the kinds its when pins gate.kind to, else to draft gates alone when it reads a draft's facts, else to every kind; what no rule or mode here decides, gates.toml's [approval] does.

### Drafts

- `drafts` (table): The drafts the workflow's steps and turns propose.
- `drafts.withdraw` (table): When an open draft the workflow proposed is withdrawn.
- `drafts.withdraw.when` (predicate): When an open draft the workflow proposed is withdrawn, over the unit's facts and the draft's.
- `drafts.withdraw.reason` (one of `assigned`, `moot`, `outside`, `joined`, default `moot`): The reason the withdrawn draft stores.
  - `assigned`: Its items got an owner.
  - `moot`: It no longer applies.
  - `outside`: It addresses someone who cannot read the conversation it posts in.
  - `joined`: The person it was written to in their own conversation joined the one it came from.
- `drafts.withdraw.rule[]` (table): A rule tried before when, in order, that withdraws with a reason of its own.
- `drafts.withdraw.rule[].when` (predicate): When the rule withdraws an open draft the workflow proposed, over the unit's facts and the draft's. Required.
- `drafts.withdraw.rule[].reason` (one of `assigned`, `moot`, `outside`, `joined`, default `moot`): The reason the draft the rule withdraws stores.
  - `assigned`: Its items got an owner.
  - `moot`: It no longer applies.
  - `outside`: It addresses someone who cannot read the conversation it posts in.
  - `joined`: The person it was written to in their own conversation joined the one it came from.
- `outbox` (table): How the Outbox heads the group of the drafts the workflow proposed.
- `outbox.title` (string, localized): The group's title, what the drafts are for, such as "Ask who hasn't answered"; the summary when absent.
- `outbox.about` (string, localized): One line under the title on when the workflow drafts them, such as "Drafted when a conversation where you asked something goes quiet."

## Steps and their arguments

- `session.close`: no arguments.
- `workspace.remove`: no arguments.
- `workspace.unregister`: no arguments.
- `turn.dismiss`:
  - `reason` (string): The reason the dismissed turn records.
- `answer.refuse`:
  - `reason` (string): The reason the refused answer records.
- `session.mark_dormant`: no arguments.
- `session.close_orphan`: no arguments.
- `turn.start`:
  - `intent` (string): The intent whose turn starts; it must serve the unit. Required.
  - `stage` (one of `first`, `follow-up`): The pass over the unit: its first, or one after; empty is the one the unit's plan holds due, one after before its first.
  - `due` (one of `plan`, `when`, default `plan`): When the round is due: plan, when the unit's plan holds it due; when, whenever the workflow's when matches, as a round to retry what a failed round left, whatever the plan holds.
  - `runner` (one of `session`, `headless`): How the turn runs, one of the intent's runners; by default its first.
  - `open_question` (one of `hold`, `replace_if_moved`, default `hold`): What an open question of the intent's newest turn does to a round of the unit's plan: hold waits for its answer; replace_if_moved runs a round planned on a head other than the one the question was asked on, which replaces the asked turn, supersedes its gate and carries the question in the new turn's history. An answer on its way holds the round either way.
- `draft.propose`:
  - `purpose` (string): What the draft is for, such as nudge or close. Required.
  - `delivery` (one of `comment`, `reaction`, `edit`, `review`, `reply`, `resolve`, `request_review`, `message`, `delete`, `unreaction`): How the draft posts, a delivery the plugin that posts it declares; empty is that plugin's default delivery.
  - `text` (file, default `text.md.tmpl`): The template of the draft's text, beside the manifest, rendered in the language of the draft's conversation.
  - `why` (file, default `why.md.tmpl`): The template of why the draft is proposed, beside the manifest, rendered in the user's language over the same data as the text; a workflow with no such file proposes with no why.
  - `reaction` (string): The emoji name, with delivery reaction.
  - `items` (one of `owed`, `resolved`, `unowned`, `unowned_aged`, `all`, default `all`): Which of the unit's items the draft lists.
  - `per` (one of `unit`, `person`, default `unit`): One draft per unit, or one per person it waits on.
  - `target` (one of `unit`, `item`, `person`, default `unit`): Where the draft posts: on the unit, on each item, or, with per = "person", in the direct conversation between you and the person it is to; an intent's help.target is free text.
  - `to` (one of `all`, `members`, `non_members`, default `all`): Whom of the people the items wait on the draft addresses: all of them, those its unit's plugin knows are members of the conversation it posts on, or those it knows are not; a person whose membership the plugin cannot tell is left out of both.
  - `effects` (list of `task.nudged`, `unit.closed`, `pr.reviewed`, `pr.nudged`): What sending the draft applies.
  - `task.nudged`: Writes the last_nudge property of the draft's task and of every open task holding one of the items it lists. It says the people were nudged, so it needs the draft's post.
  - `unit.closed`: Records the closure of the draft's unit through its plugin, which unwatches it after its warm period, then ends the open tasks that hold the unit. It applies without the draft's post too.
  - `pr.reviewed`: Writes the plugin mark reviewed: the reviews the round covered, once its review is submitted.
  - `pr.nudged`: Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft.
  - `repeat_after` (string): The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after.
  - `repeat_after_sent` (string): The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever.
  - `ended` (one of `hold`, `repeat`, default `hold`): What a draft withdrawn as its task ended does while that task stays ended: hold the workflow's proposals of no task or of that task, or hold them for repeat_after like any settled draft.
- `classify`:
  - `prompt` (file, default `prompt.md`): The classification's prompt, beside the manifest.
  - `schema` (file, default `schema.json`): The JSON schema of its answer, beside the manifest.
  - `with` (string): A list fact of task ids, such as task.candidates, whose tasks the prompt reads beside the unit: the prompt is then a template over .Facts, the unit's facts, .Task, the unit's own task when it is one, and .Tasks, the listed tasks in order, each with .ID, .Title, .Notes and .Items, a few of its items' summaries. Every untrusted value is inside this call's markers, .Begin and .End, which carry the code .Nonce. The output is cached by what the prompt reads of the unit's task and the listed tasks (ids, titles, notes, item summaries) and by the prompt and the schema, so a write that changes none of them, such as opening a task, keeps it, and an edit of the prompt or the schema asks again; the unit's facts a prompt prints through .Facts are not part of the key, and with a unit that is not a task the key holds the unit's fingerprint too. Only a reader that names it reads it. An empty list runs nothing.
- `notify.self`:
  - `text` (file, default `text.md.tmpl`): The template of the notification's text, beside the manifest: `.Workflow`, `.Unit` and `.Facts`.
- `task.verb`:
  - `verb` (string): The verb of tasks.toml run on the task, one whose by.workflow lets a workflow run it. Required.
  - `until` (string): With a verb whose handler is schedule, the date the task returns: RFC 3339 or YYYY-MM-DD.
  - `on_activity` (string): With a verb whose handler is schedule, `true` returns the task on new activity on its unit.
  - `on` (string): With a verb whose handler is waiting, the people the task waits on, comma-separated.
  - `resurface` (string): With a verb whose handler is waiting, the date the task resurfaces: RFC 3339 or YYYY-MM-DD.
  - `set` (string): With a verb whose handler is context, the contexts, comma-separated; with area, the area.
  - `by` (string): With a verb whose handler is promise, the time the task is promised by: HH:MM, YYYY-MM-DD or RFC 3339; empty clears it.
  - `task` (string): With a verb whose handler is merge, split, relate or unrelate, the fact that names the other task: a string fact holding its id, or a list fact whose first element does, such as task.link_target_tasks, or a classify output, such as classify.&lt;workflow&gt;.task, which no fact table declares before it runs; the step is refused while it names none.
  - `each` (string): With `task` naming a list fact, `true` runs the verb with every task the list names, in order, each opening its own gate where the verb is gated, so one that waits for you does not hold back the rest; else the verb runs with the first.
- `gate.open`:
  - `name` (string): The gate, of gates.toml, the step opens. Required.
- `items.resolve`:
  - `resolution` (one of `done`, `answered`, `fixed`, `delegated`, `declined`, `dismissed`, `superseded`, `moot`, default `done`): How the items resolved.
- `task.set`:
  - `prop` (string): The property, of tasks.toml, the step sets. Required.
  - `value` (string): The value, written as its type reads it: an enum's value, a date as YYYY-MM-DD or as a duration after now such as +2d, a duration, a list comma-separated. Required.

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
  - `task_post`: Posts a question or a note on the session's task during a turn, waiting on a gate.
  - `task_read`: Reads the session's task: the task, its brief, its open gates, the chat since the last read and the user's answers, and marks them read.
  - `draft_read`: Reads a draft on the session's turn's unit or proposed from it: its content, delivery state and revisions.
  - `draft_update`: Revises a draft an agent proposed on the session's turn's unit or from it, at the rev it read; the revision waits for the user's approval again.
  - `draft_withdraw`: Withdraws a draft an agent proposed on the session's turn's unit or from it, at the rev it read.
  - `github_draft`: Proposes a GitHub comment, review, review thread reply, thread resolution or review request for the user's approval.
  - `github_threads`: Reads the unresolved review threads of the session's pull request, with their comment ids, and each reviewer's latest review.
  - `slack_send_message`: Posts to Slack as the user, now.
  - `slack_read`: Reads a Slack thread or conversation.
  - `slack_search`: Searches the Slack messages the user can read.
  - `slack_draft`: Proposes a Slack message, a reaction or its removal, or an edit or deletion of the user's message, for the user's approval.
  - `slack_react`: Adds or removes a reaction on a Slack message as the user, now.
  - `slack_edit`: Replaces the text of one of the user's Slack messages, now.
  - `slack_delete`: Deletes one of the user's Slack messages, now.
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
- `task_post`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `text_required`: The text is missing.
  - `capped` (`Error`): The turn has too many posts waiting.
  - `not_in_turn` (`Error`): The session has no started turn.
  - `post_failed` (`Error`): The message could not be posted.
  - `posted` (`Gate`: number): The message is on the task, waiting on gate Gate.
- `task_read`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `no_turn` (`Error`): The session has no turn.
  - `no_gate` (`ID`: number): No answer of the session's resolved gate ID.
  - `not_the_tasks` (`Error`): The session's turns are on no unit of the task it would read.
  - `read_failed` (`Error`): The task could not be read.
  - `nothing` (`ID`: number): Nothing to read: the session's turns are on no task, and no answer of gate ID, or of any gate when ID is 0, waits.
  - `read` (`Task`, `Brief`, `Gates`, `Chat`, `Conversations`): What was read: the task, its brief record's data, its open gates, the chat since the last read and the conversations, each "" when empty.
  - `task` (`ID`, `Title`, `State`): A part of read: the task's id, title and state.
  - `gate` (`ID`: number, `Kind`, `Name`, `Intents`): A part of read: one open gate of the task, its kind, its name in gates.toml and the intents that resolve it.
  - `chat_message` (`Text`, `At`): A part of read: one message the user wrote on the task's chat, and when.
  - `conversation` (`Gate`: number, `Kind`, `Text`, `Options`, `Answers`): A part of read: a message, the gate its answers resolved (0 for one stored before answers named their gate), its kind (question or agent_post), its text and options, and its answers.
  - `turn_answers` (`Answers`): A part of read: answers to the session's turns stored before each answer named its message.
  - `option` (`ID`, `Label`): A part of conversation: one option of the message.
  - `answer` (`Option`, `Label`, `Choice`, `Text`, `At`): A part of conversation: one answer, with its option and the option's label, yes or no, its text and when it was given.
- `draft_read`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `read` (`Draft`): The draft, as JSON.
  - `not_found` (`Error`): No such draft.
  - `out_of_scope` (`Error`): The draft is neither on the session's turn's unit nor proposed from it, or, to revise, a person's or a workflow's.
  - `failed` (`Error`): The call failed.
- `draft_update`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `updated` (`Draft`): The revised draft, as JSON.
  - `rev_conflict` (`Error`): The draft is at another rev than the one given.
  - `stale` (`Error`): The basis given is not the unit's live one.
  - `not_found` (`Error`): No such draft.
  - `out_of_scope` (`Error`): The draft is neither on the session's turn's unit nor proposed from it, or, to revise, a person's or a workflow's.
  - `failed` (`Error`): The call failed.
- `draft_withdraw`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `withdrawn` (`Draft`): The withdrawn draft, as JSON.
  - `rev_conflict` (`Error`): The draft is at another rev than the one given.
  - `not_found` (`Error`): No such draft.
  - `out_of_scope` (`Error`): The draft is neither on the session's turn's unit nor proposed from it, or, to revise, a person's or a workflow's.
  - `failed` (`Error`): The call failed.
- `github_draft`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `failed` (`Error`): GitHub or the daemon failed the call.
  - `problem_kind` (`Kinds`): A problem: kind is none of the kinds.
  - `problem_text_required`: A problem: the kind needs a text, and it is missing or empty.
  - `problem_payload` (`Error`): A problem: the payload does not fit the kind's schema.
  - `problem_basis_required`: A problem: the kind needs the head commit the agent read as basis.
  - `stale` (`Basis`, `Head`): The basis is not the pull request's head now; nothing was drafted.
  - `proposed` (`ID`: number, `Rev`: number, `Unit`): The draft waits for the user's approval.
  - `approved` (`ID`: number, `Rev`: number, `Unit`): A rule of the user's approved the draft, and it will be posted.
  - `dry_run` (`ID`: number, `Rev`: number, `Unit`): The draft's approval is a dry run: it is stored and never posted.
- `github_threads`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `failed` (`Error`): GitHub or the daemon failed the call.
  - `threads` (`Unit`, `JSON`): The threads and reviews, as JSON.
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
  - `read` (`Text`): The messages read, rendered.
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
  - `found` (`Text`): The messages found, rendered.
- `slack_draft`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_kind` (`Kinds`): A problem: kind is none of the kinds.
  - `problem_reaction_required`: A problem: a react or unreact draft has no reaction.
  - `problem_message`: A problem: an edit or delete draft's target is not a message ref or permalink.
  - `problem_message_gone`: A problem: Slack holds no message at an edit or delete draft's target.
  - `problem_message_not_yours`: A problem: the message an edit or delete draft targets is not the user's.
  - `problem_text_required`: A problem: text is missing or empty.
  - `proposed` (`ID`: number, `Unit`, `Where`): The draft waits for the user's approval.
  - `approved` (`ID`: number, `Unit`, `Where`): The draft's policy approved it, and it will be posted.
  - `dry_run` (`ID`: number, `Unit`, `Where`): The draft's approval is a dry run: it is stored and never posted.
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
- `slack_delete`:
  - `refused` (`Error`): The call is refused before the tool runs, such as with no session.
  - `bad_arguments` (`Error`): The arguments do not decode; Error is the decoder's, "" when there is none.
  - `fix_these` (`Problems`): The arguments have problems, one per line.
  - `not_logged_in`: Slack is logged out.
  - `failed` (`Error`): Slack or the daemon failed the call.
  - `problem_message`: A problem: message is not a message ref or permalink.
  - `deleted` (`Conversation`, `Message`, `Permalink`, `Deleted`): The message is deleted: where, its ref and its permalink, "" when Slack gave none, and the text it held.

## What the loop tells a session (`notices.toml`)

- `<name>` (template): A notice, by its name: a template over its fields, `Name` and `Command`.
  - `answered`: Typed into an idle session, or the reason that blocks its stop while it works: the user answered Gate of the session's turn, 0 for an answer stored before answers named their gate, of the unit named as the loop's commands read it.
  - `chat`: Typed into a live session of a task when the user writes on the task's chat, or the reason that blocks its next stop while its agent works: the task's id, its own unit as the loop's commands read it, and Capable, whether the session loaded the MCP server. The notice carries no message: the agent reads it with task_read, or with the turn answer command.
  - `chat_not_yours`: Why turn answer refuses a session that has no turn on the task it names, or on a unit the task holds: the unit as the command reads it.
  - `chat_typed`: Tells the person that their chat message reached a live session of the task.
  - `chat_queued`: Tells the person that their chat message waits for the busy agent of a live session of the task, which is told of it when it stops.
  - `chat_started`: Tells the person that their chat message started a turn of Intent, Resumed when it resumes the task's last session.
  - `draft_sent`: Tells the person that the reply they sent at once, Draft, is approved and goes out.
  - `draft_by_rule`: Tells the person that a rule of theirs, Rule (its name as the gates record it, such as "workflows/review gates.approval rule 0"), posted draft Draft to Where, at Link once it is known; the notification and the draft's card show it.
  - `draft_saved`: Tells the person that the reply they wrote, Draft, waits for their approval.
  - `reply_placeholder`: The placeholder of a notification's input that saves a reply for approval.
  - `reply_button`: The button of a notification's input that saves a reply for approval.
  - `send_reply_placeholder`: The placeholder of a notification's input that sends a reply at once.
  - `send_reply_button`: The button of a notification's input that sends a reply at once.
  - `answer_placeholder`: The placeholder of a notification's input that answers an agent.
  - `answer_button`: The button of a notification's input that answers an agent.
  - `digest_title`: The title of the daily digest notification.
  - `digest_new`: The digest's first line: Count, the tasks of the badge's views that moved since the last digest.
  - `digest_view`: The digest's line for one view the badge counts: View, its label, and Count, its tasks.
  - `notify_inbox`: The title of the notification of a task in the Inbox that says what it is itself.
  - `notify_draft_for_me`: The button of an Inbox task's notification that delegates it to an agent headless.
  - `notify_reply`: The button of a notification whose text input saves a reply for approval, or answers an agent.
  - `notify_send_reply`: The button of a notification whose text input sends a reply at once.
  - `notify_pinned`: The line a question's notification adds when an answer to it approves a draft: the notification offers no such answer, and the question's card shows the draft.
  - `notify_question`: The title of the notification of an agent's open question.
  - `notify_proposal`: The title of the notification of an open proposal.
  - `notify_post`: The title of the notification of an agent's open post.
  - `notify_yes`: The button that answers yes to an agent's confirmation.
  - `notify_no`: The button that answers no to an agent's confirmation.
  - `notify_recommended`: The button of the option an agent recommends: Label, the option's label.
  - `notify_draft`: The title of the notification of a draft that waits for your approval.
  - `notify_draft_failed`: The title of the notification of a draft whose delivery failed.
  - `notify_open`: The button that opens the notification's task in the cockpit.
  - `notify_discard`: The button that discards a draft from its notification.
  - `verb_for_you`: Why a task's page, read from a session the user does not drive, offers Verb disabled: a verb acts for the user, so only the user runs it, or a session they drive.
  - `task_merged`: Why a verb sent to a task merged into another is refused: Task, the merged task's id, Into, the task it reads as now, and Verb, the verb sent. Split runs on such an id, and splits it out.
  - `nothing_to_pick`: Why a task's page offers Verb disabled when it picks from the tasks of Pick (merged or related) and the task lists none.
  - `record_owed`: The reason that blocks the stop of a session whose turn has no record: the unit, the turn's id and the directory a record file goes in.
  - `config_unavailable`: Why a request to change the configuration, such as stopping a workflow, is refused by a daemon that edits no config file.
  - `skips_unavailable`: Why a request to skip a workflow on a unit, or to undo it, is refused by a daemon that keeps no skips.
  - `workflow_unknown`: Why a request naming Workflow is refused when the profile loads no workflow of that name.
  - `attach_needs_session`: Why task attach refuses to run outside an agent session a workspace manager started: the task named as the command reads it, and the enabled workspace managers as displayed.

The fields each notice reads besides `Name` and `Command`:

  - `answered` (`Gate`: number, `Unit`)
  - `chat` (`Task`, `Unit`, `Capable`)
  - `chat_not_yours` (`Unit`)
  - `chat_typed`
  - `chat_queued`
  - `chat_started` (`Intent`, `Resumed`)
  - `draft_sent` (`Draft`: number)
  - `draft_by_rule` (`Draft`: number, `Where`, `Rule`, `Link`)
  - `draft_saved` (`Draft`: number)
  - `reply_placeholder`
  - `reply_button`
  - `send_reply_placeholder`
  - `send_reply_button`
  - `answer_placeholder`
  - `answer_button`
  - `digest_title`
  - `digest_new` (`Count`: number)
  - `digest_view` (`View`, `Count`: number)
  - `notify_inbox`
  - `notify_draft_for_me`
  - `notify_reply`
  - `notify_send_reply`
  - `notify_pinned`
  - `notify_question`
  - `notify_proposal`
  - `notify_post`
  - `notify_yes`
  - `notify_no`
  - `notify_recommended` (`Label`)
  - `notify_draft`
  - `notify_draft_failed`
  - `notify_open`
  - `notify_discard`
  - `verb_for_you` (`Verb`)
  - `task_merged` (`Task`, `Into`, `Verb`)
  - `nothing_to_pick` (`Verb`, `Pick`)
  - `record_owed` (`Unit`, `Turn`, `ScratchDir`)
  - `config_unavailable`
  - `skips_unavailable`
  - `workflow_unknown` (`Workflow`)
  - `attach_needs_session` (`Task`, `Managers`)

## A plugin's views, facets and situations (`ui.toml`)

- `views` (table): The words of the plugin's views, by view id.
- `views.<name>` (table): One view's words.
- `views.<name>.label` (string, localized): The view's name, as navigation lists it. Required.
- `views.<name>.unit` (string, localized): What one of the view's rows is, such as "Pull request", the unit column's heading; empty for a view whose rows mix kinds.
- `views.<name>.default_sort` (string, localized): What the sort menu calls a row view's own order, such as "Waiting longest".
- `views.<name>.search` (table): A custom view's search: the queries it runs at a press, the terms it toggles and the orders it offers.
- `views.<name>.search.default_sort` (string): The order a query with none is read in.
- `views.<name>.search.presets[]` (table): A query run at a press.
- `views.<name>.search.presets[].query` (string): The query, in the source's search syntax. Required.
- `views.<name>.search.presets[].label` (string, localized): Its words. Required.
- `views.<name>.search.filters[]` (table): A term the query toggles, or a window of recent activity.
- `views.<name>.search.filters[].term` (string): The term toggled in the query.
- `views.<name>.search.filters[].window` (integer): Days of recent activity the filter keeps, in place of a term.
- `views.<name>.search.filters[].label` (string, localized): Its words. Required.
- `views.<name>.search.sorts[]` (table): An order the search offers.
- `views.<name>.search.sorts[].sort` (string): The order's value in the query. Required.
- `views.<name>.search.sorts[].label` (string, localized): Its words on its button. Required.
- `views.<name>.search.sorts[].shown` (string, localized): How a result names the order, such as "waiting longest".
- `views.<name>.search.decisions` (table): How a result's decision reads, by value.
- `views.<name>.search.decisions.<name>` (string, localized): One decision's words.
- `views.<name>.search.no_decision` (string, localized): What a result with no decision reads.
- `facets` (table): The words of the plugin's facets, by facet id.
- `facets.<name>` (table): One facet's words.
- `facets.<name>.label` (string, localized): The facet's name, as the filter menu and the groups show it. Required.
- `facets.<name>.show` (one of ``, `ref`, `meta`, `flag`, `who`, `kind`, `age`, `aside`, or a table): The part of a row the facet's values show in, each a button that filters by its value: a part's name, or a table of one part to the values it shows there, such as `{ flag = ["paused", "muted"] }`, or `""` to take a facet a lower layer places off the row. Absent or `""`, the facet is only for filters, groups and sorts. A value `none` and a time facet's item without a time never show.
  - ``: Nowhere: the facet is for the lens alone, as when a user layer takes a lower layer's placement off the row.
  - `ref`: Beside the title, before the unit's own label: where it lives.
  - `meta`: Under the title: who opened it, its contexts, its brief's type, size, risk and tags.
  - `flag`: Under the title, after the meta: a state worth a glance, such as paused.
  - `who`: Whom it waits on.
  - `kind`: Its situation or state, the pill under whom it waits on.
  - `age`: How long since the facet's instant, for a facet that sorts by time; only the first such facet shows.
  - `aside`: Beside the reason: its workspace and links.
- `facets.<name>.show_values` (one of `all`, `first`, default `all`): How many of an item's values the part shows: all, or the first alone.
  - `all`: Every value, in the item's order.
  - `first`: The first value alone.
- `facets.<name>.show_when` (table by name): Shows the facet only on a row that holds, in each facet it names, one of the listed values, such as `{ turn = ["me"] }`; each named facet must be declared. Two age facets with opposite show_when place the row's one date by whom it waits on.
- `facets.<name>.show_when.<name>` (list of strings): The values one facet must hold.
- `facets.<name>.show_max` (integer): How many of an item's values the part shows at most; the rest count into one piece in the `more` row word. Absent or 0, all.
- `facets.<name>.show_label` (template, localized): For a facet shown in the age part: the label of its date, a template over `{{.age}}`, the age in the row words, and `{{.at}}`, the time, which each client writes in its own locale, such as `since {{.at}}`. Absent, the age alone.
- `facets.<name>.label_prefix` (string, localized): Text before the label of each value the facet shows on a row, such as `by ` before an author; a label that has it already keeps it.
- `facets.<name>.row_class` (table): The class of a row that holds a value, by value, such as `{ me = "mine" }`, which the cockpit's stylesheet colors.
- `facets.<name>.row_class.<name>` (string): One value's class.
- `facets.<name>.colors` (table): For the facet that colors the strip: the hue of each value's strip and dot, by value; a value it does not name takes nobody's.
- `facets.<name>.colors.<name>` (one of `me`, `author`, `reviewer`, `loop`, `nobody`): One value's hue.
  - `me`: Yours: the next step is the viewer's.
  - `author`: Someone else's, such as a pull request's author.
  - `reviewer`: Another person's, apart from the author, such as a reviewer.
  - `loop`: The loop's: an agent works on it or is due to.
  - `nobody`: Nobody's: a faint strip.
- `facets.<name>.empty` (string, localized): What a row with no value shows.
- `facets.<name>.values` (table): How each value shows, by value; a value it does not name shows as itself.
- `facets.<name>.values.<name>` (string, localized): One value's text.
- `facets.<name>.buckets` (table): How each age bucket of a time facet shows, by bucket id.
- `facets.<name>.buckets.<name>` (string, localized): One bucket's text.
- `lens` (table): The plugin's own words of its rows, replacing the row words of views.toml's [lens] by the same keys.
- `situations` (table): How each situation the plugin derives shows, by kind, in a few words.
- `situations.<name>` (string, localized): One situation's words.
- `reasons` (table): Why a unit waits, as its row says it, by the reason key its situation names; a reason with no words shows as the plugin wrote it.
- `reasons.<name>` (string, localized): One reason's words, `{{.name}}` where a value the situation gives goes, such as `changes requested by {{.actor}}`.
- `signals` (table): How each line of the plugin's cards shows, by the signal's key: the plugin sends the key, a value code, the values its words fill and the codes of its notes; a code with no words shows as itself.
- `signals.<name>` (table): One signal's words.
- `signals.<name>.label` (string, localized): The signal's name, before its value. Required.
- `signals.<name>.values` (table): How each value shows, by code.
- `signals.<name>.values.<name>` (string, localized): One value's words, `{{.name}}` where a value the signal gives goes, such as `failing: {{.names}}`.
- `signals.<name>.values_from` (one of `situations`): Where the signal's values take their words from besides its own values.
  - `situations`: The signal's value is a situation kind, worded as [situations] words it unless its own values word it.
- `signals.<name>.notes` (table): How each note shows after the signal's value, in the note's own tone, by code.
- `signals.<name>.notes.<name>` (string, localized): One note's words, `{{.name}}` where a value the note gives goes and `{{plural .name "one" "…" "other" "…"}}` its plural form, such as `stalled for {{.days}} {{plural .days "one" "working day" "other" "working days"}}`.
- `events` (table): How each event kind the plugin declares shows on a timeline, by kind.
- `events.<name>` (table): One event kind's words.
- `events.<name>.label` (string, localized): The event in a few words. Required.
- `events.<name>.loop` (bool): The event is the loop's doing, which the activity's loop filter keeps; its tone is "loop" when it sets no other.
- `events.<name>.tone` (one of ``, `loop`, `me`, `people`): The tone the timeline and the activity draw the event in.
  - ``: No tone.
  - `loop`: The loop's or an agent's doing.
  - `me`: Your doing, or what waits on you.
  - `people`: Someone else's doing.
- `events.<name>.data` (one of ``, `none`, `first_field`, `short_sha`, `requested`): How the event's data shows on a timeline.
  - ``: Its data as it is.
  - `none`: No data.
  - `first_field`: Its data up to the first colon.
  - `short_sha`: The first seven characters of a commit id.
  - `requested`: Whom it asks or stops asking: shown as from that person or team, not as data.
- `drafts` (table by name): The words of a draft's payload on its card, by the draft kind the manifest declares; a field with no word shows as its key, a value with none as itself.
- `drafts.<name>` (table): One draft kind's words.
- `drafts.<name>.fields` (table by name): The payload's fields, by the key they have in the payload, at any depth.
- `drafts.<name>.fields.<name>` (table): One field's words.
- `drafts.<name>.fields.<name>.label` (string, localized): The field's name on the card.
- `drafts.<name>.fields.<name>.values` (table by name): The words of the field's values, by value, such as an enum's; the card keeps the value itself in its tooltip.
- `drafts.<name>.fields.<name>.values.<name>` (string, localized): The value's word.
- `detail` (table): How a field of the plugin's unit detail shows, by field.
- `detail.<name>` (table): One field's words.
- `detail.<name>.label` (string, localized): The field's name. Required.
- `detail.<name>.title` (string, localized): What the field means, as its tooltip shows it.
- `menubar` (table): The heading of each menu bar section, by section id.
- `menubar.<name>` (string, localized): One section's heading.
- `decisions` (table): The plugin's words of the decisions its units wait on, by decision kind, which replace the kind's own when every unit counted is the plugin's.
- `decisions.<name>` (table): One decision kind's words.
  - `answer`: An agent's question or a post waits for the user's answer.
  - `approve-plan`: An agent's plan waits for the user's approval.
  - `send-draft`: A draft waits for the user to send, edit or drop it.
  - `review-step`: A step of a review waits for the user's look.
  - `last-step`: A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.
  - `pick-someone`: A unit waits for the user to pick who takes it next, such as a reviewer.
  - `act`: A proposal or a trust request waits for the user to act on it.
- `decisions.<name>.queue` (string, localized): The title of the kind's queue, such as "Ready to merge".
- `decisions.<name>.empty` (string, localized): What the kind's queue says when it holds nothing.
- `lens.age_now` (template, localized): A row's age under a minute.
- `lens.age_minutes` (template, localized): A row's age under an hour, over its minutes as `{{.m}}`.
- `lens.age_hours` (template, localized): A row's age under a day, over its hours `{{.h}}` and the minutes past them `{{.m}}`.
- `lens.age_days` (template, localized): A row's age from a day, over its days `{{.d}}` and the hours past them `{{.h}}`.
- `lens.filter_label` (template, localized): What a facet button on a row says to a screen reader, over the facet's `{{.label}}` and the `{{.value}}`.
- `lens.filter_title` (template, localized): A facet button's tooltip, over the facet's `{{.label}}` and the `{{.value}}`.
- `lens.filter_title_group` (template, localized): A facet button's tooltip when its facet groups, over `{{.label}}`, `{{.value}}` and the `{{.group}}` field's label.
- `lens.filter_group` (template, localized): What a facet button calls whom a row waits on when the value is a group of people, such as a Slack user group, in place of the facet's label.
- `lens.list_count` (template, localized): A row view's summary, over its `{{.unit}}`, how many rows the filter `{{.shown}}` and how many the view holds, `{{.total}}`.
- `lens.list_empty` (template, localized): What a plugin view's list says when it holds no row and the plugin gives no words of its own.
- `lens.countdown_title` (template, localized): The tooltip of a pending round's countdown, over the `{{.time}}` it sends at.
- `lens.countdown_sending` (template, localized): A pending round's countdown once its deadline passed.
- `lens.countdown_seconds` (template, localized): A pending round's countdown under 100 seconds, over the seconds `{{.s}}`.
- `lens.countdown_minutes` (template, localized): A pending round's countdown from 100 seconds, over its minutes `{{.m}}` and the seconds past them, two digits, `{{.s}}`.
- `lens.tip_named` (template, localized): A row's tooltip's start when it names whom the row waits on, over the `{{.strip}}` value's label and `{{.who}}`.
- `lens.tip_kind` (template, localized): A row's tooltip with its situation, over the `{{.tip}}` so far and the `{{.kind}}` labels.
- `lens.tip_why` (template, localized): A row's tooltip with why it waits, over the `{{.tip}}` so far and the `{{.why}}`.
- `lens.legend_title` (template, localized): The tooltip of the legend of a view's strip, the colors of its strip facet's values.
- `lens.sort_by` (template, localized): The tooltip of the list toolbar's Sort chip, over the `{{.label}}` of the facet it sorts by.
- `lens.col_unit` (template, localized): What a row view whose rows mix kinds calls its unit, as `list_count`'s `{{.unit}}`.
- `lens.stale` (template, localized): The flag of a row whose plugin is not reporting.
- `lens.stale_title` (template, localized): The stale flag's tooltip.
- `lens.briefing` (template, localized): The flag of a row whose intake waits for its brief.
- `lens.briefing_title` (template, localized): The briefing flag's tooltip, over the intake's `{{.seq}}`.
- `lens.briefing_attempt` (template, localized): What the briefing flag's tooltip adds of a retried intake, over the attempt `{{.n}}`.
- `lens.briefing_error` (template, localized): What the briefing flag's tooltip adds of an intake's last error, over the `{{.error}}`.
- `lens.questions` (template, localized): The pill of a brief's open questions, over their number `{{.n}}`.
- `lens.questions_title` (template, localized): That pill's tooltip, over `{{.n}}`.
- `lens.starred` (template, localized): The tooltip of a starred task's star.
- `lens.until` (template, localized): The flag of a task asleep until a date, over the `{{.date}}`.
- `lens.due` (template, localized): The flag of a task's deadline, over the `{{.date}}`.
- `lens.unread` (template, localized): The flag of a task whose links hold messages the extraction has not read, over how long the oldest waited, `{{.lag}}`.
- `lens.unread_title` (template, localized): The unread flag's tooltip.
- `lens.review_due` (template, localized): The flag of a project past its review.
- `lens.no_next_action` (template, localized): The flag of a project with no open child.
- `lens.open_children` (template, localized): A project's open children, over their number `{{.n}}`.
- `lens.sequential` (template, localized): The flag of a project that takes its children one at a time.
- `lens.every` (template, localized): How often a project is reviewed, over the `{{.every}}`.
- `lens.reviewed` (template, localized): When a project was last reviewed, over the `{{.date}}`.
- `lens.more` (template, localized): The piece that counts the values a facet's `show_max` leaves off a row, over their number `{{.n}}`.
- `lens.col_who` (retired, not read): The head of the list column of whom a row waits on.
- `lens.col_age` (retired, not read): The head of the list column of a row's age.
- `lens.col_why` (retired, not read): The head of the list column of why a row waits.
- `lens.col_aside` (retired, not read): The head of the list column of a row's workspace and links.
- `lens.col_actions` (retired, not read): The head of the list column of a row's actions.
- `lens.changed` (retired, not read): The flag of a task that changed since you last opened it.
- `facets.<name>.show.ref` (list of strings): The values the facet shows in the ref part.
- `facets.<name>.show.meta` (list of strings): The values the facet shows in the meta part.
- `facets.<name>.show.flag` (list of strings): The values the facet shows in the flag part.
- `facets.<name>.show.who` (list of strings): The values the facet shows in the who part.
- `facets.<name>.show.kind` (list of strings): The values the facet shows in the kind part.
- `facets.<name>.show.age` (list of strings): The values the facet shows in the age part.
- `facets.<name>.show.aside` (list of strings): The values the facet shows in the aside part.
- `noun` (string or table, localized): What one of the plugin's units is, which the briefing counts, such as "pull request". A string, or a table of its CLDR plural forms, `other` required.
- `noun.zero` (string, localized): Its zero form.
- `noun.one` (string, localized): Its one form.
- `noun.two` (string, localized): Its two form.
- `noun.few` (string, localized): Its few form.
- `noun.many` (string, localized): Its many form.
- `noun.other` (string, localized): Its other form.
- `decisions.<name>.clause` (string or table, localized): The briefing's clause on the kind when every unit counted is the plugin's, `{counter}` where its count goes. A string, or a table of its CLDR plural forms, `other` required.
- `decisions.<name>.clause.zero` (string, localized): Its zero form.
- `decisions.<name>.clause.one` (string, localized): Its one form.
- `decisions.<name>.clause.two` (string, localized): Its two form.
- `decisions.<name>.clause.few` (string, localized): Its few form.
- `decisions.<name>.clause.many` (string, localized): Its many form.
- `decisions.<name>.clause.other` (string, localized): Its other form.

## A plugin's predicates (`predicates.toml`)

- `<name>` (predicate): A predicate the plugin's manifest declares in [[source.predicates]], by name, over the facts its facts list names, the ones the plugin hands with the question; a higher layer's replaces a lower one's, and one that does not compile leaves the lower one in force. The plugin asks whether it holds for a unit with the unit's facts, such as the GitHub plugin's reviewer_required: `!(pr.owner in ['acme'])`.

## Client strings (`strings.toml`)

- `strings` (table by name): The texts the clients show, by their English text, with `{name}` placeholders.
- `strings.<name>` (string or table, localized): A text: a string, or a table of its CLDR plural forms (`zero`, `one`, `two`, `few`, `many`, `other`), `other` required.
- `strings.<name>.zero` (string, localized): The text's zero form.
- `strings.<name>.one` (string, localized): The text's one form.
- `strings.<name>.two` (string, localized): The text's two form.
- `strings.<name>.few` (string, localized): The text's few form.
- `strings.<name>.many` (string, localized): The text's many form.
- `strings.<name>.other` (string, localized): The text's other form.
- `formats` (table by name): How the Go clients write dates and numbers: Go time layouts (`time`, `date`, `datetime`) and number separators (`decimal`, `group`).
- `formats.<name>` (string, localized): One format.

## The core's event kinds (`timeline.toml`)

- `events` (table by name): The core's event kinds, by kind, as the timeline names them.
- `events.<name>` (table): One event kind.
  - `held`: Automatic rounds paused on the unit.
  - `released`: Automatic rounds resumed.
  - `muted`: The unit's task deferred, out of the lists that wait on you.
  - `unmuted`: The deferral lifted.
  - `adopted`: Someone else's pull request taken over.
  - `status`: The unit's board status changed.
  - `removed`: The unit's workspace removed.
  - `nudged`: A nudge posted.
  - `tracked`: The unit followed.
  - `untracked`: The unit no longer followed.
  - `answered`: An agent's question answered.
  - `turn_sent`: A turn sent to an agent session.
  - `turn_started`: A turn started.
  - `turn_ended`: A turn ended.
  - `turn_resumed`: A turn resumed.
  - `turn_withdrawn`: A turn withdrawn.
  - `question_dismissed`: An agent's question dismissed.
  - `turn_delivered`: A turn delivered to its session.
  - `answer_routed`: An answer routed to its session.
  - `late_record`: A record that arrived after its turn ended.
  - `stop_blocked`: An agent's stop blocked for a reason the loop gave.
  - `turn_stopped`: A turn stopped.
  - `guide_trimmed`: A turn's guide trimmed to its limit.
  - `dossier_restored`: A dossier file restored.
  - `dossier_granted`: A dossier granted to a session.
  - `dossier_call`: A dossier compaction call, with its cost.
  - `omnibox_call`: An omnibox agent call, with its cost.
  - `brief_waiting`: A request waiting for its brief.
  - `intake_accepted`: A request accepted.
  - `intake_briefing`: A request waiting for its brief.
  - `intake_starting`: A request starting.
  - `intake_started`: A request started.
  - `intake_failed`: A request that failed.
  - `intake_canceled`: A request canceled.
  - `intake_guidance`: A request's guidance replaced.
  - `workflow_step_ran`: A workflow's step ran.
  - `session_started`: An agent session started.
  - `session_launched`: An agent session launched.
  - `session_abandoned`: An agent session abandoned before it started.
  - `session_idle`: An agent session went idle.
  - `session_active`: An agent session became active again.
  - `session_closed`: An agent session closed.
  - `session_typed`: Someone typed in an agent session.
  - `session_prompted`: An agent session got a prompt.
  - `session_unit`: An agent session tied to the unit.
  - `session_adopted`: An agent session adopted.
  - `session_dormant`: An agent session went dormant.
  - `session_woken`: A dormant agent session woken.
  - `session_merged`: An agent session merged into another.
  - `mcp_seen`: An agent session reached the loop's tools.
  - `turn_dismissed`: A turn dismissed by a rule.
  - `answer_refused`: An answer refused.
  - `answer_read`: An answer read by its session.
  - `view_applied`: A view's change applied.
  - `record`: A record written, such as a brief.
  - `gate_resolved`: A decision that waited on you resolved.
  - `gate_withdrawn`: A step that waited on you withdrawn by its workflow, its subject gone; its data is the workflow's reason.
  - `message`: A message in the task's conversation with its agent.
- `events.<name>.label` (string, localized): What the timeline calls an event of the kind, such as "paused".
- `events.<name>.tone` (one of ``, `loop`, `me`, `people`): The tone the timeline and the activity draw an event of the kind in; "loop" is what the activity's loop filter keeps.
  - ``: No tone.
  - `loop`: The loop's or an agent's doing.
  - `me`: Your doing, or what waits on you.
  - `people`: Someone else's doing.
- `facts` (table by name): The facts a task's timeline folds its events into, by name; a later layer adds a fact or replaces one of the same name whole.
- `facts.<name>` (table): One fact: the events that open it, close it and join it, and how it reads.
- `facts.<name>.start` (list of strings): The event kinds that open the fact: a core kind, a plugin's declared kind or a task verb's verb_&lt;verb&gt;.
- `facts.<name>.end` (list of strings): The event kinds that close it; when set the fact is a span, running until one comes, else each start is a fact of its own.
- `facts.<name>.absorb` (list of strings): The event kinds that join the fact: its open span, or the fact just before them when it is this one.
- `facts.<name>.label` (template, localized): How the fact reads, over {{.actor}}, {{.duration}} (a closed span's), {{.count}} (its events), {{.sub}} (its first event's short value, such as a review's verdict) and {{.event}} (its first event's label); its first event's label when absent.
- `facts.<name>.running` (template, localized): How a span with no end yet reads, over the label's values but {{.duration}}; the label when absent.
- `facts.<name>.unfinished` (template, localized): How a span reads that never ended: its start kind came again before any end (the daemon stopped mid-turn, a session vanished), over the label's values but {{.duration}}; the first event's words when absent.
- `facts.<name>.tone` (one of ``, `loop`, `me`, `people`): The fact's tone; its first event's when absent.
  - ``: No tone.
  - `loop`: The loop's or an agent's doing.
  - `me`: Your doing, or what waits on you.
  - `people`: Someone else's doing.
- `facts.<name>.order` (integer): Where the fact is tried among the others, lowest first; name order breaks a tie.
- `facts.<name>.subs` (table by name): The words of the values {{.sub}} reads, by value; a value it leaves out reads as itself.
- `facts.<name>.subs.<name>` (string, localized): One value's words.

## Decision kinds (`decisions.toml`)

- `open` (string, localized): What a queue's button that opens a unit at its source says; only the default layer's file sets it.
- `gates` (table): The decision kind of an open gate, by gate kind; only the default layer's file maps gates.
- `gates.<name>` (one of `answer`, `approve-plan`, `send-draft`, `review-step`, `last-step`, `pick-someone`, `act`): One gate kind's decision kind.
  - `answer`: An agent's question or a post waits for the user's answer.
  - `approve-plan`: An agent's plan waits for the user's approval.
  - `send-draft`: A draft waits for the user to send, edit or drop it.
  - `review-step`: A step of a review waits for the user's look.
  - `last-step`: A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.
  - `pick-someone`: A unit waits for the user to pick who takes it next, such as a reviewer.
  - `act`: A proposal or a trust request waits for the user to act on it.
- `situations` (table): The decision kind of a unit in a situation its plugin derives, by situation; only a plugin's layer maps them.
- `situations.<name>` (one of `answer`, `approve-plan`, `send-draft`, `review-step`, `last-step`, `pick-someone`, `act`): One situation's decision kind.
  - `answer`: An agent's question or a post waits for the user's answer.
  - `approve-plan`: An agent's plan waits for the user's approval.
  - `send-draft`: A draft waits for the user to send, edit or drop it.
  - `review-step`: A step of a review waits for the user's look.
  - `last-step`: A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.
  - `pick-someone`: A unit waits for the user to pick who takes it next, such as a reviewer.
  - `act`: A proposal or a trust request waits for the user to act on it.
- `signals` (table): The decision kind of a unit whose link signal is on, by signal; only a plugin's layer maps them.
- `signals.<name>` (one of `answer`, `approve-plan`, `send-draft`, `review-step`, `last-step`, `pick-someone`, `act`): One signal's decision kind.
  - `answer`: An agent's question or a post waits for the user's answer.
  - `approve-plan`: An agent's plan waits for the user's approval.
  - `send-draft`: A draft waits for the user to send, edit or drop it.
  - `review-step`: A step of a review waits for the user's look.
  - `last-step`: A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.
  - `pick-someone`: A unit waits for the user to pick who takes it next, such as a reviewer.
  - `act`: A proposal or a trust request waits for the user to act on it.
- `primary` (table): What a queue offers first on a unit of the plugin, by decision kind; only a plugin's layer sets it.
- `primary.<name>` (string): A catalog action of the plugin's units, or `open`, which opens the unit at its source.
- `kinds` (table): Each decision kind's view and words, by kind; only the default layer's file sets them.
- `kinds.<name>` (table): One decision kind.
- `kinds.<name>.view` (string): The view of views.toml that lists the kind's tasks, which its queue walks. Required.
- `kinds.<name>.primary` (string): The gate intent a queue offers first on a gate of the kind; empty offers the gate's first.
- `kinds.<name>.queue` (string, localized): The title of the kind's queue. Required.
- `kinds.<name>.empty` (string, localized): What the kind's queue says when it holds nothing. Required.
- `kinds.<name>.noun` (string or table, localized): What one of the kind's tasks is, which the briefing counts, such as "question". A string, or a table of its CLDR plural forms, `other` required.
- `kinds.<name>.noun.zero` (string, localized): Its zero form.
- `kinds.<name>.noun.one` (string, localized): Its one form.
- `kinds.<name>.noun.two` (string, localized): Its two form.
- `kinds.<name>.noun.few` (string, localized): Its few form.
- `kinds.<name>.noun.many` (string, localized): Its many form.
- `kinds.<name>.noun.other` (string, localized): Its other form.
- `kinds.<name>.clause` (string or table, localized): The briefing's clause on the kind, `{counter}` where its count goes, such as "the agent has {counter} for you". A string, or a table of its CLDR plural forms, `other` required.
- `kinds.<name>.clause.zero` (string, localized): Its zero form.
- `kinds.<name>.clause.one` (string, localized): Its one form.
- `kinds.<name>.clause.two` (string, localized): Its two form.
- `kinds.<name>.clause.few` (string, localized): Its few form.
- `kinds.<name>.clause.many` (string, localized): Its many form.
- `kinds.<name>.clause.other` (string, localized): Its other form.

## The cockpit's home (`home.toml`)

- `template` (string, default `default`): The briefing's template, which a client may draw its own way.
- `join` (string, localized): What goes between two clauses of a sentence, and between two plugins' counts, such as ", ".
- `last_join` (string, localized): What goes before the last clause of a sentence, and the last plugin's count, such as " and ".
- `end` (string, localized): What ends a sentence, such as ".".
- `count` (string, localized): A counter's words, `{n}` its count and `{noun}` what it counts, such as "{n} {noun}".
- `per_sentence` (integer, default `3`): The most clauses a sentence of the briefing holds.
- `empty` (string, localized): What the briefing says when every counter is zero.
- `counters[]` (table): The briefing's counters, in order; one with nothing to count says nothing. A user's list replaces the whole list.
- `counters[].id` (string): The counter's id; a kind's counter defaults to the kind.
- `counters[].kind` (one of `answer`, `approve-plan`, `send-draft`, `review-step`, `last-step`, `pick-someone`, `act`): The decision kind the counter counts, whose view and words decisions.toml gives; set kind or view.
  - `answer`: An agent's question or a post waits for the user's answer.
  - `approve-plan`: An agent's plan waits for the user's approval.
  - `send-draft`: A draft waits for the user to send, edit or drop it.
  - `review-step`: A step of a review waits for the user's look.
  - `last-step`: A unit waits only for its last step at its source, such as a merge or a closure, which the user takes there.
  - `pick-someone`: A unit waits for the user to pick who takes it next, such as a reviewer.
  - `act`: A proposal or a trust request waits for the user to act on it.
- `counters[].view` (string): The view of views.toml whose tasks the counter counts; set kind or view.
- `sections[]` (table): The home's sections, in order: the first rows of a view, which a link opens whole. A user's list replaces the whole list.
- `sections[].id` (string): The section's id. Required.
- `sections[].view` (string): The view of views.toml it shows the first rows of; its label titles the section. Required.
- `sections[].limit` (integer, default `3`): The most rows it shows.
- `sections[].time` (one of `owed`, `moved`, default `moved`): The time each row shows.
  - `owed`: Since when the task's next step is owed.
  - `moved`: When the task last moved.
- `capture` (table): The capture field.
- `capture.hint` (string, localized): What the empty field says.
- `counters[].noun` (string or table, localized): What one of the counter's tasks is; a kind's counter defaults to the kind's noun. A string, or a table of its CLDR plural forms, `other` required.
- `counters[].noun.zero` (string, localized): Its zero form.
- `counters[].noun.one` (string, localized): Its one form.
- `counters[].noun.two` (string, localized): Its two form.
- `counters[].noun.few` (string, localized): Its few form.
- `counters[].noun.many` (string, localized): Its many form.
- `counters[].noun.other` (string, localized): Its other form.
- `counters[].clause` (string or table, localized): The briefing's clause on the counter, `{counter}` where its count goes; a kind's counter defaults to the kind's clause. A string, or a table of its CLDR plural forms, `other` required.
- `counters[].clause.zero` (string, localized): Its zero form.
- `counters[].clause.one` (string, localized): Its one form.
- `counters[].clause.two` (string, localized): Its two form.
- `counters[].clause.few` (string, localized): Its few form.
- `counters[].clause.many` (string, localized): Its many form.
- `counters[].clause.other` (string, localized): Its other form.

## The omnibox (`omnibox.toml`)

- `search` (table): How a search groups the tasks it finds.
- `search.limit` (integer, default `5`): The most results a group shows; its count counts them all.
- `search.other` (string, localized): The label of the last group, which holds what no group's view holds.
- `search.groups[]` (table): A group, in order: a result goes to the first group whose view holds it. A user's list replaces the whole list.
- `search.groups[].id` (string): The group's id. Required.
- `search.groups[].label` (string, localized): The group's label. Required.
- `search.groups[].view` (string): The view of views.toml whose tasks the group holds. Required.
- `verbs` (table): The verbs the omnibox knows, by id: typed first, a verb's word runs its target on what the rest names. No operation runs, from a verb or from the agent, that no verb targets.
- `verbs.<name>` (table): One verb: its words and one target, action or verb, with op when nothing is named.
- `verbs.<name>.words` (list of strings): The words that name the verb in every language, in one list, such as ["review", "revisar"]: vocabulary, not a text a locale translates. Required.
- `verbs.<name>.action` (string): The catalog action it runs on each unit named.
- `verbs.<name>.verb` (string): The task verb of tasks.toml it runs on each task named; never one that resolves a gate.
- `verbs.<name>.op` (one of `capture`, `start`): What it runs on the text when it names no unit or task.
  - `capture`: Captures a task titled with the text.
  - `start`: Captures a task briefed with the text and starts it.

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
- `plugins.github.gates.approval` (table): Their approval; left out, the default gates.toml gives a draft gate.
- `plugins.github.gates.approval.mode` (one of `wait`, `delay`, `send`, `dry_run`, `run`): The approval mode.
  - `wait`: Waits for you.
  - `delay`: Resolves the gate to its approving intent when the window ends, unless you resolve it first.
  - `send`: Releases the gate at once: a draft is sent, a run runs.
  - `dry_run`: A draft gate only: stores the draft and never sends it; nobody can release it. A gate of another kind takes the next rule or its default.
  - `run`: Releases the gate at once, as send.
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
- `plugins.slack.gates.approval` (table): Their approval; left out, the default gates.toml gives a draft gate.
- `plugins.slack.gates.approval.mode` (one of `wait`, `delay`, `send`, `dry_run`, `run`): The approval mode.
  - `wait`: Waits for you.
  - `delay`: Resolves the gate to its approving intent when the window ends, unless you resolve it first.
  - `send`: Releases the gate at once: a draft is sent, a run runs.
  - `dry_run`: A draft gate only: stores the draft and never sends it; nobody can release it. A gate of another kind takes the next rule or its default.
  - `run`: Releases the gate at once, as send.
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
- `workflows.<name>.enabled` (bool): Switches the workflow; left out, it is on. Off, it proposes no draft, and the drafts it holds stay.
- `workflows.<name>.observe` (bool): Plans the workflow's items pending, for you to run; left out, the manifest's.
- `workflows.<name>.only_when` (predicate): A condition ANDed with the workflow's when.

### Workflow gate approval

- `workflows.<name>.gates` (table): The gates the workflow opens: its runs' plan gates, its drafts', its turns' drafts' and its gate.open steps'.
- `workflows.<name>.gates.approval` (an [approval table](#approval-tables) of gate modes): The approval of the workflow's gates, a rule for the kinds its when pins gate.kind to, over the manifest's field by field.

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
- `drafts.repeat_after` (duration, default `7d`): The default repeat_after a config.drafts.repeat_after reference reads, and the plugin rules' delay before they propose a settled draft's key again.
- `notifications` (table): The notifications.
- `notifications.digest_at` (string, default `09:00`): The local HH:MM of the daily digest; empty for none.

### Window

- `window` (table): The app's own window on macOS, which the app reads when it starts.
- `window.enabled` (bool, default `true`): Show the cockpit in the app's own window on macOS; false opens it in the browser.
- `window.hotkey` (string, default `ctrl+option+space`): The global shortcut that opens the omnibox panel, such as cmd+shift+space; empty for none.

### Units and tasks

- `items` (table): The items.
- `items.heat` (table): How long a unit stays warm.
- `items.heat.warm_for` (duration): How long a unit stays warm after its last item resolved; left out, the heat reader's default.
- `tasks` (table): The tasks' settings.
- `tasks.stale_waiting_days` (integer, default `2`): Working days after which a waiting task goes stale.
- `tasks.areas` (list of strings): The areas a task is offered.
- `tasks.contexts` (list of strings): The contexts a task is offered.
- `tasks.candidate_window` (duration, default `14d`): How far apart two tasks' creations may be for task.candidates to pair them.
- `tasks.candidate_cap` (integer, default `5`): The most tasks task.candidates lists; 0 lists none, so no correlation runs.

### Cleanup

- `cleanup` (table): The thresholds of the session and workspace cleanup workflows.
- `cleanup.idle` (duration, default `2h`): How long a finished session stays idle before it closes.
- `cleanup.asked_idle` (duration, default `4h`): How long a session that asked you stays idle before it closes.
- `cleanup.workspace_grace` (duration, default `1d`): How long, in working time, a workspace stays after its unit finished.
- `cleanup.manual_idle` (duration, default `1d`): How long a session you started stays idle before it closes.
- `cleanup.stale_workspace` (duration, default `14d`): How long an untouched workspace stays.
- `cleanup.orphan_hold` (duration, default `1d`): How long the close of a dormant session waits while its process lingers orphaned, the app showing some terminal throughout, before it closes the session's record anyway; it ends no process.
- `cleanup.disposable` (list of strings): Ignored files and directories a tool recreates, so removing a workspace that holds them loses nothing.

### Dossiers

- `dossiers` (table): The dossiers' lifecycle and quotas; a value of zero or less keeps its default.
- `dossiers.archive_after` (duration, default `30d`): How long after its task or unit resolved a dossier is archived.
- `dossiers.purge_after` (duration, default `400d`): How long after the archive it is purged.
- `dossiers.soft_cap_mb` (integer, default `50`): The size per dossier doctor reports.
- `dossiers.total_cap_mb` (integer, default `2048`): The size of dossiers and archives doctor reports.
- `dossiers.max_file_mb` (integer, default `5`): The largest file a dossier keeps; a larger one is linked.
- `dossiers.compact_at_kb` (integer, default `256`): The log size that triggers compaction.

### Interface

- `ui` (table): The interface.
- `ui.locale` (string): The interface language, a BCP 47 tag such as pt-BR; empty follows the system's preferred languages. Applies without a restart.

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

An approval table that links here has these keys, and takes the gate modes.

- `mode` (string): The mode of every kind of gate when no rule holds; left out, gates.toml's default of the kind.
- `window` (duration): With mode delay only: how long a person has to stop the gate's consequence.
- `rule[]` (table): A rule; the first whose when holds gives the mode.
- `rule[].when` (predicate): When the rule applies to a gate.
- `rule[].mode` (string): The rule's mode.
- `rule[].window` (duration): With mode delay only: how long a person has to stop the gate's consequence.

The gate modes:

- `wait`: Waits for you.
- `delay`: Resolves the gate to its approving intent when the window ends, unless you resolve it first.
- `send`: Releases the gate at once: a draft is sent, a run runs.
- `dry_run`: A draft gate only: stores the draft and never sends it; nobody can release it. A gate of another kind takes the next rule or its default.
- `run`: Releases the gate at once, as send.

This page is generated from the grammar table by `make docs-ref`.
