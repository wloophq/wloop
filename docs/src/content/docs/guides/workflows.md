---
title: Tune workflows
description: Switch workflows on and off, change their windows, let cleanup remove workspaces and let nudges go out.
---

The `workflows` key of `config.json` tunes a bundled workflow, or one of yours, without editing its manifest:

```json
"workflows": {
  "<name>": {
    "enabled": false,
    "observe": true,
    "approval": { "mode": "delay", "window": "5m" },
    "only_when": "pr.visibility == \"private\""
  }
}
```

| Key | Values |
|---|---|
| `enabled` | `false` drops the workflow |
| `observe` | `true` plans and runs nothing by itself; `false` runs the steps |
| `approval.mode` | `run` runs an item at once; `delay` runs a pending item when its window ends; `wait` keeps it until you apply or dismiss it |
| `approval.window` | how long a `delay` item waits, such as `"60s"` or `"5m"` |
| `approval.rule` | rules tried before `mode`, the first whose `when` holds gives the item's mode; set, they replace the manifest's, and `[]` clears them |
| `only_when` | a predicate over the workflow's [facts](/reference/facts/#workflow-facts) that must hold besides the manifest's `when`; where it does not, the workflow plans nothing |

The review, address and nudge workflows wait `60s`, the `window` of their manifests. `"workflows": {"review-follow-up": {"approval": {"mode": "run"}}}` sends that workflow's rounds at once; set it per workflow. Each key of `approval` overlays the manifest's own field by field: `mode` and `window` replace those fields and keep the manifest's rules, so `{"window": "5m"}` alone lengthens a `delay` workflow's window. The config refuses at load what no manifest could fix, such as a `window` beside `mode: "wait"` or a rule that runs on an untrusted fact; what only fails once merged, such as a `window` over a manifest that waits, stops that workflow, and `{{command}} doctor` and the daemon's log name it.

## Read the plan first

```sh
{{command}} plan
{{command}} plan --workflow workspace-review-finished
{{command}} plan --unit https://github.com/acme/api/pull/7
{{command}} doctor
```

`doctor` reports whether each workflow observes, its last run, refusals of the last day and stuck cursors; for an observe workflow, the items it plans and the `config.json` entry that switches it. It warns about a `workflows.<name>` entry that names no workflow.

## Let cleanup remove workspaces

The four workflows that remove workspaces ship as observe, because a removal cannot be undone: `workspace-review-finished`, `workspace-author-finished`, `workspace-temporary` and `workspace-duplicate`. Read their plans for a few working days. When none shows an item that should have been blocked, switch each one:

```json
"workflows": {
  "workspace-review-finished": { "observe": false },
  "workspace-author-finished": { "observe": false },
  "workspace-temporary":       { "observe": false },
  "workspace-duplicate":       { "observe": false }
}
```

Even when a removal runs on its own, a workspace that holds work is never removed: the item is blocked or waits for you, with the reason and a backup command (see [cleanup guards](/concepts/workflows/#cleanup-guards)).

The thresholds:

| Key | Default | For |
|---|---|---|
| `cleanup.idle` | `2h` | closing a session of the loop whose turn ended |
| `cleanup.asked_idle` | `4h` | closing a session of the loop whose turn asked |
| `cleanup.manual_idle` | `1d` | offering to close a session you started |
| `cleanup.workspace_grace` | `1d` (working time) | removing a workspace after its pull request ended |
| `cleanup.stale_workspace` | `14d` | offering to remove a workspace with no pull request |
| `cleanup.disposable` | `node_modules`, `.venv`, `dist`, `build`, `target`, `.terraform`, ... | ignored names a workspace may lose; setting it replaces the whole list |

## Let nudges go out

`nudge-silent-30d` posts in your name, so each nudge waits for you. Once they read the way you want, let them go out when the window ends:

```json
"workflows": { "nudge-silent-30d": { "approval": { "mode": "delay", "window": "5m" } } }
```

`github.nudge.silent_days` (30) sets the silence before a nudge.

## Tune the Slack proposals

`slack-stalled-nudge`, `slack-closure` and `slack-ask-owner` propose drafts in the Outbox, where they wait for you. `drafts.approval` sets when their drafts go out, as the [draft policies](/concepts/approval/#the-outbox) say, its `rule` list tried before its `mode`:

```json
"workflows": { "slack-closure": { "drafts": { "approval": { "mode": "delay", "window": "10m" } } } }
```

A rule picks the mode by the draft's and the unit's facts; this one sends a closing reaction at once and keeps the window for a closing message:

```json
"workflows": { "slack-closure": { "drafts": { "approval": { "mode": "delay", "window": "10m",
  "rule": [{ "when": "draft.delivery == \"reaction\"", "mode": "send" }] } } } }
```

A rule never reads a fact the [facts](/reference/facts/) page marks **Untrusted**, such as `entry.unit_started_by_me`, which the extraction reads from the thread's text. The config's block overlays the workflow's `[drafts.approval]` the same way, `{"rule": [...]}` alone keeping its mode; on a workflow that has none, it overlays the block of the intent whose turns the workflow starts, and `wait` for the drafts the workflow proposes itself. One that does not merge, such as a `window` over a mode other than `delay`, holds every draft of the workflow for you, and `{{command}} doctor` names it.

A workflow's `observe` and a draft's `dry_run` mode differ: `"observe": true` plans the workflow and proposes nothing, while `"drafts": {"approval": {"mode": "dry_run"}}` proposes each draft and stores it as what would have been sent, without sending it.

Their texts are `text.md.tmpl` in each workflow's directory, a [template](/reference/workflow-manifest/) over the draft's items, where `.Line` is an item's summary on one line. `<profile>/workflows/slack-closure/text.md.tmpl` closes in your words:

```
Encerrando:
{{range .Items}}
- {{.Line}}: {{or .Resolution "resolvido"}}{{end}}
```

Your own `<profile>/workflows/slack-closure/workflow.toml` with `delivery = "reaction"`, `reaction = "white_check_mark"` and `target = "item"` in its step closes with a ✅ on the resolved message instead; its `effects = ["unit.closed"]` still ends the close entry. A reaction carries no text: the workflow's `text.md.tmpl` is not read, and a step that wants a text with its reaction names another file in `text`.

## Narrow one to some pull requests

`only_when` keeps a workflow where its predicate holds, in the [predicate language](/reference/intent-manifest/#predicates) of its manifest. Automatic address rounds, only in private repositories:

```json
"workflows": { "address": { "only_when": "pr.visibility == \"private\"" } }
```

Not in one organization, not in some repositories, not on one author's pull requests:

```json
"workflows": {
  "address": { "only_when": "pr.owner != \"acme\" && !(pr.repo in [\"me/blog\", \"me/dotfiles\"])" },
  "review-first-round": { "only_when": "pr.author != \"renovate[bot]\"" }
}
```

`pr.visibility` is empty until the pull request is read in full, so `pr.visibility == "private"` plans nothing for a pull request not read yet. A predicate that does not compile stops the configuration from loading. `{{command}} plan --unit <url>` lists the facts `only_when` read, with the manifest's.

## Turn one off

```json
"workflows": {
  "review-first-round": { "enabled": false },
  "session-duplicate": { "enabled": false }
}
```

## Pause

```sh
{{command}} pause                                         # every workflow but observe ones, and board moves, everywhere
{{command}} pause acme/api                                # only in acme/api
{{command}} pause https://github.com/acme/api/pull/7      # only on that pull request
{{command}} resume acme/api                               # what pause named
{{command}} resume                                        # everything, pull requests and repositories included
```

Resuming less than a wider pause holds is refused: with everything paused, `resume acme/api` tells you to resume everything instead.

## Write your own

A workflow of yours goes in `<profile>/workflows/<name>/workflow.toml`. This one sends you a private notification about your pull requests that wait on you for more than two days, its text in `text.md.tmpl` beside the manifest, a template over `.Workflow`, `.Unit` and `.Facts`, the unit's facts by name (`{{index .Facts "pr.slug"}} waits on you`):

```toml
summary = "Tell me about my pull requests that wait on me for two days"
provider = "github"
when = 'pr.state == "OPEN" && pr.mine && pr.waits_on_user && pr.age > duration("48h")'

[[step]]
use = "notify.self"
```

With no `[trigger]`, a workflow is evaluated on every sweep.

A workflow can also look at your GTD entries, `provider = "entry"`, every entry neither done nor in the trash of an enabled plugin, or at the Slack units a live entry is attached to, `provider = "slack"`. This one tells you about a waiting entry once its stalled nudge is due:

```toml
summary = "Tell me when a waiting entry is due a nudge"
provider = "entry"
when = 'entry.nudge_due_at != timestamp("0001-01-01T00:00:00Z") && entry.items_owed > 0'
key = ["entry.nudge_due_at"]

[[step]]
use = "notify.self"
```

A workflow can react to a verb you run on an entry. This one tells you whenever you mark an entry done that came from the Inbox:

```toml
summary = "Tell me when I finish something straight from the Inbox"
provider = "entry"
when = 'trigger.event.kind == "verb_done" && trigger.event.data == "inbox"'
key = ["trigger.event.id"]

[trigger]
on = ["verb_done"]

[[step]]
use = "notify.self"
```

`<slot>.settled_at` is the newest draft the workflow itself proposed on the unit that was sent or discarded, so a key of `["entry.settled_at"]` opens a new run after each settlement.

The [workflow manifest](/reference/workflow-manifest/) lists every field and the [facts](/reference/facts/) every condition can read. `{{command}} profile check` loads it; a broken workflow disables only itself.

## Rewrite a bundled one

A directory of your profile named as a bundled workflow overrides it: `<profile>/workflows/review-first-round/workflow.toml` replaces the bundled manifest whole, so you can change its predicate, steps or trigger. Other files you put there replace the bundled files of the same name. The `workflows` key of `config.json` still applies on top. Files whose name starts with `.` are ignored. `profile check` lists the workflow as `bundled, overridden: workflow.toml`; when your manifest does not load or cannot be read, the bundled workflow stays, the problem names your file, and `{{command}} doctor` lists the workflow as running as bundled.
