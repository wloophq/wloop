---
title: Ports and adapters
description: Each outside tool, the port the core reaches it through, its adapters and the fake the tests use.
---

Every outside tool sits behind a port the core declares (see [Layers](/architecture/layers/#ports)). A second agent, workspace manager or view sink is a new adapter and one entry in the composition root; the test-only adapters below prove it, since each runs the full flow without editing the core.

## The ports

| Outside world | Port | Adapters | For tests |
|---|---|---|---|
| The store | the `Store` of each core package (`coretasks.Store`, `outbox.Store`, `items.Store`, `intake.Store`, `usage.Store`, `dossiers.Store`, `messages` and turn stores), `automation.Ledger`, `automation.Events`, `port.LinkStore`, `port.HeatReader` | `internal/adapter/store/sqlite`, `internal/adapter/store/inventory` (pull requests, events, overrides, messages) | an in-memory store and a contract suite in each core package's `<name>test` (`taskstest`, `messagestest`, `turnstest`, `workspacestest`); `storetest` and `sqlitetest` open real stores in a temporary directory |
| Coding agents | `turns.Agent`, `turns.Asker` (one headless prompt), `turns.HeadlessAgent`, `turns.Prompts`; optional `turns.Questions`; `setup.HomeKind` and `setup.AgentHome` for the agent's home; `harness.Protocol` for its hook payloads | `internal/adapter/agent/claude` | `internal/adapter/agent/echo`, an agent whose sessions answer with scripted records |
| Workspace managers | `workspace.Manager`; optional `Board`, `Waker`, `ScreenReader`, `TerminalCloser`, `Renamer`, `PolicyHonorer` and the other capabilities in `internal/workspace`; `harness.SessionEnv` for the session's identity | `internal/workspace/orca` with `internal/adapter/orca`, `internal/workspace/claudedesktop` with `internal/adapter/claudedesktop` | `internal/workspace/fake`, `orca/orcatest` (a fake Orca runtime) |
| View sinks | `views.ViewSink` | the Orca board, in `internal/adapter/orca` | `internal/adapter/view/testsink` |
| Sources of units | `port.Source` and `port.SourceStore`, driven by `internal/core/ingest`; the rest of a source is its `source.Module` (see [Plugins](/architecture/plugins/)) | `internal/adapter/source/github`, `internal/adapter/source/slack` | `internal/adapter/source/fakethread`, `core/ingest/ingesttest` |
| Publishing an approved draft | `port.Effects` (`Publish`, `Verify`), optional `port.Validator`, `port.Locator`, `port.Deliverer` and `port.Placer` (where a draft proposed with no target posts) `port.Targeter` (whether it posts to a target, for a draft whose unit's plugin posts nothing) and `port.Lander` (the unit such a draft's post landed in) | each source's publisher | a fake publisher in `internal/core/outbox`'s tests |
| A sent draft's effects | `outbox.Applier` (`Apply`, `Pending`), each effect an `outbox.Effect`, a plugin's in `source.Module.OnPosted` | the composition root's effect table, `internal/cli/builtins_effects.go` | `core/outbox/outboxtest` |
| Drafts a workflow proposed | `automation.Withdrawals` (`Proposed`, `Withdraw`) | the Outbox's store | `core/automation/automationtest` |
| Workflow facts | `automation.Units`; optional `Refresher`, `Settler`, `Gainer`, `Owner`, `Freshener`, `Seener`, `Dueer` and `Scoper` (the facts of a unit as one workflow reads them, such as `<slot>.settled_at`) | `internal/adapter/facts/github`, `internal/adapter/facts/session`, `internal/adapter/facts/workspace`, `internal/adapter/facts/task` (the core-kept entry provider), `internal/adapter/facts/slack` (the Slack plugin's) | `core/automation/automationtest` with its contract suites |
| Git and the file system | `workspaces.Tree`, `workspaces.Remote`, `sweep.LocalGit`, `dossiers.Files` | `internal/adapter/gitcheck`, `internal/gitx`, `internal/adapter/dossier` | the `workspacestest` checkouts |
| Time and change | `port.Clock`, `port.Changes`, `port.Publisher` | the system clock, `internal/core/change` | fixed clocks in tests |

The daemon reaches the store and the GitHub reads through role ports of its own (`PullRequests`, `EventLog`, `Overrides`, `Metas`, `Records`, `Sweeper`), each at most five methods, wired in `internal/cli/serve.go`.

## Who reaches what

Each outside tool has one adapter that talks to it, and an architecture test keeps it that way:

- only `internal/adapter/store/sqlite` opens the database, and SQL text lives only under `internal/adapter/store`;
- only `internal/cli/storecmd.go` opens the store as its writer, for the daemon and the store commands; any other process reads it with `sqlite.OpenReadOnly` or checks its version with `sqlite.Check`;
- only `internal/github` reaches GitHub's API, and only it and the composition root import `internal/ghcli`, which runs `gh` for the token;
- only `internal/workspace/orca` runs `orca` or dials Orca's socket, and only the Claude Desktop manager names Desktop's control surface;
- the names of agents and managers (`claude`, `orca`, `CLAUDE_`, `mcp__` and the like) appear only in their adapters and in the composition root's registries, and only the adapters parse an agent's state;
- a package that spawns a process imports `internal/meter`, so every spawn is counted.

## Contracts

A port's fake and its adapter pass the same contract suite, kept in the core package's `<name>test` package. The fake runs it in every build; a real adapter runs it behind the `contract` build tag (`internal/adapter/orca`). Live tests against real tools run behind the `live` tag, only in a sandbox repository; see [Testing](/architecture/testing/).
