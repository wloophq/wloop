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

A gate opens on the task its unit is on: the unit's own task, else the first open task that holds it, else a new captured task, titled by what the gate asks; a done or dropped task never takes a new gate. A gate asks in the words its opener rendered from the profile, kept with it so they outlive the unit: a workflow's plan gate reads the workflow's `ask` over its unit's facts, else its `summary` (the bundled `session-manual-quiet` asks "Close the quiet session <name>?"), a `gate.open` step renders its gate's `text` in `gates.toml` over the workflow facts of the unit and of the units its task holds (Review the diff reads "Review the diff of acme/api#12, 3 files changed"), and a gate with no words of its own reads its `label` in `gates.toml`. A step gate whose subject is gone is superseded by its workflow, never left for you to dismiss: the workflow's `[gates.withdraw]` says when, and the withdrawal records `gate_withdrawn` with its reason on the task. A draft that names no task takes the task its gate opened on, so that task carries it, and a draft whose gate waited on no task takes the task that later picks up its unit. A captured task opened only to hold gates ends once one of its gates closes and none is open, in the category that gate's `ends_task` in `gates.toml` names for the status it closed with; the bundled draft gate ends it done when the draft was approved and in the trash when it was discarded or withdrawn, and the bundled plan gate ends it done when it ran and in the trash when it was skipped, dismissed or withdrawn. A workflow withdraws its plan gate when the item no longer waits: its unit left its plugin's inventory, its `when` no longer holds or its key moved, so the offer leaves the Inbox on its own. At start the daemon also ends every captured task left open by gates that closed before their `ends_task` said so. A person's edit, which replaces the draft's gate, leaves it open. At start the daemon gives each held draft of no task the task its gate waits on, withdraws one whose task has ended, as an ending verb would have, and ends each such task whose gates all closed while it stayed open. A question, post or proposal gate whose task closes moves to another the next time its unit is reconciled. A proposal gate of a verb its profile holds behind a gate opens under the approval of what asked for it: the asking workflow's `[gates.approval]`, and `gates.toml`'s `[approval]` for a person's or an agent's request. Approved by you, it runs that verb as you, through the same path your own request takes, so a delegate or an approve still runs its unit's action; released by a rule or a window, it runs the verb as the workflow that asked. A proposal that pairs a task with one you took apart from it (`task.undone`: a rejected gate of the pair, a split or an unrelate you ran) waits for you, whatever the asking workflow's approval says, so no rule redoes what you undid: taking the pair apart supersedes its open proposals, and one a rule or a window releases later is refused when it runs.

A gate is resolved only three ways: by you, through a client or an agent session you drive; by a trusted policy rule, a rule of a `[gates.approval]` table of your profile or configuration; or by its window, when a `delay` gate's deadline passes and it takes its default intent, recorded with the rule `window`. A session is one you drive when the loop never typed into it, or you prompted it since the loop last did, and no turn the loop sent runs in it: your own assistant session always is, a worker session becomes one when you type into it, and stops being one when the loop types its next turn. The gate records the session it was resolved through. An unattended session, even one that runs `{{command}}`, never resolves a gate; no session resolves a gate whose draft or question its own turn produced; and no agent's or thread's text picks a rule. A post an agent makes at once with a Slack tool is recorded as a gate already approved, which is no resolution.

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

An unattended agent's session resolves none of these: the row actions and `POST /api/plan` answer it 403 and leave the gate open, and the same holds for Run now on a round the cap held. A session you drive resolves them as you do. A trust gate decides only the block it was opened on: once another block replaces it, its gate is superseded, a new one opens on the new block, and an answer to the old gate trusts nothing.

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

### Drafts the daemon posts

A draft is a kind of its plugin, which declares its kinds, deliveries and schemas in its manifest: GitHub's comment, reply, nudge, close, ask_owner, react, edit, review (events COMMENT, REQUEST_CHANGES and APPROVE, with inline comments), thread_reply, resolve and request_review; Slack's reply, nudge, close, ask_owner, react, unreact, edit and delete, over the message, reaction, unreaction, edit and delete deliveries. Each kind's preview template lives in the plugin's profile, under `plugins/<id>/profile/drafts/<kind>/`. A draft carries its payload, which its kind's schema checks, and the daemon posts exactly that payload. The card shows all of it.

Four properties make the post safe:

- **The gate approves a revision.** A draft's rev counts its content: what it posts (its unit, the items that place a draft with no target, its kind, delivery, target, text, reaction, broadcast, payload and basis) and the effects its post applies. It is 1 as proposed and grows only when that content changes; a state move, such as its queueing, or the task that adopts it leaves it as it is. A gate records the rev it approved, and the sender posts only a draft still at that rev. A person's edit makes a new rev and a new gate. An agent's revision of a draft also supersedes its gate and opens one that waits, and a failed draft that anyone revises goes back to held, so no approval of the old content covers the new. A draft moved to another unit, as when a Slack thread is re-keyed, is a new revision: one held or approved waits for you again on a new gate at its new rev.
- **A proposer never resolves its gate.** The gate stamps every session that proposed or revised the draft; none of them may resolve it, and a person or a session you drive that did not touch it may.
- **A basis ties a draft to what the agent read.** A review's basis is the head it was read at. Propose checks it against the live head, a stale draft cannot be approved, and the publisher re-reads the live head before it posts, refusing on a mismatch. A draft refused or failed as stale keeps that cause until its content is revised, so its card, the `draft.stale` fact and a resumed turn's instructions all say it is stale; otherwise its card compares the basis with a read of the head shared among the cards for 30 seconds.
- **Rules are allow-lists over the payload.** A kind declares the payload fields a rule may read (a GitHub review's event, as `draft.github.review.event`). They are trusted, because they are what is posted, and a rule reads them as an allow-list: an absent fact never releases a draft. `hold` on a draft keeps it out of every rule, except under `dry_run`.

The bundled review and address intents ship these rules, and APPROVE matches none of them:

```toml
[[gates.approval.rule]]
when = 'draft.delivery == "review" && draft.github.review.event in ["COMMENT", "REQUEST_CHANGES"]'
mode = "send"
[[gates.approval.rule]]
when = 'draft.delivery == "reply"'
mode = "send"
[[gates.approval.rule]]
when = 'draft.delivery == "resolve"'
mode = "send"
```

The address intent also sends the round's review requests (`draft.delivery == "request_review"`) and its top-level comment (`draft.delivery == "comment"`), which tells the reviewers what changed and asks for the next look, so the round's handoff posts with its replies. A review round's top-level comment waits for you.

A session reaches its drafts through `mcp__{{name}}__draft_read`, `draft_update` and `draft_withdraw`, the proposing tool of the plugin (`github_draft`, `slack_draft`; `github_threads` reads the unresolved review threads and their comment ids, to reply to or resolve) and `{{command}} draft propose|read|update|withdraw`, which give the same results. You act on the same drafts with `{{command}} outbox approve <id> [--text <markdown> --rev <n>]` and `outbox edit <id> <markdown> --rev <n>`; the rev names the revision you saw, and a draft that moved meanwhile is refused. An agent updates or withdraws only a draft of its own turn or session, on its unit; a person's draft and a workflow's are out of reach, and an update at a stale rev is refused as a conflict.

A question's option may point to a draft, pinned to its id and rev: a draft its turn proposed on the question's unit, or one an earlier turn proposed that the turn resumes or replaces, as when a free answer resumes the turn and the agent revises the draft and asks again. When you pick the option, the gate of the draft is approved as you, the drafts of the other options are withdrawn, and the turn completes with the posted draft's permalink. A question pins only a draft that waits for you, held with its gate open: `turn_end` refuses a record whose option or confirm points to a draft a rule already released, sent, dropped or missing, naming each such pin, and the agent proposes the draft again with `hold`. A pinned draft that posts without a pick, as a revision a rule released, leaves the question open to decide, and retrying a pinned draft that failed is refused while its question decides it, unless the answer picked it. The pick withdraws the draft of every other option that has not posted, held, queued or failed alike. Only the pick approves a draft a question pins: your approval policy's rules, at the gate's open or later, and a delay's window passing leave it waiting for the question. A question that closes without picking a draft, dismissed or answered in your own words, takes the window off each draft it pinned: the draft stays, waiting for your own approval with no countdown, since no rule releases a draft its question was asked about. A pick of an option whose draft was revised since it was pinned answers the question alone, and the turn resumes. A session that proposed or revised the draft cannot pick its option: the pick is refused, and the question stays open. A question is one decision: while it is open, the drafts its options (or its confirm) pin show under those options only, in the cockpit, the terminal UI, the Outbox and the notifications, never as cards of their own, and approving such a draft by any route (a card, `outbox approve`, the task's approve verb, the API) picks the option that pins it, with the same effect and the same refusal. While the question is open, a direct approval of a draft it pins at another rev (the agent revised it) is refused, and a person's change of a pinned draft is refused too: the question decides it, and a pick of a pin the agent has since revised resumes the turn, so the agent asks again with the new rev. One decision runs on a question at a time: an approval or a pick that arrives while another is deciding the same question is refused, and once the question is answered, approving another of its drafts is refused as well. A send that fails after the turn completed becomes facts on the unit: `draft.failed`, `draft.stale` (the unit moved past what the draft was written against), `draft.failed_kind` and `draft.failed_ref` (`<id>:<rev>`), which a workflow's `when` reads; the core restarts nothing on its own. A turn ends on a draft with `{{command}} turn end <url> --turn <id> --file <path> --draft <id>:<rev>`, which sets the record's `outcome.draft`.

A draft a rule sent notifies "posted by your rule".

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

A gate a rule or a window approved whose draft its publisher now posts as another delivery than the rule decided, or whose workflow no longer loads, fails the draft for you to retry, so no rule posts what it never named. Editing a held draft, its text or a thread reply's broadcast, supersedes its gate, so an approval you gave the draft you saw never posts another; approving through `POST /api/gates/{id}/{intent}` names the gate you saw, and approving with other text edits the draft first, so the gate the edit opens is the one approved. Only you resolve a draft's gate there, yourself or through a session you drive that did not propose the draft: any other session is refused before anything is written.

A draft you write yourself (Save as draft on a task's page, Reply on a unit, see [Tasks and views](/concepts/tasks/), `outbox new`, or `slack send` to a thread) is `wait` too: Save to Outbox leaves it there, and Send now approves it right after proposing it, through the same publisher. A draft an agent session writes on these routes (`POST /api/drafts`, the reply verb, `outbox new --send` from its shell) is the session's, never yours: it is stamped with the session, which never approves it, and its Send now goes through your approval policy as any agent's draft does, the policy of the session's turn, which waits unless a rule of yours sends it. The answer says which: `draft_rule_sends` or `draft_waits` in `notices.toml`. A reply you write in a thread can ask for its broadcast (the task page's **Also send to the conversation**, the reply verb's `broadcast`, `broadcast` on `POST /api/drafts`, `slack send --broadcast`); its publisher keeps the ask on a thread reply and ignores it on one in no thread.

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

A draft has a purpose (nudge, close, ask_owner, or any label its proposer names), a delivery (a message, a reaction, a comment, an edit or a deletion) and the effects sending it applies, each exactly once, after a restart too:

| Effect | What it does once the draft is sent |
|---|---|
| `task.nudged` | stamps the last nudge of the draft's task, or of every live waiting task of its unit |
| `unit.closed` | records the unit's closure through its plugin, which on Slack lets the thread go unwatched after its warm period whatever later extraction passes say, then ends the open tasks that hold the unit |
| `pr.nudged` | records the nudge on the pull request, which the next nudge's refusal reads |

A draft's card in the cockpit says what sending it does in the words of `gates.toml`'s `[effects]`, one sentence per effect name, which a profile or a plugin's layer may reword; a key that names no effect fails to load.

Nothing follows from a draft's purpose: a closing message with no `unit.closed` closes nothing. An agent's draft may declare only the effects its intent lists in `[drafts] allowed_effects`; the draft is refused otherwise, with a message naming the intent. A draft declaring an effect its unit's provider does not implement, such as `pr.nudged` on a Slack thread, is refused when it is proposed. A sent draft whose effect cannot apply, such as `unit.closed` while its plugin is off, keeps it pending until it can.

A workflow proposes drafts with its `draft.propose` step, its text rendered from the workflow's `text.md.tmpl` through the profile's layers, the items' text as data, and its why from `why.md.tmpl` beside it (see [What a draft says about itself](#what-a-draft-says-about-itself)). It skips a draft of the same workflow, unit and person while one is open or failed, for `repeat_after_sent` after one was sent (`forever` never proposes again) and for `repeat_after` after one was otherwise settled. Its `[drafts.withdraw] when` drops an open draft it no longer calls for, superseding its gate, with the reason `moot` or, with `reason = "assigned"`, assigned; a draft whose workflow is gone is withdrawn `moot`, as is a plugin rule's draft its plugin withdraws, such as a GitHub nudge on a pull request no longer stalled; no draft of a disabled plugin is, nor one of a workflow the configuration turns off (`workflows.<name>.enabled = false`, which stops its new drafts only; a workflow your profile turns off keeps them too, while one the bundled profile ships off and the configuration never turned on has its held drafts withdrawn `moot`, since they predate it shipping off), nor one of a workflow whose manifest fails to load: when its window ends while the manifest fails, the draft fails for you to retry. A draft its `[drafts.withdraw] when` would withdraw at once is not proposed. A verb that ends a task (moves it from an open state to a done or dropped one, as done, reference, dismiss and not mine do) withdraws the held drafts the task carries with the reason `ended`, but those of a purpose the verb's `keep_drafts` in `tasks.toml` names; the bundled verbs keep none. A task reconcile ends, or one its unit's closing ends, withdraws them all the same way. Unlike a draft withdrawn `moot`, one withdrawn `ended` holds its workflow on the unit, for its person, while that task stays ended, so closing tasks never makes a workflow such as `slack-closure` or `slack-stalled-nudge` propose again on their threads. The hold covers a proposal of no task or of that same task: another task captured on the unit is not held by it. Once the task reopens, the draft holds like any settled one, for `repeat_after`; a step with `ended = "repeat"` treats it that way from the start.

### What a draft says about itself

A workflow's draft carries its **why**, rendered from the workflow's `why.md.tmpl` in your language over the same data as its text (its items, when the oldest was asked as `.Since`, the people it is to as `.People`); a workflow with no such file proposes with no why. Its text is written in the language of its conversation: a source plugin names, with its manifest's `language_fact`, the fact that tells a unit's language (the Slack plugin's `slack.language`), and the draft takes the template of the matching locale, `locales/<tag>/workflows/<name>/text.md.tmpl`, by the same layer rule as every other text; a conversation the plugin cannot tell is written in your language, and one in a language no layer translates in the default, English. The draft's `language` says which one its text is in.

A draft with `items = "owed"` and no `per = "person"` is one draft per conversation: it mentions everyone the subject's open items wait on, you left out, and lists what they owe; on a task, those are the task's own items, so a task you deferred, muted or snoozed on the same thread is neither listed nor marked nudged. Whoever asks you something there is left out with the items only they owe, unless everyone does, and then it is not proposed. When another task on the conversation comes due while that draft is held, the step proposes one draft over both tasks' items and people and withdraws the held one as `superseded`; a draft that did not list a task's items does not hold that task's nudge. An approved per-person draft of the workflow on the conversation holds it until it is sent, and proposing it withdraws the workflow's per-person drafts still held there as `superseded`. `slack-stalled-nudge` nudges this way.

Three operations sit on a draft's card beside approve and discard, as the draft's actions (`draft.close-silently`, `draft.skip-workflow`, each with the reason it is disabled) and on the daemon's API:

- **Close without a message** (`POST /api/drafts/{id}/close-silently`) discards a held draft through its gate, as you, and applies its effects as if it was sent, without posting it; only a draft whose every effect applies without a post may, such as `unit.closed` (an effect's `silent`, which a plugin's manifest may set on its own); a draft with `task.nudged`, which says someone was nudged, may not. Its reason becomes `closed_silently`, and an effect that fails is applied again at the next pass. A workflow applies such effects with no draft at all through its `effects.apply` step, as the bundled `slack-closure-quiet` closes a thread you started; a step that names an effect needing a post, or one its unit's plugin does not implement, fails to load or is refused.
- **Don't nudge this conversation** (`POST /api/units/{provider}/{key}/skip/{workflow}`, `DELETE` to undo), offered on a draft that nudges (`task.nudged`), stops the draft's workflow on its unit until you undo it, and withdraws the workflow's drafts held there as `skipped`; the task stays where it is. A workflow plans nothing on a skipped unit, nor on a task one of whose units is skipped, and a draft its workflow proposed on such a subject is withdrawn `skipped` at the next sweep. A unit's detail lists its `skipped_workflows`.
- **Stop drafting these** (`POST /api/workflows/{name}/disable`), on the head of a workflow's group in the Outbox, writes `workflows.<name>.enabled = false` to the configuration, with a backup, and applies it without a restart; the drafts it already holds stay in the Outbox.

Only you run them, yourself or through a session you drive. A task lists its pending drafts, held or failed, in its `drafts`, and `GET /api/drafts?task=<id>` serves their cards.

### The Outbox grouped by why

The cockpit draws a view whose `layout` is `outbox` (the bundled Outbox) from `GET /api/outbox`: the same drafts `GET /api/drafts` lists, in groups by why they were drafted.

```text
draft ──> its workflow? ──yes──> the workflow's group: [outbox] title and about (summary, else its name)
              │ no
              v
          you wrote it? ──yes──> written by hand: gates.toml's drafts.by_hand, by_hand_about
              │ no
              v
          its purpose's group: gates.toml's drafts.purposes.<purpose> (the purpose as named when absent)
```

The largest group comes first, then by title; inside a group the drafts keep the Outbox's order, newest first. Every text is the profile's, in the interface language: a drafting workflow's `[outbox]` table (`title`, `about`) with its translation under `locales/<tag>/workflows/<name>/workflow.toml`, and `gates.toml`'s `[drafts]`. A workflow's group shows its name and **Stop drafting these**, which asks again inline before it disables the workflow.

Each draft comes with its context: the asks of its task, each with the quotes its brief keeps, and the items it is about, each with its state. A draft's card names its purpose and, once it left your hands, its delivery state in `gates.toml`'s `[drafts.purposes]` and `[drafts.states]` words, which the views catalog serves as `draft_purposes` and `draft_states`.

```sh
{{command}} outbox                      # drafts that wait on you, by purpose
{{command}} outbox show <id>            # one draft as it reads: its preview line, its text, every value of its payload (a review's verdict and inline comments), its items and the commands it takes
{{command}} outbox new <unit|message> <markdown> # a reply of yours; --react <emoji> for a reaction (on the Slack message a ref or permalink names), --send to post it now
{{command}} outbox approve <id>         # shows it, payload included, and approves its gate once you say yes; --text <markdown> --rev <n> posts your text instead
{{command}} outbox edit <id> <markdown> --rev <n> # n is the rev outbox show printed; a revision since then is a conflict
{{command}} outbox discard <id>         # discards its gate; --reason "not needed" drops a draft its gate approved
{{command}} outbox retry <id>           # posts a failed draft again
```

On a terminal the list groups the drafts by purpose (nudges, closings, replies, ...), each with its id, its unit as its row reads (`acme/api#7 · <title>`, `#channel · <thread>`, `DM with <name>`), who it mentions, its age and its first line; a column shows only where the drafts differ, such as the state when one failed. Piped, it prints one tab-separated line per draft for scripts: id, kind, state, unit, label, mentions, creation time (RFC 3339) and first line. A unit is a pull request URL or `owner/repo#n`, a Slack message ref (`<conversation>/<ts>`) or permalink (its thread) or `<provider>:<key>`.

`outbox approve`, `outbox retry` and `outbox new --send` refuse when they find no terminal, and they, `outbox edit` and `outbox discard` refuse when they detect an agent session's environment that names no workspace manager's handle; from a session that names one, the daemon takes the command for yours only when you drive that session. Even then, an agent session never changes a draft's text or payload through these commands or the card's routes, which the Outbox itself refuses: it revises a draft with `draft_update`, which stamps the revision with the session, and a session that revised a draft never approves it. A person changes only what the draft's kind lets them (its `editable`), which the Outbox checks too, whatever the route. That is a guard against mistakes, not a security boundary: any process running as you can reach the daemon's socket. A draft still `sending` after a crash is checked against its destination, marked `sent` when it went out and `failed` when that cannot be told, so a crash never posts twice.

The review, address and nudge rounds on pull requests write nothing to GitHub from their session: they propose drafts, and the daemon's publisher posts a draft once its gate approves it, under the approval window and the ambiguity gate above. The tools an agent uses to post, react, edit or delete on Slack as you right away do so without your approval; the Outbox records each such post as a sent draft on the unit it landed in, its gate recorded as already approved by the agent with the tool as its rule, tracked or not, with the turn that made it, an edit's record keeps the text it replaced and a deletion's the text the message held, so its history shows everything an agent posted or changed in your name. `slack_draft` only proposes drafts, so a session's policy allows it too.

A draft or a post names the turn that proposed it, and that turn's unit when it is not the draft's: a pull request's turn that drafts in a team's Slack thread shows in the Outbox as `turn <n> (<intent>) on <owner>/<repo>#<n>`. Such a draft answers none of the turn's items, declares no effect and, aimed outside the turn's unit, waits for you.
