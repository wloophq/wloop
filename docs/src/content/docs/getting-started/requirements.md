---
title: Requirements
description: What must be installed and running before the first review.
---

## Tools

| Tool | Why | Check |
|---|---|---|
| [GitHub CLI](https://cli.github.com/) (`gh`), logged in | {{name}} reads pull requests through GitHub's GraphQL API with your `gh` login, and agents publish reviews and comments with it | `gh auth status` |
| `git` | worktrees, fetches and the checks that keep a workspace from losing work; fetches must run without a password prompt | `git --version` |
| A workspace manager: [Claude Desktop](https://claude.ai/download), [Orca](https://github.com/stablyai/orca), or both | hosts the worktrees and the agent sessions; see below | `{{command}} doctor` |
| [Claude Code](https://docs.claude.com/en/docs/claude-code) (`claude`) | the agent that runs every round; {{name}} registers its MCP server, hooks and skills in each Claude Code home | `claude --version` |

## Workspace managers

You need at least one of them. With both installed, {{name}} serves both at once, and `workspace_managers` (by default `["claude-desktop", "orca"]`) says which a new worktree goes to first.

| Manager | Hosts | Board | Check |
|---|---|---|---|
| Claude Desktop | Claude Code sessions, in worktrees under a root {{name}} owns | no | `/Applications/Claude.app` and `claude` on the `PATH` |
| Orca, running | any agent, in terminals; {{name}} drives it through its CLI and runtime | yes | `orca --version` |

Claude Desktop cannot start a session with your user settings alone, a strict or MCP-free session, or an agent home other than the default. Such a session, for example a review of a pull request whose project settings stay off, goes to the next manager in the list; with no manager able to launch it, the round fails and says why. `{{command}} doctor` reports each manager and each intent it cannot launch. Turn off PR Auto-fix in Claude Desktop's settings: when it is on, Desktop starts turns of its own on your pull requests that compete with {{name}}'s, and {{name}} cannot read the setting, so `doctor` only reminds you of it. See [Configuration](/reference/configuration/).

Claude Code is the only agent that runs rounds. Codex can run the bundled skills by hand: `{{command}} install` also copies them into `~/.agents/skills`.

`{{command}} doctor` checks each of these and prints the command that fixes what is missing. [`{{command}} init`](/getting-started/init/) runs the same checks first.

## Platforms

- **macOS** runs everything: the daemon under a LaunchAgent, the menu bar, notifications, and the Slack login in the login Keychain.
- **Linux** runs the daemon, the cockpit, the terminal client and the CLI. There is no menu bar and no notifications, and you keep `{{command}} serve` running with your init system (see [Install](/getting-started/install/#linux)). The Slack source needs the macOS Keychain. {{name}} drives Claude Desktop through macOS's `open`, so on Linux Orca is the workspace manager.

## Optional

- **Slack**: a Slack app of your own, created from the manifest that {{name}} ships. See [Connect Slack](/guides/slack/).
- **The brief**: a short preview of each pull request, written by one headless call of the agent per pull request and billed to your agent account. Off by default. See [The brief](/concepts/brief/).
