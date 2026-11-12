---
title: The daemon and its clients
description: The single writer of the store, the API every client speaks, its transports, and what each kind of caller may do.
---

Every piece of {{name}} is the same binary. One process, the daemon (`{{command}} serve`), owns the state; everything else is a client of it or one of its children.

## One writer

The daemon is the only process that opens the store as its writer: `internal/cli/storecmd.go` calls `sqlite.Open` for it, under the daemon's lock, and an architecture test refuses that call anywhere else outside the store's own packages. The same file holds `{{command}} store migrate` and `store restore`, which take the store's lock and stop the daemon first when it has the store open. Inside the daemon, writes go through `DB.Write`, one writer connection, and reads through a read-only pool. Every handle a process opens on one store file shares one write lock, so its write transactions queue in the process rather than polling SQLite's file lock. A command that needs the store while the daemon is down, such as the disk checks of `doctor`, opens it with `sqlite.OpenReadOnly`; `sqlite.Check` reads its schema version from the file header without opening it.

`schema.sql` is the current schema and `SchemaVersion` its version. An older store migrates forward, one version at a time, after a backup under the state directory; a release build migrates it when the daemon starts, any other build waits for `{{command}} store migrate`. A store newer than the binary is refused, and the daemon serves only its health until that changes. [Development](/architecture/development/#changing-the-stores-schema) has the steps to change the schema.

Each write notes what it changed in the daemon's change log (`internal/core/change`), kept in memory with an epoch. The read model and the clients follow it: rows are derived once from one store snapshot, revised in memory, and pulled by clients as deltas after a server-sent event.

The tasks are pulled the same way, a page since a position. A task's view also reads what is not the task's own row in the store: its units' rows, briefs and links, and its placement. The daemon has no revision per unit, so the event says which units moved: after a write of the tasks, their gates, wakes, drafts, placements, a unit's records or links, and after a row change whose units a task holds, the event carries the counters and those units, and a client that sees counters asks for the page since its position naming them (`units`). That page also holds every task holding one of them, its own unit included, those out of the view in its removed. A change too large to list is All, and it or a reconnection makes the client read its list whole.

```
a unit's row, brief or links ─┐
a task, gate, wake or draft ───┴─▶ change log ─┬─▶ read model ─▶ row change ─┐
                                               └─▶ tasks feed ◀──────────────┘
                                                       │ the units a task holds, the counters
                                                       ▼
                                           event ─▶ client ─▶ GET /api/tasks?since=…&units=…
```

An event names the rest of what moved, and a client reads only that: `plan` after a note of what `GET /api/plan` serves (a plan item's served columns, which the ledger compares before and after each write, never its save time, and its measured gain only at the tenth of a MiB or GiB a client shows, so a sweep pass that rewrites the same items notes nothing; the pending items; a session's close, whose closed sessions the plan lists; a reloaded profile), `gates` after a note of a gate or of what one shows (a draft and its items, a message, a pending plan item), `health` when the health a client shows moved. These flags never move the rows' position. The plan shows its units' rows, so a client also reads it again when a row change names a unit the plan holds; a reconnection reads everything again. A `notify` event carries its notices and makes no client read anything.

```
plan item, pending item, closed session, profile ─▶ plan  ─▶ GET /api/plan
gate, draft, item, message, pending item         ─▶ gates ─▶ GET /api/gates
a burst of health notes, the menu bar, a recheck ─▶ digest moved? ─▶ health ─▶ GET /api/health
```

The health feed builds the health after each burst of health notes (a job's end, a pause, the profile, the menu bar's status), when the menu bar connects or leaves, and every 30 seconds while a client listens, for the fields no note announces (a plugin's status, git access, the gate and effect warnings, the brief breaker's words, the viewer). It digests the health as a client shows it, without a job's last run, whether it runs, the last success of a job other than an ingestion (the sync labels read only the ingestions') and the brief queue's counts (only `doctor` reads them), and publishes only when the digest moved. A successful run of a job nobody's health shows publishes nothing; an ingestion's end moves its last success and publishes. Whether a job runs travels with no event: a client sees a sync running only when it reads the health mid-run.

The daemon computes a task's facets when it serves the task: its own facts and its brief's fields, joined with the facets of its units' rows from the read model snapshot the page already reads (see [Facets](/concepts/tasks/#facets)). Clients do no join, and the daemon counts the rail the same way: `GET /api/counters` takes the whole query as `q`, qualifiers included, and counts with the lens the clients filter with (`internal/api/lens`), so `where:general` narrows the counts as it narrows the list. `@me` in a qualifier stands for `me`. The same package places an item's facets on a row: `lens.Parts` returns, per part, the pieces each spec's `show` puts there, with their labels and the age in the profile's words, and app.js mirrors it; `internal/api/lens/testdata/cases.json` holds the cases both run. A spec of another provider places nothing on an item, and a facet the catalog merges across providers keeps each provider's display keys in `displays`, which `FacetSpec.For` applies to that provider's rows. A task page serves each task's `reason`, its primary unit's row's. A verb that takes another task (merge, relate) picks it from `GET /api/tasks?match=<text>&except=<id>`: the open tasks but the task it picks for, whose title holds the text, case aside, whatever the view, the newest change first, each with only its id, title, state, category and units, so both clients list the same tasks however much of a view they hold. Split and unrelate pick from the task's own Merged from and Related lists, and the daemon serves them disabled, with the `nothing_to_pick` notice, while that list is empty.

The home and the decision queues read the views the same way, in one pass over the tasks (`coretasks.Service.Sets`), which the counters share: `GET /api/home` serves the briefing, the home's sections and the capture hint, and `GET /api/queues/{kind}` a decision kind's items, the oldest first, narrowed by `q` as the counters are (see [The home and decision queues](/concepts/home/)).

The reads that start while the change log's generation holds share one pass, its tasks and the facts read of each, for at most two seconds: a screen's tasks, counters, home and queues, and the tasks feed's publish before them, read every task's facts once. A write that notes a change starts a new pass, a page whose position is newer than the shared pass lists from the store, and a fact a write does not note, or one that moves with the clock, is late by two seconds at most.

A page of the rows names the version of `GET /api/catalog` (`catalog`), and a page of the tasks that of `GET /api/views` (`specs`); a client holding another version reads it again, so a reloaded profile reaches it. A reload also moves every task in the next change (`all`), since it can change their facets, so a paged view reads its tasks again.

## The API

`internal/api` is the one schema every client speaks: the wire types, the route paths and the version. It imports only the domain. The version is a major, sent in the `X-Loop-API` header; the daemon answers a client of another major with 426 and both versions, and a change older clients can ignore, such as a new field, keeps it.

| Role | Prefix | Who calls it | Served on |
|---|---|---|---|
| UserAPI | `/api/...` | the CLI, the terminal client, the menu bar, the cockpit | the Unix socket, and the localhost TCP port behind a token |
| AgentAPI | `/api/agent/` | the hooks and `turn end`; agent sessions through MCP | the Unix socket; MCP on its own port |
| AdminAPI | `/api/admin/` | `doctor`, `probe`, `profile try`, timings | the Unix socket only |

The TCP port answers 404 for AgentAPI and AdminAPI, refuses an unexpected `Host` and cross-site writes, and needs the token the daemon writes to its info file. AgentAPI answers while the start-up reconcile runs; UserAPI and AdminAPI wait for it.

## Transports

- **The Unix socket** carries every role, for the clients on this machine. A CLI run inside an agent session names that session on every call (`X-Loop-Session`), so the daemon applies the same authority to a shell as to the MCP tools: an agent resolves no gate, and a draft is approved by a person only.
- **Localhost HTTP** serves the cockpit's static files (`internal/web`), UserAPI and the event stream.
- **MCP** (`internal/transport/mcp`) serves the agent sessions on `127.0.0.1:47800` (`mcp.port`): `turn_end`, `task_post` and `task_read`, and the Slack tools when their plugin is enabled. A session is identified by the `X-Loop-Session` header its manager fills, never by a tool argument, and every call carries the per-install key in `X-Loop-Key`.

## Clients

A client parses input, calls one API operation and renders the answer. The daemon computes the rows, their actions, the facets, the counters and the notifications; no client derives a situation or decides which actions a row offers. An action whose answer carries a `copy` (plain text and HTML the daemon rendered from the profile) is handled the same way by every client, whatever the action: the cockpit writes the clipboard, claiming it in the click when the action's spec says `copies`, since WebKit refuses a write that starts after the answer arrives; the terminal client and the CLI copy the plain text, or print it where they cannot. Clients import only `internal/api`, `internal/api/client`, the domain and `internal/termfmt`.

- The CLI, the terminal client (`internal/client/tui`) and the menu bar (`internal/client/menubar`, its own process under its own LaunchAgent) call UserAPI over the socket.
- The cockpit calls UserAPI over HTTP. Its scripts, plain files under `internal/web/static` loaded in order by `index.html`, each have one reason to change:
  - `app.js`: the boot, the state the pages share, the lens's pieces (`rowParts`, mirroring `lens.Parts`), the plugin rows and the unit detail;
  - `i18n.js`: the profile's strings through `t` and `tn`, dates and numbers in the user's locale;
  - `markdown.js`: agent prose as DOM nodes, safe by construction, only `https://` links;
  - `row.js`: the one two-line row every list draws, task and plugin rows alike, and its board card;
  - `rail.js`: the rail, its views by section with the empty ones folded, whom the rows wait on, the omnibox entry;
  - `toolbar.js`: a list's toolbar: group, sort, filter chips, **+ Filter**, layout, copy;
  - `views.js` and `review.js`: the task views and the Review that walks them;
  - `gate.js`: the one decision card, a gate or a draft with its actions, for the detail, the Outbox and the queues;
  - `task.js`: the task page: head, properties, primary verbs, tabs, composer;
  - `timeline.js`: a task's timeline as the facts the daemon folds, raw events folded under each;
  - `outbox.js`: the Outbox, its drafts grouped by why;
  - `detail.js`: the detail panel that holds a task's or a unit's page;
  - `window.js`: the title bar a native window draws over the page, the detach of a detail and the links to a view;
  - `keys.js` and `palette.js`: the served key map and the palette and help it opens;
  - `dash.js`, `plugins.js`, `routes.js`, `panel.js` and `update.js`: the dashboard, plugin views, routes, the omnibox panel and the update banner.
- The window (`internal/client/window`) runs in the menu bar's process on macOS. It shows the cockpit's own URL in one main window, an omnibox panel and a window per detached task or unit, through a `Host` port that one adapter implements: `internal/client/window/wails`, the only package that imports Wails, built with the `purego` tag on darwin. The main and detached windows draw their controls over the page and mark its URL `titlebar=inset`, so the cockpit draws the title bar itself, dragged through Wails' `--wails-draggable`. It follows the event stream as the events client `window`. Any process asks it to act with `POST /api/window` (`open` a route, show the `panel`, `hide-panel`, `detach` a route's task or unit). A `{{command}}://open/#<route>` link the system hands the app reaches the window through the same `Host`, which keeps a link that launched the app until the window listens; `window.LinkRoute` reads it, for the app and for `{{command}} open`. The daemon relays the request only to that client, without moving the rows' position, and answers whether one took it, so `{{command}} open` falls back to the browser when none did. The panel's page reaches the window the same way, through `panel.js`, with no bridge between Go and the page.
- The hooks (`internal/client/hook`) call AgentAPI with a 2-second budget, allow on any failure and never block an agent except to hold a stop that owes a record.

With the daemon down, every client fails within a second and names the fix, and nothing is spooled for later. Only the local checks of `doctor` and the developer tools read the store then, read-only.

## Children

The daemon starts the headless agent calls (the brief, headless turns, the Slack extraction, profile dry runs) as its own children through the agent's port. They are no clients and never reach the store. Each `claude -p` the daemon runs leads its own process group. A cancelled call sends the group SIGTERM, then SIGKILL after five seconds; once the call ends, whatever it left in the group is stopped the same way, and a stopping daemon kills every group still running. The daemon lists the live groups in its state directory, so after a crash the next start kills what the crashed one left. Every package that spawns a process imports `internal/meter`, so `{{command}} doctor --load` counts each spawn.

## Authority

Agents report facts; only your surfaces grant authority: endorsing a verdict, approving a draft, taking over a pull request, removing a workspace. Every post the loop proposes is a draft first, and only an approved draft reaches the plugin's publisher. Agents also post as you without a draft: rounds publish on GitHub through `gh`, and the tools an agent calls to post, react or edit on Slack at once go through the daemon, which records each post afterwards as a sent draft on the unit it landed in, tracked or not, with the proposing turn. The loop never hands an agent a source's token; agents read and draft Slack through the daemon's tools. [Security](/concepts/security/) states the limits of this boundary.
