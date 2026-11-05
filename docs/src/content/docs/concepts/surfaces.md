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

The page listens on `127.0.0.1` only and asks for the token that `open` hands over. It opens on **Focus**, with the [lists](/concepts/tasks/#views) beside it. Needs me, Stalled, In loop, Approved and Done are lists of tasks across sources: a pull request shows there through its task. The GitHub views list what exists in GitHub:

| View | Shows |
|---|---|
| My PRs | your own pull requests, those you took over included |
| Backlog | charts of the open pull requests: who they wait on, age, size against risk, tags, failing checks |
| All | a dense table of everything in the inventory |
| Recent | the newest events of every unit the loop follows: pull requests and the units of the other sources with a card |
| Explore | search other people's pull requests to help on |

Each view of `views.toml` explains itself: its item in the list of views shows its `summary` as a tooltip, on hover and on keyboard focus; its heading shows the summary under the label, and **Why these tasks?** opens the view's `when`, `sort` and `group` as your profile writes them. A view that lists nothing shows its `empty`.

Done and Approved offer **Remove all clean**, which removes the workspaces their tasks' pull requests hold, keeping each one whose checkout holds anything not published, with why (any view whose `remove_clean` is set in `views.toml` offers it). A row shows whom the pull request waits on (the colored strip on its left), why, since when (a pending round's countdown to the deadline of its open plan gate), its brief's size and risk, and its failing checks. The detail panel holds the timeline, the brief, the agent's question with its answers, and the actions. A unit's label opens it in the cockpit, in its plugin's view; a unit its plugin no longer lists, such as a closed agent session, opens the first open task that holds it. The service's name beside it (GitHub, Slack) opens the unit there, in a new tab. The filter box (`/`) takes text and qualifiers such as `where:`, `author:@me` or `is:stalled`; see [Cockpit filter](/reference/cockpit-filter/).

Every list, of tasks or of a plugin's rows, has the same bar over the [facets](/concepts/tasks/#facets) of its items: Group and Sort menus, List or Board, a chip per filter, the count it shows of the view, and **Copy list**. The menus offer a field only when some item of the view has a value for it, so Needs me, which mixes Slack conversations and pull requests, offers `where`, `ci` and `source`, and My PRs offers no Slack field. A task view groups by the facet that reads the fact its `group` in `views.toml` names unless you pick another (or None), and keeps the daemon's order until you pick a sort; the sort menu calls that order by `default_sort`, a row view's by its plugin's `ui.toml`. A board puts a lane per group, a task view's tasks in their rows; with nothing picked it groups by the facet that colors the strip (a task view's is the one its profile marks `strip`, the bundled `waits-on`; a row view's is its plugin's) when the view offers it, else it is one lane. Filters, grouping, order and layout stay in the URL; a view that does not offer the group or sort shows its default and leaves them in the URL for the views that do. The menus' and Copy list's words are `[lens]` in `views.toml`. The filter keeps the tasks the rail counts. **Copy list** copies what the view shows, in its order, as YAML under `items:`: each item's reference (a task's id, a row's label), link, title and units, then its facets by id, to paste into an agent session with a prompt.

A task's page shows, in order: its header (title, state, the properties your profile declares, and its links, each opening in the cockpit with the service's name beside it), the asks with the quotes they came from, the brief, the gates that wait on you, each with the intents of its gate in `gates.toml` (`1` to `9` pick one while the gate has the focus; an intent that takes an answer reads the box above it), the runs with Open workspace, Enter session and Transcript, the timeline as a chat you narrow by kind, the composer, and the task's children. **Send**, the default that Ctrl+Enter runs, writes to the task's agent (see [Chat](/concepts/tasks/#chat)); **Send reply** posts the text as a reply in the task's conversation at once and **Save as draft** holds it until you approve it, both through the verb whose handler is `reply`; the one its `send` in `tasks.toml` names comes first.

## The terminal client

```sh
{{command}} tui
```

The views of your profile's `views.toml`, in its order and starting on the first (the bundled Inbox, Needs me, Waiting, Chase, Fits now, Blocked by me, Stalled, In loop, Approved, Snoozed, Focus, Projects, Someday, Logbook, Done, Outbox, Review), the plugins' views (My PRs, the plan), updated live. A pending round's row counts down to the deadline of its open plan gate. The `/` filter takes the cockpit's qualifiers, so a list and the rail count the same tasks. A task view groups by the facet that reads the fact its `group` names, and a row view is ungrouped; `=` steps the group through the view's own, each other field some item of the view has a value for, and none, and `-` steps the sort through the view's own order and each sortable field, ascending then descending. The terminal has no board. A task's detail shows its asks with the quotes they came from, its open gates with their intents (`1` to `9` pick one; an intent that takes an answer asks for it first), and its runs with where they run and the command that resumes them. A verb's key runs the verb your profile serves, and a verb that asks for a value asks the question its `input` sets. While a plugin has not reported every unit since the daemon started, a line names it and why; a row its plugin last reported before it went down shows `(stale)`. The keys are the cockpit's: lowercase keys file tasks, uppercase keys act on a row (`R` run now, `S` skip, `G` send with guidance, `W` answer, `C` continue, `X` dismiss, `H` pause, `M` mute, `A` take over, `O` open, `V` review, `F` address, `N` nudge). `Ctrl+K` opens the palette with every action, `?` the help, where a view's key shows its `summary`, `q` quits. A view that lists nothing shows its `empty`. Actions that publish or take over ask for a `y` first. See [Keys](/reference/keys/).

`{{command}} status` prints the tasks of the views the badge counts, then the pull requests you track, once, for a script or a quick look.

## The menu bar (macOS)

`{{command}} app`, which the LaunchAgent starts from `{{app}}.app`, puts an item in the menu bar. It shows `PR <n>`, the count of the views your profile's `[badge]` names (the bundled Inbox and Needs me), or `PR ?` while the daemon does not answer. Its menu names the plugins still loading, lists up to five tasks of each badge view (a pending round's task among them, through its open plan gate), then any section a plugin's manifest declares (`[[source.ui.menubar]]`; the bundled plugins declare none), then Open cockpit (on the first badge view), Sweep now, Pause automatic rounds (or Resume), the sync line and Quit. Quit stops the menu bar; the daemon keeps running.

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
