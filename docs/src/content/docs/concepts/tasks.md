---
title: Tasks and views
description: How what people ask of you becomes tasks, how the lists and projects hold them, and how the review walks through them.
---

{{name}} keeps your work as **tasks**. A task is something you act on, wait for, read later or decide about. It points at the **items** and the **units** it comes from.

## Units and items

A **unit** is a thing work happens on: a pull request, a Slack thread, a DM topic. Units come from plugins. Two are compiled in: `github` (pull requests) and `slack` (threads and DM topics). Each can be turned off:

```toml
[plugins.slack]
enabled = false
```

The daemon reads the switch when it starts. With a plugin off, nothing of it runs, its actions leave every surface, and its cards render from the last stored snapshot, marked stale.

A task with a unit of a plugin that is off refuses every verb but Dismiss, with the reason naming the plugin. Dismiss moves a task whose units are all of plugins that are off to the Trash, as it does a task derived from items, whose identity is the unit it came from. On any other task that also has a unit of a plugin that is on, it detaches the units of the plugins that are off and the items on them, and the task stays where it is with the rest. When the task's first unit is of a plugin that is on and offers its own dismiss action, Dismiss runs that action, as on any task. A link an item makes to a unit of a plugin that is off is stored, and is left out while the plugin is off.

An **item** is one thing someone needs from someone inside a unit: a `request`, a `question`, a `decision` or a `todo`. It is open or resolved, and says who asked, who must act next and where it came from.

- The GitHub plugin derives items from what it reads, with no model call: a review requested of you, reviews to address, failing checks, a conflict, a pending or blocked round, an agent that asked.
- The Slack plugin extracts them from conversations with one model call per unit (see [Slack as a source](/concepts/slack/)).

Once you create or correct an item it is **confirmed**: extraction never overwrites it again. A later extraction that disagrees becomes a proposal in the Inbox, with Accept and Reject.

## From items to tasks

- Each open item that waits on you becomes a task in the Inbox; one you requested, or of a unit you care about, that waits on someone else becomes a task in Waiting. An item a task already holds stays with it, so a task you merged keeps its items.
- A plugin proposes the category a new task starts in (`inbox` or `ready`), never a state name: the task takes the first state of that category in `tasks.toml`. It also gives the task the `kind` its manifest names for its units, such as `thread`; the bundled `task-kind` workflow gives a task you made with no unit the kind `capture`.
- When the items of a task resolve, the task moves to Done. A task you clarified never comes back to the Inbox: an item that reopens moves a done task back to Next, marked changed, and only marks an open one changed. A task you dropped stays dropped.
- Closing a thread you started is the `slack-closure` workflow's draft on the thread, once its items resolved; sending it, through its `unit.closed` effect, ends the tasks that hold the thread.

## Who owes the next step

`owed_by.expr` in `tasks.toml` is an expression over a task's facts whose value is `me`, a person's key, or empty when nobody owes it; it is the fact `task.owed_by`, and the task's `owed_by` on the wire. The bundled one makes it yours when the task has an open gate or an item that waits on you, otherwise whoever its items wait on (`task.link_owes`), otherwise its `owner` property, and yours when it has none:

```toml
[owed_by]
expr = '''
task.category in ["done", "dropped"] ? "" :
task.gates_open > 0 || "me" in task.link_owes ? "me" :
size(task.link_owes) > 0 ? task.link_owes[0] :
task.prop.owner != "" ? task.prop.owner : "me"'''
```

`task.owed_since` is since when: the oldest of its items that wait on whoever owes it, else, for you, its oldest open gate, else the task's creation. `task.check_on` is the task's `check_on` property, else, for a task someone else owes, `tasks.stale_waiting_days` working days after `task.owed_since`. The bundled `slack-stalled-nudge` workflow nudges whoever owes a Slack task once its check date passed, listing every open item that person owes on the thread, one draft per person and thread, at most once per `drafts.repeat_after`: sending it writes `last_nudge` on every task that holds one of the items. The bundled `task-autofocus` workflow stars a task past its due date, and one someone else owes past its check date with no nudge since; a star you cleared stays cleared.

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

Each plugin says when one of its units has ended: a pull request merged or closed (`pr.ended`); a Slack unit with a closure posted or marked, or quiet for `plugins.slack.ended_after` (7 days) (`slack.ended`). The bundled workflow `task-results-ended` closes a task you captured, marking it done, once a turn ran on it, such as a start, and it has a unit and every one of them has ended (`task.captured_by_user && task.turns > 0 && task.link_ended`). A task you captured on a pull request to keep track of it, with no turn on the task itself, stays open. It never closes a task derived from items, which its items close.

## The timeline and the records

A task's timeline is its own events and those of the units it holds, every one kept forever, newest first: `GET /api/tasks/<id>/timeline` pages it by each event's `seq`, and its `kinds` parameter keeps the kinds asked for.

A record is a typed note on a unit or a task, of a kind `records.toml` declares: the JSON schema under `records/` its data follows, who may write it, and the task properties its fields set. A unit keeps one record of each kind, the newest; the timeline shows it as a `record` event. The bundled kinds are `brief`, `report` and `decision`. A brief record sets the task's `risk` from its `risk` and the task's `effort` from its `size`, when its writer may write the property: `size` stays the attention you give the task. A task's brief is its own brief record, else the brief of the first unit it holds that has one, so a pull request task's brief is its pull request's.

## Start new work

`{{command}} start --repo acme/api <brief>` creates a task from the brief through the same route, prints `task:<id>`, then starts an agent session on it. `--brief-file <path>` reads the brief from a file instead. The manifest's `[target] title` makes the title, bundled as `{{title 72 .guidance}}`: the brief's first line without its markdown heading marks, cut at a whole word within 72 characters. The whole brief is the task's brief record, which the session reads. `{{command}} start task:<id>` starts one on a task you created before, such as one `task new` made, and the cockpit offers Start on such a task. Start runs only on a task you captured; a derived task is refused.

- **With `--repo`**, the session works in a fresh worktree of that repository, branched from its default base and named after the task's title and short id. A repository no manager knows is cloned into the matching project's `projects_dir`; an existing folder there must be a checkout of that repository. `--manager` picks the workspace manager, and `--parent` the worktree the new one goes under in Orca (the one the command runs in by default). The project's `workspace_managers`, `agent` and `agent_home` apply, to the clone's trust and to the session alike. The manager is chosen before anything is cloned, so a start it refuses clones nothing. A failed start prints the command that retries it on the task with the same flags.
- **Without `--repo`**, the session runs with no checkout, in the neutral repository, for a message to post or an answer to find.

The `start` intent records `done` with the pull request's or the message's URL, which attaches it to the task (`record.attach_url`); posts it makes at once attach too (`drafts.attach_posts`). The task then closes when they end, as the next section says. When the cockpit's Start runs, it takes no `--repo`: the turn reuses the repository an earlier start on the task recorded, else it runs with no checkout.

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

A task is open while its category is neither `done` nor `dropped`. Beside its state, a task has properties: the bundled ones include a **focus** star, **contexts** (labels such as `@decide`), an **area** (switching area filters every view but the Inbox) and a **due** date. A task's **parent** is the project it belongs to, and its children keep their order.

The views are Focus (starred open tasks, the landing view), Inbox, Outbox, Next (the `ready` category), Waiting, Projects, Someday (`deferred` with no snooze), Logbook (the `done` category, newest first) and Review. The lists are inspired by David Allen's *Getting Things Done*®.

The states and the task properties are the profile's `tasks.toml`. Each state has a category (`inbox`, `ready`, `active`, `waiting`, `deferred`, `done` or `dropped`), which is all {{name}} reads of it: a task proposed in a category takes the first state of it, in the order the file declares them, and a state of your `tasks.toml` comes after the bundled ones. The store keeps each task's category beside its state, and the lists read the category. When a reload of the profile moves a state to another category, the daemon gives the stored tasks of that state the new one; a state stored tasks hold that no layer declares any more is a profile problem the health shows, naming `states.<name>`, and those tasks keep the category they had until you declare it again or move them. Each property is the fact `task.prop.<name>`, typed from its `type`, and says who may write it: a property an agent or a plugin writes, such as `risk`, is untrusted, so no approval rule that sends or runs reads it. An `int` or a `duration` property of 0 is unset.

The words of the verbs come from the profile's `tasks.toml`, and those of the views and the review's steps from its `views.toml`, which also gives each view the `key` that jumps to it: each verb's label is its button, its summary the button's tooltip and its input the question it asks first. A verb's `from` are the states it moves a task from, every state when left out, and its `to` the state it moves it to. A verb's `offer`, a condition over the task's own facts (`task.prop.kind`, `task.state`, `task.unit_provider`), says where a task offers it: `offer = 'task.prop.kind != "capture"'` on `delegate` leaves Delegate off captured tasks, and the daemon refuses it there too. A workflow's `task.verb` step is not bound by it. A verb's `handler` is what the loop runs besides its move (`project`, `reply`, `approve`, ...), bound by that name and never by the verb's. A verb's `by` says who may run it, per actor (`person`, `workflow`, `agent`, `plugin`): a condition over the task's facts, such as `workflow = 'task.prop.kind == "capture"'`, or `"gate"`, which opens the gate the verb's `gate` names in `gates.toml` for you to decide, once per proposal while it is open, and leaves the task as it is; approving it runs the verb as yours. A `by` condition reads the task's own facts (`task.id`, `task.state`, `task.category`, `task.muted`, `task.unit`, `task.unit_provider`) and its properties; naming another fact is a load problem. A verb asked from an agent's session, whose requests carry the session's handle, runs as `agent`; any other as `person`. An actor `by` leaves out may not run the verb; the bundled profile lets you run every verb, lets workflows run next, waiting, schedule, focus, done and dismiss, and gives agents none. Every verb run on a task that changes it is a `verb_<verb>` event, by its actor, that a [workflow](/concepts/workflows/#react-to-a-verb) can react to, but one whose handler is `seen` and the verbs on an Outbox draft (approve, edit, retry, dismiss), which are the draft's own. A task's events, and every event, turn, record, answer, plan item and brief of a unit linked to a task, are kept forever: retention never reaches them.

- **Changed since you saw it.** New activity on a unit by someone else, after you last opened the task, marks a waiting or scheduled task changed, and wakes one scheduled until activity into the state it had; it never brings a task back to the Inbox. Opening or moving the task clears the mark.
- **Snooze.** `s` schedules a task until a date, until activity, or both. When the date passes it returns to the state it had. Muting the unit snoozes its tasks.
- **Waiting.** `w` moves a task to Waiting, optionally naming whom it waits on, its `owner`, and a date to check on it, its `check_on`.

Approve (`a`) and delegate (`d`) move nothing themselves: on an action task with units they run that unit's action, such as Run now, a review or address round, or Act. Delegate also runs on any other task about its units, such as one you captured on a unit, and takes optional guidance and a mode: `headless` (Draft for me, which proposes drafts for the task) or `session` (Hand to an agent). Without a mode the action picks its own, headless when it can. The task says upfront whether a session can run on its unit, and why not when it cannot; a unit with no placement runs its session in a neutral directory with no checkout. A session started from a task keeps the task and its items, so the drafts it proposes serve that task.

Reply (`r`) writes a draft of your own on the task's first unit, a reply in its conversation (a comment on a pull request) or a reaction, tied to the task and its items. It waits in the Outbox, or goes out at once with Send now. From a terminal, `{{command}} outbox new <unit|message> <markdown>` writes one on any unit. The full key list is on the [Keys](/reference/keys/) page.

## Messages

A message is what a gate asks you to decide: an agent's question at the end of its turn (with its options), a post an agent makes mid-turn with the `inbox_post` tool, or a proposal to change a confirmed item. Each opens a gate of kind `question`, `post` or `proposal` on the task its unit is on, or on a new captured task when no open task holds it; a message on no unit gets a captured task named after its text. You resolve the gate with one of its intents, as `gates.toml` words them: answer, continue or dismiss a question, reply to or dismiss a post, approve or reject a proposal. A task's Answer and Approve resolve its open gate the same way, and Approve answers a question with its recommended option, or a confirmation with Yes. Your answer is stored with the message and the gate it answers, the agent is told which message you answered and reads the answer with `inbox_read`, and the message shows whether it did (see [Turns](/concepts/turns/#answers)). A proposal's gate decides the ledger: approving it applies the change, rejecting it keeps the item as you confirmed it. When the ledger refuses the change you approved, say because the item is gone, the approval answers with why and the proposal stays open on a new gate. The words of an item proposal are the first proposal gate's `text` in `gates.toml`, over the facts `proposal.op`, `proposal.item`, `proposal.unit`, `proposal.reason` and `proposal.provenance`.

## The Outbox

Every post an agent or a rule proposes is a draft first, in the Outbox, until its approval policy lets it out. See [What waits for you](/concepts/approval/#the-outbox). Default workflows, and the GitHub plugin's rule, also add drafts on their own, at each sweep. A draft a workflow proposes on a task goes to the unit its first item is on, else to its first unit:

| Workflow or rule | Proposes |
|---|---|
| `slack-stalled-nudge` | a nudge to each person a waiting task with a Slack unit waits on, once it is past its resurface date or stale and not nudged within `drafts.repeat_after` (1d) |
| `slack-closure` | a closing message for a Slack unit you started whose items resolved |
| `slack-ask-owner` | a question for a Slack unit's open items with no owner after a working day; it always waits for you |
| `github-nudge` | the GitHub plugin's nudge |

Their texts are templates of the profile; see [Tune the Slack proposals](/guides/workflows/#tune-the-slack-proposals).

## Projects

A project is an outcome that takes more than one action. `p` on a task makes a project of it or attaches it to one. Its actions are its children: each names the project as its parent and keeps its place among them. A project is parallel or sequential; a sequential one shows only its first open action in Next. Each project has a review interval, 7 days by default, kept on its task, and opens its task's [dossier](/concepts/turns/#dossiers).

## The review

The review (`g r`) walks six steps, each a view you mark done with `Enter` (`]` and `[` move between steps):

1. Inbox to zero;
2. the Outbox;
3. Waiting: nudge?
4. projects without a next action or past their review interval;
5. Someday;
6. prune Focus.

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
