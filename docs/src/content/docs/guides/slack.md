---
title: Connect Slack
description: Create your own Slack app from the manifest, log in, and choose what comes in.
---

The Slack source needs a Slack app of your own and the macOS login Keychain. Each person creates their own app: Slack hands each Socket Mode event to any one of an app's open connections, so two people sharing an app would receive each other's messages and miss their own.

`{{command}} init` walks you through these steps. To do them by hand:

## 1. Create the app

1. Open [api.slack.com/apps](https://api.slack.com/apps), choose **Create New App**, then **From a manifest**, and pick your workspace.
2. Paste the manifest below, replacing `Your Name` in `display_information.name` so the workspace admin can tell the apps apart. Slack labels your posts "Sent using" this name.
3. Create the app. On **OAuth & Permissions**, turn on **PKCE**. It cannot be turned off later, and the login has no client secret without it.
4. On **Basic Information**, under **App-Level Tokens**, choose **Generate Token and Scopes**, add the scope `connections:write` and generate it. Copy the `xapp-...` token.
5. Copy the **Client ID** from **App Credentials** on the same page.

A workspace that requires admin approval shows a request form on the first login instead of the consent page. Send the request, wait for the approval, then log in again. Adding a scope to the app later asks for a new approval.

The manifest, also at [slack-app-manifest.yaml](/slack-app-manifest.yaml):

```yaml file="slack-app-manifest.yaml"
```

## 2. Log in

In `config.toml`:

```toml
[slack]
client_id = "000000000000.000000000000"
```

Then:

```sh
{{command}} slack login               # opens the browser; --no-browser prints the URL instead
{{command}} slack login --app-token   # paste the xapp-... token; hidden at a terminal, or piped on standard input
{{command}} slack status              # team, user, token expiry, whether the app token is stored
```

`slack login` runs Slack's OAuth flow for a user token, with PKCE, and waits up to 5 minutes for the callback on `http://127.0.0.1:53682/slack/callback` (`slack.redirect_port`). The app's redirect URL must name that address; the manifest already does. `--app-token` reads the token only from standard input and refuses a value on the command line.

Restart the daemon so it connects (on macOS, `launchctl kickstart -k gui/$(id -u)/io.github.{{org}}.{{name}}`). `{{command}} doctor` then reports the socket and the login.

## 3. Choose what comes in

By default, a message comes in when it mentions you, replies to you, sits in a thread you wrote in, or is in a DM. To widen it:

```toml
[slack]
client_id = "000000000000.000000000000"
post_allowlist = ["C000", "U000"] # channel ids, or user ids for their DMs

[slack.ingest]
terms = ["api-outage", "billing"]
allow_apps = ["B000"]
```

- `ingest.terms` admits messages with any of these words.
- `ingest.allow_apps` admits the messages of these bots or apps.
- `post_allowlist`, when not empty, is the only set of channels or users any post may reach.

Follow a whole thread, so every message of it comes in:

```sh
{{command}} slack follow https://acme.slack.com/archives/C000/p1700000000000000
{{command}} slack unfollow https://acme.slack.com/archives/C000/p1700000000000000
```

## 4. Work with it

Slack threads and DM topics now give tasks in your Inbox. Their rows offer the intents that serve Slack: the bundled `act`, and any intent of yours with `provider = "slack"` or `"any"`.

**Delegate.** `d` on a Slack task (an action, a waiting task, a closure or a capture on a thread) runs `act`, or your own Slack intent of the same verb, which outranks it. It asks for optional guidance first. By default `act` runs headless: one read-only agent call that resolves what it can and proposes the replies as drafts for that task. The task detail in the cockpit has a Delegate box with two buttons: **Draft for me** (headless) and **Hand to an agent** (a session in the workspace the thread is placed in). A thread or a DM with no [placement](/reference/configuration/) runs its session in a neutral directory under the state directory, an empty repository with no project checked out; add a placement for a channel to work its threads in that repository instead. When no session can run at all, such as with no workspace manager enabled, Hand to an agent is off and says why before you click it; in the terminal the guidance prompt says it too. Tab in the guidance prompt switches to a session. The Inbox notification of a Slack task has a **Draft for me** button. A session started this way knows the task and its items, so what it drafts with `slack_draft` lands in the task's detail like a headless draft.

From a terminal, an intent that serves Slack takes a message ref (`<conversation>/<ts>`), a permalink or `slack:<key>` in place of a pull request URL. The daemon resolves a ref or a permalink to the unit the message is in, as `slack_draft` does: the tracked direct message segment or thread, else its thread:

```sh
{{command}} act https://acme.slack.com/archives/C000/p1700000000000100 'say we ship Friday'
```

The drafts made for a task are listed in its detail, where you approve, edit or discard them. In the terminal, a digit picks one (when the task asks no question), then `a`, `i` or `Backspace` acts on it.

**Reply yourself.** `r` on the task, the Reply composer in the task or thread detail, or the Reply field of its notification writes a reply in the thread (or, in the cockpit, a reaction such as `eyes`). **Save to Outbox** keeps it there for your approval; **Send now** (Tab in the terminal prompt, then `y`; **Send reply** in the notification) approves it at once. Either way it goes out through the Outbox like any draft, and counts as your reply for the thread's items. Ctrl+E in the terminal prompt writes it in `$EDITOR`.

Write one, approve one with `a`, or from a terminal:

```sh
{{command}} outbox new https://acme.slack.com/archives/C000/p1700000000000100 'Friday, after the freeze.'
{{command}} outbox new https://acme.slack.com/archives/C000/p1700000000000100 --react eyes --send
{{command}} outbox
{{command}} outbox approve <id>
```

`slack send` posts as you after a confirmation. To a message ref or permalink it replies in the thread through the Outbox, as a draft of yours approved once you say yes (or at once with `--yes`), so the reply counts for the thread's items and shows in the Logbook. To a channel, a person, or with `--thread=false` or `--broadcast`, it posts directly:

```sh
{{command}} slack send https://acme.slack.com/archives/C000/p1700000000000100 'On it.'
{{command}} slack send '#team-api' 'Deploy is done.'
```

## Log out

```sh
{{command}} slack logout
```

It revokes the user token at Slack and deletes both Keychain entries, even when the revoke fails. To keep Slack configured but stop the source, set `slack.enabled = false` and restart the daemon.

See [Slack as a source](/concepts/slack/) for how messages are admitted and turned into items, and the [configuration reference](/reference/configuration/) for every key.
