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
| `TestClientsNameNoPlugin` | No client names a plugin: the terminal, the menu bar and the other Go clients spell no plugin's id or provider constant, and the cockpit's scripts and page quote no plugin's id. |
| `TestCockpitScriptsNameNoPlugin` | The cockpit's scripts quote no plugin's id. |
| `TestContractSkeletonDeclared` | The shared names other packages build on still exist. |
| `TestCoreImports` | The core imports only the standard library, the core, the domain and the meter, and nothing that reaches processes, the network or the database. |
| `TestCoreImportsNoPlugin` | The core imports no plugin, no plugin imports another, and `pluginDebt` only shrinks. |
| `TestDaemonImports` | The daemon reaches adapters, GitHub and Orca only through ports. |
| `TestDomainImports` | The domain imports no adapter, manager or `internal/github`. |
| `TestDraftTextComesFromTheProfile` | No Go code outside its allowlist writes a draft's text from prose of its own: the Text of an outbox.Proposal or a draft.Draft is never a string constant, a format, or what a function of the same package builds from string literals. A draft's text comes from a profile template or from whoever wrote it. |
| `TestEveryRuleTestStatesItsRule` | Every rule test, a test that does not call flagged, states its rule in a doc comment. |
| `TestLoopCodeImportsNoPR` | Loop code is keyed by `unit.Unit` and imports the pull request domain only in allowlisted files. |
| `TestMeterIsALeaf` | `internal/meter` imports only the standard library. |
| `TestNoDraftKindComparisons` | Go names no draft kind, compares no draft's purpose with a value nor keys a table by it, and compares its delivery only in the publishers and the Outbox: a draft is what its proposer declares. |
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
| `TestOnlyTheDraftStepProposes` | Only the draft.propose step, and the plugin proposers its allowlist names, build a draft.Origin of kind auto: every other draft comes from a person, a turn or an agent. |
| `TestOnlyTheStoreTouchesSQL` | Only the sqlite package opens the database, and SQL text appears only under `internal/adapter/store`. |
| `TestProseIsEmbeddedOnlyFromProfiles` | Prose files are embedded only from `profiles/`, except the allowlisted ones. |
| `TestProvidersStayInAdaptersAndTheRoot` | Only a provider and the composition root import a provider's packages, provider names appear only in adapters and the root's allowlist, and only adapters parse an agent's state. |
| `TestReadModelImports` | The read model reaches its inputs only through ports. |
| `TestSpawningPackagesImportMeter` | A package that spawns a process imports the meter. |
| `TestTheCoreNamesNoProvider` | The core spells no provider id, and that outside the core a provider id appears only in its plugin's packages, the vocabulary tables, the unit package and the composition root. |
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

### `TestDraftTextComesFromTheProfile`

| `draftTextAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/source/github/proposals.go` | ✅ | GitHub's nudge text, github-nudge |

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
| `internal/core/intake/intaketest/scenarios.go` | ✅ | test fixtures of today's intents |
| `internal/core/turns/turnstest/intents.go` | ✅ | the test registry of today's intents |
| `internal/core/turns/turnstest/store.go` | ✅ | test fixtures of today's intents |
| `internal/turn/scenarios.go` | ✅ | eval fixtures |

### `TestOnlyTheDraftStepProposes`

| `pluginProposers` entry | Status | Reason |
|---|---|---|
| `internal/adapter/source/github/proposals.go` | ✅ | GitHub's nudge rule, github-nudge |
| `internal/core/proposals/proposals.go` | ✅ | the job that runs the plugins' own rules |

### `TestProseIsEmbeddedOnlyFromProfiles`

| `embedKept` entry | Status | Reason |
|---|---|---|
| `internal/adapter/source/slack/eval` | ✅ | the extraction eval's own prompts, a dev tool |
| `skills` | ✅ | the skills install writes into agent homes |

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
| `internal/adapter/store/inventory/activity.go` `unit.GitHub` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/briefruns.go` `"github"` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `unit.GitHub` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `unit.Session` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/inventory/timings.go` `unit.GitHub` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/sqlite/marks.go` `unit.GitHub` | 🧾 | a round mark notes GitHub's rounds topic |
| `internal/adapter/store/sqlite/registry.go` `unit.Session` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/sqlite/retain.go` `unit.Session` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/sqlite/retain.go` `unit.Workspace` | ✅ | the store keeps the workspaces the core keeps |
| `internal/adapter/store/sqlite/slack.go` `"slack"` | ✅ | the store keeps the Slack plugin's tables |
| `internal/adapter/store/sqlite/turns.go` `unit.GitHub` | 🧾 | a pull request's turns are keyed by owner, repository and number |
| `internal/cli/actions.go` `unit.GitHub` | 🧾 | the GitHub viewer and actions read outside the plugin |
| `internal/cli/cli.go` `"slack"` | ✅ | the slack command, the Slack plugin's own commands |
| `internal/cli/facts.go` `unit.GitHub` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/cli/help.go` `"slack"` | ✅ | the slack command, the Slack plugin's own commands |
| `internal/cli/init_config.go` `"slack"` | ✅ | the slack section of the config init writes |
| `internal/cli/intent_cmd.go` `unit.GitHub` | 🧾 | the usage line names a pull request's target |
| `internal/cli/placement.go` `unit.GitHub` | 🧾 | GitHub's default placement, wired outside the plugin's PlacementDefaults |
| `internal/cli/serve.go` `unit.GitHub` | 🧾 | the daemon's wiring of the GitHub client, viewer and forge |
| `internal/cli/serve_payloads.go` `unit.GitHub` | 🧾 | GitHub's payload reader, wired outside the plugin's Payload |
| `internal/cli/serve_plugins.go` `unit.GitHub` | 🧾 | the GitHub facilities the daemon holds go with the GitHub plugin |
| `internal/cli/storedfacts.go` `unit.GitHub` | 🧾 | the stored facts of a pull request's turn |
| `internal/cli/sweepinputs.go` `unit.GitHub` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/cli/targets.go` `unit.GitHub` | 🧾 | GitHub's provider listed before the plugins |
| `internal/cli/turn.go` `unit.GitHub` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/core/turns/headless.go` `"entry"` | ✅ | the headless turn's entry input, which shares the entry provider's spelling |
| `internal/meter/meter.go` `"github"` | ✅ | the meter's resource of a GitHub API request, not a unit provider |
| `skills/skills.go` `unit.GitHub` | 🧾 | a turn skill takes a pull request's URL |

This page is generated from the sources of `internal/archtest` by `make docs-ref`.
