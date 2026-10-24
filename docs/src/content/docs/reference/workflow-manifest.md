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
| `[trigger]` | yes | | exactly one of `every = "sweep"` (on every sweep), `on = [<event kinds>]` (from a durable cursor, so an event seen while the daemon was down is handled once) or `schedule = "<cron>"` (local time) |
| `[[step]]` | at least one | | `use` names a builtin step; other keys are its arguments, all strings |
| `[limits]` | | none | `per_run`, `per_day`, `per_unit_per_day`, `cooldown` |
| `[approval]` | | none | `default`, `window`, `wait_when` |
| `[draft_approval]` | | the intent's, else `always` | `mode`, `window`: the policy of the drafts the turns this workflow starts propose |

## Steps

Steps run in order on each matching unit. The vocabulary is closed; the [facts](/reference/facts/#workflow-steps) page lists every step, and `{{command}} workflows steps` prints them with their arguments.

- A guard refuses a step instead of skipping it: the item stays in the plan with the reason, and runs when the guard passes.
- A step that fails retries with backoff, three times per key, then stays `blocked` with its error.
- `workspace.remove` never forces: a manifest that sets `force` fails to load.
- A `turn.start` naming an unknown or broken intent breaks its workflow. Caps, the brief, and the busy and held checks apply as for any automatic round.

## Approval

`[approval]` makes a plan item `pending` before it runs:

- `default = "run"` runs it when `window` ends unless you act;
- `default = "wait"` keeps it until you apply or dismiss it;
- `wait_when` is a predicate that holds the item as `wait` with the reason shown, as the workspace workflows do for a workspace with unsaved work.

A workflow with no `window` runs a `default = "run"` item at once; the bundled `turn.start` workflows set `window = "60s"`. Dismissing or skipping an item is final for its key; holding, pausing or starting the round by hand drops the item without marking it.

## Predicates

The language is the one of the [intent manifest](/reference/intent-manifest/#predicates), with the workflow facts, and two traps:

- `!x == y` and `!x in l` fail to compile with a hint: write `!(x == y)`.
- `pr.repo` is `owner/repo` in a workflow, and the repository name in an intent.

A subtraction from a zero timestamp is an evaluation error, so providers omit a fact instead of returning zero; test for it first (`session.idle_since` is absent for a session never seen idle, and `idle_for` is 0).

## Checking

```sh
{{command}} profile check
{{command}} plan --workflow <name>
{{command}} profile schema --workflow > <profile>/workflows/workflow.schema.json
```

With `#:schema ../workflow.schema.json` at the top of a manifest, Taplo-based editors complete and check it as you type. A workflow that fails to load disables only itself; `profile check` and `doctor` list its problems.
