---
title: The sweep and workflows
description: Everything the loop does on its own, written as rules you can read and switch.
---

Everything {{name}} does without being asked is a **workflow**: a manifest, `workflow.toml`, that names the provider of the units it looks at (pull requests, agent sessions, workspaces, tasks or Slack threads), a condition over their facts, and the steps to run. The daemon evaluates every workflow on each **sweep**, right after each fast job.

GitHub cannot call your laptop, and the events that matter (a push, a reply, an approval, a merge) happen there. The sweep observes their result on a schedule, costs a few GraphQL requests per run, and does nothing when nothing changed. Each sweep plans every workflow on one snapshot of the store, reading each table once for all the units, so what it decides of one unit and another comes from the same moment. It judges the drafts for withdrawal on a snapshot of its own, taken again after each withdrawal, so the drafts after one see it.

## The bundled workflows

| Workflow | Does | Ships as |
|---|---|---|
| `review-first-round` | starts a first review round on a pull request that requests your review by name | runs after a 60 s delay |
| `review-follow-up` | starts a follow-up round when the author pushed, replied or re-requested you | runs after a 60 s delay |
| `address` | starts an address round on your pull request when reviews arrive, checks fail or it conflicts with its base | runs after a 60 s delay |
| `nudge-silent-30d` | asks whoever someone else's pull request waits on, after `plugins.github.nudge.silent_days` (30) of silence | waits for you |
| `session-pr-finished`, `session-duplicate`, `session-worktree-gone`, `session-finished-quiet`, `session-asked-quiet`, `session-close-when-done` | close agent sessions that are finished, duplicated or quiet, whose worktree or transcript is gone, or that their manager closed (a session you archived in Claude Desktop) | runs |
| `session-manual-quiet` | offers to close a session you started that went quiet | waits for you |
| `answer-unread` | notifies you when an agent session leaves an answer of yours unread for 15 minutes | runs |
| `workspace-review-finished`, `workspace-author-finished`, `workspace-temporary`, `workspace-duplicate` | remove workspaces of finished pull requests, under temporary paths, or duplicated | **observe** |
| `workspace-path-gone` | unregisters a workspace whose path no longer exists | runs |
| `workspace-idle-no-pr` | offers to remove a workspace with no pull request and no open session, idle for `cleanup.stale_workspace` | waits for you |
| `hygiene-turn-on-finished-pr`, `hygiene-orphan-answer`, `hygiene-session-dormant` | keep the turn and session records consistent; a session whose process its manager stopped listing turns dormant, which ends no process | runs |
| `hygiene-session-orphaned` | marks dormant a session whose terminal you closed in the app, its process left orphaned with no agent, when no turn runs in it and you did not prompt it; it ends no process, and the session wakes when a window shows the process again | runs |
| `session-orphan-close` | ends the process such a session left behind, once the census proves the orphan durable, under `session.close`'s guards | **off** (`enabled = false`); `workflows.session-orphan-close.enabled = true` turns it on |

A run that waits for you or for its window is a plan gate on its unit. It asks what the workflow's `ask` renders over the unit's facts (`.Unit`, the unit as people read it, and `.Facts`, by name), else its `summary`; the Inbox, the cockpit and the terminal show those words, and a task opened for the gate takes them as its title. While the item waits, the gate carries it too: the cockpit's card for the gate reads the workflow's `summary` (what running it does, what it posts or not), the unit it acts on, linked, and, folded, the facts that fired it, each in the words of the task facet that reads it, else as `name = value`; the item's steps show only when the workflow has no `summary`. The gate is withdrawn when its item stops waiting: the unit left its plugin's inventory, the workflow's `when` no longer holds on it, or its key moved.

An **observe** workflow (`observe = true`) plans and runs nothing by itself: `{{command}} plan` shows what each would do. The four workspace removals ship that way because a removal cannot be undone. [Tune workflows](/guides/workflows/) says when and how to switch them on.

## Automatic rounds

**First round.** A pull request that requests your review by name (team requests are left out), is open, not a draft, not from a fork, whose author is a member, collaborator or owner (or listed in `plugins.github.trusted_authors`), whose owner is allowed by `plugins.github.owners`, and that has no workspace of yours yet.

**Follow-up.** After your last review: new non-merge commits, a comment or review by the author (ignored after an approval), or your review re-requested. There is one follow-up per published review and head, plus one per re-request. A round with nothing new to say publishes nothing and records `settled`; the author's next push starts another. A round that finds the author silent on every point of your last review asks you instead. An automatic first round never approves on its own; an automatic follow-up approves only after a verdict you stood behind, when every blocking finding is resolved and the new commits only answer the reviews.

**Address.** On a pull request you develop: a review by someone else (bots included) with changes requested, comments, or an approval with inline comments; an unresolved thread whose last comment is someone else's; failing checks, once every check on the head finished, one round per head; or a conflict with the base that lasted the conflict window (`sweep.fast`, at least five minutes), one round per head. Turn them off with `plugins.github.address_reviews: false`, `plugins.github.address_checks: false` and `plugins.github.address_conflicts: false`, or skip some with `plugins.github.address_skip_reviewers` and `plugins.github.ignore_checks`.

**Postponed.** A round waits while the agent of its workspace works or waits for input, or while you have the workspace focused in its manager. It waits too while its intent's newest turn asks you something, unless its step sets `open_question = "replace_if_moved"` and the pull request's head moved since the question: `review-follow-up` does, so a follow-up on new commits replaces an unanswered review question and carries it (see [Turns](/concepts/turns/#answers)). `turns.per_unit_per_day` (5) bounds the automatic rounds per pull request per day; a held round marks the card unread, shows as "held (cap)", and `{{command}} review <url>` or `{{command}} address <url>` runs it by hand, outside the cap.

**Without a workspace.** A review workspace you deleted by hand frees its checkout and the pull request stays followed; the follow-up is only recorded (`would_review`) unless `plugins.github.followups_without_workspace` is on. Likewise, your pull request with no workspace gets its address round recorded (`would_address`) and shown in Needs me as "review to address, no workspace", unless `plugins.github.address_without_workspace` is on.

## React to a verb

A verb run on a task, such as Done, Someday or Delegate, records a `verb_<verb>` event on the task (`verb_done`, `verb_someday`), whoever ran it: you, or a workflow. A workflow with `provider = "task"` reacts to it with `trigger.on = ["verb_done"]`, for any verb of the profile's `tasks.toml`; its `trigger.event.data` is the state the task left, and the task's facts are read even once it is done or dismissed. Its default key is the task's fingerprint; `key = ["trigger.event.id"]` plans one run per event.

So workflows can trigger each other: one clarifies a task, another reacts to that move and sets its kind, a third starts a turn. Each event carries its chain, the workflows that led to it, and three guards bound it:

- `trigger.actors` limits the trigger to the verbs of those actors: `actors = ["person"]` listens to your moves alone;
- `trigger.depth` (3 when absent, at most 5) is the longest chain the workflow may close: an event that a chain of that many workflows caused plans it no more;
- a workflow never runs on an event whose chain it is in already, so A then B then A cannot happen.

Marks keep each trigger handled once, and the turn budgets bound what any turn a chain starts costs.

The core's own moves and a project's verbs record the event with the change, in one write; a verb that changes nothing, such as the area the task already has, records nothing. A verb another part runs, a question's answer, a unit's action through Approve, Delegate or Dismiss, or Reply, records it once that succeeded. `seen` records nothing, since clients send it when you open a task, and neither do the verbs on an Outbox draft.

An event workflow reads its task's facts even once the task is done or in the trash, so it reacts to the verb that took it there. The same holds while its runs wait and when its drafts are judged for withdrawal: guard on `task.state` in `when` and `[drafts.withdraw]` when a run should not go on after the task left its list.

A workflow moves the task itself with the `task.verb` step: `verb = "next"`, or `verb = "waiting"` with `on` and `resurface`, among the verbs whose `by.workflow` in `tasks.toml` lets a workflow run them (bundled: next, waiting, schedule, wake, focus, done and dismiss). The arguments a verb takes are its handler's. The plan blocks the step while the task's state refuses the verb or the verb's `by.workflow` does not hold on the task. A move a workflow makes records its verb event, by `workflow:<name>`, and may trigger another workflow within the bounds above. It is the task's own move: `dismiss` sends the task to the trash and never runs its unit's dismiss action, as your Dismiss does on a task whose unit offers one, and a verb's `offer` in `tasks.toml` does not bind it.

```toml
summary = "Mark done a waiting task whose unit has nothing open"
provider = "task"
when = 'task.state == "waiting" && task.unit != "" && task.items_open == 0'

[[step]]
use = "task.verb"
verb = "done"
```

A workflow sets a property of the task with the `task.set` step: `prop`, a property of `tasks.toml` whose writers include `workflow`, and `value`, read as the property's type reads it (a date as `YYYY-MM-DD`, midnight in your location, a duration as `90m` or `2d`). The step is refused for a property a workflow does not write or a value its type does not read. The property records the workflow as its writer.

## The plan

Evaluating the workflows yields **plan items**, each with a decision:

- **pending**: waits for you, with a window. `runs in 41s` runs when the window ends; `waits for you` stays until you act.
- **blocked**: a guard refused it (a workspace holds unpushed commits, say), with the reason and the command that backs the work up.
- **run**: the next sweep runs it.
- **skipped**: the workflow matched and its rules say not now, with the reason. A guard whose refusal ends at a known time skips the item until then: a draft proposal held by an earlier draft for `repeat_after` waits out that window off the list of blocked items.

A guard that refuses for good withdraws the item: its subject is gone or its step can never run on it again, such as a session close whose session is already closed, a draft proposal held while its task stays ended or by a draft sent with `repeat_after_sent = "forever"`, or one its own `[drafts.withdraw] when` would withdraw at once. The item leaves the plan with a `workflow_dropped` event that records the reason, and nothing asks you to dismiss it; the workflow plans it again when its unit matches on facts the guard passes. A blocked item is left for you only when you can act on it, and its reason says how.

```sh
{{command}} plan                                  # what the next sweep would do
{{command}} plan --unit https://github.com/acme/api/pull/7   # why nothing happens to this one
{{command}} plan apply 3f9a1c2e                   # run an item now
{{command}} plan dismiss 91c0e5d2                 # drop it until something new happens
```

`plan` writes nothing and asks GitHub nothing. `plan --unit` lists every workflow that gives the unit no item, with the facts its condition read, such as `pr.first.reason`. The cockpit and the terminal client show the same items with the same actions; the cockpit names each item's workflow by its `summary`, in the interface language, with the workflow's name in the tooltip. A task's item names the task by its title, linked to its detail, with the conversations it came from; says what applying does, each step in the words of `strings.toml` (an `items.resolve` with the open items it resolves and the resolution in words), and that it posts nothing when no step may post; and folds the facts that fired it. An item's reason reads through `strings.toml` in both clients: the engine gives each of its own reasons from a template the plan serves as `reason_key`, with the values it carries (a wait rule's held facts worded as the gate card words them), and any other reason, a guard's or a step's refusal, reads as its text where `strings.toml` has no words for it. A summary is for people: it says what running does, and names no configuration key or step; whether it posts the cockpit says from its steps.

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

A session whose terminal the app shows with no agent in it refuses too, since your own shell may run there: close that terminal, or dismiss the item; so does one with no workspace to find its terminal in. One whose terminal is closed refuses only until the census marks it dormant. A terminal you closed in Orca can leave its process behind, listed orphaned with no agent. Once two complete censuses a fast job apart list it so, `hygiene-session-orphaned` marks the session dormant without ending the process, under the guards of `session.close` that need no shown terminal: no turn sent or started in it, its agent neither working nor waiting for permission (read by its pane, else by any agent of its workspace), its workspace not the one focused, and no prompt of yours since the loop's last turn. `session-manual-quiet`, which offers only active sessions, then leaves the plan. A close of the dormant session waits, skipped and off the list of blocked items, while the census still lists its process orphaned and has not proven it a lasting orphan; it runs once the process is gone or proven so, or once the process has stayed listed orphaned for `cleanup.orphan_hold` (1d) with the app showing some terminal at every reading, whatever its last output. Such a close records the session closed and ends no process. A workspace removal does not wait on it: it closes the dormant session's record the same way. If a window shows the process meanwhile, the next census wakes the session. A turn that would wake a dormant session whose process the census still lists, or cannot tell about, starts a new session instead, so two processes never resume one agent session; while Orca cannot list its terminals completely, every such wake starts a new session. Enter on such a run refuses and asks you to show its terminal. A session already closed, or no longer known, withdraws its close.

`session-orphan-close` ends the leftover process itself. It ships off: ending a process risks a pane the app has not reattached yet. Turned on, it acts only once the census proves the orphan durable, in readings of the same runtime whose windows showed some terminal and whose window graph held for a fast job: the orphan outlived a restart of the app's windows after it stayed orphaned for a fast job in windows that showed some terminal, or it stayed orphaned two hours, every reading showing some terminal, with a known last output from before. A restart that shows processes orphaned until the windows reclaim them, or two restarts in a row, prove nothing. One case the proof cannot tell apart: a pane the windows leave unreclaimed through a whole epoch while other tabs show, say of a worktree you never open, counts as an orphan once the windows restart again. It reads a fresh census before ending the process and refuses when the process is no longer orphaned in that runtime and window graph, and it keeps the guards above.

Your own workflows go in your profile; see the [workflow manifest](/reference/workflow-manifest/).
