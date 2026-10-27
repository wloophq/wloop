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

The page listens on `127.0.0.1` only and asks for the token that `open` hands over. It opens on **Focus**, with the [GTD lists](/concepts/gtd/) beside it, and the GitHub views:

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

## The terminal client

```sh
{{command}} tui
```

The same GTD views (Focus, Inbox, Outbox, Next, Waiting, Projects, Someday, Logbook, Review), the pull request views (Needs me, In loop, My PRs), the plan and any plugin view, updated live. The keys are the cockpit's: lowercase keys file entries, uppercase keys act on a row (`R` run now, `S` skip, `G` send with guidance, `W` answer, `C` continue, `X` dismiss, `H` pause, `M` mute, `A` take over, `O` open, `V` review, `F` address, `N` nudge). `Ctrl+K` opens the palette with every action, `?` the help, `q` quits. Actions that publish or take over ask for a `y` first. See [Keys](/reference/keys/).

`{{command}} status` prints the same views once, for a script or a quick look.

## The menu bar (macOS)

`{{command}} app`, which the LaunchAgent starts from `{{app}}.app`, puts an item in the menu bar. It shows `PR <n>`, your Inbox plus your Outbox, or `PR ?` while the daemon does not answer. Its menu lists up to five entries of Focus, Inbox and Outbox, the "In loop" section with Run now, Skip, Pause or Resume and Open on each row, then Open cockpit, Sweep now, Pause automatic rounds (or Resume), the sync line and Quit. Quit stops the menu bar; the daemon keeps running.

## Notifications (macOS)

| Notification | Actions |
|---|---|
| Round in 60 s: an automatic round is pending | Run now, Skip, Pause, and a Guide field whose text steers the round and sends it at once |
| Round held: a daily cap held a round back (once per pull request per day) | Run now |
| Agent asks, or Agent stopped | one action per option (the recommended first), Yes and No for a confirmation, a Reply field, Continue, Open |
| Waiting for a permission | Open |
| First round blocked | Open |
| In your Inbox: an entry on a unit, such as a Slack thread | Draft for me (delegates headless, when its action can), a Reply field that saves your reply to the Outbox, and a Send reply field that sends it at once; a note says where it went |

Clicking a notification's body opens its row in the cockpit. Each Inbox entry and each proposed or failed draft notifies once; the rest goes in a daily digest at `notifications.digest_at` (`09:00`, `""` for none).

When notifications are off or the menu bar is not running, rounds still run at their deadline: the cockpit shows a warning and `doctor` names the fix (System Settings > Notifications > {{app}}, or `{{command}} app install`).
