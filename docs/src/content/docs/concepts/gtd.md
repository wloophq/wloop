---
title: Inbox, entries and work units
description: How what people ask of you becomes GTD entries, and how the lists, projects and the weekly Review work.
---

{{name}} keeps your work as GTD **entries**. An entry is something you act on, wait for, read later or decide about. It points at the **items** and the **units** it comes from.

## Units and items

A **unit** is a thing work happens on: a pull request, a Slack thread, a DM topic. Units come from plugins. Two are compiled in: `github` (pull requests) and `slack` (threads and DM topics). Each can be turned off:

```toml
[slack]
enabled = false
```

The daemon reads the switch when it starts. With a plugin off, nothing of it runs, its actions leave every surface, and its cards render from the last stored snapshot, marked stale.

An entry with a unit of a plugin that is off refuses every verb but Dismiss, with the reason naming the plugin. Dismiss moves an entry whose units are all of plugins that are off to the Trash, as it does an entry derived from items, whose identity is the unit it came from. On any other entry that also has a unit of a plugin that is on, it detaches the units of the plugins that are off and the items on them, and the entry stays where it is with the rest. When the entry's first unit is of a plugin that is on and offers its own dismiss action, Dismiss runs that action, as on any entry. A link an item makes to a unit of a plugin that is off is stored, and is left out while the plugin is off.

An **item** is one thing someone needs from someone inside a unit: a `request`, a `question`, a `decision` or a `todo`. It is open or resolved, and says who asked, who must act next and where it came from.

- The GitHub plugin derives items from what it reads, with no model call: a review requested of you, reviews to address, failing checks, a conflict, a pending or blocked round, an agent that asked.
- The Slack plugin extracts them from conversations with one model call per unit (see [Slack as a source](/concepts/slack/)).

Once you create or correct an item it is **confirmed**: extraction never overwrites it again. A later extraction that disagrees becomes a proposal in the Inbox, with Accept and Reject.

## From items to entries

- An open item that waits on you becomes an action in the Inbox, or in Next when it needs no clarifying.
- Items you requested, or of a unit you care about, that wait on others give one Waiting entry per person.
- A unit you started, with every item resolved and no closing message posted, gets a "Close" entry in the Inbox.
- When every item of an entry resolves, the entry moves to Done. When one reopens, the entry comes back to the Inbox, marked changed.

You can capture an entry by hand with `n`.

## Lists are states

| State | Means |
|---|---|
| `inbox` | not clarified yet |
| `next` | an action you will do |
| `waiting` | someone else owes it: who, since when, the last nudge and a date to resurface |
| `scheduled` | snoozed until a date, or until new activity on one of its units |
| `someday` | maybe later |
| `reference` | kept to read, no action |
| `done` | finished |
| `trash` | dismissed |

Beside its state, an entry has a **focus** star, **contexts** (labels such as `@decide`), an **area** (switching area filters every view but the Inbox), an optional **project** and a deadline.

The views are Focus (starred entries, the landing view), Inbox, Outbox, Next, Waiting, Projects, Someday, Logbook (reference and done) and Review.

- **Changed since you saw it.** New activity on a unit by someone else, after you last opened the entry, brings a scheduled or waiting entry back to the Inbox with the changed mark. Opening or moving the entry clears it.
- **Snooze.** `s` schedules an entry until a date, until activity, or both. When the date passes it returns to the state it had. Muting the unit snoozes its entries.
- **Waiting.** `w` moves an entry to Waiting, optionally naming whom it waits on. A waiting entry with no nudge for `gtd.stale_waiting_days` (2 working days) gets the focus star, and so does any entry past its deadline.

Approve (`a`) and delegate (`d`) move nothing themselves: on an action entry with units they run that unit's action, such as Run now, a review or address round, or Act. Delegate also runs on a waiting, closure or captured entry that has a unit, and takes optional guidance and a mode: `headless` (Draft for me, which proposes drafts for the entry) or `session` (Hand to an agent). Without a mode the action picks its own, headless when it can. The entry says upfront whether a session can run on its unit, and why not when it cannot; a unit with no placement runs its session in a neutral directory with no checkout. A session started from an entry keeps the entry and its items, so the drafts it proposes serve that entry.

Reply (`r`) writes a draft of your own on the entry's first unit, a reply in its conversation (a comment on a pull request) or a reaction, tied to the entry and its items. It waits in the Outbox, or goes out at once with Send now. From a terminal, `{{command}} outbox new <unit|permalink> <markdown>` writes one on any unit. The full key list is on the [Keys](/reference/keys/) page.

## Messages

An Inbox message needs you to decide: an agent's question at the end of its turn (with its options), a post an agent makes mid-turn with the `inbox_post` tool, or a proposal to change a confirmed item. Your answer is stored with the message it answers, the agent is told which message you answered and reads the answer with `inbox_read`, and the message's card shows whether it did (see [Turns](/concepts/turns/#answers)).

## The Outbox

Every post an agent or a rule proposes is a draft first, in the Outbox, until its approval policy lets it out. See [What waits for you](/concepts/approval/#the-outbox). Default workflows, and the GitHub plugin's rule, also add drafts on their own, at each sweep:

| Workflow or rule | Proposes |
|---|---|
| `slack-stalled-nudge` | a nudge to each person a Slack waiting entry waits on, once it is past its resurface date or stale and not nudged within `drafts.repeat_after` (1d) |
| `slack-closure` | a closing message for a Slack unit you started whose items resolved |
| `slack-ask-owner` | a question for a Slack unit's open items with no owner after a working day; it always waits for you |
| `github-nudge` | the GitHub plugin's nudge |

Their texts are templates of the profile; see [Tune the Slack proposals](/guides/workflows/#tune-the-slack-proposals).

## Projects

A project is an outcome that takes more than one action. `p` on an entry makes a project of it or attaches it to one. A project is parallel or sequential; a sequential one shows only its first open action in Next. Each project has a review interval, 7 days by default, and opens a [dossier](/concepts/turns/#dossiers).

## The weekly Review

The Review (`g r`) walks six steps, each a filtered view you mark done with `Enter` (`]` and `[` move between steps):

1. Inbox to zero;
2. the Outbox;
3. Waiting: nudge?
4. projects without a next action or past their review interval;
5. Someday;
6. prune Focus.

## Configuration

```toml
[gtd]
stale_waiting_days = 2
areas = ["oss", "work"]
contexts = ["@decide", "@reply"]

[notifications]
digest_at = "09:00"
```

The entries, messages, drafts, items and Slack messages have no retention yet: they grow until a later release prunes them. Only dossiers are archived and purged.
