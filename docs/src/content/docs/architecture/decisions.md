---
title: Decision log
description: The architecture decisions in force, each with its context, its consequence and the test that keeps it.
---

One entry per decision in force. A pull request that changes one of them changes its entry.

## One daemon writes the store

- **Context**: several processes writing one SQLite file need two lock disciplines and still race, and a lock held across a network call stalls every other writer.
- **Decision**: only the daemon opens the store as its writer, and `store migrate` and `store restore` under the store's lock with the daemon stopped. Other processes go through the daemon's API, or read the store with `sqlite.OpenReadOnly` when the daemon is down.
- **Consequence**: one transaction is the unit of consistency; clients fail when the daemon is down instead of writing around it.
- **Enforced by**: `TestOnlyServeWritesTheStore`, `TestOnlyTheStoreTouchesSQL`. See [The daemon and its clients](/architecture/daemon-and-clients/).

## Clients are thin and speak one versioned API

- **Context**: a rule computed in a client drifts from the same rule in another.
- **Decision**: the daemon computes rows, actions, facets, counters and notifications; clients parse, call one operation and render. `internal/api` is the only schema, versioned by its major.
- **Consequence**: the CLI, the terminal client, the menu bar and the cockpit show the same thing; a new surface is rendering only.
- **Enforced by**: `TestArchitecture`, `TestCockpitScriptsNameNoPlugin`; the wire goldens.

## Ports and adapters, dependencies inward

- **Context**: use cases that shell out or speak HTTP cannot be tested without the tools, and every new tool edits them.
- **Decision**: the domain imports only itself; the core imports the domain, its ports and the meter; adapters implement the ports; one composition root picks them.
- **Consequence**: the core runs in tests with no network and no process; a new agent, manager or sink is an adapter and a registry entry.
- **Enforced by**: `TestArchitecture`, `TestCoreImports`, `TestDomainImports`, `TestDaemonImports`, `TestReadModelImports`. See [Layers](/architecture/layers/).

## Small ports owned by their consumer

- **Context**: a wide interface every consumer mocks whole couples them all to every capability.
- **Decision**: a port is declared by the core package that uses it, with at most five methods; a capability only some adapters have is an optional interface found by type assertion.
- **Consequence**: a manager without a board, or an agent without questions, plugs in without stubs.
- **Enforced by**: `TestArchitecture`. See [Ports and adapters](/architecture/ports/).

## One adapter reaches each outside tool

- **Context**: tool names and protocols scattered through the code make a second agent or manager a rewrite.
- **Decision**: only `internal/github` reaches GitHub, only the Orca and Claude Desktop managers reach their apps, only the store touches SQL, and provider names live in adapters and the root. GitHub is read in process with budgets; Orca through its socket.
- **Consequence**: a change of tool or protocol stays in one package.
- **Enforced by**: `TestProvidersStayInAdaptersAndTheRoot`, `TestOnlyOrcaAdapterReachesOrca`, `TestOnlyClaudeDesktopManagerReachesDesktop`, `TestOnlyGitHubReachesGitHub`, `TestOnlyTheStoreTouchesSQL`.

## Units generalize the pull request

- **Context**: a loop keyed by pull request cannot hold a Slack thread, a session or a workspace.
- **Decision**: work is keyed by `unit.Unit{Provider, Key}`; only the provider's codec mints and parses a key.
- **Consequence**: the turns, the workflows, the GTD core and the read model serve any provider.
- **Enforced by**: `TestLoopCodeImportsNoPR`, `TestTheCoreNamesNoProvider`.

## Sources are compiled-in plugins

- **Context**: the core must not change per source, and a plugin framework costs more than the two sources need.
- **Decision**: a source is a `source.Plugin` listed in `internal/cli/sources.go`, contributing through optional `source.Module` fields; the core imports no plugin and no plugin imports another.
- **Consequence**: a disabled plugin leaves the daemon whole; the remaining edges from the core to the GitHub plugin are listed as debt.
- **Enforced by**: `TestCoreImportsNoPlugin`, `TestOnlySourcesGoNamesASource`. See [Plugins](/architecture/plugins/).

## The store is the truth

- **Context**: workspaces and sessions are deleted and recycled; a board can be moved by hand.
- **Decision**: turn state, questions, answers and history live in the store. Workspace managers and boards are sync targets: they receive a projection, fail alone and are reconciled on start; {{name}} never reads a card's column back as state.
- **Consequence**: any workspace or session can be recreated from stored context.
- **Enforced by**: the views' contract suites.

## Every outbound post is a draft

- **Context**: a post under your name cannot be taken back, and a retry after a crash can post twice.
- **Decision**: what {{name}} would post is a draft in the Outbox; only an approved draft reaches the plugin's publisher, once, with no automatic retry, and `Verify` settles a draft a crash left in `sending`.
- **Consequence**: approval policies decide what goes out unattended; agents never hold a source's token.
- **Enforced by**: `internal/core/outbox` tests.

## Authority comes from you

- **Context**: an agent reads text written by others and can be steered by it.
- **Decision**: agents report facts; endorsing a verdict, approving, taking over and removing a workspace come only from your surfaces. A session is identified by a header its manager fills, never by a tool argument.
- **Consequence**: an agent cannot endorse its own work through its channel.
- **Enforced by**: the daemon's and MCP's tests. See [Security](/concepts/security/).

## Behavior lives in the profile

- **Context**: behavior and text in Go cannot be changed by the person who uses {{name}}, in their language or their way of working.
- **Decision**: intents, workflows and judgment blocks are profile files, and a user profile overrides any bundled one; Go provides a closed vocabulary of facts, steps, builtins and gates, and never enumerates intents.
- **Consequence**: a new behavior is a profile change; Go grows only by a fact, a step or a gate.
- **Enforced by**: `TestNoIntentEnumerations`, `TestProseIsEmbeddedOnlyFromProfiles`, the boundary rules, `TestBuiltinNamesLiveInTheirTables`. See [Profile first](/architecture/profile-first/).

## The core owns what a trigger handled

- **Context**: each trigger that must fire once per signal (a review, a head's failing checks, a head's conflict) needed its own column and migration, and a source other than GitHub or a profile's own trigger had nowhere to keep it.
- **Decision**: what a trigger handled is a mark: a value per unit and name in `unit_marks`, keyed by `unit.Unit`, written in the transaction of the write that handled it, and deleted with an ended pull request, a pull request gone from the inventory, or a session no longer stored. The core owns the encoding and the comparison (`internal/domain/mark`: a string is a version, a time or an int a watermark); a plugin owns its signal's meaning, exposes it as a fact and names its own marks; a workflow records its marks with `[marks]`, under its name, and reads any with `handled()`.
- **Consequence**: a new trigger needs no schema change, and a profile can define one that fires once per signal; a signal that does not fit a version or a watermark stays in its plugin's planner.
- **Enforced by**: the `internal/domain/mark` tests, the ledger contract (`ADoneOrDismissedRunSetsTheItemsMarks`), the manifest goldens in `internal/intent/testdata/workflows`.

## One engine plans automatic work, and planning writes nothing

- **Context**: two automation mechanisms side by side disagree, and a plan that writes while it is read changes under the reader.
- **Decision**: workflows run by `internal/core/automation` are the only planner of automatic rounds and cleanup; evaluating the plan writes nothing, spawns nothing and reads no GitHub; irreversible steps ship in `observe` and automatic runs never force.
- **Consequence**: `{{command}} plan` is the dry run.
- **Enforced by**: `TestNothingImportsTheGate`, `TestTheEngineImportsNoProvider`, the engine's tests.

## The store migrates forward

- **Context**: a user's store holds work that cannot be recreated.
- **Decision**: every schema change is a new version with a migration, run after a backup, and a store newer than the binary is refused.
- **Consequence**: `{{command}} store restore` takes a store back to a backup; a migration in a release is never edited.
- **Enforced by**: the migration tests in `internal/adapter/store/sqlite`, which fail on a schema change without a migration.

## The read model lives in memory

- **Context**: deriving rows per request costs a query per row on every refresh.
- **Decision**: writes note an in-memory change log; the read model rebuilds rows from one store snapshot on change and serves deltas that clients pull after an event.
- **Consequence**: serving rows costs no SQL; a restart starts a new epoch and clients reload.
- **Enforced by**: `TestEveryWriteNotes`, `TestEveryExportedMethodIsReadOrNoted`, the projection tests.

## Every spawn is metered

- **Context**: spawning `gh` or `orca` on a hot path made the daemon slow and the cost invisible.
- **Decision**: a package that spawns a process imports `internal/meter`, which counts each boundary call by port; GitHub and Orca are reached in process or over a socket.
- **Consequence**: `{{command}} doctor --load` shows what each job and route costs; budget tests assert it.
- **Enforced by**: `TestSpawningPackagesImportMeter`, `TestMeterIsALeaf`.

## One brand package

- **Context**: the product's name must change in one place.
- **Decision**: every user-visible name derives from `internal/brand`; internal names are brand-neutral (`X-Loop-*`, `LOOP_*`); the docs write the name through placeholders.
- **Consequence**: a build with another name writes nothing that names the default one.
- **Enforced by**: `TestBrandLiteralOnlyInBrand`, `make test-brand`, `make scan-brand`.

## Architecture rules are tests

- **Context**: a rule only in prose is broken by the next change that does not read it.
- **Decision**: each rule is a test in `internal/archtest` with a self-test that plants a violation; allowlists name their reason and fail when stale.
- **Consequence**: exceptions are visible and only shrink.
- **Enforced by**: the self-tests. See [Testing](/architecture/testing/#the-architecture-tests).
