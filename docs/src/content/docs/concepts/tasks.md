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

An **item** is one thing someone needs from someone inside a unit: a `request`, a `question`, a `decision` or a `todo`. It is open or resolved, and says who asked, who must act next and where it came from.

- The GitHub plugin derives items from what it reads, with no model call: a review requested of you, reviews to address, failing checks, a conflict, the fix you wait for. A pull request you follow that asks none of these has one item, `gh:follow`, that waits on whoever the pull request waits on: you when its agent waits with no question or a round waits on you, the loop while it works on it, else its author or reviewers, and nobody when it waits on no one but you, such as your own draft; such a task is owed by nobody. A pending, held or blocked round is a gate, never an item.
- The Slack plugin extracts them from conversations with one model call per unit (see [Slack as a source](/concepts/slack/)).

Once you create or correct an item it is **confirmed**: extraction never overwrites it again. A later extraction that disagrees becomes a proposal, a gate on the unit's task, with Approve and Reject (see [Messages](#messages)).

## From items to tasks

- Each open item that waits on you becomes a task in the Inbox; one you requested, or of a unit you care about, that waits on someone else becomes a task in Waiting. An item a task already holds stays with it, so a task you merged keeps its items.
- A plugin decides how its items group into tasks: each item carries a task key, its own id unless the plugin sets one. Every item of a key is an ask of one task. The GitHub plugin keys every item of a pull request on the pull request, so each pull request you follow is one task, the pull request its first unit, and a situation change (a review, then changes to address, then the merge) changes the task's asks, never the task. A key no task holds that names the unit goes to the unit's own task, the task whose first unit it is, open or not, so an open pull request never gets a second task: a done one takes the key's items and comes back into Next, marked changed, only when the pull request asks something new of you. A gate on a pull request whose task is done reopens that task too.
- A pull request's task lives as long as the pull request: it does not close when it asks nothing for a while, such as while the loop works on it. It ends through the bundled `task-results-ended` workflow once the pull request merged or closed, and is dropped when you stop following it (its `unfollowed` property says when); following it again brings it back to Next, marked changed. A pull request task you dropped yourself stays dropped. The bundled `pr-task-clarify` workflow moves a pull request's task that lands in the Inbox to Next, so what it asks of you is in Needs me. The bundled `pr-task-approved` workflow moves the task of someone else's pull request to Done once your newest verdict on it approves it (`task.link_signal.approved_by_me`) and nothing there waits on you: what is left is the author's to merge, and a review asked of you again brings it back. It runs once per task, so a task you open again stays open.
- A plugin proposes the category a new task starts in (`inbox` or `ready`), never a state name: the task takes the first state of that category in `tasks.toml`. It also gives the task the `kind` its manifest names for its units, such as `thread`; the bundled `task-kind` workflow gives a task you made with no unit the kind `capture`.
- When the items of a task resolve, the task moves to Done. A task you clarified never comes back to the Inbox: an item that reopens only marks an open one changed, and a done task comes back to Next, marked changed, only when its unit asks something new of you, an item waiting on you that the task did not hold or that changed since the task was last updated. A done you set, while the pull request still shows the ask you closed it over, stays done. A task you dropped stays dropped.
- Closing a thread you started is the `slack-closure` workflow's draft on the thread, once its items resolved; sending it, through its `unit.closed` effect, ends the tasks that hold the thread.

## Who owes the next step

`owed_by.expr` in `tasks.toml` is an expression over a task's facts whose value is `me`, a person's key, or empty when nobody owes it; it is the fact `task.owed_by`, and the task's `owed_by` on the wire. The bundled one makes it yours when the task has an open gate; nobody's when it is closed; otherwise the `owner` a person handed it to, so Hand to on an ask that waits on you moves it to that person; otherwise yours when an item waits on you, otherwise whoever its items wait on (`task.link_owes`), otherwise its `owner` however set, nobody when its units have open items and none waits on anyone, such as a quiet pull request you follow, and yours when it has none:

```toml
[owed_by]
expr = '''
task.gates_open > 0 ? "me" :
task.category in ["done", "dropped"] ? "" :
task.prop.owner != "" && task.prop_by.owner == "person" ? task.prop.owner :
"me" in task.link_owes ? "me" :
size(task.link_owes) > 0 ? task.link_owes[0] :
task.prop.owner != "" ? task.prop.owner :
task.items_open > 0 ? "" : "me"'''
```

`task.owed_since` is since when: the oldest of its items that wait on whoever owes it, else, for you, its oldest open gate, else the task's creation. `task.check_on` is the task's `check_on` property, else, when the items the task holds wait on someone else and not on you, `tasks.stale_waiting_days` working days after the oldest of them; a gate open on the task, its own nudge's draft included, leaves it as it is. The bundled `slack-stalled-nudge` workflow nudges whoever owes a Slack task once its check date passed, listing every open item that person owes on the thread, one draft per person and thread, at most once per `drafts.repeat_after`: sending it writes `last_nudge` on every task that holds one of the items. The bundled `task-autofocus` workflow stars a task past its due date, and one whose items wait on others past its check date with no nudge since; a star you cleared stays cleared. It marks the stars it sets with the `autostar` property, and `task-autofocus-clear` takes off such a star once neither reason holds; a star you set is never taken off.

## Create a task

One route, `POST /api/tasks`, creates every task you make: a title alone captures it in the Inbox, as `n` in the terminal does. `{{command}} task new --title <title> --brief-file <path> --prop <name>=<value> --repo <owner/name>` creates one with the rest, and prints it as `task:<id>`:

- the file's markdown is the task's brief record, in its `text` field;
- each `--prop` is a property `tasks.toml` declares, parsed by its type in the daemon's time zone, such as `priority=high`, `due=2026-11-02` or `size=30m`; a property the profile does not declare, one its writers leave you out of, or a value its type refuses, creates nothing;
- `--repo` is where the task's work runs: a later `start` on the task without `--repo` works in that repository.

A task created from an agent's session is written by `agent`: its brief and properties are untrusted, and a property agents do not write is refused.

## Units attached to a task

A task's units come in order: the unit it was captured or derived on, then the units attached to it, in the order they were attached, then any unit only its items name. The task itself is never one of them.

What a turn on a task produces is attached to it, when the turn's intent asks for it:

- With `record.attach_url = true`, a turn that records `done` with an outcome `url` naming a unit, such as a pull request, attaches that unit. The address is resolved by the plugins' units; a branch name never attaches anything.
- With `drafts.attach_posts = true`, the unit of each post the turn's agent makes, sent at once or once approved, is attached; a draft on the task posted to a channel or a person attaches the thread its post starts. Reactions and edits are not posts.

Each plugin says when one of its units has ended: a pull request merged or closed (`pr.ended`); a Slack unit with a closure posted or marked, or quiet for `plugins.slack.ended_after` (7 days) (`slack.ended`). The bundled workflow `task-results-ended` marks done a pull request's task, and a task you captured once a turn ran on it, such as a start, when it has a unit and every one of them has ended and no gate waits on you there (`task.link_ended && task.gates_open == 0 && (task.prop.kind == "pull-request" || task.captured_by_user && task.turns > 0)`): a pull request's task that also holds a thread still open stays open. A task a gate opens on a unit no task holds takes the kind the unit's plugin gives its tasks, so a gate on a pull request with no task, such as a merged one, makes it the pull request's task, out of the Inbox. A task you captured on a pull request to keep track of it, with no turn on the task itself, stays open. No workflow closes a derived task while it holds an item that calls for it.

## The timeline and the records

A task's timeline is its own events and those of the units it holds, every one kept forever, newest first: `GET /api/tasks/<id>/timeline` pages it by each event's `seq`, and its `kinds` parameter keeps the kinds asked for.

A record is a typed note on a unit or a task, of a kind `records.toml` declares: the JSON schema under `records/` its data follows, who may write it, and the task properties its fields set. A unit keeps one record of each kind, the newest; the timeline shows it as a `record` event. The bundled kinds are `brief`, `report` and `decision`. A brief record sets the task's `risk` from its `risk` and the task's `effort` from its `size`, when its writer may write the property: `size` stays the attention you give the task. A task's brief is its own brief record, else the brief of the first unit it holds that has one, so a pull request task's brief is its pull request's.

## Start new work

`{{command}} start --repo acme/api <brief>` creates a task from the brief through the same route, prints `task:<id>`, then starts an agent session on it. `--brief-file <path>` reads the brief from a file instead. The manifest's `[target] title` makes the title, bundled as `{{title 72 .guidance}}`: the brief's first line without its markdown heading marks, cut at a whole word within 72 characters. The whole brief is the task's brief record, which the session reads. `{{command}} start task:<id>` starts one on a task you created before, such as one `task new` made, and the cockpit offers Start on such a task. Start runs only on a task you captured; a derived task is refused.

- **With `--repo`**, the session works in a fresh worktree of that repository, branched from its default base and named after the task's title and short id. A repository no manager knows is cloned into the matching project's `projects_dir`; an existing folder there must be a checkout of that repository. `--manager` picks the workspace manager, and `--parent` the worktree the new one goes under in Orca (the one the command runs in by default). The project's `workspace_managers`, `agent` and `agent_home` apply, to the clone's trust and to the session alike. The manager is chosen before anything is cloned, so a start it refuses clones nothing. A failed start prints the command that retries it on the task with the same flags.
- **Without `--repo`**, the session runs with no checkout, in the neutral repository, for a message to post or an answer to find.

The `start` intent records `done` with the pull request's or the message's URL, which attaches it to the task (`record.attach_url`); posts it makes at once attach too (`drafts.attach_posts`). Once a pull request is attached to a task you captured and ran a turn on, the bundled `start-review-diff` workflow opens the `review-diff` gate on it, Review the diff, which waits for you; it opens once per set of attached units, so dismissing it keeps it closed until another unit attaches. Approving it, Looks good, closes the task through the bundled `start-diff-approved` workflow when no other gate of the task is open: the work it asked for is delivered, and what the pull request asks of you later brings the task back. Otherwise the task closes when its units end, as the next section says. When the cockpit's Start runs, it takes no `--repo`: the turn reuses the repository an earlier start on the task recorded, else it runs with no checkout.

## Runs

Every turn on a task is one of its runs. The task's page lists them newest first, each with its intent and state, the workspace manager, workspace and worktree it ran in, its session and that session's state (active, dormant or closed), and the command that resumes it. A run offers the actions it can take:

- **Open workspace** brings its workspace on screen in the manager it ran in.
- **Enter session** brings its session on screen to type in it. A dormant session, the run's own, is woken first in its own manager, with the policy, trust and session name a turn of the run's intent launches with.
- **Transcript** reads what the person typed and what the agent answered in the session, page by page.

A headless call has no workspace or session, so it offers none of them. `{{command}} open` on a task focuses the workspace of its newest run that ran in one.

### Attach a session

`{{command}} task attach task:<id>`, run from an agent session a workspace manager started, makes that session a run of the task: the daemon starts the turn of the intent holding the `attach` role (the bundled `attach`) on it by hand, and the session takes the task as its unit, whatever unit it had before. Every attached session is a run of its own: attaching again from the same session replaces only that session's earlier attach (the role's `PerSession`). What the session posts, drafts or records from then on lands on the task. The `attach` intent has no `[record]`, so its turn owes no record and the Stop hook never blocks the session for one. Outside a workspace manager's session, the command refuses with the `attach_needs_session` notice and changes nothing.

## Lists are categories

| State | Category | Means |
|---|---|---|
| `inbox` | `inbox` | not clarified yet |
| `next` | `ready` | an action you will do |
| `waiting` | `waiting` | someone else owes it: who, since when, the last nudge and a date to resurface |
| `scheduled` | `deferred` | snoozed until a date, or until new activity on one of its units |
| `someday` | `deferred` | maybe later |
| `done` | `done` | finished |
| `reference` | `done` | kept to read, no action |
| `trash` | `dropped` | dismissed |

A task is open while its category is neither `done` nor `dropped`. Beside its state, a task has properties: the bundled ones include a **focus** star, **contexts** (labels such as `@decide`), an **area** and a **due** date. A property's `role` in `tasks.toml` names the mechanism of {{name}} that acts on it, so Go reads the role and never the property's name: `area` (the area filter of every view and of the counters reads it, the area verb sets it, a new project takes it from its first task), `contexts` (the contexts filter reads it, the context verb sets it, a derived task takes its item's), `star` (the focus verb toggles it between its first value and its second), `deadline` (a derived task takes its items' deadline, an Outbox draft its gate's window) and `promise` (acknowledge sets it, and `task.promised` reads it). One property takes each role; a user layer that gives a role to another property moves it. A task's **parent** is the project it belongs to, and its children keep their order.

## Views

A view is a list of the profile's `views.toml`: a `when`, a condition over a task's facts (or a gate's, with `over = "gates"`), and the tasks or gates it holds, with its `label`, the `key` that jumps to it, the fact it groups by (`group`) and those it sorts by (`sort`). A view lists its tasks the oldest first, a parent's children in their sibling order, then by its `sort`; `hide_disabled` leaves out the tasks whose units are all of a disabled plugin; `remove_clean` offers to remove at once the workspaces its tasks' links hold, keeping each one whose checkout holds anything not published, with why. The area and the contexts you pick narrow every view and its count. The bundled views:

| View | Lists |
|---|---|
| Inbox | what nobody has clarified (`task.category == 'inbox'`) |
| Needs me | a clarified task whose next step is yours: an open gate, or your own action (`task.owed_by == 'me'`); a sequential project's next action first (`-task.sequential_next`) |
| Waiting | a task someone else owes, grouped by `task.owed_by` |
| Chase | a task someone else owes whose `check_on` passed with nothing moving since, grouped by `task.owed_by`: time to ask them |
| Fits now | a clarified task whose next step is yours and whose `size`, the attention it takes, is at most half an hour, the smallest first |
| Blocked by me | a task whose asks wait on you, grouped by who asked (`task.askers`), the oldest ask first (`task.ask_age`) |
| Stalled | a task a source says stalled (`task.link_signal.stalled`), or, when no link reports `stalled`, one someone else owes whose `check_on` passed with nothing moving since (`task.since_movement`) |
| In loop | a task a source's loop holds (`task.link_signal.in_loop`), or with a live run (`task.runs_live`) |
| Approved | a task a source says is ready (`task.link_signal.ready`); it offers Remove all clean |
| Snoozed | a task asleep on a wake |
| Focus | an open starred task |
| Projects | the open projects |
| Someday | the `someday` state |
| Logbook | the `done` category, the latest moved first (`task.since_movement`) |
| Done | a done task whose links still hold a workspace on disk (`task.link_signal.workspace`), to remove; it offers Remove all clean |
| Outbox | over gates: the drafts waiting on you, and those approved whose delivery is under way or failed, so you can retry them |
| Projects to review | an open project past its review, or, unless in Someday, with no ready child (`task.review_due`, `task.children_ready`) |
| Review | lists nothing: the clients walk the review's steps |

The menu bar's badge adds up the views `[badge] views` names, Inbox and Needs me in the bundled profile: an open gate makes its task yours, even a done one, so it shows in Needs me, and a task still in the Inbox counts there once. A view whose condition reads only the task's own fields (its state, category and properties), and not `now`, pages since a position: a page lists the tasks written after it; any other view, such as Needs me, which reads gates, wakes and items, is served whole each time. Your own `views.toml` changes a bundled view's keys, or adds views after the bundled ones, in the order it writes them.

A plugin reports per unit the link signals its manifest declares (`[[source.signals]]`): the GitHub plugin reports `stalled` (an open pull request it follows whose situation lasted past its `github.stalled.*` threshold), `in_loop` (an open one a review workspace holds), `ready` (one whose work is approved), `workspace` (one a workspace of which remains on disk, open or not) and `approved_by_me` (someone else's open pull request whose newest verdict of yours approves it, and that does not ask your review again). A task reads each declared signal as the fact `task.link_signal.<name>`, on when it is on for any unit the task links, under the host's own state: a round, a run or a mute on the pull request turns `ready` off and its own situation decides `stalled`. A view or a workflow naming a signal no plugin declares is a load problem. `task.since_movement` is the time since the task changed or a unit it links last moved: its situation began, or a run changed it. `task.link_signals` lists the signals a task's links report, on or off. The bundled profile lets a source that reports `stalled` keep its own staleness: Stalled, `task-autofocus` and `task-check-on` read the check date only when `'stalled'` is not in `task.link_signals`, so Stalled lists a pull request only when GitHub says it stalled. The bundled `task-check-on` workflow gives a task someone else owes, when no unit it links says whom it waits on or a person handed it to its owner, and no unit keeps its own staleness, a `check_on` two days ahead, so Stalled lists it once that date passes with nothing moving.

The states and the task properties are the profile's `tasks.toml`. Each state has a category (`inbox`, `ready`, `active`, `waiting`, `deferred`, `done` or `dropped`), which is all {{name}} reads of it: a task proposed in a category takes the first state of it, in the order the file declares them, and a state of your `tasks.toml` comes after the bundled ones. The store keeps each task's category beside its state, and the lists read the category. When a reload of the profile moves a state to another category, the daemon gives the stored tasks of that state the new one; a state stored tasks hold that no layer declares any more is a profile problem the health shows, naming `states.<name>`, and those tasks keep the category they had until you declare it again or move them. Each property is the fact `task.prop.<name>`, typed from its `type`, and who last wrote it the fact `task.prop_by.<name>` (`person`, `workflow`, `agent` or `plugin`, empty while unset), and says who may write it: a property an agent or a plugin writes, such as `risk`, is untrusted, so no approval rule that sends or runs reads it. An `int` or a `duration` property of 0 is unset.

The words of the verbs come from the profile's `tasks.toml`: each verb's label is its button, its summary the button's tooltip and its input the question it asks first. A verb's `from` are the states it moves a task from, every state when left out, and its `to` the state it moves it to. A verb's `offer`, a condition over the task's own facts (`task.prop.kind`, `task.state`, `task.unit_provider`), says where a task offers it: `offer = 'task.prop.kind != "capture"'` on `delegate` leaves Delegate off captured tasks, and the daemon refuses it there too. A workflow's `task.verb` step is not bound by it. A verb that moves an open task to a done or dropped state withdraws the held drafts it carries, but those of the purposes its `keep_drafts` lists (see [What waits for you](/concepts/approval/#the-outbox)). A verb's `handler` is what the loop runs besides its move (`project`, `reply`, `approve`, `promise`, ...), bound by that name and never by the verb's. A profile may declare verbs of its own, named in lowercase letters, digits and hyphens: one with no handler only moves the task, and a workflow can react to its event; one whose handler asks for a value (`waiting`, `schedule`, `project`, `context`, `area`, `delegate`, `reply`, `promise`) needs its `input`. A verb's `by` says who may run it, per actor (`person`, `workflow`, `agent`, `plugin`): a condition over the task's facts, such as `workflow = 'task.prop.kind == "capture"'`, or `"gate"`, which opens the gate the verb's `gate` names in `gates.toml` for you to decide, once per proposal while it is open, and leaves the task as it is; approving it runs the verb as yours. A `by` condition reads the task's own facts (`task.id`, `task.state`, `task.category`, `task.unit`, `task.unit_provider`) and its properties; naming another fact is a load problem. A verb asked from an agent's session, whose requests carry the session's handle, runs as `agent`; any other as `person`. An actor `by` leaves out may not run the verb; the bundled profile lets you run every verb, lets workflows run next, waiting, schedule, wake, focus, done and dismiss, and gives agents none. Every verb run on a task that changes it is a `verb_<verb>` event, by its actor, that a [workflow](/concepts/workflows/#react-to-a-verb) can react to, but one whose handler is `seen` and the verbs on an Outbox draft (approve, edit, retry, dismiss), which are the draft's own. A task's events, and every event, turn, record, answer, plan item and brief of a unit linked to a task, are kept forever: retention never reaches them.

- **Changed since you saw it.** New activity on a unit by someone else, after you last opened the task, marks a waiting or scheduled task changed, and matches the wake of one scheduled until activity; it never brings a task back to the Inbox. Opening or moving the task clears the mark.
- **Snooze.** `s` schedules a task until a date, until activity, or both: it puts a wake on the task, one per task, which remembers the state the task had. A wake matches when its date passes, on activity, or once its condition over the task's facts holds (`task.wake_until` and `task.wake_when` read it); the loop then records `task.woke` on the task, and the bundled `task-wake` workflow runs the `wake` verb, which brings the task back to that state. Moving the task by hand drops its wake. Muting a pull request is a wake on its task: for a while, or until whom it waits on (`task.link_owes`) changes, or a review workspace takes or leaves it, or its work becomes approved (`task.link_signal.in_loop`, `task.link_signal.ready`); unmute drops it and brings the task back at once.
- **Waiting.** `w` moves a task to Waiting, optionally naming whom it waits on, its `owner`, and a date to check on it, its `check_on`.
- **Triage.** Acknowledge tells the asker you are on it: given a time (`17:00`, today, or tomorrow once it passed; a date, 18:00 that day), it sets the task's `promised` property and the bundled `task-acknowledge-reply` workflow proposes a reply saying you are looking into it, by then; given none, `task-acknowledge-react` proposes an :eyes: reaction on the ask. Either moves the task to Next. Not mine moves it to the trash, and `task-not-mine` proposes a reply pointing the asker elsewhere, for you to edit before you approve it. Hand to moves it to Waiting on the person you name, like Waiting, and `task-check-on` gives it a check date two days ahead, so Chase lists it once that date passes with nothing moving. Every draft these propose waits for your approval in the Outbox. `task.ask_age` is the age of the oldest open ask a task holds that waits on you, and `task.askers` who asked it.
- **Extraction health.** A plugin reports per unit what its extraction has not read yet; `task.extraction_lag` is how long the oldest such message on a task's units has waited, and a task's row shows it (`unread 12m`), since a missed request is worse than a noisy one.

Approve (`a`) and delegate (`d`) move nothing themselves: on an action task with units they run that unit's action, such as Run now, a review or address round, or Act. Delegate also runs on any other task about its units, such as one you captured on a unit, and takes optional guidance and a mode: `headless` (Draft for me, which proposes drafts for the task) or `session` (Hand to an agent). Without a mode the action picks its own, headless when it can. The task says upfront whether a session can run on its unit, and why not when it cannot; a unit with no placement runs its session in a neutral directory with no checkout. A session started from a task keeps the task and its items, so the drafts it proposes serve that task.

Reply (`r`) writes a draft of your own on the task's first unit, a reply in its conversation (a comment on a pull request) or a reaction, tied to the task and its items. Send posts it at once, as your approval of it; Save as draft holds it on its gate for your approval. The verb's `send` in `tasks.toml` (`now` in the bundled profile) picks which is the default: the daemon serves it in the catalog, and the task page's composer, the unit composer, the terminal prompt (Enter does the default, Tab the other) and a notification's order of Reply and Send reply all follow it. A reply that names neither takes it too. The verb is the one whose `handler` is `reply`, whatever the profile calls it. From a terminal, `{{command}} outbox new <unit|message> <markdown>` writes one on any unit. The full key list is on the [Keys](/reference/keys/) page.

## Messages

A message is what a gate asks you to decide: an agent's question at the end of its turn (with its options), a post an agent makes mid-turn with the `task_post` tool, or a proposal to change a confirmed item. Each opens a gate of kind `question`, `post` or `proposal` on the task its unit is on, or on a new captured task when no open task holds it; a message on no unit gets a captured task named after its text. You resolve the gate with one of its intents, as `gates.toml` words them: answer, continue or dismiss a question, reply to or dismiss a post, approve or reject a proposal. A task's Answer and Approve resolve its open gate the same way, and Approve answers a question with its recommended option, or a confirmation with Yes. Ending the task yourself, with a verb whose handler is `finish` or `drop` such as Done or Dismiss, once the task is closed resolves each of its open gates whose `gates.toml` entry names a `closes_with` intent with that intent, as yours: the bundled profile dismisses a question, a post, a stopped run's question and Review the diff, so the decision does not move to the task's unit and bring the task back. A gate with no `closes_with`, such as a draft's, stays open, as does a gate of a disabled plugin's unit; `closes_with` names an intent that refuses the gate, never one that approves or answers it. Your answer is stored with the message and the gate it answers, the agent is told which gate you answered and reads the answer with `task_read`, and the message shows whether it did (see [Turns](/concepts/turns/#answers)). A proposal's gate decides the ledger: approving it applies the change, rejecting it keeps the item as you confirmed it. When the ledger refuses the change you approved, say because the item is gone, the approval answers with why and the proposal stays open on a new gate. The words of an item proposal are the first proposal gate's `text` in `gates.toml`, over the facts `proposal.op`, `proposal.item`, `proposal.unit`, `proposal.reason` and `proposal.provenance`.

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

The live session is any session whose unit is the task or one it holds, a pull request or a thread included, so a task the loop derived from a Slack thread you never captured takes a chat the same way. The notice carries no text: the agent reads it with `task_read`, which marks it read, or, in a session that did not load {{name}}'s MCP server, with `{{command}} turn answer task:<id>`, which the notice then names alone. Only a session with a turn on the task, or on a unit it holds, reads its chat: any other is refused and reads nothing. A message to an agent that is working or waiting on a permission is never typed over it: its next stop is blocked once with the notice. With no live session, `[chat] intent` in `tasks.toml` names the intent of the turn, `chat` in the bundled profile: a session on the task, in a worktree of the task's repository when it has one, else with no checkout. A message that starts a turn counts as read. An agent's session cannot write on a task's chat.

## The Outbox

Every post an agent or a rule proposes is a draft first, held by its draft gate until its approval policy lets it out, and listed in the Outbox. See [What waits for you](/concepts/approval/#the-outbox). Default workflows, and the GitHub plugin's rule, also add drafts on their own, at each sweep. A draft a workflow proposes on a task goes to the unit its first item is on, else to its first unit:

| Workflow or rule | Proposes |
|---|---|
| `slack-stalled-nudge` | a nudge to each person who owes a task with a Slack unit, once its check date passed and it was not nudged within `drafts.repeat_after` (1d) |
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
