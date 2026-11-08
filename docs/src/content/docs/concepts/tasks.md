---
title: Tasks and views
description: How what people ask of you becomes tasks, how the lists and projects hold them, and how the review walks through them.
---

{{name}} keeps your work as **tasks**. A task is something you act on, wait for, read later or decide about. It points at the **items** and the **units** it comes from. The lists follow the method David Allen describes in *Getting Things Done*®.

## Units and items

A **unit** is a thing work happens on: a pull request, a Slack thread, a DM topic. Units come from plugins. Two are compiled in: `github` (pull requests) and `slack` (threads and DM topics). Each can be turned off:

```toml
[plugins.slack]
enabled = false
```

The daemon reads the switch when it starts. With a plugin off, nothing of it runs, its actions leave every surface, and its cards render from the last stored snapshot, marked stale.

A task with a unit of a plugin that is off refuses every verb but Dismiss, with the reason naming the plugin. Dismiss moves a task whose units are all of plugins that are off to the Trash, as it does a task derived from items, whose identity is the unit it came from. On any other task that also has a unit of a plugin that is on, it detaches the units of the plugins that are off and the items on them, and the task stays where it is with the rest. When the task's first unit is of a plugin that is on and offers its own dismiss action, Dismiss runs that action, as on any task. A link an item makes to a unit of a plugin that is off is stored, and is left out while the plugin is off.

An **item** is one thing someone needs from someone inside a unit: a `request`, a `question`, a `decision` or a `todo`. It is open or resolved, and says who asked, who must act next and where it came from. An item is what a conversation or pull request asks, extracted; a task is the work you manage, which holds one or more items.

- The GitHub plugin derives items from what it reads, with no model call: a review requested of you, reviews to address, failing checks, a conflict, the fix you wait for. A pull request you follow that asks none of these has one item, `gh:follow`, that waits on whoever the pull request waits on: you when a turn on it asks you something no answer reached yet, when its agent waits with no question or a round waits on you, the loop while it works on it, else its author or reviewers, and nobody when it waits on no one but you, such as your own draft; such a task is owed by nobody. A pending, held or blocked round is a gate, never an item.
- The Slack plugin extracts them from conversations with one model call per unit (see [Slack as a source](/concepts/slack/)).

Once you create or correct an item it is **confirmed**: extraction never overwrites it again. A later extraction that disagrees becomes a proposal, a gate on the unit's task, with Approve and Reject (see [Messages](#messages)).

## From items to tasks

- Each open item that waits on you becomes a task in the Inbox; one you requested, or are assigned while no other assignee is what it waits on, or of a unit you care about, that waits on someone else becomes a task in Next, committed, and the Waiting view lists it while someone else owes its next step. You care about a pull request in scope, adopted or tracked, and about a Slack thread you follow or a 1:1 DM with a person; an open item alone does not make you care about its thread, so an ask between other people never becomes your task. An item a task already holds stays with it while it waits on someone, so a task you merged keeps its items and a task stays open when its item no longer names you; a pull request's own task still drops once you stop following it.
- A plugin decides how its items group into tasks: each item carries a task key, its own id unless the plugin sets one. Every item of a key is an ask of one task. The GitHub plugin keys every item of a pull request on the pull request, so each pull request you follow is one task, the pull request its first unit, and a situation change (a review, then changes to address, then the merge) changes the task's asks, never the task. A key no task holds that names the unit goes to the unit's own task, the task whose first unit it is, open or not, so an open pull request never gets a second task: a done one takes the key's items and comes back into Next, marked changed, only when the pull request asks something new of you. A gate on a pull request whose task is done reopens that task too.
- A pull request's task follows what the pull request asks of you: it moves to Done once every ask it held resolved after the task last opened, such as when the pull request merged or closed, or when it is someone else's, your newest verdict on it approves it and nothing there waits on you, since merging it is its author's. While the loop works on the pull request its ask waits on the loop, so the task stays. A task you opened again stays open until the pull request moves after that. It also ends through the bundled `task-results-ended` workflow once the pull request merged or closed, and is dropped when you stop following it (its `unfollowed` property says when); following it again brings it back to Next, marked changed. A pull request task you dropped yourself stays dropped. The bundled `pr-task-clarify` workflow moves a pull request's task that lands in the Inbox to Next: a review request is a commitment to review, so what it asks of you is in Needs me.
- A plugin proposes the category a new task starts in (`inbox` or `ready`), never a state name: the task takes the first state of that category in `tasks.toml`. It also gives the task the `kind` its manifest names for its units, such as `thread`; the bundled `task-kind` workflow gives a task you made with no unit the kind `capture`.
- When the items of a task resolve, the task moves to Done. A task you clarified never comes back to the Inbox: an item that reopens only marks an open one changed, and a done task comes back to Next, marked changed, only when its unit asks something new of you, an item waiting on you that the task did not hold or that changed after the task closed. A Done you set, while the pull request still shows the ask you closed it over, stays done. A task you dropped stays dropped.
- Closing a thread you started is the `slack-closure` workflow's draft on the thread, once its items resolved; sending it, through its `unit.closed` effect, ends the tasks that hold the thread.

## Who owes the next step

`owed_by.expr` in `tasks.toml` is an expression over a task's facts whose value is `me`, a person's key, or empty when nobody owes it; it is the fact `task.owed_by`, and the task's `owed_by` on the wire. The bundled one makes it yours when the task has an open gate; nobody's when it is closed; otherwise the `owner` you handed it to, yourself or through a session you drive, so Hand to on an ask that waits on you moves it to that person; otherwise yours when an item waits on you, otherwise whoever its items wait on (`task.link_owes`), otherwise its `owner` however set, nobody while an agent works on it (`task.turns_running`) or when its units have open items and none waits on anyone, such as a quiet pull request you follow, and yours when it has none:

```toml
[owed_by]
expr = '''
task.gates_open > 0 ? "me" :
task.category in ["done", "dropped"] ? "" :
task.prop.owner != "" && task.prop_by.owner == "person" ? task.prop.owner :
"me" in task.link_owes ? "me" :
size(task.link_owes) > 0 ? task.link_owes[0] :
task.prop.owner != "" ? task.prop.owner :
task.turns_running > 0 || task.items_open > 0 ? "" : "me"'''
```

`task.owed_since` is since when: the oldest of its items that wait on whoever owes it, else, for you, its oldest open gate, else the task's creation. `task.check_on` is the task's `check_on` property, else, when the items the task holds wait on someone else and not on you, `tasks.stale_waiting_days` working days after the oldest of them; a gate open on the task, its own nudge's draft included, leaves it as it is. The bundled `slack-stalled-nudge` workflow nudges whoever owes a Slack task once its check date passed, listing every open item that person owes on the thread, one draft per person and thread, at most once per `drafts.repeat_after`: sending it writes `last_nudge` on every task that holds one of the items. The bundled `task-autofocus` workflow stars a task past its due date, and one whose items wait on others past its check date with no nudge since; a star you cleared stays cleared. It marks the stars it sets with the `autostar` property, and `task-autofocus-clear` takes off such a star once neither reason holds; a star you set is never taken off.

## Create a task

One route, `POST /api/tasks`, creates every task you make: a title alone captures it in the Inbox, as `n` in the terminal does. `{{command}} task new --title <title> --brief-file <path> --prop <name>=<value> --repo <owner/name>` creates one with the rest, and prints it as `task:<id>`:

- the file's markdown is the task's brief record, in its `text` field;
- each `--prop` is a property `tasks.toml` declares, parsed by its type in the daemon's time zone, such as `priority=high`, `due=2026-11-02` or `size=30m`; a property the profile does not declare, one its writers leave you out of, or a value its type refuses, creates nothing;
- `--repo` is where the task's work runs: a later `start` on the task without `--repo` works in that repository.

A task created from an agent's session is written by `agent`: its brief and properties are untrusted, and a property agents do not write is refused. The bundled properties agents write are those you write, plus those only agents write; a property you write is written only from a session you drive, and a session the loop runs a turn in is refused it with `403`.

## Units attached to a task

A task's units come in order: the unit it was captured or derived on, then the units attached to it, in the order they were attached, then any unit only its items name. The task itself is never one of them.

What a turn on a task produces is attached to it, when the turn's intent asks for it:

- With `record.attach_url = true`, a turn that records `done` with an outcome `url` naming a unit, such as a pull request, attaches that unit. The address is resolved by the plugins' units; a branch name never attaches anything.
- With `drafts.attach_posts = true`, the unit of each post the turn's agent makes, sent at once or once approved, is attached; a draft on the task posted to a channel or a person attaches the thread its post starts. Reactions and edits are not posts.

Each plugin says when one of its units has ended: a pull request merged or closed (`pr.ended`); a Slack unit with a closure posted or marked, or quiet for `plugins.slack.ended_after` (7 days) (`slack.ended`). The bundled workflow `task-results-ended` marks done a task that holds a pull request, whatever its own kind (a Slack ask merged with the pull request's task included), and a task you captured once a turn ran on it, such as a start, when it has a unit and every one of them has ended and no gate waits on you there (`task.link_ended && task.gates_open == 0 && ("pull-request" in task.unit_kinds || task.captured_by_user && task.turns > 0)`): a pull request's task that also holds a thread still open stays open. An open step whose subject is gone does not hold it: a workflow's `[gates.withdraw]` closes it first, as Review the diff's once its pull request merged. A task a gate opens on a unit no task holds takes the kind the unit's plugin gives its tasks, so a gate on a pull request with no task, such as a merged one, makes it the pull request's task, out of the Inbox. A task you captured on a pull request to keep track of it, with no turn on the task itself, stays open. `task.verb` never closes a derived task while it holds an item that calls for it: a workflow closes one by resolving its items with the `items.resolve` step, as the next section says.

### Closing a task its source shows done

A task derived from a unit's items closes when its items resolve. Besides the extraction, two things resolve them from what the source shows:

- An item's `review` and `merge` links settle it from the current state of the pull requests they name, as the [glossary](/architecture/glossary/) says: once every one has settled, it resolves `done` when one did, else `moot`; an item you reopened or edited since stays open. A pull request an open linked item waits on is read from GitHub at the background's priority when the GitHub plugin has not seen it for `sweep.slow` (an hour), even one the plugin does not follow; a merged one is never read again, one that ended and still settles nothing is read once a day, and one GitHub will not show waits a day before the next read. A close counts while the plugin saw the pull request closed within `sweep.slow`; an older report is read again first, since a reopening records no event. One read this way that your scope covers, such as one older than the sweep's window, is followed like one the sweep finds.
- The `items.resolve` step resolves every open item a derived task holds, as the workflow (provenance `workflow`, evidence of kind `workflow` anchored at `workflow:<name>`), with its `resolution`; an item you confirmed becomes a proposal on the task instead. It refuses a task that is not derived or holds no open item, and posts nothing.

The Slack plugin's layer bundles three workflows over the `items.resolve` step, one per signal, each on an open `thread` task with open items, and each a plan gate that waits for you and asks the workflow's summary. The two that read a link signal offer only on a task of one unit, since a signal on one thread says nothing of another's items. They exclude each other in the table's order, so a task gets one offer at a time:

| Workflow | Signal | Resolution |
|---|---|---|
| `slack-done-reacted` | `task.link_signal.asked_reacted`: whoever asked checked a later message, such as your answer | `done` |
| `slack-done-replied` | `task.link_signal.replied_last`: you posted last, after an answer to each ask you made, and nobody spoke since, for 3 days | `answered` |
| `slack-done-quiet` | `task.link_ended`: every thread of the task ended, a closure posted or marked, or quiet for `plugins.slack.ended_after` | `moot` |

Skipping one is final until the task changes (`key = ["task.fingerprint"]`). To let one close on its own, set its plan gate's mode in the configuration:

```toml
[[workflows.slack-done-reacted.gates.approval.rule]]
when = 'gate.kind == "plan"'
mode = "run"
```

The signals come from the thread, which anyone in it writes: with `run`, a check mark from whoever asked resolves your items with no look from you.

Resolving the items of a thread you started can make `slack-closure` propose its closure, a draft that waits for you like any other.

## One piece of work in several tasks

The same work can reach you from more than one source: a Slack ask to review a pull request, and the pull request's own task; a DM and the channel thread it continues. Four verbs deal with it, each with another task. Merge and Relate pick it from the open tasks in the cockpit's or the terminal's palette, narrowed by title; Split and Unrelate act on one of the tasks the task's page lists under Merged from or Related.

| Verb | Does |
|---|---|
| Merge | Folds one task into the other: the survivor takes the other's items, units, gates, held drafts, messages, wake and properties. |
| Split | Gives a task merged into this one back, as it was before the merge. |
| Relate / Unrelate | Links two tasks as distinct work. Nothing moves; each lists the other. |

**Who runs them.** You, and a session you drive, run all four. A workflow asks for a merge or a relation through the verb's gate, "Merge these tasks?" or "Relate these tasks?", which says which task folds into which; the asking workflow's approval decides it, as every gate's is decided ([What waits for you](/concepts/approval/#gates)). Approved by you, the verb runs as yours; released by the workflow's rule, it runs as the workflow, and the timeline names it "the loop, by workflow <name>" (`attribution.via_workflow` in `tasks.toml`). A pair you took apart, by rejecting its gate, splitting it or unrelating it, waits for you whatever the workflow's approval says (`task.undone`), also once either task is merged into another; taking a pair apart supersedes its open merge and relate gates, and one a rule or a window releases later runs nothing. A workflow never splits or unrelates, and a plugin runs none of the four. A merge takes two open tasks, never a project, a draft or a message.

**The survivor.** The task you shaped by hand stays: one you clarified, moved or filed in a project, yourself or through a session you drive, or whose properties you wrote (`task.clarified`); an agent's property write, such as a brief's, does not count. When neither or both were, the older one stays. The other task's id keeps resolving: its address reads the survivor, through any chain of merges.

**What a merge keeps.** Nothing of the folded task is lost or duplicated. Its items, resolved ones included, its units and its gates move over; open proposals that name it are superseded, since the pair they asked for is done. Its timeline, chat and live session are reachable from the survivor, and its dossier stays open with the survivor, read-only to the survivor's sessions. The survivor's page lists the tasks merged into it ("Merged from"), and the related ones.

**Sync does not undo it.** A task that holds a merged task's items keeps them, and a unit that syncs again, reopens or gains a sibling item feeds the survivor, never a new task. A merged task's items settle unit by unit: the survivor resolves only when no item of any unit is open.

**Split is by unit.** The units of the folded task go back with the items on them; items that arrived on the survivor after the merge stay where they are. With nothing changed since, the folded task comes back exactly as it was. A task merged into the folded one earlier comes back inside it.

### Merges and relations a workflow asks for

Five bundled workflows ask for merges and relations. Each opens the verb's gate and nothing else, and its `[gates.approval]` says which run at once:

| Workflow | When | Asks for | Its approval |
|---|---|---|---|
| `task-fold-linked` | a task that holds no pull request (`task.unit_kinds`) and whose items' review or merge links target exactly one unit (`task.link_target_units`), held by exactly one task (`task.link_target_tasks`); a sweep, so it holds whichever task came first | merge with that task | released at once |
| `task-relate-linked` | a task's items link the task of a pull request, and it is not the fold's case: an ask about several pull requests, or a task that holds a pull request of its own | relate with each of those tasks, in one pass (`each`) | released at once |
| `task-correlate-merge-linked` | the judgment says same work, a link target or a cited unit also ties the pair (`task.linked_candidates`), the task holds no pull request and does not link the candidate's | merge with that candidate | released at once |
| `task-correlate-merge` | the judgment says same work, the task does not link the candidate's pull request, and only a person ties the pair or the task holds a pull request of its own | merge with that candidate | waits for you |
| `task-correlate-relate` | the judgment says related work, and the task does not link the candidate's pull request, which `task-fold-linked` and `task-relate-linked` handle | relate with that candidate | released at once |

Two tasks that each hold their own pull request are never merged at once: an ask that links several pull requests is related to each one's task, and a merge the correlation judges between two pull requests' tasks waits for you. You can still merge them yourself, as a stack of pull requests that is one change.

`task-correlate` judges whether a task just created, or whose items just gained a link, is the same work as one of its **candidates**, related work, or neither. The candidates are the open tasks created within `tasks.candidate_window` (14 days) of the task, ranked by what they share, and cut to `tasks.candidate_cap` (5). A unit one task holds that the other's items link to scores most, then a link target in common, then each person in common (requester, assignees, whom it waits on). A task with no candidate costs no call. The judgment is a model call (`classify` step) that reads the candidates' titles, notes and a few item summaries as data and answers `same`, `related` or `none`, with the candidate's id. A sub-step of a larger task, a follow-up of the same change or another deliverable of the same launch is related work; the same person, week or words about another system is neither. What it reads is untrusted text: it only picks which workflow asks, and must name one of the candidates; the rule that releases a gate reads the gate alone.

To have every merge wait for you, pin a rule to merge gates in the configuration:

```
[[workflows.task-fold-linked.gates.approval.rule]]
when = 'gate.kind == "proposal" && gate.name == "merge"'
mode = "wait"
```

and the same for `task-correlate-merge-linked`, or `task-correlate-relate` and `task-relate-linked` with `gate.name == "relate"`.

Limits keep it cheap and quiet:

- one call per task and per set of candidates, never again while the titles, notes and item summaries it read, the prompt and the schema stay the same: opening or moving a task does not call again;
- the day's calls count against `classify.per_day` (50), shared with every classify workflow;
- a gate you reject, a split or an unrelate you run is remembered: the pair is not asked for again until an item of either task changes (`task.refused_pairs`), and from then on it waits for you (`task.undone`);
- an open merge gate ends with its tasks: when either closes, is merged elsewhere or is split.

To turn the correlation off, which turns off the three workflows acting on it:

```
{{command}} config set workflows.task-correlate.enabled false
```

Restart the daemon to apply it. `tasks.candidate_cap = 0` also stops it, and `task-fold-linked` turns off the same way, with `workflows.task-fold-linked.enabled`.

## The timeline and the records

A task's timeline is its own events and those of the units it holds, every one kept forever, newest first, but the loop's bookkeeping that only triggers workflows (`task_created`, `task_linked`, `classified`) and the merge records: `GET /api/tasks/<id>/timeline` pages it by each event's `seq`, and its `kinds` parameter keeps the kinds asked for. With `as=nodes` it answers the events folded into facts by the profile's `timeline.toml`, as the task page shows them (see [The timeline](/concepts/timeline/)).

The open tasks that hold a unit or one of its items are at `GET /api/units/<provider>/<key>/tasks`, one read by the unit whatever view lists them, oldest first with children in their sibling order, but those whose units all belong to a disabled plugin; the cockpit's unit detail shows them from it. The unit's detail, `GET /api/units/<provider>/<key>`, carries its items and, by item id in `item_ties`, whom each waits on (the user marked `me`) and the task that holds it, among its items or those it resolved, an open one before a closed one and never one a disabled plugin hides, or none.

A record is a typed note on a unit or a task, of a kind `records.toml` declares: the JSON schema under `records/` its data follows, who may write it, and the task properties its fields set. A unit keeps one record of each kind, the newest; the timeline shows it as a `record` event. The bundled kinds are `brief`, `report` and `decision`. A brief record sets the task's `risk` from its `risk` and the task's `effort` from its `size`, when its writer may write the property: `size` stays the attention you give the task. A task's brief is its own brief record, else the brief of the first unit it holds that has one, so a pull request task's brief is its pull request's.

## Start new work

`{{command}} start --repo acme/api <brief>` creates a task from the brief through the same route, prints `task:<id>`, then starts an agent session on it. `--brief-file <path>` reads the brief from a file instead. The manifest's `[target] title` makes the title, bundled as `{{title 72 .guidance}}`: the brief's first line without its markdown heading marks, cut at a whole word within 72 characters. The whole brief is the task's brief record, which the session reads. `{{command}} start task:<id>` starts one on a task you created before, such as one `task new` made, and the cockpit offers Start on such a task. Start runs only on a task you captured; a derived task is refused. Starting work commits the task: while a turn someone started by hand is open on it, or on a unit it links (`task.turns_open_by_hand`, which leaves out the chat's), the bundled `task-commit-on-start` workflow moves it from the Inbox, Scheduled or Someday to Next, once per turn: it marks the newest such turn's send (`task.turn_by_hand_at`), so moving the task back while that turn is open keeps it where you put it, and a later turn commits it again. A done task stays done, and In loop lists it while the turn runs.

- **With `--repo`**, the session works in a fresh worktree of that repository, branched from its default base and named after the task's title and short id. A repository no manager knows is cloned into the matching project's `projects_dir`; an existing folder there must be a checkout of that repository. `--manager` picks the workspace manager, and `--parent` the worktree the new one goes under in Orca (the one the command runs in by default). The project's `workspace_managers`, `agent` and `agent_home` apply, to the clone's trust and to the session alike. The manager is chosen before anything is cloned, so a start it refuses clones nothing. A failed start prints the command that retries it on the task with the same flags.
- **Without `--repo`**, the session runs with no checkout, in the neutral repository, for a message to post or an answer to find.

The `start` intent records `done` with the pull request's or the message's URL, which attaches it to the task (`record.attach_url`); posts it makes at once attach too (`drafts.attach_posts`). While a pull request attached to a task you captured and ran a turn on is open (`"pull-request" in task.open_unit_kinds`), the bundled `start-review-diff` workflow opens the `review-diff` gate on it, which waits for you. Its card reads as `gates.toml`'s text words it over the task's facts and its pull request's, such as "Review the diff of acme/api#12, 3 files changed", shows the pull request's title and reference linked to it, and says what each answer does: Looks good marks the diff reviewed and posts nothing, Dismiss closes the step. It opens once per set of attached units, so dismissing it keeps it closed until another unit attaches. Once no pull request of the task is open, the workflow's `[gates.withdraw]` withdraws the step at the start of the next sweep, recording why on the task's timeline, and the task closes when its units have ended, as the next section says. When the cockpit's Start runs, it takes no `--repo`: the turn reuses the repository an earlier start on the task recorded, else it runs with no checkout.

## Runs

Every turn on a task is one of its runs. The task's page lists them newest first, each with its intent and state, the workspace manager, workspace and worktree it ran in, its session and that session's state (active, dormant or closed), and the command that resumes it. A run offers the actions it can take:

- **Open workspace** brings its workspace on screen in the manager it ran in.
- **Enter session** brings its session on screen to type in it. A dormant session, the run's own, is woken first in its own manager, with the policy, trust and session name a turn of the run's intent launches with.
- **Transcript** reads what the person typed and what the agent answered in the session, page by page.

A headless call has no workspace or session, so it offers none of them. `{{command}} open` on a task focuses the workspace of its newest run that ran in one.

### Attach a session

`{{command}} task attach task:<id>`, run from an agent session a workspace manager started, makes that session a run of the task: the daemon starts the turn of the intent holding the `attach` role (the bundled `attach`) on it by hand, and the session takes the task as its unit, whatever unit it had before. Every attached session is a run of its own: attaching again from the same session replaces only that session's earlier attach (the role's `PerSession`). What the session posts, drafts or records from then on lands on the task. The `attach` intent has no `[record]`, so its turn owes no record and the Stop hook never blocks the session for one. Outside a workspace manager's session, the command refuses with the `attach_needs_session` notice and changes nothing.

## Lists are categories

The category is the commitment, a decision stored on the task; who has the ball now is derived from its gates, turns and links (`task.owed_by`), and the views read it. A task in the `ready` category is committed: you, an agent or the person it waits on will do its next step.

| State | Category | Means |
|---|---|---|
| `inbox` | `inbox` | not clarified yet |
| `next` | `ready` | committed: you or an agent will do it |
| `waiting` | `ready` | committed and parked on someone, by hand: who, since when, the last nudge and a date to resurface |
| `scheduled` | `deferred` | snoozed until a date, or until new activity on one of its units |
| `someday` | `deferred` | maybe later |
| `done` | `done` | finished |
| `reference` | `done` | kept to read, no action |
| `trash` | `dropped` | dismissed |

A task is open while its category is neither `done` nor `dropped`. Beside its state, a task has properties: the bundled ones include a **focus** star, **contexts** (labels such as `@decide`), an **area** and a **due** date. A property's `role` in `tasks.toml` names the mechanism of {{name}} that acts on it, so Go reads the role and never the property's name: `area` (the area filter of every view and of the counters reads it, the area verb sets it, a new project takes it from its first task), `contexts` (the contexts filter reads it, the context verb sets it, a derived task takes its item's), `star` (the focus verb toggles it between its first value and its second), `deadline` (a derived task takes its items' deadline, an Outbox draft its gate's window) and `promise` (acknowledge sets it, and `task.promised` reads it). One property takes each role; a user layer that gives a role to another property moves it. A task's **parent** is the project it belongs to, and its children keep their order.

## Views

A view is a list of the profile's `views.toml`: a `when`, a condition over a task's facts (or a gate's, with `over = "gates"`), and the tasks or gates it holds, with its `label`, its `summary` (one line on what it lists, which the cockpit shows on the view's item and under its heading, and the help beside its key), its `empty` (what it shows when it lists nothing, the top-level `empty` of `views.toml` when absent, with the view's label as `{{.label}}`), the `key` that jumps to it, the fact it groups by (`group`) and those it sorts by (`sort`). A view lists its tasks the oldest first, a parent's children in their sibling order, then by its `sort`; `hide_disabled` leaves out the tasks whose units are all of a disabled plugin; `remove_clean` offers to remove at once the workspaces its tasks' links hold, keeping each one whose checkout holds anything not published, with why; `layout` is how the cockpit draws it: `list` (the default), `board` or `outbox`. The area and the contexts you pick narrow every view and its count. The bundled views:

| View | Lists |
|---|---|
| Inbox | what nobody has clarified (`task.category == 'inbox'`) |
| Needs me | a clarified task whose next step is yours: an open gate, or your own action (`task.owed_by == 'me'`); a sequential project's next action first (`-task.sequential_next`) |
| Waiting | a task someone else owes, grouped by `task.owed_by`; one nobody owes while an agent works on it is In loop's |
| Chase | a task someone else owes whose `check_on` passed with nothing moving since, grouped by `task.owed_by`: time to ask them |
| Fits now | a clarified task whose next step is yours and whose `size`, the attention it takes, is at most half an hour, the smallest first |
| Blocked by me | a task whose asks wait on you, grouped by who asked (`task.asker_names`), the oldest ask first (`task.ask_age`) |
| Stalled | a task a source says stalled (`task.link_signal.stalled`), or, when no link reports `stalled`, one someone else owes whose `check_on` passed with nothing moving since (`task.since_movement`) |
| In loop | a task a source's loop holds (`task.link_signal.in_loop`), or one an agent works on, done ones included (`task.turns_running`) |
| Approved | a task a source says is ready (`task.link_signal.ready`); it offers Remove all clean |
| Snoozed | a task asleep on a wake |
| Focus | an open starred task |
| Projects | the open projects |
| Someday | the `someday` state |
| Logbook | the `done` category, the latest moved first (`task.since_movement`) |
| Done | a done task whose links still hold a workspace on disk (`task.link_signal.workspace`), to remove; it offers Remove all clean |
| Outbox | over gates: the drafts waiting on you, and those approved whose delivery is under way or failed, so you can retry them |
| Projects to review | an open project past its review, or, unless in Someday, with no ready child whose next step is yours (`task.review_due`, `task.children_mine`) |
| Review | lists nothing: the clients walk the review's steps |

The menu bar's badge adds up the views `[badge] views` names, Inbox and Needs me in the bundled profile: an open gate makes its task yours, even a done one, so it shows in Needs me, and a task still in the Inbox counts there once. A view whose condition reads only the task's own fields (its state, category and properties), and not `now`, pages since a position: a page lists the tasks written after it and those whose placement, or a unit whose row, brief or links, moved since; any other view, such as Needs me, which reads gates, wakes and items, is served whole each time. Your own `views.toml` changes a bundled view's keys, keeping the ones it leaves out, such as its `summary`, or adds views after the bundled ones, in the order it writes them; a view of your own needs its `label` and its `summary`.

A plugin reports per unit the link signals its manifest declares (`[[source.signals]]`): the GitHub plugin reports `stalled` (an open pull request it follows whose situation lasted past its `github.stalled.*` threshold), `in_loop` (an open one a review workspace holds), `ready` (one whose work is approved) and `workspace` (one a workspace of which remains on disk, open or not). A task reads each declared signal as the fact `task.link_signal.<name>`, on when it is on for any unit the task links, under the host's own state: a round, a run or a mute on the pull request turns `ready` off and its own situation decides `stalled`. A view or a workflow naming a signal no plugin declares is a load problem. `task.since_movement` is the time since the task changed or a unit it links last moved: its situation began, or a run changed it. `task.link_signals` lists the signals a task's links report, on or off. The bundled profile lets a source that reports `stalled` keep its own staleness: Stalled, `task-autofocus` and `task-check-on` read the check date only when `'stalled'` is not in `task.link_signals`, so Stalled lists a pull request only when GitHub says it stalled. The bundled `task-check-on` workflow gives a task someone else owes, when no unit it links says whom it waits on or a person handed it to its owner, and no unit keeps its own staleness nor an agent works on it, a `check_on` two days ahead, so Stalled lists it once that date passes with nothing moving.

The states and the task properties are the profile's `tasks.toml`. Each state has a category (`inbox`, `ready`, `active`, `waiting`, `deferred`, `done` or `dropped`), which is all {{name}} reads of it: a task proposed in a category takes the first state of it, in the order the file declares them, and a state of your `tasks.toml` comes after the bundled ones. The store keeps each task's category beside its state, and the lists read the category. When a reload of the profile moves a state to another category, the daemon gives the stored tasks of that state the new one; a state stored tasks hold that no layer declares any more is a profile problem the health shows, naming `states.<name>`, and those tasks keep the category they had until you declare it again or move them. Each property is the fact `task.prop.<name>`, typed from its `type`, and who last wrote it the fact `task.prop_by.<name>` (`person`, `workflow`, `agent` or `plugin`, empty while unset), and says who may write it: a property an agent or a plugin writes, such as `risk`, is untrusted, so no approval rule that sends or runs reads it. An `int` or a `duration` property of 0 is unset.

The words of the verbs come from the profile's `tasks.toml`: each verb's label is its button, its summary the button's tooltip and its input the question it asks first. A verb's `from` are the states it moves a task from, every state when left out, and its `to` the state it moves it to. A verb's `offer`, a condition over the task's own facts (`task.prop.kind`, `task.state`, `task.unit_provider`), says where a task offers it: `offer = 'task.prop.kind != "capture"'` on `delegate` leaves Delegate off captured tasks, and the daemon refuses it there too. A workflow's `task.verb` step is not bound by it, but no actor other than you, or a session you drive, runs a verb that would end a task an open gate with a `closes_with` intent holds (see [Messages](#messages)). A verb that moves an open task to a done or dropped state withdraws the held drafts it carries, but those of the purposes its `keep_drafts` lists (see [What waits for you](/concepts/approval/#the-outbox)). A verb's `handler` is what the loop runs besides its move (`project`, `reply`, `approve`, `promise`, ...), bound by that name and never by the verb's. A profile may declare verbs of its own, named in lowercase letters, digits and hyphens: one with no handler only moves the task, and a workflow can react to its event; one whose handler asks for a value (`waiting`, `schedule`, `project`, `context`, `area`, `delegate`, `reply`, `promise`) needs its `input`. A verb's `by` says who may run it, per actor (`person`, `workflow`, `agent`, `plugin`): a condition over the task's facts, such as `workflow = 'task.prop.kind == "capture"'`, or `"gate"`, which opens the gate the verb's `gate` names in `gates.toml` for you to decide, once per proposal while it is open, and leaves the task as it is; approving it runs the verb as yours. A `by` condition reads the task's own facts (`task.id`, `task.state`, `task.category`, `task.unit`, `task.unit_provider`) and its properties; naming another fact is a load problem. A verb asked from an agent's session, whose requests carry the session's handle, runs as `agent` through that session; any other as `person`. The daemon runs an agent's verb only from a session you drive: one the loop never typed into, or you prompted since it last did, with no turn the loop sent running in it; any other is refused before its `by` is read, and the task page offers it nothing. An actor `by` leaves out may not run the verb; the bundled profile lets you and your sessions run every verb (`agent` has what `person` has) and lets workflows run next, waiting, schedule, wake, focus, done and dismiss. A profile that restricts `person` on a verb restricts `agent` too, or a session you drive keeps the bundled permission. Each verb's event records the session it came through, and the task page's timeline names it with `[attribution] via_session` of `tasks.toml` ("you, via session …"). Every verb run on a task that changes it is a `verb_<verb>` event, by its actor, that a [workflow](/concepts/workflows/#react-to-a-verb) can react to, but one whose handler is `seen` and the verbs on an Outbox draft (approve, edit, retry, dismiss), which are the draft's own. A task's events, and every event, turn, record, answer, plan item and brief of a unit linked to a task, are kept forever: retention never reaches them.

- **Changed since you saw it.** New activity on a unit by someone else, after you last opened the task, marks changed a task parked on someone, a deferred one or an open one out of the Inbox that has an owner other than you, a date to check on it, or an item waiting on someone else, and matches the wake of one scheduled until activity; it never brings a task back to the Inbox. Opening or moving the task clears the mark.
- **Snooze.** `s` schedules a task until a date, until activity, or both: it puts a wake on the task, one per task, which remembers the state the task had. A wake matches when its date passes, on activity, or once its condition over the task's facts holds (`task.wake_until` and `task.wake_when` read it); the loop then records `task.woke` on the task, and the bundled `task-wake` workflow runs the `wake` verb, which brings the task back to that state. Moving the task by hand drops its wake. Muting a pull request is a wake on its task: for a while, or until whom it waits on (`task.link_owes`) changes, or a review workspace takes or leaves it, or its work becomes approved (`task.link_signal.in_loop`, `task.link_signal.ready`); unmute drops it and brings the task back at once.
- **Waiting.** `w` moves a task to Waiting, optionally naming whom it waits on, its `owner`, and a date to check on it, its `check_on`.
- **Triage.** Acknowledge tells the asker you are on it: given a time (`17:00`, today, or tomorrow once it passed; a date, 18:00 that day), it sets the task's `promised` property and the bundled `task-acknowledge-reply` workflow proposes a reply saying you are looking into it, by then; given none, `task-acknowledge-react` proposes an :eyes: reaction on the ask. Either moves the task to Next. Not mine moves it to the trash, and `task-not-mine` proposes a reply pointing the asker elsewhere, for you to edit before you approve it. Hand to moves it to Waiting on the person you name, like Waiting, and `task-check-on` gives it a check date two days ahead, so Chase lists it once that date passes with nothing moving. Every draft these propose waits for your approval in the Outbox. `task.ask_age` is the age of the oldest open ask a task holds that waits on you, `task.askers` who asked it, and `task.asker_names` their names.
- **Extraction health.** A plugin reports per unit what its extraction has not read yet; `task.extraction_lag` is how long the oldest such message on a task's units has waited, and a task's row shows it (`unread 12m`), since a missed request is worse than a noisy one.

Approve (`a`) and delegate (`d`) move nothing themselves: on an action task with units they run that unit's action, such as Run now, a review or address round, or Act. Delegate also runs on any other task about its units, such as one you captured on a unit, and takes optional guidance and a mode: `headless` (Draft for me, which proposes drafts for the task) or `session` (Hand to an agent). Without a mode the action picks its own, headless when it can. The task says upfront whether a session can run on its unit, and why not when it cannot; a unit with no placement runs its session in a neutral directory with no checkout. A session started from a task keeps the task and its items, so the drafts it proposes serve that task.

Reply (`r`) writes a draft of your own on the task's first unit, a reply in its conversation (a comment on a pull request) or a reaction, tied to the task and its items. Send posts it at once, as your approval of it; Save as draft holds it on its gate for your approval. The verb's `send` in `tasks.toml` (`now` in the bundled profile) picks which is the default: the daemon serves it in the catalog, and the task page's composer, the unit composer, the terminal prompt (Enter does the default, Tab the other) and a notification's order of Reply and Send reply all follow it. A reply that names neither takes it too. A reply that goes in a thread whose conversation can also show it carries a broadcast, as its publisher places it when it is proposed: the task page's composer then shows **Also send to the conversation**, checked as the publisher would place it, and both buttons send what it says as the verb's `broadcast` argument (`true` or `false`; `broadcast` `on` or `off` on `POST /api/drafts`). The daemon asks the publisher on the single task's page only, never on a list, and keeps the answer for a minute. A reply in no thread ignores the argument. The verb is the one whose `handler` is `reply`, whatever the profile calls it. From a terminal, `{{command}} outbox new <unit|message> <markdown>` writes one on any unit. The full key list is on the [Keys](/reference/keys/) page.

## Facets

A facet is a field every client filters, groups and sorts a list by. A task has facets from three layers, which the daemon joins and serves with the task: its own facts (source, state, category, whom it waits on, who asked, its placement, when it last moved, how many gates it has open, its link signals, its enum properties), its brief (type, risk, size, tags and the brief intent's enum extras), and the facets of the rows of its units, whatever plugin emitted them (a pull request's checks and situation, a conversation's waits-on).

`where` is the reserved facet for where a unit lives: a pull request's `owner/repo`, a Slack channel. Every source plugin emits it for its units, and `where:wloop` finds `acme/wloop`; a view sorts its unit column by it. `task.placement` is where a task's work runs (its workspace's repository), and `task.moved_at` is when the task, or a unit it links, last moved.

`views.toml`'s `[facets.<id>]` tables declare the task facets: a `fact` (a fact of the task, or a field of a record kind such as `brief.risk`), a `label` and the mechanics (`group`, `sort`, `rank`, `match`, `buckets`), plus `merge`, `units`, `strip`, and `show`, `show_values` and `label_prefix` (see [Where a facet shows on a row](#where-a-facet-shows-on-a-row)). A facet with no declared order or labels takes them from `tasks.toml` (a state's or an enum property's values). `merge` says how a task's value joins its units' rows' values: `union` keeps both, the task's first; `rows-first` takes the task's only when no row has one, so a hand-made task still has a "Waits on" and a task with a Slack unit shows the unit's; `own` keeps the task's alone. `units` is `all` (every unit contributes, in unit order) or `primary` (the first alone). A facet with `strip = true` is what a task view's board groups by when nothing is picked (`turn` in the bundled profile). `[lens]` holds the words of the rows and the list toolbar, and the top-level `default_sort` what the sort menu calls a view's own order. A view's `group` must name the `fact` of a facet.

### Where a facet shows on a row

Every list, of tasks or of a plugin's rows, draws its items with one row, and a board draws them as cards of the same row: a title, then parts filled from the item's facets. A facet's `show` names the part its values show in: `ref` (where the unit lives, before its own label), `meta` (who opened it, its contexts, its brief's type, size, risk and tags), `flag` (a state worth a glance), `who` (whom it waits on), `kind` (its situation or state), `age` (how long since the facet's instant; only the first such facet of an item shows, one time) or `aside` (its workspace). The table form lists the values a part shows, `show = { flag = ["paused", "muted"] }`; `show_values = "first"` shows an item's first value alone, `label_prefix` goes before each shown label (`@` before a context, `by ` before an author) and never in a group's head. A facet without `show`, or with `show = ""`, is for the lens alone, so a user profile's `ui.toml` takes a plugin's facet off its rows with `show = ""`. The value `none` and a time facet's item without a time never show. Each piece is a button that adds its `field:value` to the filter, so a click keeps the row in the list; Shift groups by its field and Alt leaves the value out.

A task row shows what `views.toml` places: a plugin's facet copied onto a task shows on it only when `views.toml` declares that facet with a `show`. The bundled profile places a task's `state` in `kind` (the cockpit draws it as the row's glyph, the terminal as a word), `moved` in `age`, `waits-on` in `who`, and its source, contexts, asker, area and brief fields in `meta`; the area is the `prop.area` facet, which reads `task.prop.area`, so a profile that names its area property otherwise names it in that facet's `fact` too; its units' labels are its ref, each a button that filters by the `where` value it names (the bundled task facet `where`, which takes its rows' value first and the task's placement otherwise), and its primary unit's reason fills the row's why. A plugin's rows show what the plugin's `ui.toml` places (`[facets.<id>] show`, `show_values`, `label_prefix`, `row_class`), which a user profile's `plugins/<id>/ui.toml` overrides; where two plugins declare a facet of one id, each one's rows keep its own placement. `row_class` gives a row holding a value a class the cockpit colors, as GitHub's `author` marks your own pull requests and its `is` a stalled one. The `[lens]` row words say what no facet carries: the flags, the facet buttons' tooltips, a pending round's countdown in both clients (`countdown_sending`, `countdown_seconds`, `countdown_minutes`), a plugin view's empty list (`list_empty`) and how a row's tooltip joins its strip, whom it waits on, its situation and why (`tip_named`, `tip_kind`, `tip_why`).

### The strip

A row's strip, the hue of a task's glyph and of the dot before whom it waits on, comes from its own provider's strip facet: a task's from the facet `views.toml` marks `strip`, a plugin row's from the one facet its manifest marks `strip`. Each declares its own values, rank and hues: `colors` in `views.toml` or the plugin's `ui.toml` gives each value one of the strip's hues, `me`, `author`, `reviewer`, `loop` or `nobody`, matched regardless of case, and a value it does not name takes `nobody`'s. The bundled task strip is `turn`, over the fact `task.turn` with `merge = "own"`: `me` when you owe the next step, `others` when someone else does, `nobody` when no one does. It follows who owes the task, the fact the views select by (`task.owed_by`), so a task in Needs me reads your turn; the row's who shows the people its units' rows wait on, which may differ, such as a draft you owe on a pull request that waits on its author. GitHub's strip is `wait` (`me`, `loop`, `reviewer`, `author`, `nobody`); Slack's is `turn` too (`me` when a conversation waits on you or on your closure, `others`, `nobody`), with its situation in `situation` and how long it has waited in `age`. The dot is a button that filters by the row's own strip value, so a click keeps the row in the list. A row whose provider has no strip facet, or that holds no value of it, has no dot and its glyph no hue; a facet that two plugins declare is the strip only of the rows of the plugins that mark it. The legend of a row view lists its strip facet's values in rank, its tooltip the `legend_title` row word, and the Sort menu sorts by it. A row's tooltip names whom it waits on unless that is you or nobody. A facet whose `rank` lists only the plugin's situation kinds holds them, so its values read as its `[situations]` words unless its `values` word them; a facet two plugins declare keeps each one's bucket words for its rows.

What a row says beside its facets (its age, its facet buttons' tooltip and screen-reader label, its columns' heads, the flags and tooltips no facet carries, such as stale, briefing or a project's review) are the row words of `[lens]` in `views.toml`; a plugin's `ui.toml` `[lens]` replaces any of them for its own rows. In a list that mixes plugins, each row takes its own plugin's words. The age format lives in the lens, so the cockpit's rows and the terminal's lines print it alike; `filter_group` names the facet when a who value is a group of people, as a Slack user group is.

A facet id is one field across the profile and every plugin: a plugin's row facet of the same id must group, sort, rank, match and bucket the same way, and a disagreement is a profile problem naming both sources, which `profile check` and the health report. A fact that is false, zero or empty gives the task no value, so a list has no "0" or "false" group. The menus offer a field only when some item of the view has a value for it.

## Messages

A message is what a gate asks you to decide: an agent's question at the end of its turn (with its options), a post an agent makes mid-turn with the `task_post` tool, or a proposal to change a confirmed item. Each opens a gate of kind `question`, `post` or `proposal` on the task its unit is on, or on a new captured task when no open task holds it; a message on no unit gets a captured task named after its text. You resolve the gate with one of its intents, as `gates.toml` words them: answer, continue or dismiss a question, reply to or dismiss a post, approve or reject a proposal. An open gate whose `gates.toml` entry names a `closes_with` intent is something the task still asks you, so it holds the task open: its items resolving leave it open, and a workflow's verb that would end it is refused, until you settle the gate; the next sync then ends the task. When you end the task yourself, or through a session you drive, each such gate closes with its `closes_with` intent, in your name (one whose question that session's own turn asked is superseded instead): the bundled profile dismisses a question, a post, a stopped run's question and Review the diff. When the task's unit ends (a pull request merged or closed, a thread's closure posted), the gate closes as superseded, moot. `closes_with` names an intent that refuses the gate, never one that approves or answers it, and a gate opened after the task ended stays open. A gate with none, such as a pending round's, neither holds its task nor closes with it, and a draft's gate settles with its draft. A task's Answer and Approve resolve its open gate the same way, and Approve answers a question with its recommended option, or a confirmation with Yes. Your answer is stored with the message and the gate it answers, the agent is told which gate you answered and reads the answer with `task_read`, and the message shows whether it did (see [Turns](/concepts/turns/#answers)). A proposal's gate decides the ledger: approving it applies the change, rejecting it keeps the item as you confirmed it. When the ledger refuses the change you approved, say because the item is gone, the approval answers with why and the proposal stays open on a new gate. The words of an item proposal are the first proposal gate's `text` in `gates.toml`, over the facts `proposal.op`, `proposal.item`, `proposal.unit`, `proposal.reason` and `proposal.provenance`.

## Chat

What you write in the task page's composer and Send goes to the task's agent, kept on the task's timeline as a `message` event by you:

```
you ── Send ──▶ message event on the task
                  │
                  ├─ a session of the task is live, its agent idle ──▶ notice typed: "the user wrote on task T"
                  │                                                     agent ── task_read ──▶ the chat since its last read
                  ├─ a session of the task is live, its agent busy ──▶ the notice blocks its next stop
                  │
                  └─ none is live ──▶ a turn of [chat] intent, every message its agents have not read
                                      as its guidance, oldest first, resuming the last run on the task
                                      that had a session
```

The live session is a session whose unit is the task or one it holds, a pull request or a thread included, and that has a turn on one of them, so a task the loop derived from a Slack thread you never captured takes a chat the same way. A session with no turn there, such as one a start left open on the agent's trust dialog, cannot read the chat, so it is passed over and the message starts a turn. The notice carries no text: the agent reads it with `task_read`, which marks it read, or, in a session that did not load {{name}}'s MCP server, with `{{command}} turn answer task:<id>`, which the notice then names alone. Only a session with a turn on the task, or on a unit it holds, reads its chat: any other is refused and reads nothing. A message to an agent that is working or waiting on a permission is never typed over it: its next stop is blocked once with the notice. With no live session, `[chat] intent` in `tasks.toml` names the intent of the turn, `chat` in the bundled profile: a session on the task, in a worktree of the task's repository when it has one, else with no checkout. A message that starts a turn counts as read. Only you write on a task's chat: an agent's session is refused, unless you drive it.

## The Outbox

Every post an agent or a rule proposes is a draft first, held by its draft gate until its approval policy lets it out, and listed in the Outbox. See [What waits for you](/concepts/approval/#the-outbox). Default workflows, and the GitHub plugin's rule, also add drafts on their own, at each sweep. A draft a workflow proposes on a task goes to the unit its first item is on, else to its first unit:

| Workflow or rule | Proposes |
|---|---|
| `slack-stalled-nudge` | one nudge per Slack thread for the tasks someone else owes there, each once its check date passed and it was not nudged within `drafts.repeat_after` (7d): it mentions everyone those tasks' open items wait on and lists what they owe, and a task coming due while it is held joins it; never to someone with an open ask to you in that thread, one that waits on you, or is assigned to you and waits on nobody else (`draft.person_asks_you`); it leaves out those known not to be members of the conversation (`to = "members"`; where membership is unknown, everyone as before), and a held one that addresses someone known not to be a member is withdrawn as `outside` |
| `slack-stalled-nudge-dm` | for the same tasks, one nudge per person they wait on who is known not to be a member of the conversation, such as a third person an item of a direct message names: it posts in your direct conversation with them (`target = "person"`), quotes the items they owe, since they cannot open the conversation, is written in the language of the conversation the items came from, always waits for you (`gates.drafts_wait`), and is withdrawn as `joined` once the person joins the conversation (`draft.target_joined`) |
| `slack-closure` | a closing message for a Slack unit you started whose items resolved |
| `slack-ask-owner` | a question for a Slack unit's open items with no owner after a working day; it always waits for you |
| `github-nudge` | the GitHub plugin's nudge |
| `task-acknowledge-reply`, `task-acknowledge-react` | when you acknowledge a task: a reply saying you are looking into it, by the time you promised, or a reaction on its ask when you gave none |
| `task-not-mine` | when you say a task is not yours: a reply pointing the asker elsewhere |

Their texts are templates of the profile; see [Tune the Slack proposals](/guides/workflows/#tune-the-slack-proposals).

## Projects

A project is an outcome that takes more than one action. `p` on a task makes a project of it or attaches it to one. Its actions are its children: each names the project as its parent and keeps its place among them. A project is parallel or sequential; a sequential one's first open action, `task.sequential_next`, comes first in Needs me. Each project has a review interval, 7 days by default, kept on its task, and opens its task's [dossier](/concepts/turns/#dossiers).

## The review

The review (`g r`) walks the steps `views.toml`'s `[review]` declares, in order, each a view you mark done with `Enter` (`]` and `[` move between steps); a step's `label` is what it asks of you, and a step must name a view. The bundled ones:

1. Inbox to zero (`inbox`);
2. the Outbox (`outbox`);
3. Waiting: nudge? (`waiting`);
4. projects without a next action or past their review interval (`review-projects`);
5. Someday (`someday`);
6. prune Focus (`focus`).

## Configuration

```toml
[tasks]
stale_waiting_days = 2
areas = ["oss", "work"]
contexts = ["@decide", "@reply"]

[notifications]
digest_at = "09:00"
```

The tasks, messages, drafts, items and Slack messages have no retention yet: they grow until a later release prunes them. Only dossiers are archived and purged.
