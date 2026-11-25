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

The bundled review guide asks for inline comments on the lines they concern, labeled with Conventional Comments (`issue`, `suggestion`, `nitpick`, `question`, `note`, decorated `(blocking)` or `(non-blocking)`), suggestion blocks for direct replacements, and a body only for what has no line. A clean approval is the event alone, with no body.

The round never posts on its own: it writes the review as a draft, and the daemon posts it once the draft's gate approves it. With the bundled rules, a review whose event is COMMENT or REQUEST_CHANGES, a reply in a review thread and the resolving of a thread are sent at once, and a round that would approve writes an APPROVE draft that waits for you. Behavior change: an APPROVE always waits for you, whatever the round and your rules say, where an earlier version's follow-up round could approve on its own after a verdict you stood behind. When the round would approve, it holds the APPROVE draft and its COMMENT variant and asks, with each option pointing to the draft it would post: picking the option approves that draft, as you edited it on the card, withdraws the others and completes the turn; a review the ambiguity gate holds alone ends the turn on that one draft, which waits on its card for you. In the session, telling the agent to post it, or how, has it revise the draft and post it with `draft_update` and `draft: false`. A free answer resumes the turn, and the agent revises the draft instead of writing a new one.

The card shows the whole payload that will be posted, and you can edit its text and its event there; an edit makes a new revision, and approving posts exactly the revision you approved. A review is bound to the head the agent read: when a push lands while it waits, the draft shows as stale and cannot be approved. Once the store reads the new head, `review-draft-stale` reopens the review round on its own, while the pull request is open and the draft is a review turn's: the round replaces the question that pinned the draft, and its instructions point to the draft (`draft.held_ref`) and carry the question, so the agent revises it against the new head and asks again, or withdraws it. You see only that round's output. A post GitHub refuses (a 422 on an inline comment) fails the draft with GitHub's message, and the unit reports `draft.failed` (`draft.stale` for a push) so a workflow can start a round for the agent to revise the draft. An agent's revision of a draft you approved opens a new gate that waits for you, until you tell the session you drive to post it.

To see what a review would publish without publishing it:

```sh
{{command}} profile try https://github.com/acme/api/pull/4610
```

It runs a round in a throwaway worktree with every GitHub write refused, and prints the review. It takes a few minutes.

## When it asks

The card moves to `needs-me`, the pull request shows in Needs me, and on macOS a notification offers the answers. Answer in the cockpit's detail, the notification, or the terminal client (`W`). Continue (`C`) sends the agent back without an answer; Dismiss (`X`) closes the question and the pull request goes back to whoever it waits on.

Once your answer reaches the agent, the question leaves the card and the row shows the loop working. When the agent is busy, the card says your answer is queued until its session is free; a new answer sent then replaces it.

## Follow-ups

After your review, the daemon starts the next round on its own when the author pushes new commits, replies, or re-requests you. Each one waits in the [approval window](/concepts/approval/) first. After an approval, only new commits or a re-request start one. A round with nothing new publishes nothing.

To stop the automatic rounds on one pull request:

```sh
{{command}} pause https://github.com/acme/api/pull/4610      # until resume
{{command}} resume https://github.com/acme/api/pull/4610
```

## First rounds on their own

A pull request that requests your review by name, from a trusted author in an allowed owner, gets a first round without your asking. Limit it with `plugins.github.owners` (the GitHub owners automated rounds run for) and widen it with `plugins.github.trusted_authors` (logins such as `github-actions[bot]` whose pull requests count as trusted). `{{command}} plan --unit <url>` says why a pull request got no first round in `pr.first.reason`: the profile does not want it, it is a draft, closed or yours, you reviewed it before, it fails the trust check, or its workspace exists. Turn first rounds off with:

```toml
[workflows.review-first-round]
enabled = false
```

The GitHub plugin's `review_wanted` predicate says which pull requests get one. The bundled one is `pr.review_requested_me`, the request by name. Your profile's value replaces it, so keep `pr.review_requested_me` in it when you widen it to review pull requests nobody asked you to, such as every one of a repository that has no CODEOWNERS:

```toml
# plugins/github/predicates.toml of your profile
review_wanted = "pr.review_requested_me || (pr.repo == 'acme/api' && !pr.mine)"
```

A value without `pr.review_requested_me`, such as `pr.repo == 'acme/api' && !pr.mine`, drops every review requested by name outside that repository. It still loads, since narrowing on purpose is valid, and `{{command}} profile check` warns about it.

It reads the pull request's [facts](/reference/facts/), and the inventory still decides which pull requests it sees: list the repository in `plugins.github.scope.repos`. A draft, your own pull request and one that fails the trust check above never get a first round, whatever it says; a pull request of a `plugins.github.trusted_authors` bot passes it, and gets one too. One the predicate wants without a request by name gets its first round once: after a review round of the loop or a review of yours on GitHub, only the follow-ups of a review start rounds, so removing its review workspace or a new push does not start a first round again. While it holds, no reviewer is requested and you have not reviewed its head, the pull request waits on your review.

No daily cap bounds how many first rounds it starts: `turns.per_unit_per_day` counts per pull request, and a first round runs once on each. When a wider predicate takes effect, every open pull request it holds for that you never reviewed gets its first round at once, each in its own workspace, after the approval window. Widen it one repository at a time, or `plugins.github.owners` and the inventory narrow which pull requests it sees. Most facts are the author's to set: `pr.labels`, `pr.base`, `pr.draft`, `pr.changed_files`, `pr.checks`, `pr.requested_teams` and the activity facts. A predicate over one lets authors opt their pull requests in.

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
