---
title: Testing
description: Fakes and contracts, goldens, budgets, the brand build and the architecture tests that keep the boundaries.
---

`make test` runs `go test -race ./...`; `make lint` runs gofmt and golangci-lint. Both pass before a pull request. CI splits the tests into shards that `scripts/test-shard.sh` names; a new package falls in the `rest` shard, and `scripts/test-shard.sh check` fails when the matrices of `ci.yml` and `cache.yml` drift from the script.

## Fakes and contracts

The core is tested without a network, a process or a real tool: the architecture tests keep `os/exec`, `net/http` and `database/sql` out of it. Each port's fake lives in the core package's `<name>test` package (`turnstest`, `gtdtest`, `messagestest`, `viewstest`, `workspacestest`, `automationtest`, `intaketest`, `ingesttest`) beside the contract suite the fake and the real adapter both pass. A real adapter runs its contract behind the `contract` build tag.

Test-only adapters prove that each seam holds a second implementation: the `echo` agent (`TestEchoAgentRunsWithoutClaudeOrOrca` runs install, a review, the hooks and `turn end` with no Claude or Orca token anywhere), the `testsink` view sink, the fake workspace manager and the `fakethread` provider.

## Time

Tests never read the wall clock. Code with timers, deadlines or goroutines runs in `synctest.Test` and asserts after `synctest.Wait`, on exact durations; real processes, sockets and files stay outside the bubble, behind a condition poll whose deadline only guards a hang. A test waits on the state it asserts, and a fake answers by state rather than by call count.

## Goldens

- The wire goldens in `internal/daemon/testdata/wire` (rows, actions, specs, detail, health) are read by the daemon's tests and by each client's, so server and clients agree on the bytes.
- The rendered turn instructions of every scenario in `internal/turn` and the terminal views in `internal/cli/testdata` are goldens.
- Goldens that spell the product name go through `internal/brand/brandtest`, so they hold under any brand.

## Budgets and the change log

`internal/meter/metertest` asserts what a unit of work cost (queries, spawns, GitHub calls), and `internal/adapter/store/storetest` seeds a store the size of a live one. `TestEveryWriteNotes` and `TestEveryExportedMethodIsReadOrNoted` keep every store write noted in the change log.

## The world outside

Tests never touch your home, your agent homes, your workspaces or GitHub: they run on temporary directories and fakes. Live tests (`internal/e2e`) run behind the `live` build tag, only against a sandbox repository and in isolated daemons. The repository is public, so tests, fixtures and pull request bodies carry invented names; `make scan-real-data SCAN_BASE=origin/main` checks a branch against your own deny list before you push.

## The brand

Every user-visible name comes from `internal/brand`. `make test-brand` runs the suite built with a test product name, and `make scan-brand` builds with it and fails when anything the program writes spells the default name.

## The architecture tests

`internal/archtest` holds the rules of [Layers](/architecture/layers/), [Ports](/architecture/ports/), [Plugins](/architecture/plugins/) and [Profile first](/architecture/profile-first/). Each rule has a self-test that plants a violation and expects it caught. An allowlist entry names its file and its reason, and an entry that no longer matches fails the test, so allowlists only shrink.

| Test | Rule |
|---|---|
| `TestArchitecture` | only the store imports the SQLite driver; clients import only the API, the domain and `termfmt`; the domain imports only the domain; the brief reaches agents and the store only through its ports; the API imports only the domain; only `intent/expr` imports cel-go; a core interface has at most five methods |
| `TestCoreImports` | the core imports the standard library, the core, the domain and the meter, and nothing that reaches processes, the network or the database |
| `TestDomainImports` | the domain imports no adapter, manager or `internal/github` |
| `TestDaemonImports` | the daemon reaches adapters, GitHub and Orca through ports |
| `TestReadModelImports` | the read model reaches its inputs through ports |
| `TestMeterIsALeaf` | `internal/meter` imports only the standard library |
| `TestProvidersStayInAdaptersAndTheRoot` | only a provider and the composition root import a provider's packages; provider names appear only in adapters and the root's allowlist; only adapters parse an agent's state |
| `TestOnlyOrcaAdapterReachesOrca`, `TestOnlyClaudeDesktopManagerReachesDesktop` | only each manager's package reaches its control surface |
| `TestOnlyGitHubReachesGitHub` | only `internal/github` reaches GitHub's API; `ghcli` only from it and the root |
| `TestOnlyTheStoreTouchesSQL` | only the sqlite package opens the database; SQL text only under `internal/adapter/store` |
| `TestOnlyServeWritesTheStore` | only `storecmd.go` opens the store as its writer, for the daemon and the store commands; other processes use `OpenReadOnly` |
| `TestSpawningPackagesImportMeter` | a package that spawns a process imports the meter |
| `TestBrandLiteralOnlyInBrand` | the default product name appears only in `internal/brand` and the exempt metadata |
| `TestCoreImportsNoPlugin` | the core imports no plugin and no plugin imports another; `pluginDebt` only shrinks |
| `TestOnlySourcesGoNamesASource` | only `cli/sources.go` (and `cli/slack*.go` for Slack) imports a source |
| `TestLoopCodeImportsNoPR` | loop code is keyed by `unit.Unit`; the pull request domain only in allowlisted files |
| `TestNoProviderKindsInAPI` | a provider's kinds live in its own package, never in the API |
| `TestTheEngineImportsNoProvider` | the automation engine imports no provider's domain |
| `TestTheCoreNamesNoProvider` | the core spells no provider id |
| `TestCockpitScriptsNameNoPlugin` | the cockpit's scripts quote no plugin's id |
| `TestNothingImportsTheGate` | the engine is the only planner of automatic rounds |
| `TestNoIntentEnumerations` | Go compares, switches on or lists no intent name outside allowlisted files |
| `TestProseIsEmbeddedOnlyFromProfiles` | prose files are embedded only from `profiles/`, but the allowlisted ones |
| `TestBoundaryImports` | the catalog, the intent vocabulary's leaves, the loader and `termfmt` import only their allowed sets |
| `TestOnlyTheCatalogBuildsActions` | only the catalog builds action specs |
| `TestOnlyTheCatalogEvaluatesIntentPredicates` | only the loader and the catalog read an intent's predicates |
| `TestBuiltinNamesLiveInTheirTables` | builtin names only in their table files |
| `TestContractSkeletonDeclared` | the shared names other packages build on still exist |

Outside `internal/archtest`, `TestEveryBuiltinNameHasAnImplementation` (`internal/cli`) checks that every name the vocabulary lists is wired, and `TestFactsDocIsCurrent` that the facts reference matches the binary (`make docs-ref` regenerates it).

A new rule comes with its self-test and a line in this table.
