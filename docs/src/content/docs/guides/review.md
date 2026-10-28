---
title: Review someone else's pull request
description: Start a review round, steer it, answer its questions and let the follow-ups run.
---

## Start a round

From a terminal:

```sh
{{command}} review https://github.com/acme/api/pull/4610
{{command}} review https://github.com/acme/api/pull/4610 compare the retry logic with the one in billing
```

From the cockpit: Review (`V`) on the pull request's row, with guidance in the box. From Explore, Review on a result adds the pull request to the loop first.

From an agent session you already have open, with the bundled skill:

```text
/{{name}}-review https://github.com/acme/api/pull/4610 focus on the migration
```

Or the CLI form, which prints the instructions into the session you are in:

```sh
{{command}} instructions review https://github.com/acme/api/pull/4610 -- focus on the migration
```

The round runs in the review workspace `REVIEW <head-branch>`, created at the pull request's head when it does not exist. An existing one is reset to the new head, refusing when tracked files are modified or an agent is working there. A second request for the same pull request while the first waits joins it and replaces its guidance.

## Steer it

Guidance goes verbatim, newlines kept, into the round's stored instructions. Use it for what this round should look at: a concern, a comparison, a question for the reviewer. What you want in every review belongs in your [profile](/guides/review-profile/) instead.

## What the round publishes

The bundled review guide asks for inline comments on the lines they concern, labeled with Conventional Comments (`issue`, `suggestion`, `nitpick`, `question`, `note`, decorated `(blocking)` or `(non-blocking)`), suggestion blocks for direct replacements, and a body only for what has no line. A clean approval is the event alone, with no body. The round approves when nothing blocks, except where the [ambiguity gate](/concepts/approval/#the-ambiguity-gate) stops it.

To see what a review would publish without publishing it:

```sh
{{command}} profile try https://github.com/acme/api/pull/4610
```

It runs a round in a throwaway worktree with every GitHub write refused, and prints the review. It takes a few minutes.

## When it asks

The card moves to `needs-me`, the pull request shows in Needs me, and on macOS a notification offers the answers. Answer in the cockpit's detail, the notification, or the terminal client (`W`). Continue (`C`) sends the agent back without an answer; Dismiss (`X`) closes the question and the pull request goes back to whoever it waits on.

## Follow-ups

After your review, the daemon starts the next round on its own when the author pushes new commits, replies, or re-requests you. Each one waits in the [approval window](/concepts/approval/) first. After an approval, only new commits or a re-request start one. A round with nothing new publishes nothing.

To stop the automatic rounds on one pull request:

```sh
{{command}} pause https://github.com/acme/api/pull/4610      # until resume
{{command}} resume https://github.com/acme/api/pull/4610
```

## First rounds on their own

A pull request that requests your review by name, from a trusted author in an allowed owner, gets a first round without your asking. Limit it with `plugins.github.owners` (the GitHub owners automated rounds run for) and widen it with `plugins.github.trusted_authors` (logins such as `github-actions[bot]` whose pull requests count as trusted). `{{command}} plan --unit <url>` says why a requested review got no first round. Turn first rounds off with:

```toml
[workflows.review-first-round]
enabled = false
```

## The checkout's own agent settings

A review session runs in the pull request's head, and by default loads that checkout's agent settings like any session there: `.claude/settings.json`, `.claude/settings.local.json`, the servers of `.mcp.json` and `CLAUDE.md`. Someone who can push to the pull request can change them. A pull request that fails the trust check (a fork, or an author outside the members, collaborators, owners and `plugins.github.trusted_authors`) never loads them, in a round you start by hand too, so its session runs in a manager that can start Claude Code with your user settings alone, such as Orca. `intents.<intent>.project_settings.when` turns them off where a predicate over the pull request's [facts](/reference/facts/#intent-facts) does not hold:

```toml
[intents.review.project_settings]
when = 'pr.visibility == "private"'
```

Where it does not hold, the session starts with `--setting-sources user`: your own settings, hooks and MCP servers, {{name}}'s included, and none of the checkout's. The procedure then tells the agent to read the checkout's `CLAUDE.md` and `AGENTS.md` as text. When such a round stops on Claude Code's trust question, trusting from the cockpit trusts that worktree alone, not the repository. `when = "false"` turns them off everywhere; `pr.owner`, `pr.repo_name`, `pr.author`, `pr.association` and `pr.labels` narrow it like `pr.visibility`. An intent with no `project_settings` keeps loading them.

Claude Desktop cannot start a session with `--setting-sources user`, so a session with the checkout's settings off goes to the next manager in `workspace_managers` that can launch it, Orca by default. With no such manager the round fails and says why (see [Configuration](/reference/configuration/)).

## When it is done

Once the pull request merges or closes, the card moves to `completed` and the cockpit's Done view lists the workspace. Remove it there, or with `{{command}} remove <url>`. The `workspace-review-finished` workflow can do it on its own once you switch it on (see [Tune workflows](/guides/workflows/#let-cleanup-remove-workspaces)).
