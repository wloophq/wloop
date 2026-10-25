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

The `extract:slack` job turns the new messages of each unit into item changes, with one model call per unit on the fast tier. It waits until a unit has been quiet for 90 seconds, and never longer than 10 minutes after its oldest change, so a burst of messages costs one call. DM conversations are split into topic segments, each its own unit, with up to 10 earlier messages shown as read-only context. Your confirmed items are never overwritten.

Spend is capped: $0.10 per call, $3.00 and 500 calls per local day. After 3 failed calls in a row the job pauses for 15 minutes before a trial call. `slack.extraction.enabled: false` turns it off. The [configuration reference](/reference/configuration/#slack) lists every knob, and the [extraction eval](/reference/slack-eval/) measures the extraction against your own conversations.

## The agent tools

While Slack is configured and the plugin is on, the daemon's MCP server serves three tools to agent sessions:

| Tool | Does |
|---|---|
| `slack_read` | reads the messages of a unit (`slack:<key>`) or a permalink, 50 by default, up to 200 |
| `slack_draft` | proposes a draft (`reply`, `nudge`, `close`, `ask_owner`, `react`) on the turn's Slack unit; it posts nothing, and the draft waits in the Outbox |
| `slack_send_message` | posts as you, now; `install` puts it under Claude Code's `permissions.ask`, so you confirm every call, and it refuses `@channel`, `@here`, `@everyone` and group mentions |

From a terminal, `{{command}} slack send <target> <text>` shows the message and asks before it posts, unless `--yes`. A target is a permalink (a reply in its thread), `#channel`, a channel id, `@handle`, an email or a user id. A reply in a thread goes through the Outbox as a draft of yours, approved when you say yes; the other targets, `--thread=false` and `--broadcast` post directly. `{{command}} outbox new <permalink> <text>` writes the reply as a draft that waits for your approval instead. `slack.post_allowlist`, when not empty, is the only set of channels or users any post may reach.

## Tokens

Tokens live only in the macOS login Keychain, under the service `{{name}}-slack` (`slack.keychain_service`). {{name}} never puts them in a command line, a log, the store or a tool result, and never hands one to an agent. Any process running as you can still read your login Keychain with `security`.

The user token is refreshed before it expires. When Slack refuses the refresh you are logged out: every Slack tool call fails naming `{{command}} slack login` and posts nothing. `doctor` and the cockpit warn when the login is 25 days old, before Slack's 30-day limit.
