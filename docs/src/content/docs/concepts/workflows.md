---
title: The sweep and workflows
description: Everything the loop does on its own, written as rules you can read and switch.
---

Everything {{name}} does without being asked is a **workflow**: a manifest, `workflow.toml`, that names the provider of the units it looks at (pull requests, agent sessions, workspaces, GTD entries or Slack threads), a condition over their facts, and the steps to run. The daemon evaluates every workflow on each **sweep**, right after each fast job.

GitHub cannot call your laptop, and the events that matter (a push, a reply, an approval, a merge) happen there. The sweep observes their result on a schedule, costs a few GraphQL requests per run, and does nothing when nothing changed.

## The bundled workflows

| Workflow | Does | Ships as |
|---|---|---|
| `review-first-round` | starts a first review round on a pull request that requests your review by name | runs after a 60 s delay |
| `review-follow-up` | starts a follow-up round when the author pushed, replied or re-requested you | runs after a 60 s delay |
| `address` | starts an address round on your pull request when reviews arrive, checks fail or it conflicts with its base | runs after a 60 s delay |
| `nudge-silent-30d` | asks whoever someone else's pull request waits on, after `github.nudge.silent_days` (30) of silence | waits for you |
| `session-pr-finished`, `session-duplicate`, `session-worktree-gone`, `session-finished-quiet`, `session-asked-quiet`, `session-close-when-done` | close agent sessions that are finished, duplicated or quiet, whose worktree or transcript is gone, or that their manager closed (a session you archived in Claude Desktop) | runs |
| `session-manual-quiet` | offers to close a session you started that went quiet | waits for you |
| `workspace-review-finished`, `workspace-author-finished`, `workspace-temporary`, `workspace-duplicate` | remove workspaces of finished pull requests, under temporary paths, or duplicated | **observe** |
| `workspace-path-gone` | unregisters a workspace whose path no longer exists | runs |
| `workspace-idle-no-pr` | offers to remove a workspace with no pull request and no open session, idle for `cleanup.stale_workspace` | waits for you |
| `hygiene-turn-on-finished-pr`, `hygiene-orphan-answer`, `hygiene-session-dormant` | keep the turn and session records consistent; a session whose process its manager stopped listing turns dormant | runs |

An **observe** workflow (`observe = true`) plans and runs nothing by itself: `{{command}} plan` shows what each would do. The four workspace removals ship that way because a removal cannot be undone. [Tune workflows](/guides/workflows/) says when and how to switch them on.

## Automatic rounds

**First round.** A pull request that requests your review by name (team requests are left out), is open, not a draft, not from a fork, whose author is a member, collaborator or owner (or listed in `github.trusted_authors`), whose owner is allowed by `github.owners`, and that has no workspace of yours yet.

**Follow-up.** After your last review: new non-merge commits, a comment or review by the author (ignored after an approval), or your review re-requested. There is one follow-up per published review and head, plus one per re-request. A round with nothing new to say publishes nothing and records `settled`; the author's next push starts another. A round that finds the author silent on every point of your last review asks you instead. An automatic first round never approves on its own; an automatic follow-up approves only after a verdict you stood behind, when every blocking finding is resolved and the new commits only answer the reviews.

**Address.** On a pull request you develop: a review by someone else (bots included) with changes requested, comments, or an approval with inline comments; an unresolved thread whose last comment is someone else's; failing checks, once every check on the head finished, one round per head; or a conflict with the base that lasted the conflict window (`sweep.fast`, at least five minutes), one round per head. Turn them off with `github.address_reviews: false`, `github.address_checks: false` and `github.address_conflicts: false`, or skip some with `github.address_skip_reviewers` and `github.ignore_checks`.

**Postponed.** A round waits while the agent of its workspace works or waits for input, or while you have the workspace focused in its manager. `turns.per_unit_per_day` (5) bounds the automatic rounds per pull request per day; a held round marks the card unread, shows as "held (cap)", and `{{command}} review <url>` or `{{command}} address <url>` runs it by hand, outside the cap.

**Without a workspace.** A review workspace you deleted by hand frees its checkout and the pull request stays followed; the follow-up is only recorded (`would_review`) unless `github.followups_without_workspace` is on. Likewise, your pull request with no workspace gets its address round recorded (`would_address`) and shown in Needs me as "review to address, no workspace", unless `github.address_without_workspace` is on.

## React to a verb

A verb you run on an entry, such as Done, Someday or Delegate, records a `verb_<verb>` event on the entry (`verb_done`, `verb_someday`). A workflow with `provider = "entry"` reacts to it with `trigger.on = ["verb_done"]`; its `trigger.event.data` is the state the entry left, and the entry's facts are read even once it is done or dismissed. Its default key is the entry's fingerprint; `key = ["trigger.event.id"]` plans one run per event.

The core's own moves record the event with the change, in one write. A verb another part runs, a project's, a question's, a unit's action through Approve, Delegate or Dismiss, or Reply, records it once that succeeded. `seen` records nothing: clients send it when you open an entry.

## The plan

Evaluating the workflows yields **plan items**, each with a decision:

- **pending**: waits for you, with a window. `runs in 41s` runs when the window ends; `waits for you` stays until you act.
- **blocked**: a guard refused it (a workspace holds unpushed commits, say), with the reason and the command that backs the work up.
- **run**: the next sweep runs it.
- **skipped**: the workflow matched and its rules say not now, with the reason.

```sh
{{command}} plan                                  # what the next sweep would do
{{command}} plan --unit https://github.com/acme/api/pull/7   # why nothing happens to this one
{{command}} plan apply 3f9a1c2e                   # run an item now
{{command}} plan dismiss 91c0e5d2                 # drop it until something new happens
```

`plan` writes nothing and asks GitHub nothing. `plan --unit` lists every workflow that gives the unit no item, with the facts its condition read, such as `pr.first.reason`. The cockpit and the terminal client show the same items with the same actions.

## Pause

`{{command}} pause` stops every workflow but the observe ones, and every board move until `{{command}} resume`; `{{command}} pause <owner/repo>` or `{{command}} pause <url>` stops them on that repository or pull request alone, until `resume` names it, and `resume` with no argument resumes everything. Observe workflows keep planning. The cockpit's header has the same switch, and `doctor` says when workflows are paused.

## Cleanup guards

`workspace.remove` never forces. A workspace that holds work is blocked, or waits for you, with the reason and a command that pushes a backup to the workspace's own remote:

- an untracked file outside `cleanup.disposable` (`node_modules`, `.venv`, `dist`, `.terraform` and the like);
- an ignored file that differs from the main checkout's copy;
- a nested repository with changes;
- commits on no remote, or a deleted remote branch while local commits exist;
- a live session that closing would refuse.

`session.close` refuses while a turn is sent or started, the agent works or waits for permission, the workspace is focused in its manager, or you typed in the session after the loop's last turn. A dormant session has no process, so the checks on the agent's work and permission prompts do not apply to it.

Your own workflows go in your profile; see the [workflow manifest](/reference/workflow-manifest/).
