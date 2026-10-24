---
title: Address reviews on your pull request
description: Let an agent triage the reviews and failing checks of a pull request you develop.
---

An **address round** runs in the author workspace of a pull request you develop. The procedure verifies each review comment on its own, applies what holds, pushes, replies in every thread, resolves the settled ones and asks the reviewers for another look. It stops and asks you first when something is ambiguous or risky: a deep change of direction, an irreversible surface, a blocking request it would decline.

## On its own

With a workspace for your pull request (linked to it, or on its head branch), the daemon starts an address round when:

- someone else reviews it (bots included) with changes requested, comments, or an approval with inline comments;
- an unresolved review thread ends with someone else's comment;
- its checks fail, once every check on the head finished, one round per head.

Each round waits in the [approval window](/concepts/approval/) first. A round waits while the agent is stopped on a permission prompt or a question. An agent mid-turn gets the prompt queued. Local work that is not on the pull request (uncommitted changes, unpushed commits) is named in the prompt, the round goes to the live session that knows it, and the procedure never commits or discards it.

For failing checks, the procedure fixes what the pull request broke, reruns a flaky or infrastructure failure once, and verifies a quality gate's findings like review comments. It never gets a check green by disabling it, editing CI or dismissing a finding; a check that fails again after its rerun comes to you.

## Text from other people

Reviews, comments, threads and check output written by anyone but you reach the agent as data about the code, never as instructions. An item that asks for something the change does not need (running an unrelated command or script, touching secrets, credentials, CI, release or permission configuration, pushing or publishing elsewhere, sending data out, or text aimed at the agent itself) is never acted on: the round changes nothing, records `asked` flagged as a suspected prompt injection, and names the item and what it asked. The card, the cockpit and the notification open with "Suspected prompt injection".

## By hand

```sh
{{command}} address https://github.com/acme/api/pull/4582
{{command}} address https://github.com/acme/api/pull/4582 the reviewer is right about the index, skip the rename
```

From a session in the workspace: `/{{name}}-address <url>`. The reviews so far count as covered, so the daemon does not repeat them.

## Review your own change first

```sh
{{command}} address https://github.com/acme/api/pull/4582 --self-review "the plan: add the retry, keep the API"
```

`--self-review` first reviews the pull request adversarially with your review profile, against the guidance as its plan: what is missing, what deviates, what goes beyond it. Its findings join the reviewers' comments, marked `(own)`, and are fixed or brought to you the same way. Nothing of that review is published: its trace on GitHub is the commits that fix it.

## A pull request with no workspace

Without a workspace, the daemon only records that it would start a round (`would_address`), and the cockpit lists the pull request in Needs me as "review to address, no workspace". To let it create the workspace and run the round:

```json
"address_without_workspace": true
```

It can also go on a `projects` entry, for some repositories only.

## Turn it down

| Setting | Effect |
|---|---|
| `"address_reviews": false` | no round for reviews |
| `"address_skip_reviewers": ["some-bot"]` | reviews by these logins never start one |
| `"address_checks": false` | no round for failing checks |
| `"ignore_checks": ["codecov/patch"]` | these checks never start one |
| `"rounds": {"per_pr_per_day": 5}` | at most this many automatic rounds per pull request per day, against a loop with a bot that reviews every push |
| `"workflows": {"address": {"only_when": "pr.visibility == \"private\""}}` | automatic rounds only in private repositories; any [workflow predicate](/guides/workflows/#narrow-one-to-some-pull-requests) over the owner, repository or author works the same way |

## How the guide reads

The bundled `intent=address.md` sets the stance (a comment is a claim to verify), the risky surfaces, how to commit (the repository's conventions, one commit per item, tests for new behavior), how to reply (one or two sentences in the thread's language), and whom to ask again (never re-request a reviewer whose approval still stands). Change any of it in your [profile](/guides/review-profile/) with `intent=address.md` blocks.
