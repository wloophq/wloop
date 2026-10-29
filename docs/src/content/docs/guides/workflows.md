---
title: Tune workflows
description: Switch workflows on and off, change their windows, let cleanup remove workspaces and let nudges go out.
---

The `workflows` table of `config.toml` tunes a bundled workflow, or one of yours, without editing its manifest:

```toml
[workflows.<name>]
enabled = false
observe = true
only_when = 'pr.visibility == "private"'

[[workflows.<name>.gates.approval.rule]]
when = 'gate.kind == "plan"'
mode = "delay"
window = "5m"
```

| Key | Values |
|---|---|
| `enabled` | `false` drops the workflow |
| `observe` | `true` plans and runs nothing by itself; `false` runs the steps |
| `gates.approval.mode` | the mode of every gate the workflow opens: its runs' plan gates, its drafts and its turns' drafts, its `gate.open` gates. `run` and `send` release a gate at once, `delay` when its window ends, `wait` keeps it until you resolve it, `dry_run` stores a draft without sending it |
| `gates.approval.window` | how long a `delay` gate waits, such as `"60s"` or `"5m"` |
| `gates.approval.rule` | rules tried before `mode`, the first whose `when` holds gives the gate's mode; a rule applies to the kinds of gate its `when` pins `gate.kind` to, to draft gates alone when it reads a draft's facts, else to every kind; set, they replace the manifest's, and `[]` clears them |
| `only_when` | a predicate over the workflow's [facts](/reference/facts/#workflow-facts) that must hold besides the manifest's `when`; where it does not, the workflow plans nothing |

The review and address workflows wait `60s` before a round starts, by a rule of their manifests pinned to `gate.kind == "plan"`. A rule whose `when` reads `gate.kind` alone decides every gate of that kind, as a mode of its own. To start that workflow's rounds at once, pin a rule to its plan gates, so its drafts keep their own approval:

```toml
[[workflows.review-follow-up.gates.approval.rule]]
when = 'gate.kind == "plan"'
mode = "run"
```

Each key of `gates.approval` overlays the manifest's own field by field: `mode` and `window` replace those fields for every kind of gate and keep the manifest's rules, so `window = "5m"` alone lengthens a `delay` workflow's window. What neither the config nor the manifest decides, `gates.toml`'s `[approval]` does: a run starts at once, every other decision waits for you. The config refuses at load what no manifest could fix, such as a `window` beside `mode = "wait"`, a rule that releases a gate on an untrusted fact, or `dry_run` pinned to a kind but `draft`; what only fails once merged, such as a `window` over a manifest that waits, stops that workflow, and `{{command}} doctor` and the daemon's log name it.

## Read the plan first

```sh
{{command}} plan
{{command}} plan --workflow workspace-review-finished
{{command}} plan --unit https://github.com/acme/api/pull/7
{{command}} doctor
```

`doctor` reports whether each workflow observes, its last run, refusals of the last day and stuck cursors; for an observe workflow, the items it plans and the `config.toml` line that switches it. It warns about a `workflows.<name>` entry that names no workflow.

## Let cleanup remove workspaces

The four workflows that remove workspaces ship as observe, because a removal cannot be undone: `workspace-review-finished`, `workspace-author-finished`, `workspace-temporary` and `workspace-duplicate`. Read their plans for a few working days. When none shows an item that should have been blocked, switch each one:

```toml
# read their plans for a few working days first
[workflows]
workspace-review-finished.observe = false
workspace-author-finished.observe = false
workspace-temporary.observe = false
workspace-duplicate.observe = false
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

`nudge-silent-30d` posts in your name, so each nudge waits for you. Once they read the way you want, let them start when the window ends:

```toml
[[workflows.nudge-silent-30d.gates.approval.rule]]
when = 'gate.kind == "plan"'
mode = "delay"
window = "5m"
```

The comment each nudge drafts still waits for you, unless a rule that names its delivery, `draft.delivery == "comment"`, releases it.

`plugins.github.nudge.silent_days` (30) sets the silence before a nudge.

## Tune the Slack proposals

`slack-stalled-nudge`, `slack-closure` and `slack-ask-owner` propose drafts in the Outbox, where they wait for you. `gates.approval` sets when their drafts go out, as the [draft policies](/concepts/approval/#the-outbox) say, its `rule` list tried before its `mode`. A Slack message posts, as the Slack plugin's manifest marks it, so only a rule that names its delivery releases one; a rule over a draft's facts applies to draft gates alone, and leaves the workflow's runs as they are:

```toml
[[workflows.slack-closure.gates.approval.rule]]
when = 'draft.delivery == "message"'
mode = "delay"
window = "10m"
```

A rule picks the mode by the draft's and the unit's facts; this one sends a closing reaction at once and keeps the window for a closing message:

```toml
[[workflows.slack-closure.gates.approval.rule]]
when = 'draft.delivery == "reaction"'
mode = "send"

[[workflows.slack-closure.gates.approval.rule]]
when = 'draft.delivery == "message"'
mode = "delay"
window = "10m"
```

A rule never reads a fact the [facts](/reference/facts/) page marks **Untrusted**, such as `task.link_started_by_me`, which the extraction reads from the thread's text. The config's block overlays the workflow's `[gates.approval]` the same way, a `rule` list alone keeping its mode; for the drafts of a workflow whose manifest names none for a draft gate, it overlays the approval of the intent whose turns the workflow starts, else `gates.toml`'s default for a draft gate. One that does not merge, such as a `window` over a mode other than `delay`, holds every draft of the workflow for you, and `{{command}} doctor` names it.

A workflow's `observe` and a draft's `dry_run` mode differ: `observe = true` plans the workflow and proposes nothing, while a `dry_run` rule over a draft's facts proposes each draft and stores it as what would have been sent, without sending it.

Their texts are `text.md.tmpl` in each workflow's directory, which the Slack plugin's profile layer ships in `plugins/slack/profile/workflows/`, a [template](/reference/workflow-manifest/) over the draft's items, where `.Line` is an item's summary on one line. `<profile>/workflows/slack-closure/text.md.tmpl` closes in your words:

```
Encerrando:
{{range .Items}}
- {{.Line}}: {{or .Resolution "resolvido"}}{{end}}
```

Your own `<profile>/workflows/slack-closure/workflow.toml` with `delivery = "reaction"`, `reaction = "white_check_mark"` and `target = "item"` in its step closes with a ✅ on the resolved message instead; its `effects = ["unit.closed"]` still records the closure and ends the thread's tasks. A reaction carries no text: the workflow's `text.md.tmpl` is not read, and a step that wants a text with its reaction names another file in `text`.

## Narrow one to some pull requests

`only_when` keeps a workflow where its predicate holds, in the [predicate language](/reference/intent-manifest/#predicates) of its manifest. Automatic address rounds, only in private repositories:

```toml
[workflows.address]
only_when = 'pr.visibility == "private"'
```

Not in one organization, not in some repositories, not on one author's pull requests:

```toml
[workflows.address]
only_when = 'pr.owner != "acme" && !(pr.repo in ["me/blog", "me/dotfiles"])'

[workflows.review-first-round]
only_when = 'pr.author != "renovate[bot]"'
```

`pr.visibility` is empty until the pull request is read in full, so `pr.visibility == "private"` plans nothing for a pull request not read yet. A predicate that does not compile stops the configuration from loading. `{{command}} plan --unit <url>` lists the facts `only_when` read, with the manifest's.

## Turn one off

```toml
[workflows]
review-first-round.enabled = false
session-duplicate.enabled = false
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

A workflow can also look at your tasks, `provider = "task"`, every task neither done nor in the trash of an enabled plugin, or at the Slack units a live task is attached to, `provider = "slack"`. This one tells you about a task someone else owes once its check date passed:

```toml
summary = "Tell me when a task I am owed is due a check"
provider = "task"
when = 'task.owed_by != "me" && task.owed_by != "" && task.check_on > timestamp(0) && task.check_on <= now'
key = ["task.owed_by"]

[[step]]
use = "notify.self"
```

A workflow can react to a verb you run on a task. This one tells you whenever you mark a task done that came from the Inbox:

```toml
summary = "Tell me when I finish something straight from the Inbox"
provider = "task"
when = 'trigger.event.kind == "verb_done" && trigger.event.data == "inbox"'
key = ["trigger.event.id"]

[trigger]
on = ["verb_done"]

[[step]]
use = "notify.self"
```

`<slot>.settled_at` is the newest draft the workflow itself proposed on the unit that was sent or discarded, so a key of `["task.settled_at"]` opens a new run after each settlement.

The [workflow manifest](/reference/workflow-manifest/) lists every field and the [facts](/reference/facts/) every condition can read. `{{command}} profile check` loads it; a broken workflow disables only itself.

## Rewrite a bundled one

A directory of your profile named as a bundled workflow overrides it: `<profile>/workflows/review-first-round/workflow.toml` replaces the bundled manifest whole, so you can change its predicate, steps or trigger. Other files you put there replace the bundled files of the same name. The `workflows` table of `config.toml` still applies on top. Files whose name starts with `.` are ignored. `profile check` lists the workflow as `bundled, overridden: workflow.toml`; when your manifest does not load or cannot be read, the bundled workflow stays, the problem names your file, and `{{command}} doctor` lists the workflow as running as bundled.
