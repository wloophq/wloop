---
title: Workflow manifest
description: Every field of workflow.toml, its triggers, approval and limits.
---

A workflow lives in `<profile>/workflows/<name>/workflow.toml`, beside the intents, and is loaded and checked the same way. The bundled ones are listed in [The sweep and workflows](/concepts/workflows/#the-bundled-workflows). A name is a lower-case letter, then up to 39 lower-case letters, digits or `-`, and may not repeat a bundled workflow; the configuration's `workflows.<name>` overrides a bundled one instead.

```toml
summary = "Close a session whose pull request is merged or closed"
provider = "session"
when = 'session.pr_state in ["MERGED", "CLOSED"]'
order = 11

[[step]]
use = "session.close"

[limits]
per_run = 10
per_day = 100
```

The top-level keys come before the first table, and a step's arguments are sibling keys of `use`:

```toml
[[step]]
use = "turn.start"
intent = "review"
stage = "follow-up"
```

## Fields

The loader refuses any other key, naming a close one when one exists. Without `[trigger]` a workflow is planned on every sweep; `on` plans it from a durable cursor of events, so an event seen while the daemon was down is handled once, and `schedule` on a cron in local time. The configuration's `workflows.<name>` overlays `observe`, `enabled`, `only_when`, `[approval]` and `[drafts.approval]`, field by field: `mode` and `window` replace those fields and `rule` replaces the rules (`[]` clears them); over a workflow without `[drafts.approval]`, the config's drafts block overlays the intent's for its turns' drafts. The config loads what holds alone; a block that fails once merged is named by `doctor`.

### What it matches

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `summary` | string |  | One line, what the workflow does; its plan items' description. Required. |
| `provider` | one of `entry`, `github`, `session`, `slack`, `workspace` |  | The provider of the units it matches. Required. |
| `when` | predicate |  | The match condition, over the unit's facts, the trigger's and config.*. Required. |
| `order` | integer | `100` | Evaluation order, lowest first. |
| `key` | list of facts |  | The facts forming the idempotency key, by default &lt;slot&gt;.fingerprint: a key that ran or was dismissed is not planned again. A workflow with trigger.on may add trigger.event.id, so each event plans its own run. |
| `marks` | table by name |  | Mark name to fact, stored when a run is done or dismissed, which handled() reads. |
| `marks.&lt;name&gt;` | fact |  | The fact whose value the mark stores. |

### When it is planned

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `trigger` | table |  | What plans the workflow: on or schedule; absent, every sweep. |
| `trigger.on` | list of `opened`, `push`, `review_requested`, `review_request_removed`, `review`, `comment`, `check`, `ready`, `draft`, `merged`, `closed`, `round_sent`, `round_held`, `round_pending`, `round_skipped`, `guidance`, `round_blocked`, `round_unblocked`, `muted`, `unmuted`, `held`, `released`, `adopted`, `tracked`, `untracked`, `nudged`, `removed`, `status`, `would_review`, `would_address`, `review_ended`, `verb_next`, `verb_waiting`, `verb_schedule`, `verb_someday`, `verb_reference`, `verb_focus`, `verb_project`, `verb_approve`, `verb_delegate`, `verb_done`, `verb_dismiss`, `verb_context`, `verb_area`, `verb_answer`, `verb_reviewed`, `verb_sequential`, `verb_reply` |  | The source event kinds the workflow reacts to; verb_&lt;verb&gt;, a verb a person ran on an entry, with provider entry. |
| `trigger.schedule` | string |  | A cron schedule, in local time. |
| `observe` | bool | `false` | Plans items pending and never runs them by itself; a person applies an item by hand. |
| `limits` | table |  | How many runs the workflow may take. |
| `limits.per_run` | integer | `0` | Runs per evaluation; 0 is no limit. |
| `limits.per_day` | integer | `0` | Runs per local day; 0 is no limit. |
| `limits.per_unit_per_day` | integer | `0` | Runs on one unit per local day; 0 is no limit. |
| `limits.cooldown` | duration |  | The least time between runs on one unit. |

### Steps

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `step[]` | table |  | A step the run takes, in order. Required: at least one. |
| `step[].use` | one of `session.close`, `workspace.remove`, `workspace.unregister`, `turn.dismiss`, `answer.refuse`, `session.mark_dormant`, `turn.start`, `draft.propose`, `classify`, `notify.self`, `entry.verb` |  | The step. |
| `step[].&lt;name&gt;` | string or list |  | The step's arguments. |

### Run approval

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `approval` | table |  | The run approval: how a matched item runs. |
| `approval.mode` | one of `run`, `delay`, `wait` | `run` | The mode when no rule matches. |
| `approval.window` | duration |  | How long a person has before the item runs; only with mode delay, which needs it. |
| `approval.rule[]` | table |  | A rule tried in order; the first whose when holds gives the mode. |
| `approval.rule[].when` | predicate |  | When the rule applies. |
| `approval.rule[].mode` | one of `run`, `delay`, `wait` |  | The mode when the rule applies. |
| `approval.rule[].window` | duration |  | The rule's window; only with mode delay, which needs it. |

### Drafts

| Key | Kind | Default | Meaning |
|---|---|---|---|
| `drafts` | table |  | The drafts the workflow's steps and turns propose. |
| `drafts.withdraw` | table |  | When an open draft the workflow proposed is withdrawn. |
| `drafts.withdraw.when` | predicate |  | When an open draft the workflow proposed is withdrawn, over the unit's facts and the draft's. |
| `drafts.withdraw.reason` | one of `assigned`, `moot` | `moot` | The reason the withdrawn draft stores. |
| `drafts.approval` | table |  | The approval of the drafts the workflow's steps and turns propose. |
| `drafts.approval.mode` | one of `send`, `delay`, `wait`, `dry_run` | `wait` | The mode when no rule matches. |
| `drafts.approval.window` | duration |  | How long a person has before the draft is sent; only with mode delay, which needs it. |
| `drafts.approval.rule[]` | table |  | A rule tried in order; the first whose when holds gives the mode. |
| `drafts.approval.rule[].when` | predicate |  | When the rule applies. |
| `drafts.approval.rule[].mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The mode when the rule applies. |
| `drafts.approval.rule[].window` | duration |  | The rule's window; only with mode delay, which needs it. |

## Steps

Steps run in order on each matching unit. The vocabulary is closed; the [facts](/reference/facts/#workflow-steps) page lists what each step does, and `{{command}} workflows steps` prints them with their arguments. A step takes only its own arguments:

| Step | Argument | Kind | Default | Meaning |
|---|---|---|---|---|
| `turn.dismiss` | `reason` | string |  | The reason the dismissed turn records. |
| `answer.refuse` | `reason` | string |  | The reason the refused answer records. |
| `turn.start` | `intent` | string |  | The intent whose turn starts; it must serve the unit. Required. |
| `turn.start` | `stage` | one of `first`, `follow-up` |  | The pass over the unit: its first, or one after. |
| `turn.start` | `runner` | one of `session`, `headless` |  | How the turn runs, one of the intent's runners; by default its first. |
| `draft.propose` | `purpose` | string |  | What the draft is for, such as nudge or close. Required. |
| `draft.propose` | `delivery` | one of `message`, `reaction`, `comment` | `message` | How the draft posts. |
| `draft.propose` | `text` | file | `text.md.tmpl` | The template of the draft's text, beside the manifest. |
| `draft.propose` | `reaction` | string |  | The emoji name, with delivery reaction. |
| `draft.propose` | `items` | one of `owed`, `resolved`, `unowned`, `unowned_aged`, `all` | `all` | Which of the unit's items the draft lists. |
| `draft.propose` | `per` | one of `unit`, `person` | `unit` | One draft per unit, or one per person it waits on. |
| `draft.propose` | `target` | one of `unit`, `item` | `unit` | Where the draft posts: on the unit, or on each item; an intent's help.target is free text. |
| `draft.propose` | `effects` | list of `entry.nudged`, `unit.closed`, `pr.nudged` |  | What sending the draft applies. |
| `draft.propose` | `repeat_after` | string |  | The least time after a settled draft of the same person before another: a duration, or a config.* duration fact such as config.drafts.repeat_after. |
| `draft.propose` | `repeat_after_sent` | string |  | The same after a sent draft, by default repeat_after: a duration, a config.* duration fact, or forever. |
| `classify` | `prompt` | file | `prompt.md` | The classification's prompt, beside the manifest. |
| `classify` | `schema` | file | `schema.json` | The JSON schema of its answer, beside the manifest. |
| `notify.self` | `text` | file | `text.md.tmpl` | The template of the notification's text, beside the manifest: `.Workflow`, `.Unit` and `.Facts`. |
| `entry.verb` | `verb` | one of `next`, `waiting`, `schedule`, `someday`, `reference`, `focus`, `context`, `area`, `done`, `dismiss` |  | The verb run on the entry. Required. |
| `entry.verb` | `until` | string |  | With schedule, the date the entry returns: RFC 3339 or YYYY-MM-DD. |
| `entry.verb` | `on_activity` | string |  | With schedule, `true` returns the entry on new activity on its unit. |
| `entry.verb` | `on` | string |  | With waiting, the people the entry waits on, comma-separated. |
| `entry.verb` | `resurface` | string |  | With waiting, the date the entry resurfaces: RFC 3339 or YYYY-MM-DD. |
| `entry.verb` | `set` | string |  | With context, the contexts, comma-separated; with area, the area. |

- A guard refuses a step instead of skipping it: the item stays in the plan with the reason, and runs when the guard passes.
- A step that fails retries with backoff, three times per key, then stays `blocked` with its error.
- `workspace.remove` never forces: a manifest that sets `force` fails to load.
- A `turn.start` naming an unknown, broken or `builds` intent breaks its workflow. On a pull request, caps, the brief, and the busy and held checks apply as for any automatic round. On any other unit, the workflow must set `limits.per_day`, and every such turn of every workflow counts toward `turns.per_day` for the local day; a turn of the intent still open on the unit, or an intent whose repository comes from an input, refuses the step. The turn runs with the step's `runner`, else the intent's first runner, where the intent's [workspace](/reference/intent-manifest/#where-a-turn-runs) says.

## Approval

`[approval]` says how a matched item runs, and `[[approval.rule]]` overrides it for the items a rule's `when` holds for, the first match winning:

- `mode = "run"`, the default, runs it at once;
- `mode = "delay"` with a `window` runs it when the window ends unless you act;
- `mode = "wait"` keeps it `pending` until you apply or dismiss it, as the workspace workflows' rule does for a workspace with unsaved work, with the reason shown.

The bundled `turn.start` workflows set `mode = "delay"` and `window = "60s"`. A rule that runs an item (`run` or `delay`) never reads an untrusted fact, `trigger.event.data` included; a `wait` rule may, and the workflow's `when` reads any fact. `observe = true` plans items and never runs them on its own: you apply one by hand. Dismissing or skipping an item is final for its key; holding, pausing or starting the round by hand drops the item without marking it.

## Draft approval

`[drafts.approval]` tries its rules in order and gives a draft the mode of the first whose `when` holds, else `mode`:

```toml
[drafts.approval]
mode = "wait"

[[drafts.approval.rule]]
when = 'draft.purpose == "thanks" && slack.conversation == "dm"'
mode = "send"
```

It covers the drafts of the turns the workflow starts and of its `draft.propose` steps; without it, a turn's drafts follow the intent's, and a step's wait for you. A rule's `when` reads the [draft approval facts](/reference/facts/#draft-approval-facts) and the unit's facts, never `brief.*`, `trigger.*`, `config.*` or `handled()`. `draft.purpose` is the `draft.propose` step's `purpose`; for a draft of a turn the workflow started it reads as empty, since the turn's agent names it. A rule that sends on its own (`send` or `delay`) applies to a comment draft only when `draft.delivery == "comment"` is a term of its `when`'s top-level `&&`, and `mode` alone never releases a comment. `dry_run` stores the draft and never sends it. `[drafts.withdraw] when` withdraws an open draft the workflow's `draft.propose` proposed, with its `reason`.

## Predicates

The language is the one of the [intent manifest](/reference/intent-manifest/#predicates), with the workflow facts. `!x == y` and `!x in l` fail to compile with a hint: write `!(x == y)`.

A subtraction from a zero timestamp is an evaluation error, so providers omit a fact instead of returning zero; test for it first (`session.idle_since` is absent for a session never seen idle, and `idle_for` is 0).

## Marks

A mark is what a unit's trigger already handled, kept on the unit after the plan and its runs are gone. `[marks]` names a string, time or int fact of the provider's slot for each mark; an item takes the facts' values when it is planned, and running it to the end or dismissing it stores them on the unit as `<workflow>/<name>`. A predicate reads a mark back with `handled("<workflow>/<name>", <fact>)`:

```toml
summary = "Address a lasting conflict once per head"
provider = "github"
when = 'pr.mine && pr.conflicting_for > duration("10m") && !handled("conflict-once/head", pr.head)'
key = ["pr.head"]

[[step]]
use = "turn.start"
intent = "address"

[marks]
head = "pr.head"
```

`handled` compares by the fact's type: a string is a version, handled by the same value; a time or an int is a watermark, handled up to the stored one, which a later run never moves back. A unit with no such mark has handled nothing, and an empty fact (an empty string, a zero time) counts as handled, since there is nothing to handle. A workflow writes only its own marks. It may read any mark a loaded workflow records, and these of the GitHub plugin, on a pull request you develop: `addressed.conflict` and `addressed.checks` (the head an address round covered, read with `pr.head`) and `addressed.reviews` (the newest review it triaged, a time). The mark's name is a string literal, checked when the profile loads. A mark name has no slash, space or control character.

## Checking

```sh
{{command}} profile check
{{command}} plan --workflow <name>
{{command}} profile schema --workflow > <profile>/workflows/workflow.schema.json
```

With `#:schema ../workflow.schema.json` at the top of a manifest, Taplo-based editors complete and check it as you type. A workflow that fails to load disables only itself; `profile check` and `doctor` list its problems.
