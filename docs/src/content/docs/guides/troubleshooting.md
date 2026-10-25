---
title: Troubleshooting
description: Find out why something did not happen, with doctor, the plan and the logs.
---

## Start with doctor

```sh
{{command}} doctor
```

`doctor` changes nothing. It checks `gh`, each workspace manager (available and reachable; one unreachable manager is an `[info]`, all of them a `[missing]`), a `launch <intent>` check for each intent no enabled manager can launch, the configuration, every agent home (the MCP registration, the hooks, the permission, the skills, and Orca's competing status hook), the review profile, the LaunchAgents, the store, the daemon and whether it runs the same version as the CLI, each of its jobs, the start-up reconcile, whether workflows are paused, each workflow, the `PATH` the login shell gives the daemon, and git access without prompts. Each problem comes with its fix on its own line. Piped, it prints one `[ok]`, `[info]` or `[missing]` line per check.

| Variant | Shows |
|---|---|
| `doctor --git` | runs an `ls-remote` now, as the daemon would, to check git access |
| `doctor --load` | what the daemon spent since it started: calls, spawns and rate limit points |
| `profile show <owner/repo>` | which profile blocks a round there reads |
| `probe --workspace <id-or-path>` | sends a self-test turn into a new session of the workspace, to prove an agent home end to end |

## Why did nothing happen to this pull request?

```sh
{{command}} plan --unit https://github.com/acme/api/pull/7
```

It lists the unit's plan items, then every workflow that gives it none, with the facts its condition read and the reason beside them, such as `pr.review.reason = nothing new since my last review`. Common reasons:

- the pull request is from a fork, a draft, or by an author who is not a member, collaborator or owner (see `trusted_authors`);
- its owner is not in `owners`;
- the agent of its workspace is working, or you have the workspace focused in its manager;
- no enabled manager can launch the round's session: `doctor` names the intent in a `launch <intent>` check, and the round's failure lists each manager's reason (Claude Desktop, say, cannot start a session without the checkout's project settings, so such a session needs Orca in `workspace_managers`);
- the per-pull-request daily cap (`rounds.per_pr_per_day`) held the round (`held (cap)` in the cockpit; `{{command}} review <url>` or `{{command}} address <url>` runs it by hand, outside the cap);
- the pull request, its repository or everything is paused, or the pull request is muted;
- the workspace was deleted by hand (see "Without a workspace" in [The sweep and workflows](/concepts/workflows/)).

## The daemon is not running

Every command that needs it fails within a second and names the fix:

```sh
launchctl kickstart gui/$(id -u)/io.github.{{org}}.{{name}}   # macOS, after app install
{{command}} serve                                               # or in a terminal
```

The menu bar shows `PR ?` until the daemon answers. When the store is the reason, `doctor` says so under the daemon error. If the LaunchAgent is missing or runs another command, `{{command}} app install` rewrites it.

## The store needs attention

The menu bar shows `PR !` and the daemon serves only its health when it cannot open the store. `doctor`, the cockpit and the menu bar say why and name the command:

| State | Why | Fix |
|---|---|---|
| migration failed | a migration failed; the store stays at the version it reached and a backup holds the one before | `{{command}} store migrate` retries with each step shown; a store that fails SQLite's integrity check names `{{command}} store restore` |
| needs a migration | a build that is not a release found an older store | `{{command}} store migrate` |
| written by a newer release | the store is at a schema version this release does not read | `brew upgrade {{name}}`, or `{{command}} store restore` |
| a store command is running | `store migrate` or `store restore` holds the store | wait for it to finish |

The daemon checks again every five seconds, so it starts once the store fits; no restart is needed. See [Upgrade](/guides/upgrade/) for the backups and the way back.

## A round is blocked on an untrusted folder

Claude Code asks to trust a folder before it works there. {{name}} never trusts a folder for you: a round that finds one untrusted is shown as blocked, with the command, and goes ahead on the next sweep once you run it:

```sh
{{command}} trust ~/projects/api
```

It trusts the main repository root, which covers every worktree of it; `--exact` trusts the folder alone.

## A turn ended without a record

The card shows `needs-me` with `stopped: ...`. Common causes: the session ran outside the enabled workspace managers (a record needs the process handle that `ORCA_TERMINAL_HANDLE` carries in an Orca terminal, or `CLAUDE_CODE_HOST_SESSION_ID` in a Claude Desktop session), the home lacks the MCP registration, its `headersHelper` or the `X-Loop-Key` header (`{{command}} install` fixes it; `{{command}} doctor` names which), or the daemon was down when the agent recorded. Continue sends a resume turn.

To prove a home end to end:

```sh
{{command}} probe --workspace <workspace-id-or-path> [--home <dir>]
```

It sends a self-test turn into a new session of that workspace: the Stop hook's block, two questions for you to answer through `turn_end`, and a resume in a new session. Its pull request is synthetic, so nothing reaches GitHub.

## Claude Desktop comes to the front

{{name}} imports each new Claude Desktop session with `open -g`, which should leave the app in the background, but the import has been seen to bring Claude Desktop to the front. A round that starts or wakes a Desktop session may do that. List `orca` first in `workspace_managers` to send new worktrees to Orca instead.

## Notifications do not show

Allow them in System Settings > Notifications > {{app}}. If the menu bar is not running, `{{command}} app install` reinstalls its LaunchAgent. Rounds still run at their deadline without notifications; the cockpit shows a warning.

## Logs

The state directory is `~/.local/state/{{name}}/` (or `$XDG_STATE_HOME/{{name}}/`):

| File | Holds |
|---|---|
| `serve.out.log`, `serve.err.log` | the daemon's output under its LaunchAgent; a `held:` line means a cap stopped a round |
| `app.out.log`, `app.err.log` | the menu bar's output |

On Linux the daemon's output goes wherever your init system sends it (`journalctl --user -u {{name}}` for the systemd example).

## Inspect a pull request's turns

```sh
{{command}} turn show https://github.com/acme/api/pull/7
{{command}} config get rounds.per_pr_per_day
```

`turn show` prints every turn with its records and answers. `config get` prints the effective value of a key, defaults included.
