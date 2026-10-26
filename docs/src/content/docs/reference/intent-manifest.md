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
- A directory named as a bundled intent overrides it file by file: each file it holds replaces the bundled file of the same name, and the bundled intent's other files are kept. An `intent.toml` there replaces the bundled manifest whole; files whose name starts with `.` are ignored. When the result does not load, the bundled intent stays and `profile check` reports the problem against your file, against your manifest for a problem it causes in a bundled file. In a clash of key, flag or `drives_row` with one of your own intents, the override yields.
- Judgment blocks `intent=<name>.md` apply to any intent whose manifest names a `profile`.

The bundled intents are written the same way; `{{command}} profile show <owner/repo> --show` renders any of them. A worked example is in [Add your own intent](/guides/custom-intent/).

## Fields

`intent.toml` is TOML: every string is quoted, so a predicate such as `enabled = '!pr.mine'` cannot turn into something else. The loader applies exactly these defaults and rejects any other key, and the keys only the bundled intents set (`context`, `launch`, `policy`, `reconcile`) in any intent of yours that does not override a bundled one; an intent of yours that takes a bundled intent's role may set `reconcile`. A manifest without `provider` takes the one `defaults.toml` names at the profile's root; the bundled profile names `github`, and a layer's own `defaults.toml` overrides it. Every bundled intent names its own provider, so a `defaults.toml` with `provider = "slack"` changes only the manifests that name none, such as yours.

| Field | Required | Default | Values |
|---|---|---|---|
| `label` | yes | | text |
| `summary` | yes | | text, one line |
| `verb` | | `""` | the GTD verb the intent's `[action]` fulfills: `approve`, `delegate` or `dismiss`; needs an `[action]` |
| `key` | | `""` | one uppercase letter, unique among intents and built-in actions; a refused key loads the intent without it, reachable from the palette |
| `user_facing` | | `true` | `false` keeps the intent out of the CLI and by-hand sessions, and allows no `[action]` or `skill` |
| `skill` | | `false` | `true` has `install` write the intent a turn skill, `/{{name}}-<intent> <url>`; the bundled `review`, `address` and `nudge` set it |
| `workspace` | yes, unless `builds` | | `review`, `author`, `pr_else_main`, `given`, or a table: `{ repo = "<owner/name>" }` or `{ repo_input = "<input>" }`, see [Where a turn runs](#where-a-turn-runs) |
| `provider` | | the profile's `defaults.toml` (`"github"`) | the provider whose units the turns run on, such as `github` or `slack`, or `any`; a list for several |
| `modes` | | `["session"]` | `session`, `headless`, or both; the first is what `turn.start` runs without a `mode` |
| `role` | | `""` | what {{name}} resolves the intent by, see [Roles](#roles) |
| `builds` | | `""` | `brief`: the intent builds the brief and is never a turn |
| `round_kind` | | from `role` | `review` or `address`: the sweep's round the intent's `turn.start` sends on a pull request; `round.review` gives `review`, `round.address` gives `address` |
| `cap` | | `rounds` with a round kind or the `nudge` role | `rounds`: the intent's turns spend the per-pull request daily round cap |
| `reason_fact` | | `""` | the workflow fact that says why the round is due; a review's first round says why itself |
| `refuse_when` | | `""` | a string workflow fact whose non-empty value refuses `turn.start` with that text; on a pull request, a mute refuses too |
| `reason` | | `""` | `address_round`: the turn carries the reviewers, failing checks and conflict its round answers |
| `draft_approval.mode`, `draft_approval.window` | | `always` | `always`, `window` (with a positive `window` such as `"10m"`), `none` or `observe` |
| `[[draft_approval.rule]]` | | none | `when`, `mode`, `window`: tried in order before `mode`, see [`[draft_approval]`](#draft_approval) |
| `session.strategy` | yes | | `reuse_live`, `new` |
| `session.title` | yes | | 1 to 12 lower-case letters |
| `session.close_when_done` | | `false` | |
| `intake` | | `false` | the request waits for the brief through a numbered intake |
| `start_line` | | `"starting the "` and the label in lower case | text |
| `owner_only` | | `false` | the pull request must be yours or taken over |
| `answerable` | | `true` | |
| `board` | | `"none"` | `review`, `author`, `none` |
| `drives_row` | | `false` | at most one intent per workspace role |
| `reports` | | `true` | |
| `brief.render`, `brief.wait` | | `true`, `true` | |
| `profile` | | `"none"` | an intent name with judgment blocks, or `none` |
| `dims` | | `[]` | `round` |
| `records` | | `"true"` | predicate, turn slot |
| `prepare` | | `""` | `""`, `nudge.prepare` |
| `on_sent` | | `[]` | effect names, see [builtins](#builtins) |
| `guidance.label`, `guidance.help`, `guidance.max` | | `"Guidance"`, `""`, `0` | `max` in runes, 0 is no limit |
| `headline.resumed` | yes | | template over the turn context |
| `headline.new` | yes, at least one case; the last without `when` | | `[{when, text}]` |
| `inputs.<name>` | | none | below |
| `also_profiles` | | `[]` | below |
| `action` | | absent: offered nowhere | below |
| `join.differs_on` | | `[]` | persisted input names |
| `record.states` | yes | | subset of `done`, `settled`, `asked`, `failed`; `asked` needs `answerable` |
| `record.artifacts` | | `[]` | `review-draft`, `triage` |
| `record.outcome` | | `[]` | `verdict`, `url` |
| `record.outcome_doc` | | `""` | the sentence the record step teaches for `outcome` |
| `record.history_artifact` | | `""` | one of `record.artifacts` |
| `record.require` | | `[]` | `[{state, artifact, fields}]` |
| `resume.head_moved` | | a generic sentence | text |

### `[inputs.<name>]`

| Field | Required | Default | Values |
|---|---|---|---|
| `type` | yes | | `flag` (bool), `text` (string), `list` (strings, only with `from = "prepare"`) |
| `flag` | for `from = "user"` | | `--`, a lower-case letter, then lower-case letters, digits or `-`; two manifests may declare the same flag only with the same input name and type |
| `help` | for `from = "user"` | | text |
| `from` | | `"user"` | `user`, `prepare` |
| `persist` | | `false` | stored with the turn and restored on resume |
| `hidden` | | `false` | not in help |
| `by_hand_only` | | `false` | automatic rounds never set it |
| `before` | | `""` | `take_over` |
| `max` | | `0` | runes, text only |
| `surfaces` | | `["cli", "instructions"]` | `cli`, `instructions`, `try` |

Guidance is not an input: the text after the flags, or after `--`, is the turn's guidance. `--no-wait` and `--no-brief` exist only for intents with `intake = true`; never declare them.

### `[[also_profiles]]`

Adds another intent's judgment to the turn: `profile` (required), `when` (predicate, turn slot, required), `round` (`first` or `follow-up`), `define` (required; the template define that renders it) and `source_prefix` (default the profile's name and `" guide: "`). The bundled `address` uses it for `--self-review`.

### `[action]`

Makes the intent an action on rows:

| Field | Default | Values |
|---|---|---|
| `group` | `"round"` | |
| `surfaces` | `["row", "detail", "tui", "cli", "explore"]` | any of `row`, `detail`, `tui`, `menubar`, `notification`, `cli`, `explore` |
| `visible` | `"row.open"` | predicate, action slot |
| `enabled` | `"true"` | predicate, action slot |
| `reason_off` | | template, required when `enabled` is not `"true"` |
| `primary` | `"false"` | predicate |
| `confirm` | none | template |
| `hint` | none | template |
| `note_when_question` | none | text |

### `[draft_approval]`

```toml
[draft_approval]
mode = "always"

[[draft_approval.rule]]
when = 'draft.delivery == "comment" && pr.repo == "acme/docs"'
mode = "window"
window = "10m"
```

The policy of the drafts the intent's turns propose: the first rule whose `when` holds gives the mode, else `mode`. A rule's `when` reads `draft.delivery`, `draft.proposer` and the [facts](/reference/facts/) of the unit the draft is on, for every provider the intent serves; never `draft.purpose` or `draft.effects`, which the turn's agent names, nor `brief.*` or a fact marked **Untrusted**. A rule over one provider's facts applies only to drafts on that provider's units; a rule over two providers' facts is refused. When the unit's facts cannot be read, a draft whose rules read them waits for you. A rule that sends on its own (`none` or `window`) applies to a comment draft only when `draft.delivery == "comment"` is a term of its `when`'s top-level `&&`; `mode` alone never releases a comment. A workflow's own `[draft_approval]` wins over the intent's for the turns it starts. See [the Outbox](/concepts/approval/#the-outbox).

## Providers

`provider` names the units the intent works on: a turn is built only on a unit of that provider, and the catalog offers the action only on its rows; `any` serves every provider. When several intents share a `verb`, the entry's key runs the one of highest rank the row enables: an intent of the unit's own provider before one of `any`, and yours before a bundled one within each. So a `provider = "slack"` intent with `verb = "delegate"` replaces the bundled `act` on Slack entries. Only a `github` intent may set `drives_row`. An intent whose provider is not `github` cannot read the `pr.*` facts, and its templates cannot read `.PR`. Every template reads the unit's wording from the provider: `{{.Wording.Noun}}` ("pull request", "Slack thread"), `{{.Wording.This}}`, `{{.Wording.Ref}}` (`acme/api#7`) and `{{.Wording.Short}}`.

## Roles

{{name}} finds the intents it runs on its own through their `role`, never by name: `round.review` sends the sweep's review rounds and reads their history, `round.address` the address rounds, `nudge` the nudges, `probe` the agent-home probe and `headless.default` a headless request that names no intent. Each turn stores the role its intent held, so history reads by role too. At most one intent of a layer holds a role on a provider; one of yours holding a bundled intent's role takes it over, so an intent `my-review` with `role = "round.review"` gets every automatic review round. It also takes the bundled holder's key and its row driving, and may set the bundled holder's `reconcile`: a copy of the bundled `review` renamed `my-review` loads whole, and the bundled `review` keeps loading, for a turn by hand, without its key or `drives_row`. When yours does not load, the bundled holder keeps them.

## Where a turn runs

A `workspace` builtin places a pull request's turn. On any other unit, the turn runs where the unit's placement says, else in a neutral repository with no checkout. A workspace table names the repository instead: `repo` outright, or `repo_input` by one of the intent's inputs. The turn runs in that repository's main checkout when `session.strategy` is `reuse_live`, else in a worktree of its own, created by the workspace manager that keeps it. The repository must be one the manager knows. An input is set only by a person: a workflow's `turn.start` refuses an intent that takes its repository from an input, so no text a workflow reads chooses where a turn runs.

## Predicates

`visible`, `enabled`, `primary`, `records`, `when` and `also_profiles.when` are [CEL](https://github.com/google/cel-spec) expressions, checked at load.

- Types are `bool`, `int`, `string`, `list` (of strings), `duration` and `timestamp`. A predicate is a non-empty `bool`. `in`, `size()`, `exists()` and `all()` work on lists; `duration("2h")` and `timestamp("2026-10-21T09:00:00-03:00")` are literals; a timestamp minus a timestamp is a duration.
- Fields resolve against the facts of the predicate's slot: the **action** slot reads the row the daemon serves, the **turn** slot reads the turn being built. An unknown field fails with the closest name. The [facts](/reference/facts/) page lists them.
- `inputs.<name>` reads the intent's own declared inputs, in the turn slot, with the input's type.
- `!` applies before `==`, `!=`, `<`, `>` and `in`: write `!(turn.round == "first")`; `!turn.round == "first"` fails to load.
- A missing fact reads as its type's zero value; an error at evaluation reads as false.

## Templates

- **Action texts** (`reason_off`, `confirm`, `hint`) run over a nested map of the action facts: `{{.pr.slug}}`, `{{index .row.nudge.who 0}}`; `join` is the one extra function (`{{join .row.nudge.who ", "}}`).
- **Headlines** run over the turn context: `{{.PR.Slug}}`, `{{.PR.Author}}`, `{{.Who}}` ("asked for by you" or the loop's name).
- **`instructions.md.tmpl`** runs over the turn context with `missingkey=error`. `.Headline`, `.PR`, `.Wording`, `.Guidance`, `.Inputs.<name>` (its zero value when unset), `.Resume`, `.History` and `.Command` are there. The partials `history`, `resume`, `record`, `brief`, `judgment`, `review-judgment`, `items` and `unit` are {{name}}'s; a profile replaces one with a file of the same name in its `partials/` directory, as [Change a partial](/guides/custom-intent/#change-a-partial) shows. Include `{{template "history" .}}` and `{{template "record" .}}` in any intent that records, and `{{if .Resume}}{{template "resume" .}}{{end}}` in any that can be answered.
- Every text is parsed at load and rendered against sample facts and sample turns, so a template that would fail at run time fails `profile check`.

## Builtins

- Workspaces: `review` (the review workspace, created at the head), `author` (the workspace that develops it), `pr_else_main` (the pull request's workspace, else the repository's main checkout), `given` (the workspace the request names).
- Prepare: `nudge.prepare`, which refuses a nudge with the reason the catalog shows and fills the inputs `situation`, `waits_on_maintainers` and `who`.
- Before: `take_over`.
- Effects (`on_sent`): `plan.drop_pending`, `event.round_sent`, `event.nudged`, `record_round`, `keeps_guidance`, `mark_reviewed`, `mark_addressed`.

## Keys

`key` is checked on every surface the action reaches (web, terminal, menu bar). The core owns every lowercase letter, every digit and its chord prefixes, and built-in actions keep their keys. Bundled intents claim their keys first, then yours by name. One of yours with a clashing key loads without it, and `profile check` reports the clash.

## The action catalog

For every row the daemon computes the actions it offers: the built-in ones (Run now, Skip, Guide, Answer, Continue, Dismiss, Take over, Pause, Resume, Mute, Unmute, Track, Untrack, Open, Remove, Force remove, Refresh brief, Start without brief, Cancel) and each intent's `[action]`. Clients render that list and decide nothing: a request for an action the row does not offer, or offers disabled, is refused with the same reason.

## Errors

Every problem is one line, `file:line:col: field.path: message`, sorted; `profile check`, `doctor` and the daemon's start print them all:

```text
intents/ask-author/intent.toml:6:1: sesion: unknown key; did you mean session?
intents/ask-author/intent.toml:15:11: action.visible: 13: unknown field pr.mien; did you mean pr.mine?
intents/ask-author/intent.toml:15:11: action.enabled: unexpected character U+0021 '!' at start of value; a predicate is a string: quote it, as in enabled = '!pr.mine'
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
