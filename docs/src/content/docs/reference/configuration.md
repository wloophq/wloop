---
title: Configuration
description: Every key of config.json, with its default.
---

The configuration is `$XDG_CONFIG_HOME/{{name}}/config.json`, by default `~/.config/{{name}}/config.json`. Every key is optional; a key the loop does not read is an error that names it, and `{{command}} doctor` reports it. `{{command}} config get <key>` prints the effective value of one, defaults included (`sweep.fast` for a nested key).

The daemon reads the file when it starts. It follows a change of `profile` live; any other change takes effect after a restart, and until then the daemon reports its configuration as stale. Durations are written `"2d"`, `"36h"`, `"90m"` or `"60s"`.

## Workspaces and agents

| Key | Default | Means |
|---|---|---|
| `projects_dir` | none | where `review` clones a repository no workspace manager knows yet; needed only then |
| `workspace_managers` | `["claude-desktop", "orca"]` | the enabled workspace managers, in the order a new worktree picks from; below |
| `agent` | `"claude"` | the coding agent that runs the rounds; `claude` (Claude Code) is the one supported |
| `agent_home` | `""` | the agent's configuration directory (`CLAUDE_CONFIG_DIR` for Claude Code), for a home other than `~/.claude`; when unset, `review` and `take-over` use the home of the session that runs them |
| `agent_models` | `{}` | pins a model tier (`fast`, `balanced`, `frontier`) to a model name per agent: `{"claude": {"fast": "claude-haiku-5-5"}}`; without a pin, Claude Code runs `haiku`, `sonnet` and `opus` |
| `profile` | `""` | your review profile: a name under `~/.config/{{name}}/profiles/`, or a path |
| `review_prefix` | `"REVIEW "` | the display-name prefix that marks a review workspace |
| `project_settings` | `{}` | per intent, a predicate over the pull request's [facts](/reference/facts/#intent-facts) where its sessions load the checkout's own agent settings (`.claude/`, `.mcp.json`, `CLAUDE.md`); elsewhere they start with your settings alone. An intent it does not name loads them; see [Review](/guides/review/#the-checkouts-own-agent-settings) |
| `projects` | `[]` | per-repository overrides, below |

### `projects`

```json
"projects": [
  { "match": "me/*", "agent_home": "~/.claude-personal", "projects_dir": "~/projects/oss" },
  { "match": "acme/api", "address_without_workspace": true }
]
```

`match` is `owner/repo` or a glob such as `owner/*`, case-insensitive; the first match wins. An entry may set `projects_dir`, `workspace_managers` (replaces the top-level list), `agent`, `agent_home`, `trusted_authors` (replaces the top-level list), `followups_without_workspace` and `address_without_workspace`. Empty fields keep the top-level value. What your profile says for those repositories goes in its `organization=` and `repo=` blocks.

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

Orca honors every launch policy. Claude Desktop honors a session that loads the checkout's project settings, uses the default agent home, and adds allows and denies, which {{name}} writes into the worktree's `.claude/settings.local.json`. A session that needs your user settings alone (where `project_settings` is off for its intent, and always for a pull request that fails the trust check, whose checkout settings are its author's), a strict or MCP-free session, or a non-default `agent_home` falls through to the next manager. When no manager can launch it, the round fails closed and names each manager's reason. `{{command}} doctor` reports whether each manager is available and reachable (one unreachable manager is a note, all of them unreachable is a failure) and a `launch <intent>` check for each intent no enabled manager can launch.

An empty list is an error. The old `workspace_manager` key is refused; list the manager in `workspace_managers` instead.

## Automatic rounds

| Key | Default | Means |
|---|---|---|
| `owners` | `[]` | GitHub owners automated rounds run for; empty means the owners of every repository a workspace manager knows |
| `trusted_authors` | `[]` | logins whose pull requests get automated rounds although they are not a member, collaborator or owner, such as `github-actions[bot]`; a fork is still refused |
| `automated_reviewers` | `[]` | logins the review procedure treats like bots: contradicting them never stops a round |
| `rounds.per_pr_per_day` | `5` | automatic rounds of every workflow per pull request per local day; a workflow's own caps are its manifest's [`[limits]`](/reference/workflow-manifest/) |
| `address_reviews` | `true` | address rounds for reviews on your pull requests |
| `address_skip_reviewers` | `[]` | logins whose reviews never start one |
| `address_checks` | `true` | address rounds for failing checks |
| `address_conflicts` | `true` | address rounds for a conflict with the base |
| `ignore_checks` | `[]` | check names that never start one |
| `followups_without_workspace` | `false` | a follow-up may recreate a review workspace you deleted |
| `address_without_workspace` | `false` | an address round may create the author workspace of your pull request |
| `nudge.silent_days` | `30` | calendar days of silence before `nudge-silent-30d` plans a nudge |
| `classify.daily_cap` | `50` | headless classifier calls a workflow's `classify` step may spend per day |
| `workflows.<name>` | `{}` | `enabled`, `mode` (`observe` or `apply`), `approval.default` (`run` or `wait`), `approval.window` and `only_when` (a predicate that must hold besides the manifest's `when`) of a workflow, over its manifest's; see [Tune workflows](/guides/workflows/) |

## Scope and the daemon

| Key | Default | Means |
|---|---|---|
| `scope.people` | `[]` | logins whose pull requests, and whose repositories' pull requests, the daemon follows |
| `scope.teams` | `[]` | teams, as `org/team`, whose members count as `people` |
| `scope.repos` | `[]` | repositories, as `owner/repo`, whose pull requests the daemon follows |
| `scope.watch` | `[]` | logins whose pull requests enter as if they requested your review |
| `stalled.review` | `"2d"` | working days a pull request may wait on reviewers before it reads as stalled |
| `stalled.reply` | `"1d"` | the same, waiting on an answer |
| `stalled.merge` | `"1d"` | the same, approved and green but not merged |
| `sweep.fast` | `"2m"` | the fast job's interval |
| `sweep.slow` | `"1h"` | the slow job's interval |
| `sweep.rate_limit_fraction` | `0.5` | share of the GitHub hourly budget below which the fast job waits |
| `http.port` | `0` | the cockpit's port on `127.0.0.1`; 0 picks a free one |
| `mcp.port` | `47800` | the MCP server's port on `127.0.0.1`; after changing it, restart the daemon and run `install` again |
| `brief.enabled` | `false` | write the [brief](/concepts/brief/) of each pull request |
| `brief.tier` | `"fast"` | the model tier of brief calls |
| `brief.max_diff_bytes` | `60000` | the diff a brief reads, between 1000 and 200000 |

## Cleanup

| Key | Default | Means |
|---|---|---|
| `cleanup.idle` | `"2h"` | a loop session whose turn ended closes after this quiet time |
| `cleanup.asked_idle` | `"4h"` | a loop session whose turn asked closes after this quiet time |
| `cleanup.manual_idle` | `"1d"` | a session you started is offered for closing after this quiet time |
| `cleanup.workspace_grace` | `"1d"` | working time after a pull request ended before its workspace may go |
| `cleanup.stale_workspace` | `"14d"` | a workspace with no pull request is offered for removal after this |
| `cleanup.disposable` | `node_modules`, `.venv`, `venv`, `__pycache__`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `dist`, `build`, `target`, `bin`, `.terraform`, `.next`, `coverage`, `.gradle`, `_build`, `.docusaurus`, `.DS_Store`, `.coverage`, `.loop` | ignored names a workspace may lose; setting it replaces the whole list |

## Board

Only Orca has a board. A workspace Claude Desktop owns has no card; its state shows in the cockpit, the terminal client and the menu bar.

| Key | Default | Means |
|---|---|---|
| `views.orca.columns.working` | `"reviewing"` | while a review round runs |
| `views.orca.columns.working-fallback` | `"in-review"` | when the board has no `working` column |
| `views.orca.columns.needs-me` | `"needs-me"` | an agent asked or stopped |
| `views.orca.columns.reviewed` | `"reviewed"` | a review published without approving |
| `views.orca.columns.ready` | `"ready-to-merge"` | your approved, green pull request |

The author columns `in-progress`, `in-review` and `completed` map the same way. See [board columns](/concepts/board/).

## GTD, drafts and headless turns

| Key | Default | Means |
|---|---|---|
| `plugins.<id>.enabled` | `true` | turns the `github` or `slack` plugin off; read at start |
| `gtd.stale_waiting_days` | `2` | working days before a waiting entry with no nudge gets the focus star |
| `gtd.areas`, `gtd.contexts` | `[]` | the areas and contexts offered |
| `notifications.digest_at` | `"09:00"` | local time of the daily digest; `""` for none |
| `proposals.renudge_after` | `"1d"` | a proposal rule does not propose the same draft again sooner |
| `proposals.<rule>.approval` | `{"mode": "always"}` | the draft approval of `stalled-nudge`, `closure`, `ask-owner` or `github-nudge`: `mode` `always`, `window` (with `window`), `none` or `observe` |
| `headless.tier` | `"balanced"` | the model tier of headless turns |
| `headless.per_call_usd` | `0.5` | the cap of one headless call; 0 is uncapped |
| `headless.per_day_usd` | `5` | the cap of a day of headless calls; 0 is uncapped |
| `dossiers.archive_after` | `"30d"` | a resolved dossier is archived after this |
| `dossiers.purge_after` | `"400d"` | an archive is purged after this |
| `dossiers.soft_cap_mb` | `50` | the size per dossier `doctor` reports against |
| `dossiers.total_cap_mb` | `2048` | the size of all dossiers `doctor` reports against |
| `dossiers.max_file_mb` | `5` | a larger file in a dossier is refused |
| `items.heat.warm_for` | | how long a unit stays warm after its last item resolved |
| `placement` | `[]` | the rules that place a unit's work, below |

### `placement`

```json
"placement": [
  { "match": "slack:T000/C000/*", "repo": "acme/api", "checkout": "main", "role": "work" }
]
```

The first rule whose `match`, a glob over `<provider>:<key>`, matches gives the repository, the checkout (`""` for a new worktree, `main`, or a path), the role and whether the workspace is `shared` by several units. A unit no rule, hint or provider default places, such as a Slack DM, runs its sessions in `<state>/neutral`, an empty repository with no project checked out.

## Slack

| Key | Default | Means |
|---|---|---|
| `slack.client_id` | `""` | your Slack app's client id; empty leaves Slack off |
| `slack.redirect_port` | `53682` | the login callback's port on `127.0.0.1` |
| `slack.keychain_service` | `"{{name}}-slack"` | the Keychain service of the tokens |
| `slack.post_allowlist` | `[]` | when set, the only channel or user ids a post may reach |
| `slack.request_log` | `false` | log each Slack call's method and status |
| `slack.ingest.terms` | `[]` | words that admit a message |
| `slack.ingest.allow_apps`, `slack.ingest.deny_apps` | `[]` | bot (`B000`) or app (`A000`) ids to admit or drop |
| `slack.ingest.socket` | `true` | Socket Mode |
| `slack.ingest.since` | 7 days back | the first start's backfill date |
| `slack.extraction.enabled` | `true` | the extraction job |
| `slack.extraction.tier` | `"fast"` | the model tier of extraction calls |
| `slack.extraction.language` | `"English"` | the language items are written in |
| `slack.extraction.max_call_usd` | `0.10` | the cap of one call |
| `slack.extraction.max_day_usd` | `3.0` | the cap of a day |
| `slack.extraction.max_day_calls` | `500` | calls per day |
| `slack.extraction.quiet` | `"90s"` | how long a unit stays quiet before extraction |
| `slack.extraction.max_wait` | `"10m"` | the longest wait after a unit's oldest change |
| `slack.extraction.window_chars` | `40000` | the conversation text one call reads |
| `slack.extraction.max_failures` | `3` | failures in a row before a unit waits for its next change |
| `slack.extraction.context_messages` | `10` | earlier DM messages shown as read-only context |
| `slack.extraction.context_chars` | `4000` | the text budget of that context |
| `slack.extraction.escalate_over_chars` | `0` | a conversation longer than this goes to the frontier tier; 0 is off |
| `slack.turns.max_chars` | `30000` | the Slack text a turn's instructions carry |

## Example

```json
{
  "projects_dir": "~/projects",
  "profile": "mine",
  "scope": { "teams": ["acme/backend"], "repos": ["acme/api"] },
  "trusted_authors": ["github-actions[bot]"],
  "brief": { "enabled": true },
  "workflows": {
    "workspace-review-finished": { "mode": "apply" },
    "workspace-author-finished": { "mode": "apply" }
  },
  "projects": [
    { "match": "me/*", "agent_home": "~/.claude-personal" }
  ]
}
```
