---
title: Overview
description: What the tool does, the problem it solves, and how the pieces fit.
---

{{name}} is a work loop for the requests that reach you from GitHub pull requests and Slack. AI coding agents do the review and address rounds in local worktrees, and {{name}} keeps track of every round, every question an agent asks you and every pull request that waits on someone.

## The problem

Handing a review to an agent is one command. Everything after it is manual: noticing that the author answered, finding the worktree again, moving it to the new head, asking the agent for the next round, and keeping track of which of your own pull requests wait on you. With a dozen reviews and a few pull requests of your own in flight, that bookkeeping takes most of your attention.

## What {{name}} does

- **Reviews someone else's pull request.** `{{command}} review <url>` opens a dedicated worktree at the pull request's head and starts an agent on a review round, judged by your review profile. When the author pushes, replies or asks you again, the next round goes to the same worktree on its own.
- **Answers reviews on your pull requests.** When someone reviews a pull request you develop, or its checks fail, an agent in your workspace verifies each comment, applies what holds, replies in every thread and asks the reviewers for another look. It stops and asks you when something is ambiguous or risky.
- **Keeps one list of what you owe.** Review requests, changes requested on your pull requests, an agent's question and a Slack thread that asks something of you become entries in an Inbox, with Next, Waiting, Someday and a weekly Review.
- **Announces rounds before they start.** Each automatic round waits in an approval window where you can skip it, pause the pull request or add guidance. Inside a round, the agent is instructed to stop and ask when something is ambiguous or risky; otherwise it publishes its reviews and replies on GitHub itself. Drafts that an agent or a rule proposes wait in the Outbox until you approve them, by default. The [threat model](/concepts/security/) says what this does not cover.
- **Shows who each pull request waits on.** The daemon follows the pull requests of your area and shows, in a local web cockpit, a terminal client and the macOS menu bar, whom each one waits on, since when, and which ones are stalled.

## How the pieces fit

- **The daemon** (`{{command}} serve`) runs in the background. It reads GitHub, decides what to do, keeps every round in its store and serves every client.
- **A workspace manager**, Claude Desktop or Orca (or both at once), hosts the worktrees and the agent sessions. {{name}} creates workspaces and starts, wakes and types into sessions through it; Orca also shows a board.
- **Claude Code** runs each round in a session of that manager and reports the end of the round back to {{name}} through an MCP tool.
- **You** answer from the cockpit, the terminal client, a notification or the CLI.

```text
$ {{command}} plan
pending (1)
  3f9a1c2e  review-follow-up  acme/api#4610: runs when its window ends
            runs in 41s · steps turn.start
run (1)
  b07d44a9  session-pr-finished  session:7f2c
            steps session.close · frees 410.0 MiB of memory
skipped (1)
  91c0e5d2  address  acme/api#4582: paused
```

## Next steps

1. Check the [requirements](/getting-started/requirements/).
2. [Install](/getting-started/install/) the binary.
3. Run [`{{command}} init`](/getting-started/init/).
4. Start your [first review](/getting-started/first-review/).
