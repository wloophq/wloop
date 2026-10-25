---
title: How the pieces fit
description: The daemon, its clients, the ports to the outside tools, and the rules that keep them apart.
---

{{name}} orchestrates turns and keeps their state, context and history. The workspace managers, Orca and Claude Desktop, host the worktrees and the agent sessions in them. The words this page uses are defined in the [glossary](/architecture/glossary/). Everything you or an agent touches is a thin client of one daemon, and every outside tool sits behind a small port.

## System context

| Actor | Gives {{name}} | Gets from {{name}} |
|---|---|---|
| You | answers, guidance, holds, review and take-over requests | questions, drafts, counts, notifications, the cockpit |
| GitHub | pull requests, reviews, checks and the rate budget, read over its API with the token of `gh` | the comments of drafts you approved, posted by the GitHub plugin |
| Agents (Claude Code) | turn records and hook events | instructions; they publish reviews, push and reply on GitHub under your name |
| Orca | the workspace list, terminals, agent activity, card placement | workspaces created, linked, renamed and removed; sessions started, woken and typed into; board writes |
| Claude Desktop | the sessions it runs and their processes, the worktrees it made for the sessions not archived | sessions imported, woken and typed into; worktrees under a root {{name}} owns; the app's own worktrees removed when you remove their workspace |
| macOS | | the LaunchAgents, the app bundle, notifications |

Each manager is both an actuator (workspaces, sessions, and Orca's board) and a source of live signals (processes, activity, focus), never of record.

## Managers and custody

Both managers run at the same time. `workspace_managers` lists the enabled ones in order (by default Claude Desktop, then Orca). A new worktree goes to the first manager that is enabled, available, hosts the project's agent and honors the session's launch policy; that manager becomes its owner. The store records the custody of every worktree and the units it stands for, so a worktree two managers list still has one owner: the manager that created it, as custody or the manager itself says, else the one custody names, else the one with a live agent there, else the first by order. A round's new session starts in the owner; when the owner cannot launch it, it starts in the next manager by order that lists the worktree and can, and the daemon logs why the owner could not.

Sessions are routed by the id {{name}} mints, not by a handle. A turn names its session; the router finds the session's manager and the handle of its live process. A session missing from its manager's process census turns dormant, never closed, and a manager that closes or restarts never marks another manager's sessions. The next turn wakes a dormant session in its own manager when it is still warm, and starts a new session otherwise.

## One binary, one daemon

The same binary is every piece:

- **the daemon**, `{{command}} serve`, a single instance that owns the SQLite store and serves the API over a Unix socket, the cockpit over localhost HTTP with a token and server-sent events, and the MCP endpoint on a fixed localhost port;
- **the CLI**, the **terminal client** (`tui`) and the **menu bar** (`app`), which call the API over the socket;
- **the web cockpit**, which calls the same API over HTTP;
- **the agent hooks** (`hook session-start`, `hook stop`, `hook prompt`), which call the agent side of the API with a 2-second budget and never block an agent by failing;
- **agent sessions**, which call `turn_end` and the Slack tools over MCP, identified by the `X-Loop-Session` header, never by a tool argument. The registration's `headersHelper`, `{{command}} hook mcp-headers`, runs in the session's environment and sets the header to the process handle a manager put there (`ORCA_TERMINAL_HANDLE` in Orca, `CLAUDE_CODE_HOST_SESSION_ID` in Claude Desktop); the daemon resolves it to the session. A `${VAR}` header would not do: Claude Desktop sends it empty.

The daemon also spawns headless agent calls (the brief, headless turns, the Slack extraction, profile dry runs); those are its children, not clients.

## Inside the daemon

The daemon is laid out as ports and adapters: transports call the API, the API calls the core's use cases, the use cases work on the domain and reach every outside tool through a port, and adapters implement the ports. The pages below hold the detail:

- [Layers and the dependency rule](/architecture/layers/): the packages, what each may import, the composition root and where a new piece of code goes;
- [Ports and adapters](/architecture/ports/): each outside tool, its port, its adapters and its fake;
- [The daemon and its clients](/architecture/daemon-and-clients/): the single writer of the store, the API roles and the transports;
- [Plugins](/architecture/plugins/): how a source of units plugs into the GTD core;
- [Profile first](/architecture/profile-first/): what the profile decides and what Go provides;
- [Testing](/architecture/testing/): fakes, goldens and the architecture tests;
- [Decision log](/architecture/decisions/): each decision in force and the test that keeps it.

Four properties hold across them:

- **The store is the truth.** Turn state, questions, answers and history live only in the store. Orca's board is a view sink: it receives the same projection as every other view, fails alone and is reconciled on start. Any workspace or session can be recreated from stored context, in any manager that hosts its agent.
- **Every change leaves a trail.** Each command writes its domain events in the same transaction as its state change, so a turn's timeline can be read from events alone.
- **Authority comes from you.** Agents report facts; only your surfaces endorse a verdict, approve a draft, take over or remove a workspace.
- **Down means down.** With the daemon stopped, every client fails within a second and names the fix; only the hooks allow and go on. On start the daemon reconciles turns, sessions and cards before it serves clients.
