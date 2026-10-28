---
title: Plugins
description: How a source of units plugs into the core, what it contributes, and the boundary that keeps the core free of it.
---

The core of {{name}} is a task system over opaque units: tasks, the Inbox, the Outbox, projects, the review, the turns, the workflows and the read model work on `unit.Unit{Provider, Key}` and never on a pull request or a Slack thread. Where units come from is a plugin's business. The plugins are compiled in, `github` and `slack`, with no plugin framework and no plugins loaded at run time.

## A plugin

A plugin is a `source.Plugin{ID, Name, Codec, Build}` listed in `internal/cli/sources.go`, the one file that names a source. Its codec mints and parses the keys of its units and stays registered when the plugin is disabled, so stored units still render. `Build` returns the plugin's `source.Module` from the configuration and the shared handles (`source.Deps`).

A module must have its codec, an ingestion job with an interval and its rows; a module that derives items must have a card. A module that fails to build, or whose keys clash with the core's, is left out with its error reported, and the daemon runs without it. Everything else is optional, and the core does without what a plugin leaves out:

| A plugin contributes | Through `Module` | What the core does with it |
|---|---|---|
| Units and their changes | `Ingest` (an `ingest.Job` over its `port.Source` and `port.SourceStore`), `Slow`, `Run` (a long-running stream, restarted after 30 s), `Jobs` | runs them as daemon jobs; a source never writes, ingest saves through the source's store |
| Rows | `Rows`, a read model provider | serves them to every surface with the rest of the read model |
| Items and cards | `Items` (deterministic, no I/O), `Card`, `Identity`, `UnitEvents` | turns items into tasks; renders the card on every surface; resolves linked items from unit events |
| Workflow facts | `Facts`, an `automation.Units` | the engine evaluates workflows over the plugin's units; a fact that names a signal, such as `pr.head`, is what a workflow's mark records; `<slot>.ended` (`builtin.EndedFact`), such as `pr.ended` or `slack.ended`, is the plugin's word that a unit has ended, which the tasks it is attached to read as `task.link_ended` |
| Marks | its planner's own mark names, written through the store | stores them per unit, retains them with the unit, and lets `handled()` read them |
| What a turn reads about a unit | `Payload`, a `turn.PayloadReader` | renders it into the turn's instructions |
| Where its work runs | `PlacementDefaults`, `PlacementHints` | the placement service decides the repository and checkout |
| Publishing | `Publisher`, a `port.Effects` | sends an approved draft once, with no automatic retry; `Verify` settles a draft a crash left in `sending` |
| Following units | `Track`, `Untrack` | `{{command}} track` and untrack go through it |
| UI | `UI` (facets, saved views, notification templates, keys, menu bar sections, terminal views), `WebAssets` served under `/plugins/<id>/`, `CLI` | renders the declarative parts on every surface; mounts the plugin's own web module and terminal views |
| Health | `Health` | shows it with the daemon's health |
| Its checkouts | `Checkouts` | a session on its unit runs in the unit's own checkout, with no placer asked |
| Its briefs | `Briefs`, a `brief.BriefSource` | the brief queue reads the unit's input, revision and staleness through it; an `IntentUser` gets its provider's brief intent |
| Links to other plugins' units | `Links`, a `LinkResolver` | resolves a link of its units to the unit another plugin names |
| A detail page | `Detail` | a client opens the unit's detail in the cockpit, in place of its link |
| Addresses a person types | `Resolve` | the daemon answers `GET /api/resolve` with the unit an address lands in, such as a Slack message ref or permalink in its direct message segment or thread; a command line's target asks it |

`Module.Capabilities()` names what a plugin can do (`checkouts`, `briefs`, `publishes`, `reacts`, `items`, `detail`), served in `api.Plugin.Capabilities`. The daemon and the clients ask a plugin what it can do, never which one it is (`api.PluginCan`). A unit's codec says the rest: `Equal` compares two keys of its provider, such as GitHub's without case, for units and person keys alike (`unit.Registry.Same`, `items.Identity.Codecs`); `URL` and `KeyOf` name a unit by its web address, so a unit with one is typed by its address (`unit.Registry.ParseURL`); `Target` is how a command line names one of its units, such as `<url>`, in an intent's usage line (`unit.Registry.Target`).

The UI comes in three tiers: the card, which every surface renders the same way and which is kept as a stored snapshot; the declarative contributions the core renders; and the plugin's own views. Keys follow one map: the core owns every lowercase letter and digit, and a plugin or an intent gets one uppercase letter (`internal/keys`).

## A disabled plugin

`<id>.enabled = false` (`github.enabled`, `slack.enabled`) leaves the module unbuilt: no ingestion, rows, jobs, facts, items, actions, notifications, MCP tools or publisher. Its codec stays, cards render from the stored snapshot, and every write to one of its units answers `409 {"error": "<id> is disabled"}` before the handler runs. On a task, Dismiss still works: it detaches the disabled plugin's units and their items, and an item's link to such a unit is stored and left out while the plugin is off ([Tasks and views](/concepts/tasks/)). The core works with no plugin enabled.

## The boundary

The core never imports a plugin package, and no plugin imports another. The core here is `internal/core`, `internal/domain`, `internal/readmodel`, `internal/api`, `internal/web` and `internal/client`; each plugin owns its packages:

| Plugin | Packages |
|---|---|
| `github` | `internal/adapter/source/github`, `internal/adapter/facts/github`, `internal/readmodel/prrow`, `internal/situation`, `internal/domain/github`, `internal/domain/pr`, `internal/domain/inventory`, `internal/domain/change`, `internal/github` |
| `slack` | `internal/adapter/source/slack`, `internal/domain/slack`, `internal/slack` |

Test imports count too, except a plugin's own test fake: a core test builds its units as `unit.Unit` values, and a test that exercises a plugin's behavior lives with the plugin, such as the read model's tests over GitHub rows in `internal/readmodel/prrow`. No client names a plugin either: the cockpit's scripts and page quote no plugin's id, and the terminal and the menu bar spell no plugin's id or provider constant (`TestClientsNameNoPlugin`); they read the provider and its capabilities from the data the daemon serves. Outside the core, a provider id appears only in its plugin's packages, the vocabulary tables of `internal/intent/builtin`, `internal/domain/unit`, the codecs of the providers the core keeps, `internal/cli/root.go`, `sources.go` and `slack*.go`, and an adapter spells only its own provider's id. The places that still name one are listed in `coreProviderAllowlist` (`TestTheCoreNamesNoProvider`) by file, declaration and literal, each with how many times it spells it and its reason, so a new spelling, even in a listed declaration, fails the rule; the list only shrinks. Code that tells a pull request from another unit by asking the GitHub plugin's package (`pr.RefOf`, `pr.Codec{}.Provider()`, `port.PROf`, the daemon's `isPullRequest`) is listed the same way in `pluginIdentityAllowlist` (`TestProviderIdentityThroughAPluginPackageOnlyShrinks`). The edges that break the rule today are listed in `pluginDebt` in `internal/archtest/rules_plugins_test.go`, mostly core packages that still name `domain/pr`. The list only shrinks: an entry that no longer matches an import fails the test. Loop code outside the GitHub plugin is keyed by `unit.Unit`, and the daemon files that still name a pull request are listed with their reason (`TestLoopCodeImportsNoPR`).

`session`, `workspace` and `entry` are providers the core keeps for its own units. They have no plugin: their facts come from the turn registry and the workspace managers, through `internal/adapter/facts/session` and `internal/adapter/facts/workspace`.

## The plugin protocol

`pkg/plugin` is the SDK of plugins that run as their own processes, through `hashicorp/go-plugin` over gRPC; the daemon does not run any yet. It holds what every plugin shares, with its protocol under `proto/`:

- the handshake and the protocol versions (`VersionedPlugins`, protocol 1), negotiated when the host starts a plugin;
- the lifecycle every kind answers: `Health`, `Start`, `Stop`, and `Notify(units, topics)`, which tells a kind that host state it reads changed;
- `Serve`, which serves one implementation per kind from one binary, the kind being go-plugin's plugin name;
- the logger, which writes JSON lines to stderr for the host to relay; a plugin writes nothing to stdout, which carries the handshake (`TestPluginWritesNothingToStdout`);
- the exit when the parent dies: a plugin polls its parent process id and exits when it changes, with no reattach.

A plugin's directory holds a `plugin.toml` (`pkg/plugin/manifest`) with its `id`, the `kinds` its binary serves, the `protocol` it speaks, and one table per kind, which the kind's SDK package decodes. `pkg/plugin/plugintest` serves kinds in-process, for tests on either side. Nothing under `pkg/plugin` imports `internal/`, and the host passes its product name through the environment (`plugin.BrandEnv`).

## Adding a source

1. A package under `internal/adapter/source/<id>` with its codec, its `port.Source`, its store and its rows; its domain values under `internal/domain/<id>`; a client for the service if it needs one.
2. One entry in `plugins` in `internal/cli/sources.go`.
3. Its packages under its id in `pluginRoots` in `internal/archtest/rules_plugins_test.go`.
4. Its tables in `internal/adapter/store/sqlite/schema.sql`.
5. A configuration section and its documentation.

`internal/adapter/source/fakethread`, a provider of chat threads nothing registers, shows the minimum.
