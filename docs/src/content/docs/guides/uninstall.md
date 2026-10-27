---
title: Uninstall
description: Remove the app, the background service and the agent integration, and optionally your data.
---

```sh
{{command}} uninstall --dry-run   # what it would remove, changing nothing
{{command}} uninstall
```

By default `uninstall`:

- on macOS, stops and removes both LaunchAgents and `~/Applications/{{app}}.app`;
- takes the MCP registration, the hooks, the permissions and the bundled skills out of every agent home, leaving a skill you changed;
- revokes the stored Slack tokens and deletes them from the Keychain;
- keeps your configuration and the state directory, so a later install picks up where you left.

It asks before it removes anything; `--yes` skips the question.

## Delete your data too

```sh
{{command}} uninstall --purge
```

`--purge` also deletes the state directory (`~/.local/state/{{name}}/`: the store and its backups, the MCP key, the dossiers and the logs) and the configuration directory (`~/.config/{{name}}/`: `config.toml` and the profiles in it). A profile directory outside the configuration directory is never deleted; `uninstall` prints its path so you can remove it yourself. It asks for confirmation unless `--yes`.

## Remove the package

`uninstall` never removes the binary itself. It ends by printing the Homebrew command that does:

```sh
brew uninstall --cask {{command}}
```

On Linux, also disable the service you created for `{{command}} serve`.
