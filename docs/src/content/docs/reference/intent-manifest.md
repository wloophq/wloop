---
title: Intent manifest
description: Every field of intent.toml, the predicate language, the templates and the builtins.
---

```text
<profile>/
  intents/
    intent.schema.json       # optional, from `{{command}} profile schema`, for editors
    <name>/
      intent.toml            # the definition
      instructions.md.tmpl   # the turn's instructions (Go text/template)
  intent=<name>.md           # judgment blocks, anywhere in the condition tree
```

- The name is the directory's: a lower-case letter, then 1 to 23 lower-case letters, digits or `-`. `intent.toml` has no name key.
- A name may not be a CLI command, a built-in action id, a profile dimension, or one of `try`, `eval`, `settings`, `intents` and `tags`.
- A directory named as a bundled intent overrides it file by file: each file it holds replaces the bundled file of the same name, and the bundled intent's other files are kept. An `intent.toml` there replaces the bundled manifest whole; files whose name starts with `.` are ignored. When the result does not load, the bundled intent stays and `profile check` reports the problem against your file, against your manifest for a problem it causes in a bundled file. In a clash of shortcut, flag or role with one of your own intents, the override yields.
- Judgment blocks `intent=<name>.md` apply to any intent whose manifest names a `profile`.

The bundled intents are written the same way; `{{command}} profile show <owner/repo> --show` renders any of them. A worked example is in [Add your own intent](/guides/custom-intent/).

## Fields

`intent.toml` is TOML: every string is quoted, so a predicate such as `when = '!pr.mine'` cannot turn into something else. The loader applies exactly these defaults and refuses any other key, naming a close one when one exists. A manifest without `provider` takes the one `defaults.toml` names at the profile's root; the bundled profile names `github`, and a layer's own `defaults.toml` overrides it. Every bundled intent names its own provider, so a `defaults.toml` with `provider = "slack"` changes only the manifests that name none, such as yours. The [profile language](/reference/language/) lists every file's keys on one page.

### Identity and help

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `label` | string |  | The name people read. Required. |
| `summary` | string |  | One line on what it does: its action's tooltip, its help line, its profile listing. Required. |
| `shortcut` | string |  | One uppercase letter for its action, not a built-in action's. |
| `verb` | one of `approve`, `delegate`, `dismiss` |  | The verb its action fulfils. |
| `user_facing` | bool | `true` | Offered as a command, a skill and an action. |
| `skill` | bool | `false` | Install writes it a turn skill; needs help.target. |
| `help` | table |  | How its command presents it. |
| `help.target` | string |  | Free text saying what the command's argument names, such as a pull request URL. |
| `help.summary` | string |  | The main help's line; the intent's summary when absent. |
| `help.group` | string |  | The title of the main help's group that lists the command, free text; action.group is the action's. |
| `help.examples` | list of strings |  | Command lines without the command's name. |
| `help.docs` | string |  | A page path of the documentation site. |

### Binding

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `role` | one of `pr.review`, `pr.address`, `pr.nudge`, `probe`, `headless` |  | What binds it to a plugin's mechanism. One intent per role and provider. |
| `provider` | one of `github`, `session`, `slack`, `task`, `workspace`, `any`, or a list of them |  | The units its turns serve: a provider id, a list of them, or any; defaults.toml provider when absent. |
| `builds` | one of `brief` |  | A builder, never a turn. |
| `prompt` | file |  | A builder's prompt file, through the profile layers. |
| `schema` | file |  | A builder's extra fields' schema file, beside the manifest. |

### Where it runs

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `workspace` | one of `review`, `author`, `linked_else_main`, `given`, or a table |  | Where its turn runs, and the board it moves: a workspace builtin, or a table naming the repository. |
| `workspace.repo` | string |  | The repository the turn runs in, owner/name. |
| `workspace.repo_input` | string |  | The input that holds the repository the turn runs in. |
| `workspace.repo_optional` | bool | `false` | With repo_input: a turn whose input names no repository runs in the neutral repository, with no checkout. |
| `workspace.name` | template |  | The name of the worktree created for the turn's unit, over the name facts, with slug; the loop's own when absent. |
| `runners` | list of `session`, `headless` |  | How its turns may run; ["session"] when absent. |
| `session` | table |  | The agent session its turns run in. |
| `session.strategy` | one of `reuse_live`, `new` |  | Whether a turn reuses the unit's live session or starts a new one. |
| `session.title` | string |  | The session's title: 1 to 12 lowercase letters. |
| `session.name` | template |  | What the workspace manager and the agent show the session as, over the name facts; defaults.toml session_name when absent, else the title. A name that session.title leads and the start time ends, as dd/mm hh:mm, lets the loop recognize its session when it lost track of it. |
| `session.close_when_done` | bool | `false` | The session closes when its turn is done. |
| `target` | table |  | What a run with no unit does. |
| `target.missing` | one of `capture` |  | capture: a run with no unit captures its guidance as a new task, the turn's unit; refused when absent. |
| `target.title` | template |  | The captured task's title, over .guidance, with firstLine. |

### How a turn runs

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `intake` | bool | `false` | Runs through numbered intake. |
| `owner_only` | bool | `false` | The unit must be yours or taken over. |
| `join` | table |  | When a request joins an open turn. |
| `join.differs_on` | list of strings |  | Persisted inputs whose change starts a new turn. |
| `answerable` | bool | `true` | The turn may stop on a question. |
| `reports` | bool | `true` | The turn reports. |

### Inputs

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `inputs` | table by name |  | The turn's inputs, by name. |
| `inputs.&lt;name&gt;` | table |  | An input. |
| `inputs.&lt;name&gt;.type` | one of `bool`, `text`, `list` |  | The input's type. |
| `inputs.&lt;name&gt;.flag` | string |  | The CLI flag that sets it; required from the user. |
| `inputs.&lt;name&gt;.help` | string |  | What it does; required from the user. |
| `inputs.&lt;name&gt;.from` | one of `user`, `prepare` | `user` | Who sets it. |
| `inputs.&lt;name&gt;.persist` | bool | `false` | Stored with the turn, restored on resume. |
| `inputs.&lt;name&gt;.hidden` | bool | `false` | Left out of help. |
| `inputs.&lt;name&gt;.by_hand_only` | bool | `false` | An automated turn never sets it. |
| `inputs.&lt;name&gt;.before` | one of `take_over` |  | What runs before the turn when it is set. |
| `inputs.&lt;name&gt;.max` | integer | `0` | A text input's length cap in runes; 0 for none. |
| `inputs.&lt;name&gt;.surfaces` | list of `cli`, `instructions`, `try` |  | Where it is offered; cli and instructions when absent. |

### When a turn is due or refused

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `why` | template |  | Why the turn is due, over the unit's workflow facts. |
| `refuse[]` | table |  | A refusal of turn.start; the first whose when holds refuses with its why. |
| `refuse[].when` | predicate |  | When it refuses, over the unit's workflow facts. |
| `refuse[].why` | template |  | What the refusal says, over the unit's workflow facts. |

### Its action on a row

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `action` | table |  | How the intent's action is offered on a row; its description is the intent's summary. |
| `action.group` | one of `start`, `turn`, `control`, `workspace`, `brief`, `intake` | `start` | The group the action is listed under. |
| `action.surfaces` | list of `row`, `detail`, `tui`, `menubar`, `notification`, `cli`, `explore` |  | Where the action is offered: row, detail, tui, cli and explore when absent. |
| `action.when` | predicate | `row.open` | When the action is offered on a row. |
| `action.primary` | table |  | When the action is the row's primary one. |
| `action.primary.when` | predicate | `false` | When the action is the row's primary one. |
| `action.refuse[]` | table |  | A case that offers the action disabled; the first that holds says why. |
| `action.refuse[].when` | predicate |  | When the action is offered disabled. |
| `action.refuse[].why` | template |  | Why the action is disabled, over the action facts. |
| `action.note` | template |  | What the action would do on this row, over the action facts. |
| `action.warning` | template |  | Shown under the action, over the action facts; empty shows nothing. |
| `action.confirm` | template |  | The dialog that confirms the action, over the action facts; empty asks nothing. |

### What the prompt opens with

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `headline` | table |  | The line that opens the turn's prompt. |
| `headline.resumed` | template |  | What a resumed turn continues, over the turn's facts. |
| `headline.new[]` | table |  | A headline case; the last has no when. |
| `headline.new[].when` | predicate |  | When the headline case applies. |
| `headline.new[].line` | template |  | The headline, over the turn's facts. |
| `start_line` | string |  | The intake's first line. |
| `resume` | table |  | What a resumed turn reads. |
| `resume.changed` | string |  | The text when the unit changed since the question. |

### Guidance and brief

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `guidance` | table |  | The guidance a person gives the turn. |
| `guidance.label` | string | `Guidance` | The guidance's label. |
| `guidance.help` | string |  | What the guidance is for. |
| `guidance.max` | integer | `0` | The guidance's length cap in runes; 0 for none. |
| `brief` | table |  | How the turn reads the unit's brief. |
| `brief.render` | bool | `true` | The instructions carry the brief. |
| `brief.wait` | bool | `true` | The turn waits for the brief. |
| `brief.need` | list of strings |  | The brief intent's extra fields the turn needs. |

### Judgment

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `profile` | string |  | The intent whose judgment blocks the turn reads, or none; the configuration's profile is the profile directory. |
| `dims` | list of `stage`, `reviewer` |  | The dimensions valid under its profile. |
| `also_profile[]` | table |  | Other judgment the turn reads. |
| `also_profile[].profile` | string |  | The intent with judgment blocks. |
| `also_profile[].when` | predicate |  | When the turn reads the other judgment. |
| `also_profile[].dims` | table |  | Dimensions it pins, such as { stage = "first" }. |
| `also_profile[].dims.stage` | one of `first`, `follow-up` |  | The pinned stage. |
| `also_profile[].dims.reviewer` | string |  | The pinned reviewer login. |
| `also_profile[].define` | string |  | The template define that renders it. |
| `also_profile[].prefix` | string |  | What its sources are listed under; "&lt;profile&gt; guide: " when absent. |

### What it records

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `record` | table |  | The turn's record. |
| `record.when` | predicate | `true` | When the turn records its outcome. |
| `record.states` | list of `done`, `settled`, `asked`, `failed` |  | The states a record may take. Required. |
| `record.artifacts` | list of `review-draft`, `triage` |  | The artifacts a record may carry. |
| `record.outcome` | list of `verdict`, `url` |  | The outcome fields a record may carry. |
| `record.attach_url` | bool | `false` | On a turn whose unit is a task, a done record's outcome url, when it names a unit such as a pull request, attaches that unit to the task; needs url in record.outcome. |
| `record.outcome_doc` | string |  | What the outcome holds, for the instructions. |
| `record.history_artifact` | string |  | The artifact the unit's history shows. |
| `record.require[]` | table |  | What a record in a state must carry. |
| `record.require[].state` | string |  | The state. |
| `record.require[].artifact` | string |  | The artifact it must carry. |
| `record.require[].fields` | list of strings |  | The outcome fields it must carry. |

### Effects and drafts

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `effects` | list of `plan.cleared`, `guidance.handed`, `pr.round_sent`, `pr.reviewed`, `pr.addressed`, `pr.nudged` |  | What sending the turn applies. |
| `drafts` | table |  | The drafts its turns' agents propose. |
| `drafts.allowed_effects` | list of `task.nudged`, `unit.closed`, `pr.nudged` |  | The effects its agents may declare on their drafts. |
| `drafts.attach_posts` | bool | `false` | On a turn whose unit is a task, the unit of each post its agent makes, sent at once or once approved, is attached to the task; reactions and edits are not posts. |
| `drafts.approval` | table |  | The approval of its drafts; wait when absent. |
| `drafts.approval.mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The approval when no rule holds. |
| `drafts.approval.window` | duration |  | With mode delay only: how long a person has to stop the draft. |
| `drafts.approval.rule[]` | table |  | A rule; the first whose when holds gives the mode. |
| `drafts.approval.rule[].when` | predicate |  | When the approval rule applies to a draft. |
| `drafts.approval.rule[].mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The rule's approval. |
| `drafts.approval.rule[].window` | duration |  | With mode delay only: how long a person has to stop the draft. |

An intent with `builds` is a builder: it needs `prompt` and holds no role. Any other needs `workspace`, `[session]`, `[headline]` and `[record]`.

### Declaring inputs

`[inputs.<name>]` declares an input. A `list` input comes only `from = "prepare"`; a user input needs `flag` (`--`, a lower-case letter, then lower-case letters, digits or `-`; two manifests may declare the same flag only with the same input name and type) and `help`. Guidance is not an input: the text after the flags, or after `--`, is the turn's guidance. `--no-wait` and `--no-brief` exist only for intents with `intake = true`; never declare them.

### `[[also_profile]]`

Adds another intent's judgment to the turn: `profile` and `when` (a predicate over the turn's facts) are required, `dims` pins dimensions such as `{ stage = "first" }`, `define` names the template define that renders it, and `prefix` what its sources are listed under. The bundled `address` uses it for `--self-review`.

### `[action]`

Makes the intent an action on rows. Its description is the intent's `summary`. `when` offers it, `primary = { when = ... }` makes it the row's first, and each `[[action.refuse]]` whose `when` holds offers it disabled with its `why`. `note`, `warning` and `confirm` are templates over the action facts.

### `[drafts.approval]`

```toml
[drafts.approval]
mode = "wait"

[[drafts.approval.rule]]
when = 'draft.delivery == "comment" && pr.repo_name == "docs"'
mode = "delay"
window = "10m"
```

The policy of the drafts the intent's turns propose: the first rule whose `when` holds gives the mode, else `mode`; `wait` when the table is absent. A rule's `when` reads `draft.delivery`, `draft.proposer` and the [facts](/reference/facts/) of the unit the draft is on, for every provider the intent serves; never `draft.purpose` or `draft.effects`, which the turn's agent names, nor `brief.*` or a fact marked **Untrusted**. A rule over one provider's facts applies only to drafts on that provider's units; a rule over two providers' facts is refused. When the unit's facts cannot be read, a draft whose rules read them waits for you. A rule that sends on its own (`send` or `delay`) applies to a comment draft only when `draft.delivery == "comment"` is a term of its `when`'s top-level `&&`; `mode` alone never releases a comment. `dry_run` stores the draft and never sends it. A workflow's own `[drafts.approval]` wins over the intent's for the turns it starts; the configuration's `workflows.<name>.drafts.approval` is laid over the workflow's, or over the intent's when the workflow has none. See [the Outbox](/concepts/approval/#the-outbox).

## Providers

`provider` names the units the intent works on: a turn is built only on a unit of that provider, and the catalog offers the action only on its rows; `any` serves every provider. When several intents share a `verb`, the task's key runs the one of highest rank the row enables: an intent of the unit's own provider before one of `any`, and yours before a bundled one within each. So a `provider = "slack"` intent with `verb = "delegate"` replaces the bundled `act` on Slack tasks. An intent whose provider is not `github` cannot read the `pr.*` facts, and its templates cannot read `.PR`. Every template reads the unit's wording from the provider: `{{.Wording.Noun}}` ("pull request", "Slack thread"), `{{.Wording.This}}`, `{{.Wording.Short}}`, and the fact `unit.ref` (`acme/api#7`).

## Roles

{{name}} finds the intents it runs on its own through their `role`, never by name: `pr.review` sends the sweep's review rounds and reads their history, `pr.address` the address rounds, `pr.nudge` the nudges, `probe` the agent-home probe and `headless` a headless request that names no intent. What a role implies (its round, its reconcile, what it prepares, its context and launch, its sessions' agent policy, whether its turns drive their workspace's row) is the role's, never a manifest key. Each turn stores the role its intent held, so history reads by role too. At most one intent of a layer holds a role on a provider; one of yours holding a bundled intent's role takes it over, so an intent `my-review` with `role = "pr.review"` gets every automatic review round, and the bundled holder's shortcut. A copy of the bundled `review` renamed `my-review` loads whole, and the bundled `review` keeps loading, for a turn by hand, without its shortcut or its row. When yours does not load, the bundled holder keeps them.

## Where a turn runs

A `workspace` builtin places a pull request's turn. On any other unit, the turn runs where the unit's placement says, else in a neutral repository with no checkout. A workspace table names the repository instead: `repo` outright, or `repo_input` by one of the intent's inputs. The turn runs in that repository's main checkout when `session.strategy` is `reuse_live`, else in a worktree of its own, created by the workspace manager that keeps it. The repository must be one the manager knows. An input is set only by a person: a workflow's `turn.start` refuses an intent that takes its repository from an input, so no text a workflow reads chooses where a turn runs.

## Predicates

Every `when` key is a [CEL](https://github.com/google/cel-spec) expressions, checked at load.

- Types are `bool`, `int`, `string`, `list` (of strings), `duration` and `timestamp`. A predicate is a non-empty `bool`. `in`, `size()`, `exists()` and `all()` work on lists; `duration("2h")` and `timestamp("2026-10-21T09:00:00-03:00")` are literals; a timestamp minus a timestamp is a duration.
- Fields resolve against the facts of the predicate's slot: the **action** slot reads the row the daemon serves, the **turn** slot reads the turn being built. An unknown field fails, naming a close one when one exists. The [facts](/reference/facts/) page lists them.
- `inputs.<name>` reads the intent's own declared inputs, in the turn slot, with the input's type.
- `!` applies before `==`, `!=`, `<`, `>` and `in`: write `!(turn.stage == "first")`; `!turn.stage == "first"` fails to load.
- A missing fact reads as its type's zero value; an error at evaluation reads as false.

## Templates

- **Action texts** (`note`, `warning`, `confirm`, `[[action.refuse]] why`) run over a nested map of the action facts: `{{.pr.slug}}`, `{{index .pr.nudge.who 0}}`; `join` is the one extra function (`{{join .pr.nudge.who ", "}}`).
- **Headlines** (`line`, `resumed`), `why` and `[[refuse]] why` run over a nested map of the facts: `{{.pr.slug}}`, `{{.unit.ref}}`, and the partial `{{template "started_by" .}}` ("asked for by you" or the loop's name).
- **`instructions.md.tmpl`** runs over the turn context with `missingkey=error`. `.Headline`, `.PR`, `.Wording`, `.Guidance`, `.Inputs.<name>` (its zero value when unset), `.Resume`, `.History` and `.Command` are there. The partials `history`, `resume`, `record`, `brief`, `judgment`, `review-judgment`, `items` and `unit` are {{name}}'s; a profile replaces one with a file of the same name in its `partials/` directory, as [Change a partial](/guides/custom-intent/#change-a-partial) shows. Include `{{template "history" .}}` and `{{template "record" .}}` in any intent that records, and `{{if .Resume}}{{template "resume" .}}{{end}}` in any that can be answered.
- Every text is parsed at load and rendered against sample facts and sample turns, so a template that would fail at run time fails `profile check`.

## Builtins

- Workspaces: `review` (the review workspace, created at the head), `author` (the workspace that develops it), `linked_else_main` (the workspace linked to the unit, else the repository's main checkout), `given` (the workspace the request names).
- Before: `take_over`.
- Effects: the names `effects` and `drafts.allowed_effects` list are on the [facts](/reference/facts/#draft-effects) page.

## Shortcuts

`shortcut` is checked on every surface the action reaches (web, terminal, menu bar). The core owns every lowercase letter, every digit and its chord prefixes, and built-in actions keep their shortcuts. Bundled intents claim their shortcuts first, then yours by name. One of yours with a clashing shortcut loads without it, and `profile check` reports the clash.

## The action catalog

For every row the daemon computes the actions it offers: the built-in ones, worded by the profile's `actions.toml` (`label`, `summary`, `note`, `warning`, `confirm`, `why` and `[<id>.input]`), and each intent's `[action]`. Clients render that list and decide nothing: a request for an action the row does not offer, or offers disabled, is refused with the same reason, and with a code that names the text it shows (`<id>.why` of a built-in action, `action.refuse[<i>]` of an intent's, `not_offered`), which a client reads instead of the words.

## Errors

Every problem is one line, `file:line:col: field.path: message`, sorted; `profile check`, `doctor` and the daemon's start print them all:

```text
intents/ask-author/intent.toml:6:10: sesion: unknown key; did you mean session?
intents/ask-author/intent.toml:15:8: action.when: 13: unknown field pr.mien; did you mean pr.mine?
intents/ask-author/intent.toml:16:8: role: "round.review" is not one of "pr.review", "pr.address", "pr.nudge", "probe" or "headless"; did you mean "pr.review"?
intents/ask-author/instructions.md.tmpl:5:6: inputs.nope is not declared
```

A broken intent of yours disables only itself.

## Editor setup

```sh
{{command}} profile schema > <profile>/intents/intent.schema.json
```

```toml
#:schema ../intent.schema.json
```

Editors with Taplo then complete keys, describe each field and flag unknown keys as you type.
