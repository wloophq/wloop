---
title: Files on disk
description: Where the configuration, the state, the logs and the installed pieces live.
---

## Configuration

`$XDG_CONFIG_HOME/{{name}}/`, by default `~/.config/{{name}}/`:

| Path | Holds |
|---|---|
| `config.toml` | the [configuration](/reference/configuration/) |
| `profiles/<name>/` | review profiles named by `profile` |

## State

`$XDG_STATE_HOME/{{name}}/`, by default `~/.local/state/{{name}}/`:

| Path | Holds |
|---|---|
| `store.db` | the SQLite store, which only the daemon opens: the history of every unit, what each plugin last reported of it, your holds, the round state and the pause, the turns with their instructions, records and answers, the agent sessions, the tasks with their properties, wakes and gates, the messages, the drafts and the items. Open it read-only while the daemon runs |
| `plugins/bundled/<id>/` | the bundled plugins `install` placed: each one's manifest, profile layer and binary |
| `plugins/checksums` | the SHA-256 of each bundled plugin's binary, as `install` recorded it (`<hex>  <id>` lines); the daemon runs a bundled plugin only with it |
| `plugins/<id>/plugin.db` | a plugin's own store: GitHub's pull requests, Slack's admitted messages and extraction state. The plugin opens it; `import.db` is what the store's migration to version 7 exported for the plugin, renamed `import.db.done` once imported |
| `mcp.key` | the secret of the MCP endpoint (mode 0600) |
| `daemon.sock` | the socket the CLI talks to |
| `daemon.lock` | keeps a second daemon from starting |
| `store.lock` | held by whoever writes the store: the daemon, or `store migrate` and `store restore` while they run |
| `backups/` | copies of the store taken before each migration (`store-v<version>-<time>.db`, the three newest kept) and before each restore (`*.pre-restore.db`, kept 30 days), each with the plugins' stores of that moment in `<backup>.plugins/<id>/` |
| `http.json` | the cockpit's port and token (mode 0600), kept across restarts so an open page reconnects |
| `notified.json` | the held rounds the menu bar already announced today |
| `dossiers/` | left by releases before schema version 15, which kept the task dossiers there; none reads or writes it, and `doctor` names it until you remove it |
| `headless/` | the empty directory headless turns run in |
| `brief/` | the empty directory brief calls run in |
| `omnibox/run/` | the empty directory omnibox agent calls run in |
| `omnibox/snapshots/` | each running omnibox call's read-model snapshot (`ask-*/`), removed when the call returns or, after a crash, when the daemon starts |
| `worktrees/<repo>/<name>/` | the worktrees of the Claude Desktop workspaces, which {{name}} creates and removes |
| `serve.out.log`, `serve.err.log` | the daemon's output under its LaunchAgent |
| `app.out.log`, `app.err.log` | the menu bar's output |

## In each workspace

| Path | Holds |
|---|---|
| `.loop/turn-<id>.json` | a record a session without the MCP tool writes before `{{command}} turn end`, deleted once stored |

## Installed elsewhere

| Path | Written by |
|---|---|
| each Claude Code home's `settings.json` | `install`: the hooks and permissions, backed up to `<file>.loop-<stamp>.bak` before the first change |
| each Claude Code home's MCP registration | `install`, through `claude mcp add-json` at user scope |
| `<home>/skills/{{name}}*/`, `~/.agents/skills/{{name}}*/` | `install`: the bundled skills |
| `~/Applications/{{app}}.app` | `app install` (macOS) |
| `~/Library/LaunchAgents/io.github.{{org}}.{{name}}.plist` | `app install`: the daemon's LaunchAgent |
| `~/Library/LaunchAgents/io.github.{{org}}.{{name}}.app.plist` | `app install`: the menu bar's LaunchAgent |
| the login Keychain, service `{{name}}-slack` | `slack login`: the Slack tokens |

`{{command}} uninstall` removes everything in this last table; see [Uninstall](/guides/uninstall/).
