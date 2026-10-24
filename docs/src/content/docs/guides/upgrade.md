---
title: Upgrade
description: Upgrade the binary and refresh what it installed.
---

```sh
brew upgrade {{command}}
{{command}} install            # the hooks, permissions, MCP registration and skills of every agent home
{{command}} app install        # macOS: rebuilds {{app}}.app and reloads both LaunchAgents
{{command}} doctor
```

Run them in this order. `install` rewrites what each agent home needs from the new release; `app install` rebuilds the app bundle from the new binary and restarts the daemon and the menu bar on it. On a Mac without the menu bar, use `{{command}} app install --no-menu-bar`. On Linux, restart your `{{command}} serve` service.

An open cockpit tab notices the restarted daemon serves another build and reloads itself. While it holds text you have not sent (a reply, a guidance, an answer, a draft being edited) or an open dialog, it shows "A new version is running." with a **Reload** button instead.

## The store

A release that changes the store's schema migrates it when its daemon starts, which happens on its own about 20 seconds after `brew upgrade` replaces the binary. First it copies the store to `<state>/backups/store-v<version>-<time>.db`; then it runs each migration in its own transaction and checks the result. `serve.err.log` lists each step, and `{{command}} doctor` shows the schema version and the newest backup. The three newest pre-migration backups stay.

A migration that fails leaves the store at the last version it reached, usually the one it started from, and the daemon serves only its health until you act: the menu bar shows `PR !` with the reason and the command, the cockpit and `doctor` show the same message. Run the command in a terminal to retry with each step shown:

```sh
{{command}} store migrate --dry-run   # the migrations, the backup and the space it needs
{{command}} store migrate
```

A build that is not a release, such as `make dev-install` from a checkout, never migrates on its own; `make dev-install` runs `store migrate`, which asks first.

`doctor` reports a daemon older than the CLI, and a client and a daemon of different API major versions refuse each other and name the one to restart. To restart the daemon by hand on macOS:

```sh
launchctl kickstart -k gui/$(id -u)/io.github.{{org}}.{{name}}
```

## Going back to an earlier release

A release can declare that earlier releases keep reading the store after its migration, when it only adds what they ignore; such a downgrade needs nothing more. Otherwise the earlier release's daemon refuses the newer store and serves only its health, naming `{{command}} store restore`, which puts back the newest backup it can read and keeps the replaced store in `<state>/backups` as a pre-restore copy. What changed since that backup is lost: sessions, notes, ledger entries and drafts; pull requests come back from GitHub on the next sync.

```sh
{{command}} store restore --list   # the backups, their schema versions and dates
{{command}} store restore          # the newest one this release reads
```

Release v0.1.0 has no `store restore` and refuses a store at any version but 1; to go back to it, run `{{command}} store restore` with the newer binary first, choosing a backup at the earlier release's version.

Run `{{command}} uninstall` first, while the newer binary is still installed: it takes the LaunchAgents, the app and the MCP registration, the hooks and the permissions of every agent home out, which an earlier release would otherwise leave calling a command it may not have; the config and the data stay. Then install the earlier release and run `install` and `app install` again.

## Rotating the MCP key

The daemon's MCP endpoint requires a per-install secret, `<state>/mcp.key`, which `install` writes into every home's registration as the `X-Loop-Key` header. To rotate it, delete the file and run `{{command}} install` again. Sessions started before keep the old header until they restart.
