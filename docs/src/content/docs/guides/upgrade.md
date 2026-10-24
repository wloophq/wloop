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

`doctor` reports a daemon older than the CLI, and a client and a daemon of different API major versions refuse each other and name the one to restart. To restart the daemon by hand on macOS:

```sh
launchctl kickstart -k gui/$(id -u)/io.github.{{org}}.{{name}}
```

## The store

When a release changes the store's schema, the daemon migrates `<state>/store.db` the first time it starts on it. Before the first change it copies the store beside itself as `store.db.v<N>.bak`, `<N>` being the version it had; an existing backup is kept and the new one takes a time suffix. An earlier release refuses a migrated store, and `doctor` says so.

## Going back to an earlier release

When the newer release migrated the store, stop the daemon and move the backup over the store before installing the earlier release, which cannot read the migrated one:

```sh
launchctl bootout gui/$(id -u)/io.github.{{org}}.{{name}}    # macOS; on Linux, stop your {{command}} serve service
mv <state>/store.db.v1.bak <state>/store.db
rm -f <state>/store.db-wal <state>/store.db-shm
```

What the daemon recorded after the migration is lost with the newer store.

Run `{{command}} uninstall` first, while the newer binary is still installed: it takes the LaunchAgents, the app and the MCP registration, the hooks and the permissions of every agent home out, which an earlier release would otherwise leave calling a command it may not have; the config and the data stay. Then install the earlier release and run `install` and `app install` again.

## Rotating the MCP key

The daemon's MCP endpoint requires a per-install secret, `<state>/mcp.key`, which `install` writes into every home's registration as the `X-Loop-Key` header. To rotate it, delete the file and run `{{command}} install` again. Sessions started before keep the old header until they restart.
