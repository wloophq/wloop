---
title: The daemon
description: The background process that reads GitHub, keeps the store and serves every client.
---

`{{command}} serve` is the resident process behind the loop. It runs headless, normally under its LaunchAgent on macOS or your init system on Linux, and only one runs at a time. It is the only process that opens {{name}}'s SQLite store. It serves:

- the API every client uses, over a Unix socket (`<state>/daemon.sock`);
- the cockpit, on `127.0.0.1` (`http.port`, a free port by default), behind a token that `{{command}} open` hands over;
- {{name}}'s MCP server on `127.0.0.1:47800` (`mcp.port`), which agent sessions call to record their turns. The daemon does not start when that port is taken, since every session sends its key there.

Every command that reads or writes {{name}}'s state goes through the daemon. With no daemon, a command fails within a second and says how to start it. `doctor` still runs the checks that need no state, and `install`, `config get` and `app install` need no daemon at all. A client and a daemon of different API major versions refuse each other and name the one to restart.

## Jobs

| Job | When | Does |
|---|---|---|
| fast | every `sweep.fast` (2 minutes) | reads the pull requests whose `updatedAt` moved or whose head checks changed, the review requests and the workspace managers' worktrees; every 10 minutes it also lists your own open pull requests, so a base that moves and creates a conflict shows |
| sweep | right after each fast job | evaluates the [workflows](/concepts/workflows/): first rounds, follow-ups, address rounds, cleanup and board columns |
| slow | every `sweep.slow` (1 hour) | team members, the repositories of your area, every open pull request in scope and those closed or merged in the last 14 days |
| brief | after each fast job | writes the [brief](/concepts/brief/) of up to 5 pull requests, when briefs are on |

When the GitHub rate limit falls below `github.rate_limit_fraction` (half) of the hour's budget, the fast job waits for the reset and the cockpit shows "slow sync". A failing job is logged to `serve.err.log`, shown in the cockpit and reported by `doctor`.

Sweep now in the menu bar asks the daemon for one sweep now; `{{command}} plan` prints what a sweep would do.

## Scope

The daemon's **inventory** is the set of pull requests it follows:

- your own pull requests;
- those that request your review;
- those of your area: the pull requests of `github.scope.people` and of the members of `github.scope.teams`, plus every pull request of a repository where one of them opened one in the last 90 days, or listed in `github.scope.repos`;
- those of the logins in `github.scope.watch`, which enter as if they requested your review;
- those that one of your workspaces stands for;
- those you add with `{{command}} track <url>`.

```toml
[github.scope]
people = ["ana", "bruno"]
teams = ["acme/backend"]
repos = ["acme/api"]
watch = ["carla"]
```

The loop acts only where a local workspace exists or a rule creates one; a pull request without one stays in the inventory, visible in the cockpit.

## Who a pull request waits on

From the history of each pull request the daemon derives whom it waits on (the author, the requested reviewers, you, the loop or nobody), since when and why. Past `github.stalled.review` (2 working days), `github.stalled.reply` (1) or `github.stalled.merge` (1) it reads as **stalled**. An approved pull request with merge conflicts waits on its author.

## Tracking by hand

`{{command}} track <url>` adds a pull request with the reason `tracked`; from then on it is followed like any other in scope. Up to 50 are tracked at a time. A track is refused while the GitHub budget is low, and for a pull request already merged or closed; one that merges or closes later loses its track by itself. `{{command}} untrack <url>` drops the reason.

## Explore

`{{command}} explore <query>`, and the cockpit's Explore view, look for other people's open pull requests to help on. The query is a pull request URL, `owner/repo`, `@login`, `@org/team` or any GitHub search. A bare word is a repository you follow when one has that name, else a login, else text searched in the owners of the repositories you follow. It lists 30, the one that has waited longest first, with their size, review state, checks and your last review. From a result you can Review, Nudge, Take over or Track it.

## Timings

`{{command}} timings` reports what the daemon recorded over the last 30 days: the median and 90th percentile of the wait from a review request to your first review, from opening to approval and to merge, turn durations by intent, brief calls and cost, tokens by model, and how long followed pull requests waited on you, on others and on the loop.
