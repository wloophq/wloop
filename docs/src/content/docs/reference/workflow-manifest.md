---
title: Workflow manifest
description: Every field of workflow.toml, its triggers, approval and limits.
---

A workflow lives in `<profile>/workflows/<name>/workflow.toml`, beside the intents, and is loaded and checked the same way. The bundled ones are listed in [The sweep and workflows](/concepts/workflows/#the-bundled-workflows). A name is a lower-case letter, then up to 39 lower-case letters, digits or `-`, and may not repeat a bundled workflow; the configuration's `workflows.<name>` overrides a bundled one instead.

```toml
description = "Close a session whose pull request is merged or closed"
subject = "session"
when = 'session.pr_state in ["merged", "closed"]'
mode = "apply"
order = 11

[trigger]
every = "sweep"

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
round = "follow-up"
```

## Fields

| Field | Required | Default | Means |
|---|---|---|---|
| `description` | yes | | one line |
| `subject` | yes | | the unit provider: `github` (slot `pr`), `session` or `workspace` |
| `when` | yes | | a predicate over the subject's slot, `trigger.*`, `config.*` and `now` |
| `mode` | | `apply` | `observe` plans and runs nothing; `apply` runs the steps |
| `order` | | `100` | evaluation order among workflows on the same trigger, lower first |
| `allow_visible` | | `false` | lets a step whose effect others see run without approval |
| `key` | | the subject's `fingerprint` fact | facts that identify one run: a key that ran is not run again |
| `[marks]` | | none | `<name> = "<fact>"`: what a done or dismissed run records on its unit; see [Marks](#marks) |
| `[trigger]` | yes | | exactly one of `every = "sweep"` (on every sweep), `on = [<event kinds>]` (from a durable cursor, so an event seen while the daemon was down is handled once) or `schedule = "<cron>"` (local time) |
| `[[step]]` | at least one | | `use` names a builtin step; other keys are its arguments, all strings |
| `[limits]` | | none | `per_run`, `per_day`, `per_unit_per_day`, `cooldown` |
| `[approval]` | | none | `default`, `window`, `wait_when` |
| `[draft_approval]` | | the intent's, else `wait` | `mode`, `window` and `[[draft_approval.rule]]` (`when`, `mode`, `window`): the policy of the drafts the turns this workflow starts, and its `draft.propose` steps, propose; see [Draft approval](#draft-approval). `workflows.<name>.draft_approval` in the configuration replaces it, rules included |
| `[withdraw]` | | none | `when`, a predicate over the subject's slot and `draft.*`, withdraws an open draft the workflow's `draft.propose` proposed; `reason` is `moot` (the default) or `assigned` |

## Steps

Steps run in order on each matching unit. The vocabulary is closed; the [facts](/reference/facts/#workflow-steps) page lists every step, and `{{command}} workflows steps` prints them with their arguments.

- A guard refuses a step instead of skipping it: the item stays in the plan with the reason, and runs when the guard passes.
- A step that fails retries with backoff, three times per key, then stays `blocked` with its error.
- `workspace.remove` never forces: a manifest that sets `force` fails to load.
- A `turn.start` naming an unknown, broken or `builds` intent breaks its workflow. On a pull request, caps, the brief, and the busy and held checks apply as for any automatic round. On any other unit, the workflow must set `limits.per_day`, and every such turn of every workflow counts toward `turns.automated_per_day` for the local day; a turn of the intent still open on the unit, or an intent whose repository comes from an input, refuses the step. The turn runs in the step's `mode`, else the intent's first mode, where the intent's [workspace](/reference/intent-manifest/#where-a-turn-runs) says.

## Approval

`[approval]` makes a plan item `pending` before it runs:

- `default = "run"` runs it when `window` ends unless you act;
- `default = "wait"` keeps it until you apply or dismiss it;
- `wait_when` is a predicate that holds the item as `wait` with the reason shown, as the workspace workflows do for a workspace with unsaved work.

A workflow with no `window` runs a `default = "run"` item at once; the bundled `turn.start` workflows set `window = "60s"`. Dismissing or skipping an item is final for its key; holding, pausing or starting the round by hand drops the item without marking it.

## Draft approval

`[draft_approval]` tries its rules in order and gives a draft the mode of the first whose `when` holds, else `mode`:

```toml
[draft_approval]
mode = "wait"

[[draft_approval.rule]]
when = 'draft.purpose == "thanks" && slack.conversation == "dm"'
mode = "send"
```

A rule's `when` reads the [draft approval facts](/reference/facts/#draft-approval-facts) and the subject's slot, never `brief.*`, `trigger.*`, `config.*` or `handled()`. `draft.purpose` is the `draft.propose` step's `purpose`; for a draft of a turn the workflow started it reads as empty, since the turn's agent names it. A rule that sends on its own (`send` or `delay`) applies to a comment draft only when `draft.delivery == "comment"` is a term of its `when`'s top-level `&&`, and `mode` alone never releases a comment.

## Predicates

The language is the one of the [intent manifest](/reference/intent-manifest/#predicates), with the workflow facts, and two traps:

- `!x == y` and `!x in l` fail to compile with a hint: write `!(x == y)`.
- `pr.repo` is `owner/repo` in a workflow, and the repository name in an intent.

A subtraction from a zero timestamp is an evaluation error, so providers omit a fact instead of returning zero; test for it first (`session.idle_since` is absent for a session never seen idle, and `idle_for` is 0).

## Marks

A mark is what a unit's trigger already handled, kept on the unit after the plan and its runs are gone. `[marks]` names a string, time or int fact of the subject's slot for each mark; an item takes the facts' values when it is planned, and running it to the end or dismissing it stores them on the unit as `<workflow>/<name>`. A predicate reads a mark back with `handled("<workflow>/<name>", <fact>)`:

```toml
description = "Address a lasting conflict once per head"
subject = "github"
when = 'pr.mine && pr.conflicting_for > duration("10m") && !handled("conflict-once/head", pr.head)'
key = ["pr.head"]

[trigger]
every = "sweep"

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
