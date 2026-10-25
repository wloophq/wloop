---
title: Install
description: Install the binary with Homebrew, then set it up.
---

## Homebrew

On macOS or Linux:

```sh
brew install {{org}}/tap/{{command}}
{{command}} init
```

[`{{command}} init`](/getting-started/init/) checks the requirements, writes your configuration and installs the pieces below, asking before each one. You can also install them one by one:

```sh
{{command}} install        # MCP server, hooks, permissions and skills in every Claude Code home
{{command}} app install    # macOS: {{app}}.app and the LaunchAgents of the daemon and the menu bar
{{command}} doctor         # what is still missing, with the command that fixes each item
{{command}} open           # the cockpit in your browser
```

## What `install` writes

`{{command}} install` writes into each Claude Code home it finds:

- the registration of {{name}}'s MCP server at user scope, with the URL from `mcp.port`, the `X-Loop-Key` header, and `{{command}} hook mcp-headers` as the `headersHelper` that names the session in `X-Loop-Session`;
- the `Stop`, `SessionStart` and `UserPromptSubmit` hooks (`{{command}} hook stop || true`, and so on);
- the permissions in `settings.json`: `mcp__{{name}}__turn_end` allowed, and `mcp__{{name}}__slack_send_message` under `ask`, so Claude Code asks before every Slack post;
- the bundled skills (`{{name}}`, `{{name}}-review`, `{{name}}-address`, `{{name}}-nudge` and `{{name}}-profile`), also into `~/.agents/skills` for Codex.

It finds the homes on its own: `~/.claude`, `CLAUDE_CONFIG_DIR` when set, every `agent_home` of the configuration and of each `projects` entry, and every Orca Claude profile home. `--home <dir>` (repeatable) names them instead. It backs up `settings.json` and the MCP registration before their first change, replaces a skill only while it is unedited, prints every entry it adds, changes or removes, and is safe to run again. `--dry-run` prints and changes nothing. It needs no daemon. The hooks and the `headersHelper` call the binary by the absolute path you ran `install` with, a symlink such as Homebrew's `bin/{{command}}` included; a hook that calls the same binary by another path, its link or its target, is right as it is, so `{{command}} doctor` and the daemon, which may run the binary by different paths, agree on it.

Run it again after changing `mcp.port` and after every upgrade.

## macOS: the app and the daemon

`{{command}} app install` builds `~/Applications/{{app}}.app` from the binary you run it with, signs it ad hoc, and installs two LaunchAgents:

- `io.github.{{org}}.{{name}}` runs `{{command}} serve` through your login shell, so the daemon gets your profile's `PATH`, at login and again whenever it exits;
- `io.github.{{org}}.{{name}}.app` runs the menu bar at login and restarts it only after a crash, so Quit stays quit until the next login.

macOS gives notifications to apps, not to bare binaries, and the stable path of the bundle keeps your answer to the notification permission across upgrades. The first start asks to allow notifications: allow them.

On a Mac where you do not want the menu bar, `{{command}} app install --no-menu-bar` installs only the daemon's LaunchAgent.

## Linux

Run `{{command}} serve` under your init system. A systemd user service, for example:

```ini
# ~/.config/systemd/user/{{name}}.service
[Unit]
Description={{name}} daemon

[Service]
ExecStart=/bin/sh -lc 'exec {{command}} serve'
Restart=always

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now {{name}}.service
```

The login shell (`sh -l`) gives the daemon the `PATH` where `gh`, `git`, `orca` and `claude` live. Without notifications, each automatic round still waits in its approval window, and the cockpit and the terminal client let you act on it.

## Check the setup

```sh
{{command}} doctor
```

`doctor` changes nothing. It checks `gh`, each workspace manager, the configuration, every agent home, the review profile, the LaunchAgents, the daemon and its jobs, and prints the fix for each problem on its own line. See [Troubleshooting](/guides/troubleshooting/).
