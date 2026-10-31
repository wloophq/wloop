---
title: Your first review
description: Start a review round on someone else's pull request and follow it to the end.
---

Pick an open pull request that someone else opened, in a repository you can clone.

## 1. Start the round

```sh
{{command}} review https://github.com/acme/api/pull/7 look at the migration first
```

Everything after the URL is guidance: free text that goes, verbatim, into the round's instructions. The command prints each step until the agent starts the turn:

1. It finds the repository's checkout among the ones your workspace managers know, by remote, and clones it into `projects_dir` only when there is none.
2. It fetches the pull request's head and creates the review workspace `REVIEW <head-branch>` at it, in the first manager of `workspace_managers` that can launch the round. On the Orca board the card shows `reviewing`.
3. It starts Claude Code in that workspace with a prompt that names the turn. The agent runs `{{command}} instructions review --turn <id> <url>` to read its instructions: the review procedure plus your review profile.

Ctrl-C stops following the request, not the request itself. `--no-wait` returns at once with the request's number; `{{command}} intake <n>` follows it again.

## 2. Watch it

```sh
{{command}} open          # the cockpit, in your browser
{{command}} status        # the same views, in the terminal
{{command}} tui           # the terminal client, live
```

The pull request shows in the **In loop** view while the agent works.

## 3. The round ends

The agent ends every round with a record, through the `turn_end` tool of {{name}}'s MCP server:

- **done**: it published the review. The card moves to `reviewed`, or to `completed` when it approved.
- **asked**: it held its draft and asks you something, for example whether a finding blocks or whether to approve a change to a risky surface. The card moves to `needs-me` and the question shows in the cockpit's **Needs me** view, the terminal client and, on macOS, a notification. The question holds the next rounds until you answer it, but only on the head it asked about: once the head moves and a follow-up is due, that follow-up reviews the new head and the question closes as superseded, unless your answer is already on its way.
- **failed**: something broke and nothing was published. It shows in **Needs me** with what broke.

## 4. Answer a question

In the cockpit, open the pull request's detail. The question's card shows the drafted verdict, the agent's recommendation, the draft itself and one button per option. Choose one, or type your own answer. {{name}} types the answer into the agent's session, or starts a new session that resumes the turn when the old one is gone. See [Turns](/concepts/turns/#answers).

## 5. The next round comes on its own

When the author pushes new commits, replies to your review or re-requests you, the daemon plans a follow-up round. It waits in the [approval window](/concepts/approval/) (60 seconds by default) and then goes to the same workspace. You do nothing unless the agent asks.

## Next

- Tell the reviewer what you care about: [Write your review profile](/guides/review-profile/).
- See a review without publishing it: `{{command}} profile try <url>`.
- Let the loop answer reviews on your own pull requests: [Address reviews](/guides/address/).
