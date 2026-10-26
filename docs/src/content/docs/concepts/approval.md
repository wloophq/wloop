---
title: What waits for you
description: The approval window of automatic rounds, the ambiguity gate inside a round, and the Outbox of drafts.
---

Three mechanisms keep the loop from acting where you would not:

1. the **approval window** announces every automatic round before it goes out;
2. the **ambiguity gate** stops a round that is unsure and asks you before publishing;
3. the **Outbox** holds every draft that an agent or a rule proposes to post, until its approval policy lets it out.

## The approval window

An automatic review, address or nudge round is first a pending item. The row moves to Needs me as "round soon" with a countdown, and on macOS the menu bar counts it and a notification `Round in 60 s · owner/repo #n` appears. Until the window ends you can:

| Do | How | Effect |
|---|---|---|
| Skip | Skip in the cockpit, the notification or the menu; `{{command}} plan dismiss <url>` | recorded as handled, without counting against the caps; it comes back only when something new happens on the pull request |
| Pause | `{{command}} pause <url>`, or Pause | no automatic round runs on the pull request until `{{command}} resume <url>` |
| Run now | Run now, `R` in the terminal client, `{{command}} plan apply <url>` | the round is sent at once |
| Steer | the guidance box and Run now in the cockpit, or the notification's Guide field | the round is sent at once with your text as its guidance |

With no answer, the round runs when the window ends. The window is the workflow's `[approval] window` under `mode = "delay"`, `60s` in the bundled review and address workflows; `workflows.<name>.approval.window` in the config changes it, and `"approval": {"mode": "run"}` sends that workflow's rounds at once. A pending item survives a restart: one whose window ended while the daemon was down runs once when it starts.

Guidance is not lost when its round does not go out (a pause, a cap, a busy workspace): it is kept for the next round of that pull request, and the cockpit's detail shows it as "Next round guidance". Rounds you ask for (`review`, `address`, `plan apply`) do not wait.

A muted pull request (`{{command}} mute <url>`) stays muted: its round is not announced and goes out at the deadline unless you skip it or pause the pull request.

The nudge workflow ships with `[approval] mode = "wait"`: each nudge stays pending until you apply it. See [Tune workflows](/guides/workflows/#let-nudges-go-out).

## The ambiguity gate

Inside a round, the procedure publishes on its own except when one of these holds; then it records `asked` with the full draft and waits for you:

- it is unsure whether a finding blocks, or the verdict depends on intent the pull request does not state;
- it would approve a change to an irreversible or risky surface (your profile lists them: schema migrations, data deletion, auth, billing, infrastructure by default);
- a finding alleges a security problem;
- the review would contradict a decision another human reviewer left. Bots and the logins in `github.automated_reviewers` never trigger this.

An address round stops the same way before a deep change of direction, an irreversible surface, or declining a blocking request.

## The Outbox

Every outbound post that an agent or a rule proposes (a reply, a nudge, a closing message, a Slack reaction) is a **draft** first, in the Outbox:

```text
proposed --> approved --> sending --> sent
   |            |            |
   v            v            v
discarded    discarded     failed --> approved (retry, by hand)
```

A draft goes out only once its approval policy lets it:

| Policy | The draft |
|---|---|
| `wait` (default) | waits in the Outbox until you approve it |
| `delay` | is sent when its `window` ends unless you discard or edit it first; an edit makes it `wait` |
| `send` | is sent at once |
| `dry_run` | is stored as what would have been sent, and never sent |

A draft you write yourself (Reply on an entry or a unit, see [GTD](/concepts/gtd/), `outbox new`, or `slack send` to a thread) is `wait` too: Save to Outbox leaves it there, and Send now approves it right after proposing it, through the same publisher.

Every bundled intent, workflow and rule uses `wait`. Your own intent or workflow can set another with a `[drafts.approval]` table, a workflow's drafts with `workflows.<name>.drafts.approval` in the configuration, and the drafts of a plugin's own rules (GitHub's nudge rule) with `<id>.drafts.approval`, such as `github.drafts.approval`. A turn's draft takes the `[drafts.approval]` of the workflow that started the turn, else its intent's, else `wait`; a workflow's own draft takes the workflow's, else `wait`. The configuration's `workflows.<name>.drafts.approval` is laid over that block.

A `[drafts.approval]` table holds a `mode` (`wait` when left out) and, before it, rules tried in order; the first whose `when` holds gives the draft's mode, and a draft no rule matches takes `mode`:

```toml
[drafts.approval]
mode = "wait"

[[drafts.approval.rule]]
when = 'draft.delivery == "comment" && pr.author == "dependabot[bot]"'
mode = "send"

[[drafts.approval.rule]]
when = 'draft.delivery == "message" && slack.conversation == "dm"'
mode = "delay"
window = "10m"
```

A rule reads the facts of the draft and of the unit the draft is on (a workflow's draft: its provider's). `draft.delivery` is how the unit's plugin posts the draft, not what its proposer asked for: GitHub posts every draft that is not a reaction or an edit as a comment on the pull request, and refuses those two, so an agent picks between a message and a reaction and never between a comment and something else. `draft.proposer` names the workflow, intent or plugin rule that proposed it. `draft.purpose` and `draft.effects` read as empty unless a workflow or a plugin rule named them; an agent names its own, so an intent's rules may not read them at all. A rule never reads a brief, which an agent writes, its extra fields or a classification's output, nor a fact the [facts](/reference/facts/#draft-approval-facts) page marks **Untrusted**: one whose value a third party's text sets through the extraction (such as `slack.started_by_me`), or that the proposing turn itself moves before its drafts are proposed (such as `slack.items_unowned`, which a turn's ledger changes). The loader refuses a rule that reads one, naming the manifest or the configuration key, the rule and the fact.

A rule applies only to a draft whose unit its facts describe: in an intent that serves Slack and GitHub, a rule over `slack.*` does not apply to a draft on a pull request, which falls through to the next rule, and a rule over the facts of two providers never applies, so the loader refuses it. When the unit's facts cannot be read, a draft whose rules read them waits for you, and the daemon logs why. A turn a workflow started reads its unit's facts as that workflow reads them (its `settled_at` included); a turn started by hand reads them unscoped. A rule's mode is `wait`, `delay` (with its `window`), `send` or `dry_run`. The configuration's `workflows.<name>.drafts.approval` takes the same `mode`, `window` and `rule` list and overlays a table field by field, for the drafts the workflow proposes and those of the turns it starts: `mode` and `window` replace those fields, `rule` replaces the rules, and `[]` clears them; a key left out keeps the table's. It overlays the workflow's `[drafts.approval]`; on a workflow that has none, the intent's for a turn's draft, and `wait` for the workflow's own. The configuration refuses at load a block no table could fix (an unknown mode, a `window` beside a mode other than `delay`, a rule without its mode or that reads an untrusted fact); one that fails only once merged, such as a `window` over a table that waits, holds every draft of the workflow for you, and `doctor` and the daemon's log name it.

Three floors hold whatever the policy says:

- a comment draft waits for you unless a rule whose `when` has `draft.delivery == "comment"` as a term of its top-level `&&` releases it with `send` or `delay`: a catch-all `mode = "send"`, or a rule that does not name comment delivery, never sends a comment on a pull request under your name;
- a draft an agent aims outside its own unit waits for you, unless the policy is `dry_run`; so does a draft a session proposes on a Slack thread no turn of it is on, which follows no intent's policy;
- an agent never sets its own approval: the purpose and effects an agent gives its draft read as empty in a rule, an intent's rules may not read them at all, and no rule reads an untrusted fact.

No agent's or thread's text picks a draft's approval, and a run releases a draft only through it. A workflow's `[approval]` rule that runs an item (`run` or `delay`) may not read an untrusted fact, nor `trigger.event.data`, which carries an intake's note, an answer or a sent nudge's mentions; a `wait` rule may, since it only holds an item. A workflow's `when` reads any fact: it is the trigger, and the bundled workflows gate on facts an agent or the extraction moves, such as `pr.first.due` or `workspace.pendency`. So a `when` that reads one may start a turn or propose a draft without a person, never post: no step posts outside, a step that acts where others see it always waits for you, and the draft a run proposes goes out only under the mode you set on its workflow or a rule over trusted facts, with the floors above.

A rule that releases (`send` or `delay`) and reads `draft.delivery` in a form that never names comment delivery, such as `draft.delivery in ["comment"]`, loads with a warning naming the form that does. A configuration rule over another provider's facts than its workflow's is refused: `doctor` and the daemon's log say so, and every draft of that workflow waits for you until it is fixed.

The configuration's `workflows.slack-ask-owner.drafts.approval` sets the approval of the questions `slack-ask-owner` proposes like those of any other workflow: `delay` sends them when the window ends, `send` at once, though they name people other than you.

A draft has a purpose (nudge, close, ask_owner, or any label its proposer names), a delivery (a message, a reaction, a comment or an edit) and the effects sending it applies, each exactly once, after a restart too:

| Effect | What it does once the draft is sent |
|---|---|
| `entry.nudged` | stamps the last nudge of the draft's entry, or of every live waiting entry of its unit |
| `unit.closed` | records the unit's closure through its plugin: on Slack, it ends the close entry and lets the thread go unwatched after its warm period, whatever later extraction passes say |
| `pr.nudged` | records the nudge on the pull request, which the next nudge's refusal reads |

Nothing follows from a draft's purpose: a closing message with no `unit.closed` closes nothing. An agent's draft may declare only the effects its intent lists in `[drafts] allowed_effects`; the draft is refused otherwise, with a message naming the intent. A draft declaring an effect its unit's provider does not implement, such as `pr.nudged` on a Slack thread, is refused when it is proposed. A sent draft whose effect cannot apply, such as `unit.closed` while its plugin is off, keeps it pending until it can.

A workflow proposes drafts with its `draft.propose` step, its text rendered from the workflow's `text.md.tmpl` through the profile's layers, the items' text as data. It skips a draft of the same workflow, unit and person while one is open or failed, for `repeat_after_sent` after one was sent (`forever` never proposes again) and for `repeat_after` after one was otherwise settled. Its `[drafts.withdraw] when` discards an open draft it no longer calls for, with the reason `moot` or, with `reason = "assigned"`, assigned; a draft whose workflow is gone or disabled is withdrawn `moot`; no draft of a disabled plugin is, nor one of a workflow whose manifest fails to load, which waits for you to fix it: its window does not end while the manifest fails, and resumes once it loads. A draft its `[drafts.withdraw] when` would withdraw at once is not proposed.

```sh
{{command}} outbox                      # drafts that wait on you, by purpose
{{command}} outbox show <id>            # one draft as it reads, its items and the commands it takes
{{command}} outbox new <unit> <markdown> # a reply of yours; --react <emoji> for a reaction, --send to post it now
{{command}} outbox approve <id>         # shows it and posts it once you say yes
{{command}} outbox edit <id> <markdown>
{{command}} outbox discard <id> --reason "not needed"
```

On a terminal the list groups the drafts by purpose (nudges, closings, replies, ...), each with its id, its unit as its row reads (`acme/api#7 · <title>`, `#channel · <thread>`, `DM with <name>`), who it mentions, its age and its first line; a column shows only where the drafts differ, such as the state when one failed. Piped, it prints one tab-separated line per draft for scripts: id, kind, state, unit, label, mentions, creation time (RFC 3339) and first line. A unit is a pull request URL or `owner/repo#n`, a Slack message permalink (its thread) or `<provider>:<key>`.

`outbox approve`, `outbox retry` and `outbox new --send` refuse when they detect an agent session's environment or no terminal. That is a guard against mistakes, not a security boundary: any process running as you can reach the daemon's socket. A draft still `sending` after a crash is checked against its destination, marked `sent` when it went out and `failed` when that cannot be told, so a crash never posts twice.

The review, address and nudge rounds on pull requests publish through `gh` from their own session, under the approval window and the ambiguity gate above. The tool an agent uses to post to Slack as you right away posts without your approval; a post that lands in a unit the loop tracks is recorded as a sent draft. `slack_draft`, `slack_react` and `slack_edit` only propose drafts, so a session's policy allows them too.
