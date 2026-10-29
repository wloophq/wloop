---
title: Architecture rules
description: The architecture rules internal/archtest enforces, and the entries each allowlist lets pass.
---

Each rule is a test in `internal/archtest`, with a self-test that plants a violation and expects it caught. [Testing](/architecture/testing/#the-architecture-tests) says how the rules work.

| Test | Rule |
|---|---|
| `TestApprovalRulesReadTrustedFacts` | No [gates.approval] rule that releases a gate (a draft gate's rule of any mode, any other gate's of mode send, run or delay) loads when it reads an untrusted fact, in each object that holds one: an intent's and a workflow's manifest, for its plan gates and its draft gates, and the config's workflows.<name>.gates.approval. The untrusted facts are the table's, the brief's extra fields, a classification's output and a task property an agent or a plugin writes, so an agent or a thread's text never releases a send or a run. |
| `TestArchitecture` | Only the store imports the SQLite driver; clients import only the API, the domain and `termfmt`; the domain imports only the domain; the brief reaches agents and the store only through its ports; the API imports only the domain; only `intent/expr` imports cel-go; a core interface has at most five methods. |
| `TestBoundaryImports` | The catalog, the intent vocabulary's leaves, the loader and `termfmt` import only their allowed sets. |
| `TestBrandLiteralOnlyInBrand` | The default product name appears only in `internal/brand` and the exempt metadata. |
| `TestBuiltinNamesLiveInTheirTables` | Builtin names appear only in their table files. A released migration keeps the words that version stored, so migrate_v*.go files are history and exempt. |
| `TestCLIImportsNoGitHubClient` | No file under `internal/cli` imports the GitHub plugin's API client or gh: the CLI reaches GitHub through its plugin's calls. The test files in `cliGitHubDebt` pass, and the list only shrinks. |
| `TestClientsNameNoPlugin` | No client names a plugin: the terminal, the menu bar and the other Go clients spell no plugin's id or provider constant, and the cockpit's scripts and page quote no plugin's id. |
| `TestCockpitScriptsNameNoPlugin` | The cockpit's scripts quote no plugin's id. |
| `TestContractSkeletonDeclared` | The shared names other packages build on still exist. |
| `TestCoreImports` | The core imports only the standard library, the core, the domain and the meter, and nothing that reaches processes, the network or the database. |
| `TestCoreImportsNoPluginDomain` | The core imports no plugin, no plugin imports another, and `pluginDebt` only shrinks. |
| `TestDaemonImports` | The daemon reaches adapters, GitHub and Orca only through ports. |
| `TestDaemonImportsNoPluginDomain` | The daemon's own code imports no plugin package, under no debt: it reaches source logic through the plugins' Action and View only. |
| `TestDomainImports` | The domain imports no adapter, manager or `internal/github`. |
| `TestDraftTextComesFromTheProfile` | No Go code outside its allowlist writes a draft's text from prose of its own: the Text of an outbox.Proposal, a draft.Draft or a plugin's source.Draft is never a string constant, a format, or what a function of the same package builds from string literals. A draft's text comes from a profile template or from whoever wrote it. |
| `TestEveryDecisionNamesATestThatExists` | Every entry of the decision log names on its Enforced by line at least one test, and only tests the repository declares. |
| `TestEveryRuleTestStatesItsRule` | Every rule test, a test that does not call flagged, states its rule in a doc comment. |
| `TestGitHubPluginImportsOnlyTheSDK` | No package under `plugins/github`, tests included, imports a package under `internal/` or a package of another plugin: the GitHub plugin builds against the SDK and its own packages alone. |
| `TestGoNamesNoBundledProperty` | Go names no property the bundled tasks.toml gives a role (area, contexts, star, deadline): outside internal/intent, no Go call of Prop, SetProp, PropText, PropList or PropTime passes such a name as a constant. Go names the role and reads the property the profile gives it. |
| `TestGoNamesNoState` | Go reads a task's category and never names a state: no Go literal is converted to, assigned to or compared with a task.State or a task.Category outside internal/intent and the version 7 migration, but the Category constants and Categories of internal/domain/task/schema.go, which declare the closed set; and no SQL text under internal/adapter/store compares the state column with a state the bundled profile declares, nor inlines a category, which it binds as a parameter. |
| `TestGoNamesNoView` | Go never names a view: no Go constant, a literal or a conversion of one, is typed task.View outside internal/intent. The views and their ids are the profile's views.toml. |
| `TestLoopCodeImportsNoPR` | Loop code is keyed by `unit.Unit` and imports the pull request domain only in allowlisted files. |
| `TestManifestKeysAreInTheGrammar` | Every toml key the module decodes is a word of builtin.Grammar and that only internal/intent, internal/profile, internal/frontmatter and internal/config decode a profile file's or the configuration's TOML, so no key is read outside the table the loaders check, the schemas and the reference pages derive from. The configuration's keys are checked against the table by internal/config's TestEveryConfigKeyIsInTheGrammar. The plugin SDK under `pkg/plugin`, which reads a plugin's `plugin.toml`, is outside the rule. |
| `TestMeterIsALeaf` | `internal/meter` imports only the standard library. |
| `TestNoDraftKindComparisons` | Go names no draft kind, compares no draft's purpose with a value nor keys a table by it, and compares its delivery only in the publishers and the Outbox: a draft is what its proposer declares. |
| `TestNoIntentEnumerations` | Go compares, switches on or lists no intent name outside allowlisted files. |
| `TestNoProviderKindsInAPI` | A provider's kinds live in its own package, never in the API. |
| `TestNoQueryJoinsPluginAndCoreTables` | No query of `internal/adapter/store`, and no transaction (a function literal passed to `Write`, or a function taking a `*Tx`, with the functions it hands the transaction to), names both a table a source plugin owns and a core table: the plugin tables leave the core store, so a query that joins them with the core's becomes a snapshot read or a broker call, and a write of both becomes two. The migrations, history of the shared file, are exempt. |
| `TestNothingImportsTheGate` | Nothing imports `internal/gate`, the old round gate: the workflow engine is the only planner of automatic rounds. |
| `TestOnlyClaudeDesktopManagerReachesDesktop` | Only the Claude Desktop manager's package reaches Claude Desktop's control surface. |
| `TestOnlyGitHubReachesGitHub` | Nothing in the host names GitHub's API: the GitHub plugin reaches it, with its own credentials. |
| `TestOnlyOrcaAdapterReachesOrca` | Only the Orca manager's package reaches Orca's control surface. |
| `TestOnlyServeWritesTheStore` | Only `storecmd.go` opens the store as its writer, for the daemon and the store commands, and other processes use `OpenReadOnly`. |
| `TestOnlySourcesGoNamesASource` | Only `cli/sources.go`, and for one source the files that wire it (`cli/slack*.go` and `cli/serve_slack.go` for Slack, `cli/serve_github.go` for GitHub), imports a source. |
| `TestOnlyTheCatalogBuildsActions` | Only the catalog builds action specs. |
| `TestOnlyTheCatalogEvaluatesIntentPredicates` | Only the loader and the catalog read an intent's predicates. |
| `TestOnlyTheDraftStepProposes` | Only the draft.propose step, and the plugin proposers its allowlist names, build a draft.Origin of kind auto: every other draft comes from a person, a turn or an agent. |
| `TestOnlyTheGatesServiceResolvesAGate` | Outside the gates service, the sqlite gates store and the version 7 migration no code calls Resolve on a gates.Store or writes gates.status in SQL: every decision is closed by the service, which accepts only a person, a trusted policy rule or a passed window. |
| `TestOnlyTheStoreTouchesSQL` | Only the sqlite package opens the database, and SQL text appears only under `internal/adapter/store`. |
| `TestPluginWritesNothingToStdout` | No file of `pkg/plugin` or `plugins/` writes to stdout: go-plugin reads a plugin's handshake from it, so a plugin logs to stderr only. |
| `TestProseIsEmbeddedOnlyFromProfiles` | Prose files are embedded only from `profiles/` and a plugin's profile layer, `plugins/<id>/profile/`, except the allowlisted ones. |
| `TestProviderIdentityThroughAPluginPackageOnlyShrinks` | Code outside a provider's homes tells a pull request from another unit through the GitHub plugin's package only where pluginIdentityAllowlist lists it. |
| `TestProviderIdsInStringsOutsideThePluginsOnlyShrink` | A plugin's id spelled inside a Go string outside its plugin (an SQL literal 'github', a config key "plugins.slack.enabled", a struct tag) appears only in the store's migrations, which hold data already written, and in the files of a counted allowlist that only shrinks. |
| `TestProvidersStayInAdaptersAndTheRoot` | Only a provider and the composition root import a provider's packages, provider names appear only in adapters and the root's allowlist, and only adapters parse an agent's state. |
| `TestReadModelImports` | The read model reaches its inputs only through ports. |
| `TestServeRunsThroughTheHarness` | A test of internal/cli starts serve's jobs only through the harness, so none outlives the test's files. |
| `TestSlackPluginImportsOnlyTheSDK` | No package under `plugins/slack`, tests included, imports a package under `internal/`: the Slack plugin builds against the SDK alone. |
| `TestSpawningPackagesImportMeter` | A package that spawns a process imports the meter. |
| `TestTheCoreNamesNoProvider` | The core spells no provider id, and that outside the core a provider id appears only in its plugin's packages, the vocabulary tables, the unit package and the composition root. |
| `TestTheEngineImportsNoProvider` | The automation engine imports no provider's domain. |
| `TestTheSDKImportsNoInternal` | No package under `pkg/`, tests included, imports a package under `internal/`: plugins built outside this module compile against it. |

## Allowlists

The entries a rule lets pass, each with its reason. An entry that matches nothing fails its rule, so these lists only shrink. Status is the entry's triage against the architecture rules: ✅ the exception holds; 🔧 it does not, and the fix is mechanical; 🧾 it does not, and the fix is a refactor.

### `TestCLIImportsNoGitHubClient`

| `cliGitHubDebt` entry | Status | Reason |
|---|---|---|
| `internal/cli/githubplugin_test.go` | ✅ | the test's forge reads the fake GitHub as the plugin does, standing in for its binary |
| `internal/cli/harness_test.go` | ✅ | the harness reads a pull request the inventory lacks from the fake GitHub as the plugin does |
| `internal/cli/plugin_binary_test.go` | ✅ | the test serves a fake GitHub to the plugin's binary |

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
| `internal/core/proposals/proposals.go` | ✅ | the job that runs the plugins' own rules |
| `internal/plugins/source/broker_gates.go` | ✅ | a source plugin's own rule, which proposes through OpenGate with no session |
| `internal/plugins/source/proposals.go` | ✅ | a source plugin's own rules, which the proposals job reads through Planner.Proposals |

### `TestProseIsEmbeddedOnlyFromProfiles`

| `embedKept` entry | Status | Reason |
|---|---|---|
| `skills` | ✅ | the skills install writes into agent homes |

### `TestProviderIdentityThroughAPluginPackageOnlyShrinks`

| `pluginIdentityAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/store/inventory/byunit.go` `unitRef` `pr.RefOf` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/brief/github_source.go` `GitHubSource.ref` `pr.RefOf` `1` | 🧾 | the brief queue builds GitHub's brief source and its units outside the plugin |
| `internal/brief/queue.go` `Queue.intent` `pr.Codec{}.Provider` `1` | 🧾 | the brief queue builds GitHub's brief intent outside the plugin |
| `internal/brief/queue.go` `Queue.source` `pr.Codec{}.Provider` `1` | 🧾 | the brief queue builds GitHub's brief source and its units outside the plugin |
| `internal/brief/units.go` `refOf` `pr.RefOf` `1` | 🧾 | the brief queue builds GitHub's brief source and its units outside the plugin |
| `internal/cli/launcher.go` `cliLauncher.Launch` `pr.RefOf` `1` | 🧾 | the launcher reads a pull request's workspace outside the plugin |
| `internal/cli/launcher.go` `githubRef` `pr.Codec{}.Provider` `1` | 🧾 | the launcher reads a pull request's workspace outside the plugin |
| `internal/cli/launcher.go` `isPR` `pr.RefOf` `1` | 🧾 | the launcher reads a pull request's workspace outside the plugin |
| `internal/cli/serve_ended.go` `openPRBooks.Books` `pr.RefOf` `1` | 🧾 | the ended turns' books are pull requests |

### `TestProviderIdsInStringsOutsideThePluginsOnlyShrink`

| `providerStringAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/store/inventory/store.go` `github` `2` | 🧾 | the inventory keeps GitHub's pull request tables (issue 62) |
| `internal/adapter/store/inventory/timings.go` `github` `3` | 🧾 | the inventory keeps GitHub's pull request tables (issue 62) |
| `internal/adapter/store/sqlite/retain.go` `github` `23` | 🧾 | retention drops a pull request's rows once GitHub's reports say it ended (issue 62) |
| `internal/adapter/store/sqlite/retain.go` `slack` `5` | 🧾 | retention tells a Slack unit gone by the Slack plugin's reports (issue 62) |
| `internal/adapter/store/sqlite/retain_briefs.go` `github` `2` | 🧾 | retention drops a pull request's rows once GitHub says it ended (issue 62) |
| `internal/adapter/store/sqlite/rounds.go` `github` `7` | 🧾 | the round state keeps GitHub's review rounds (issue 62) |
| `internal/cli/doctor.go` `github` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/config/config.go` `github` `7` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/config/plugins.go` `github` `1` | 🧾 | GitHub's settings are a field of the configuration until its plugin reads its table through the broker (X8a2) |
| `internal/config/plugins.go` `slack` `1` | 🧾 | Slack's settings are a field of the configuration while init and doctor read them there, until they ask the plugin (X9) |
| `internal/intent/builtin/builtin.go` `slack` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/cmd/factsdoc/main.go` `slack` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/cmd/grammardoc/groups.go` `github` `8` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/cmd/grammardoc/groups.go` `slack` `2` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/facts.go` `github` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/grammar_config.go` `github` `25` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/grammar_config.go` `slack` `30` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/renamed.go` `github` `7` | ✅ | the renames name the keys of the configuration a release moved |
| `internal/intent/builtin/renamed.go` `slack` `9` | ✅ | the renames name the keys of the configuration a release moved |

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
| `internal/plugins/host` | ✅ | plugin processes, which live as long as the daemon and meter their own calls through the broker |

### `TestTheCoreNamesNoProvider`

| `coreProviderAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/store/inventory/activity.go` `Store.RecentActivity` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/briefruns.go` `githubBriefs` `"github"` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.Ended` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.FirstRoundBlocks` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.UnitlessEvents` `unit.Session` `1` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/inventory/store.go` `refOf` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/timings.go` `Store.activeRefs` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/sqlite/marks.go` `marksTopic` `unit.GitHub` `1` | 🧾 | a round mark notes GitHub's rounds topic |
| `internal/adapter/store/sqlite/registry.go` `sessionUnit` `unit.Session` `1` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/sqlite/retain.go` `retainWorkflowEventsSQL` `unit.Session` `1` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/sqlite/retain.go` `retainWorkflowEventsSQL` `unit.Workspace` `1` | ✅ | the store keeps the workspaces the core keeps |
| `internal/adapter/store/sqlite/turns.go` `compareUnits` `unit.GitHub` `2` | 🧾 | pull requests sort by owner, repository and number |
| `internal/catalog/catalog.go` `withSource` `unit.GitHub` `1` | 🧾 | the catalog's built-in actions name the GitHub plugin |
| `internal/cli/actions.go` `Env.fetchPR` `unit.GitHub` `1` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/cli/cli.go` `commands` `"slack"` `1` | ✅ | the slack command, the Slack plugin's own commands |
| `internal/cli/help.go` `devCommand` `"slack"` `1` | ✅ | the slack command, the Slack plugin's own commands |
| `internal/cli/placement.go` `Env.placements` `unit.GitHub` `1` | 🧾 | GitHub's default placement, wired outside the plugin's PlacementDefaults |
| `internal/cli/placement.go` `githubPlacement.Default` `unit.GitHub` `2` | 🧾 | GitHub's default placement reads the pull request and the viewer outside the plugin |
| `internal/cli/serve.go` `serveAllDeps` `unit.GitHub` `1` | 🧾 | the daemon's wiring of the GitHub viewer and the forge |
| `internal/cli/serve.go` `trackedBooks.Books` `unit.GitHub` `1` | 🧾 | a carded pull request without a turn still gets an empty book, so its card stays |
| `internal/cli/serve_payloads.go` `Env.payloadReaders` `unit.GitHub` `1` | 🧾 | GitHub's payload reader, wired outside the plugin's Payload |
| `internal/cli/storedfacts.go` `storedFacts.Facts` `unit.GitHub` `1` | 🧾 | the stored facts of a pull request's turn |
| `internal/cli/turn.go` `Env.storedViewer` `unit.GitHub` `1` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/client/tui/views.go` `taskProvider` `unit.Task` `1` | 🧾 | the TUI keys its busy map by the task provider; K2 keys it by the served unit |
| `internal/config/config.go` `Config.ProjectSettingsFor` `unit.GitHub` `1` | 🧾 | an intent's project settings compile against the pull request's turn facts, though the intents live in the GitHub plugin's layer (X10) |
| `internal/config/config.go` `Config.validateWorkflows` `unit.GitHub` `1` | 🧾 | an intent's project settings compile against the pull request's turn facts, though the intents live in the GitHub plugin's layer (X10) |
| `internal/core/intake/intake.go` `SourceSession` `"session"` `1` | ✅ | the intake source a managed session asks from, which shares the session provider's spelling |
| `internal/core/turns/headless.go` `taskInput` `"task"` `1` | ✅ | the headless turn's task input, which shares the task provider's spelling |
| `internal/meter/meter.go` `GitHub` `"github"` `1` | ✅ | the meter's resource of a GitHub API request, not a unit provider |
| `internal/turn/payload.go` `PR.Unit` `unit.GitHub` `1` | 🧾 | the pull request payload a host round hands its turn, while the host sends GitHub's rounds |
| `internal/workspace/orca/orca.go` `Manager.LinkUnit` `unit.GitHub` `1` | ✅ | Orca's linkedPR holds a GitHub pull request only |

This page is generated from the sources of `internal/archtest` by `make docs-ref`.
