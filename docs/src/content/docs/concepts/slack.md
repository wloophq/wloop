---
title: Slack as a source
description: Which Slack messages come in, how they become items, and how agents read and post.
---

The `slack` plugin brings Slack threads and DM topics in as units. What you are asked for there becomes items and entries in your [Inbox](/concepts/gtd/), agents read Slack through the daemon, and every post goes out as you only through a draft under its approval policy, or through a send that asks first. Setting it up is in [Connect Slack](/guides/slack/).

## What comes in

The daemon reads Slack through Socket Mode, plus an ingestion job every minute: a mention search every 5 minutes, and a catch-up from each conversation's last read message after a start, a reconnect or a gap. Its first start reads back 7 days (`slack.ingest.since` sets another date).

A message is **admitted** when it:

- mentions you;
- contains one of `slack.ingest.terms` (whole words, any case);
- replies to a message of yours, or sits in a thread you wrote in;
- is in a DM or group DM;
- belongs to a thread you follow (`{{command}} slack follow <message>`, a message ref or permalink).

Anything else is dropped and never stored. Messages of bots and of Slackbot are dropped unless `slack.ingest.allow_apps` lists their bot or app id; `deny_apps` drops one always. A post an agent app makes on behalf of a person counts as that person's.

`doctor` and the cockpit warn when the socket has been down for 5 minutes, when messages arrive only through the catch-up (the app's event subscription may be off), and when the mention search fails.

## Extraction

The `extract:slack` job turns the new messages of each unit into item changes, with one model call per unit on the fast tier. It waits until a unit has been quiet for 90 seconds, and never longer than 10 minutes after its oldest change, so a burst of messages costs one call. DM conversations are split into topic segments, each its own unit, with up to 10 earlier messages shown as read-only context: an item or a link that cites one as its origin or evidence is dropped, and message text that looks like the context or segment markers is defused. Your confirmed items are never overwritten.

Spend is capped: $0.10 per call, $3.00 and 500 calls per local day; a day's cap frees at local midnight, and the job's skip line says when. After 3 failed calls in a row the job pauses for 15 minutes before a trial call. `slack.extraction.enabled: false` turns it off. The [configuration reference](/reference/configuration/) lists every knob, and the [extraction eval](/reference/slack-eval/) measures the extraction against your own conversations.

## The agent tools

While Slack is configured and the plugin is on, the daemon's MCP server serves these tools to agent sessions:

| Tool | Does | Slack calls and scopes |
|---|---|---|
| `slack_read` | reads the messages of a unit (`slack:<key>`), or the thread of a message ref or permalink, 50 by default, up to 200; each message prints with its ref | `conversations.replies`, `conversations.info` (`channels:history`, `groups:history`, `channels:read`, `groups:read`) |
| `slack_search` | searches the messages you can read, newest first, with a query and optional `channel`, `from`, `after`, `before` and `limit` (20 by default, up to 100); one line per message: permalink (the ref when Slack gives none), author, time in your time zone and first line; a direct message shows only when tracked | `search.messages` (`search:read`); `conversations.info` for a channel id and `users.lookupByEmail` for an email (`users:read.email`) |
| `slack_draft` | proposes a draft (`reply`, `nudge`, `close`, `ask_owner`, `react`) on the turn's Slack unit, or in the thread of a message, by its ref or permalink, any thread; from a turn on a unit whose plugin posts nothing, such as a GTD entry, also to a channel or a person; it posts nothing, and the draft waits in the Outbox | once approved, `chat.postMessage` (`chat:write`) or `reactions.add` (`reactions:write`) |
| `slack_react` | adds a reaction to a message, by its ref or permalink, or with `remove` takes yours off, now, without a prompt (`install` allows it) | `reactions.add`, `reactions.remove` (`reactions:write`); `conversations.info` |
| `slack_edit` | replaces the text of one of your own messages, by its ref or permalink, now, without a prompt (`install` allows it); it refuses a message someone else posted, and the record keeps the text it replaced | `conversations.history` or `conversations.replies` to read the message and its author, then `chat.update` (`chat:write`) |
| `slack_send_message` | posts as you, in the thread of a message by its ref or permalink, or to a conversation or person, now, without a prompt (`install` allows it), and tells the agent to ask you first when it is unclear whether you want the message posted in your name; a post in a tracked unit is recorded as a sent draft; it refuses `@channel`, `@here`, `@everyone` and group mentions | `chat.postMessage` (`chat:write`) |

### The message ref

A message's ref is `<conversation>/<ts>`, such as `C0123ABCD/1700000000.000100`: its conversation id and its Slack timestamp. `slack_read` and a Slack turn print every message as `[m<N> <ref>] <author>`, and every tool that takes a message (`slack_react`, `slack_edit`, `slack_draft`, `slack_send_message` and `slack_read` itself) takes that ref or the message's permalink, through one parser, so an agent can act on any message it has read; the ledger anchors an item on the same ref, the Outbox records the message a post made or reacted to by it, and every command that takes a unit takes it for the message's thread; `outbox new <message> --react <emoji>` reacts to that message itself. `m<N>` only numbers the messages of one read, for `reply to m<K>`. A ref does not say the thread its message is in: a reply to it goes into that thread, which {{name}} reads from the store when it holds the message, and from Slack (`conversations.replies`) when it does not, when the stored message is deleted, when search stored it without saying its thread, or when the store fails. A permalink's `thread_ts`, when it has one, says the thread itself. Each message prints its ref and not its permalink, which would need the team's URL and cost about 80 characters a message; the first message's permalink heads the read.

Where a draft posts is decided when it is proposed and stored as its target, so the Outbox shows it before you approve and the publisher never guesses. A draft with no target on a thread posts in that thread. A direct message segment has no thread of its own: its draft answers the newest message its items name, as a reply in that message's thread, which a top-level message starts. A reaction goes on the newest message one of its items started at, the request you answer, never on a later message of the trail, which may be your own. A message you write on a segment from the cockpit or `outbox new` with no items goes to the conversation, as typing in the direct message does; a reaction you write there with no items has no message to land on and is refused, since neither names a message. A draft an agent or a workflow proposes on a segment with neither a target nor items that name a message of the conversation is refused when it is proposed, with the reason. The workflows' `slack-closure` and `slack-ask-owner` drafts take this path. A draft stored without a target before placement existed is placed when you approve it, retry it, its policy releases it, or the sender takes it already approved; one a previous version left sending is looked for at the top level of the conversation, where that version posted it.

A draft `slack_draft` proposes on a thread that is not the turn's unit, such as a team's thread from a pull request's turn, names that turn and its unit, answers no item, declares no effect and waits for your approval. A draft to a channel or a person from a turn on a GTD entry stays on the entry, and the Outbox gives it to the plugin whose publisher takes its target, the Slack one, which posts it at the top level of the channel or in the direct message once you approve it. A target that more than one plugin takes is refused, and a turn on a unit whose plugin posts keeps its drafts on that plugin whatever their target. Every post `slack_send_message`, `slack_react` and `slack_edit` make is recorded in the Outbox as a sent draft, on the thread or segment it landed in, tracked or not, with the turn that made it. A post in a direct message no segment covers yet is recorded on its message, and moves onto the segment that covers it once the extraction segments the conversation. In the text of `slack_draft`, `slack_edit` and `slack_send_message`, an `@handle`, an `@<user id>` or an email that names one person of the workspace becomes a mention of them; one that names nobody, or whose lookup fails, stays text, one several people hold is refused, and code spans, code blocks, links and URLs are left as written. An edit also clears the message's blocks, so the new text is what Slack shows. A draft also posts a typed `<@<user id>>` or `<@<user id>|name>` as that mention, and every other Slack token it spells, such as a channel's `<#<channel id>>` or a `<!date...>`, as text; `<!here>`, `<!channel>`, `<!everyone>` and a group's `<!subteam^...>` are refused. A draft keeps the mention as `@{slack:<user id>}`, so the Outbox shows whom it pings; `outbox show` of a recorded edit shows the text it replaced. Resolving a handle reads `users.list` (`users:read`), an email `users.lookupByEmail` (`users:read.email`). Every scope above is in the app manifest and the login's scopes; none is new.

A post a turn on an entry makes, recorded at once or sent once approved, is attached to that entry when the turn's intent sets `drafts.attach_posts`; reactions and edits are not posts. The Slack plugin says a unit has ended (`slack.ended`) once a closure was posted or marked (`unit.closed`), or once its newest stored message is older than `slack.ended_after` (7 days); an entry you captured closes once every unit attached to it has ended (see [Units attached to an entry](/concepts/gtd/#units-attached-to-an-entry)).

From a terminal, `{{command}} slack send <target> <text>` shows the message and asks before it posts, unless `--yes`. A target is a message ref or permalink (a reply in its thread), `#channel`, a channel id, `@handle`, an email or a user id. A reply in a thread goes through the Outbox as a draft of yours, approved when you say yes; the other targets, `--thread=false` and `--broadcast` post directly. `{{command}} outbox new <message> <text>` writes the reply as a draft that waits for your approval instead. `slack.post_allowlist`, when not empty, is the only set of channels or users any post may reach.

## Tokens

Tokens live only in the macOS login Keychain, under the service `{{name}}-slack` (`slack.keychain_service`). {{name}} never puts them in a command line, a log, the store or a tool result, and never hands one to an agent. Any process running as you can still read your login Keychain with `security`.

The user token is refreshed before it expires. When Slack refuses the refresh you are logged out: every Slack tool call fails naming `{{command}} slack login` and posts nothing. `doctor` and the cockpit warn when the login is 25 days old, before Slack's 30-day limit.
