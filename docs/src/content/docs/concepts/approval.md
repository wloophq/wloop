---
title: What waits for you
description: The gates that wait for you, the approval window of automatic rounds, the ambiguity gate inside a round, and the drafts' gates the Outbox lists.
---

Every decision that waits for you is a **gate**: a draft to send, a round to run, an agent's question or post, a proposal, a repository's first run, a workflow's `gate.open`. Three mechanisms keep the loop from acting where you would not:

1. the **approval window** announces every automatic round before it goes out;
2. the **ambiguity gate** stops a round that is unsure and asks you before publishing;
3. a **draft gate** holds every draft that an agent or a rule proposes to post until it lets the draft out; the **Outbox** lists them.

## Gates

A gate has a kind (`draft`, `plan`, `question`, `post`, `proposal`, `trust` or `step`), which names what resolving it does: publish the draft, release the round, deliver the answer to its session, apply the proposal, unblock the first run, or, for a `step` gate, record the event `gate_resolved` on its task (its key `<name>/<id>`, its data the intent, its actor who resolved it), which a workflow's `trigger.on` names. `gates.toml` words each gate and declares its intents, the buttons you resolve it with, in the order clients show them; each intent says the status it resolves the gate to (`approved`, `rejected`, `answered`, `dismissed`) and whether it takes text. A gate the loop opens by its kind takes the profile's first gate of that kind, so a profile declares one of each kind, or fails to load.

A gate opens on the task its unit is on: the unit's own task, else the first open task that holds it, else a new captured task; a done or dropped task never takes a new gate. A draft that names no task takes the task its gate opened on, so that task carries it, and a draft whose gate waited on no task takes the task that later picks up its unit. A captured task opened only to hold gates ends once one of its gates closes and none is open, in the category that gate's `ends_task` in `gates.toml` names for the status it closed with; the bundled draft gate ends it done when the draft was approved and in the trash when it was discarded or withdrawn, and no other bundled gate ends it. A person's edit, which replaces the draft's gate, leaves it open. At start the daemon gives each held draft of no task the task its gate waits on, withdraws one whose task has ended, as an ending verb would have, and ends each such task whose gates all closed while it stayed open. A question, post or proposal gate whose task closes moves to another the next time its unit is reconciled. A proposal gate of a verb its profile holds behind a gate runs that verb as you once you approve it, through the same path your own request takes, so a delegate or an approve still runs its unit's action.

A gate is resolved only three ways: by you, through a client; by a trusted policy rule, a rule of a `[gates.approval]` table of your profile or configuration; or by its window, when a `delay` gate's deadline passes and it takes its default intent, recorded with the rule `window`. An agent's session, even one that runs `{{command}}`, never resolves a gate, and no agent's or thread's text picks a rule. A post an agent makes at once with a Slack tool is recorded as a gate already approved, which is no resolution.

A gate's resolution is written before its consequence runs, and the gate records when its consequence ran. When the daemon stops in between, its gates' first tick after the start runs each consequence that had not run, and every later tick does the same. A consequence runs once: a draft already queued or sent is not queued again, a proposal whose decision was applied applies nothing more, and an answer already given to its session is not given again. A round the daily cap held and a verb a gate held, which act in your name and leave no record a second run could read, are marked run before they run: a daemon that stops during one never runs it twice, and may leave it not run; its health then warns, naming the gate, so you check whether it ran and act again if it did not. A consequence that ran and failed stays failed, for you to retry: no tick retries it.

Every approval is a `[gates.approval]` table, in an intent's manifest, a workflow's, `config.toml`'s `workflows.<name>.gates.approval` and `plugins.<id>.gates.approval`. Its rules are tried before its `mode`; a rule applies to the kinds of gate its `when` pins `gate.kind` to, to draft gates alone when it reads a draft's facts, else to every kind. A rule whose `when` reads `gate.kind` alone decides every gate of its kind, as that kind's mode. A rule that reaches draft gates but reads a fact no draft gate's rule may read, an untrusted one or a second unit's, fails to load rather than being left to the other kinds, since a later rule would then decide the draft; pin it to the kinds it means, such as `gate.kind == "plan"`. What no table decides, `gates.toml`'s `[approval]` does: the bundled one starts a run at once and holds every other gate for you.

| Mode | The gate |
|---|---|
| `wait` | waits for you |
| `delay` | resolves to its approving intent when its `window` ends, unless you resolve it first |
| `send`, `run` | is released at once |
| `dry_run` | a draft gate only: the draft is stored as what would have been sent, and never sent; one pinned to another kind fails to load, and on any other kind the next rule or the default decides |

## The approval window

An automatic review, address or nudge round is first a pending item that waits on its `plan` gate; any workflow item that waits for you, or for its window, does too, and its gate is closed as superseded once the item leaves the plan. A workflow in observe mode opens no gate, and a step deferred while it runs asynchronously is no decision. The row moves to Needs me as "round soon" with a countdown, and on macOS the menu bar counts it and a notification `Round in 60 s · owner/repo #n` appears. Until the window ends you can:

| Do | How | Effect |
|---|---|---|
| Skip | Skip in the cockpit, the notification or the menu; `{{command}} plan dismiss <url>` | recorded as handled, without counting against the caps; it comes back only when something new happens on the pull request |
| Pause | `{{command}} pause <url>`, or Pause | no automatic round runs on the pull request until `{{command}} resume <url>` |
| Run now | Run now, `R` in the terminal client, `{{command}} plan apply <url>` | the round is sent at once |
| Steer | the guidance box and Run now in the cockpit, or the notification's Guide field | the round is sent at once with your text as its guidance |

Skip, Run now and Guide are the `skip`, `run_now` and `guide` intents of the `plan` gate in `gates.toml`, which words them; the row offers them while the gate is open, and `{{command}} plan apply` and `plan dismiss` on a waiting item resolve its gate too. A round the daily cap held waits on a `plan` gate of its own, for its local day, which no approval rule resolves: Run now approves it and runs the round by hand, and the next day's cap supersedes it. With no round pending or held, Run now runs a round the daily cap held and Guide keeps your text for the next round. A round whose start the agent blocked on its question, such as trusting its folder, waits on a `trust` gate, opened when the block is recorded and closed once it lifts: Trust folder and run and Dismiss block are its `trust_run` and `unblock` intents, and no approval rule resolves it. An Orca card stays in Needs me while a gate of its unit is open.

An agent's session resolves none of these: the row actions and `POST /api/plan` answer it 403 and leave the gate open, and the same holds for Run now on a round the cap held. A trust gate decides only the block it was opened on: once another block replaces it, its gate is superseded, a new one opens on the new block, and an answer to the old gate trusts nothing.

With no answer, the round runs when the window ends: the window is its gate's deadline, which the daemon's gates tick waits for, and the gate resolves to its default `run_now`, recorded with the rule `window`. The window is a rule of the workflow's `[gates.approval]` pinned to `gate.kind == "plan"` with `mode = "delay"`, `60s` in the bundled review and address workflows; a rule of `workflows.<name>.gates.approval` in the config pinned the same way changes it, and one with `mode = "run"` sends that workflow's rounds at once. A pending item survives a restart: one whose window ended while the daemon was down runs once when it starts.

Guidance is not lost when its round does not go out (a pause, a cap, a busy workspace): it is kept for the next round of that pull request, and the cockpit's detail shows it as "Next round guidance". Rounds you ask for (`review`, `address`, `plan apply`) do not wait.

A muted pull request (`{{command}} mute <url>`) stays muted: its round is not announced and goes out at the deadline unless you skip it or pause the pull request.

The nudge workflow's plan gates wait: each nudge stays pending until you apply it. See [Tune workflows](/guides/workflows/#let-nudges-go-out).

## The ambiguity gate

Inside a round, the procedure publishes on its own except when one of these holds; then it records `asked` with the full draft and waits for you:

- it is unsure whether a finding blocks, or the verdict depends on intent the pull request does not state;
- it would approve a change to an irreversible or risky surface (your profile lists them: schema migrations, data deletion, auth, billing, infrastructure by default);
- a finding alleges a security problem;
- the review would contradict a decision another human reviewer left. Bots and the logins in `plugins.github.automated_reviewers` never trigger this.

An address round stops the same way before a deep change of direction, an irreversible surface, or declining a blocking request.

## The Outbox

Every outbound post that an agent or a rule proposes (a reply, a nudge, a closing message, a Slack reaction) is a **draft** first, held by a **draft gate**: the gate holds the decision (approve or discard, in `gates.toml`'s words), the draft keeps only its delivery.

```text
held --(its gate approves it)--> queued --> sending --> sent
  |                                |   |        |
  v                                v   |        v
dropped (its gate refused it,  dropped +------> failed --> queued (retry, by hand)
         or its proposer withdrew it)
```

The sender posts only a queued draft whose gate approved it. A draft its gate approved but its publisher cannot take (the provider is off, or the draft cannot be placed) fails without ever being `sending`, so a restart never looks for a post that was never made. The Outbox lists the drafts whose gate is open and those whose gate approved them and whose delivery failed or a restart has yet to settle, each failed one with Retry; `gate.delivery` is that delivery state of a draft gate, for a view over gates.

A draft's gate opens under its approval policy:

| Policy | The gate |
|---|---|
| `wait` (default) | waits in the Outbox until you approve or discard the draft |
| `delay` | approves the draft once its `window` ends, at the gates' next check, at most 30 seconds later, unless you decide first; an edit supersedes the gate with one that waits |
| `send` | approves the draft at once |
| `dry_run` | discards the draft at once: it is stored as what would have been sent, and never sent |

A gate a rule or a window approved whose draft its publisher now posts as another delivery than the rule decided, or whose workflow no longer loads, fails the draft for you to retry, so no rule posts what it never named. Editing a held draft supersedes its gate, so an approval you gave the text you saw never posts another; approving through `POST /api/gates/{id}/{intent}` names the gate you saw, and approving with other text edits the draft first, so the gate the edit opens is the one approved. Only a person resolves a draft's gate there: an agent's session is refused before anything is written.

A draft you write yourself (Save as draft on a task's page, Reply on a unit, see [Tasks and views](/concepts/tasks/), `outbox new`, or `slack send` to a thread) is `wait` too: Save to Outbox leaves it there, and Send now approves it right after proposing it, through the same publisher.

Every bundled intent, workflow and rule leaves its drafts to `gates.toml`'s default, `wait`. Your own intent or workflow can set another with a `[gates.approval]` table, a workflow's drafts with `workflows.<name>.gates.approval` in the configuration, and the drafts of a plugin's own rules (GitHub's nudge rule) with `plugins.<id>.gates.approval`, such as `plugins.github.gates.approval`. A turn's draft takes the `[gates.approval]` of the workflow that started the turn when it names a draft gate, else its intent's, else `gates.toml`'s default; a workflow's own draft takes the workflow's, else the default. The configuration's `workflows.<name>.gates.approval` is laid over that table.

The rules of a `[gates.approval]` are tried in order before its `mode`; the first whose `when` holds gives the draft's mode, and a draft no rule matches takes `mode`, else the default:

```toml
[[gates.approval.rule]]
when = 'draft.delivery == "comment" && pr.author == "dependabot[bot]"'
mode = "send"

[[gates.approval.rule]]
when = 'draft.delivery == "message" && slack.conversation == "dm"'
mode = "delay"
window = "10m"
```

A rule reads the facts of the draft and of the unit the draft is on (a workflow's draft: its provider's). `draft.delivery` is how the unit's plugin posts the draft, not what its proposer asked for: GitHub posts every draft that is not a reaction or an edit as a comment on the pull request, and refuses those two, so an agent picks between a message and a reaction and never between a comment and something else. `draft.proposer` names the workflow, intent or plugin rule that proposed it. `draft.purpose` and `draft.effects` read as empty unless a workflow or a plugin rule named them; an agent names its own, so an intent's rules may not read them at all. A rule never reads a brief, which an agent writes, its extra fields or a classification's output, nor a fact the [facts](/reference/facts/#draft-approval-facts) page marks **Untrusted**: one whose value a third party's text sets through the extraction (such as `slack.started_by_me`), or that the proposing turn itself moves before its drafts are proposed (such as `slack.items_unowned`, which a turn's ledger changes). The loader refuses a rule that reads one, naming the manifest or the configuration key, the rule and the fact.

A rule applies only to a draft whose unit its facts describe: in an intent that serves Slack and GitHub, a rule over `slack.*` does not apply to a draft on a pull request, which falls through to the next rule, and a rule over the facts of two providers never applies, so the loader refuses it. When the unit's facts cannot be read, a draft whose rules read them waits for you, and the daemon logs why. A turn a workflow started reads its unit's facts as that workflow reads them (its `settled_at` included); a turn started by hand reads them unscoped. A rule's mode is `wait`, `delay` (with its `window`), `send`, `run` or `dry_run`. The configuration's `workflows.<name>.gates.approval` takes the same `mode`, `window` and `rule` list and overlays a table field by field, for every gate the workflow opens, its drafts and those of the turns it starts included: `mode` and `window` replace those fields, `rule` replaces the rules, and `[]` clears them; a key left out keeps the table's. For drafts it overlays the workflow's `[gates.approval]`; on a workflow that names no draft gate, the intent's for a turn's draft, and `gates.toml`'s default for the workflow's own. The configuration refuses at load a block no table could fix (an unknown mode, a `window` beside a mode other than `delay`, a rule without its mode or that reads an untrusted fact); one that fails only once merged, such as a `window` over a table that waits, holds every draft of the workflow for you, and `doctor` and the daemon's log name it.

Three floors hold whatever the policy says:

- a draft whose delivery posts, as its plugin's manifest marks it (a GitHub comment, a Slack message), waits for you unless a rule whose `when` has `draft.delivery == "<delivery>"` as a term of its top-level `&&` releases it with `send`, `run` or `delay`: a catch-all `mode = "send"`, or a rule that does not name its delivery, never posts a comment or a message under your name;
- a draft an agent aims outside its own unit waits for you, unless the policy is `dry_run`; so does a draft a session proposes on a Slack thread no turn of it is on, which follows no intent's policy;
- an agent never sets its own approval: the purpose and effects an agent gives its draft read as empty in a rule, an intent's rules may not read them at all, and no rule reads an untrusted fact.

No agent's or thread's text picks a draft's approval, and a run releases a draft only through it. A `[gates.approval]` rule that releases a gate (`send`, `run` or `delay`) may not read an untrusted fact, nor `trigger.event.data`, which carries an intake's note, an answer or a sent nudge's mentions; a `wait` rule may, since it only holds an item. A workflow's `when` reads any fact: it is the trigger, and the bundled workflows gate on facts an agent or the extraction moves, such as `pr.first.due` or `workspace.pendency`. So a `when` that reads one may start a turn or propose a draft without a person, never post: no step posts outside, a step that acts where others see it always waits for you, and the draft a run proposes goes out only under the mode you set on its workflow or a rule over trusted facts, with the floors above.

A rule that releases (`send`, `run` or `delay`) and reads `draft.delivery` in a form that names no delivery, such as `draft.delivery in ["comment"]`, loads with a warning naming the form that does. A configuration rule over another provider's facts than its workflow's is refused: `doctor` and the daemon's log say so, and every draft of that workflow waits for you until it is fixed.

The configuration's `workflows.slack-ask-owner.gates.approval` sets the approval of the questions `slack-ask-owner` proposes like those of any other workflow: a rule that names their delivery, `draft.delivery == "message"`, with `delay` sends them when the window ends, with `send` at once, though they name people other than you.

A draft has a purpose (nudge, close, ask_owner, or any label its proposer names), a delivery (a message, a reaction, a comment or an edit) and the effects sending it applies, each exactly once, after a restart too:

| Effect | What it does once the draft is sent |
|---|---|
| `task.nudged` | stamps the last nudge of the draft's task, or of every live waiting task of its unit |
| `unit.closed` | records the unit's closure through its plugin, which on Slack lets the thread go unwatched after its warm period whatever later extraction passes say, then ends the open tasks that hold the unit |
| `pr.nudged` | records the nudge on the pull request, which the next nudge's refusal reads |

Nothing follows from a draft's purpose: a closing message with no `unit.closed` closes nothing. An agent's draft may declare only the effects its intent lists in `[drafts] allowed_effects`; the draft is refused otherwise, with a message naming the intent. A draft declaring an effect its unit's provider does not implement, such as `pr.nudged` on a Slack thread, is refused when it is proposed. A sent draft whose effect cannot apply, such as `unit.closed` while its plugin is off, keeps it pending until it can.

A workflow proposes drafts with its `draft.propose` step, its text rendered from the workflow's `text.md.tmpl` through the profile's layers, the items' text as data. It skips a draft of the same workflow, unit and person while one is open or failed, for `repeat_after_sent` after one was sent (`forever` never proposes again) and for `repeat_after` after one was otherwise settled. Its `[drafts.withdraw] when` drops an open draft it no longer calls for, superseding its gate, with the reason `moot` or, with `reason = "assigned"`, assigned; a draft whose workflow is gone or disabled is withdrawn `moot`; no draft of a disabled plugin is, nor one of a workflow whose manifest fails to load: when its window ends while the manifest fails, the draft fails for you to retry. A draft its `[drafts.withdraw] when` would withdraw at once is not proposed. A verb that ends a task (moves it from an open state to a done or dropped one, as done, reference, dismiss and not mine do) withdraws the held drafts the task carries with the reason `ended`, but those of a purpose the verb's `keep_drafts` in `tasks.toml` names; the bundled verbs keep none. A task reconcile ends, or one its unit's closing ends, withdraws them all the same way. Unlike a draft withdrawn `moot`, one withdrawn `ended` holds its workflow on the unit, for its person, while that task stays ended, so closing tasks never makes a workflow such as `slack-closure` or `slack-stalled-nudge` propose again on their threads. The hold covers a proposal of no task or of that same task: another task captured on the unit is not held by it. Once the task reopens, the draft holds like any settled one, for `repeat_after`; a step with `ended = "repeat"` treats it that way from the start.

```sh
{{command}} outbox                      # drafts that wait on you, by purpose
{{command}} outbox show <id>            # one draft as it reads, its items and the commands it takes
{{command}} outbox new <unit|message> <markdown> # a reply of yours; --react <emoji> for a reaction (on the Slack message a ref or permalink names), --send to post it now
{{command}} outbox approve <id>         # shows it and approves its gate once you say yes
{{command}} outbox edit <id> <markdown>
{{command}} outbox discard <id>         # discards its gate; --reason "not needed" drops a draft its gate approved
{{command}} outbox retry <id>           # posts a failed draft again
```

On a terminal the list groups the drafts by purpose (nudges, closings, replies, ...), each with its id, its unit as its row reads (`acme/api#7 · <title>`, `#channel · <thread>`, `DM with <name>`), who it mentions, its age and its first line; a column shows only where the drafts differ, such as the state when one failed. Piped, it prints one tab-separated line per draft for scripts: id, kind, state, unit, label, mentions, creation time (RFC 3339) and first line. A unit is a pull request URL or `owner/repo#n`, a Slack message ref (`<conversation>/<ts>`) or permalink (its thread) or `<provider>:<key>`.

`outbox approve`, `outbox retry` and `outbox new --send` refuse when they detect an agent session's environment or no terminal. That is a guard against mistakes, not a security boundary: any process running as you can reach the daemon's socket. A draft still `sending` after a crash is checked against its destination, marked `sent` when it went out and `failed` when that cannot be told, so a crash never posts twice.

The review, address and nudge rounds on pull requests publish through `gh` from their own session, under the approval window and the ambiguity gate above. The tools an agent uses to post, react or edit on Slack as you right away do so without your approval; the Outbox records each such post as a sent draft on the unit it landed in, its gate recorded as already approved by the agent with the tool as its rule, tracked or not, with the turn that made it, and an edit's record keeps the text it replaced, so its history shows everything an agent posted or changed in your name. `slack_draft` only proposes drafts, so a session's policy allows it too.

A draft or a post names the turn that proposed it, and that turn's unit when it is not the draft's: a pull request's turn that drafts in a team's Slack thread shows in the Outbox as `turn <n> (<intent>) on <owner>/<repo>#<n>`. Such a draft answers none of the turn's items, declares no effect and, aimed outside the turn's unit, waits for you.
