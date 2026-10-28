---
title: Facts and steps
description: The facts that intent and workflow predicates read, and the steps a workflow can run.
---

Predicates are CEL expressions; their syntax is on the [intent manifest](/reference/intent-manifest/#predicates) page. A missing fact reads as its type's zero value.

## Intent facts

The predicates of an intent manifest read these. The **action** slot (`[action]` predicates) reads the row the daemon serves; the **turn** slot (`[record] when`, headline cases, `also_profile[].when`) reads the turn being built.

### The pull request (`pr.*`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `pr.mine` | bool | action, turn | The pull request's author is the viewer. |
| `pr.author` | string | action, turn | The pull request's author login; empty when not stored. |
| `pr.state` | string | action, turn | The pull request's state on GitHub: OPEN, CLOSED or MERGED. |
| `pr.slug` | string | action, turn | owner/repo#number. |
| `pr.owner` | string | action, turn | The repository owner. |
| `pr.repo` | string | action, turn | owner/repo. |
| `pr.repo_name` | string | action, turn | The repository name, without its owner. |
| `pr.base` | string | action, turn | The base branch. |
| `pr.association` | string | turn | The author's association with the repository, from the Basics read. |
| `pr.cross_repository` | bool | action, turn | The head branch lives in a fork. |
| `pr.visibility` | string | turn | The repository's visibility: public, private or internal, from the Basics read; empty before it. |
| `pr.requested_teams` | list | turn | The teams a review of the pull request was requested through, as <org>-<slug>. |
| `pr.labels` | list | turn | The pull request's labels, from the Basics read. |
| `pr.address.reviewers` | list | turn | The reviewers the address round answers: on its turn, those it answers, empty on any other; in a workflow, those it would answer. |
| `pr.address.checks` | list | turn | The failing checks the address round fixes: on its turn, those it fixes, empty on any other; in a workflow, those it would fix. |
| `pr.address.conflict` | bool | turn | The head conflicts with the base past the conflict window and the address round resolves it: on its turn, it does, false on any other; in a workflow, it would. |
| `pr.draft` | bool | action | The pull request is a draft, from the inventory row. |
| `pr.review_requested_me` | bool | action | A review is requested from the viewer, from the inventory row. |
| `pr.adopted` | bool | action | The pull request was taken over into the loop. |
| `pr.held` | bool | action | Rounds on the pull request are held. |
| `pr.muted` | bool | action | The pull request is muted, by the server clock. |
| `pr.nudge.who` | list | action | Whom a nudge would address, from nudge.targets. |
| `pr.nudge.why` | string | action | Why the pull request waits on them, from nudge.targets. |
| `pr.nudge.refusal` | string | action | The text nudge.prepare would refuse with now; empty when a nudge may go. |
| `pr.last_review.exists` | bool | turn | The viewer reviewed the pull request before. |
| `pr.last_review.verdict` | string | turn | The viewer's newest review state, any state; empty when none. |
| `pr.last_review.decision` | string | turn | The viewer's newest non-comment verdict; empty when none. |

### The row (`row.*`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `row.kind` | string | action | The row's situation kind; a plugin's row_kinds close it on its units. |
| `row.open` | bool | action | The pull request is open: kind is not done, or, untracked, its GitHub state is open. |
| `row.tracked` | bool | action | The pull request is in the inventory. |
| `row.question` | bool | action | A turn waits on the user's answer. |
| `row.pending` | bool | action | A round is pending. |
| `row.capped` | bool | action | The daily budget held the row's automatic round. |
| `row.intake` | bool | action | An intake is open on the pull request. |
| `row.intake_seq` | int | action | The number of the open intake, the waiting one first; 0 when none is open. |
| `row.author_workspace` | bool | action | A linked workspace holds the author role. |
| `row.yours` | bool | action | The row's unit is the viewer's to act on: any unit but a pull request, or a pull request that is the viewer's, taken over or held by an author workspace. |
| `row.review_workspace` | bool | action | A linked workspace holds the review role. |
| `row.trust_folder` | string | action | The folder whose trust lifts the row's start block; empty for a block on another question. |
| `row.trust_homes` | list | action | The agent homes trusting row.trust_folder writes in. |
| `row.remove_refusal` | string | action | What refused removing the row's workspace, which a force remove overrides; empty when nothing did. |
| `row.recommended` | string | action | The runner recommended for the row's items: session or headless; empty when none is. |

### The task (`task.*`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `task.captured_by_user` | bool | action | The user captured the task: origin user and kind capture; false for a derived task, such as one from a Slack thread. |
| `task.title` | string | name | The task's title. |
| `task.short_id` | string | name | The task's id, short: its first six hex digits. |

### The turn (`turn.*`, `unit.*`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `turn.automated` | bool | turn | The sweep started the turn. |
| `turn.owned` | bool | turn | The viewer owns the pull request's loop. |
| `turn.needs_take_over` | bool | turn | owner_only, not owned, and no input with before = "take_over" set. |
| `turn.resume` | bool | turn | The turn resumes an earlier one. |
| `turn.try` | bool | turn | The turn is a dry run (profile try). |
| `turn.planned` | string | turn | The round the request planned: first, follow-up, or empty for none. |
| `turn.stage` | string | turn | first or follow-up; only intents with dims = ["stage"] may read it. |
| `unit.ref` | string | turn | The unit as the instructions name it, such as owner/repo#12. |
| `unit.label` | string | name | The unit as people read it: a pull request as owner/repo#12, any other unit as provider:key. |

### A name (`session.*`, `workspace.*`; slot `name`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `session.title` | string | name | The manifest's session.title. |
| `workspace.name` | string | name | The name of the worktree created for the unit; empty when the turn creates none or before it is named. |
| `session.started` | timestamp | name | When the session starts; a template formats it with Go's layout, as {{.session.started.Format "02/01 15:04"}}. Zero when unset; test with `> timestamp(0)`. |

### A draft's actions (`draft.*`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `draft.state` | string | draft_action | The draft's state. |
| `draft.provider` | string | draft_action | The provider of the draft's unit. |
| `draft.refusal` | string | draft_action | Why the action is disabled: plugin_off, state or posts_nothing; empty when it is enabled. |
| `draft.maybe_posted` | bool | draft_action | The draft failed after its post may have reached its provider. |

### The configuration (`config.*`)

| Fact | Type | Slots | Meaning |
|---|---|---|---|
| `config.plugins.github.address_reviews` | bool | action, turn | The plugins.github.address_reviews setting for the pull request. |
| `config.plugins.github.address_checks` | bool | action, turn | The plugins.github.address_checks setting for the pull request. |
| `config.plugins.github.followups_without_workspace` | bool | action, turn | The plugins.github.followups_without_workspace setting for the pull request. |
| `config.plugins.github.address_without_workspace` | bool | action, turn | The plugins.github.address_without_workspace setting for the pull request. |

## Workflow facts

A workflow's `when` reads the facts of its provider's slot, plus `trigger.*`, `config.*` and `now`; a provider whose units have a brief also reads `brief.*`, and `[drafts.withdraw] when` also reads `draft.*`. `{{command}} workflows facts [github|session|slack|task|workspace]` prints them. A pull request fact has one name and one spelling in every slot: `pr.repo` is `owner/repo` and `pr.repo_name` the bare name.

### Sessions (`session.*`)

| `session` fact (provider `session`) | Type | Meaning |
|---|---|---|
| `session.state` | string | active (it has a live process), dormant (no process; its manager can wake it) or closed. |
| `session.manager` | string | The workspace manager that owns the session, such as orca or claude-desktop. |
| `session.managed` | bool | The session was started by the loop (sessions.managed). |
| `session.listed` | bool | The session's manager lists its process as usable. |
| `session.orphaned` | bool | The session's manager lists its process as not usable. |
| `session.agent_state` | string | The agent's state as the manager reports it. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.user_active` | bool | The user acts in the session now. |
| `session.user_typed_after_turn` | bool | The user typed in the session after the loop's last turn. |
| `session.prompt_knowable` | bool | Whether the user typed can be known, from the hook or the transcript. |
| `session.prompted_while_idle` | bool | A manual session got a prompt during its idle period. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.open_turn` | string | The state of the session's open turn; empty when none. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.newest_state` | string | The state of the session's newest turn. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.newest_age` | duration | Time since the newest turn was sent or started. |
| `session.newest_intent` | string | The intent of the newest turn. |
| `session.closes_when_done` | bool | The newest turn's intent closes its session when done. |
| `session.idle_since` | timestamp | When the current idle period started. Zero when unset; test with `> timestamp(0)`. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.idle_for` | duration | Time since idle_since. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.unread_answers` | int | Answers the user gave to the session's turns whose notice reached the session and that its agent has not read with inbox_read. |
| `session.unread_since` | timestamp | When the notice of the oldest of unread_answers reached the session; zero when none. Test with `> timestamp(0)`. |
| `session.unread_for` | duration | Time since unread_since; 0 when none. |
| `session.pr_state` | string | The state of the book's pull request: OPEN, CLOSED or MERGED; empty when none. |
| `session.duplicate` | bool | Another listed session resumes the same agent session; the focused one, one the user prompted, and else the oldest are never duplicates. |
| `session.worktree_gone` | bool | The session's worktree path no longer exists. |
| `session.closed_by_manager` | bool | The dormant session's manager keeps its own record of it, and the user closed it there, such as archiving it in Claude Desktop. |
| `session.transcript_gone` | bool | The agent's transcript of the session, as its hooks reported it, no longer exists. |
| `session.process_gone` | bool | The active session's process was missed by two complete censuses of its manager. |
| `session.fingerprint` | string | The session id, its state, the start of the idle period and the newest turn's sequence. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `session.repo` | string | owner/repo of the book's pull request; empty when none. |

### Workspaces (`workspace.*`)

| `workspace` fact (provider `workspace`) | Type | Meaning |
|---|---|---|
| `workspace.role` | string | author (a taken-over workspace included), review or none. |
| `workspace.main` | bool | The workspace is the repository's main checkout. |
| `workspace.pr` | string | The linked pull request, owner/repo#number; empty when none. |
| `workspace.pr_state` | string | The linked pull request's state: OPEN, CLOSED or MERGED; empty when none. |
| `workspace.pr_finished_for` | duration | The linked pull request's pr.finished_for. |
| `workspace.path_exists` | bool | The worktree path exists. |
| `workspace.temporary` | bool | The worktree lives under a temporary path. |
| `workspace.duplicate` | bool | Neither a main checkout nor the workspace the manager links to its pull request, while another non-main workspace is. |
| `workspace.dirty` | int | Modified tracked files. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.untracked` | int | Untracked files outside cleanup.disposable. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.unpushed` | int | Commits on no remote. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.newest_turn_state` | string | The state of the newest turn of the linked pull request's book; empty when none. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.open_turn` | bool | The linked pull request's book has an open turn. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.pendency` | list | What holds work: modified files, untracked files, commits on no remote, turn asked unanswered, turn failed, turn unrecorded, turn open. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.sessions` | int | Agent sessions in the workspace that are active, or dormant and active within cleanup.idle. |
| `workspace.manager` | string | The workspace manager that owns the worktree. |
| `workspace.last_activity` | timestamp | The newest activity in the workspace. Zero when unset; test with `> timestamp(0)`. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.quiet_for` | duration | Time since last_activity. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.fingerprint` | string | The worktree path and its head. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `workspace.repo` | string | owner/repo; empty when unknown. |

### Tasks (`task.*`)

| `task` fact (provider `task`) | Type | Meaning |
|---|---|---|
| `task.id` | string | The task's id. |
| `task.kind` | string | The task's kind, such as action, waiting, close, message, capture or project. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.state` | string | The task's state, one of the states tasks.toml declares. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.waiting_on` | list | The person keys the task waits on, such as slack:U0123. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.resurface_at` | timestamp | When the task resurfaces; zero when it has no date. Test with `> timestamp(0)`. |
| `task.waiting_days` | int | Working days the task has waited, in the configured location, as the waiting rules count them. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.since_nudge` | duration | Time since the task's last nudge; very large when it was never nudged. |
| `task.nudge_due_at` | timestamp | When a stalled nudge is due: the resurface date when set, else tasks.stale_waiting_days working days after the task started waiting, then drafts.repeat_after after the last nudge; zero when the task is not stalled. Test with `> timestamp(0)`. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.muted` | bool | The task is muted. |
| `task.unit` | string | The task's first unit as provider:key: the unit it was captured or derived on, else its first attachment; empty for a task with none. |
| `task.unit_provider` | string | The provider of task.unit; empty for a task with none. |
| `task.units` | list | The task's units as provider:key, in order: the unit it was captured or derived on, then those attached to it in attach order, then any only its items name. The task itself is not one of them. |
| `task.unit_providers` | list | The providers of task.units, each once, in order. |
| `task.link_ended` | bool | The task has a unit, and every one of task.units has ended as its plugin says (pr.ended, slack.ended); a unit of a plugin that says nothing never ends. |
| `task.turns` | int | Turns run on the task itself, such as a start; turns on its units are not counted. |
| `task.captured_by_user` | bool | The user captured the task: origin user and kind capture. False for a task derived from a unit's items, such as one from a Slack thread, or opened by another service. |
| `task.link_started_by_me` | bool | The viewer started task.unit, from the facts the tasks receive. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.link_closure_posted` | bool | A closure was posted on task.unit, from the facts the tasks receive. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.items_open` | int | Open items of the task's units. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.items_resolved` | int | Resolved items of the task's units. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.items_owed` | int | Open items of the task's units that the task holds. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.settled_at` | timestamp | The newest update of a settled draft the workflow proposed on the task, withdrawn ones included; zero when none. Test with `> timestamp(0)`. |
| `task.fingerprint` | string | The task's id, state and update time. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.category` | string | The category of the task's state: inbox, ready, active, waiting, deferred, done or dropped. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.owed_by` | string | Who owes the task's next step, as tasks.toml's owed_by says: me, a person's key, or empty when nobody does. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.owed_since` | timestamp | Since when task.owed_by has owed the next step; zero when nobody does. Test with `> timestamp(0)`. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.since_movement` | duration | Time since the task, or a unit linked to it, last moved: a state change, a source event, a run. |
| `task.check_on` | timestamp | When to check on the task, its check_on property; zero when unset. Test with `> timestamp(0)`. |
| `task.gates_open` | int | The task's open gates. |
| `task.runs_live` | int | The task's runs whose session is live. |
| `task.wake_until` | timestamp | When the task's wake wakes it; zero when it has none, or one that waits on events or a condition only. Test with `> timestamp(0)`. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.wake_when` | string | The condition the task's wake waits on; empty when none. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.repo` | string | owner/repo of the task's work: its placement, else its pull request's; empty when unknown. |
| `task.sequential_next` | bool | The task is the first open child of a sequential parent. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.review_due` | bool | The task is a parent whose review is due. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.children_ready` | bool | The task has a child in the ready category. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.ask_age` | duration | The age of the task's oldest unanswered ask; zero when none is. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.promised` | timestamp | The date the user promised the task by, its promised property; zero when unset. Test with `> timestamp(0)`. |
| `task.extraction_lag` | duration | How long the oldest message of a unit linked to the task has waited to be extracted; zero when none waits. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `task.link_owes` | list | The people the task's links owe a step to, as the plugins report them, each once. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |

### Pull requests (`pr.*`)

| `pr` fact (provider `github`) | Type | Meaning |
|---|---|---|
| `pr.mine` | bool | The pull request's author is the viewer. |
| `pr.author` | string | The pull request's author login; empty when not stored. |
| `pr.state` | string | The pull request's state on GitHub: OPEN, CLOSED or MERGED. |
| `pr.slug` | string | owner/repo#number. |
| `pr.owner` | string | The repository owner. |
| `pr.repo` | string | owner/repo. |
| `pr.repo_name` | string | The repository name, without its owner. |
| `pr.base` | string | The base branch. |
| `pr.association` | string | The author's association with the repository, from the Basics read. |
| `pr.cross_repository` | bool | The head branch lives in a fork. |
| `pr.visibility` | string | The repository's visibility: public, private or internal, from the Basics read; empty before it. |
| `pr.requested_teams` | list | The teams a review of the pull request was requested through, as <org>-<slug>. |
| `pr.labels` | list | The pull request's labels, from the Basics read. |
| `pr.address.reviewers` | list | The reviewers the address round answers: on its turn, those it answers, empty on any other; in a workflow, those it would answer. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.address.checks` | list | The failing checks the address round fixes: on its turn, those it fixes, empty on any other; in a workflow, those it would fix. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.address.conflict` | bool | The head conflicts with the base past the conflict window and the address round resolves it: on its turn, it does, false on any other; in a workflow, it would. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.draft` | bool | The pull request is a draft, from the inventory row. |
| `pr.review_requested_me` | bool | A review is requested from the viewer, from the inventory row. |
| `pr.adopted` | bool | The pull request was taken over into the loop. |
| `pr.held` | bool | Rounds on the pull request are held. |
| `pr.muted` | bool | The pull request is muted, by the server clock. |
| `pr.nudge.who` | list | Whom a nudge would address, from nudge.targets. |
| `pr.nudge.why` | string | Why the pull request waits on them, from nudge.targets. |
| `pr.nudge.refusal` | string | The text nudge.prepare would refuse with now; empty when a nudge may go. |
| `pr.last_review.exists` | bool | The viewer reviewed the pull request before. |
| `pr.last_review.verdict` | string | The viewer's newest review state, any state; empty when none. |
| `pr.last_review.decision` | string | The viewer's newest non-comment verdict; empty when none. |
| `pr.merged_at` | timestamp | When the pull request merged; zero when it did not. Test with `> timestamp(0)`. |
| `pr.closed_at` | timestamp | When the pull request closed; zero when open. Test with `> timestamp(0)`. |
| `pr.finished_for` | duration | Working time since it ended, max(merged_at, closed_at), the rule retention uses; zero while open. |
| `pr.ended` | bool | The pull request merged or closed: the GitHub plugin's word that the unit has ended. |
| `pr.head` | string | The head commit's oid. |
| `pr.conflicting_for` | duration | How long the pull request has conflicted with its base, from when the inventory first saw it; zero when it does not. |
| `pr.checks` | string | The checks' rollup on the head: success, failure, pending, error or expected; empty with no checks. |
| `pr.stalled` | bool | The checks stalled on the head. |
| `pr.age` | duration | Time since the pull request opened. |
| `pr.has_workspace` | bool | A linked workspace holds the pull request. |
| `pr.stale` | bool | The stored detail is older than sweep.slow + 2 x sweep.fast. |
| `pr.open_turn` | string | The state of the book's open turn; empty when none. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.open_turn_seq` | int | The sequence of the book's open turn; 0 when none. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.orphan_answer_id` | int | The id of an answer whose turn ended; 0 when none. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.review.action` | string | What the review rules decide: empty, follow-up or complete. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.review.reason` | string | Why the review rules decide so. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.review.observed` | bool | The review rules observe without acting. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.first.due` | bool | A first round is due. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.first.reason` | string | Why a first round is due or not. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.address.due` | bool | An address round is due. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.address.reason` | string | Why an address round is due or not. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.address.observed` | bool | The address rules observe without acting. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.last_interaction_at` | timestamp | The newest comment, review, push or review request, by anyone. Zero when unset; test with `> timestamp(0)`. |
| `pr.silent_days` | int | Calendar days since last_interaction_at. |
| `pr.waits_on_user` | bool | The row waits on the user, per situation.Derive. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `pr.mute.refusal` | string | Why the pull request is muted, such as muted until its state changes; empty when it is not. |
| `pr.fingerprint` | string | What the sweep records as handled until something new happens: recorded heads, review and check fingerprints, skips. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |

### Slack conversations (`slack.*`)

| `slack` fact (provider `slack`) | Type | Meaning |
|---|---|---|
| `slack.channel` | string | The conversation's channel id. |
| `slack.conversation` | string | The conversation's kind: channel, dm or group. |
| `slack.started_by_me` | bool | The viewer started the thread. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `slack.closure_posted` | bool | A closure was posted on the unit, by the extraction or a sent draft's unit.closed. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `slack.live_task` | bool | A live task, neither done nor in the trash and not muted, is attached to the unit. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `slack.items_open` | int | Open items whose unit is this unit. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `slack.items_unowned` | int | Open items of the unit with no assignee, nobody waited on and no basis. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `slack.items_unowned_age_days` | int | Working days, in the configured location, since the oldest unowned item appeared. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `slack.ended` | bool | The Slack plugin's word that the unit has ended: a closure was posted or marked (unit.closed), or its newest stored message is older than plugins.slack.ended_after. |
| `slack.settled_at` | timestamp | The newest update of a settled draft the workflow proposed on the unit, withdrawn ones included; zero when none. Test with `> timestamp(0)`. |
| `slack.fingerprint` | string | The unit, its newest message and the update time of its items. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |

### Briefs (`brief.*`)

| `brief` fact | Type | Meaning |
|---|---|---|
| `brief.goal` | string | The brief's goal; empty when the unit has no brief. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `brief.risk` | string | The brief's risk: low, medium or high. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `brief.kind` | string | The brief's kind, from the provider's taxonomy. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `brief.size` | string | The brief's size; GitHub briefs only. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `brief.tags` | list | The brief's normalized key:value tags. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `brief.missing` | list | The brief intent's extra fields the unit's brief lacks: new or changed since it was written. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |

### Drafts (`draft.*`)

| `draft` fact | Type | Meaning |
|---|---|---|
| `draft.person_waited` | bool | The draft's person is still waited on by its task; true for a draft of no person. |
| `draft.items_owed` | int | Items of the draft still open and held by its person, or unowned for a draft of no person. |
| `draft.items_assigned` | int | Items of the draft that have an owner, someone waited on, or are no longer open. |
| `draft.unit_open` | bool | The draft's unit still exists: its task is neither done nor in the trash, or its unit is stored. |

### Gates (`gate.*`)

| `gate` fact | Type | Meaning |
|---|---|---|
| `gate.kind` | string | The gate's kind: draft, plan, question, post, proposal, trust or step. |
| `gate.name` | string | The gate's name in gates.toml; empty for a gate of no profile name. |
| `gate.status` | string | The gate's status: open, approved, rejected, answered, dismissed or superseded. |
| `gate.delivery` | string | A draft gate's delivery state, such as sending or failed; empty for any other gate. |

### Triggers (`trigger.*`)

| `trigger` fact | Type | Meaning |
|---|---|---|
| `trigger.kind` | string | sweep, event or schedule. |
| `trigger.event.kind` | string | The event's kind; empty unless kind is event. |
| `trigger.event.key` | string | The event's key. |
| `trigger.event.id` | int | The event's id, unique and growing; 0 unless kind is event. |
| `trigger.event.data` | string | The event's data: a person's or an agent's text, such as an intake's note, an answer or the mentions of a sent nudge. **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it. |
| `trigger.fired_at` | timestamp | When the schedule fired; zero unless kind is schedule. Test with `> timestamp(0)`. |

### The configuration (`config.*`)

| `config` fact | Type | Meaning |
|---|---|---|
| `config.cleanup.idle` | duration | The cleanup.idle setting. |
| `config.cleanup.asked_idle` | duration | The cleanup.asked_idle setting. |
| `config.cleanup.workspace_grace` | duration | The cleanup.workspace_grace setting, in working time. |
| `config.cleanup.manual_idle` | duration | The cleanup.manual_idle setting. |
| `config.cleanup.stale_workspace` | duration | The cleanup.stale_workspace setting. |
| `config.plugins.github.nudge.silent_days` | int | The plugins.github.nudge.silent_days setting, in calendar days. |
| `config.tasks.stale_waiting_days` | int | The tasks.stale_waiting_days setting, in working days. |
| `config.drafts.repeat_after` | duration | The drafts.repeat_after setting. |
| `config.turns.per_day` | int | The turns.per_day setting: automated turns on units whose plugin keeps no per-unit count, all workflows together. |

## Draft approval facts

A `[drafts.approval]` rule's `when` reads these, the facts of the draft, plus the facts of the unit the draft is on (a workflow's: its provider's), never `brief.*`, `trigger.*`, `config.*` or a fact marked **Untrusted**, whose value a third party's text, read by the extraction, or the proposing turn itself sets. A rule over the facts of another provider than the draft's unit's does not apply to it. A rule that releases a draft on its own (`send` or `delay`) applies to a comment draft only when its `when` has `draft.delivery == "comment"` as a term of its top-level `&&`.

| Approval fact | Type | Meaning |
|---|---|---|
| `draft.delivery` | string | How the unit's plugin posts the draft: message, reaction or comment. A plugin that posts every text as a comment makes each draft that is not a reaction a comment, whatever its proposer asked for. |
| `draft.proposer` | string | What proposed the draft: workflow:<name> for a workflow's draft.propose, intent:<name> for a turn's or a session's, rule:<name> for a plugin rule's. |
| `draft.effects` | list | The effects the draft applies once sent when the loop named them, a workflow's draft.propose or a plugin rule; empty for a turn's or a session's draft, whose agent names them. A workflow's rules and the configuration's read it; an intent's do not. |
| `draft.purpose` | string | The draft's purpose when the loop named it, a workflow's draft.propose or a plugin rule; empty for a turn's or a session's draft, whose agent names it. A workflow's rules and the configuration's read it; an intent's do not. |

## Workflow steps

The steps a `[[step]]` may name with `use`. `{{command}} workflows steps` prints them with their arguments.

| Step | Unit | Does |
|---|---|---|
| `session.close` | session | Closes the agent session. |
| `workspace.remove` | workspace | Removes the workspace, after its guards. Never with `force`: a manifest that sets it fails to load. |
| `workspace.unregister` | workspace | Unregisters a workspace whose path is gone. |
| `turn.dismiss` | session, github | Dismisses the open turn, recording the reason. |
| `answer.refuse` | github | Refuses a queued answer whose route is gone. |
| `session.mark_dormant` | session | Marks dormant an active session whose process its manager's census no longer lists: the session stays open, and the next turn wakes it. |
| `turn.start` | any | Starts a turn of `intent` on the unit, which the intent must serve; `stage` (`first` or `follow-up`) for a review; `runner` (`session` or `headless`), by default the intent's first runner. On a pull request, caps, the brief, busy and held checks apply as for any automatic round; on any other unit, the workflow's `limits.per_day` and `turns.per_day`. |
| `draft.propose` | any | Proposes a draft in the Outbox: `purpose` (a label), `delivery` (`message`, `reaction` or `comment`), `text` (a template in the workflow's directory, by default `text.md.tmpl`), `reaction` (an emoji name), `items` (`owed`, `resolved`, `unowned`, `unowned_aged` or `all`), `per` (`unit` or `person`), `target` (`unit` or `item`), `effects` (the effects sending it applies), `repeat_after` (the least time after a settled draft of the same person, a duration or a duration of the config such as `config.drafts.repeat_after`) and `repeat_after_sent` (the same after a sent one, or `forever`). Holds while a draft of the workflow, unit and person is open or failed; a run that leaves a person under a delay that ends with time stays pending until it ends. |
| `classify` | any | A headless classification whose output becomes facts: `prompt` (a file in the workflow's directory, by default `prompt.md`) and `schema` (the JSON schema its answer follows, by default `schema.json`). Event triggers only, cached by fingerprint, under `classify.per_day`. |
| `notify.self` | any | A private notification: `text` (a template in the workflow's directory, by default `text.md.tmpl`, over `.Workflow`, `.Unit` and `.Facts`, the unit's facts by name, such as `{{index .Facts "pr.slug"}}`, none once the unit is gone). |
| `task.verb` | task | Runs a verb on the task: `verb` (`next`, `waiting`, `schedule`, `someday`, `reference`, `focus`, `context`, `area`, `done` or `dismiss`), with `until` and `on_activity` for schedule, `on` and `resurface` for waiting, and `set` for context and area. The plan blocks it while the task's state refuses the verb, and `done` and `dismiss` on a task derived from a unit's items, which closes when they resolve. It records no `verb_<verb>` event, which only a person's verb records, so a workflow never triggers another through it. |
| `gate.open` | any | Opens the gate `name` of gates.toml on the unit, a decision that waits for you with the intents gates.toml gives it. |
| `task.set` | task | Sets the task's property `prop` of tasks.toml to `value`, written by the workflow; a property the workflow may not write is refused. |

## Draft effects

What a sent draft applies, once: the names a `draft.propose` step lists in `effects` and an intent lists in `[drafts] allowed_effects`. A turn's own `effects` are on the [language](/reference/language/) page.

| Effect | Unit | Does |
|---|---|---|
| `plan.cleared` | any | Drops the unit's pending plan items. |
| `task.nudged` | any | Stamps the last nudge of the draft's task, or of every live waiting task of its unit when it names none. |
| `unit.closed` | any | Records the closure of the draft's unit, which ends its close task and unwatches it after its warm period; through the unit's plugin. |
| `pr.guidance_handed` | github | Hands the guidance queued on the pull request to the turn, and queues it back when the turn is not sent. |
| `pr.round_sent` | github | Records the round-sent event on the pull request. |
| `pr.reviewed` | github | Writes the plugin mark reviewed: the reviews the round covered, once its review is submitted. |
| `pr.addressed` | github | Writes the plugin marks addressed.*: the reviews, checks and conflict the address round covered. |
| `pr.nudged` | github | Records the nudged event on the pull request, which the nudge refusal reads: when its turn is sent, keyed by the turn so that a turn that ended before its agent started it does not count, or as the GitHub publisher posts its draft. |

This page is generated from the binary's fact registry by `make docs-ref`.
