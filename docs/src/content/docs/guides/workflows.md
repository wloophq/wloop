---
title: Tune workflows
description: Switch workflows on and off, change their windows, let cleanup remove workspaces and let nudges go out.
---

The `workflows` key of `config.json` tunes a bundled workflow, or one of yours, without editing its manifest:

```json
"workflows": {
  "<name>": {
    "enabled": false,
    "mode": "apply",
    "approval": { "default": "run", "window": "5m" },
    "only_when": "pr.visibility == \"private\""
  }
}
```

| Key | Values |
|---|---|
| `enabled` | `false` drops the workflow |
| `mode` | `observe` plans and runs nothing; `apply` runs the steps |
| `approval.default` | `run` runs a pending item when its window ends; `wait` keeps it until you apply or dismiss it |
| `approval.window` | how long a pending item waits, such as `"60s"` or `"5m"` |
| `only_when` | a predicate over the workflow's [facts](/reference/facts/#workflow-facts) that must hold besides the manifest's `when`; where it does not, the workflow plans nothing |

The review, address and nudge workflows wait `60s`, the `window` of their manifests. `"workflows": {"review-follow-up": {"approval": {"window": "0s"}}}` sends that workflow's rounds at once; set it per workflow.

## Read the plan first

```sh
{{command}} plan
{{command}} plan --workflow workspace-review-finished
{{command}} plan --unit https://github.com/acme/api/pull/7
{{command}} doctor
```

`doctor` reports each workflow's mode, last run, refusals of the last day and stuck cursors; for an observe workflow, the items it plans and the `config.json` entry that switches it. It warns about a `workflows.<name>` entry that names no workflow.

## Let cleanup remove workspaces

The four workflows that remove workspaces ship in observe mode, because a removal cannot be undone: `workspace-review-finished`, `workspace-author-finished`, `workspace-temporary` and `workspace-duplicate`. Read their plans for a few working days. When none shows an item that should have been blocked, switch each one:

```json
"workflows": {
  "workspace-review-finished": { "mode": "apply" },
  "workspace-author-finished": { "mode": "apply" },
  "workspace-temporary":       { "mode": "apply" },
  "workspace-duplicate":       { "mode": "apply" }
}
```

Even in apply mode, a workspace that holds work is never removed: the item is blocked or waits for you, with the reason and a backup command (see [cleanup guards](/concepts/workflows/#cleanup-guards)).

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
"workflows": { "nudge-silent-30d": { "approval": { "default": "run", "window": "5m" } } }
```

`nudge.silent_days` (30) sets the silence before a nudge.

## Narrow one to some pull requests

`only_when` keeps a workflow where its predicate holds, in the [predicate language](/reference/workflow-manifest/#predicates) of its manifest. Automatic address rounds, only in private repositories:

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
{{command}} pause                                         # every apply workflow and board move, everywhere
{{command}} pause acme/api                                # only in acme/api
{{command}} pause https://github.com/acme/api/pull/7      # only on that pull request
{{command}} resume acme/api                               # what pause named
{{command}} resume                                        # everything, pull requests and repositories included
```

Resuming less than a wider pause holds is refused: with everything paused, `resume acme/api` tells you to resume everything instead.

## Write your own

A workflow of yours goes in `<profile>/workflows/<name>/workflow.toml`. This one sends you a private notification about your pull requests that wait on you for more than two days:

```toml
description = "Tell me about my pull requests that wait on me for two days"
subject = "github"
when = 'pr.state == "open" && pr.mine && pr.waits_on_user && pr.age > duration("48h")'

[trigger]
every = "sweep"

[[step]]
use = "notify.self"
text = "This pull request has waited on you for two days"
```

A workflow can also look at your GTD entries, `subject = "entry"`, every entry neither done nor in the trash of an enabled plugin, or at the Slack units a live entry is attached to, `subject = "slack"`. This one tells you about a waiting entry once its stalled nudge is due:

```toml
description = "Tell me when a waiting entry is due a nudge"
subject = "entry"
when = 'entry.nudge_due_at != timestamp("0001-01-01T00:00:00Z") && entry.items_held > 0'
key = ["entry.nudge_due_at"]

[trigger]
every = "sweep"

[[step]]
use = "notify.self"
text = "A waiting entry is due a nudge"
```

`<slot>.settled_at` is the newest draft the workflow itself proposed on the subject that was sent or discarded, so a key of `["entry.settled_at"]` opens a new run after each settlement.

The [workflow manifest](/reference/workflow-manifest/) lists every field and the [facts](/reference/facts/) every condition can read. `{{command}} profile check` loads it; a broken workflow disables only itself.

## Rewrite a bundled one

A directory of your profile named as a bundled workflow overrides it: `<profile>/workflows/review-first-round/workflow.toml` replaces the bundled manifest whole, so you can change its predicate, steps or trigger. Other files you put there replace the bundled files of the same name. The `workflows` key of `config.json` still applies on top. Files whose name starts with `.` are ignored. `profile check` lists the workflow as `bundled, overridden: workflow.toml`; when your manifest does not load or cannot be read, the bundled workflow stays, the problem names your file, and `{{command}} doctor` lists the workflow as running as bundled.
