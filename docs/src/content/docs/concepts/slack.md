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
- belongs to a thread you follow (`{{command}} slack follow <permalink>`).

Anything else is dropped and never stored. Messages of bots and of Slackbot are dropped unless `slack.ingest.allow_apps` lists their bot or app id; `deny_apps` drops one always. A post an agent app makes on behalf of a person counts as that person's.

`doctor` and the cockpit warn when the socket has been down for 5 minutes, when messages arrive only through the catch-up (the app's event subscription may be off), and when the mention search fails.

## Extraction

The `extract:slack` job turns the new messages of each unit into item changes, with one model call per unit on the fast tier. It waits until a unit has been quiet for 90 seconds, and never longer than 10 minutes after its oldest change, so a burst of messages costs one call. DM conversations are split into topic segments, each its own unit, with up to 10 earlier messages shown as read-only context: an item or a link that cites one as its origin or evidence is dropped, and message text that looks like the context or segment markers is defused. Your confirmed items are never overwritten.

Spend is capped: $0.10 per call, $3.00 and 500 calls per local day; a day's cap frees at local midnight, and the job's skip line says when. After 3 failed calls in a row the job pauses for 15 minutes before a trial call. `slack.extraction.enabled: false` turns it off. The [configuration reference](/reference/configuration/) lists every knob, and the [extraction eval](/reference/slack-eval/) measures the extraction against your own conversations.

## The agent tools

While Slack is configured and the plugin is on, the daemon's MCP server serves these tools to agent sessions:

| Tool | Does | Slack calls and scopes |
|---|---|---|
| `slack_read` | reads the messages of a unit (`slack:<key>`) or a permalink, 50 by default, up to 200 | `conversations.replies`, `conversations.info` (`channels:history`, `groups:history`, `channels:read`, `groups:read`) |
| `slack_search` | searches the messages you can read, newest first, with a query and optional `channel`, `from`, `after`, `before` and `limit` (20 by default, up to 100); one line per message: permalink, author, time in your time zone and first line; a direct message shows only when tracked | `search.messages` (`search:read`); `conversations.info` for a channel id and `users.lookupByEmail` for an email (`users:read.email`) |
| `slack_draft` | proposes a draft (`reply`, `nudge`, `close`, `ask_owner`, `react`) on the turn's Slack unit, or in the thread of a message permalink, any thread; it posts nothing, and the draft waits in the Outbox | once approved, `chat.postMessage` (`chat:write`) or `reactions.add` (`reactions:write`) |
| `slack_react` | adds a reaction to the message of a permalink, or with `remove` takes yours off, now, without a prompt (`install` allows it) | `reactions.add`, `reactions.remove` (`reactions:write`); `conversations.info` |
| `slack_edit` | replaces the text of one of your own messages, now, without a prompt (`install` allows it); it refuses a message someone else posted, and the record keeps the text it replaced | `conversations.history` or `conversations.replies` to read the message and its author, then `chat.update` (`chat:write`) |
| `slack_send_message` | posts as you, now, without a prompt (`install` allows it), and tells the agent to ask you first when it is unclear whether you want the message posted in your name; a post in a tracked unit is recorded as a sent draft; it refuses `@channel`, `@here`, `@everyone` and group mentions | `chat.postMessage` (`chat:write`) |

A draft `slack_draft` proposes on a thread that is not the turn's unit, such as a team's thread from a pull request's turn, names that turn and its unit, answers no item, declares no effect and waits for your approval. Every post `slack_send_message`, `slack_react` and `slack_edit` make is recorded in the Outbox as a sent draft, on the thread or segment it landed in, tracked or not, with the turn that made it. A post in a direct message no segment covers yet is recorded on its message, and moves onto the segment that covers it once the extraction segments the conversation. In the text of `slack_draft`, `slack_edit` and `slack_send_message`, an `@handle`, an `@<user id>` or an email that names one person of the workspace becomes a mention of them; one that names nobody, or whose lookup fails, stays text, one several people hold is refused, and code spans, code blocks, links and URLs are left as written. An edit also clears the message's blocks, so the new text is what Slack shows. A draft keeps the mention as `@{slack:<user id>}`, so the Outbox shows whom it pings; `outbox show` of a recorded edit shows the text it replaced. Resolving a handle reads `users.list` (`users:read`), an email `users.lookupByEmail` (`users:read.email`). Every scope above is in the app manifest and the login's scopes; none is new.

From a terminal, `{{command}} slack send <target> <text>` shows the message and asks before it posts, unless `--yes`. A target is a permalink (a reply in its thread), `#channel`, a channel id, `@handle`, an email or a user id. A reply in a thread goes through the Outbox as a draft of yours, approved when you say yes; the other targets, `--thread=false` and `--broadcast` post directly. `{{command}} outbox new <permalink> <text>` writes the reply as a draft that waits for your approval instead. `slack.post_allowlist`, when not empty, is the only set of channels or users any post may reach.

## Tokens

Tokens live only in the macOS login Keychain, under the service `{{name}}-slack` (`slack.keychain_service`). {{name}} never puts them in a command line, a log, the store or a tool result, and never hands one to an agent. Any process running as you can still read your login Keychain with `security`.

The user token is refreshed before it expires. When Slack refuses the refresh you are logged out: every Slack tool call fails naming `{{command}} slack login` and posts nothing. `doctor` and the cockpit warn when the login is 25 days old, before Slack's 30-day limit.
