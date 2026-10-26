---
title: The daemon and its clients
description: The single writer of the store, the API every client speaks, its transports, and what each kind of caller may do.
---

Every piece of {{name}} is the same binary. One process, the daemon (`{{command}} serve`), owns the state; everything else is a client of it or one of its children.

## One writer

The daemon is the only process that opens the store as its writer: `internal/cli/storecmd.go` calls `sqlite.Open` for it, under the daemon's lock, and an architecture test refuses that call anywhere else outside the store's own packages. The same file holds `{{command}} store migrate` and `store restore`, which take the store's lock and stop the daemon first when it has the store open. Inside the daemon, writes go through `DB.Write`, one writer connection, and reads through a read-only pool. A command that needs the store while the daemon is down, such as the disk checks of `doctor` or `slack eval export`, opens it with `sqlite.OpenReadOnly`; `sqlite.Check` reads its schema version from the file header without opening it.

`schema.sql` is the current schema and `SchemaVersion` its version. An older store migrates forward, one version at a time, after a backup under the state directory; a release build migrates it when the daemon starts, any other build waits for `{{command}} store migrate`. A store newer than the binary is refused, and the daemon serves only its health until that changes. [Development](/architecture/development/#changing-the-stores-schema) has the steps to change the schema.

Each write notes what it changed in the daemon's change log (`internal/core/change`), kept in memory with an epoch. The read model and the clients follow it: rows are derived once from one store snapshot, revised in memory, and pulled by clients as deltas after a server-sent event.

## The API

`internal/api` is the one schema every client speaks: the wire types, the route paths and the version. It imports only the domain. The version is a major, sent in the `X-Loop-API` header; the daemon answers a client of another major with 426 and both versions, and a change older clients can ignore, such as a new field, keeps it.

| Role | Prefix | Who calls it | Served on |
|---|---|---|---|
| UserAPI | `/api/...` | the CLI, the terminal client, the menu bar, the cockpit | the Unix socket, and the localhost TCP port behind a token |
| AgentAPI | `/api/agent/` | the hooks and `turn end`; agent sessions through MCP | the Unix socket; MCP on its own port |
| AdminAPI | `/api/admin/` | `doctor`, `probe`, `profile try`, timings | the Unix socket only |

The TCP port answers 404 for AgentAPI and AdminAPI, refuses an unexpected `Host` and cross-site writes, and needs the token the daemon writes to its info file. AgentAPI answers while the start-up reconcile runs; UserAPI and AdminAPI wait for it.

## Transports

- **The Unix socket** carries every role, for the clients on this machine.
- **Localhost HTTP** serves the cockpit's static files (`internal/web`), UserAPI and the event stream.
- **MCP** (`internal/transport/mcp`) serves the agent sessions on `127.0.0.1:47800` (`mcp.port`): `turn_end`, and the Slack and `inbox_post` tools when their plugin is enabled. A session is identified by the `X-Loop-Session` header its manager fills, never by a tool argument, and every call carries the per-install key in `X-Loop-Key`.

## Clients

A client parses input, calls one API operation and renders the answer. The daemon computes the rows, their actions, the facets, the counters and the notifications; no client derives a situation or decides which actions a row offers. Clients import only `internal/api`, `internal/api/client`, the domain and `internal/termfmt`.

- The CLI, the terminal client (`internal/client/tui`) and the menu bar (`internal/client/menubar`, its own process under its own LaunchAgent) call UserAPI over the socket.
- The cockpit calls UserAPI over HTTP.
- The hooks (`internal/client/hook`) call AgentAPI with a 2-second budget, allow on any failure and never block an agent except to hold a stop that owes a record.

With the daemon down, every client fails within a second and names the fix, and nothing is spooled for later. Only the local checks of `doctor` and the developer tools read the store then, read-only.

## Children

The daemon starts the headless agent calls (the brief, headless turns, the Slack extraction, profile dry runs) as its own children through the agent's port. They are no clients and never reach the store. Each `claude -p` the daemon runs leads its own process group. A cancelled call sends the group SIGTERM, then SIGKILL after five seconds; once the call ends, whatever it left in the group is stopped the same way, and a stopping daemon kills every group still running. The daemon lists the live groups in its state directory, so after a crash the next start kills what the crashed one left. Every package that spawns a process imports `internal/meter`, so `{{command}} doctor --load` counts each spawn.

## Authority

Agents report facts; only your surfaces grant authority: endorsing a verdict, approving a draft, taking over a pull request, removing a workspace. Every post the loop proposes is a draft first, and only an approved draft reaches the plugin's publisher. Agents also post as you without a draft: rounds publish on GitHub through `gh`, and a tool an agent calls can post to Slack at once through the daemon, recorded afterwards as a sent draft. The loop never hands an agent a source's token; agents read and draft Slack through the daemon's tools. [Security](/concepts/security/) states the limits of this boundary.
