---
title: Architecture rules
description: The architecture rules internal/archtest enforces, and the entries each allowlist lets pass.
---

Each rule is a test in `internal/archtest`, with a self-test that plants a violation and expects it caught. [Testing](/architecture/testing/#the-architecture-tests) says how the rules work.

| Test | Rule |
|---|---|
| `TestApprovalRulesReadTrustedFacts` | No approval rule that releases (a draft rule of any mode, a run rule of mode run or delay) loads when it reads an untrusted fact, in each object that holds one: an intent's and a workflow's [drafts.approval], a workflow's [approval], and the config's workflows.<name>.drafts.approval and workflows.<name>.approval. The untrusted facts are the table's, the brief's extra fields, a classification's output and a task property an agent or a plugin writes, so an agent or a thread's text never releases a send or a run. |
| `TestArchitecture` | Only the store imports the SQLite driver; clients import only the API, the domain and `termfmt`; the domain imports only the domain; the brief reaches agents and the store only through its ports; the API imports only the domain; only `intent/expr` imports cel-go; a core interface has at most five methods. |
| `TestBoundaryImports` | The catalog, the intent vocabulary's leaves, the loader and `termfmt` import only their allowed sets. |
| `TestBrandLiteralOnlyInBrand` | The default product name appears only in `internal/brand` and the exempt metadata. |
| `TestBuiltinNamesLiveInTheirTables` | Builtin names appear only in their table files. A released migration keeps the words that version stored, so migrate_v*.go files are history and exempt. |
| `TestClientsNameNoPlugin` | No client names a plugin: the terminal, the menu bar and the other Go clients spell no plugin's id or provider constant, and the cockpit's scripts and page quote no plugin's id. |
| `TestCockpitScriptsNameNoPlugin` | The cockpit's scripts quote no plugin's id. |
| `TestContractSkeletonDeclared` | The shared names other packages build on still exist. |
| `TestCoreImports` | The core imports only the standard library, the core, the domain and the meter, and nothing that reaches processes, the network or the database. |
| `TestCoreImportsNoPlugin` | The core imports no plugin, no plugin imports another, and `pluginDebt` only shrinks. |
| `TestDaemonImports` | The daemon reaches adapters, GitHub and Orca only through ports. |
| `TestDomainImports` | The domain imports no adapter, manager or `internal/github`. |
| `TestDraftTextComesFromTheProfile` | No Go code outside its allowlist writes a draft's text from prose of its own: the Text of an outbox.Proposal or a draft.Draft is never a string constant, a format, or what a function of the same package builds from string literals. A draft's text comes from a profile template or from whoever wrote it. |
| `TestEveryDecisionNamesATestThatExists` | Every entry of the decision log names on its Enforced by line at least one test, and only tests the repository declares. |
| `TestEveryRuleTestStatesItsRule` | Every rule test, a test that does not call flagged, states its rule in a doc comment. |
| `TestGoNamesNoState` | Go reads a task's category and never names a state: no Go literal is converted to, assigned to or compared with a task.State or a task.Category outside internal/intent and the version 7 migration, but the Category constants and Categories of internal/domain/task/schema.go, which declare the closed set; and no SQL text under internal/adapter/store compares the state column with a state the bundled profile declares, nor inlines a category, which it binds as a parameter. |
| `TestLoopCodeImportsNoPR` | Loop code is keyed by `unit.Unit` and imports the pull request domain only in allowlisted files. |
| `TestManifestKeysAreInTheGrammar` | Every toml key the module decodes is a word of builtin.Grammar and that only internal/intent, internal/profile, internal/frontmatter and internal/config decode a profile file's or the configuration's TOML, so no key is read outside the table the loaders check, the schemas and the reference pages derive from. The configuration's keys are checked against the table by internal/config's TestEveryConfigKeyIsInTheGrammar. |
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
| `TestProviderIdentityThroughAPluginPackageOnlyShrinks` | Code outside a provider's homes tells a pull request from another unit through the GitHub plugin's package only where pluginIdentityAllowlist lists it. |
| `TestProviderIdsInStringsOutsideThePluginsOnlyShrink` | A plugin's id spelled inside a Go string outside its plugin (an SQL literal 'github', a config key "slack.enabled", a struct tag) appears only in the store's migrations, which hold data already written, and in the files of a counted allowlist that only shrinks. |
| `TestProvidersStayInAdaptersAndTheRoot` | Only a provider and the composition root import a provider's packages, provider names appear only in adapters and the root's allowlist, and only adapters parse an agent's state. |
| `TestReadModelImports` | The read model reaches its inputs only through ports. |
| `TestServeRunsThroughTheHarness` | A test of internal/cli starts serve's jobs only through the harness, so none outlives the test's files. |
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

### `TestProviderIdentityThroughAPluginPackageOnlyShrinks`

| `pluginIdentityAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/facts/session/facts.go` `repoOf` `port.PROf` `1` | 🧾 | a session's facts read its linked pull request |
| `internal/adapter/store/inventory/byunit.go` `unitRef` `port.PROf` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/brief/github_source.go` `GitHubSource.ref` `pr.RefOf` `1` | 🧾 | the brief queue builds GitHub's brief source and its units outside the plugin |
| `internal/brief/queue.go` `Queue.intent` `pr.Codec{}.Provider` `1` | 🧾 | the brief queue builds GitHub's brief intent outside the plugin |
| `internal/brief/queue.go` `Queue.source` `pr.Codec{}.Provider` `1` | 🧾 | the brief queue builds GitHub's brief source and its units outside the plugin |
| `internal/brief/units.go` `refOf` `pr.RefOf` `1` | 🧾 | the brief queue builds GitHub's brief source and its units outside the plugin |
| `internal/catalog/catalog.go` `withSource` `pr.Codec{}.Provider` `1` | 🧾 | the catalog's built-in actions name the GitHub plugin |
| `internal/cli/launcher.go` `cliLauncher.Launch` `pr.RefOf` `1` | 🧾 | the launcher reads a pull request's workspace outside the plugin |
| `internal/cli/launcher.go` `githubRef` `pr.Codec{}.Provider` `1` | 🧾 | the launcher reads a pull request's workspace outside the plugin |
| `internal/cli/launcher.go` `isPR` `pr.RefOf` `1` | 🧾 | the launcher reads a pull request's workspace outside the plugin |
| `internal/cli/serve_ended.go` `openPRBooks.Books` `pr.RefOf` `1` | 🧾 | the ended turns' books are pull requests |
| `internal/core/port/port.go` `PROf` `pr.RefOf` `1` | 🧾 | the core's pull request event helper, tracked in pluginDebt |
| `internal/core/turns/service.go` `unitArg` `port.PROf` `1` | 🧾 | a turn's URL is its pull request's |
| `internal/daemon/actions.go` `Server.readAheadQueued` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/actions.go` `Server.runAction` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/api.go` `Server.handleRemoveDone` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/api.go` `Server.parsePR` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/attention_tasks.go` `taskViewer.rowNotice` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/briefs.go` `refsOf` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/headless.go` `Server.runHeadless` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/plan.go` `Server.actOnPlanItem` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/refs.go` `isPullRequest` `port.PROf` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/refs.go` `refOf` `port.PROf` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/rows.go` `Server.detail` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/unitactions.go` `sourceFollowed` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |
| `internal/daemon/unitactions.go` `unitAction` `isPullRequest` `1` | 🧾 | the daemon gates the GitHub-only facilities on a pull request, see prAllowed |

### `TestProviderIdsInStringsOutsideThePluginsOnlyShrink`

| `providerStringAllowlist` entry | Status | Reason |
|---|---|---|
| `internal/adapter/store/inventory/store.go` `github` `8` | 🧾 | the inventory keeps GitHub's pull request tables (issue 62) |
| `internal/adapter/store/inventory/timings.go` `github` `3` | 🧾 | the inventory keeps GitHub's pull request tables (issue 62) |
| `internal/adapter/store/sqlite/retain.go` `github` `21` | 🧾 | retention drops a pull request's rows once GitHub says it ended (issue 62) |
| `internal/adapter/store/sqlite/retain.go` `slack` `4` | 🧾 | retention tells a Slack unit gone by the Slack tables; the plugin should say (issue 62) |
| `internal/adapter/store/sqlite/retain_briefs.go` `github` `3` | 🧾 | retention drops a pull request's rows once GitHub says it ended (issue 62) |
| `internal/adapter/store/sqlite/rounds.go` `github` `7` | 🧾 | the round state keeps GitHub's review rounds (issue 62) |
| `internal/cli/doctor.go` `github` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/config/config.go` `github` `8` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/config/config.go` `slack` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/builtin.go` `slack` `2` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/cmd/factsdoc/main.go` `slack` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/cmd/grammardoc/groups.go` `github` `9` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/cmd/grammardoc/groups.go` `slack` `3` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/facts.go` `github` `1` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/facts.go` `slack` `13` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/grammar_config.go` `github` `26` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |
| `internal/intent/builtin/grammar_config.go` `slack` `31` | 🧾 | the central fact and config tables name the plugins' facts and config keys, which the plugins should declare (issue 62) |

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
| `internal/adapter/facts/session/facts.go` `Provider.build` `unit.GitHub` `1` | 🧾 | a session's facts read its linked pull request's state |
| `internal/adapter/orca/orca.go` `Adapter.Apply` `unit.GitHub` `1` | ✅ | Orca links a worktree to a GitHub pull request only |
| `internal/adapter/store/inventory/activity.go` `Store.RecentActivity` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/briefruns.go` `githubBriefs` `"github"` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.Ended` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.FirstRoundBlocks` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.Reviewed` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.StoredFacts` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.Tracked` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/store.go` `Store.UnitlessEvents` `unit.Session` `1` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/inventory/store.go` `refOf` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/inventory/timings.go` `Store.activeRefs` `unit.GitHub` `1` | 🧾 | the inventory keeps GitHub's pull request tables |
| `internal/adapter/store/sqlite/marks.go` `marksTopic` `unit.GitHub` `1` | 🧾 | a round mark notes GitHub's rounds topic |
| `internal/adapter/store/sqlite/registry.go` `sessionUnit` `unit.Session` `1` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/sqlite/retain.go` `retainWorkflowEventsSQL` `unit.Session` `1` | ✅ | the store keeps the sessions the core keeps |
| `internal/adapter/store/sqlite/retain.go` `retainWorkflowEventsSQL` `unit.Workspace` `1` | ✅ | the store keeps the workspaces the core keeps |
| `internal/adapter/store/sqlite/slack.go` `slackProvider` `"slack"` `1` | ✅ | the store keeps the Slack plugin's tables |
| `internal/adapter/store/sqlite/turns.go` `compareUnits` `unit.GitHub` `2` | 🧾 | pull requests sort by owner, repository and number |
| `internal/adapter/store/sqlite/turns.go` `prOf` `unit.GitHub` `1` | 🧾 | a pull request's turns are keyed by owner, repository and number |
| `internal/cli/actions.go` `Env.fetchPR` `unit.GitHub` `1` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/cli/cli.go` `commands` `"slack"` `1` | ✅ | the slack command, the Slack plugin's own commands |
| `internal/cli/facts.go` `inventoryFacts.MyReviews` `unit.GitHub` `1` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/cli/help.go` `devCommand` `"slack"` `1` | ✅ | the slack command, the Slack plugin's own commands |
| `internal/cli/init_config.go` `mergeInitConfig` `"slack"` `1` | ✅ | the slack section of the config init writes |
| `internal/cli/placement.go` `Env.placements` `unit.GitHub` `1` | 🧾 | GitHub's default placement, wired outside the plugin's PlacementDefaults |
| `internal/cli/placement.go` `githubPlacement.Default` `unit.GitHub` `2` | 🧾 | GitHub's default placement reads the pull request and the viewer outside the plugin |
| `internal/cli/serve.go` `serveAllDeps` `unit.GitHub` `3` | 🧾 | the daemon's wiring of the GitHub viewer and the forge |
| `internal/cli/serve.go` `trackedBooks.Books` `unit.GitHub` `1` | 🧾 | a carded pull request without a turn still gets an empty book, so its card stays |
| `internal/cli/serve_payloads.go` `Env.payloadReaders` `unit.GitHub` `1` | 🧾 | GitHub's payload reader, wired outside the plugin's Payload |
| `internal/cli/serve_plugins.go` `withoutGitHub` `unit.GitHub` `1` | 🧾 | the GitHub facilities the daemon holds go with the GitHub plugin |
| `internal/cli/storedfacts.go` `storedFacts.Facts` `unit.GitHub` `1` | 🧾 | the stored facts of a pull request's turn |
| `internal/cli/sweepinputs.go` `storeInputs.Viewer` `unit.GitHub` `1` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/cli/turn.go` `Env.storedViewer` `unit.GitHub` `1` | 🧾 | the GitHub viewer read outside the plugin |
| `internal/client/tui/views.go` `taskProvider` `unit.Task` `1` | 🧾 | the TUI keys its busy map by the task provider; K2 keys it by the served unit |
| `internal/core/turns/headless.go` `taskInput` `"task"` `1` | ✅ | the headless turn's task input, which shares the task provider's spelling |
| `internal/meter/meter.go` `GitHub` `"github"` `1` | ✅ | the meter's resource of a GitHub API request, not a unit provider |
| `internal/workspace/orca/orca.go` `Manager.LinkUnit` `unit.GitHub` `1` | ✅ | Orca's linkedPR holds a GitHub pull request only |

This page is generated from the sources of `internal/archtest` by `make docs-ref`.
