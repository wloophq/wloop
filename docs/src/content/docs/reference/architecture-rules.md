---
title: Architecture rules
description: The architecture rules internal/archtest enforces, and the entries each allowlist lets pass.
---

Each rule is a test in `internal/archtest`, with a self-test that plants a violation and expects it caught. [Testing](/architecture/testing/#the-architecture-tests) says how the rules work.

| Test | Rule |
|---|---|
| `TestArchitecture` | Only the store imports the SQLite driver; clients import only the API, the domain and `termfmt`; the domain imports only the domain; the brief reaches agents and the store only through its ports; the API imports only the domain; only `intent/expr` imports cel-go; a core interface has at most five methods. |
| `TestBoundaryImports` | The catalog, the intent vocabulary's leaves, the loader and `termfmt` import only their allowed sets. |
| `TestBrandLiteralOnlyInBrand` | The default product name appears only in `internal/brand` and the exempt metadata. |
| `TestBuiltinNamesLiveInTheirTables` | Builtin names appear only in their table files. |
| `TestCockpitScriptsNameNoPlugin` | The cockpit's scripts quote no plugin's id. |
| `TestContractSkeletonDeclared` | The shared names other packages build on still exist. |
| `TestCoreImports` | The core imports only the standard library, the core, the domain and the meter, and nothing that reaches processes, the network or the database. |
| `TestCoreImportsNoPlugin` | The core imports no plugin, no plugin imports another, and `pluginDebt` only shrinks. |
| `TestDaemonImports` | The daemon reaches adapters, GitHub and Orca only through ports. |
| `TestDomainImports` | The domain imports no adapter, manager or `internal/github`. |
| `TestEveryRuleTestStatesItsRule` | Every rule test, a test that does not call flagged, states its rule in a doc comment. |
| `TestLoopCodeImportsNoPR` | Loop code is keyed by `unit.Unit` and imports the pull request domain only in allowlisted files. |
| `TestMeterIsALeaf` | `internal/meter` imports only the standard library. |
| `TestNoIntentEnumerations` | Go compares, switches on or lists no intent name outside allowlisted files. |
| `TestNoProviderKindsInAPI` | A provider's kinds live in its own package, never in the API. |
| `TestNothingImportsTheGate` | Nothing imports `internal/gate`: the workflow engine is the only planner of automatic rounds. |
| `TestOnlyClaudeDesktopManagerReachesDesktop` | Only the Claude Desktop manager's package reaches Claude Desktop's control surface. |
| `TestOnlyGitHubReachesGitHub` | Only `internal/github` reaches GitHub's API, and only it and the composition root import `ghcli`. |
| `TestOnlyOrcaAdapterReachesOrca` | Only the Orca manager's package reaches Orca's control surface. |
| `TestOnlyServeWritesTheStore` | Only `storecmd.go` opens the store as its writer, for the daemon and the store commands, and other processes use `OpenReadOnly`. |
| `TestOnlySourcesGoNamesASource` | Only `cli/sources.go`, and `cli/slack*.go` for Slack, imports a source. |
| `TestOnlyTheCatalogBuildsActions` | Only the catalog builds action specs. |
| `TestOnlyTheCatalogEvaluatesIntentPredicates` | Only the loader and the catalog read an intent's predicates. |
| `TestOnlyTheStoreTouchesSQL` | Only the sqlite package opens the database, and SQL text appears only under `internal/adapter/store`. |
| `TestProseIsEmbeddedOnlyFromProfiles` | Prose files are embedded only from `profiles/`, except the allowlisted ones. |
| `TestProvidersStayInAdaptersAndTheRoot` | Only a provider and the composition root import a provider's packages, provider names appear only in adapters and the root's allowlist, and only adapters parse an agent's state. |
| `TestReadModelImports` | The read model reaches its inputs only through ports. |
| `TestSpawningPackagesImportMeter` | A package that spawns a process imports the meter. |
| `TestTheCoreNamesNoProvider` | The core spells no provider id. |
| `TestTheEngineImportsNoProvider` | The automation engine imports no provider's domain. |

## Allowlists

The entries a rule lets pass, each with its reason. An entry that matches nothing fails its rule, so these lists only shrink. Status is the entry's triage against the architecture rules: ✅ the exception holds; 🔧 it does not, and the fix is mechanical; 🧾 it does not, and the fix is a refactor.

### `TestCoreImportsNoPlugin`

| `pluginDebt` entry | Status | Reason |
|---|---|---|
| `internal/api` `internal/domain/pr` | 🧾 | the reconcile report, the turn book, events, explore candidates and remove-done results carry a pr.Ref where the API should carry a unit.Unit |
| `internal/api/client` `internal/domain/pr` | 🧾 | TurnBook and Facts take a pr.Ref, following the API |
| `internal/client/tui` `internal/domain/pr` | 🧾 | the filter jumps to a pasted pull request URL with pr.ParseURL, and shortRef shortens an owner/repo#n label with pr.ParseKey; a client parses what the daemon should resolve |
| `internal/client/turncli` `internal/domain/pr` | 🧾 | unitArg parses a pull request URL argument with pr.ParseURL; a client parses what the daemon should resolve |
| `internal/core/port` `internal/domain/pr` | 🧾 | PROf reads a pull request from a github event |
| `internal/core/sweep` `internal/domain/inventory` | 🧾 | the Inputs port returns the stored pull request rows as inventory.PR |
| `internal/core/sweep` `internal/domain/pr` | 🧾 | the sweep's Facts embed pr.Facts |
| `internal/core/usage` `internal/domain/pr` | 🧾 | a Turn names its pull request as a pr.Ref |
| `internal/readmodel` `internal/domain/inventory` | 🧾 | the RowDeps.Intake hook takes an inventory.BriefRecord |

### `TestLoopCodeImportsNoPR`

| `prAllowed` entry | Status | Reason |
|---|---|---|
| `internal/daemon/briefs.go` | 🧾 | the brief queue is keyed by pull request, not by unit |
| `internal/daemon/explore.go` | 🧾 | explore lists GitHub search candidates, the GitHub plugin's job |
| `internal/daemon/probe.go` | ✅ | the e2e probe names a pull request |
| `internal/daemon/refs.go` | ✅ | the daemon's one edge to pull request references, for the GitHub-only roles keyed by one |
| `internal/daemon/timings.go` | 🧾 | review timings read pr.Facts, which the plugin's Facts should provide |
| `internal/daemon/timings_derive.go` | 🧾 | review timings read pr.Facts, which the plugin's Facts should provide |

### `TestNoIntentEnumerations`

| `intentAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/facts/github/facts.go` | ✅ | the GitHub round planners' due logic (pr.first.due, pr.review.action, pr.address.due) reads the newest review turn and its verdict |
| `internal/cli/rounds.go` | ✅ | the sweep sends review and address rounds |
| `internal/cli/serve.go` | ✅ | the sweep's review and address questions and follow-ups |
| `internal/cli/stepstart.go` | ✅ | turn.start sends the sweep's review and address rounds and guards a nudge as its prepare does |
| `internal/cli/try.go` | ✅ | profile try is a review |
| `internal/core/intake/intaketest/scenarios.go` | ✅ | test fixtures of today's intents |
| `internal/core/turns/turnstest/intents.go` | ✅ | the test registry of today's intents |
| `internal/core/turns/turnstest/store.go` | ✅ | test fixtures of today's intents |
| `internal/daemon/probe.go` | ✅ | the probe launches the probe intent |
| `internal/situation/situation.go` | ✅ | a pull request's situation reads whether a review or address turn is in flight or addressed it, the GitHub rounds' due logic |
| `internal/turn/scenarios.go` | ✅ | eval fixtures |

### `TestProseIsEmbeddedOnlyFromProfiles`

| `embedKept` entry | Status | Reason |
|---|---|---|
| `internal/adapter/source/slack/eval` | ✅ | the extraction eval's own prompts, a dev tool |
| `skills` | ✅ | the skills install writes into agent homes |

| `embedMoving` entry | Status | Reason |
|---|---|---|
| `internal/adapter/source/slack/extract/prompt.md` | ✅ | the Slack extraction prompt |
| `internal/brief/prompt.md` | ✅ | the brief prompt |
| `internal/brief/taxonomy.md` | ✅ | the brief taxonomy |
| `internal/turn/templates` | ✅ | turn partials |

### `TestProvidersStayInAdaptersAndTheRoot`

| `allowlist` entry | Status | Reason |
|---|---|---|
| `internal/cli/root.go` `Claude Desktop` | ✅ | the Claude Desktop manager entry's display name |
| `internal/cli/root.go` `Orca` | ✅ | the Orca manager entry's display name |
| `internal/cli/root.go` `claude` | ✅ | the registry key of the Claude agent entry |
| `internal/cli/root.go` `orca` | ✅ | the registry key and the sink name of the Orca manager entry |
| `internal/intent/builtin/facts.go` `The dormant session's manager keeps its own record of it, and the user closed it there, such as archiving it in Claude Desktop.` | ✅ | session.closed_by_manager's doc names the manager that keeps such a record |
| `internal/intent/builtin/facts.go` `The workspace manager that owns the session, such as orca or claude-desktop.` | ✅ | session.manager's doc names the managers' keys |

### `TestSpawningPackagesImportMeter`

| `spawnExempt` entry | Status | Reason |
|---|---|---|
| `internal/adapter/store/storetest` | ✅ | test support that reads the inventory with sqlite3 for the e2e harness |
| `internal/cli` | ✅ | one-shot user commands: bundle, install, launchctl |
| `internal/client/tui` | ✅ | the user's editor, run in the foreground on a prompt's text |
| `internal/e2e` | ✅ | the end-to-end harness |

### `TestTheCoreNamesNoProvider`

| `coreProviderAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/core/port/port.go` `unit.GitHub` | 🧾 | PROf reads a pull request from a github event; a pluginDebt edge |

This page is generated from the sources of `internal/archtest` by `make docs-ref`.
