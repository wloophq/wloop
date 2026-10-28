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
| `store.db` | the SQLite store, which only the daemon opens: the inventory and the history of every pull request, your holds and mutes, the round state and the pause, the turns with their instructions, records and answers, the agent sessions, the tasks, the Inbox messages, the Outbox drafts, the items and the admitted Slack messages. Open it read-only while the daemon runs |
| `mcp.key` | the secret of the MCP endpoint (mode 0600) |
| `daemon.sock` | the socket the CLI talks to |
| `daemon.lock` | keeps a second daemon from starting |
| `store.lock` | held by whoever writes the store: the daemon, or `store migrate` and `store restore` while they run |
| `backups/` | copies of the store taken before each migration (`store-v<version>-<time>.db`, the three newest kept) and before each restore (`*.pre-restore.db`, kept 30 days) |
| `http.json` | the cockpit's port and token (mode 0600), kept across restarts so an open page reconnects |
| `notified.json` | the held rounds the menu bar already announced today |
| `dossiers/` | the dossiers and their archives (mode 0700) |
| `headless/` | the empty directory headless turns run in |
| `brief/` | the empty directory brief calls run in |
| `worktrees/<repo>/<name>/` | the worktrees of the Claude Desktop workspaces, which {{name}} creates and removes |
| `serve.out.log`, `serve.err.log` | the daemon's output under its LaunchAgent |
| `app.out.log`, `app.err.log` | the menu bar's output |

## Dossiers

```text
<state>/dossiers/<provider>/<unit-key>/
<state>/dossiers/project/<slug>/
  notes.md      curated notes
  sources/      material gathered
  drafts/       drafts being written
  log/          one append-only file per turn
<state>/dossiers/.archive/
```

Each dossier is a local git repository the daemon commits after every turn, never inside a repository checkout. After each turn the daemon checks the rules (only a compaction or the closing call rewrites `notes.md`, `log/` is append-only, no deletes, links or nested repositories) and restores what broke them.

| Status | When |
|---|---|
| active | the unit or project has open work; never archived or purged |
| resolved | no open item or task is left |
| archived | `dossiers.archive_after` (30 days) after it resolved, as a tarball under `.archive/` |
| purged | `dossiers.purge_after` (400 days) after the archive |

Open work on the unit again revives the dossier, from its archive if needed.

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
