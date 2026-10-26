---
title: Profile language
description: Every reserved word of the profile files and the configuration, one line each.
---

Every key a profile file or `config.json` may set, with its kind, its values and its default. A key or a value not listed here fails to load, with the closest listed one as a suggestion. `<name>` is a name the author chooses, and `[]` marks an array of tables (`[[step]]`). Predicates and templates read the [facts](/reference/facts/); the manifests are detailed in the [intent manifest](/reference/intent-manifest/), [workflow manifest](/reference/workflow-manifest/) and [configuration](/reference/configuration/) pages.

## Intent manifest (`intents/<name>/intent.toml`)

### Identity and help

- `label` (string): The name people read. Required.
- `summary` (string): One line on what it does: its action's tooltip, its help line, its profile listing. Required.
- `shortcut` (string): One uppercase letter for its action, not a built-in action's.
- `verb` (one of `approve`, `delegate`, `dismiss`): The GTD verb its action fulfils.
- `user_facing` (bool, default `true`): Offered as a command, a skill and an action.
- `skill` (bool, default `false`): Install writes it a turn skill; needs help.target.
- `help` (table): How its command presents it.
- `help.target` (string): What the command's target is.
- `help.summary` (string): The main help's line; the intent's summary when absent.
- `help.group` (string): The main help's group title.
- `help.examples` (list of strings): Command lines without the command's name.
- `help.docs` (string): A page path of the documentation site.

### Binding

- `role` (one of `pr.review`, `pr.address`, `pr.nudge`, `probe`, `headless`): What binds it to a plugin's mechanism. One intent per role and provider.
- `provider` (string or table): The units its turns serve: a provider id, a list of them, or any; defaults.toml provider when absent.
- `builds` (one of `brief`): A builder, never a turn.
- `prompt` (file): A builder's prompt file, through the profile layers.
- `schema` (file): A builder's extra fields' schema file, beside the manifest.

### Where it runs

- `workspace` (string or table): Where its turn runs, and the board it moves: a workspace builtin, or a table naming the repository.
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

- `profile` (string): The intent whose judgment blocks the turn reads, or none.
- `dims` (list of `stage`, `reviewer`): The dimensions valid under its profile.
- `also_profile[]` (table): Other judgment the turn reads.
- `also_profile[].profile` (string): The intent with judgment blocks.
- `also_profile[].when` (predicate): When the turn reads the other judgment.
- `also_profile[].dims` (table by name): Dimensions it pins, such as { stage = "first" }.
- `also_profile[].dims.<name>` (string): A pinned dimension's value.
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

- `effects` (list of `plan.cleared`, `guidance.handed`, `pr.counted`, `pr.round_sent`, `pr.reviewed`, `pr.addressed`, `pr.nudged`): What sending the turn applies.
- `drafts` (table): The drafts its turns' agents propose.
- `drafts.allowed_effects` (list of `entry.nudged`, `unit.closed`, `pr.nudged`): The effects its agents may declare on their drafts.
- `drafts.approval` (table): The approval of its drafts; wait when absent.
- `drafts.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`): The approval when no rule holds.
- `drafts.approval.window` (duration): With mode delay only: how long a person has to stop the draft.
- `drafts.approval.rule[]` (table): A rule; the first whose when holds gives the mode.
- `drafts.approval.rule[].when` (predicate): When the approval rule applies to a draft.
- `drafts.approval.rule[].mode` (one of `send`, `delay`, `wait`, `dry_run`): The rule's approval.
- `drafts.approval.rule[].window` (duration): With mode delay only: how long a person has to stop the draft.

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

## Workflow manifest (`workflows/<name>/workflow.toml`)

### What it matches

- `summary` (string): One line, what the workflow does; its plan items' description. Required.
- `provider` (one of `entry`, `github`, `session`, `slack`, `workspace`): The provider of the units it matches. Required.
- `when` (predicate): The match condition, over the unit's facts, the trigger's and config.*. Required.
- `order` (integer, default `100`): Evaluation order, lowest first.
- `key` (list of facts): The facts forming the idempotency key, by default &lt;slot&gt;.fingerprint: a key that ran or was dismissed is not planned again.
- `marks` (table by name): Mark name to fact, stored when a run is done or dismissed, which handled() reads.
- `marks.<name>` (fact): The fact whose value the mark stores.

### When it is planned

- `trigger` (table): What plans the workflow: on or schedule; absent, every sweep.
- `trigger.on` (list of strings): The source event kinds the workflow reacts to.
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
- `step[].<name>` (string or table): The step's arguments.

### Run approval

- `approval` (table): The run approval: how a matched item runs.
- `approval.mode` (one of `run`, `delay`, `wait`, default `run`): The mode when no rule matches.
- `approval.window` (duration): How long a person has before the item runs; only with mode delay, which needs it.
- `approval.rule[]` (table): A rule tried in order; the first whose when holds gives the mode.
- `approval.rule[].when` (predicate): When the rule applies.
- `approval.rule[].mode` (one of `run`, `delay`, `wait`): The mode when the rule applies.
- `approval.rule[].window` (duration): The rule's window; only with mode delay, which needs it.

### Drafts

- `drafts` (table): The drafts the workflow's steps and turns propose.
- `drafts.withdraw` (table): When an open draft the workflow proposed is withdrawn.
- `drafts.withdraw.when` (predicate): When an open draft the workflow proposed is withdrawn, over the unit's facts and the draft's.
- `drafts.withdraw.reason` (one of `assigned`, `moot`, default `moot`): The reason the withdrawn draft stores.
- `drafts.approval` (table): The approval of the drafts the workflow's steps and turns propose.
- `drafts.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`, default `wait`): The mode when no rule matches.
- `drafts.approval.window` (duration): How long a person has before the draft is sent; only with mode delay, which needs it.
- `drafts.approval.rule[]` (table): A rule tried in order; the first whose when holds gives the mode.
- `drafts.approval.rule[].when` (predicate): When the rule applies.
- `drafts.approval.rule[].mode` (one of `send`, `delay`, `wait`, `dry_run`): The mode when the rule applies.
- `drafts.approval.rule[].window` (duration): The rule's window; only with mode delay, which needs it.

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
  - `stage` (one of `first`, `follow-up`): The pass over the unit, for a review.
  - `runner` (one of `session`, `headless`): How the turn runs; by default the intent's first runner.
- `draft.propose`:
  - `purpose` (string): What the draft is for, such as nudge or close. Required.
  - `delivery` (one of `message`, `reaction`, `comment`, default `message`): How the draft posts.
  - `text` (file, default `text.md.tmpl`): The template of the draft's text, beside the manifest.
  - `reaction` (string): The emoji name, with delivery reaction.
  - `items` (one of `owed`, `resolved`, `unowned`, `unowned_aged`, `all`, default `all`): Which of the unit's items the draft lists.
  - `per` (one of `unit`, `person`, default `unit`): One draft per unit, or one per person it waits on.
  - `target` (one of `unit`, `item`, default `unit`): Where the draft posts: on the unit, or on each item.
  - `effects` (list of `entry.nudged`, `unit.closed`, `pr.nudged`): What sending the draft applies.
  - `repeat_after` (string): The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after.
  - `repeat_after_sent` (string): The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever.
- `classify`:
  - `prompt` (file, default `prompt.md`): The classification's prompt, beside the manifest.
  - `schema` (file, default `schema.json`): The JSON schema of its answer, beside the manifest.
- `notify.self`:
  - `text` (file, default `text.md.tmpl`): The notification's text, beside the manifest.

## Defaults (`defaults.toml`)

- `provider` (string): The provider of an intent whose manifest names none.
- `refusal` (string): What a [[refuse]] case whose when holds says when its why renders empty.

## Block headings (`labels.toml`)

- `other` (string): The heading of a dimension dims does not name: {Dim} and {value}.
- `tag` (string): The heading of a tag condition: {key} and {value}.
- `dims` (table by name): A dimension's heading, by dimension: {value}.
- `dims.<name>` (string): The dimension's heading.
- `values` (table by name): The heading of one condition, by "&lt;dim&gt;=&lt;value&gt;".
- `values.<name>` (string): The condition's heading.

## Judgment block front matter (`+++` in `<dim>=<value>/.../intent=<name>.md`)

- `override` (bool, default `false`): Drops the blocks before it whose path is an ancestor of its own.
- `title` (string): Replaces the block's heading.
- `include` (list of files): Up to eight .md or .txt files of the profile the block inlines, relative to it.
- `paths` (list of strings): The block applies only when a changed file matches one of these globs.
- `delegates` (table by name): Per agent, skills the finding step runs besides the procedure's own work.
- `delegates.<name>` (list of strings): The agent's skills.

## Configuration (`config.json`)

### Agent and workspaces

- `profile` (string): The user profile layered over the bundled one: a name under the profiles directory, or a path.
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
- `github.drafts` (table): The drafts the plugin's own rules propose.
- `github.drafts.approval` (table): Their approval; left out, each waits for you.
- `github.drafts.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`): The approval mode.
- `github.drafts.approval.window` (duration): How long a delay waits before the draft goes; only with mode delay.

### GitHub address rounds

- `github.address_reviews` (bool, default `true`): Someone's review of a pull request you develop starts an address round.
- `github.address_skip_reviewers` (list of strings): Logins whose reviews never start an address round.
- `github.address_checks` (bool, default `true`): Failing checks on the head of a pull request you develop start an address round.
- `github.ignore_checks` (list of strings): Checks whose failure never starts an address round.
- `github.address_conflicts` (bool, default `true`): A pull request you develop that conflicts with its base past the conflict window starts an address round.

### GitHub scope and stalls

- `github.scope` (table): Whose pull requests the GitHub plugin follows.
- `github.scope.people` (list of strings): Logins whose pull requests it follows.
- `github.scope.teams` (list of strings): Teams whose pull requests it follows.
- `github.scope.repos` (list of strings): Repositories whose pull requests it follows.
- `github.scope.watch` (list of strings): Repositories it watches.
- `github.stalled` (table): When a pull request counts as stalled.
- `github.stalled.review` (duration, default `2d`): Waiting on a review for this long.
- `github.stalled.reply` (duration, default `1d`): Waiting on a reply for this long.
- `github.stalled.merge` (duration, default `1d`): Approved and unmerged for this long.

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
- `workflows.<name>.approval` (table): The approval of the workflow's runs, over the manifest's field by field.
- `workflows.<name>.approval.mode` (one of `run`, `delay`, `wait`): The mode when no rule matches; left out, the manifest's.
- `workflows.<name>.approval.window` (duration): How long a delay waits before it goes; only with mode delay.
- `workflows.<name>.approval.rule[]` (table): Rules tried in order before the mode, the first match wins; set, they replace the manifest's, and [] clears them.
- `workflows.<name>.approval.rule[].when` (predicate): When the rule applies.
- `workflows.<name>.approval.rule[].mode` (one of `run`, `delay`, `wait`): The rule's mode.
- `workflows.<name>.approval.rule[].window` (duration): The rule's delay; only with mode delay.

### Workflow draft approval

- `workflows.<name>.drafts` (table): The drafts the workflow proposes.
- `workflows.<name>.drafts.approval` (table): The approval of the workflow's drafts, over the manifest's field by field.
- `workflows.<name>.drafts.approval.mode` (one of `send`, `delay`, `wait`, `dry_run`): The mode when no rule matches; left out, the manifest's.
- `workflows.<name>.drafts.approval.window` (duration): How long a delay waits before it goes; only with mode delay.
- `workflows.<name>.drafts.approval.rule[]` (table): Rules tried in order before the mode, the first match wins; set, they replace the manifest's, and [] clears them.
- `workflows.<name>.drafts.approval.rule[].when` (predicate): When the rule applies.
- `workflows.<name>.drafts.approval.rule[].mode` (one of `send`, `delay`, `wait`, `dry_run`): The rule's mode.
- `workflows.<name>.drafts.approval.rule[].window` (duration): The rule's delay; only with mode delay.

### Turns and headless calls

- `turns` (table): The automated turn budgets.
- `turns.per_day` (integer, default `20`): Automated turns per day, all workflows together, on units whose plugin keeps no per-unit count (every provider but GitHub today).
- `turns.per_unit_per_day` (integer, default `5`): Automated turns on one unit per local day, on units whose plugin keeps a per-unit count (GitHub today); an automated turn counts when its intent declares the pr.counted effect.
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
- `nudge` (table): The nudges.
- `nudge.silent_days` (integer, default `30`): Calendar days of silence after which a nudge stops.
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
- `cleanup.workspace_grace` (duration, default `1d`): How long, in working time, a finished pull request's workspace stays.
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
- `sweep.rate_limit_fraction` (number, default `0.5`): The share of the GitHub rate limit the sweeps may spend, a number between 0 and 1.
- `http` (table): The daemon's web cockpit.
- `http.port` (integer, default `0`): The cockpit's port; 0 is none.
- `mcp` (table): The daemon's MCP endpoint, which agent homes register.
- `mcp.port` (integer, default `47800`): The MCP endpoint's port.
- `views` (table by name): Each view sink's own settings, by sink.
- `views.<name>` (table): One sink's settings.
- `views.<name>.columns` (table by name): The sink's column for each status: working, working-fallback, needs-me, reviewed, ready, in-progress, in-review, completed.
- `views.<name>.columns.<name>` (string): The column of the status.

This page is generated from the grammar table by `make docs-ref`.
