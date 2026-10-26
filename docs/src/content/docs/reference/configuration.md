---
title: Configuration
description: Every key of config.json, with its default.
---

The configuration is `$XDG_CONFIG_HOME/{{name}}/config.json`, by default `~/.config/{{name}}/config.json`. Every key is optional; a key the loop does not read is an error that names it, and a close key when one exists, and `{{command}} doctor` reports it. A value of the wrong kind, or outside its set, is refused the same way; `null` leaves a key unset. `{{command}} config get <key>` prints the effective value of one, defaults included (`sweep.fast` for a nested key).

The daemon reads the file when it starts. It follows a change of `profile` live; any other change takes effect after a restart, and until then the daemon reports its configuration as stale. Durations are written `"2d"`, `"36h"`, `"90m"` or `"60s"`. `{{command}} profile schema --config` prints the file's JSON schema, for editors.

## Settings

A plugin's settings live under its id (`github`, `slack`), and `<id>.enabled` switches it, read at start. `workflows.<name>` overlays a workflow's manifest field by field, as [Tune workflows](/guides/workflows/) shows; its `approval` and `drafts.approval` take `mode`, `window` and `rule`, a list of `{"when", "mode", "window"}`: a `rule` list replaces the manifest's rules and `[]` clears them, a key left out keeps the manifest's, and a block that does not merge with the manifest's is named by `doctor`. `intents.<name>.project_settings.when` is a predicate over the pull request's [facts](/reference/facts/#intent-facts) where the intent's sessions load the checkout's own agent settings (`.claude/`, `.mcp.json`, `CLAUDE.md`); elsewhere they start with your settings alone, and an intent it does not name loads them; see [Review](/guides/review/#the-checkouts-own-agent-settings). Unless the table says otherwise, `workspace_managers` is `["claude-desktop", "orca"]` and `agent` is `"claude"`.

### Agent and workspaces

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `profile` | string |  | The user profile layered over the bundled one: a name under the profiles directory, or a path; an intent's profile names an intent. |
| `agent` | string |  | The agent the sessions run. |
| `agent_home` | string |  | The agent's own configuration directory; empty is the agent's default. |
| `agent_models` | table by name |  | Per agent, a model name for each model tier. |
| `agent_models.&lt;name&gt;` | table by name |  | One agent's models by tier. |
| `agent_models.&lt;name&gt;.&lt;name&gt;` | string |  | The agent's model name for the tier. |
| `projects_dir` | string |  | Where review clones a repository the workspace manager does not know yet. |
| `workspace_managers` | list of strings |  | The enabled workspace managers, in the order a new worktree picks them. |
| `intents` | table by name |  | Each intent's settings, by intent name. |
| `intents.&lt;name&gt;` | table |  | One intent's settings. |
| `intents.&lt;name&gt;.project_settings` | table |  | Where the intent's sessions load the checkout's own agent settings. |
| `intents.&lt;name&gt;.project_settings.when` | predicate |  | Where the intent's sessions load the checkout's settings; elsewhere they start without them. |

### Projects

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `projects[]` | table |  | Settings for the pull requests of matching repositories; the first match wins. |
| `projects[].match` | string |  | An owner/repo pattern, such as acme/* or acme/api. |
| `projects[].projects_dir` | string |  | The project's projects_dir. |
| `projects[].agent` | string |  | The agent the project's sessions run. |
| `projects[].agent_home` | string |  | The project's agent_home. |
| `projects[].workspace_managers` | list of strings |  | The managers a new worktree of the project picks from, in order. |
| `projects[].github` | table |  | The GitHub settings the project overrides. |
| `projects[].github.trusted_authors` | list of strings |  | The project's github.trusted_authors. |
| `projects[].github.followups_without_workspace` | bool |  | The project's github.followups_without_workspace. |
| `projects[].github.address_without_workspace` | bool |  | The project's github.address_without_workspace. |

### Placement

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `placement[]` | table |  | The rules that place units; the first match wins. |
| `placement[].match` | string |  | The units the rule places. |
| `placement[].repo` | string |  | The repository their work checks out. |
| `placement[].checkout` | string |  | The branch their work checks out. |
| `placement[].role` | one of `review`, `author`, `work` |  | The role of their workspace. |
| `placement[].shared` | bool |  | The units share one workspace. |

### GitHub

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `github` | table |  | The GitHub plugin's settings. |
| `github.enabled` | bool |  | Switches the GitHub plugin; left out, it is on. |
| `github.review_prefix` | string | `REVIEW ` | The prefix of a review workspace's name. |
| `github.automated_reviewers` | list of strings |  | Logins of review bots whose reviews count as automated. |
| `github.owners` | list of strings |  | The GitHub owners automated follow-ups run on; empty is every owner the workspace manager knows. |
| `github.trusted_authors` | list of strings |  | Logins whose pull requests pass the author association check of automated rounds. |
| `github.followups_without_workspace` | bool | `false` | A follow-up review recreates the review workspace the user deleted; off, the sweep only logs it. |
| `github.address_without_workspace` | bool | `false` | An address round creates its author workspace when it has none; off, the sweep only records it. |
| `github.drafts` | table |  | The drafts the plugin's own rules propose. |
| `github.drafts.approval` | table |  | Their approval; left out, each waits for you. |
| `github.drafts.approval.mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The approval mode. |
| `github.drafts.approval.window` | duration |  | How long a delay waits before the draft goes; only with mode delay. |

### GitHub address rounds

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `github.address_reviews` | bool | `true` | Someone's review of a pull request you develop starts an address round. |
| `github.address_skip_reviewers` | list of strings |  | Logins whose reviews never start an address round. |
| `github.address_checks` | bool | `true` | Failing checks on the head of a pull request you develop start an address round. |
| `github.ignore_checks` | list of strings |  | Checks whose failure never starts an address round. |
| `github.address_conflicts` | bool | `true` | A pull request you develop that conflicts with its base past the conflict window starts an address round. |

### GitHub scope and stalls

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `github.scope` | table |  | Whose pull requests the GitHub plugin follows. |
| `github.scope.people` | list of strings |  | Logins whose pull requests it follows. |
| `github.scope.teams` | list of strings |  | Teams whose pull requests it follows. |
| `github.scope.repos` | list of strings |  | Repositories whose pull requests it follows. |
| `github.scope.watch` | list of strings |  | Repositories it watches. |
| `github.stalled` | table |  | When a pull request counts as stalled. |
| `github.stalled.review` | duration | `2d` | Waiting on a review for this long. |
| `github.stalled.reply` | duration | `1d` | Waiting on a reply for this long. |
| `github.stalled.merge` | duration | `1d` | Approved and unmerged for this long. |

### Slack

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `slack` | table |  | The Slack plugin's settings; no secret lives here. |
| `slack.enabled` | bool |  | Switches the Slack plugin; left out, it is on. |
| `slack.client_id` | string |  | The Slack app's client id; empty leaves Slack unconfigured. |
| `slack.redirect_port` | integer |  | The login callback port registered in the Slack app; 0 is 53682. |
| `slack.keychain_service` | string |  | The name of the Keychain items; empty is the brand's. |
| `slack.post_allowlist` | list of strings |  | When not empty, the only channel ids, or user ids naming the DM with that user, a post may go to. |
| `slack.request_log` | bool | `false` | Logs one line per Slack call: method and HTTP status. |
| `slack.turns` | table |  | What a turn on a Slack unit reads. |
| `slack.turns.max_chars` | integer |  | The characters of the unit's messages rendered into the instructions; 0 is 30000. |
| `slack.drafts` | table |  | The drafts the plugin's own rules propose. |
| `slack.drafts.approval` | table |  | Their approval; left out, each waits for you. |
| `slack.drafts.approval.mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The approval mode. |
| `slack.drafts.approval.window` | duration |  | How long a delay waits before the draft goes; only with mode delay. |

### Slack ingest

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `slack.ingest` | table |  | What the Slack plugin admits and how it listens. |
| `slack.ingest.terms` | list of strings |  | A message that holds one of them as a word, in any case, is admitted. |
| `slack.ingest.allow_apps` | list of strings |  | Bot ids (B...) or app ids (A...) whose messages are admitted. |
| `slack.ingest.deny_apps` | list of strings |  | Bot ids (B...) or app ids (A...) whose messages are not. |
| `slack.ingest.socket` | bool |  | Listens on a socket; left out, it does. |
| `slack.ingest.since` | string |  | The first start's backfill date, YYYY-MM-DD; empty is 7 days back. |

### Slack extraction

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `slack.extraction` | table |  | The extraction job; a zero value keeps its default. |
| `slack.extraction.enabled` | bool |  | Switches the extraction job; left out, it is on. |
| `slack.extraction.tier` | one of `fast`, `balanced`, `frontier` |  | Its model tier; left out, fast. |
| `slack.extraction.language` | string |  | The language the extraction writes in; empty is English. |
| `slack.extraction.per_call_usd` | number |  | What one extraction call may cost, in USD; 0 is 0.10. |
| `slack.extraction.per_day_usd` | number |  | What extraction may cost in a day, in USD; 0 is 3. |
| `slack.extraction.per_day` | integer |  | Extraction calls per day; 0 is 500. |
| `slack.extraction.quiet` | duration |  | How long a unit stays unchanged before it runs; empty is 90s. |
| `slack.extraction.max_wait` | duration |  | The longest delay of a unit's oldest change; empty is 10m. |
| `slack.extraction.window_chars` | integer |  | The characters of conversation one call reads; 0 is 40000. |
| `slack.extraction.max_failures` | integer |  | Failed calls in a row after which the job gives up on a unit until its next change; 0 is 3. |
| `slack.extraction.context_messages` | integer |  | The read-only messages shown before a DM's window; 0 is 10, -1 turns them off. |
| `slack.extraction.context_chars` | integer |  | The characters of those messages; 0 is 4000, -1 turns them off. |
| `slack.extraction.escalate_over_chars` | integer |  | A unit whose rendered conversation is longer goes to the frontier tier; 0 or -1 is off. |

### Workflow overrides

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `workflows` | table by name |  | Overrides of each workflow's manifest, by workflow name. |
| `workflows.&lt;name&gt;` | table |  | One workflow's overrides. |
| `workflows.&lt;name&gt;.enabled` | bool |  | Switches the workflow; left out, it is on. |
| `workflows.&lt;name&gt;.observe` | bool |  | Plans the workflow's items pending, for you to run; left out, the manifest's. |
| `workflows.&lt;name&gt;.only_when` | predicate |  | A condition ANDed with the workflow's when. |
| `workflows.&lt;name&gt;.approval` | table |  | The approval of the workflow's runs, over the manifest's field by field. |
| `workflows.&lt;name&gt;.approval.mode` | one of `run`, `delay`, `wait` |  | The mode when no rule matches; left out, the manifest's. |
| `workflows.&lt;name&gt;.approval.window` | duration |  | How long a delay waits before it goes; only with mode delay. |
| `workflows.&lt;name&gt;.approval.rule[]` | table |  | Rules tried in order before the mode, the first match wins; set, they replace the manifest's, and [] clears them. |
| `workflows.&lt;name&gt;.approval.rule[].when` | predicate |  | When the rule applies. |
| `workflows.&lt;name&gt;.approval.rule[].mode` | one of `run`, `delay`, `wait` |  | The rule's mode. |
| `workflows.&lt;name&gt;.approval.rule[].window` | duration |  | The rule's delay; only with mode delay. |

### Workflow draft approval

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `workflows.&lt;name&gt;.drafts` | table |  | The drafts the workflow proposes. |
| `workflows.&lt;name&gt;.drafts.approval` | table |  | The approval of the workflow's drafts, over the manifest's field by field. |
| `workflows.&lt;name&gt;.drafts.approval.mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The mode when no rule matches; left out, the manifest's. |
| `workflows.&lt;name&gt;.drafts.approval.window` | duration |  | How long a delay waits before it goes; only with mode delay. |
| `workflows.&lt;name&gt;.drafts.approval.rule[]` | table |  | Rules tried in order before the mode, the first match wins; set, they replace the manifest's, and [] clears them. |
| `workflows.&lt;name&gt;.drafts.approval.rule[].when` | predicate |  | When the rule applies. |
| `workflows.&lt;name&gt;.drafts.approval.rule[].mode` | one of `send`, `delay`, `wait`, `dry_run` |  | The rule's mode. |
| `workflows.&lt;name&gt;.drafts.approval.rule[].window` | duration |  | The rule's delay; only with mode delay. |

### Turns and headless calls

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `turns` | table |  | The automated turn budgets. |
| `turns.per_day` | integer | `20` | Automated turns per day, all workflows together, on units whose plugin keeps no per-unit count (every provider but GitHub today). |
| `turns.per_unit_per_day` | integer | `5` | Automated turns on one unit per local day, on units whose plugin keeps a per-unit count (GitHub today). |
| `headless` | table |  | The headless turns' caps; a cost of zero leaves it uncapped. |
| `headless.tier` | one of `fast`, `balanced`, `frontier` | `balanced` | The model tier of their calls. |
| `headless.per_call_usd` | number | `0.5` | What one call may cost, in USD. |
| `headless.per_day_usd` | number | `5` | What they may cost in a day, in USD. |
| `brief` | table |  | The brief: one headless call that previews each unit. |
| `brief.enabled` | bool | `false` | Briefs each unit. |
| `brief.tier` | one of `fast`, `balanced`, `frontier` | `fast` | The brief's model tier. |
| `brief.max_input_bytes` | integer | `60000` | The most bytes of input a brief reads, between 1000 and 200000. |
| `classify` | table |  | The classify step. |
| `classify.per_day` | integer | `50` | Classify calls per day. |

### Drafts and notifications

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `drafts` | table |  | The settings every proposer's drafts share. |
| `drafts.repeat_after` | duration | `1d` | The default repeat_after a config.drafts.repeat_after reference reads, and the plugin rules' delay before they propose a settled draft's key again. |
| `nudge` | table |  | The nudges. |
| `nudge.silent_days` | integer | `30` | Calendar days of silence after which a nudge stops. |
| `notifications` | table |  | The notifications. |
| `notifications.digest_at` | string | `09:00` | The local HH:MM of the daily digest; empty for none. |

### Units and GTD

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `items` | table |  | The items. |
| `items.heat` | table |  | How long a unit stays warm. |
| `items.heat.warm_for` | duration |  | How long a unit stays warm after its last item resolved; left out, the heat reader's default. |
| `gtd` | table |  | The GTD entries' settings. |
| `gtd.stale_waiting_days` | integer | `2` | Working days after which a waiting entry goes stale. |
| `gtd.areas` | list of strings |  | The areas an entry is offered. |
| `gtd.contexts` | list of strings |  | The contexts an entry is offered. |

### Cleanup

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `cleanup` | table |  | The thresholds of the session and workspace cleanup workflows. |
| `cleanup.idle` | duration | `2h` | How long a finished session stays idle before it closes. |
| `cleanup.asked_idle` | duration | `4h` | How long a session that asked you stays idle before it closes. |
| `cleanup.workspace_grace` | duration | `1d` | How long, in working time, a workspace stays after its unit finished. |
| `cleanup.manual_idle` | duration | `1d` | How long a session you started stays idle before it closes. |
| `cleanup.stale_workspace` | duration | `14d` | How long an untouched workspace stays. |
| `cleanup.disposable` | list of strings |  | Ignored files and directories a tool recreates, so removing a workspace that holds them loses nothing. |

### Dossiers

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `dossiers` | table |  | The dossiers' lifecycle and quotas; a value of zero or less keeps its default. |
| `dossiers.archive_after` | duration | `30d` | How long after its unit or project resolved a dossier is archived. |
| `dossiers.purge_after` | duration | `400d` | How long after the archive it is purged. |
| `dossiers.soft_cap_mb` | integer | `50` | The size per dossier doctor reports. |
| `dossiers.total_cap_mb` | integer | `2048` | The size of dossiers and archives doctor reports. |
| `dossiers.max_file_mb` | integer | `5` | The largest file a dossier keeps; a larger one is linked. |
| `dossiers.compact_at_kb` | integer | `256` | The log size that triggers compaction. |

### Daemon

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `sweep` | table |  | How often the sweeps run. |
| `sweep.fast` | duration | `2m` | The fast sweep's interval. |
| `sweep.slow` | duration | `1h` | The slow sweep's interval. |
| `sweep.rate_limit_fraction` | number | `0.5` | The share of the GitHub rate limit the sweeps may spend, a number between 0 and 1. |
| `http` | table |  | The daemon's web cockpit. |
| `http.port` | integer | `0` | The cockpit's port; 0 is none. |
| `mcp` | table |  | The daemon's MCP endpoint, which agent homes register. |
| `mcp.port` | integer | `47800` | The MCP endpoint's port. |
| `views` | table by name |  | Each view sink's own settings, by sink. |
| `views.&lt;name&gt;` | table |  | One sink's settings. |
| `views.&lt;name&gt;.columns` | table by name |  | The sink's column for each status: working, working-fallback, needs-me, reviewed, ready, in-progress, in-review, completed. |
| `views.&lt;name&gt;.columns.&lt;name&gt;` | string |  | The column of the status. |

### `projects`

```json
"projects": [
  { "match": "me/*", "agent_home": "~/.claude-personal", "projects_dir": "~/projects/oss" },
  { "match": "acme/api", "github": { "address_without_workspace": true } }
]
```

`match` is `owner/repo` or a glob such as `owner/*`, case-insensitive; the first match wins. An entry may set `projects_dir`, `workspace_managers` (replaces the top-level list), `agent`, `agent_home`, and under `github` the project's `trusted_authors` (replaces the top-level list), `followups_without_workspace` and `address_without_workspace`. Empty fields keep the top-level value. What your profile says for those repositories goes in its `organization=` and `repo=` blocks.

### `workspace_managers`

```json
"workspace_managers": ["claude-desktop", "orca"],
"projects": [{ "match": "acme/*", "workspace_managers": ["orca"] }]
```

{{name}} serves every listed manager at once: it reads their worktrees and sessions and acts on each worktree through its owner. The order matters only for a new worktree, which goes to the first manager that is

- enabled: listed here, or in the matching `projects` entry;
- available: installed and reachable;
- able to host the project's `agent`;
- able to honor the session's launch policy.

Orca honors every launch policy. Claude Desktop honors a session that loads the checkout's project settings, uses the default agent home, and adds allows and denies, which {{name}} writes into the worktree's `.claude/settings.local.json`. A session that needs your user settings alone (where `intents.<name>.project_settings.when` does not hold for its intent, and always for a pull request that fails the trust check, whose checkout settings are its author's), a strict or MCP-free session, or a non-default `agent_home` falls through to the next manager. When no manager can launch it, the round fails closed and names each manager's reason. `{{command}} doctor` reports whether each manager is available and reachable (one unreachable manager is a note, all of them unreachable is a failure) and a `launch <intent>` check for each intent no enabled manager can launch.

An empty list is an error. The old `workspace_manager` key is refused; list the manager in `workspace_managers` instead.

## Board

Only Orca has a board. A workspace Claude Desktop owns has no card; its state shows in the cockpit, the terminal client and the menu bar.

`views.orca.columns` maps a status to a column: `working` (while a review round runs, `reviewing` by default), `working-fallback` (when the board has no `working` column, `in-review`), `needs-me` (an agent asked or stopped), `reviewed` (a review published without approving), `ready` (your approved, green pull request, `ready-to-merge`), and the author statuses `in-progress`, `in-review` and `completed`. See [board columns](/concepts/board/).

### `placement`

```json
"placement": [
  { "match": "slack:T000/C000/*", "repo": "acme/api", "checkout": "main", "role": "work" }
]
```

The first rule whose `match`, a glob over `<provider>:<key>`, matches gives the repository, the checkout (`""` for a new worktree, `main`, or a path), the role and whether the workspace is `shared` by several units. A unit no rule, hint or provider default places, such as a Slack DM, runs its sessions in `<state>/neutral`, an empty repository with no project checked out.

## Example

```json
{
  "projects_dir": "~/projects",
  "profile": "mine",
  "github": {
    "scope": { "teams": ["acme/backend"], "repos": ["acme/api"] },
    "trusted_authors": ["github-actions[bot]"]
  },
  "brief": { "enabled": true },
  "workflows": {
    "workspace-review-finished": { "observe": false },
    "workspace-author-finished": { "observe": false }
  },
  "projects": [
    { "match": "me/*", "agent_home": "~/.claude-personal" }
  ]
}
```
