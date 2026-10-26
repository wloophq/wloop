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

With no answer, the round runs when the window ends. The window is the workflow's `[approval].window`, `60s` in the bundled review, address and nudge workflows; `workflows.<name>.approval.window` in the config changes it, and `"0s"` sends that workflow's rounds at once. A pending item survives a restart: one whose window ended while the daemon was down runs once when it starts.

Guidance is not lost when its round does not go out (a pause, a cap, a busy workspace): it is kept for the next round of that pull request, and the cockpit's detail shows it as "Next round guidance". Rounds you ask for (`review`, `address`, `plan apply`) do not wait.

A muted pull request (`{{command}} mute <url>`) stays muted: its round is not announced and goes out at the deadline unless you skip it or pause the pull request.

The nudge workflow ships with `approval.default = "wait"`: each nudge stays pending until you apply it. See [Tune workflows](/guides/workflows/#let-nudges-go-out).

## The ambiguity gate

Inside a round, the procedure publishes on its own except when one of these holds; then it records `asked` with the full draft and waits for you:

- it is unsure whether a finding blocks, or the verdict depends on intent the pull request does not state;
- it would approve a change to an irreversible or risky surface (your profile lists them: schema migrations, data deletion, auth, billing, infrastructure by default);
- a finding alleges a security problem;
- the review would contradict a decision another human reviewer left. Bots and the logins in `automated_reviewers` never trigger this.

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
| `always` (default) | waits in the Outbox until you approve it |
| `window` | is sent when its window ends unless you discard or edit it first; an edit makes it `always` |
| `none` | is sent at once |
| `observe` | is recorded as what would have been sent, and never sent |

A draft you write yourself (Reply on an entry or a unit, see [GTD](/concepts/gtd/), `outbox new`, or `slack send` to a thread) is `always` too: Save to Outbox leaves it there, and Send now approves it right after proposing it, through the same publisher.

Every bundled intent, workflow and rule uses `always`. Your own intent or workflow can set another with a `[draft_approval]` table, a workflow's drafts with `workflows.<name>.draft_approval` in the configuration, and GitHub's nudge rule with `proposals.github-nudge.approval`; the older `proposals.stalled-nudge`, `closure` and `ask-owner` approvals still apply to the workflows that replaced them, with a warning naming `workflows.<name>.draft_approval`. A draft an agent aims outside its own unit is always `always`, unless the policy is `observe`, and so are a draft whose workflow or intent sets `auto_send = false` and every comment draft: a comment on a pull request goes out under your name only once you approve it.

A draft has a purpose (nudge, close, ask_owner, or any label its proposer names), a delivery (a message, a reaction or a comment) and the effects sending it applies, each exactly once, after a restart too:

| Effect | What it does once the draft is sent |
|---|---|
| `entry.nudged` | stamps the last nudge of the draft's entry, or of every live waiting entry of its unit |
| `unit.closed` | records the unit's closure through its plugin: on Slack, it ends the close entry and lets the thread go unwatched after its warm period, whatever later extraction passes say |
| `pr.nudged` | records the nudge on the pull request, which the next nudge's refusal reads |

Nothing follows from a draft's purpose: a closing message with no `unit.closed` closes nothing. An agent's draft may declare only the effects its intent lists in `draft_effects`; the draft is refused otherwise, with a message naming the intent. A draft declaring an effect its unit's provider does not implement, such as `pr.nudged` on a Slack thread, is refused when it is proposed. A sent draft whose effect cannot apply, such as `unit.closed` while its plugin is off, keeps it pending until it can.

A workflow proposes drafts with its `draft.propose` step, its text rendered from the workflow's `text.md.tmpl` through the profile's layers, the items' text as data. It holds a draft of the same workflow, subject and person while one is open or failed, for `hold_after_sent` after one was sent (`forever` never proposes again) and for `hold` after one was otherwise settled. Its `[withdraw] when` discards an open draft it no longer calls for, with the reason `moot` or, with `reason = "assigned"`, assigned; a draft whose workflow is gone or disabled is withdrawn `moot`; no draft of a disabled plugin is, nor one of a workflow whose manifest fails to load, which waits for you to fix it. A draft its `[withdraw] when` would withdraw at once is not proposed.

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

The review, address and nudge rounds on pull requests publish through `gh` from their own session, under the approval window and the ambiguity gate above. `slack_send_message`, the tool an agent uses to post to Slack as you right away, asks Claude Code's permission prompt before every call.
