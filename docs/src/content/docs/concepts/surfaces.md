---
title: Cockpit, terminal client and menu bar
description: The three places where you see what waits on you and act on it.
---

All three are clients of the daemon. They show the same views and offer the same actions, which the daemon computes for every row; a client renders them and decides nothing.

## The cockpit

```sh
{{command}} open                         # opens the cockpit: in the app's window, or your browser
{{command}} open --print                 # prints the URL with its token, for another browser
{{command}} open --print --view stalled  # opens on one view
{{command}} open '#task=<id>'            # opens a cockpit route; so does a URL of the cockpit itself
{{command}} open '{{command}}://open/#task=<id>'  # opens a link copied from the cockpit
{{command}} open <url>                   # focuses the pull request's workspace in its manager
```

On macOS, while `{{app}}.app` runs, `open` and a notification's click show the cockpit in the app's own window. There is one window, and it reopens where you left it. It is in the Dock and ⌘Tab while it shows; closing it hides it. Without the app, with `window.enabled = false`, or on another system, `open` uses the browser, which always works.

The window has no title strip: its controls sit over the cockpit's top strip, which becomes the title bar. You drag the window by it, and it holds back and forward, the title of what shows (the list, or the task or unit whose detail is open), the copy-link button and the omnibox button, which opens the floating panel. The app marks the page's address (`titlebar=inset`) so the cockpit draws this only in the app's window; in a browser tab the top strip stays as it is.

**Detach.** The detail of a task or unit has a button, and the key `t` while the focus is in it, that opens the detail in a window of its own. There is one such window per task or unit: detaching it again brings its window forward. It follows changes as the main window does, a link in it to anything else opens in the main window, and closing it closes it for good; it is not reopened when the app starts again. In a browser, detach opens the detail in a new tab.

**Links.** The copy-link button, in the title bar and in the detail, and the key `c` copy a link to what shows: `{{command}}://open/#<route>`, a list, a task or a unit. The link carries neither the daemon's port nor its token, so it works after a restart and is safe to paste in a chat. The app registers the `{{command}}` scheme: opening such a link (from a chat, a note or `open` in a terminal) shows its route in the main window and brings it forward, as `{{command}} open` does with the link; with `window.enabled = false` the menu bar opens it in the browser. A link that cannot be read opens the default view, which says so. The cockpit in a browser copies the same link.

The global shortcut `window.hotkey` (`ctrl+option+space`; `""` for none) opens a floating panel over whatever app you are in, without leaving it until you type, on the route `#panel=omnibox`. A link to any other route in the panel opens that route in the main window and brings it forward. Escape hides the panel, and so does clicking elsewhere. A shortcut that does not parse, or that the system refuses, is logged as `window.hotkey` with the chord; the window and the menu bar keep working. Another app holding the same chord through a system shortcut takes it without an error.

The page listens on `127.0.0.1` only and asks for the token that `open` hands over, and loads nothing from elsewhere: its scripts and its fonts (Geist for prose, Geist Mono for refs, counts and dates) ship in the binary. It follows the system's light or dark appearance, and moves only in answer to what you do, not at all when the system asks for reduced motion. It opens on **Focus**, with the [lists](/concepts/tasks/#views) beside it. Needs me, Stalled, In loop, Approved and Done are lists of tasks across sources: a pull request shows there through its task. The GitHub views list what exists in GitHub:

| View | Shows |
|---|---|
| My PRs | your own pull requests, those you took over included |
| Backlog | charts of the open pull requests: who they wait on, age, size against risk, tags, failing checks |
| All | a dense list of everything in the inventory |
| Recent | the newest events of every unit the loop follows: pull requests and the units of the other sources with a card |
| Explore | search other people's pull requests to help on |

Each view of `views.toml` explains itself: its item in the list of views shows its `summary` as a tooltip, on hover and on keyboard focus; its heading shows the summary under the label, and **Why these tasks?** opens the view's `when`, `sort` and `group` as your profile writes them. A view that lists nothing shows its `empty`.

Done and Approved offer **Remove all clean**, which removes the workspaces their tasks' pull requests hold, keeping each one whose checkout holds anything not published, with why (any view whose `remove_clean` is set in `views.toml` offers it). Task lists and plugin row views draw their items with the same two-line row. The first line holds a task's state glyph (its shape the state's category, its hue whom the task waits on, a button that filters by the state), the kind pieces, the title without the unit label the second line shows, a dot when the task changed since you saw it and a marker counting its pending drafts, then on the right the meta and flag pieces (at most the facet's `show_max`, then `+n`) and the actions. The second line holds the ref and why it waits, then on the right the workspace and link, whom it waits on, and the date the time rule picks with its label: since when it waits on you, or when someone last moved it (a pending round shows its countdown to the deadline of its open plan gate instead). Each line stays one line, the title and the reason cut with an ellipsis. Each piece comes from a facet its profile or plugin places on the row (see [Where a facet shows on a row](/concepts/tasks/#where-a-facet-shows-on-a-row)), and every piece that shows a facet's value is a button that filters by it; the Sort menu orders the list. A grouped list puts each group under a sticky head with its count. A row whose provider has a strip facet shows a dot in its hue before whom it waits on (see [The strip](/concepts/tasks/#the-strip)). The detail panel holds the timeline, the brief, the agent's question with its answers, and the actions. A unit's label opens it in the cockpit, in its plugin's view; a unit its plugin no longer lists, such as a closed agent session, opens the first open task that holds it. The service's name beside it (GitHub, Slack) opens the unit there, in a new tab. The filter box (`/`) takes text and qualifiers such as `where:`, `author:@me` or `is:stalled`; see [Cockpit filter](/reference/cockpit-filter/).

Every list, of tasks or of a plugin's rows, has the same bar over the [facets](/concepts/tasks/#facets) of its items: Group and Sort menus, List or Board, a chip per filter, the count it shows of the view, and **Copy list**. The menus offer a field only when some item of the view has a value for it, so Needs me, which mixes Slack conversations and pull requests, offers `where`, `ci` and `source`, and My PRs offers no Slack field. A task view groups by the facet that reads the fact its `group` in `views.toml` names unless you pick another (or None), and keeps the daemon's order until you pick a sort; the sort menu calls that order by `default_sort`, a row view's by its plugin's `ui.toml`. A board puts a lane per group, each item a card with its list row's slots, the workspace and actions aside: the title, which opens it from anywhere on the card, then the kind, meta and flags, the ref and reason, whom it waits on and the date, each piece the same facet button as on the list and the tooltip the row's (a draft's card keeps the draft under its title). A plugin's own view lists its rows with the same row. With nothing picked a board groups by the facet that colors the strip (a task view's is the one its profile marks `strip`, the bundled `turn`; a row view's is its plugin's) when the view offers it, else it is one lane. Filters, grouping, order and layout stay in the URL; a view that does not offer the group or sort shows its default and leaves them in the URL for the views that do. The menus' and Copy list's words are `[lens]` in `views.toml`. The filter keeps the tasks the rail counts. **Copy list** copies what the view shows, in its order, as YAML under `items:`: each item's reference (a task's id, a row's label), link, title and units, then its facets by id, to paste into an agent session with a prompt.

A task's page shows, in order: its header (title, state, the properties your profile declares, and its links, each opening in the cockpit with the service's name beside it), the asks with the quotes they came from, the brief, the gates that wait on you, each with the intents of its gate in `gates.toml` (`1` to `9` pick one while the gate has the focus; an intent that takes an answer reads the box above it), the runs with Open workspace, Enter session and Transcript, the timeline as a chat you narrow by kind, the composer, and the task's children. **Send**, the default that Ctrl+Enter runs, writes to the task's agent (see [Chat](/concepts/tasks/#chat)); **Send reply** posts the text as a reply in the task's conversation at once and **Save as draft** holds it until you approve it, both through the verb whose handler is `reply`; the one its `send` in `tasks.toml` names comes first.

The cockpit reads in the interface language (`ui.locale`, else the system's; see [Translate the interface](/guides/localization/)): its own words are the profile's `strings.toml`, the core's event kinds are `timeline.toml`'s, and dates and numbers follow the locale. It renders in English at once when the daemon does not serve the strings within a moment, and follows a language changed while it is open.

## The terminal client

```sh
{{command}} tui
```

The views of your profile's `views.toml`, in its order and starting on the first (the bundled Inbox, Needs me, Waiting, Chase, Fits now, Blocked by me, Stalled, In loop, Approved, Snoozed, Focus, Projects, Someday, Logbook, Done, Outbox, Review), the plugins' views (My PRs, the plan), updated live. A pending round's row counts down to the deadline of its open plan gate. The `/` filter takes the cockpit's qualifiers, so a list and the rail count the same tasks. A task view groups by the facet that reads the fact its `group` names, and a row view is ungrouped; `=` steps the group through the view's own, each other field some item of the view has a value for, and none, and `-` steps the sort through the view's own order and each sortable field, ascending then descending. The terminal has no board. A task's line, a row's line and a draft's line in the Outbox are the same: the age (a pending round's countdown, in the `countdown_*` row words), the ref (a draft's target), the title (a draft's text), whom it waits on with its kind (a draft's state) from 60 columns, and the meta part from 100 columns (a project's review facts with it); flags, the reason and the workspace are in the detail. A task's detail shows its asks with the quotes they came from, its open gates with their intents (`1` to `9` pick one; an intent that takes an answer asks for it first), and its runs with where they run and the command that resumes them. A verb's key runs the verb your profile serves, and a verb that asks for a value asks the question its `input` sets. While a plugin has not reported every unit since the daemon started, a line names it and why; a row its plugin last reported before it went down shows `(stale)`. The keys are the cockpit's: lowercase keys file tasks, uppercase keys act on a row (`R` run now, `S` skip, `G` send with guidance, `W` answer, `C` continue, `X` dismiss, `H` pause, `M` mute, `A` take over, `O` open, `V` review, `F` address, `N` nudge). `Ctrl+K` opens the palette with every action, `?` the help, where a view's key shows its `summary`, `q` quits. A view that lists nothing shows its `empty`. Actions that publish or take over ask for a `y` first. See [Keys](/reference/keys/).

`{{command}} status` prints the tasks of the views the badge counts, then the pull requests you track, once, for a script or a quick look.

The terminal client reads in the interface language, with the words of the profile's `strings.toml` (English until the daemon serves them); key names stay as written.

## The menu bar (macOS)

`{{command}} app`, which the LaunchAgent starts from `{{app}}.app`, puts an item in the menu bar. It shows `PR <n>`, the count of the views your profile's `[badge]` names (the bundled Inbox and Needs me), or `PR ?` while the daemon does not answer. Its menu names the plugins still loading, lists up to five tasks of each badge view (a pending round's task among them, through its open plan gate), then any section a plugin's manifest declares (`[[source.ui.menubar]]`; the bundled plugins declare none), then Open cockpit (on the first badge view, in the app's window when it has one), Sweep now, Pause automatic rounds (or Resume), the sync line and Quit. Quit stops the menu bar; the daemon keeps running. The menu bar and its notes read in the interface language (`ui.locale`, else the system's; see [Translate the interface](/guides/localization/)), with the words of the profile's `strings.toml`; until the daemon answers they read in English.

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
