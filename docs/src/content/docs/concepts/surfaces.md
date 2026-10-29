---
title: Cockpit, terminal client and menu bar
description: The three places where you see what waits on you and act on it.
---

All three are clients of the daemon. They show the same views and offer the same actions, which the daemon computes for every row; a client renders them and decides nothing.

## The cockpit

```sh
{{command}} open                         # opens the cockpit in your browser
{{command}} open --print                 # prints the URL with its token, for another browser
{{command}} open --print --view stalled  # opens on one view
{{command}} open <url>                   # focuses the pull request's workspace in its manager
```

The page listens on `127.0.0.1` only and asks for the token that `open` hands over. It opens on **Focus**, with the [lists](/concepts/tasks/) beside it, and the GitHub views:

| View | Shows |
|---|---|
| Needs me | what waits on you: an agent asked or stopped, a round is pending or held, a review request, your pull request with changes requested |
| Stalled | pull requests waiting on someone past the stalled threshold |
| My PRs | your own pull requests, those you took over included |
| In loop | pull requests with a review workspace the loop follows |
| Approved | open pull requests that wait on nothing of yours |
| Backlog | charts of the open pull requests: who they wait on, age, size against risk, tags, failing checks |
| Done | workspaces whose pull request merged or closed, ready to remove |
| All | a dense table of everything in the inventory |
| Recent | the newest events of every unit the loop follows: pull requests and the units of the other sources with a card |
| Explore | search other people's pull requests to help on |

A row shows whom the pull request waits on (the colored strip on its left), why, since when, its brief's size and risk, and its failing checks. The detail panel holds the timeline, the brief, the agent's question with its answers, and the actions. A unit's label opens it in the cockpit, in its plugin's view; the service's name beside it (GitHub, Slack) opens the unit there, in a new tab. The filter box (`/`) takes text and qualifiers such as `repo:`, `author:@me` or `is:stalled`; see [Cockpit filter](/reference/cockpit-filter/). Filters, grouping and order stay in the URL. **Copy list** copies what the view shows as YAML, to paste into an agent session with a prompt.

A task's page shows, in order: its header (title, state, the properties your profile declares, and its links, each opening in the cockpit with the service's name beside it), the asks with the quotes they came from, the brief, the gates that wait on you, each with the intents of its gate in `gates.toml` (`1` to `9` pick one while the gate has the focus; an intent that takes an answer reads the box above it), the runs with Open workspace, Enter session and Transcript, the timeline as a chat you narrow by kind, the composer, and the task's children. **Send**, the default that Ctrl+Enter runs, writes to the task's agent (see [Chat](/concepts/tasks/#chat)); **Send reply** posts the text as a reply in the task's conversation at once and **Save as draft** holds it until you approve it, both through the verb whose handler is `reply`; the one its `send` in `tasks.toml` names comes first. A list groups its tasks by the fact its view in `views.toml` names in `group`, and a state or a category groups in the order of your states.

## The terminal client

```sh
{{command}} tui
```

The views of your profile's `views.toml`, in its order and starting on the first (the bundled Inbox, Needs me, Waiting, Snoozed, Focus, Projects, Someday, Logbook, Outbox, Review), the pull request views, the plan and any plugin view, updated live. A list groups its tasks by the fact its view names in `group`, a state or a category in the order of your states. A task's detail shows its asks with the quotes they came from, its open gates with their intents (`1` to `9` pick one; an intent that takes an answer asks for it first), and its runs with where they run and the command that resumes them. A verb's key runs the verb your profile serves, and a verb that asks for a value asks the question its `input` sets. While a plugin has not reported every unit since the daemon started, a line names it and why; a row its plugin last reported before it went down shows `(stale)`. The keys are the cockpit's: lowercase keys file tasks, uppercase keys act on a row (`R` run now, `S` skip, `G` send with guidance, `W` answer, `C` continue, `X` dismiss, `H` pause, `M` mute, `A` take over, `O` open, `V` review, `F` address, `N` nudge). `Ctrl+K` opens the palette with every action, `?` the help, `q` quits. Actions that publish or take over ask for a `y` first. See [Keys](/reference/keys/).

`{{command}} status` prints the same views once, for a script or a quick look.

## The menu bar (macOS)

`{{command}} app`, which the LaunchAgent starts from `{{app}}.app`, puts an item in the menu bar. It shows `PR <n>`, the count of the views your profile's `[badge]` names (the bundled Inbox and Needs me), or `PR ?` while the daemon does not answer. Its menu names the plugins still loading, lists up to five tasks of each badge view, the "In loop" section with Run now, Skip, Pause or Resume and Open on each row, then Open cockpit (on the first badge view), Sweep now, Pause automatic rounds (or Resume), the sync line and Quit. Quit stops the menu bar; the daemon keeps running.

## Notifications (macOS)

| Notification | Actions |
|---|---|
| Round in 60 s: an automatic round is pending | Run now, Skip, Pause, and a Guide field whose text steers the round and sends it at once |
| Round held: a daily cap held a round back (once per pull request per day) | Run now |
| Agent asks, or Agent stopped | one action per option (the recommended first), Yes and No for a confirmation, a Reply field, Continue, Open |
| Waiting for a permission | Open |
| First round blocked | Open |
| In your Inbox: a task on a unit, such as a Slack thread | Draft for me (delegates headless, when its action can), a Reply field that saves your reply to the Outbox, and a Send reply field that sends it at once; a note says where it went |

Clicking a notification's body opens its row in the cockpit. Each task in the Inbox, each task anywhere else while a question, post or proposal gate is open on it, and each proposed or failed draft notifies once; a button that runs a task verb runs the verb your profile gives that handler. The rest goes in a daily digest at `notifications.digest_at` (`09:00`, `""` for none): how many tasks of the badge views moved since the last one, and each badge view's count, worded by `digest_title`, `digest_new` and `digest_view` in `notices.toml`. A notification's titles and buttons are the `notify_*` keys of `notices.toml`.

When notifications are off or the menu bar is not running, rounds still run at their deadline: the cockpit shows a warning and `doctor` names the fix (System Settings > Notifications > {{app}}, or `{{command}} app install`).
